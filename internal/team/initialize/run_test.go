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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/teamtest"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// sourceFS is the repository's team definitions with the majordomo role made
// plain (no host mounts, no task), so these tests count only what they add.
func sourceFS() map[string]fs.FS {
	source, err := teamtest.PlainSource("../../..")
	if err != nil {
		panic(err)
	}
	return map[string]fs.FS{"kukeon": source}
}

func setupRun(t *testing.T) Options {
	t.Helper()
	// kuketeams.yaml is optional (issue #1591): a fresh host has none.
	root := filepath.Join(t.TempDir(), ".kuke")
	return Options{Root: root, Home: filepath.Dir(root), Sources: sourceFS(), Client: newFake(), Force: true}
}

// writeTeamsConfig writes a hand-written kuketeams.yaml.
func writeTeamsConfig(t *testing.T, root, content string) string {
	t.Helper()
	path := filepath.Join(root, "kuketeams.yaml")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRunExistingEntryRequiresForce: "already initialized" is the kuke team
// entry, not kuketeams.yaml (issue #1591).
func TestRunExistingEntryRequiresForce(t *testing.T) {
	opts := setupRun(t)
	if _, err := Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")
	before, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	opts.Force = false
	f := newFake()
	opts.Client = f
	if _, err = Run(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "already initialized") ||
		!strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "--reapply") {
		t.Fatalf("expected already-initialized guidance, got %v", err)
	}
	after, err := os.ReadFile(entryPath)
	if err != nil || !bytes.Equal(before, after) || f.applies != 0 {
		t.Fatal("existing registration or daemon changed")
	}
}

// TestRunWithoutConfiguration is AC1 and AC4: on a host whose global git
// configuration signs with an SSH key, init reads none of it, writes no
// kuketeams.yaml and prints no git note.
func TestRunWithoutConfiguration(t *testing.T) {
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Dev\n\temail = dev@example.com\n"+
		"\tsigningkey = ~/.ssh/id_ed25519.pub\n[commit]\n\tgpgsign = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	opts := setupRun(t)
	opts.Force = false
	result, err := Run(t.Context(), opts)
	if err != nil || result.Resources != 4 || result.ConfigPath != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(opts.Root, "kuketeams.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("init wrote kuketeams.yaml: %v", statErr)
	}
	for _, note := range result.Notes {
		if strings.Contains(strings.ToLower(note), "git") {
			t.Fatalf("init printed a git note: %q", note)
		}
	}
	if f := opts.Client.(*fakeClient); f.creates != 0 || f.stackDefaults != nil {
		t.Fatalf("init stored a secret or stack git defaults: creates=%d stack=%+v", f.creates, f.stackDefaults)
	}
}

// TestRunForceResetsEntryAndKeepsConfiguration: --force replaces the kuke
// team entry and never touches a hand-written kuketeams.yaml (AC4).
func TestRunForceResetsEntryAndKeepsConfiguration(t *testing.T) {
	opts := setupRun(t)
	original := []byte("# authored comment\napiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  defaults: {}\n")
	path := writeTeamsConfig(t, opts.Root, string(original))
	for range 2 {
		result, runErr := Run(t.Context(), opts)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if result.Resources != 4 || result.EntryPath != filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml") ||
			result.ConfigPath != path {
			t.Fatalf("result=%+v", result)
		}
		after, readErr := os.ReadFile(path)
		info, statErr := os.Stat(path)
		if readErr != nil || statErr != nil || !bytes.Equal(after, original) || info.Mode().Perm() != 0o640 {
			t.Fatal("init --force touched the hand-written configuration")
		}
		entryBytes, readErr := os.ReadFile(result.EntryPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		entry, decodeErr := config.DecodeEntry(bytes.NewReader(entryBytes))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if entry.Metadata.Name != "kuke" || len(entry.Spec.Roles) != 1 || len(entry.Spec.Sources) != 1 ||
			len(entry.Spec.Harnesses) != 2 {
			t.Fatal("initial explicit declaration lost")
		}
		info, statErr = os.Stat(result.EntryPath)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("unsafe entry permissions")
		}
	}
	f := opts.Client.(*fakeClient)
	if f.applies != 2 || f.team != "kuke" {
		t.Fatal("rerun did not update stable identity")
	}
}

// TestRunRejectsLeftoverSpecGit is D7: a kuketeams.yaml that still carries
// spec.git fails before anything reaches the daemon, naming the replacement.
func TestRunRejectsLeftoverSpecGit(t *testing.T) {
	for _, reapplying := range []bool{false, true} {
		opts := setupRun(t)
		if reapplying {
			if _, err := Run(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			opts.Force, opts.Reapply = false, true
		}
		writeTeamsConfig(t, opts.Root, "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n"+
			"  git:\n    author: {name: Dev, email: dev@example.com}\n")
		f := newFake()
		opts.Client = f
		_, err := Run(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "kuke connector import git") || f.applies != 0 {
			t.Fatalf("reapply=%v: err=%v applies=%d", reapplying, err, f.applies)
		}
	}
}

func TestRegistrationOnlyChangesAfterFullSuccess(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"rpc", "total", "partial", "preflight"} {
			t.Run(fmt.Sprintf("existing-%t/%s", existing, mode), func(t *testing.T) {
				opts := setupRun(t)
				entryPath := filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")
				if existing {
					if _, err := Run(t.Context(), opts); err != nil {
						t.Fatal(err)
					}
				}
				before, _ := os.ReadFile(entryPath)
				f := newFake()
				opts.Client = f
				switch mode {
				case "rpc":
					f.applyErr = io.EOF
				case "total":
					f.resources = []kukeonv1.ApplyResourceResult{
						{Action: "failed", Error: "bad blueprint"},
						{Action: "failed", Error: "bad config"},
					}
				case "partial":
					f.resources = []kukeonv1.ApplyResourceResult{
						{Action: "updated"},
						{Action: "failed", Error: "bad config"},
					}
				case "preflight":
					f.missingRealm = true
				}
				result, err := Run(t.Context(), opts)
				if err == nil || result.EntryPath != "" {
					t.Fatalf("claimed successful registration: %+v, %v", result, err)
				}
				after, readErr := os.ReadFile(entryPath)
				if existing {
					if readErr != nil || !bytes.Equal(before, after) {
						t.Fatal("prior registration changed")
					}
				} else if !os.IsNotExist(readErr) {
					t.Fatal("failed team registered")
				}
				entries, readErr := os.ReadDir(filepath.Join(opts.Root, "kuketeam.d"))
				if readErr != nil {
					t.Fatal(readErr)
				}
				for _, entry := range entries {
					if strings.HasPrefix(entry.Name(), ".kuke-") {
						t.Fatal("staging file leaked")
					}
				}
			})
		}
	}
}

func TestRunForceResetsExistingMajordomoDeclaration(t *testing.T) {
	opts := setupRun(t)
	team := config.Majordomo()
	team.Spec.Roles = []config.Role{
		{Name: "custom", Source: "kukeon", Path: "team/roles/majordomo", Harness: "codex", InitialPath: "/opt/custom"},
	}
	entry, err := config.Entry(team)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(opts.Client.(*fakeClient).raw), "kuke-custom-codex") {
		t.Fatal("authored majordomo selection retained")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := config.DecodeEntry(bytes.NewReader(after))
	if err != nil || accepted.Spec.Roles[0].Name != "majordomo" {
		t.Fatal("entry was not reset")
	}
}

func TestForceReplacesInvalidExistingEntry(t *testing.T) {
	for _, raw := range []string{"kind: Wrong\n", "apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata:\n  name: different\nspec: {}\n"} {
		opts := setupRun(t)
		path := filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Run(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		if opts.Client.(*fakeClient).applies != 1 {
			t.Fatal("initial majordomo was not applied")
		}
	}
}

func TestLegacyRootRegistrationIsUntouched(t *testing.T) {
	opts := setupRun(t)
	legacy := filepath.Join(opts.Root, "kuketeam.d", "root.yaml")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(
		"# legacy declaration kept verbatim\napiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: {name: root}\nspec: {}\n",
	)
	if err := os.WriteFile(legacy, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("legacy registration changed")
	}
	if opts.Client.(*fakeClient).team != "kuke" {
		t.Fatal("applied legacy identity")
	}
}
