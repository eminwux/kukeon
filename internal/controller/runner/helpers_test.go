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

//nolint:testpackage // this package is used to test the private functions in the helpers package
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestHasCreateStages covers the gate setupStatuses uses to decide whether a
// container has anything stage-side worth dialing kuketty for.
func TestHasCreateStages(t *testing.T) {
	tests := []struct {
		name string
		spec intmodel.ContainerSpec
		want bool
	}{
		{
			name: "nil tty has no create stages",
			spec: intmodel.ContainerSpec{Tty: nil},
			want: false,
		},
		{
			name: "tty with only start stages has no create stages",
			spec: intmodel.ContainerSpec{Tty: &intmodel.ContainerTty{OnInit: []intmodel.TtyStage{
				{Script: "echo hi", RunOn: v1beta1.RunOnStart},
				{Script: "echo bye"},
			}}},
			want: false,
		},
		{
			name: "tty with a create stage is detected",
			spec: intmodel.ContainerSpec{Tty: &intmodel.ContainerTty{OnInit: []intmodel.TtyStage{
				{Script: "echo hi", RunOn: v1beta1.RunOnStart},
				{Script: "git clone ...", RunOn: v1beta1.RunOnCreate},
			}}},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasCreateStages(tt.spec); got != tt.want {
				t.Errorf("hasCreateStages = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsValidationError(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		want    bool
		wantErr string
	}{
		{
			name: "nil error returns false",
			err:  nil,
			want: false,
		},
		{
			name: "ErrCellNameRequired returns true",
			err:  errdefs.ErrCellNameRequired,
			want: true,
		},
		{
			name: "ErrCellIDRequired returns true",
			err:  errdefs.ErrCellIDRequired,
			want: true,
		},
		{
			name: "ErrRealmNameRequired returns true",
			err:  errdefs.ErrRealmNameRequired,
			want: true,
		},
		{
			name: "ErrSpaceNameRequired returns true",
			err:  errdefs.ErrSpaceNameRequired,
			want: true,
		},
		{
			name: "ErrStackNameRequired returns true",
			err:  errdefs.ErrStackNameRequired,
			want: true,
		},
		{
			name: "ErrContainerNameRequired returns true",
			err:  errdefs.ErrContainerNameRequired,
			want: true,
		},
		{
			name: "wrapped validation error returns true",
			err:  fmt.Errorf("context: %w", errdefs.ErrCellNameRequired),
			want: true,
		},
		{
			name: "non-validation error returns false",
			err:  errors.New("some other error"),
			want: false,
		},
		{
			name: "wrapped non-validation error returns false",
			err:  fmt.Errorf("context: %w", errors.New("some other error")),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isValidationError(tt.err)
			if got != tt.want {
				t.Errorf("isValidationError() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBuildRootCNINetworkName_DeterministicWhenSpaceMissing is the regression
// guard for issue #685. The stop/kill/delete teardown sites used to resolve the
// purge-target network name via GetSpace+getSpaceNetworkName and swallow the
// GetSpace error, so when space metadata was gone or corrupt networkName stayed
// "" and the post-delete CNI/IPAM purge (purgeCNIForContainer) was gated out —
// leaking the root container's IPAM reservation at /var/lib/cni/networks/<net>/<ip>.
// buildRootCNINetworkName derives the name deterministically from (realm, space)
// so the purge gate is satisfied even with no space metadata on disk. Contrast
// TestResolveRootCNINetworkName_EmptyWhenSpaceMissing above: the re-ADD helper
// deliberately still returns "" so attach is skipped for a vanished space.
func TestBuildRootCNINetworkName_DeterministicWhenSpaceMissing(t *testing.T) {
	r := newMetadataTestExec(t, t.TempDir(), time.Now())
	// No space metadata is seeded under the tmpdir RunPath, so GetSpace would
	// fail here — the exact condition that used to leave networkName empty.
	got := r.buildRootCNINetworkName("default", "nonexistent-space")
	want := "default-nonexistent-space"
	if got != want {
		t.Errorf("buildRootCNINetworkName() = %q, want %q when space metadata is absent", got, want)
	}
}

// TestMain default-stubs the package iptables shell-out (cniIptablesRun) so no
// unit test that reaches the #1324 masquerade-chain sweep via purgeCNIForContainer
// ever touches the host firewall. The erroring stub makes the sweep find nothing
// and append no marker. Tests that assert on the sweep itself
// (TestSweepCNIMasqChain*) install their own recording fake over this default.
func TestMain(m *testing.M) {
	cniIptablesRun = func(_ context.Context, _ ...string) ([]byte, error) {
		return nil, errors.New("iptables stubbed in test")
	}
	os.Exit(m.Run())
}

// TestContainsExactContainerID covers the safety-net matcher every CNI/IPAM
// leak fix in this subsystem relies on to decide whether a host-local IPAM
// file belongs to the container being purged. The prefix false-positive case
// is the load-bearing one: container IDs routinely share a common prefix, and
// a substring match would purge a live sibling's reservation.
func TestContainsExactContainerID(t *testing.T) {
	const id = "cid-100"
	tests := []struct {
		name        string
		content     string
		containerID string
		want        bool
	}{
		{
			name:        "empty container ID never matches",
			content:     "anything",
			containerID: "",
			want:        false,
		},
		{
			name:        "exact match after trimming whitespace",
			content:     id + "\n",
			containerID: id,
			want:        true,
		},
		{
			name:        "host-local IPAM file with ifname line matches",
			content:     id + "\neth0\n",
			containerID: id,
			want:        true,
		},
		{
			name:        "container ID embedded in structured JSON matches",
			content:     `{"containerID":"` + id + `"}`,
			containerID: id,
			want:        true,
		},
		{
			name:        "shared-prefix sibling does not match",
			content:     "cid-1000\neth0\n",
			containerID: id,
			want:        false,
		},
		{
			name:        "shared-prefix suffix does not match",
			content:     id + "extra\n",
			containerID: id,
			want:        false,
		},
		{
			name:        "last_reserved_ip content without the ID does not match",
			content:     "10.88.0.5\n",
			containerID: id,
			want:        false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsExactContainerID(tt.content, tt.containerID); got != tt.want {
				t.Errorf("containsExactContainerID(%q, %q) = %v, want %v", tt.content, tt.containerID, got, tt.want)
			}
		})
	}
}

// TestPurgeCNIForContainer_RemovesOnlyMatchingIPAMFile pins the core of the
// post-delete IPAM purge safety net: scanning the host-local networks dir and
// removing only the IPAM allocation file whose content names the container
// being purged. The shared-prefix sibling and the last_reserved_ip bookkeeping
// file must survive — purging either would leak or corrupt a live reservation.
func TestPurgeCNIForContainer_RemovesOnlyMatchingIPAMFile(t *testing.T) {
	const (
		networkName  = "default-myspace"
		containerID  = "cid-100"
		matchingIP   = "10.88.0.5"
		siblingIP    = "10.88.0.6"
		siblingID    = "cid-1000"
		reservedFile = "last_reserved_ip.0"
	)

	// Redirect the host-local IPAM scan at a temp dir; production never
	// reassigns CNINetworksDir, so this is the only writer.
	networksRoot := t.TempDir()
	prevNetworksDir := cni.CNINetworksDir
	cni.CNINetworksDir = networksRoot
	t.Cleanup(func() { cni.CNINetworksDir = prevNetworksDir })

	ipamDir := filepath.Join(networksRoot, networkName)
	if err := os.MkdirAll(ipamDir, 0o755); err != nil {
		t.Fatalf("mkdir ipam dir: %v", err)
	}
	writeFile := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(ipamDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// IP file whose content names our container — must be removed.
	writeFile(matchingIP, containerID+"\neth0\n")
	// Same-prefix sibling owned by a different container — must survive.
	writeFile(siblingIP, siblingID+"\neth0\n")
	// IPAM bookkeeping file holding an IP, not a container ID — must survive.
	writeFile(reservedFile, matchingIP+"\n")

	tmp := t.TempDir()
	r := &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		// Empty conf dir makes findCNIConfigPath miss, so the CNI DEL block
		// (which needs a real plugin chain) is skipped; the cache dir points
		// at an empty temp dir so the cache scan finds nothing on the host.
		cniConf: &cni.Conf{CniConfigDir: tmp, CniBinDir: tmp, CniCacheDir: tmp},
	}

	// Empty netnsPath: the dead/absent-task purge path the teardown sites hit.
	if err := r.purgeCNIForContainer(containerID, "", networkName); err != nil {
		t.Fatalf("purgeCNIForContainer returned error: %v", err)
	}

	entries, err := os.ReadDir(ipamDir)
	if err != nil {
		t.Fatalf("read ipam dir after purge: %v", err)
	}
	var remaining []string
	for _, e := range entries {
		remaining = append(remaining, e.Name())
	}
	sort.Strings(remaining)
	want := []string{reservedFile, siblingIP}
	sort.Strings(want)
	if len(remaining) != len(want) {
		t.Fatalf("remaining files = %v, want %v", remaining, want)
	}
	for i := range want {
		if remaining[i] != want[i] {
			t.Fatalf("remaining files = %v, want %v", remaining, want)
		}
	}
}

// TestPurgeCNIForContainer_CallsDelWithEmptyNetns is the regression guard for
// issue #1174: deleting a cell whose containerd task was already stopped left
// netnsPath == "", and the old `if netnsPath != ""` gate skipped CNI DEL
// entirely — permanently leaking the bridge plugin's per-cell CNI-* iptables
// masquerade chains (88 observed on one host). libcni's DelNetworkList is
// netns-optional, so DEL must run unconditionally. This test wires a fake CNI
// plugin that records its invocation and asserts DEL fires even with an empty
// netnsPath.
func TestPurgeCNIForContainer_CallsDelWithEmptyNetns(t *testing.T) {
	const (
		networkName = "default-myspace"
		containerID = "cid-100"
	)

	binDir := t.TempDir()
	confDir := t.TempDir()
	cacheDir := t.TempDir()

	// Fake CNI plugin: records each invocation's CNI_COMMAND to a marker file
	// and exits 0. libcni execs it by the conflist's plugin "type".
	markerPath := filepath.Join(t.TempDir(), "del-calls")
	pluginScript := "#!/bin/sh\nprintf '%s\\n' \"$CNI_COMMAND\" >> \"" + markerPath + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(binDir, "recorddel"), []byte(pluginScript), 0o755); err != nil { //nolint:gosec // test fixture plugin must be executable
		t.Fatalf("write fake plugin: %v", err)
	}

	// cniVersion 0.4.0+ makes a DEL cache miss non-fatal (libcni nils the
	// cached result and still execs the plugin), so no cache seeding is needed.
	conflist := `{
  "cniVersion": "0.4.0",
  "name": "` + networkName + `",
  "plugins": [ { "type": "recorddel" } ]
}`
	if err := os.WriteFile(filepath.Join(confDir, networkName+".conflist"), []byte(conflist), 0o644); err != nil { //nolint:gosec // test fixture conflist
		t.Fatalf("write conflist: %v", err)
	}

	r := &Exec{
		ctx:     context.Background(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		cniConf: &cni.Conf{CniConfigDir: confDir, CniBinDir: binDir, CniCacheDir: cacheDir},
	}

	// Empty netnsPath: the already-stopped-task delete path that used to skip DEL.
	if err := r.purgeCNIForContainer(containerID, "", networkName); err != nil {
		t.Fatalf("purgeCNIForContainer returned error: %v", err)
	}

	calls, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("CNI DEL was not invoked with empty netns (marker file absent): %v", err)
	}
	if !strings.Contains(string(calls), "DEL") {
		t.Fatalf("expected a DEL invocation recorded, got %q", string(calls))
	}
}

// TestFindCNIConfigPath_ResolvesRunPathConflist is the core regression guard for
// issue #1324. Space conflists are written per-space as
// <RunPath>/<metadata>/<realm>/<space>/network.conflist, never as
// <networkName>.conflist under CniConfigDir — so the old findCNIConfigPath
// (which only ever checked the CniConfigDir standard location and had an
// unimplemented run-path fallback) always missed for space networks, silently
// skipping the CNI DEL and leaking the bridge plugin's per-container masquerade
// nat chains on every networked-cell purge. The realm name here carries a '-'
// to exercise the no-reverse-split resolution (the network name "<realm>-<space>"
// is matched by recomputing it per space dir, not by splitting on '-').
func TestFindCNIConfigPath_ResolvesRunPathConflist(t *testing.T) {
	const (
		realmName = "dev-init-attach" // '-' in realm name: name is not reversible by split
		spaceName = "ds"
	)
	runPath := t.TempDir()
	confPath, err := fs.SpaceNetworkConfigPath(runPath, realmName, spaceName)
	if err != nil {
		t.Fatalf("SpaceNetworkConfigPath: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(confPath), 0o750); err != nil {
		t.Fatalf("mkdir conflist dir: %v", err)
	}
	if err = os.WriteFile(confPath, []byte(`{"cniVersion":"0.4.0","name":"dev-init-attach-ds","plugins":[]}`), 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}

	// Empty CniConfigDir so the standard-location lookup misses and the run-path
	// fallback is the only thing that can resolve the conflist.
	emptyConfDir := t.TempDir()
	r := &Exec{
		ctx:     context.Background(),
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:    Options{RunPath: runPath},
		cniConf: &cni.Conf{CniConfigDir: emptyConfDir, CniBinDir: emptyConfDir, CniCacheDir: emptyConfDir},
	}

	networkName, err := naming.BuildSpaceNetworkName(realmName, spaceName)
	if err != nil {
		t.Fatalf("BuildSpaceNetworkName: %v", err)
	}
	got, err := r.findCNIConfigPath(networkName)
	if err != nil {
		t.Fatalf("findCNIConfigPath(%q) returned error: %v", networkName, err)
	}
	if got != confPath {
		t.Fatalf("findCNIConfigPath(%q) = %q, want run-path conflist %q", networkName, got, confPath)
	}

	// A network with no matching space dir must still error (the lookup must not
	// resolve to an unrelated space's conflist).
	if _, err = r.findCNIConfigPath("no-such-network"); err == nil {
		t.Fatalf("findCNIConfigPath(no-such-network) = nil error, want not-found")
	}
}

// TestPurgeCNIForContainer_RunPathConflistRunsDEL is the end-to-end #1324 guard:
// with the conflist present ONLY at the per-space run path (and the CniConfigDir
// standard location empty), purgeCNIForContainer must resolve it via the
// fallback and actually run the CNI DEL, recording "cni-del" in the purge result
// rather than a silent skip. A fake CNI plugin records the DEL invocation.
func TestPurgeCNIForContainer_RunPathConflistRunsDEL(t *testing.T) {
	const (
		realmName   = "default"
		spaceName   = "myspace"
		containerID = "cid-100"
	)
	networkName, err := naming.BuildSpaceNetworkName(realmName, spaceName)
	if err != nil {
		t.Fatalf("BuildSpaceNetworkName: %v", err)
	}

	binDir := t.TempDir()
	cacheDir := t.TempDir()
	emptyConfDir := t.TempDir() // CniConfigDir intentionally empty: forces the run-path fallback.

	// Fake CNI plugin: records each invocation's CNI_COMMAND to a marker file.
	markerPath := filepath.Join(t.TempDir(), "del-calls")
	pluginScript := "#!/bin/sh\nprintf '%s\\n' \"$CNI_COMMAND\" >> \"" + markerPath + "\"\nexit 0\n"
	if err = os.WriteFile(filepath.Join(binDir, "recorddel"), []byte(pluginScript), 0o755); err != nil {
		t.Fatalf("write fake plugin: %v", err)
	}

	// Conflist lives ONLY at the per-space run path — the exact on-disk layout
	// that made findCNIConfigPath miss before this fix.
	runPath := t.TempDir()
	confPath, err := fs.SpaceNetworkConfigPath(runPath, realmName, spaceName)
	if err != nil {
		t.Fatalf("SpaceNetworkConfigPath: %v", err)
	}
	if err = os.MkdirAll(filepath.Dir(confPath), 0o750); err != nil {
		t.Fatalf("mkdir conflist dir: %v", err)
	}
	conflist := `{
  "cniVersion": "0.4.0",
  "name": "` + networkName + `",
  "plugins": [ { "type": "recorddel" } ]
}`
	if err = os.WriteFile(confPath, []byte(conflist), 0o600); err != nil {
		t.Fatalf("write conflist: %v", err)
	}

	var logBuf bytes.Buffer
	r := &Exec{
		ctx:     context.Background(),
		logger:  slog.New(slog.NewTextHandler(&logBuf, nil)),
		opts:    Options{RunPath: runPath},
		cniConf: &cni.Conf{CniConfigDir: emptyConfDir, CniBinDir: binDir, CniCacheDir: cacheDir},
	}

	if err = r.purgeCNIForContainer(containerID, "", networkName); err != nil {
		t.Fatalf("purgeCNIForContainer returned error: %v", err)
	}

	// DEL must have fired through the run-path conflist.
	calls, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("CNI DEL was not invoked via run-path conflist (marker file absent): %v", err)
	}
	if !strings.Contains(string(calls), "DEL") {
		t.Fatalf("expected a DEL invocation recorded, got %q", string(calls))
	}
	// The purge result must record a real cni-del, not a skip/failure.
	logged := logBuf.String()
	if !strings.Contains(logged, "cni-del") || strings.Contains(logged, "cni-del-skipped") ||
		strings.Contains(logged, "cni-del-failed") {
		t.Fatalf("purge result did not record a clean cni-del: %s", logged)
	}
}

// TestPurgeCNIForContainer_SurfacesSkippedDEL covers AC #2: a skipped CNI DEL
// (conflist resolvable nowhere) must surface above Debug (Warn) and be reflected
// in the purge result, not swallowed and reported as a clean purge (#1324).
func TestPurgeCNIForContainer_SurfacesSkippedDEL(t *testing.T) {
	emptyDir := t.TempDir()
	var logBuf bytes.Buffer
	r := &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(&logBuf, nil)),
		// RunPath points at a tree with no metadata, so the run-path fallback
		// also misses and the DEL is genuinely unresolvable.
		opts:    Options{RunPath: t.TempDir()},
		cniConf: &cni.Conf{CniConfigDir: emptyDir, CniBinDir: emptyDir, CniCacheDir: emptyDir},
	}

	if err := r.purgeCNIForContainer("cid-100", "", "default-myspace"); err != nil {
		t.Fatalf("purgeCNIForContainer returned error: %v", err)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "level=WARN") {
		t.Fatalf("expected a WARN log for the skipped DEL, got: %s", logged)
	}
	if !strings.Contains(logged, "cni-del-skipped:config-not-found") {
		t.Fatalf("expected purge result to record cni-del-skipped:config-not-found, got: %s", logged)
	}
}

// TestCNIMasqChainName pins the per-container masquerade chain-name replica used
// by the purge sweep against golden values captured from live-created chains in
// the #1324 dev-init repro. cniMasqChainName must equal utils.FormatChainName
// from github.com/containernetworking/plugins byte-for-byte, or the sweep would
// target the wrong (or no) chain and the masquerade nat rules would still leak.
func TestCNIMasqChainName(t *testing.T) {
	tests := []struct {
		network     string
		containerID string
		want        string
	}{
		{"default-default", "default_default_natleak-test_root", "CNI-e12a15b29243523ec04d02b9"},
		{"dev-init-attach-ds", "ds_dks_cattach_root", "CNI-ec12256cec4057b1122e3662"},
	}
	for _, tt := range tests {
		if got := cniMasqChainName(tt.network, tt.containerID); got != tt.want {
			t.Errorf("cniMasqChainName(%q, %q) = %q, want %q", tt.network, tt.containerID, got, tt.want)
		}
	}
}

// TestSweepCNIMasqChain is the regression guard for the #1324 belt-and-suspenders
// sweep: on the purge path the bridge plugin's empty-netns DEL never reaches its
// ipMasq teardown, so the masquerade chain + its POSTROUTING jumps must be
// removed directly. It injects a fake iptables runner and asserts the sweep
// discovers the jumps, deletes them by line number (descending so earlier
// deletes don't renumber later ones), then flushes and deletes the chain.
func TestSweepCNIMasqChain(t *testing.T) {
	const (
		network     = "default-default"
		containerID = "default_default_natleak-test_root"
	)
	chain := cniMasqChainName(network, containerID) // CNI-e12a15b29243523ec04d02b9

	// Canned `iptables -t nat -L POSTROUTING --line-numbers -n`: two rules jump
	// to our chain (lines 2 and 4); the header rows and unrelated rules must be
	// ignored.
	listOutput := strings.Join([]string{
		"Chain POSTROUTING (policy ACCEPT)",
		"num  target          prot opt source       destination",
		"1    KUKEON-FORWARD  all  --  0.0.0.0/0    0.0.0.0/0",
		"2    " + chain + "  all  --  10.89.0.36   0.0.0.0/0",
		"3    OTHER-CHAIN     all  --  0.0.0.0/0    0.0.0.0/0",
		"4    " + chain + "  all  --  10.88.0.36   0.0.0.0/0",
		"",
	}, "\n")

	var calls [][]string
	prev := cniIptablesRun
	cniIptablesRun = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if len(args) >= 3 && args[2] == "-L" {
			return []byte(listOutput), nil
		}
		return nil, nil
	}
	t.Cleanup(func() { cniIptablesRun = prev })

	r := &Exec{ctx: context.Background(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	marker := r.sweepCNIMasqChain(network, containerID)

	if want := "cni-masq-swept:" + chain; marker != want {
		t.Fatalf("marker = %q, want %q", marker, want)
	}

	wantSeq := [][]string{
		{"-t", "nat", "-L", "POSTROUTING", "--line-numbers", "-n"},
		{"-t", "nat", "-D", "POSTROUTING", "4"}, // descending: line 4 before line 2
		{"-t", "nat", "-D", "POSTROUTING", "2"},
		{"-t", "nat", "-F", chain},
		{"-t", "nat", "-X", chain},
	}
	if len(calls) != len(wantSeq) {
		t.Fatalf("issued %d iptables calls, want %d: %v", len(calls), len(wantSeq), calls)
	}
	for i := range wantSeq {
		if strings.Join(calls[i], " ") != strings.Join(wantSeq[i], " ") {
			t.Errorf("call %d = %v, want %v", i, calls[i], wantSeq[i])
		}
	}
}

// TestSweepCNIMasqChain_NoChainNoMarker covers the common post-live-netns-DEL
// case: the chain is already gone, so the sweep finds no jumps, its flush/delete
// no-op, and it returns "" (no false "swept" marker in the purge result).
func TestSweepCNIMasqChain_NoChainNoMarker(t *testing.T) {
	prev := cniIptablesRun
	cniIptablesRun = func(_ context.Context, args ...string) ([]byte, error) {
		if len(args) >= 3 && args[2] == "-L" {
			// POSTROUTING with no rule jumping to our chain.
			return []byte("Chain POSTROUTING (policy ACCEPT)\nnum  target\n1    KUKEON-FORWARD\n"), nil
		}
		// -F / -X on an absent chain fail, like real iptables.
		return nil, errors.New("iptables: No chain/target/match by that name")
	}
	t.Cleanup(func() { cniIptablesRun = prev })

	r := &Exec{ctx: context.Background(), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if marker := r.sweepCNIMasqChain("default-default", "default_default_natleak-test_root"); marker != "" {
		t.Fatalf("marker = %q, want empty (chain already gone)", marker)
	}
}
