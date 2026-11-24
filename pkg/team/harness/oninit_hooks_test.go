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

package harness_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// stubPreCommit records its working directory and arguments, then exits with
// STUB_PRE_COMMIT_EXIT (0 when unset).
const stubPreCommit = `#!/bin/sh
printf '%s %s\n' "$PWD" "$*" >> "$STUB_PRE_COMMIT_LOG"
exit "${STUB_PRE_COMMIT_EXIT:-0}"
`

// TestBundledOnInitInstallsProjectHooks guards issue #1642: an agent cell
// commits without the project's pre-commit hooks unless startup installs
// them. Every shipped mode must run `pre-commit install` in $PROJECT_DIR when
// the project has a .pre-commit-config.yaml, skip it otherwise, and still
// start the harness when the install fails.
func TestBundledOnInitInstallsProjectHooks(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("onInit scripts target the Linux work container")
	}
	for _, tool := range []string{"jq", "tee", "cat"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	wantExit := map[string]int{"claude/" + api.ModeHeadless: 1}
	for _, name := range []string{"claude", "codex", "opencode"} {
		h, err := harness.Load(os.DirFS("../../../team"), "harnesses/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, lookErr := exec.LookPath(h.Spec.Shell); lookErr != nil {
			t.Skipf("%s: shell %s not available", name, h.Spec.Shell)
		}
		for mode := range h.Spec.Modes {
			want, ok := wantExit[name+"/"+mode]
			if !ok {
				want = stubHarnessExit
			}
			t.Run(name+"/"+mode, func(t *testing.T) {
				script, env := onInitScript(t, h, name, mode)
				bin := t.TempDir()
				stub := filepath.Join(bin, "pre-commit")
				if writeErr := os.WriteFile(stub, []byte(stubPreCommit), 0o755); writeErr != nil {
					t.Fatal(writeErr)
				}
				env = withPathPrefix(env, bin)

				for _, tc := range []struct {
					name       string
					config     bool
					installRC  string
					wantCalled bool
				}{
					{name: "config present", config: true, installRC: "0", wantCalled: true},
					{name: "install fails", config: true, installRC: "1", wantCalled: true},
					{name: "no config", config: false, installRC: "0", wantCalled: false},
				} {
					project := t.TempDir()
					if tc.config {
						cfg := filepath.Join(project, ".pre-commit-config.yaml")
						if writeErr := os.WriteFile(cfg, []byte("repos: []\n"), 0o644); writeErr != nil {
							t.Fatal(writeErr)
						}
					}
					log := filepath.Join(t.TempDir(), "pre-commit.log")
					caseEnv := append(slices.Clone(env), "PROJECT_DIR="+project,
						"STUB_PRE_COMMIT_LOG="+log, "STUB_PRE_COMMIT_EXIT="+tc.installRC)

					if code := runOnInit(t, h.Spec.Shell, script, caseEnv); code != want {
						t.Fatalf("%s: shell exit code = %d, want %d (the harness must still start)",
							tc.name, code, want)
					}
					got, readErr := os.ReadFile(log)
					if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
						t.Fatal(readErr)
					}
					if !tc.wantCalled {
						if len(got) != 0 {
							t.Fatalf("%s: pre-commit ran without a project config: %q", tc.name, got)
						}
						continue
					}
					if line := strings.TrimSpace(string(got)); line != project+" install" {
						t.Fatalf("%s: pre-commit calls = %q, want %q", tc.name, line, project+" install")
					}
				}
			})
		}
	}
}

// withPathPrefix returns env with dir prepended to its PATH entry, which
// onInitScript always sets. The entry is replaced rather than appended: exec
// keeps the last of duplicate keys.
func withPathPrefix(env []string, dir string) []string {
	out := slices.Clone(env)
	for i, kv := range out {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			out[i] = "PATH=" + dir + string(os.PathListSeparator) + v
		}
	}
	return out
}
