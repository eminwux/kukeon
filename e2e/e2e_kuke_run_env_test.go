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
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestKuke_RunRuntimeEnvReuse checks the actual attachable shell, rather than
// only the OCI record: every run gets its own env while the writable snapshot
// and authored metadata remain intact. Regression for #1369.
func TestKuke_RunRuntimeEnvReuse(t *testing.T) {
	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realm, space, stack, cell := generateUniqueRealmName(
		t,
	), generateUniqueSpaceName(
		t,
	), generateUniqueStackName(
		t,
	), generateUniqueCellName(
		t,
	)
	t.Cleanup(func() {
		cleanupCellNoDaemon(t, runPath, realm, space, stack, cell)
		cleanupStackNoDaemon(t, runPath, realm, space, stack)
		cleanupSpaceNoDaemon(t, runPath, realm, space)
		cleanupRealmNoDaemon(t, runPath, realm)
	})
	invoke := func(args ...string) []byte {
		t.Helper()
		return runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), args...)...)
	}
	invoke("create", "realm", realm)
	invoke("create", "space", space, "--realm", realm)
	invoke("create", "stack", stack, "--realm", realm, "--space", space)
	fixture := filepath.Join(t.TempDir(), "runtime-env.yaml")
	doc := fmt.Sprintf(`apiVersion: v1beta1
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
    - id: work
      image: docker.io/library/busybox:latest
      command: /bin/sh
      attachable: true
      env: ["VALUE=spec"]
`, cell, cell, realm, space, stack)
	if err := os.WriteFile(fixture, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	scope := []string{"--realm", realm, "--space", space, "--stack", stack}
	var baselineSpec any
	type runtimeRecord struct {
		SnapshotKey string            `json:"SnapshotKey"`
		Labels      map[string]string `json:"Labels"`
		CreatedAt   string            `json:"CreatedAt"`
	}
	var baselineRecord runtimeRecord
	for i, expected := range []string{"first", "second", "spec"} {
		args := []string{"run", cell, "-d"}
		if i == 0 {
			args = []string{"run", "-f", fixture, "-d"}
		}
		if i < 2 {
			args = append(args, "--env", "VALUE="+expected, "--env", "RUNTIME_ONLY="+expected)
		}
		invoke(append(args, scope...)...)
		output := invoke(append([]string{"get", "cell", cell, "-o", "json"}, scope...)...)
		authored, err := parseCellJSON(t, output)
		if err != nil {
			t.Fatal(err)
		}
		// Compare the entire authored spec, including env, after every invocation.
		var raw map[string]any
		if err = json.Unmarshal(output, &raw); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			baselineSpec = raw["spec"]
		} else if !reflect.DeepEqual(baselineSpec, raw["spec"]) {
			t.Fatalf("run changed persisted spec: %s", output)
		}
		for _, c := range authored.Spec.Containers {
			if c.ID != "work" {
				continue
			}
			if !reflect.DeepEqual(c.Env, []string{"VALUE=spec"}) {
				t.Fatalf("runtime env persisted: %v", c.Env)
			}
			ctrID := c.ContainerdID
			info, infoErr := exec.Command("ctr", "--namespace", realm+".kukeon.io", "containers", "info", ctrID).
				CombinedOutput()
			if infoErr != nil {
				t.Fatalf("container info: %v: %s", infoErr, info)
			}
			var record runtimeRecord
			if err = json.Unmarshal(info, &record); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				baselineRecord = record
			} else if !reflect.DeepEqual(record, baselineRecord) {
				t.Fatalf("runtime record replaced or labels changed: %s", info)
			}
		}
		waitForSocket(t, fs.ContainerSocketSymlinkPath(runPath, realm, space, stack, cell, "work"), 10*time.Second)
		session := startPTY(t, nil, kuke, append(append(buildKukeDaemonArgs(host), "attach", cell), scope...)...)
		t.Cleanup(session.Close)
		time.Sleep(attachConnectGrace)
		command := "printf 'ENV_RESULT:%s:%s\\n' \"$VALUE\" \"${RUNTIME_ONLY-unset}\"; "
		if i == 0 {
			command += "printf kept > /tmp/runtime-env-overlay; "
		}
		command += "printf 'OVERLAY_RESULT:%s\\n' \"$(cat /tmp/runtime-env-overlay)\"\n"
		if err = session.Write([]byte(command)); err != nil {
			t.Fatal(err)
		}
		runtimeOnly := expected
		if i == 2 {
			runtimeOnly = "unset"
		}
		if err = session.WaitForOutput([]byte("ENV_RESULT:"+expected+":"+runtimeOnly), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err = session.WaitForOutput([]byte("OVERLAY_RESULT:kept"), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if err = session.Write(sbshDetachSequence); err != nil {
			t.Fatal(err)
		}
		code, out, err := session.Wait(attachExitTimeout)
		if err != nil || code != 0 {
			t.Fatalf("detach: code=%d err=%v output=%s", code, err, out)
		}
		invoke(append([]string{"stop", cell}, scope...)...)
	}
}
