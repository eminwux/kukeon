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

// stubHarnessExit is what the stub harness binary exits with.
const stubHarnessExit = 7

// stubHarness stands in for the harness binary. With STUB_LEFTOVER set it
// reads its whole stdin into that file: the onInit script arrives on the
// shell's stdin, just as sbsh types it into the PTY, so anything the shell has
// not read yet lands in the harness's input. It prints an error turn result
// so the claude supervisor loop ends on its first turn.
const stubHarness = `#!/bin/sh
if [ -n "${STUB_LEFTOVER:-}" ]; then cat > "$STUB_LEFTOVER"; fi
printf '{"type":"result","is_error":true}\n'
exit 7
`

// TestBundledOnInitEndsWithHarness guards issue #1479: sbsh types every
// onInit stage into the PTY at once and bash reads one command at a time, so
// any line after the command that starts the harness sits in the harness's
// input (an interactive TUI shows it typed into its prompt). Each shipped
// mode's onInit runs in its real shell with a stub harness and must
//   - leave nothing unread when the harness starts, and
//   - end the shell when the harness ends, without reading further input,
//     with the harness's own exit code (the claude headless supervisor maps a
//     failed turn to 1 by design).
func TestBundledOnInitEndsWithHarness(t *testing.T) {
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
		modes := slices.Sorted(func(yield func(string) bool) {
			for m := range h.Spec.Modes {
				if !yield(m) {
					return
				}
			}
		})
		for _, mode := range modes {
			t.Run(name+"/"+mode, func(t *testing.T) {
				script, env := onInitScript(t, h, name, mode)
				leftover := filepath.Join(t.TempDir(), "leftover")

				// Nothing may be left for the harness to read.
				runOnInit(t, h.Spec.Shell, script, append(env, "STUB_LEFTOVER="+leftover))
				got, readErr := os.ReadFile(leftover)
				if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
					t.Fatal(readErr)
				}
				if len(got) != 0 {
					t.Fatalf("onInit leaves text after the harness command; the harness "+
						"reads it as input: %q", got)
				}

				// The shell must end with the harness, never read the next line.
				want, ok := wantExit[name+"/"+mode]
				if !ok {
					want = stubHarnessExit
				}
				if code := runOnInit(t, h.Spec.Shell, script+"exit 99\n", env); code != want {
					t.Fatalf("shell exit code = %d, want %d (99 means the shell outlived "+
						"the harness and read further input)", code, want)
				}
			})
		}
	}
}

// onInitScript returns a mode's onInit stages in order plus an environment
// that satisfies their `set -u` references, with the stub harness on PATH.
func onInitScript(t *testing.T, h *harness.Harness, name, mode string) (string, []string) {
	t.Helper()
	m := h.Spec.Modes[mode]
	var b strings.Builder
	for _, stage := range append(slices.Clone(h.Spec.OnInit), m.OnInit...) {
		b.WriteString(stage.Script)
		if !strings.HasSuffix(stage.Script, "\n") {
			b.WriteString("\n")
		}
	}

	bin, home := t.TempDir(), t.TempDir()
	stubs := map[string]string{name: stubHarness, "make": "#!/bin/sh\nexit 0\n"}
	for file, body := range stubs {
		if err := os.WriteFile(filepath.Join(bin, file), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"HOME=" + home, "INITIAL_PATH=" + home,
		"ROLE_SOURCE_DIR=" + home, "ROLE_PATH=role", "HARNESS_SETTINGS=settings.json",
	}
	for _, vars := range []map[string]string{h.Spec.Environment, m.Environment} {
		for k := range vars {
			env = append(env, k+"=x")
		}
	}
	for _, p := range append(slices.Clone(h.Spec.Parameters), m.Parameters...) {
		env = append(env, p.Name+"=x")
	}
	return b.String(), env
}

// runOnInit feeds script to shell on a pipe (bash then reads it a line at a
// time, as from the PTY) and returns the shell's exit code.
func runOnInit(t *testing.T, shell, script string, env []string) int {
	t.Helper()
	cmd := exec.Command(shell, "-s")
	cmd.Dir = t.TempDir()
	cmd.Env = env
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	default:
		t.Fatalf("run %s: %v\n%s", shell, err, out)
		return -1
	}
}
