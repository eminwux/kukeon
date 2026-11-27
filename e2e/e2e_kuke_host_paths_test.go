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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
)

// hostPathsBlueprint has the host mounts and KUKEON_HOST of a team blueprint
// whose role needs `kukeond-socket` and `kuke-binary`
// (pkg/team/compose/renderers/kukeon/bundled/partials.tmpl.yaml), on a
// busybox container that idles.
const hostPathsBlueprint = `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: host-paths
  realm: %[1]s
  space: %[2]s
  stack: %[3]s
spec:
  cell:
    containers:
      - id: work
        image: docker.io/library/busybox:latest
        command: sh
        args: ["-c", "trap 'exit 0' TERM; sleep 3600 & wait"]
        env: ["KUKEON_HOST=unix:///run/kukeon/kukeond/${KUKEOND_SOCKET_NAME}"]
        volumes:
          - source: ${KUKEOND_SOCKET_DIR}/kukeond
            target: /run/kukeon/kukeond
            readOnly: true
          - source: ${KUKEON_RUN_PATH}/bin/client
            target: /.kukeon/client
            readOnly: true
          - source: ${KUKEON_RUN_PATH}/bin/kuke-shim
            target: /usr/local/bin/kuke
            readOnly: true
---
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: host-paths
  realm: %[1]s
  space: %[2]s
  stack: %[3]s
spec:
  blueprint:
    name: host-paths
    realm: %[1]s
    space: %[2]s
    stack: %[3]s
`

// stageStaticKukeClient puts a kuke client where `kuke init --run-path
// runPath` stages it (<runPath>/bin/client/kuke and the <runPath>/bin/kuke-shim
// exec shim, cmd/kuke/internal/lifecycle/kukeclient.go). It is built static:
// the test image is busybox, which may not carry the build host's libc.
func stageStaticKukeClient(t *testing.T, runPath string) {
	t.Helper()
	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	clientDir := filepath.Join(runPath, "bin", "client")
	if err := os.MkdirAll(clientDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(clientDir, "kuke"), "./cmd/")
	build.Dir = binDir
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build a static kuke client: %v\n%s", err, out)
	}
	shim := []byte("#!/bin/sh\nexec /.kukeon/client/kuke \"$@\"\n")
	if err := os.WriteFile(filepath.Join(runPath, "bin", "kuke-shim"), shim, 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestKuke_ConfigCell_HostPathsFollowDaemon pins #1710: a Config-bound
// cell whose blueprint mounts the daemon's socket directory and staged kuke
// client through the reserved host-path variables gets this daemon's run
// path and socket, not the standard /opt/kukeon and /run/kukeon paths. The
// in-cell kuke then reaches the daemon that started the cell: on a host with
// nothing at the standard paths the old literal mounts did not start, and
// on a host where another daemon owns them (a kukeon dev cell) they reached
// that daemon, which does not know this cell.
func TestKuke_ConfigCell_HostPathsFollowDaemon(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	socket := strings.TrimPrefix(host, "unix://")
	stageStaticKukeClient(t, runPath)
	realm := generateUniqueRealmName(t)
	space := generateUniqueSpaceName(t)
	stack := generateUniqueStackName(t)
	cell := generateUniqueCellName(t)
	daemon := buildKukeDaemonArgs(host)
	scope := []string{"--realm", realm, "--space", space, "--stack", stack}
	t.Cleanup(func() {
		cleanupCell(t, host, realm, space, stack, cell)
		cleanupStack(t, host, realm, space, stack)
		cleanupSpace(t, host, realm, space)
		cleanupRealm(t, host, realm)
	})

	runReturningBinary(t, nil, kuke, append(daemon, "create", "realm", realm)...)
	runReturningBinary(t, nil, kuke, append(daemon, "create", "space", space, "--realm", realm)...)
	runReturningBinary(t, nil, kuke,
		append(daemon, "create", "stack", stack, "--realm", realm, "--space", space)...)
	manifest := filepath.Join(t.TempDir(), "host-paths.yaml")
	if err := os.WriteFile(manifest, []byte(fmt.Sprintf(hostPathsBlueprint, realm, space, stack)), 0o600); err != nil {
		t.Fatal(err)
	}
	runReturningBinary(t, nil, kuke, append(daemon, "apply", "-f", manifest)...)
	runReturningBinary(t, nil, kuke,
		append(append(daemon, "create", "cell", cell, "--from-config", "host-paths"), scope...)...)
	runReturningBinary(t, nil, kuke, append(append(daemon, "start", cell), scope...)...)

	out := runReturningBinary(
		t,
		nil,
		kuke,
		append(append(daemon, "get", "cell", cell), append(scope, "-o", "json")...)...)
	doc, err := parseCellJSON(t, out)
	if err != nil {
		t.Fatalf("parse cell JSON: %v", err)
	}
	wantSources := map[string]string{
		"/run/kukeon/kukeond": filepath.Join(filepath.Dir(socket), consts.KukeondSocketShareSubdir),
		"/.kukeon/client":     filepath.Join(runPath, "bin", "client"),
		"/usr/local/bin/kuke": filepath.Join(runPath, "bin", "kuke-shim"),
	}
	wantHost := "KUKEON_HOST=unix:///run/kukeon/kukeond/" + filepath.Base(socket)
	for _, c := range doc.Spec.Containers {
		if c.ID != "work" {
			continue
		}
		for _, v := range c.Volumes {
			if want, ok := wantSources[v.Target]; ok && v.Source != want {
				t.Errorf("mount %s: source %q, want %q", v.Target, v.Source, want)
			}
		}
		if !strings.Contains(strings.Join(c.Env, "\n"), wantHost) {
			t.Errorf("env %q lacks %s", c.Env, wantHost)
		}
	}

	code, stdout, stderr := runBinary(t, nil, kuke, append(append(append(daemon, "exec", cell), scope...),
		append([]string{"-c", "work", "--", "kuke", "get", "cell", cell, "-o", "json"}, scope...)...)...)
	t.Logf("kuke get cell in the cell: exit %d\n%s%s", code, stdout, stderr)
	if code != 0 || !strings.Contains(string(stdout), cell) {
		t.Fatalf("the cell's kuke does not reach the daemon that started it: exit %d\n%s%s", code, stdout, stderr)
	}
}
