// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package gitimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Key fixtures are deliberately not real keys: the import only checks the
// armor, and the tests assert the material never leaks.
const (
	armor         = "PRIVATE" + " KEY-----"
	plainKey      = "-----BEGIN RSA " + armor + "\nplain-key-material-canary\n-----END RSA " + armor + "\n"
	encryptedKey  = "-----BEGIN RSA " + armor + "\nProc-Type: 4,ENCRYPTED\nencrypted-key-material-canary\n-----END RSA " + armor + "\n"
	passphrase    = "passphrase-canary"
	publicKeyLine = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureFixtureFixtureFixtureFixtureFixtureFi operator@host"
)

// fakeGit is a global git configuration; absent keys are ErrMissing.
type fakeGit map[string]string

func (f fakeGit) Lookup(_ context.Context, key string, _ bool) (string, error) {
	if value, ok := f[key]; ok {
		return value, nil
	}
	return "", ErrMissing
}

// unavailableGit is a host without git.
type unavailableGit struct{}

func (unavailableGit) Lookup(context.Context, string, bool) (string, error) {
	return "", ErrUnavailable
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertNoKeyMaterial(t *testing.T, text string) {
	t.Helper()
	for _, canary := range []string{"key-material-canary", passphrase, "PRIVATE KEY"} {
		if strings.Contains(text, canary) {
			t.Fatalf("key material %q leaked: %s", canary, text)
		}
	}
}

// operator is a git configuration with an author.
func operator() fakeGit {
	return fakeGit{"user.name": "Dev Operator", "user.email": "dev@example.com"}
}

func TestImportKeyDiscoveryOrder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		present  []string // files under home that exist
		signing  string   // user.signingkey, relative to home ("" = unset)
		explicit string   // --key, relative to home ("" = unset)
		want     string   // relative path of the imported key
	}{
		{"explicit-wins", []string{"keys/deploy", "keys/signing", ".ssh/id_ed25519"}, "keys/signing.pub", "keys/deploy", "keys/deploy"},
		{"signing-key-public-half", []string{"keys/signing", ".ssh/id_ed25519"}, "keys/signing.pub", "", "keys/signing"},
		{"signing-key-itself", []string{"keys/signing", ".ssh/id_rsa"}, "keys/signing", "", "keys/signing"},
		{"signing-key-missing-falls-back", []string{".ssh/id_ecdsa"}, "keys/absent.pub", "", ".ssh/id_ecdsa"},
		{"ed25519-first", []string{".ssh/id_rsa", ".ssh/id_ecdsa", ".ssh/id_ed25519"}, "", "", ".ssh/id_ed25519"},
		{"ecdsa-before-rsa", []string{".ssh/id_rsa", ".ssh/id_ecdsa"}, "", "", ".ssh/id_ecdsa"},
		{"rsa-last", []string{".ssh/id_rsa"}, "", "", ".ssh/id_rsa"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			for _, file := range tc.present {
				writeFile(t, filepath.Join(home, file), strings.Replace(plainKey, "plain-key", file, 1))
			}
			git := fakeGit{}
			if tc.signing != "" {
				git["user.signingkey"] = filepath.Join(home, tc.signing)
			}
			opts := Options{Home: home, Git: git}
			if tc.explicit != "" {
				opts.Key = filepath.Join(home, tc.explicit)
			}
			result, err := Import(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Secrets) != 1 ||
				!strings.Contains(result.Secrets[0].Spec.SSHKey.PrivateKey, tc.want+"-material") ||
				result.KeyPath != filepath.Join(home, tc.want) {
				t.Fatalf("imported %s, want %s", result.KeyPath, tc.want)
			}
		})
	}
	// `~/` in an explicit key is the given home, not the process's.
	home := t.TempDir()
	writeFile(t, filepath.Join(home, "keys/deploy"), plainKey)
	if _, err := Import(t.Context(), Options{Home: home, Git: fakeGit{}, Key: "~/keys/deploy"}); err != nil {
		t.Fatal(err)
	}
}

// TestImportKeyIdentity is AC7: one ssh-key Secret tagged git with the author,
// committer, hosts and signing, plus the allowed-signers Secret.
func TestImportKeyIdentity(t *testing.T) {
	home := t.TempDir()
	key := writeFile(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	writeFile(t, key+".pub", publicKeyLine+"\n")
	writeFile(t, key+"-cert.pub", "ssh-ed25519-cert-v01@openssh.com AAAACertificate operator\n")
	signers := writeFile(t, filepath.Join(home, ".ssh/allowed_signers"), "dev@example.com "+publicKeyLine+"\n")
	hosts := writeFile(t, filepath.Join(home, "known_hosts"), "git.example.com ssh-ed25519 AAAAPinned\n")
	git := operator()
	git["user.signingkey"] = key + ".pub"
	git["commit.gpgsign"], git["tag.gpgsign"] = "true", "true"
	git["gpg.ssh.allowedSignersFile"] = "~/.ssh/allowed_signers"

	result, err := Import(t.Context(), Options{Home: home, Git: git, KnownHostsFile: hosts})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Secrets) != 2 {
		t.Fatalf("secrets = %d, want the key and the allowed-signers file", len(result.Secrets))
	}
	identity := map[string]string{
		"hosts":             "github.com",
		"author":            "Dev Operator <dev@example.com>",
		"committer":         "Dev Operator <dev@example.com>",
		"sign":              "commits,tags",
		"allowedSignersRef": "git-allowed-signers",
	}
	wantKey := v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{Name: "git-ssh", Realm: "default"},
		Spec: v1beta1.SecretSpec{
			Type: v1beta1.SecretTypeSSHKey, Connector: "git", Identity: identity,
			SSHKey: &v1beta1.SecretSSHKey{
				PrivateKey:  plainKey,
				PublicKey:   publicKeyLine,
				Certificate: "ssh-ed25519-cert-v01@openssh.com AAAACertificate operator",
				KnownHosts:  "git.example.com ssh-ed25519 AAAAPinned\n",
			},
		},
	}
	// The allowed-signers file is stored before the identity naming it.
	if !reflect.DeepEqual(result.Secrets[1], wantKey) {
		t.Fatalf("key secret = %+v", result.Secrets[1])
	}
	signersDoc := result.Secrets[0]
	wantSigners := v1beta1.SecretMetadata{Name: "git-allowed-signers", Realm: "default"}
	if signersDoc.Metadata != wantSigners || signersDoc.Spec.Type != "" || signersDoc.Spec.SSHKey != nil ||
		signersDoc.Spec.Connector != "" || signersDoc.Spec.Data != "dev@example.com "+publicKeyLine+"\n" {
		t.Fatalf("allowed-signers secret = %+v", signersDoc)
	}
	if result.Name != "git-ssh" || result.Realm != "default" || result.Type != v1beta1.SecretTypeSSHKey ||
		result.Home != home || result.KeyPath != key || result.AllowedSignersPath != signers ||
		!reflect.DeepEqual(result.Identity, identity) || len(result.Notes) != 0 {
		t.Fatalf("summary = %+v", result)
	}
	// The summary — what --dry-run and the web UI show — carries no material.
	summary, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	assertNoKeyMaterial(t, string(summary))
	if strings.Contains(string(summary), "AAAAPinned") {
		t.Fatalf("summary carries secret contents: %s", summary)
	}

	// --hosts, --name and --realm.
	result, err = Import(t.Context(), Options{
		Home: home, Git: git, Hosts: []string{"github.com, gitlab.com/group", "github.com"},
		Name: "work-ssh", Realm: "work",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity["hosts"] != "github.com,gitlab.com/group" ||
		result.Secrets[1].Metadata != (v1beta1.SecretMetadata{Name: "work-ssh", Realm: "work"}) ||
		result.Secrets[0].Metadata.Realm != "work" {
		t.Fatalf("result = %+v", result)
	}
}

// Signing is imported only for an operator who signs with the imported key.
func TestImportSigningFollowsTheOperator(t *testing.T) {
	home := t.TempDir()
	key := writeFile(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	writeFile(t, key+".pub", publicKeyLine+"\n")
	other := writeFile(t, filepath.Join(home, ".ssh/signing.pub"), "ssh-ed25519 AAAAOtherKeyBlob signer@host\n")
	copyOfKey := writeFile(t, filepath.Join(home, "keys/work.pub"), publicKeyLine+"\n")
	for _, tc := range []struct {
		name       string
		signingKey string
		gpgsign    bool
		signs      bool
		noted      bool
	}{
		{"no-signing-key", "", false, false, false},
		{"key-without-gpgsign", key + ".pub", false, false, false},
		{"gpg-key-id", "3AA5C34371567BD2", true, false, true},
		{"other-ssh-key-path", other, true, false, true},
		{"other-ssh-key-literal", "key::ssh-ed25519 AAAAOtherKeyBlob", true, false, true},
		{"relative-path", ".ssh/id_ed25519.pub", true, false, true},
		{"imported-key-pub-path", key + ".pub", true, true, false},
		{"imported-key-private-path", "~/.ssh/id_ed25519", true, true, false},
		{"imported-key-literal", "key::" + publicKeyLine, true, true, false},
		{"imported-key-under-another-path", copyOfKey, true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			git := fakeGit{}
			if tc.signingKey != "" {
				git["user.signingkey"] = tc.signingKey
			}
			if tc.gpgsign {
				git["commit.gpgsign"] = "true"
			}
			result, err := Import(t.Context(), Options{Home: home, Git: git, Key: key})
			if err != nil || len(result.Secrets) != 1 {
				t.Fatalf("secrets=%d err=%v", len(result.Secrets), err)
			}
			noted := slices.ContainsFunc(result.Notes, func(n string) bool {
				return strings.Contains(n, "git signing was not imported")
			})
			sign := result.Secrets[0].Spec.Identity["sign"]
			if (sign == "commits") != tc.signs || noted != tc.noted {
				t.Fatalf("sign=%q notes=%v, want signs=%v noted=%v", sign, result.Notes, tc.signs, tc.noted)
			}
			if result.Secrets[0].Spec.Identity["hosts"] != "github.com" {
				t.Fatalf("identity = %+v, want the key still bound to github.com", result.Secrets[0].Spec.Identity)
			}
		})
	}
	// gpgsign without user.signingkey is noted by discovery.
	result, err := Import(t.Context(), Options{Home: home, Git: fakeGit{"tag.gpgsign": "true"}, Key: key})
	if err != nil || result.Identity["sign"] != "" || !slices.ContainsFunc(result.Notes, func(n string) bool {
		return strings.Contains(n, "tag.gpgsign is enabled without user.signingkey")
	}) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestImportPassphrase(t *testing.T) {
	home := t.TempDir()
	key := writeFile(t, filepath.Join(home, ".ssh/id_ed25519"), encryptedKey)
	file := writeFile(t, filepath.Join(home, "passphrase"), passphrase+"\r\n")
	// Refused without the passphrase: the cell could never unlock the key.
	_, err := Import(t.Context(), Options{Home: home, Git: fakeGit{}})
	if err == nil || !strings.Contains(err.Error(), "passphrase-protected") ||
		!strings.Contains(err.Error(), "--passphrase-file") || !strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v", err)
	}
	assertNoKeyMaterial(t, err.Error())
	result, err := Import(t.Context(), Options{Home: home, Git: fakeGit{}, PassphraseFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if got := result.Secrets[0].Spec.SSHKey; got.PrivateKey != encryptedKey || got.Passphrase != passphrase {
		t.Fatalf("passphrase not stored with the key (trailing newline trimmed): %q", got.Passphrase)
	}
	// A passphrase supplied for a plain key is kept; it is harmless.
	writeFile(t, key, plainKey)
	if _, err = Import(t.Context(), Options{Home: home, Git: fakeGit{}, PassphraseFile: file}); err != nil {
		t.Fatal(err)
	}
}

// TestImportAuthorOnly is AC8: with no SSH key the identity is author-only,
// and with neither a key nor an author the import fails naming both.
func TestImportAuthorOnly(t *testing.T) {
	home := t.TempDir()
	git := operator()
	git["commit.gpgsign"], git["user.signingkey"] = "true", "~/.ssh/id_ed25519.pub"
	result, err := Import(t.Context(), Options{Home: home, Git: git, Hosts: []string{"gitlab.com"}})
	if err != nil {
		t.Fatal(err)
	}
	want := v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{Name: "git-author", Realm: "default"},
		Spec: v1beta1.SecretSpec{
			Type: v1beta1.SecretTypeOpaque, Connector: "git", Data: "kukeon:git-author-only",
			Identity: map[string]string{
				"author": "Dev Operator <dev@example.com>", "committer": "Dev Operator <dev@example.com>",
			},
		},
	}
	if len(result.Secrets) != 1 || !reflect.DeepEqual(result.Secrets[0], want) {
		t.Fatalf("secrets = %+v", result.Secrets)
	}
	if result.Type != v1beta1.SecretTypeOpaque || result.KeyPath != "" || result.Name != "git-author" {
		t.Fatalf("summary = %+v", result)
	}
	for _, mention := range []string{"author-only", "git signing was not imported", "--hosts was ignored"} {
		if !slices.ContainsFunc(result.Notes, func(n string) bool { return strings.Contains(n, mention) }) {
			t.Errorf("notes %v do not mention %q", result.Notes, mention)
		}
	}
	if result, err = Import(t.Context(), Options{Home: home, Git: operator(), Name: "me"}); err != nil ||
		result.Secrets[0].Metadata.Name != "me" {
		t.Fatalf("named author-only: %+v %v", result, err)
	}

	for name, reader := range map[string]GitReader{
		"no author":        fakeGit{},
		"incomplete":       fakeGit{"user.name": "Dev"},
		"git unavailable":  unavailableGit{},
		"malformed author": fakeGit{"user.name": "Dev", "user.email": "nope"},
	} {
		t.Run(name, func(t *testing.T) {
			_, importErr := Import(t.Context(), Options{Home: home, Git: reader})
			if !errors.Is(importErr, errdefs.ErrGitImport) ||
				!strings.Contains(importErr.Error(), "no SSH private key") ||
				!strings.Contains(importErr.Error(), "no git author") {
				t.Fatalf("err = %v, want it to name the missing key and author", importErr)
			}
		})
	}
}

func TestImportFailuresNeverEchoKeyMaterial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home string, git fakeGit, opts *Options)
		want  string
	}{
		{"explicit-key-missing", func(_ *testing.T, home string, _ fakeGit, opts *Options) {
			opts.Key = filepath.Join(home, "absent")
		}, "cannot read private key"},
		{"public-key-given", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			opts.Key = writeFile(t, filepath.Join(home, "id.pub"), publicKeyLine+" key-material-canary\n")
		}, "is not a private key"},
		{"empty-key", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			opts.Key = writeFile(t, filepath.Join(home, "empty"), "\n")
		}, "is empty"},
		{"unreadable-key", func(t *testing.T, home string, _ fakeGit, _ *Options) {
			if err := os.MkdirAll(filepath.Join(home, ".ssh/id_rsa"), 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(home, ".ssh/id_ecdsa"), plainKey)
			if err := os.Chmod(filepath.Join(home, ".ssh/id_ecdsa"), 0); err != nil {
				t.Fatal(err)
			}
		}, "cannot read private key"},
		{"passphrase-file-missing", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			writeFile(t, filepath.Join(home, ".ssh/id_rsa"), encryptedKey)
			opts.PassphraseFile = filepath.Join(home, "absent")
		}, "cannot read passphrase"},
		{"known-hosts-missing", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			writeFile(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			opts.KnownHostsFile = filepath.Join(home, "absent")
		}, "cannot read known_hosts"},
		{"allowed-signers-missing", func(t *testing.T, home string, git fakeGit, _ *Options) {
			writeFile(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			git["gpg.ssh.allowedSignersFile"] = filepath.Join(home, "absent")
		}, "cannot read allowed signers file"},
		{"bad-host", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			writeFile(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			opts.Hosts = []string{"ssh://github.com"}
		}, "use a bare host"},
		{"bad-name", func(t *testing.T, home string, _ fakeGit, opts *Options) {
			writeFile(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			opts.Name = "../escape"
		}, "not a valid secret name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			git := operator()
			opts := Options{Home: home, Git: git}
			tc.setup(t, home, git, &opts)
			if tc.name == "unreadable-key" && os.Geteuid() == 0 {
				t.Skip("root reads a mode-0 file")
			}
			result, err := Import(t.Context(), opts)
			if err == nil || result.Secrets != nil || !strings.Contains(err.Error(), tc.want) ||
				!errors.Is(err, errdefs.ErrGitImport) || !strings.HasPrefix(err.Error(), "import git identity: ") {
				t.Fatalf("secrets=%v err=%v, want %q", result.Secrets, err, tc.want)
			}
			assertNoKeyMaterial(t, err.Error())
		})
	}
	if _, err := Import(t.Context(), Options{Git: fakeGit{}}); !errors.Is(err, errdefs.ErrGitImport) {
		t.Fatalf("no home: err = %v", err)
	}
}

func isolateGit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for subprocess integration tests:", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "global.gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	return filepath.Join(home, "global.gitconfig")
}

func TestCommandGitBooleanConversion(t *testing.T) {
	global := isolateGit(t)
	for _, tc := range []struct{ spelling, want string }{
		{"yes", "true"},
		{"on", "true"},
		{"1", "true"},
		{"TRUE", "true"},
		{"-2", "true"},
		{"no", "false"},
		{"off", "false"},
		{"0", "false"},
		{"false", "false"},
		{"", "false"},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			writeFile(t, global, "[commit]\n gpgsign = "+tc.spelling+"\n")
			value, err := (CommandGit{}).Lookup(t.Context(), "commit.gpgsign", true)
			if err != nil || value != tc.want {
				t.Fatalf("value=%q error=%v, want %s", value, err, tc.want)
			}
		})
	}
	writeFile(t, global, "[commit]\n gpgsign\n")
	value, err := (CommandGit{}).Lookup(t.Context(), "commit.gpgsign", true)
	if err != nil || value != "true" {
		t.Fatalf("implicit boolean: value=%q error=%v", value, err)
	}
}

func TestCommandGitErrorClassification(t *testing.T) {
	global := isolateGit(t)
	reader := CommandGit{}
	if _, err := reader.Lookup(t.Context(), "user.name", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("absent file: %v", err)
	}
	writeFile(t, global, "[user]\n name = Operator\n")
	if _, err := reader.Lookup(t.Context(), "user.email", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("absent key: %v", err)
	}
	for _, content := range []string{"[invalid secret-content", "[commit]\n gpgsign = secret-content\n"} {
		writeFile(t, global, content)
		_, err := reader.Lookup(t.Context(), "commit.gpgsign", true)
		if err == nil || errors.Is(err, ErrMissing) || errors.Is(err, ErrUnavailable) ||
			strings.Contains(err.Error(), "secret-content") {
			t.Fatalf("error = %v", err)
		}
		_, err = Import(t.Context(), Options{Home: t.TempDir(), Git: reader})
		if err == nil || !strings.Contains(err.Error(), "Git key") || strings.Contains(err.Error(), "secret-content") {
			t.Fatalf("import error = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.Lookup(ctx, "user.name", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := reader.Lookup(t.Context(), "user.name", false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
}

// TestCommandGitUsesGlobalOnly: the import reads the global configuration,
// never the repository the process runs in.
func TestCommandGitUsesGlobalOnly(t *testing.T) {
	global := isolateGit(t)
	home := t.TempDir()
	key := writeFile(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	writeFile(t, key+".pub", publicKeyLine+"\n")
	writeFile(t, global,
		"[user]\n name = Global\n email = global@example.test\n[commit]\n gpgsign = on\n[tag]\n gpgsign = yes\n"+
			"[user]\n signingkey = "+key+".pub\n")
	repo := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", "init", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	writeFile(t, filepath.Join(repo, ".git", "config"), "[user]\n name = Local\n email = local@example.test\n")
	t.Chdir(repo)
	result, err := Import(t.Context(), Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if result.Identity["author"] != "Global <global@example.test>" || result.Identity["sign"] != "commits,tags" {
		t.Fatalf("global settings not used: %+v", result.Identity)
	}
}
