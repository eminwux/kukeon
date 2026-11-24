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
	"strings"
	"syscall"
	"testing"
	"time"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// leftoverHarness stands in for a harness binary that finishes its run but
// leaves a child behind holding its stdout and stderr, as a backgrounded tool
// or a daemonized helper does. It prints a successful turn result so the
// claude supervisor loop ends on its first turn.
const leftoverHarness = `#!/bin/sh
printf '{"type":"result","is_error":false}\n'
sleep 300 &
exit 0
`

// leftoverDeadline bounds how long a headless onInit may outlive its harness.
const leftoverDeadline = 20 * time.Second

// TestBundledHeadlessEndsDespiteLeftoverChild guards against a headless run
// that never ends: once the harness exits, its shell must exit too, even
// while a child the harness left behind still holds the harness's stdout.
// A `harness | tee` stage fails this, because tee waits for every writer of
// the pipe to close and the shell waits for tee.
func TestBundledHeadlessEndsDespiteLeftoverChild(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("onInit scripts target the Linux work container")
	}
	for _, tool := range []string{"jq", "tail", "sleep"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	for _, name := range []string{"claude", "codex", "opencode", "script"} {
		t.Run(name, func(t *testing.T) {
			h, err := harness.Load(os.DirFS("../../../team"), "harnesses/"+name)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if _, ok := h.Spec.Modes[api.ModeHeadless]; !ok {
				t.Skipf("%s declares no %s mode", name, api.ModeHeadless)
			}
			if _, lookErr := exec.LookPath(h.Spec.Shell); lookErr != nil {
				t.Skipf("%s: shell %s not available", name, h.Spec.Shell)
			}
			script, env := onInitScript(t, h, name, api.ModeHeadless)
			bin := filepath.Join(t.TempDir(), "bin")
			if mkErr := os.Mkdir(bin, 0o755); mkErr != nil {
				t.Fatal(mkErr)
			}
			if wErr := os.WriteFile(filepath.Join(bin, name), []byte(leftoverHarness), 0o755); wErr != nil {
				t.Fatal(wErr)
			}
			// Later entries win: put the leftover stub ahead of the default
			// stub, and point the script harness's COMMAND at it.
			env = append(env, "PATH="+bin+string(os.PathListSeparator)+pathOf(env), "COMMAND="+name)

			code, out := runDetached(t, h.Spec.Shell, script, env)
			if code != 0 {
				t.Fatalf("shell exit code = %d, want 0\n%s", code, out)
			}
		})
	}
}

// pathOf returns the last PATH entry of env.
func pathOf(env []string) string {
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	return path
}

// runDetached runs script in shell with its output on a file rather than a
// pipe, so a leftover child holding that output cannot hold up the wait, as
// sbsh waits on the shell process rather than on its PTY. It fails the test
// when the shell outlives leftoverDeadline, and kills the shell's whole
// process group when done so no leftover child survives the test.
func runDetached(t *testing.T, shell, script string, env []string) (int, string) {
	t.Helper()
	outPath := filepath.Join(t.TempDir(), "out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	in, err := os.CreateTemp(t.TempDir(), "script")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, wErr := in.WriteString(script); wErr != nil {
		t.Fatal(wErr)
	}
	if _, sErr := in.Seek(0, 0); sErr != nil {
		t.Fatal(sErr)
	}

	cmd := exec.Command(shell, "-s")
	cmd.Dir = t.TempDir()
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = in, out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if startErr := cmd.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	defer func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err = <-done:
	case <-time.After(leftoverDeadline):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
		got, _ := os.ReadFile(outPath)
		t.Fatalf("shell still running %s after the harness exited; a leftover "+
			"child holding its output keeps the run alive\n%s", leftoverDeadline, got)
	}
	got, _ := os.ReadFile(outPath)
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0, string(got)
	case errors.As(err, &exitErr):
		return exitErr.ExitCode(), string(got)
	default:
		t.Fatalf("run %s: %v\n%s", shell, err, got)
		return -1, ""
	}
}
