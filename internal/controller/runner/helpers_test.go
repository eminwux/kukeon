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
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
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

// TestResolveRootCNINetworkName_EmptyWhenSpaceMissing pins the best-effort
// contract the issue #630 teardown sites rely on: when the space metadata
// can't be loaded, resolveRootCNINetworkName returns "" rather than panicking,
// which makes the downstream purgeCNIForContainer safety net a no-op instead
// of dereferencing a nil network name.
func TestResolveRootCNINetworkName_EmptyWhenSpaceMissing(t *testing.T) {
	r := newMetadataTestExec(t, t.TempDir(), time.Now())
	if got := r.resolveRootCNINetworkName("default", "nonexistent-space"); got != "" {
		t.Errorf("resolveRootCNINetworkName() = %q, want \"\" when space metadata is absent", got)
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
