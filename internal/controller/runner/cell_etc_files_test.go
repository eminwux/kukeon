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
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellprofile"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	utilfs "github.com/eminwux/kukeon/internal/util/fs"
)

// newCellForEtcFilesTest builds a minimally-populated Cell suitable for
// exercising the cell_etc_files helpers. The container layout is what
// cellRootHostNetwork inspects to decide whether /etc/hosts should be
// suppressed.
func newCellForEtcFilesTest(rootHostNetwork bool) *intmodel.Cell {
	return &intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "kukeond"},
		Spec: intmodel.CellSpec{
			RealmName:       "kuke-system",
			SpaceName:       "kukeon",
			StackName:       "kukeon",
			RootContainerID: "kukeond",
			Containers: []intmodel.ContainerSpec{
				{ID: "kukeond", Root: true, HostNetwork: rootHostNetwork},
			},
		},
	}
}

// TestEnsureCellEtcFilesExistPreCNI_PreservesIPLine is the regression guard
// for the /etc/hosts clobber bug the reviewer flagged on PR #352: a prior
// StartCell tick has already rewritten /etc/hosts with the CNI-assigned IP
// line, and a subsequent ensureCellContainers tick (driven by update_cell
// or update_container on a running cell) must not truncate that file back
// to the localhost-only block — doing so regresses the DNS-lookup fix
// issue #345 ships.
func TestEnsureCellEtcFilesExistPreCNI_PreservesIPLine(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := newCellForEtcFilesTest(false)

	// Simulate the post-StartCell state: /etc/hosts has the cell IP line.
	if err := r.renderCellEtcFilesPreCNI(cell); err != nil {
		t.Fatalf("seed pre-CNI render: %v", err)
	}
	hostsPath := utilfs.CellEtcHostsPath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	withIP := etcHostsLocalhostBlock + "10.22.0.5\tkukeond\n"
	if err := os.WriteFile(hostsPath, []byte(withIP), 0o644); err != nil {
		t.Fatalf("seed /etc/hosts with IP: %v", err)
	}

	// The fix: ensure-only path must not rewrite content when the file
	// already exists.
	if err := r.ensureCellEtcFilesExistPreCNI(cell); err != nil {
		t.Fatalf("ensureCellEtcFilesExistPreCNI: %v", err)
	}

	got, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read /etc/hosts: %v", err)
	}
	if string(got) != withIP {
		t.Errorf("/etc/hosts was clobbered by ensure helper:\n--- got ---\n%s\n--- want ---\n%s", got, withIP)
	}
	if !strings.Contains(string(got), "10.22.0.5\tkukeond") {
		t.Errorf("/etc/hosts lost cell IP entry after ensure: %q", got)
	}
}

// TestEnsureCellEtcFilesExistPreCNI_FillsInMissingFiles verifies the helper
// still creates the per-cell source files when they are absent — the
// existing-root branch can race a stale or partial prior run that left a
// file missing, and the bind-mount source must exist before the OCI specs
// reference it.
func TestEnsureCellEtcFilesExistPreCNI_FillsInMissingFiles(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := newCellForEtcFilesTest(false)

	if err := r.ensureCellEtcFilesExistPreCNI(cell); err != nil {
		t.Fatalf("ensureCellEtcFilesExistPreCNI: %v", err)
	}

	hostnamePath := utilfs.CellEtcHostnamePath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	hostsPath := utilfs.CellEtcHostsPath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)

	hn, err := os.ReadFile(hostnamePath)
	if err != nil {
		t.Fatalf("read /etc/hostname: %v", err)
	}
	if string(hn) != "kukeond\n" {
		t.Errorf("/etc/hostname = %q, want %q", hn, "kukeond\n")
	}

	hosts, err := os.ReadFile(hostsPath)
	if err != nil {
		t.Fatalf("read /etc/hosts: %v", err)
	}
	if string(hosts) != etcHostsLocalhostBlock {
		t.Errorf("/etc/hosts = %q, want pre-CNI localhost block", hosts)
	}
}

// TestStampContainerRecreateRuntimeFields is the regression guard for
// issue #354: the StartContainer single-container recreate path was the
// only BuildContainerSpec call site that did not apply the per-cell
// /etc/hosts + /etc/hostname bind-mount stamps or the CellProfileName
// runtime stamp. The helper bundles both per-spec stamps so the recreate
// path produces a spec consistent with every other recreate path.
func TestStampContainerRecreateRuntimeFields(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name:   "work-cell",
			Labels: map[string]string{cellprofile.LabelProfile: "kukeon-pr"},
		},
		Spec: intmodel.CellSpec{
			RealmName:       "default",
			SpaceName:       "team-a",
			StackName:       "web",
			RootContainerID: "root",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work"},
			},
		},
	}
	work := &cell.Spec.Containers[1]

	r.stampContainerRecreateRuntimeFields(work, cell)

	wantHostname := utilfs.CellEtcHostnamePath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	wantHosts := utilfs.CellEtcHostsPath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	if work.EtcHostnamePath != wantHostname {
		t.Errorf("EtcHostnamePath = %q, want %q", work.EtcHostnamePath, wantHostname)
	}
	if work.EtcHostsPath != wantHosts {
		t.Errorf("EtcHostsPath = %q, want %q", work.EtcHostsPath, wantHosts)
	}
	if work.CellProfileName != "kukeon-pr" {
		t.Errorf("CellProfileName = %q, want %q", work.CellProfileName, "kukeon-pr")
	}
}

// TestStampContainerRecreateRuntimeFields_HostNetworkSuppressesHosts pins the
// host-network carve-out for the recreate path: a host-network cell still
// gets /etc/hostname but must leave EtcHostsPath empty so the host's
// /etc/hosts remains authoritative — same rule the cell-wide stamp applies.
func TestStampContainerRecreateRuntimeFields_HostNetworkSuppressesHosts(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "kukeond"},
		Spec: intmodel.CellSpec{
			RealmName:       "kuke-system",
			SpaceName:       "kukeon",
			StackName:       "kukeon",
			RootContainerID: "kukeond",
			Containers: []intmodel.ContainerSpec{
				{ID: "kukeond", Root: true, HostNetwork: true},
				{ID: "sidecar"},
			},
		},
	}
	side := &cell.Spec.Containers[1]

	r.stampContainerRecreateRuntimeFields(side, cell)

	wantHostname := utilfs.CellEtcHostnamePath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	if side.EtcHostnamePath != wantHostname {
		t.Errorf("EtcHostnamePath = %q, want %q", side.EtcHostnamePath, wantHostname)
	}
	if side.EtcHostsPath != "" {
		t.Errorf("EtcHostsPath = %q, want empty (host-network suppresses /etc/hosts)", side.EtcHostsPath)
	}
}

// TestStampContainerRecreateRuntimeFields_NoProfileLabel covers a plain
// CellDoc-sourced cell: no profile label means CellProfileName stays empty
// so kukeonDefaultEnv emits no KUKEON_CELL_PROFILE_NAME entry — the
// downstream BuildContainerSpec test in ctr already pins that env mapping.
func TestStampContainerRecreateRuntimeFields_NoProfileLabel(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := &intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "plain"},
		Spec: intmodel.CellSpec{
			RealmName:       "default",
			SpaceName:       "team-a",
			StackName:       "web",
			RootContainerID: "root",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "work"},
			},
		},
	}
	work := &cell.Spec.Containers[1]

	r.stampContainerRecreateRuntimeFields(work, cell)

	if work.CellProfileName != "" {
		t.Errorf("CellProfileName = %q, want empty (no profile label on cell)", work.CellProfileName)
	}
	if work.EtcHostnamePath == "" {
		t.Errorf("EtcHostnamePath empty; etc-files stamp should still apply when no profile label")
	}
}

// TestEnsureCellEtcFilesExistPreCNI_HostNetworkSkipsHosts confirms the
// host-network carve-out: the kukeond cell's root runs HostNetwork=true,
// so /etc/hosts must not be rendered (the host's /etc/hosts is the right
// view) — but /etc/hostname is still produced.
func TestEnsureCellEtcFilesExistPreCNI_HostNetworkSkipsHosts(t *testing.T) {
	runPath := t.TempDir()
	r := newProvisionTestExec(t, runPath, false)
	cell := newCellForEtcFilesTest(true)

	if err := r.ensureCellEtcFilesExistPreCNI(cell); err != nil {
		t.Fatalf("ensureCellEtcFilesExistPreCNI: %v", err)
	}

	hostnamePath := utilfs.CellEtcHostnamePath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	hostsPath := utilfs.CellEtcHostsPath(
		runPath,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)

	if _, err := os.Stat(hostnamePath); err != nil {
		t.Errorf("/etc/hostname should exist on host-network cells: %v", err)
	}
	if _, err := os.Stat(hostsPath); !os.IsNotExist(err) {
		t.Errorf("/etc/hosts should be suppressed on host-network cells; stat err = %v", err)
	}
}
