// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// setupReapply initializes the kuke team from the role as shipped (host
// mounts and the suspended health sweep included) and returns options for a
// following --reapply against the same fake daemon.
func setupReapply(t *testing.T) (Options, *fakeClient, string, string) {
	t.Helper()
	opts := setupRun(t)
	opts.Sources = map[string]fs.FS{"kukeon": os.DirFS("../../..")}
	if _, err := Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	fake := opts.Client.(*fakeClient)
	if !bytes.Contains(fake.raw, []byte("name: kuke-majordomo-codex-health-sweep")) ||
		!bytes.Contains(fake.raw, []byte("suspend: true")) {
		t.Fatalf("init must render the sweep suspended:\n%s", fake.raw)
	}
	opts.Force, opts.Reapply = false, true
	return opts, fake, filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml"), filepath.Join(opts.Root, "kuketeams.yaml")
}

func editEntry(t *testing.T, path string, edit func(*config.TeamEntry)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	edit(entry)
	if raw, err = yaml.Marshal(entry); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReapplyEnablesTheSweepFromTheStoredEntry(t *testing.T) {
	opts, fake, entryPath, configPath := setupReapply(t)
	// A hand-written kuketeams.yaml is read, never written (issue #1591).
	writeTeamsConfig(
		t,
		opts.Root,
		"# hand-written\napiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  defaults: {}\n",
	)
	global, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	on := false
	editEntry(t, entryPath, func(entry *config.TeamEntry) {
		entry.Spec.Roles[0].Tasks = &[]config.TaskSelection{{Name: "health-sweep", Suspend: &on}}
	})
	applies := fake.applies

	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.applies != applies+1 || fake.team != "kuke" || result.EntryPath != entryPath ||
		result.ConfigPath != configPath {
		t.Fatalf("applies=%d team=%q result=%+v", fake.applies, fake.team, result)
	}
	if !bytes.Contains(fake.raw, []byte("cron: '@hourly'")) && !bytes.Contains(fake.raw, []byte("cron: \"@hourly\"")) {
		t.Fatalf("sweep schedule missing:\n%s", fake.raw)
	}
	if bytes.Contains(fake.raw, []byte("suspend: true")) {
		t.Fatal("sweep is still suspended after the entry switched it on")
	}
	if after, readErr := os.ReadFile(configPath); readErr != nil || !bytes.Equal(global, after) {
		t.Fatal("kuketeams.yaml must be untouched by --reapply")
	}
	rewritten, err := os.ReadFile(entryPath)
	if err != nil || !bytes.Contains(rewritten, []byte("suspend: false")) {
		t.Fatalf("entry lost the edit: %s (%v)", rewritten, err)
	}
}

// A config the new composition no longer renders is absent from the apply
// set; kukeond prunes every kuke-labelled config the set does not include.
func TestReapplyDropsObsoleteConfigsFromTheApplySet(t *testing.T) {
	opts, fake, entryPath, _ := setupReapply(t)
	editEntry(t, entryPath, func(entry *config.TeamEntry) {
		entry.Spec.Roles[0].Tasks = &[]config.TaskSelection{}
		entry.Spec.Harnesses = entry.Spec.Harnesses[:1]
	})
	if _, err := Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if fake.team != "kuke" {
		t.Fatalf("apply was not scoped to the kuke team (prune key): %q", fake.team)
	}
	for _, gone := range []string{"health-sweep", "kuke-majordomo-claude"} {
		if bytes.Contains(fake.raw, []byte(gone)) {
			t.Errorf("%s is still in the apply set", gone)
		}
	}
	if !bytes.Contains(fake.raw, []byte("name: kuke-majordomo-codex")) {
		t.Fatal("the remaining config is missing")
	}
}

func TestReapplyRefusals(t *testing.T) {
	t.Run("no entry", func(t *testing.T) {
		opts, fake, entryPath, _ := setupReapply(t)
		if err := os.Remove(entryPath); err != nil {
			t.Fatal(err)
		}
		applies := fake.applies
		_, err := Run(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "kuke team init") || !strings.Contains(err.Error(), entryPath) {
			t.Fatalf("err = %v", err)
		}
		if fake.applies != applies {
			t.Fatal("daemon was contacted without an entry")
		}
		if _, statErr := os.Stat(entryPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatal("--reapply must not create an entry")
		}
	})
	t.Run("with force", func(t *testing.T) {
		opts, fake, _, _ := setupReapply(t)
		opts.Force = true
		applies := fake.applies
		if _, err := Run(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "mutually exclusive") ||
			fake.applies != applies {
			t.Fatalf("err = %v applies=%d", err, fake.applies-applies)
		}
	})
	t.Run("invalid edited entry", func(t *testing.T) {
		for name, mutate := range map[string]func(string) string{
			"unknown field": func(s string) string {
				return strings.Replace(s, "spec:\n", "spec:\n  sweepEnabled: true\n", 1)
			},
			"misspelled task key": func(s string) string {
				return strings.Replace(s, "initialPath:", "tasks: [{name: health-sweep, suspended: false}]\n      initialPath:", 1)
			},
			"unknown task": func(s string) string {
				return strings.Replace(s, "initialPath:", "tasks: [nightly-sweep]\n      initialPath:", 1)
			},
		} {
			opts, fake, entryPath, _ := setupReapply(t)
			raw, err := os.ReadFile(entryPath)
			if err != nil {
				t.Fatal(err)
			}
			edited := mutate(string(raw))
			if edited == string(raw) {
				t.Fatalf("%s: fixture edit did not apply:\n%s", name, raw)
			}
			if err = os.WriteFile(entryPath, []byte(edited), 0o600); err != nil {
				t.Fatal(err)
			}
			applies := fake.applies
			if _, err = Run(t.Context(), opts); err == nil {
				t.Errorf("%s: accepted", name)
			}
			if fake.applies != applies {
				t.Errorf("%s: something was applied", name)
			}
			if after, readErr := os.ReadFile(entryPath); readErr != nil || string(after) != edited {
				t.Errorf("%s: the edited entry was rewritten", name)
			}
		}
	})
	t.Run("failed apply keeps the entry", func(t *testing.T) {
		opts, fake, entryPath, _ := setupReapply(t)
		on := false
		editEntry(t, entryPath, func(entry *config.TeamEntry) {
			entry.Spec.Roles[0].Tasks = &[]config.TaskSelection{{Name: "health-sweep", Suspend: &on}}
		})
		before, err := os.ReadFile(entryPath)
		if err != nil {
			t.Fatal(err)
		}
		fake.applyErr = errors.New("daemon refused")
		if _, err = Run(t.Context(), opts); err == nil {
			t.Fatal("apply failure not reported")
		}
		if after, readErr := os.ReadFile(entryPath); readErr != nil || !bytes.Equal(before, after) {
			t.Fatal("the entry was rewritten after a failed apply")
		}
	})
}
