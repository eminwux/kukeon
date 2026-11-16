// Copyright 2026 Emiliano Spinella (eminwux)
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

package initialize

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// Key fixtures are deliberately not real keys: importGitIdentity only checks
// the armor, and the tests assert the material never leaks.
const (
	armor         = "PRIVATE" + " KEY-----"
	plainKey      = "-----BEGIN RSA " + armor + "\nplain-key-material-canary\n-----END RSA " + armor + "\n"
	encryptedKey  = "-----BEGIN RSA " + armor + "\nProc-Type: 4,ENCRYPTED\nencrypted-key-material-canary\n-----END RSA " + armor + "\n"
	passphrase    = "passphrase-canary"
	publicKeyLine = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixtureFixtureFixtureFixtureFixtureFixtureFi operator@host"
)

func writeKey(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

//nolint:gochecknoglobals // read-only test fixture.
var operatorSources = []config.Source{
	{Name: "kukeon", URL: "github.com/eminwux/kukeon", Branch: "main", ClonePath: "/home/user/kukeon-src"},
	{Name: "agents", URL: "git@GitHub.com:eminwux/agents", Branch: "main", ClonePath: "/home/user/agents"},
	{Name: "internal", URL: "ssh://git@git.example.com:2222/team/defs.git", Tag: "v1"},
}

func assertNoKeyMaterial(t *testing.T, text string) {
	t.Helper()
	for _, canary := range []string{"key-material-canary", passphrase, "PRIVATE KEY"} {
		if strings.Contains(text, canary) {
			t.Fatalf("key material %q leaked: %s", canary, text)
		}
	}
}

func TestImportGitIdentityKeyDiscoveryOrder(t *testing.T) {
	for _, tc := range []struct {
		name     string
		present  []string // files under home that exist
		signing  string   // spec.git.signingKey, relative to home ("" = unset)
		explicit string   // --git-key, relative to home ("" = unset)
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
				writeKey(t, filepath.Join(home, file), strings.Replace(plainKey, "plain-key", file, 1))
			}
			cfg := &config.TeamsConfig{}
			if tc.signing != "" {
				cfg.Spec.Git = &config.Git{SigningKey: filepath.Join(home, tc.signing)}
			}
			imp := GitImport{Enabled: true}
			if tc.explicit != "" {
				imp.Key = filepath.Join(home, tc.explicit)
			}
			docs, err := importGitIdentity(cfg, operatorSources, home, imp)
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 1 || !strings.Contains(docs[0].Spec.SSHKey.PrivateKey, tc.want+"-material") {
				t.Fatalf("imported the wrong key, want %s", tc.want)
			}
		})
	}
	// `~/` in an explicit key is the operator's home, not the process's.
	home := t.TempDir()
	writeKey(t, filepath.Join(home, "keys/deploy"), plainKey)
	if _, err := importGitIdentity(&config.TeamsConfig{}, nil, home, GitImport{Enabled: true, Key: "~/keys/deploy"}); err != nil {
		t.Fatal(err)
	}
}

func TestImportGitIdentityBuildsSecretsAndRewritesConfig(t *testing.T) {
	home := t.TempDir()
	key := writeKey(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	writeKey(t, key+".pub", publicKeyLine+"\n")
	writeKey(t, key+"-cert.pub", "ssh-ed25519-cert-v01@openssh.com AAAACertificate operator\n")
	writeKey(t, filepath.Join(home, ".ssh/allowed_signers"), "dev@example.com "+publicKeyLine+"\n")
	hosts := writeKey(t, filepath.Join(home, "known_hosts"), "git.example.com ssh-ed25519 AAAAPinned\n")
	existing := config.SecretRef{Name: "work-key", Realm: "default"}
	cfg := &config.TeamsConfig{Spec: config.Spec{Git: &config.Git{
		Author:         &config.Identity{Name: "Dev", Email: "dev@example.com"},
		SigningKey:     key + ".pub",
		Sign:           []string{"commits", "tags"},
		AllowedSigners: "~/.ssh/allowed_signers",
		// A host the operator already bound keeps its credential.
		Credentials: []config.GitCredential{{Host: "git.example.com", SecretRef: existing, Port: 2222}},
	}}}
	docs, err := importGitIdentity(cfg, operatorSources, home, GitImport{Enabled: true, KnownHostsFile: hosts})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %d, want the key and the allowed-signers file", len(docs))
	}
	keyDoc, signersDoc := docs[0], docs[1]
	wantKey := api.SecretDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
		Metadata: api.SecretMetadata{Name: "git-ssh", Realm: "default"},
		Spec: api.SecretSpec{Type: api.SecretTypeSSHKey, SSHKey: &api.SecretSSHKey{
			PrivateKey:  plainKey,
			PublicKey:   publicKeyLine,
			Certificate: "ssh-ed25519-cert-v01@openssh.com AAAACertificate operator",
			KnownHosts:  "git.example.com ssh-ed25519 AAAAPinned\n",
		}},
	}
	if !reflect.DeepEqual(keyDoc, wantKey) {
		t.Fatalf("key secret = %+v", keyDoc)
	}
	if signersDoc.Metadata != (api.SecretMetadata{Name: "git-allowed-signers", Realm: "default"}) ||
		signersDoc.Spec.Type != "" || signersDoc.Spec.SSHKey != nil ||
		signersDoc.Spec.Data != "dev@example.com "+publicKeyLine+"\n" {
		t.Fatalf("allowed-signers secret = %+v", signersDoc)
	}
	keyRef := config.SecretRef{Name: "git-ssh", Realm: "default"}
	signersRef := config.SecretRef{Name: "git-allowed-signers", Realm: "default"}
	want := &config.Git{
		Author: &config.Identity{Name: "Dev", Email: "dev@example.com"},
		Credentials: []config.GitCredential{
			{Host: "git.example.com", SecretRef: existing, Port: 2222},
			{Host: "github.com", SecretRef: keyRef},
		},
		Signing: &config.GitSigning{SecretRef: keyRef, AllowedSignersRef: &signersRef},
		Sign:    []string{"commits", "tags"},
	}
	if !reflect.DeepEqual(cfg.Spec.Git, want) {
		t.Fatalf("configuration git = %+v, want %+v (host paths cleared, hosts bound once)", cfg.Spec.Git, want)
	}
	// The rewritten configuration is valid and carries no key material.
	cfg.APIVersion, cfg.Kind = config.APIVersion, config.Kind
	var encoded bytes.Buffer
	if err = config.Encode(&encoded, cfg); err != nil {
		t.Fatal(err)
	}
	assertNoKeyMaterial(t, encoded.String())
	if strings.Contains(encoded.String(), home) || strings.Contains(encoded.String(), "signingKey") {
		t.Fatalf("host path left in the configuration: %s", encoded.String())
	}
}

// Signing is imported only for an operator who already signs; otherwise the
// key is a credential and nothing more.
func TestImportGitIdentitySigningFollowsTheOperator(t *testing.T) {
	home := t.TempDir()
	writeKey(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	stored := config.SecretRef{Name: "signing-key", Realm: "default"}
	for _, tc := range []struct {
		name string
		git  *config.Git
		want *config.GitSigning
	}{
		{"no-git-block", nil, nil},
		{"identity-only", &config.Git{Author: &config.Identity{Name: "Dev", Email: "dev@example.com"}}, nil},
		{"key-without-sign", &config.Git{SigningKey: filepath.Join(home, ".ssh/id_ed25519.pub")}, nil},
		{
			"signs", &config.Git{SigningKey: filepath.Join(home, ".ssh/id_ed25519.pub"), Sign: []string{"commits"}},
			&config.GitSigning{SecretRef: config.SecretRef{Name: "git-ssh", Realm: "default"}},
		},
		{
			"already-stored-signing", &config.Git{Signing: &config.GitSigning{SecretRef: stored}, Sign: []string{"tags"}},
			&config.GitSigning{SecretRef: stored},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.TeamsConfig{Spec: config.Spec{Git: tc.git}}
			docs, err := importGitIdentity(cfg, nil, home, GitImport{Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if len(docs) != 1 || !reflect.DeepEqual(cfg.Spec.Git.Signing, tc.want) ||
				cfg.Spec.Git.SigningKey != "" || cfg.Spec.Git.AllowedSigners != "" {
				t.Fatalf("docs=%d git=%+v", len(docs), cfg.Spec.Git)
			}
			// With no sources to read a host from, the key is bound to github.com.
			if !slices.ContainsFunc(cfg.Spec.Git.Credentials, func(c config.GitCredential) bool {
				return c.Host == "github.com" && c.SecretRef.Name == "git-ssh"
			}) {
				t.Fatalf("credentials = %+v", cfg.Spec.Git.Credentials)
			}
		})
	}
}

func TestImportGitIdentityPassphrase(t *testing.T) {
	home := t.TempDir()
	key := writeKey(t, filepath.Join(home, ".ssh/id_ed25519"), encryptedKey)
	file := writeKey(t, filepath.Join(home, "passphrase"), passphrase+"\r\n")
	// Refused without the passphrase: the cell could never unlock the key.
	_, err := importGitIdentity(&config.TeamsConfig{}, nil, home, GitImport{Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "passphrase-protected") ||
		!strings.Contains(err.Error(), "--git-key-passphrase-file") || !strings.Contains(err.Error(), key) {
		t.Fatalf("err = %v", err)
	}
	assertNoKeyMaterial(t, err.Error())
	docs, err := importGitIdentity(&config.TeamsConfig{}, nil, home, GitImport{Enabled: true, PassphraseFile: file})
	if err != nil {
		t.Fatal(err)
	}
	if got := docs[0].Spec.SSHKey; got.PrivateKey != encryptedKey || got.Passphrase != passphrase {
		t.Fatalf("passphrase not stored with the key (trailing newline trimmed): %q", got.Passphrase)
	}
	// A passphrase supplied for a plain key is kept; it is harmless.
	writeKey(t, key, plainKey)
	if _, err = importGitIdentity(&config.TeamsConfig{}, nil, home, GitImport{Enabled: true, PassphraseFile: file}); err != nil {
		t.Fatal(err)
	}
}

func TestImportGitIdentityFailuresNeverEchoKeyMaterial(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, home string, cfg *config.TeamsConfig, imp *GitImport)
		want  string
	}{
		{"no-key", func(*testing.T, string, *config.TeamsConfig, *GitImport) {}, "no SSH private key found"},
		{"explicit-key-missing", func(_ *testing.T, home string, _ *config.TeamsConfig, imp *GitImport) {
			imp.Key = filepath.Join(home, "absent")
		}, "cannot read private key"},
		{"public-key-given", func(t *testing.T, home string, _ *config.TeamsConfig, imp *GitImport) {
			imp.Key = writeKey(t, filepath.Join(home, "id.pub"), publicKeyLine+" key-material-canary\n")
		}, "is not a private key"},
		{"empty-key", func(t *testing.T, home string, _ *config.TeamsConfig, imp *GitImport) {
			imp.Key = writeKey(t, filepath.Join(home, "empty"), "\n")
		}, "is empty"},
		{"passphrase-file-missing", func(t *testing.T, home string, _ *config.TeamsConfig, imp *GitImport) {
			writeKey(t, filepath.Join(home, ".ssh/id_rsa"), encryptedKey)
			imp.PassphraseFile = filepath.Join(home, "absent")
		}, "cannot read passphrase"},
		{"known-hosts-missing", func(t *testing.T, home string, _ *config.TeamsConfig, imp *GitImport) {
			writeKey(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			imp.KnownHostsFile = filepath.Join(home, "absent")
		}, "cannot read known_hosts"},
		{"allowed-signers-missing", func(t *testing.T, home string, cfg *config.TeamsConfig, _ *GitImport) {
			writeKey(t, filepath.Join(home, ".ssh/id_rsa"), plainKey)
			cfg.Spec.Git = &config.Git{
				SigningKey: filepath.Join(home, ".ssh/id_rsa.pub"), Sign: []string{"commits"},
				AllowedSigners: filepath.Join(home, "absent"),
			}
		}, "cannot read allowed signers file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			cfg := &config.TeamsConfig{}
			imp := GitImport{Enabled: true}
			tc.setup(t, home, cfg, &imp)
			docs, err := importGitIdentity(cfg, operatorSources, home, imp)
			if err == nil || docs != nil || !strings.Contains(err.Error(), tc.want) ||
				!strings.HasPrefix(err.Error(), "import git identity: ") {
				t.Fatalf("docs=%v err=%v, want %q", docs, err, tc.want)
			}
			assertNoKeyMaterial(t, err.Error())
		})
	}
}

// globalGit points `git config --global` at a file describing an operator who
// signs commits with the key at keyPath.
func globalGit(t *testing.T, keyPath string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gitconfig")
	content := "[user]\n\tname = Dev Operator\n\temail = dev@example.com\n\tsigningkey = " + keyPath + ".pub\n" +
		"[commit]\n\tgpgsign = true\n[gpg]\n\tformat = ssh\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", path)
}

// TestRunImportsGitIdentityWithConsent is the `kuke team init
// --import-git-identity` path end to end against the fake daemon: the key
// becomes a stored secret, the stack inherits the identity, and the operator
// configuration names the secret instead of a path under ~/.ssh.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one result.
func TestRunImportsGitIdentityWithConsent(t *testing.T) {
	opts := setupRun(t)
	key := writeKey(t, filepath.Join(opts.Home, ".ssh/id_ed25519"), plainKey)
	writeKey(t, key+".pub", publicKeyLine+"\n")
	globalGit(t, key)
	f := opts.Client.(*fakeClient)
	configPath := filepath.Join(opts.Root, "kuketeams.yaml")

	// Without consent nothing is read from ~/.ssh and no secret is created; the
	// operator is told the host path has no effect.
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if f.creates != 0 || !slices.ContainsFunc(result.Notes, func(n string) bool {
		return strings.Contains(n, "host paths") && strings.Contains(n, "--import-git-identity")
	}) {
		t.Fatalf("creates=%d notes=%v", f.creates, result.Notes)
	}
	if f.stackDefaults == nil || f.stackDefaults.Git.Signing != nil || len(f.stackDefaults.Git.Credentials) != 0 ||
		f.stackDefaults.Git.Author.Email != "dev@example.com" {
		t.Fatalf("stack git defaults without import = %+v", f.stackDefaults)
	}

	opts.ImportGit = GitImport{Enabled: true}
	for attempt := range 2 {
		result, err = Run(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		// The key is created once; a rerun reuses the stored secret.
		if f.creates != 1 || len(f.created) != 1 {
			t.Fatalf("attempt %d: creates = %d", attempt, f.creates)
		}
		created := f.created[0]
		if created.Metadata != (api.SecretMetadata{Name: "git-ssh", Realm: "default"}) ||
			created.Spec.Type != api.SecretTypeSSHKey || created.Spec.SSHKey.PrivateKey != plainKey ||
			created.Spec.SSHKey.PublicKey != publicKeyLine {
			t.Fatalf("created secret = %+v", created.Metadata)
		}
		assertNoKeyMaterial(t, string(f.raw))
		ref := &api.ContainerSecretRef{Name: "git-ssh", Realm: "default"}
		want := &api.ContainerGit{
			Author:      &api.GitIdentity{Name: "Dev Operator", Email: "dev@example.com"},
			Committer:   &api.GitIdentity{Name: "Dev Operator", Email: "dev@example.com"},
			Credentials: []api.GitCredential{{Host: "github.com", SecretRef: ref}},
			Signing:     &api.GitSigning{SecretRef: ref},
			Sign:        []string{"commits"},
		}
		if !reflect.DeepEqual(f.stackDefaults.Git, want) {
			t.Fatalf("attempt %d: stack git defaults = %+v, want %+v", attempt, f.stackDefaults.Git, want)
		}
		// First import updates the stack (4 documents + the stack); the rerun
		// finds it already there.
		if wantResources := 5 - attempt; result.Resources != wantResources {
			t.Fatalf("attempt %d: resources = %d, want %d", attempt, result.Resources, wantResources)
		}
		if len(result.MissingSecrets) != 0 || len(result.Notes) != 0 {
			t.Fatalf("attempt %d: missing=%v notes=%v", attempt, result.MissingSecrets, result.Notes)
		}
		raw, readErr := os.ReadFile(configPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		assertNoKeyMaterial(t, string(raw))
		cfg, decodeErr := config.Decode(bytes.NewReader(raw))
		if decodeErr != nil {
			t.Fatalf("rewritten configuration is invalid: %v\n%s", decodeErr, raw)
		}
		keyRef := config.SecretRef{Name: "git-ssh", Realm: "default"}
		if cfg.Spec.Git.SigningKey != "" || cfg.Spec.Git.Signing == nil || cfg.Spec.Git.Signing.SecretRef != keyRef ||
			len(
				cfg.Spec.Git.Credentials,
			) != 1 || cfg.Spec.Git.Credentials[0] != (config.GitCredential{Host: "github.com", SecretRef: keyRef}) ||
			bytes.Contains(raw, []byte(".ssh")) {
			t.Fatalf("configuration does not reference the stored secret:\n%s", raw)
		}
		if info, statErr := os.Stat(configPath); statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("configuration permissions: %v", statErr)
		}
	}
}

// A failed import or a failed apply leaves the daemon and the configuration
// without any reference to a secret that was never stored.
func TestRunImportFailuresChangeNothingDurable(t *testing.T) {
	t.Run("no-key", func(t *testing.T) {
		opts := setupRun(t)
		opts.ImportGit = GitImport{Enabled: true}
		f := opts.Client.(*fakeClient)
		_, err := Run(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "no SSH private key found") || f.applies != 0 ||
			f.creates != 0 {
			t.Fatalf("err=%v applies=%d creates=%d", err, f.applies, f.creates)
		}
		if _, statErr := os.Stat(filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")); !os.IsNotExist(statErr) {
			t.Fatal("team registered despite the failed import")
		}
	})
	t.Run("apply-fails", func(t *testing.T) {
		opts := setupRun(t)
		key := writeKey(t, filepath.Join(opts.Home, ".ssh/id_ed25519"), plainKey)
		globalGit(t, key)
		opts.ImportGit = GitImport{Enabled: true}
		f := opts.Client.(*fakeClient)
		f.applyErr = os.ErrDeadlineExceeded
		_, err := Run(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "apply RPC failed") {
			t.Fatalf("err=%v", err)
		}
		assertNoKeyMaterial(t, err.Error())
		raw, readErr := os.ReadFile(filepath.Join(opts.Root, "kuketeams.yaml"))
		if readErr != nil || bytes.Contains(raw, []byte("git-ssh")) {
			t.Fatalf("configuration references a secret before the apply succeeded: %v\n%s", readErr, raw)
		}
	})
}
