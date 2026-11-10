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

package e2e_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestKuke_EnvChangesReachTheRunningProcess verifies Config-lineage, direct
// Cell, and standalone Container updates against the process environment.
// Only the changed workload container should be replaced: the root snapshot
// and an unrelated sidecar must remain intact. Regression for #1368.
func TestKuke_EnvChangesReachTheRunningProcess(t *testing.T) {
	runPath := getRandomRunPath(t)
	host := startKukeondDaemonWithReconcileInterval(t, runPath, "100ms")
	realm := generateUniqueRealmName(t)
	space := generateUniqueSpaceName(t)
	stack := generateUniqueStackName(t)
	configCell := generateUniqueCellName(t)
	directCell := generateUniqueCellName(t)
	standaloneCell := generateUniqueCellName(t)
	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realm, space, stack, configCell)
		cleanupCellNoDaemon(t, runPath, realm, space, stack, directCell)
		cleanupCellNoDaemon(t, runPath, realm, space, stack, standaloneCell)
		cleanupStackNoDaemon(t, runPath, realm, space, stack)
		cleanupSpaceNoDaemon(t, runPath, realm, space)
		cleanupRealmNoDaemon(t, runPath, realm)
	})

	invoke := func(args ...string) []byte {
		t.Helper()
		return runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), args...)...)
	}
	apply := func(name, manifest string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		invoke("apply", "-f", path)
	}
	getCell := func(name string) *v1beta1.CellDoc {
		t.Helper()
		output := invoke("get", "cell", name, "--realm", realm, "--space", space, "--stack", stack, "-o", "json")
		cell, err := parseCellJSON(t, output)
		if err != nil {
			t.Fatalf("parse cell %q: %v", name, err)
		}
		return cell
	}

	invoke("create", "realm", realm)
	invoke("create", "space", space, "--realm", realm)
	invoke("create", "stack", stack, "--realm", realm, "--space", space)

	blueprint := func(defaultMSG string) string {
		return fmt.Sprintf(`apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: recipe
  realm: %s
  space: %s
  stack: %s
spec:
  parameters:
    - name: MSG
      default: %s
  cell:
    containers:
      - id: main
        image: docker.io/library/busybox:latest
        command: /bin/sh
        attachable: true
        env: ["MSG=${MSG}", "EXTRA=from-config"]
      - id: sidecar
        image: docker.io/library/busybox:latest
        command: sleep
        args: ["3600"]
`, realm, space, stack, defaultMSG)
	}
	config := func(msg string) string {
		return fmt.Sprintf(`apiVersion: v1beta1
kind: CellConfig
metadata:
  name: binding
  realm: %s
  space: %s
  stack: %s
spec:
  blueprint:
    name: recipe
    realm: %s
    space: %s
    stack: %s
  values:
    MSG: %s
`, realm, space, stack, realm, space, stack, msg)
	}

	apply("config-lineage.yaml", blueprint("v1")+"---\n"+config("v1"))
	scope := []string{"--realm", realm, "--space", space, "--stack", stack}
	invoke(
		append([]string{"create", "cell", configCell, "--from-config", "binding", "--env", "EXTRA=kept"}, scope...)...)
	invoke(append([]string{"start", configCell}, scope...)...)
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, configCell, "v1:kept")

	configBefore := getCell(configCell)
	configRecordsBefore := workloadRuntimeRecords(t, realm, configBefore)
	apply("config-update.yaml", config("v2"))
	waitCellOutOfSync(t, getCell, configCell)
	invoke(append([]string{"restart", configCell}, scope...)...)
	configAfter := getCell(configCell)
	assertOnlyWorkloadRecreated(t, configRecordsBefore, workloadRuntimeRecords(t, realm, configAfter))
	assertContainerEnvironment(t, configAfter, "main", "MSG=v2", "EXTRA=kept")
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, configCell, "v2:kept")

	makeDirectCell := func(name, msg string) string {
		return fmt.Sprintf(`apiVersion: v1beta1
kind: Cell
metadata:
  name: %s
spec:
  id: %s
  realmId: %s
  spaceId: %s
  stackId: %s
  containers:
    - id: root
      root: true
      image: docker.io/library/busybox:latest
      command: sleep
      args: ["3600"]
    - id: main
      image: docker.io/library/busybox:latest
      command: /bin/sh
      attachable: true
      env: ["MSG=%s", "EXTRA=kept"]
    - id: sidecar
      image: docker.io/library/busybox:latest
      command: sleep
      args: ["3600"]
`, name, name, realm, space, stack, msg)
	}
	apply("direct-cell.yaml", makeDirectCell(directCell, "v1"))
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, directCell, "v1:kept")
	directBefore := getCell(directCell)
	directRecordsBefore := workloadRuntimeRecords(t, realm, directBefore)
	apply("direct-cell-update.yaml", makeDirectCell(directCell, "v2"))
	directAfter := getCell(directCell)
	assertOnlyWorkloadRecreated(t, directRecordsBefore, workloadRuntimeRecords(t, realm, directAfter))
	assertContainerEnvironment(t, directAfter, "main", "MSG=v2", "EXTRA=kept")
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, directCell, "v2:kept")

	invoke(append([]string{"restart", directCell}, scope...)...)
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, directCell, "v2:kept")

	makeStandaloneContainer := func(msg string) string {
		return fmt.Sprintf(`apiVersion: v1beta1
kind: Container
metadata:
  name: main
  labels: {}
spec:
  id: main
  realmId: %s
  spaceId: %s
  stackId: %s
  cellId: %s
  image: docker.io/library/busybox:latest
  command: /bin/sh
  args: []
  attachable: true
  env: ["MSG=%s", "EXTRA=kept"]
  ports: []
  volumes: []
  networks: []
  networksAliases: []
  privileged: false
  restartPolicy: ""
`, realm, space, stack, standaloneCell, msg)
	}
	apply("standalone-container-cell.yaml", makeDirectCell(standaloneCell, "v1"))
	standaloneSocket := fs.ContainerSocketSymlinkPath(runPath, realm, space, stack, standaloneCell, "main")
	waitForSocket(t, standaloneSocket, 10*time.Second)
	standaloneAttachArgs := append(buildKukeDaemonArgs(host), "attach", standaloneCell,
		"--realm", realm, "--space", space, "--stack", stack)
	standaloneSession := startPTY(t, nil, kuke, standaloneAttachArgs...)
	t.Cleanup(standaloneSession.Close)
	time.Sleep(attachConnectGrace)
	if err := standaloneSession.Write([]byte("printf 'ISSUE1368_ENV:%s:%s\\n' \"$MSG\" \"${EXTRA-unset}\"\n")); err != nil {
		t.Fatalf("write standalone container environment probe: %v", err)
	}
	if err := standaloneSession.WaitForOutput([]byte("ISSUE1368_ENV:v1:kept"), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	standaloneBefore := getCell(standaloneCell)
	assertContainerState(t, standaloneBefore, "main", v1beta1.ContainerStateReady)
	standaloneRecordsBefore := workloadRuntimeRecords(t, realm, standaloneBefore)
	apply("standalone-container-update.yaml", makeStandaloneContainer("v2"))
	standaloneSession.Close()
	standaloneAfter := getCell(standaloneCell)
	assertOnlyWorkloadRecreated(t, standaloneRecordsBefore, workloadRuntimeRecords(t, realm, standaloneAfter))
	assertContainerState(t, standaloneAfter, "main", v1beta1.ContainerStateReady)
	assertContainerEnvironment(t, standaloneAfter, "main", "MSG=v2", "EXTRA=kept")
	assertAttachedEnvironment(t, host, runPath, realm, space, stack, standaloneCell, "v2:kept")
}

func waitCellOutOfSync(t *testing.T, getCell func(string) *v1beta1.CellDoc, name string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if getCell(name).Status.OutOfSync {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("cell %q did not become OutOfSync after its Config changed", name)
}

func assertAttachedEnvironment(
	t *testing.T,
	host, runPath, realm, space, stack, cell, want string,
) {
	t.Helper()
	socket := fs.ContainerSocketSymlinkPath(runPath, realm, space, stack, cell, "main")
	waitForSocket(t, socket, 10*time.Second)
	args := append(buildKukeDaemonArgs(host), "attach", cell,
		"--realm", realm, "--space", space, "--stack", stack)
	session := startPTY(t, nil, kuke, args...)
	t.Cleanup(session.Close)
	time.Sleep(attachConnectGrace)
	if err := session.Write([]byte("printf 'ISSUE1368_ENV:%s:%s\\n' \"$MSG\" \"${EXTRA-unset}\"\n")); err != nil {
		t.Fatalf("write attached environment probe: %v", err)
	}
	if err := session.WaitForOutput([]byte("ISSUE1368_ENV:"+want), 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := session.Write(sbshDetachSequence); err != nil {
		t.Fatalf("detach from %q: %v", cell, err)
	}
	if code, output, err := session.Wait(attachExitTimeout); err != nil || code != 0 {
		t.Fatalf("detach from %q: code=%d err=%v output=%s", cell, code, err, output)
	}
}

type envRuntimeRecord struct {
	SnapshotKey string `json:"SnapshotKey"`
	CreatedAt   string `json:"CreatedAt"`
}

func workloadRuntimeRecords(t *testing.T, realm string, cell *v1beta1.CellDoc) map[string]envRuntimeRecord {
	t.Helper()
	records := make(map[string]envRuntimeRecord, 3)
	for _, id := range []string{"root", "main", "sidecar"} {
		containerID := cellContainerdID(t, cell, id)
		output, err := exec.Command("ctr", "--namespace", realm+".kukeon.io", "containers", "info", containerID).
			CombinedOutput()
		if err != nil {
			t.Fatalf("container info %q: %v: %s", containerID, err, output)
		}
		var record envRuntimeRecord
		if decodeErr := json.Unmarshal(output, &record); decodeErr != nil {
			t.Fatalf("parse container info %q: %v: %s", containerID, decodeErr, output)
		}
		records[id] = record
	}
	return records
}

func cellContainerdID(t *testing.T, cell *v1beta1.CellDoc, id string) string {
	t.Helper()
	for _, container := range cell.Spec.Containers {
		if container.ID == id {
			if container.ContainerdID == "" {
				t.Fatalf("container %q has no containerd ID", id)
			}
			return container.ContainerdID
		}
	}
	t.Fatalf("cell %q has no container %q", cell.Metadata.Name, id)
	return ""
}

func assertContainerEnvironment(t *testing.T, cell *v1beta1.CellDoc, id string, expected ...string) {
	t.Helper()
	for _, container := range cell.Spec.Containers {
		if container.ID != id {
			continue
		}
		for _, want := range expected {
			found := false
			for _, env := range container.Env {
				if env == want {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("container %q env = %v, missing %q", id, container.Env, want)
			}
		}
		return
	}
	t.Fatalf("cell %q has no container %q", cell.Metadata.Name, id)
}

func assertContainerState(t *testing.T, cell *v1beta1.CellDoc, id string, want v1beta1.ContainerState) {
	t.Helper()
	for _, container := range cell.Status.Containers {
		if container.ID == id {
			if container.State != want {
				t.Fatalf("container %q state = %v, want %v", id, container.State, want)
			}
			return
		}
	}
	t.Fatalf("cell %q has no status for container %q", cell.Metadata.Name, id)
}

func assertOnlyWorkloadRecreated(
	t *testing.T,
	before, after map[string]envRuntimeRecord,
) {
	t.Helper()
	for _, id := range []string{"root", "sidecar"} {
		if before[id] != after[id] {
			t.Errorf("unrelated %s container changed: before=%+v after=%+v", id, before[id], after[id])
		}
	}
	if before["main"].CreatedAt == after["main"].CreatedAt {
		t.Errorf("main container was not recreated: before=%+v after=%+v", before["main"], after["main"])
	}
}
