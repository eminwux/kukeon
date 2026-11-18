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

package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/bootstrap"
	"github.com/eminwux/kukeon/internal/team/initialize"
)

// TestInitImportGitIdentityFlags: copying a private key off the host needs the
// operator's explicit --import-git-identity; the dependent flags only refine it.
func TestInitImportGitIdentityFlags(t *testing.T) {
	run := func(t *testing.T, args ...string) (initialize.GitImport, bool, error) {
		t.Helper()
		var got initialize.GitImport
		ran := false
		cmd := newInitCommand(
			func() (string, error) { return "/home/operator", nil },
			func(_ context.Context, opts initialize.Options) (initialize.Result, error) {
				got, ran = opts.ImportGit, true
				return initialize.Result{}, nil
			},
		)
		cmd.SilenceUsage, cmd.SilenceErrors = true, true
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return got, ran, cmd.Execute()
	}

	// Default: no consent, nothing forwarded.
	got, ran, err := run(t)
	if err != nil || !ran || got != (initialize.GitImport{}) {
		t.Fatalf("default import = %+v ran=%v err=%v", got, ran, err)
	}
	got, ran, err = run(t, "--import-git-identity")
	if err != nil || !ran || got != (initialize.GitImport{Enabled: true}) {
		t.Fatalf("consent alone = %+v ran=%v err=%v", got, ran, err)
	}
	got, ran, err = run(t, "--force", "--import-git-identity",
		"--git-key", "/keys/deploy",
		"--git-key-passphrase-file", "/keys/passphrase",
		"--git-known-hosts", "/keys/known_hosts",
	)
	want := initialize.GitImport{
		Enabled: true, Key: "/keys/deploy", PassphraseFile: "/keys/passphrase", KnownHostsFile: "/keys/known_hosts",
	}
	if err != nil || !ran || got != want {
		t.Fatalf("import = %+v ran=%v err=%v", got, ran, err)
	}

	// Each refining flag is refused without consent, before anything runs.
	for _, args := range [][]string{
		{"--git-key", "/keys/deploy"},
		{"--git-key-passphrase-file", "/keys/passphrase"},
		{"--git-known-hosts", "/keys/known_hosts"},
		{"--import-git-identity=false", "--git-key", "/keys/deploy"},
	} {
		_, ran, err = run(t, args...)
		if err == nil || ran || !strings.Contains(err.Error(), "require --import-git-identity") {
			t.Fatalf("%v: ran=%v err=%v", args, ran, err)
		}
	}
}

func TestInitHelpExplainsStoredGitCredentials(t *testing.T) {
	cmd := newInitCommand(func() (string, error) { return "", nil },
		func(context.Context, initialize.Options) (initialize.Result, error) {
			t.Fatal("init must not run for --help")
			return initialize.Result{}, nil
		})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"--import-git-identity", "--git-key ", "--git-key-passphrase-file", "--git-known-hosts",
		"~/.ssh is never mounted", "consent", "git-ssh",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help lacks %q:\n%s", want, out.String())
		}
	}
}

const (
	hostPathNote = "spec.git.signingKey / allowedSigners are host paths and no longer reach a cell"
	renameNote   = "blueprint claude already exists in realm default with different content; applied this team's as claude-0123456789abcdef"
)

// Notes follow the missing-secret guidance on a successful init, and are not
// printed at all when there are none.
func TestInitPrintsNotes(t *testing.T) {
	for _, notes := range [][]string{nil, {hostPathNote, renameNote}} {
		home := t.TempDir()
		cmd := newInitCommand(func() (string, error) { return home, nil },
			func(context.Context, initialize.Options) (initialize.Result, error) {
				return initialize.Result{
					EntryPath: filepath.Join(home, ".kuke", "kuketeam.d", "kuke.yaml"),
					Resources: 5,
					Bootstrap: bootstrap.Result{
						Path:    filepath.Join(home, ".kuke", "kuketeams.yaml"),
						Created: true,
					},
					MissingSecrets: []string{"git-ssh  →  kuke create secret git-ssh --realm default --type ssh-key"},
					Notes:          notes,
				}, nil
			})
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Applied 5 resources; registered kuke team:") ||
			!strings.Contains(got, "  git-ssh  →  kuke create secret git-ssh --realm default --type ssh-key\n") {
			t.Fatalf("output=%q", got)
		}
		if notes == nil {
			if strings.Contains(got, "Notes:") {
				t.Fatalf("empty notes section printed: %q", got)
			}
			continue
		}
		want := "Notes:\n  " + hostPathNote + "\n  " + renameNote + "\n"
		if !strings.HasSuffix(got, want) || strings.Index(got, "Missing secrets") > strings.Index(got, "Notes:") ||
			errOut.Len() != 0 {
			t.Fatalf("output=%q stderr=%q", got, &errOut)
		}
	}
}

func TestAddPrintsNotes(t *testing.T) {
	for _, notes := range [][]string{nil, {renameNote}} {
		cmd := newAddCommand(func() (string, error) { return "/home/operator", nil },
			func(context.Context, initialize.AddOptions) (initialize.Result, error) {
				return initialize.Result{
					Resources: 3, EntryPath: "/home/operator/.kuke/kuketeam.d/esp32.yaml", Notes: notes,
				}, nil
			})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{"esp32", "--source", "git@github.com:example/esp32.git"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.Contains(got, "Applied 3 resources; registered team esp32:") {
			t.Fatalf("output=%q", got)
		}
		if hasNotes := strings.HasSuffix(got, "Notes:\n  "+renameNote+"\n"); hasNotes != (notes != nil) ||
			(notes == nil && strings.Contains(got, "Notes:")) {
			t.Fatalf("output=%q", got)
		}
	}
}

// A failed run prints neither guidance nor notes: nothing was applied.
func TestNotesAreNotPrintedOnFailure(t *testing.T) {
	cmd := newInitCommand(func() (string, error) { return "/home/operator", nil },
		func(context.Context, initialize.Options) (initialize.Result, error) {
			return initialize.Result{Notes: []string{hostPathNote}}, context.DeadlineExceeded
		})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil || out.Len() != 0 {
		t.Fatalf("err=%v output=%q", err, &out)
	}
}
