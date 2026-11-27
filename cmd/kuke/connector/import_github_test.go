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

package connector_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

const ghToken = "ghp-cli-token-canary"

// runStdin executes `kuke connector` against daemon (nil: no daemon may be
// reached) with stdin as its standard input.
func runStdin(t *testing.T, daemon *fakeDaemon, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := connector.NewConnectorCmdWithCatalog(connectortest.Catalog(t))
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	var client connector.Client = &unreachable{}
	if daemon != nil {
		client = daemon
	}
	cmd.SetContext(context.WithValue(context.Background(), connector.MockControllerKey{}, client))
	cmd.SetArgs(args)
	err := cmd.Execute()
	if strings.Contains(out.String(), ghToken) || (err != nil && strings.Contains(err.Error(), ghToken)) {
		t.Fatalf("output carries the token: %s (err %v)", out, err)
	}
	return out.String(), err
}

// TestImportGitHubWithGitAndDefault: --git stores the token twice — the
// github identity and a basic-auth git identity for github.com that names it
// — and --default makes both the Realm defaults; a rerun rotates both.
func TestImportGitHubWithGitAndDefault(t *testing.T) {
	daemon := newFakeDaemon()
	out, err := runStdin(t, daemon, ghToken+"\n", "import", "github", "--token-stdin", "--git", "--default",
		"--author", "Dev <dev@example.com>")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(daemon.created) != 2 {
		t.Fatalf("created %d secrets, want 2", len(daemon.created))
	}
	token, git := daemon.created[0], daemon.created[1]
	if token.Metadata != (v1beta1.SecretMetadata{Name: "github-token", Realm: "default"}) ||
		token.Spec.Connector != "github" || token.Spec.Data != ghToken {
		t.Errorf("github identity = %+v", token)
	}
	if git.Metadata.Name != "github-git" || git.Spec.Type != v1beta1.SecretTypeBasicAuth ||
		git.Spec.BasicAuth.Username != "x-access-token" || git.Spec.BasicAuth.Token != ghToken ||
		git.Spec.Identity["hosts"] != "github.com" || git.Spec.Identity["author"] != "Dev <dev@example.com>" ||
		git.Spec.Identity["kukeon.io/token-from"] != "github-token@default" {
		t.Errorf("git identity = %+v", git)
	}
	if ref := daemon.realmGitDefault(); ref == nil || ref.Name != "github-git" {
		t.Errorf("realm git default = %+v", ref)
	}
	defaults := daemon.realms["default"].Spec.Defaults
	if defaults == nil || defaults.Secrets["github-token"].SecretRef == nil ||
		defaults.Secrets["github-token"].SecretRef.Name != "github-token" {
		t.Errorf("realm github default = %+v", defaults)
	}
	for _, want := range []string{
		`Secret "github-token" (realm "default"): created`,
		"Git identity github-git@default (basic-auth for github.com, token from github-token@default)",
		`Realm "default" default github identity: github-token@default (was none)`,
		`Realm "default" default git identity: github-git@default (was none)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}

	// `kuke connector list` shows both identities and both defaults.
	if out, err = runStdin(t, daemon, "", "list"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, row := range [][]string{
		{"github", "github-token", "default", "opaque", "-", "-"},
		{"git", "github-git", "default", "basic-auth", "-", "-"},
		{"github", "token", "default", "github-token@default"},
		{"git", "-", "default", "github-git@default"},
	} {
		if !hasRow(out, row) {
			t.Errorf("list lacks row %v:\n%s", row, out)
		}
	}

	// A rerun with --git rotates the linked identity and keeps its author.
	tokenFile := writeFile(t, filepath.Join(t.TempDir(), "token"), ghToken+"-2\n")
	if out, err = runStdin(t, daemon, "", "import", "github", "--token-file", tokenFile, "--git"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	rotated := daemon.created[3]
	if rotated.Metadata.Name != "github-git" || rotated.Spec.BasicAuth.Token != ghToken+"-2" ||
		rotated.Spec.Identity["author"] != "Dev <dev@example.com>" ||
		!strings.Contains(out, `Secret "github-git" (realm "default"): updated`) {
		t.Errorf("rotation stored %+v:\n%s", rotated, out)
	}
}

// TestImportGitHubWithoutGit: without --git only the github identity is
// stored and git is left alone.
func TestImportGitHubWithoutGit(t *testing.T) {
	daemon := newFakeDaemon()
	out, err := runStdin(t, daemon, ghToken, "import", "github", "--token-stdin", "--default")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(daemon.created) != 1 || daemon.created[0].Spec.Connector != "github" || daemon.realmGitDefault() != nil {
		t.Fatalf("created = %+v, git default = %+v", daemon.created, daemon.realmGitDefault())
	}
	if strings.Contains(out, "Warning") {
		t.Errorf("no git identity is linked, yet the import warns:\n%s", out)
	}
}

// TestImportGitHubWithoutGitWarnsOfTheLinkedIdentity: rotating the github
// identity without --git leaves its linked git identity on the old token, and
// the command says so and how to rotate it (issue #1707).
func TestImportGitHubWithoutGitWarnsOfTheLinkedIdentity(t *testing.T) {
	daemon := newFakeDaemon()
	if out, err := runStdin(t, daemon, ghToken, "import", "github", "--token-stdin", "--git"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := runStdin(t, daemon, ghToken+"-2", "import", "github", "--token-stdin")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(daemon.created) != 3 || daemon.created[2].Spec.Connector != "github" {
		t.Fatalf("created = %+v, want the github identity alone rotated", daemon.created)
	}
	want := "Warning: git identity github-git@default still holds the previous token: it is linked to github " +
		"identity github-token@default and was not rotated. Rerun with --git to rotate it too."
	if !strings.Contains(out, want) {
		t.Errorf("output lacks %q:\n%s", want, out)
	}
}

// TestImportGitHubWithoutGitStoresWhenTheLookupFails: without --git the
// linked git identity is looked up only to warn, so a failed lookup is
// reported and the token is stored all the same; with --git it fails the
// command before anything is stored.
func TestImportGitHubWithoutGitStoresWhenTheLookupFails(t *testing.T) {
	daemon := newFakeDaemon()
	daemon.listSecretsErr = errors.New("list refused")
	out, err := runStdin(t, daemon, ghToken, "import", "github", "--token-stdin")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(daemon.created) != 1 ||
		!strings.Contains(
			out,
			"Warning: could not look for a git identity linked to the github identity: list refused",
		) {
		t.Errorf("created = %+v:\n%s", daemon.created, out)
	}
	if _, err = runStdin(t, daemon, ghToken, "import", "github", "--token-stdin", "--git"); err == nil ||
		!strings.Contains(err.Error(), "list refused") || len(daemon.created) != 1 {
		t.Errorf("with --git: err = %v, created %d", err, len(daemon.created))
	}
}

// TestImportGitHubDryRun: --dry-run prints both identities and reaches no
// daemon.
func TestImportGitHubDryRun(t *testing.T) {
	out, err := runStdin(t, nil, ghToken, "import", "github", "--token-stdin", "--git", "--default", "--dry-run",
		"--username", "octo", "--hosts", "github.com,ghe.example.com")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"Dry run: nothing is stored.",
		"Secret: github-token@default (type opaque, connector github)",
		"Secret: github-git@default (type basic-auth, connector git)",
		"  username: octo",
		"  hosts: github.com,ghe.example.com",
		"  kukeon.io/token-from: github-token@default",
		`Would set the realm "default" default git identity to github-git@default.`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

// TestImportGitHubRejections: the token comes from exactly one source, git
// options need --git, and nothing is stored on a rejection.
func TestImportGitHubRejections(t *testing.T) {
	file := writeFile(t, filepath.Join(t.TempDir(), "token"), ghToken)
	for name, args := range map[string][]string{
		"no token source":   {"import", "github"},
		"two token sources": {"import", "github", "--token-stdin", "--token-file", file},
		"empty token":       {"import", "github", "--token-file", writeFile(t, filepath.Join(t.TempDir(), "e"), "\n")},
		"author without git": {
			"import", "github", "--token-stdin", "--author", "Dev <dev@example.com>",
		},
	} {
		t.Run(name, func(t *testing.T) {
			daemon := newFakeDaemon()
			_, err := runStdin(t, daemon, ghToken, args...)
			if !errors.Is(err, errdefs.ErrGitHubToken) {
				t.Fatalf("err = %v, want ErrGitHubToken", err)
			}
			if len(daemon.created) != 0 {
				t.Errorf("stored %+v", daemon.created)
			}
		})
	}
}
