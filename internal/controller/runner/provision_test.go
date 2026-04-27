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
		ctx:             context.Background(),
		logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:            opts,
		cniConf:         &opts.CniConf,
		subnetAllocator: cni.NewDefaultSubnetAllocator(runPath),
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

// TestCreateSpaceCNIConfig_TwoSpacesGetDistinctSubnets covers AC items 3 and 4
// from #131: two spaces created back-to-back must land on different /24
// chunks of the parent CIDR. Drives createSpaceCNIConfig directly because
// the higher-level CreateSpace path requires a containerd connection.
func TestCreateSpaceCNIConfig_TwoSpacesGetDistinctSubnets(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	const realmName = "default"
	spaces := []string{"alpha", "beta"}
	subnets := make(map[string]string, len(spaces))

	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		t.Fatalf("cni.NewManager: %v", err)
	}

	for _, name := range spaces {
		space := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: name},
			Spec:     intmodel.SpaceSpec{RealmName: realmName},
		}
		confPath, ccErr := r.createSpaceCNIConfig(space)
		if ccErr != nil {
			t.Fatalf("createSpaceCNIConfig %q: %v", name, ccErr)
		}
		subnet, rsErr := mgr.ReadSubnetCIDR(confPath)
		if rsErr != nil {
			t.Fatalf("ReadSubnetCIDR %q: %v", name, rsErr)
		}
		if subnet == "" {
			t.Fatalf("conflist %q has empty subnet", name)
		}
		subnets[name] = subnet
	}

	if subnets["alpha"] == subnets["beta"] {
		t.Errorf("two spaces collided on subnet %q (alpha=%q, beta=%q) — phase 2 allocator regressed",
			subnets["alpha"], subnets["alpha"], subnets["beta"])
	}

	// Each per-space network.json should hold the same subnet that was
	// written into the conflist — the two files are the allocator's
	// bidirectional contract with the runner.
	alloc := cni.NewDefaultSubnetAllocator(runPath)
	for _, name := range spaces {
		assigned, lErr := alloc.LoadAssigned(realmName, name)
		if lErr != nil {
			t.Fatalf("LoadAssigned %q: %v", name, lErr)
		}
		if assigned != subnets[name] {
			t.Errorf("space %q: network.json subnet = %q, conflist subnet = %q (must match)",
				name, assigned, subnets[name])
		}
	}
}

// TestRunnerSubnetAlloc_ReleasedSubnetIsReusedByNextCreate is the
// runner-level integration counterpart to the allocator's Release/reuse
// unit test: it proves that the same allocator instance the runner uses
// (built once and cached on *Exec) actually frees subnets back into the
// pool when DeleteSpace's release call fires. Drives the file system
// directly to avoid the containerd dependency in DeleteSpace itself.
func TestRunnerSubnetAlloc_ReleasedSubnetIsReusedByNextCreate(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	const realmName = "default"

	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		t.Fatalf("cni.NewManager: %v", err)
	}

	createAndRead := func(name string) string {
		t.Helper()
		space := intmodel.Space{
			Metadata: intmodel.SpaceMetadata{Name: name},
			Spec:     intmodel.SpaceSpec{RealmName: realmName},
		}
		confPath, ccErr := r.createSpaceCNIConfig(space)
		if ccErr != nil {
			t.Fatalf("createSpaceCNIConfig %q: %v", name, ccErr)
		}
		subnet, rsErr := mgr.ReadSubnetCIDR(confPath)
		if rsErr != nil {
			t.Fatalf("ReadSubnetCIDR %q: %v", name, rsErr)
		}
		return subnet
	}

	first := createAndRead("alpha")
	second := createAndRead("beta")

	// DeleteSpace's runner code calls r.subnetAlloc().Release. Mirror
	// just that call here so we can validate reuse without standing up
	// containerd. The conflist on disk for "alpha" can stay — what
	// matters for the next allocation is whether network.json is gone.
	if relErr := r.subnetAllocator.Release(realmName, "alpha"); relErr != nil {
		t.Fatalf("Release alpha: %v", relErr)
	}

	third := createAndRead("gamma")
	if third != first {
		t.Errorf("released subnet %q was not reused by the next create — got %q (beta still owns %q)",
			first, third, second)
	}
}

// TestEnsureSpaceCNIConfig_PreservesLegacySharedSubnet covers #131's
// deliberate deferral: spaces with a pre-allocator conflist on the shared
// 10.88.0.0/16 (no per-space network.json) must keep that subnet through
// idempotent re-runs. Migrating those spaces is the architectural fork
// carved off into #133, not part of phase 2.
func TestEnsureSpaceCNIConfig_PreservesLegacySharedSubnet(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)

	// Names short enough that SafeBridgeName is a no-op so the bridge
	// already matches; the only reason to regenerate would be the
	// ForceRegenerateCNI flag, which this test does not set.
	const realmName = "default"
	const spaceName = "alpha"
	correctBridge := cni.SafeBridgeName(realmName + "-" + spaceName)
	confPath := writeStaleConflist(t, runPath, realmName, spaceName, correctBridge)

	space := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	}
	if _, err := r.ensureSpaceCNIConfig(space); err != nil {
		t.Fatalf("ensureSpaceCNIConfig: %v", err)
	}

	mgr, err := cni.NewManager(r.cniConf.CniBinDir, r.cniConf.CniConfigDir, r.cniConf.CniCacheDir)
	if err != nil {
		t.Fatalf("cni.NewManager: %v", err)
	}
	subnet, err := mgr.ReadSubnetCIDR(confPath)
	if err != nil {
		t.Fatalf("ReadSubnetCIDR: %v", err)
	}
	// writeStaleConflist hard-codes 10.22.0.0/16 — preserved verbatim
	// proves the legacy path did not migrate the space.
	if subnet != "10.22.0.0/16" {
		t.Errorf("legacy subnet was migrated unexpectedly: got %q, want %q", subnet, "10.22.0.0/16")
	}

	// And no per-space allocator state should have been written, so a
	// future #133 migrator can identify legacy spaces by absence of
	// network.json.
	alloc := cni.NewDefaultSubnetAllocator(runPath)
	got, lerr := alloc.LoadAssigned(realmName, spaceName)
	if lerr != nil {
		t.Fatalf("LoadAssigned: %v", lerr)
	}
	if got != "" {
		t.Errorf("legacy preservation wrote allocator state: got %q, want \"\"", got)
	}
}

// TestEnsureSpaceCNIConfig_ForceRegenerateUsesAllocatorWhenLegacyMissing
// covers the recovery path: a force-regenerate request against a space
// whose conflist is unreadable (pretend deleted) and whose network.json
// is also absent must mint a fresh per-space subnet rather than crashing.
func TestEnsureSpaceCNIConfig_ForceRegenerateUsesAllocatorWhenLegacyMissing(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, true)

	const realmName = "default"
	const spaceName = "recovered"

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
	subnet, err := mgr.ReadSubnetCIDR(confPath)
	if err != nil {
		t.Fatalf("ReadSubnetCIDR: %v", err)
	}
	// Recovery should land on the lowest free /24, not the legacy /16.
	if subnet != "10.88.0.0/24" {
		t.Errorf("recovered space subnet = %q, want 10.88.0.0/24", subnet)
	}
	alloc := cni.NewDefaultSubnetAllocator(runPath)
	if got, _ := alloc.LoadAssigned(realmName, spaceName); got != subnet {
		t.Errorf("recovery did not persist allocator state: got %q, want %q", got, subnet)
	}
}
