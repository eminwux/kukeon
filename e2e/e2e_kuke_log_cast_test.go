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
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestKuke_LogDecodesAsciicastCapture is the e2e scenario for issue #1783's
// recording: an Attachable cell's capture is an asciicast v2 file,
// capture.cast, and `kuke log` prints the terminal output it holds — a
// multi-byte character exactly, and no JSON — through kukeond and in-process
// (KUKEON_NO_DAEMON) alike.
func TestKuke_LogDecodesAsciicastCapture(t *testing.T) {
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

	// The shell prints "─" (U+2500, three bytes) and "é" (two bytes) from
	// octal escapes, so only the output — not the echoed command line —
	// holds the characters themselves.
	nonce := strconv.FormatInt(time.Now().UnixNano(), 10)
	want := fmt.Sprintf("M%s─é─", nonce)
	// --raw sends the backslashes to the shell unchanged.
	runKukeOK(t, nil, daemonCmd("write", "--raw",
		fmt.Sprintf(`printf 'M%s\342\224\200\303\251\342\224\200\n'`, nonce)+"\r")...)
	waitForScreen(t, daemonCmd("screenshot"), want)

	capturePath := fs.ContainerCapturePath(runPath, realmName, spaceName, stackName, cellName, "work")
	recording, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatalf("read capture %s: %v", capturePath, err)
	}
	if !bytes.HasPrefix(recording, []byte(`{"version":2`)) {
		t.Fatalf("capture %s does not start with an asciicast v2 header:\n%.200s", capturePath, recording)
	}

	// The output may reach the recording a moment after the screen.
	var logged string
	deadline := time.Now().Add(screenPollTimeout)
	for time.Now().Before(deadline) {
		logged = runKukeOK(t, nil, daemonCmd("log")...)
		if strings.Contains(logged, want) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !strings.Contains(logged, want) {
		t.Fatalf("kuke log lacks %q:\n%s", want, logged)
	}
	if strings.Contains(logged, `"version"`) || strings.Contains(logged, `,"o","`) {
		t.Fatalf("kuke log printed the recording's JSON:\n%s", logged)
	}

	inProcess := append(append(buildKukeRunPathArgs(runPath), "log", cellName), scope...)
	if local := runKukeOK(t, nil, inProcess...); !strings.Contains(local, want) {
		t.Errorf("in-process kuke log lacks %q:\n%s", want, local)
	}
}
