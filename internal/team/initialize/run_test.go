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
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/bootstrap"
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
	// Run bootstraps from the real `git config --global`; keep the developer's
	// identity out so resource counts do not depend on the host.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	root := filepath.Join(t.TempDir(), ".kuke")
	_, err := bootstrap.Run(t.Context(), bootstrap.Options{Root: root, Git: emptyGit{}})
	if err != nil {
		t.Fatal(err)
	}
	return Options{Root: root, Home: filepath.Dir(root), Sources: sourceFS(), Client: newFake(), Force: true}
}

type emptyGit struct{}

func (emptyGit) Lookup(context.Context, string, bool) (string, error) {
	return "", bootstrap.ErrMissing
}

func TestRunExistingConfigurationRequiresForce(t *testing.T) {
	opts := setupRun(t)
	opts.Force = false
	path := filepath.Join(opts.Root, "kuketeams.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected existing configuration guidance, got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || opts.Client.(*fakeClient).applies != 0 {
		t.Fatal("existing configuration or daemon changed")
	}
}

func TestRunCreatesMissingConfiguration(t *testing.T) {
	opts := setupRun(t)
	path := filepath.Join(opts.Root, "kuketeams.yaml")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	opts.Force = false
	result, err := Run(t.Context(), opts)
	if err != nil || !result.Bootstrap.Created || result.Resources != 4 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunForceResetsConfigurationAndEntry(t *testing.T) {
	opts := setupRun(t)
	path := filepath.Join(opts.Root, "kuketeams.yaml")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	original = append([]byte("# authored comment\n"), original...)
	if err = os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		result, runErr := Run(t.Context(), opts)
		if runErr != nil {
			t.Fatal(runErr)
		}
		if result.Resources != 4 || result.EntryPath != filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml") {
			t.Fatalf("result=%+v", result)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil || bytes.Equal(after, original) || bytes.Contains(after, []byte("authored comment")) {
			t.Fatal("configuration not reset")
		}
		entryBytes, readErr := os.ReadFile(result.EntryPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		entry, decodeErr := config.DecodeEntry(bytes.NewReader(entryBytes))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if entry.Metadata.Name != "kukeon" || len(entry.Spec.Roles) != 1 || len(entry.Spec.Sources) != 1 ||
			len(entry.Spec.Harnesses) != 2 {
			t.Fatal("initial explicit declaration lost")
		}
		info, statErr := os.Stat(result.EntryPath)
		if statErr != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("unsafe entry permissions")
		}
	}
	f := opts.Client.(*fakeClient)
	if f.applies != 2 || f.team != "kukeon" {
		t.Fatal("rerun did not update stable identity")
	}
}

func TestRegistrationOnlyChangesAfterFullSuccess(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"rpc", "total", "partial", "preflight"} {
			t.Run(fmt.Sprintf("existing-%t/%s", existing, mode), func(t *testing.T) {
				opts := setupRun(t)
				entryPath := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
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
					if strings.HasPrefix(entry.Name(), ".kukeon-") {
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
	path := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = Run(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(opts.Client.(*fakeClient).raw), "kukeon-custom-codex") {
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
		path := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
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
	if opts.Client.(*fakeClient).team != "kukeon" {
		t.Fatal("applied legacy identity")
	}
}
