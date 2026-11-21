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

package e2e_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/util/fs"
)

// screenPollTimeout caps how long the scenario polls `kuke screenshot` for
// text it just wrote. sh echoes typed input and runs a builtin in tens of
// milliseconds; the cap only keeps a regression from hanging the suite.
const screenPollTimeout = 15 * time.Second

// TestKuke_WriteThenScreenshot is the e2e scenario for `kuke write` and
// `kuke screenshot` (issue #1536): write into an Attachable cell's shell,
// screenshot it and see the written text, then submit it with ^M and see
// the shell's output. Both commands reach the terminal through the per-test
// kukeond (ConnectContainerTerminal); the last screenshot also runs
// in-process (--run-path promotes KUKEON_NO_DAEMON) to cover that route.
func TestKuke_WriteThenScreenshot(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)

	realmName := generateUniqueRealmName(t)
	spaceName := generateUniqueSpaceName(t)
	stackName := generateUniqueStackName(t)
	cellName := generateUniqueCellName(t)

	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realmName, spaceName, stackName, cellName)
		cleanupStackNoDaemon(t, runPath, realmName, spaceName, stackName)
		cleanupSpaceNoDaemon(t, runPath, realmName, spaceName)
		cleanupRealmNoDaemon(t, runPath, realmName)
	})

	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "space", spaceName, "--realm", realmName)...)
	runReturningBinary(t, nil, kuke,
		append(buildKukeDaemonArgs(host), "create", "stack", stackName,
			"--realm", realmName, "--space", spaceName)...)

	// `work` runs `sh -i`: an interactive shell that echoes typed input and
	// runs a submitted line, so the screen shows both stages.
	applyAttachableCell(t, host, "attachable-shell-cell.yaml",
		realmName, spaceName, stackName, cellName)
	waitForSocket(t,
		fs.ContainerSocketSymlinkPath(runPath, realmName, spaceName, stackName, cellName, "work"),
		10*time.Second)

	scope := []string{"--realm", realmName, "--space", spaceName, "--stack", stackName}
	daemonCmd := func(verb string, rest ...string) []string {
		args := append(buildKukeDaemonArgs(host), verb, cellName)
		return append(append(args, scope...), rest...)
	}

	// The typed form contains "$((6*7))"; only the shell's evaluation
	// prints "[42]", so seeing the latter proves ^M submitted the line.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	typed := fmt.Sprintf("echo W%s[$((6*7))]", nonce)
	ran := fmt.Sprintf("W%s[42]", nonce)

	// Stage 1: caret-free text, no newline added: the line sits typed but
	// unsubmitted.
	runKukeOK(t, nil, daemonCmd("write", typed)...)
	waitForScreen(t, daemonCmd("screenshot"), typed)
	if screen := screenshotText(t, daemonCmd("screenshot")); strings.Contains(screen, ran) {
		t.Fatalf("line ran before ^M was written:\n%s", screen)
	}

	// Stage 2: ^M (caret-expanded) submits it.
	runKukeOK(t, nil, daemonCmd("write", "^M")...)
	waitForScreen(t, daemonCmd("screenshot"), ran)

	// Stage 3: stdin and --raw. stdin carries caret notation (expanded);
	// --raw sends a literal carriage return.
	stdinRan := fmt.Sprintf("S%s[6]", nonce)
	runKukeOK(t, strings.NewReader(fmt.Sprintf("echo S%s[$((2*3))]^M", nonce)), daemonCmd("write", "-")...)
	waitForScreen(t, daemonCmd("screenshot"), stdinRan)
	rawRan := fmt.Sprintf("R%s[9]", nonce)
	runKukeOK(t, nil, daemonCmd("write", "--raw", fmt.Sprintf("echo R%s[$((3*3))]\r", nonce))...)
	waitForScreen(t, daemonCmd("screenshot"), rawRan)

	// -o ansi carries the same grid (sh prints no colors, so the written
	// text is present verbatim); -o json carries the documented schema.
	if ansi := runKukeOK(t, nil, daemonCmd("screenshot", "-o", "ansi")...); !strings.Contains(ansi, rawRan) {
		t.Errorf("-o ansi output missing %q:\n%s", rawRan, ansi)
	}
	var doc struct {
		Cols          int    `json:"cols"`
		Rows          int    `json:"rows"`
		CursorVisible *bool  `json:"cursorVisible"`
		AltScreen     *bool  `json:"altScreen"`
		Text          string `json:"text"`
		ANSI          string `json:"ansi"`
	}
	jsonOut := runKukeOK(t, nil, daemonCmd("screenshot", "-o", "json")...)
	if err := json.Unmarshal([]byte(jsonOut), &doc); err != nil {
		t.Fatalf("-o json is not JSON: %v\n%s", err, jsonOut)
	}
	if doc.Cols <= 0 || doc.Rows <= 0 || doc.CursorVisible == nil || doc.AltScreen == nil {
		t.Errorf("-o json missing size/cursor/altScreen fields: %s", jsonOut)
	}
	if !strings.Contains(doc.Text, rawRan) || !strings.Contains(doc.ANSI, rawRan) {
		t.Errorf("-o json text/ansi missing %q: %s", rawRan, jsonOut)
	}

	// In-process route: --run-path promotes KUKEON_NO_DAEMON, so the
	// screenshot dials the socket itself via the local client.
	inProcess := append(append(buildKukeRunPathArgs(runPath), "screenshot", cellName), scope...)
	if screen := runKukeOK(t, nil, inProcess...); !strings.Contains(screen, rawRan) {
		t.Errorf("in-process screenshot missing %q:\n%s", rawRan, screen)
	}

	// Refusal parity with `kuke attach`: the root container is not
	// Attachable.
	code, _, stderr := runBinary(t, nil, kuke, daemonCmd("screenshot", "--container", "root")...)
	if code == 0 || !strings.Contains(string(stderr), "not attachable") {
		t.Errorf("screenshot --container root: exit %d, stderr %q; want a not-attachable refusal", code, stderr)
	}
}

// runKukeOK runs kuke with optional stdin and returns stdout, failing the
// test on a non-zero exit. Unlike runReturningBinary it accepts empty
// output (`kuke write` prints nothing).
func runKukeOK(t *testing.T, stdin *strings.Reader, args ...string) string {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".."
	}
	bin := filepath.Join(dir, kuke)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = e2eRunEnv(nil, args)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("run %s %v: %v", bin, args, err)
		}
		t.Fatalf("%s %v exited %d\nstdout:\n%s\nstderr:\n%s",
			bin, args, exitErr.ExitCode(), stdout.String(), stderr.String())
	}
	return stdout.String()
}

func screenshotText(t *testing.T, args []string) string {
	t.Helper()
	return runKukeOK(t, nil, args...)
}

// waitForScreen polls `kuke screenshot` until its text contains want.
func waitForScreen(t *testing.T, args []string, want string) {
	t.Helper()
	deadline := time.Now().Add(screenPollTimeout)
	var screen string
	for time.Now().Before(deadline) {
		screen = screenshotText(t, args)
		if strings.Contains(screen, want) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("screen did not show %q within %s; last screenshot:\n%s", want, screenPollTimeout, screen)
}
