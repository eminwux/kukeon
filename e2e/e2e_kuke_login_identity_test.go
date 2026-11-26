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
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestKuke_LoginIdentity verifies login identities end to end against a real
// daemon (issue #1685): a Volume tagged with a connector reads back its tag;
// its login status follows a marker file the test fakes — `empty`, then
// `logged in` — without the daemon reading it; `kuke connector list` and
// `kuke get volumes -o wide` list it; a Config that logs in through it reports
// it; a Volume tagged for another connector is refused as a state volume; and
// a login identity Configs of another role already use is refused with an
// error naming both roles. Nothing here starts a container, so no image is
// pulled.
//
//nolint:gocyclo,cyclop // one login identity followed through a flat list of independent checks.
func TestKuke_LoginIdentity(t *testing.T) {
	t.Parallel()

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realm := generateUniqueRealmName(t)
	space := generateUniqueSpaceName(t)
	stack := generateUniqueStackName(t)
	t.Cleanup(func() {
		cleanupStackNoDaemon(t, runPath, realm, space, stack)
		cleanupSpaceNoDaemon(t, runPath, realm, space)
		cleanupRealmNoDaemon(t, runPath, realm)
	})

	invoke := func(args ...string) []byte {
		t.Helper()
		return runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), args...)...)
	}
	try := func(args ...string) (int, string) {
		t.Helper()
		code, stdout, stderr := runBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), args...)...)
		return code, string(stdout) + string(stderr)
	}
	apply := func(name, manifest string) {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		invoke("apply", "-f", path)
	}
	scope := []string{"--realm", realm, "--space", space, "--stack", stack}
	getVolume := func(name string) v1beta1.VolumeDoc {
		t.Helper()
		var doc v1beta1.VolumeDoc
		out := invoke(append([]string{"get", "volume", name, "-o", "json"}, scope...)...)
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("parse volume %q: %v\n%s", name, err, out)
		}
		return doc
	}

	invoke("create", "realm", realm)
	invoke("create", "space", space, "--realm", realm)
	invoke("create", "stack", stack, "--realm", realm, "--space", space)

	// A login identity: a Volume tagged with its connector, and the marker
	// whose presence means it holds a login.
	const volume, marker, loginContent = "dev-state", "auth.json", "login-content-canary"
	apply("volume.yaml", fmt.Sprintf(`apiVersion: v1beta1
kind: Volume
metadata:
  name: %s
  realm: %s
  space: %s
  stack: %s
spec:
  reclaimPolicy: Retain
  connector: agent
  method: login
  loginMarker: %s
`, volume, realm, space, stack, marker))

	got := getVolume(volume)
	if got.Spec.Connector != "agent" || got.Spec.Method != "login" || got.Spec.LoginMarker != marker ||
		got.Spec.ReclaimPolicy != v1beta1.ReclaimRetain {
		t.Fatalf("volume spec = %+v, want the agent login tag and Retain", got.Spec)
	}
	if got.Status.Login == nil || got.Status.Login.State != v1beta1.LoginStateEmpty {
		t.Fatalf("login before the marker exists = %+v, want empty", got.Status.Login)
	}

	// Fake the login: the daemon only checks that the marker exists.
	markerPath := filepath.Join(fs.VolumePath(runPath, realm, space, stack, volume), marker)
	if err := os.WriteFile(markerPath, []byte(loginContent), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	got = getVolume(volume)
	if got.Status.Login == nil || got.Status.Login.State != v1beta1.LoginStateLoggedIn || got.Status.Login.Since == "" {
		t.Fatalf("login with the marker = %+v, want logged in with a time", got.Status.Login)
	}

	wide := string(invoke(append([]string{"get", "volumes", "-o", "wide"}, scope...)...))
	for _, want := range []string{"CONNECTOR", "agent", "login", v1beta1.LoginStateLoggedIn} {
		if !strings.Contains(wide, want) {
			t.Errorf("get volumes -o wide lacks %q:\n%s", want, wide)
		}
	}

	// A blueprint whose connectors keep a login in the state volume, as
	// `kuke team` renders one, and Configs of two roles.
	apply("blueprint.yaml", fmt.Sprintf(`apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: agent
  realm: %[1]s
  space: %[2]s
  stack: %[3]s
spec:
  parameters:
    - name: STATE_VOLUME
    - name: STATE_VOLUME_REALM
    - name: STATE_VOLUME_SPACE
    - name: STATE_VOLUME_STACK
  components:
    - kind: connector
      name: agent-login
      optional: true
      group: auth
      connector:
        name: agent
        method: login
        delivery: stateVolume
        secretTypes: [opaque]
        harness: agent
        login:
          marker: %[4]s
    - kind: connector
      name: other-login
      optional: true
      group: auth
      connector:
        name: other
        method: login
        delivery: stateVolume
        secretTypes: [opaque]
        harness: other
  cell:
    containers:
      - id: main
        image: docker.io/library/busybox:latest
        command: /bin/sh
        attachable: true
        volumes:
          - kind: volume
            volumeRef:
              name: "${STATE_VOLUME}"
              realm: "${STATE_VOLUME_REALM}"
              space: "${STATE_VOLUME_SPACE}"
              stack: "${STATE_VOLUME_STACK}"
            target: /home/user/.agent
            ensure: true
`, realm, space, stack, marker))
	configManifest := func(name, role, component string) string {
		labels := ""
		if role != "" {
			labels = fmt.Sprintf("  labels:\n    kukeon.io/team: e2e\n    kukeon.io/role: %s\n", role)
		}
		return fmt.Sprintf(`apiVersion: v1beta1
kind: CellConfig
metadata:
  name: %[1]s
  realm: %[2]s
  space: %[3]s
  stack: %[4]s
%[5]sspec:
  blueprint:
    name: agent
    realm: %[2]s
    space: %[3]s
    stack: %[4]s
  components: [%[6]s]
  values:
    STATE_VOLUME: %[7]s
    STATE_VOLUME_REALM: %[2]s
    STATE_VOLUME_SPACE: %[3]s
    STATE_VOLUME_STACK: %[4]s
`, name, realm, space, stack, labels, component, volume)
	}
	config := func(name, role, component string) {
		t.Helper()
		apply(name+".yaml", configManifest(name, role, component))
	}
	getConfig := func(name string) v1beta1.CellConfigDoc {
		t.Helper()
		var doc v1beta1.CellConfigDoc
		out := invoke(append([]string{"get", "config", name, "-o", "json"}, scope...)...)
		if err := json.Unmarshal(out, &doc); err != nil {
			t.Fatalf("parse config %q: %v\n%s", name, err, out)
		}
		return doc
	}
	dryRun := func(cfg string) (int, string) {
		t.Helper()
		return try(append([]string{"create", "cell", "probe", "--from-config", cfg, "--dry-run"}, scope...)...)
	}

	// One role: the Config reports its login identity and its login state,
	// and renders.
	config("dev", "dev", "agent-login")
	status := getConfig("dev").Status
	if status == nil || len(status.Connectors) != 1 {
		t.Fatalf("config status = %+v, want one connector", status)
	}
	login := status.Connectors[0]
	if login.State != v1beta1.ConnectorResolved || login.VolumeRef == nil || login.VolumeRef.Name != volume ||
		login.Login == nil || login.Login.State != v1beta1.LoginStateLoggedIn {
		t.Fatalf("connector status = %+v (login %+v), want resolved on %s, logged in", login, login.Login, volume)
	}
	if code, out := dryRun("dev"); code != 0 {
		t.Fatalf("dry-run of the first role failed (exit %d):\n%s", code, out)
	}

	// The list names the identity, its status and the Config using it, and
	// never the content of the login.
	list := string(invoke("connector", "list"))
	row := ""
	for _, line := range strings.Split(list, "\n") {
		if strings.Contains(line, volume) {
			row = strings.Join(strings.Fields(line), " ")
		}
	}
	wantRow := strings.Join(
		[]string{"agent", volume, realm + "/" + space + "/" + stack, "login", "logged in", "dev"},
		" ",
	)
	if row != wantRow {
		t.Errorf("connector list row = %q, want %q\n%s", row, wantRow, list)
	}
	for _, out := range []string{list, wide} {
		if strings.Contains(out, loginContent) {
			t.Errorf("an output carries the content of the login:\n%s", out)
		}
	}

	// A second Config of the same role shares the identity; one without a
	// role does not conflict either.
	config("dev-task", "dev", "agent-login")
	if code, out := dryRun("dev-task"); code != 0 {
		t.Errorf("a Config of the same role was refused (exit %d):\n%s", code, out)
	}

	// Another role binding the same login identity is refused when its Config
	// is applied, naming both roles — so nothing is stored, and the role that
	// already uses the identity keeps rendering.
	pmPath := filepath.Join(t.TempDir(), "pm.yaml")
	if err := os.WriteFile(pmPath, []byte(configManifest("pm", "pm", "agent-login")), 0o600); err != nil {
		t.Fatalf("write pm.yaml: %v", err)
	}
	code, out := try("apply", "-f", pmPath)
	if code == 0 {
		t.Fatalf("a Config of another role was stored on the same login identity:\n%s", out)
	}
	for _, want := range []string{"e2e/dev", "e2e/pm", volume} {
		if !strings.Contains(out, want) {
			t.Errorf("role guard error does not mention %q:\n%s", want, out)
		}
	}
	if code, out = dryRun("dev"); code != 0 {
		t.Errorf("the first role stopped rendering after the refused apply (exit %d):\n%s", code, out)
	}

	// A Volume tagged for one connector is refused as the state volume of
	// another.
	config("stranger", "", "other-login")
	code, out = dryRun("stranger")
	if code == 0 || !strings.Contains(out, `"agent"`) || !strings.Contains(out, `"other"`) {
		t.Errorf(
			"a volume of another connector was accepted, or the error names no connector (exit %d):\n%s",
			code,
			out,
		)
	}

	// Ephemeral harness state cannot be combined with a login method.
	code, out = try(
		append(
			[]string{"create", "cell", "probe", "--from-config", "dev", "--state", "ephemeral", "--dry-run"},
			scope...)...)
	if code == 0 || !strings.Contains(out, "would not survive") {
		t.Errorf("--state ephemeral with a login method was accepted, or does not say why (exit %d):\n%s", code, out)
	}
}
