//go:build !integration

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

//nolint:testpackage // tests exercise private methods on *Exec
package runner

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/cni"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// newProvisionTestExec builds a minimal *Exec suitable for exercising
// provision-path helpers that touch the filesystem but not containerd.
func newProvisionTestExec(t *testing.T, runPath string, forceRegenerateCNI bool) *Exec {
	t.Helper()
	opts := Options{
		RunPath: runPath,
		CniConf: cni.Conf{
			CniConfigDir: filepath.Join(runPath, "cni", "config"),
			CniCacheDir:  filepath.Join(runPath, "cni", "cache"),
			CniBinDir:    filepath.Join(runPath, "cni", "bin"),
		},
		ForceRegenerateCNI: forceRegenerateCNI,
	}
	return &Exec{
		ctx:     context.Background(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:    opts,
		cniConf: &opts.CniConf,
	}
}

// writeStaleConflist writes a pre-SafeBridgeName conflist at the expected
// space-network-config path so ensureSpaceCNIConfig has something to self-heal.
// The realm/space name pair chosen by the caller must be long enough to force
// SafeBridgeName to truncate, so the "stale" bridge can differ from the fixed one.
func writeStaleConflist(t *testing.T, runPath, realmName, spaceName, staleBridge string) string {
	t.Helper()
	confPath, err := fs.SpaceNetworkConfigPath(runPath, realmName, spaceName)
	if err != nil {
		t.Fatalf("SpaceNetworkConfigPath: %v", err)
	}
	if mkErr := os.MkdirAll(filepath.Dir(confPath), 0o750); mkErr != nil {
		t.Fatalf("mkdir conflist parent: %v", mkErr)
	}
	// Deliberately hand-rolled to mirror what a pre-fix release wrote: the
	// bridge field carries the raw network name, which exceeds IFNAMSIZ-1.
	contents := `{
  "cniVersion": "0.4.0",
  "name": "` + realmName + `-` + spaceName + `",
  "plugins": [
    {
      "type": "bridge",
      "bridge": "` + staleBridge + `",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "ranges": [[{"subnet": "10.22.0.0/16"}]],
        "routes": [{"dst": "0.0.0.0/0"}]
      }
    },
    {"type": "loopback"}
  ]
}`
	if wErr := os.WriteFile(confPath, []byte(contents), 0o600); wErr != nil {
		t.Fatalf("write stale conflist: %v", wErr)
	}
	return confPath
}

func TestEnsureSpaceCNIConfig_HealsStaleBridgeName(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	// Pick names whose concatenation exceeds IFNAMSIZ so SafeBridgeName will
	// produce a hashed bridge, making the stale → fresh transition observable.
	const realmName = "kuke-system"
	const spaceName = "kukeon"
	staleBridge := realmName + "-" + spaceName // 18 chars — the pre-fix bug
	confPath := writeStaleConflist(t, runPath, realmName, spaceName, staleBridge)

	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		t.Fatalf("cni.NewManager: %v", err)
	}
	if got, rErr := mgr.ReadBridgeName(confPath); rErr != nil || got != staleBridge {
		t.Fatalf("precondition: expected stale bridge %q on disk, got %q (err=%v)", staleBridge, got, rErr)
	}

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	}
	if _, err = r.ensureSpaceCNIConfig(space); err != nil {
		t.Fatalf("ensureSpaceCNIConfig: %v", err)
	}

	got, err := mgr.ReadBridgeName(confPath)
	if err != nil {
		t.Fatalf("ReadBridgeName after ensure: %v", err)
	}
	want := cni.SafeBridgeName(realmName + "-" + spaceName)
	if got != want {
		t.Errorf("bridge after self-heal = %q, want %q", got, want)
	}
	if len(got) > 15 {
		t.Errorf("bridge after self-heal is %d chars, exceeds IFNAMSIZ-1", len(got))
	}
}

func TestEnsureSpaceCNIConfig_LeavesMatchingConflistAlone(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	const realmName = "kuke-system"
	const spaceName = "kukeon"
	correctBridge := cni.SafeBridgeName(realmName + "-" + spaceName)
	confPath := writeStaleConflist(t, runPath, realmName, spaceName, correctBridge)

	before, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	}
	if _, err = r.ensureSpaceCNIConfig(space); err != nil {
		t.Fatalf("ensureSpaceCNIConfig: %v", err)
	}

	after, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("conflist was rewritten despite matching bridge name\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestEnsureSpaceCNIConfig_ForceRegenerateCNIOverwrites(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, true)

	const realmName = "kuke-system"
	const spaceName = "kukeon"
	correctBridge := cni.SafeBridgeName(realmName + "-" + spaceName)
	confPath := writeStaleConflist(t, runPath, realmName, spaceName, correctBridge)

	before, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	}
	if _, err = r.ensureSpaceCNIConfig(space); err != nil {
		t.Fatalf("ensureSpaceCNIConfig: %v", err)
	}

	after, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	// The rewritten conflist uses BuildDefaultConflist, which emits pretty-printed
	// JSON different from our hand-rolled test fixture — so forcing a regenerate
	// must change the bytes on disk even though the bridge name already matched.
	if string(before) == string(after) {
		t.Error("ForceRegenerateCNI did not rewrite conflist when bridge name matched")
	}
}

func TestEnsureSpaceCNIConfig_CreatesWhenMissing(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	const realmName = "kuke-system"
	const spaceName = "kukeon"

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	}
	if _, err := r.ensureSpaceCNIConfig(space); err != nil {
		t.Fatalf("ensureSpaceCNIConfig: %v", err)
	}

	confPath, err := fs.SpaceNetworkConfigPath(runPath, realmName, spaceName)
	if err != nil {
		t.Fatalf("SpaceNetworkConfigPath: %v", err)
	}
	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		t.Fatalf("cni.NewManager: %v", err)
	}
	got, err := mgr.ReadBridgeName(confPath)
	if err != nil {
		t.Fatalf("ReadBridgeName: %v", err)
	}
	if want := cni.SafeBridgeName(realmName + "-" + spaceName); got != want {
		t.Errorf("bridge on created conflist = %q, want %q", got, want)
	}
}
