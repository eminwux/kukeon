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

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
)

const legacyNote = "Note: ~/.kuke/kuketeams.yaml and ~/.kuke/kuketeam.d/ are no longer read"

// legacyHome writes the old per-user team files that exist under home.
func legacyHome(t *testing.T, home string, file, dir bool) {
	t.Helper()
	root := filepath.Join(home, ".kuke")
	if file {
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "kuketeams.yaml"), []byte("not: [a team"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if dir {
		if err := os.MkdirAll(filepath.Join(root, "kuketeam.d"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "kuketeam.d", "kuke.yaml"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLegacyFilesGetOneNote is AC2: when either old path exists, init, add
// and apply print one note and nothing reads, converts or imports the files.
func TestLegacyFilesGetOneNote(t *testing.T) {
	verbs := map[string][]string{
		"init":  {"init"},
		"add":   {"add", "proj", "--source", projectURL, "--branch", "main", "--dry-run"},
		"apply": {"apply", "kuke"},
	}
	for name, args := range verbs {
		for _, layout := range []struct {
			name      string
			file, dir bool
		}{{"file", true, false}, {"directory", false, true}, {"both", true, true}} {
			t.Run(name+"/"+layout.name, func(t *testing.T) {
				h := projectHarness(t, repoTeam)
				if name == "apply" {
					h.store.putTeam(t, config.Majordomo())
				}
				legacyHome(t, h.home, layout.file, layout.dir)
				before := snapshot(t, filepath.Join(h.home, ".kuke"))
				out, err := h.run(t, args...)
				if err != nil {
					t.Fatalf("%v\n%s", err, out)
				}
				if strings.Count(out, legacyNote) != 1 || !strings.Contains(out, "kuke team add") ||
					!strings.HasPrefix(out, legacyNote) {
					t.Fatalf("output:\n%s", out)
				}
				if after := snapshot(t, filepath.Join(h.home, ".kuke")); after != before {
					t.Fatalf("the old files changed:\nbefore %s\nafter  %s", before, after)
				}
			})
		}
	}
}

// TestNoLegacyNote: without the old files, and for the verbs that never
// read them, there is no note.
func TestNoLegacyNote(t *testing.T) {
	h := projectHarness(t, repoTeam)
	if out, err := h.run(t, "init"); err != nil || strings.Contains(out, "Note:") {
		t.Fatalf("%v\n%s", err, out)
	}
	legacyHome(t, h.home, true, true)
	for _, args := range [][]string{{"source", "list"}, {"defaults", "set", "--harness", "codex"}} {
		if out, err := h.run(t, args...); err != nil || strings.Contains(out, "Note:") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
}

// snapshot lists every path under root with its content.
func snapshot(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		b.WriteString(path + "\n")
		if !d.IsDir() {
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			b.Write(raw)
			b.WriteString("\n")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}
