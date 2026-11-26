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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// turnLogHarness stands in for claude: it records the file its stdout is
// redirected to (the loop's turn log), then prints a final, wakeup-free result
// so the supervisor loop ends after one turn.
const turnLogHarness = `#!/bin/sh
target=$(readlink "/proc/$$/fd/1")
printf '%s\n' "$target" >> "$STUB_STDOUT"
printf '{"type":"result","is_error":false}\n'
`

// TestClaudeHeadlessTurnLogIsPrivate guards issue #1695: the claude headless
// supervisor wrote each turn to a fixed /tmp/claude-turn-$turn.jsonl, so this
// package's own tests (run by `make test` inside a live headless cell)
// truncated the live supervisor's log and it read the stub's result. Each run
// must keep its turn logs in a directory of its own under TMPDIR, and two runs
// must never share a file.
func TestClaudeHeadlessTurnLogIsPrivate(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("onInit scripts target the Linux work container")
	}
	for _, tool := range []string{"jq", "tail", "mktemp"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	h, err := harness.Load(os.DirFS("../../../team"), "harnesses/claude")
	if err != nil {
		t.Fatal(err)
	}
	if _, lookErr := exec.LookPath(h.Spec.Shell); lookErr != nil {
		t.Skipf("shell %s not available", h.Spec.Shell)
	}
	script, env := onInitScript(t, h, "claude", api.ModeHeadless)
	if strings.Contains(script, "/tmp/claude-turn-") {
		t.Fatal("claude headless onInit still names the shared /tmp/claude-turn-* path")
	}
	bin, tmp := t.TempDir(), t.TempDir()
	if wErr := os.WriteFile(filepath.Join(bin, "claude"), []byte(turnLogHarness), 0o755); wErr != nil {
		t.Fatal(wErr)
	}
	stdouts := filepath.Join(t.TempDir(), "stdouts")
	// Later entries win: the recording stub ahead of the default one.
	env = append(env, "PATH="+bin+string(os.PathListSeparator)+pathOf(env),
		"TMPDIR="+tmp, "STUB_STDOUT="+stdouts)

	// Two supervisor runs in the same TMPDIR, as two loops in one container.
	for range 2 {
		if code := runOnInit(t, h.Spec.Shell, script, env); code != 0 {
			t.Fatalf("headless onInit exit code = %d, want 0", code)
		}
	}

	// Where claude's stdout really went: the run's own turn log, never the
	// shared /tmp/claude-turn-*.jsonl a live supervisor in this container uses.
	data, err := os.ReadFile(stdouts)
	if err != nil {
		t.Fatal(err)
	}
	targets := strings.Fields(string(data))
	if len(targets) != 2 || targets[0] == targets[1] {
		t.Fatalf("claude stdout targets = %q, want two distinct turn logs", targets)
	}
	for _, target := range targets {
		if shared, _ := filepath.Match("/tmp/claude-turn-*.jsonl", target); shared ||
			!strings.HasPrefix(target, tmp+string(os.PathSeparator)) {
			t.Fatalf("claude stdout went to %s, want a file under TMPDIR %s", target, tmp)
		}
	}

	logs, err := filepath.Glob(filepath.Join(tmp, "*", "turn-1.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 || filepath.Dir(logs[0]) == filepath.Dir(logs[1]) {
		t.Fatalf("turn-1 logs under TMPDIR = %v, want one per run in separate dirs", logs)
	}
	for _, p := range logs {
		content, readErr := os.ReadFile(p)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(content), `"type":"result"`) {
			t.Fatalf("%s lacks the turn's result: %q", p, content)
		}
	}
}
