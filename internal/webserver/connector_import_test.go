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

package webserver

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const importKeyCanary = "import-key-material-canary"

// importHost points the server account's home and global git configuration
// at a temporary host: an SSH key the operator signs commits with, unless
// withKey is false.
func importHost(t *testing.T, withKey bool, gitconfig string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("git is required:", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	global := filepath.Join(home, "global.gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if err := os.WriteFile(global, []byte(gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if withKey {
		if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
			t.Fatal(err)
		}
		key := "-----BEGIN OPENSSH PRIVATE" + " KEY-----\n" + importKeyCanary + "\n-----END OPENSSH PRIVATE" + " KEY-----\n"
		for name, content := range map[string]string{
			".ssh/id_ed25519":     key,
			".ssh/id_ed25519.pub": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture dev@host\n",
		} {
			if err := os.WriteFile(filepath.Join(home, name), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return home
}

// TestGitImportFromHost is AC12's handler: the import runs on the server,
// stores the identity, optionally makes it the Realm default, and never sends
// key material back.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one import.
func TestGitImportFromHost(t *testing.T) {
	home := importHost(
		t,
		true,
		"[user]\n\tname = Dev\n\temail = dev@example.com\n\tsigningkey = ~/.ssh/id_ed25519.pub\n[commit]\n\tgpgsign = true\n",
	)
	c := devScopes()
	handler := connectorHandler(t, c)
	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import",
		`{"realm":"dev","hosts":["github.com","gitlab.com"],"makeDefault":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), importKeyCanary) || strings.Contains(w.Body.String(), "PRIVATE KEY") {
		t.Fatalf("response carries key material: %s", w.Body)
	}
	got := decodeBody[gitImportResult](t, w)
	if got.Name != "git-ssh" || got.Realm != "dev" || got.Type != v1beta1.SecretTypeSSHKey || got.Home != home ||
		got.KeyPath != filepath.Join(home, ".ssh/id_ed25519") || got.Identity["hosts"] != "github.com,gitlab.com" ||
		got.Identity["author"] != "Dev <dev@example.com>" || got.Identity["sign"] != "commits" {
		t.Fatalf("result = %+v", got)
	}
	if len(got.Secrets) != 1 || got.Secrets[0] != (gitImportSecret{Name: "git-ssh", Realm: "dev", Action: "created"}) {
		t.Fatalf("secrets = %+v", got.Secrets)
	}
	if len(c.created) != 1 || c.created[0].Spec.SSHKey == nil ||
		!strings.Contains(
			c.created[0].Spec.SSHKey.PrivateKey,
			importKeyCanary,
		) || c.created[0].Spec.Connector != "git" {
		t.Fatalf("created = %+v", c.created)
	}
	if got.Default == nil || got.Default.Identity == nil || got.Default.Identity.Name != "git-ssh" ||
		got.Default.Previous != nil || got.Default.Realm != "dev" {
		t.Fatalf("default = %+v", got.Default)
	}
	var realm v1beta1.RealmDoc
	if len(c.applied) != 1 || yaml.Unmarshal(c.applied[0], &realm) != nil ||
		realm.Spec.Defaults.Git == nil || realm.Spec.Defaults.Git.IdentityRef.Name != "git-ssh" ||
		realm.Spec.Defaults.Values["REGION"] != "eu" {
		t.Fatalf("realm document = %s", c.applied)
	}

	// Without makeDefault, the Realm is left alone; a rerun updates in place.
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import", `{"realm":"dev"}`)
	if w.Code != http.StatusOK || len(c.applied) != 1 {
		t.Fatalf("status=%d applied=%d body=%s", w.Code, len(c.applied), w.Body)
	}
	if got = decodeBody[gitImportResult](t, w); got.Default != nil || got.Secrets[0].Action != "updated" {
		t.Fatalf("rerun = %+v", got)
	}
}

// TestGitImportAuthorOnlyAndRejections: no key gives an author-only identity;
// a file outside the server's home, a missing author and an encrypted key
// fail with a 400 that carries no material.
func TestGitImportAuthorOnlyAndRejections(t *testing.T) {
	importHost(t, false, "[user]\n\tname = Dev\n\temail = dev@example.com\n")
	c := devScopes()
	handler := connectorHandler(t, c)
	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import", `{"realm":"dev"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	got := decodeBody[gitImportResult](t, w)
	if got.Type != v1beta1.SecretTypeOpaque || got.Name != "git-author" || len(c.created) != 1 ||
		c.created[0].Spec.Data != "kukeon:git-author-only" || got.Identity["hosts"] != "" {
		t.Fatalf("author-only result = %+v created = %+v", got, c.created)
	}

	for body, mention := range map[string]string{
		`{"realm":"dev","key":"/etc/ssh/ssh_host_ed25519_key"}`: "home directory",
		`{"realm":"dev","key":"../outside"}`:                    "home directory",
		`{"realm":"dev","passphraseFile":"/etc/shadow"}`:        "home directory",
		`{"realm":"dev","key":".ssh/absent"}`:                   "cannot read private key",
		`{"realm":"dev","name":"../x"}`:                         "unsafe secret name",
		`{"realm":"dev","hosts":["ssh://github.com"]}`:          "bare host",
		`{"realm":"../dev"}`:                                    "unsafe scope",
		`{"realm":"dev","unknown":true}`:                        "invalid request",
	} {
		w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), mention) {
			t.Errorf("%s: status=%d body=%s, want 400 naming %q", body, w.Code, w.Body, mention)
		}
	}

	importHost(t, false, "")
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import", `{"realm":"dev"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no SSH private key") ||
		!strings.Contains(w.Body.String(), "no git author") {
		t.Fatalf("no key, no author: status=%d body=%s", w.Code, w.Body)
	}

	home := importHost(t, false, "")
	encrypted := "-----BEGIN RSA PRIVATE" + " KEY-----\nProc-Type: 4,ENCRYPTED\n" + importKeyCanary +
		"\n-----END RSA PRIVATE" + " KEY-----\n"
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh/id_rsa"), []byte(encrypted), 0o600); err != nil {
		t.Fatal(err)
	}
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/git/import", `{"realm":"dev"}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "passphrase-protected") ||
		strings.Contains(w.Body.String(), importKeyCanary) {
		t.Fatalf("encrypted key: status=%d body=%s", w.Code, w.Body)
	}
}
