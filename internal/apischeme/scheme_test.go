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

package apischeme_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

func TestRealmRoundTripV1Beta1(t *testing.T) {
	input := ext.RealmDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindRealm,
		Metadata: ext.RealmMetadata{
			Name:   "realm0",
			Labels: map[string]string{"a": "b"},
		},
		Spec: ext.RealmSpec{
			Namespace: "realm0",
		},
		Status: ext.RealmStatus{
			State: ext.RealmStateCreating,
		},
	}

	internal, version, err := apischeme.NormalizeRealm(input)
	if err != nil {
		t.Fatalf("NormalizeRealm failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "realm0" || internal.Spec.Namespace != "realm0" {
		t.Fatalf("unexpected internal realm: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.RealmStateReady

	output, err := apischeme.BuildRealmExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindRealm {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.RealmStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
}

func TestSpaceRoundTripV1Beta1(t *testing.T) {
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata: ext.SpaceMetadata{
			Name:   "space0",
			Labels: map[string]string{"c": "d"},
		},
		Spec: ext.SpaceSpec{
			RealmID: "realm0",
		},
		Status: ext.SpaceStatus{
			State: ext.SpaceStatePending,
		},
	}

	internal, version, err := apischeme.NormalizeSpace(input)
	if err != nil {
		t.Fatalf("NormalizeSpace failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "space0" || internal.Spec.RealmName != "realm0" {
		t.Fatalf("unexpected internal space: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.SpaceStateReady

	output, err := apischeme.BuildSpaceExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindSpace {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.SpaceStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
}

// TestSpaceDefaultsRoundTripV1Beta1 verifies that spec.defaults.container
// survives NormalizeSpace → BuildSpaceExternalFromInternal with all nested
// fields (user, readOnly, caps, securityOpts, tmpfs, resources) intact.
func TestSpaceDefaultsRoundTripV1Beta1(t *testing.T) {
	roTrue := true
	memLimit := int64(4 * 1024 * 1024 * 1024)
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata:   ext.SpaceMetadata{Name: "agent-sandbox"},
		Spec: ext.SpaceSpec{
			RealmID: "agents",
			Defaults: &ext.SpaceDefaults{
				Container: &ext.SpaceContainerDefaults{
					User:                   "1000:1000",
					ReadOnlyRootFilesystem: &roTrue,
					Capabilities: &ext.ContainerCapabilities{
						Drop: []string{"ALL"},
						Add:  []string{"NET_BIND_SERVICE"},
					},
					SecurityOpts: []string{"no-new-privileges"},
					Tmpfs: []ext.ContainerTmpfsMount{
						{Path: "/tmp", SizeBytes: 64 * 1024 * 1024},
					},
					Resources: &ext.ContainerResources{
						MemoryLimitBytes: &memLimit,
					},
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeSpace(input)
	if err != nil {
		t.Fatalf("NormalizeSpace failed: %v", err)
	}
	if internal.Spec.Defaults == nil || internal.Spec.Defaults.Container == nil {
		t.Fatalf("internal Defaults.Container is nil after NormalizeSpace: %+v", internal.Spec.Defaults)
	}
	intContainer := internal.Spec.Defaults.Container
	if intContainer.User != "1000:1000" {
		t.Errorf("internal User = %q, want 1000:1000", intContainer.User)
	}
	if intContainer.ReadOnlyRootFilesystem == nil || !*intContainer.ReadOnlyRootFilesystem {
		t.Errorf("internal ReadOnlyRootFilesystem = %v, want *true", intContainer.ReadOnlyRootFilesystem)
	}
	if intContainer.Capabilities == nil ||
		len(intContainer.Capabilities.Drop) != 1 || intContainer.Capabilities.Drop[0] != "ALL" {
		t.Errorf("internal Capabilities.Drop = %+v, want [ALL]", intContainer.Capabilities)
	}
	if len(intContainer.SecurityOpts) != 1 || intContainer.SecurityOpts[0] != "no-new-privileges" {
		t.Errorf("internal SecurityOpts = %v", intContainer.SecurityOpts)
	}
	if len(intContainer.Tmpfs) != 1 || intContainer.Tmpfs[0].Path != "/tmp" {
		t.Errorf("internal Tmpfs = %+v", intContainer.Tmpfs)
	}
	if intContainer.Resources == nil || intContainer.Resources.MemoryLimitBytes == nil ||
		*intContainer.Resources.MemoryLimitBytes != memLimit {
		t.Errorf("internal Resources = %+v, want MemoryLimit=4GiB", intContainer.Resources)
	}

	output, err := apischeme.BuildSpaceExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal failed: %v", err)
	}
	if output.Spec.Defaults == nil || output.Spec.Defaults.Container == nil {
		t.Fatalf("external Defaults.Container is nil after round trip: %+v", output.Spec.Defaults)
	}
	extContainer := output.Spec.Defaults.Container
	if extContainer.User != "1000:1000" {
		t.Errorf("external User = %q, want 1000:1000", extContainer.User)
	}
	if extContainer.ReadOnlyRootFilesystem == nil || !*extContainer.ReadOnlyRootFilesystem {
		t.Errorf("external ReadOnlyRootFilesystem not round-tripped: %v", extContainer.ReadOnlyRootFilesystem)
	}
	if extContainer.Capabilities == nil ||
		len(extContainer.Capabilities.Drop) != 1 || extContainer.Capabilities.Drop[0] != "ALL" ||
		len(extContainer.Capabilities.Add) != 1 || extContainer.Capabilities.Add[0] != "NET_BIND_SERVICE" {
		t.Errorf("external Capabilities not round-tripped: %+v", extContainer.Capabilities)
	}

	// YAML round trip — ensures the tags decode / encode correctly.
	encoded, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal failed: %v", err)
	}
	var decoded ext.SpaceDoc
	if err = yaml.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("yaml.Unmarshal failed: %v\nyaml:\n%s", err, encoded)
	}
	if decoded.Spec.Defaults == nil || decoded.Spec.Defaults.Container == nil ||
		decoded.Spec.Defaults.Container.User != "1000:1000" {
		t.Errorf("YAML round trip lost Defaults.Container: decoded=%+v", decoded.Spec.Defaults)
	}
	if !strings.Contains(string(encoded), "defaults:") {
		t.Errorf("YAML missing defaults block: %s", encoded)
	}
}

func TestStackRoundTripV1Beta1(t *testing.T) {
	input := ext.StackDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindStack,
		Metadata: ext.StackMetadata{
			Name:   "stack0",
			Labels: map[string]string{"e": "f"},
		},
		Spec: ext.StackSpec{
			ID:      "stack-id-0",
			RealmID: "realm0",
			SpaceID: "space0",
		},
		Status: ext.StackStatus{
			State:      ext.StackStatePending,
			CgroupPath: "/sys/fs/cgroup/stack0",
		},
	}

	internal, version, err := apischeme.NormalizeStack(input)
	if err != nil {
		t.Fatalf("NormalizeStack failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "stack0" || internal.Spec.ID != "stack-id-0" || internal.Spec.RealmName != "realm0" ||
		internal.Spec.SpaceName != "space0" {
		t.Fatalf("unexpected internal stack: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.StackStateReady

	output, err := apischeme.BuildStackExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildStackExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindStack {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.StackStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
}

func TestSpaceEgressPolicyRoundTripV1Beta1(t *testing.T) {
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata:   ext.SpaceMetadata{Name: "agents"},
		Spec: ext.SpaceSpec{
			RealmID: "main",
			Network: &ext.SpaceNetwork{
				Egress: &ext.EgressPolicy{
					Default: ext.EgressDefaultDeny,
					Allow: []ext.EgressAllowRule{
						{Host: "api.anthropic.com", Ports: []int{443}},
						{CIDR: "10.0.0.0/8", Ports: []int{5432}},
					},
				},
			},
		},
	}
	internal, version, err := apischeme.NormalizeSpace(input)
	if err != nil {
		t.Fatalf("NormalizeSpace: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Spec.Network == nil || internal.Spec.Network.Egress == nil {
		t.Fatalf("egress dropped in conversion: %+v", internal.Spec)
	}
	eg := internal.Spec.Network.Egress
	if string(eg.Default) != string(ext.EgressDefaultDeny) {
		t.Fatalf("default not preserved: %q", eg.Default)
	}
	if len(eg.Allow) != 2 {
		t.Fatalf("allow rule count: got %d, want 2", len(eg.Allow))
	}
	if eg.Allow[0].Host != "api.anthropic.com" || len(eg.Allow[0].Ports) != 1 || eg.Allow[0].Ports[0] != 443 {
		t.Fatalf("host rule not preserved: %+v", eg.Allow[0])
	}

	// Round-trip back to external.
	out, err := apischeme.BuildSpaceExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
	}
	if out.Spec.Network == nil || out.Spec.Network.Egress == nil {
		t.Fatalf("egress dropped on reverse conversion: %+v", out.Spec)
	}
	if len(out.Spec.Network.Egress.Allow) != 2 {
		t.Fatalf("allow rules lost on reverse conversion: %+v", out.Spec.Network.Egress)
	}
	// Confirm the YAML round-trip: re-serializing shouldn't blow up on
	// the new nested struct.
	if _, err = yaml.Marshal(out); err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
}

func TestSpaceEgressNilFieldsOmittedInYAML(t *testing.T) {
	// Minimal space with no network/egress — YAML must not render a
	// "network: null" line or expose the Egress pointer.
	input := ext.SpaceDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSpace,
		Metadata:   ext.SpaceMetadata{Name: "blog"},
		Spec:       ext.SpaceSpec{RealmID: "main"},
	}
	b, err := yaml.Marshal(input)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(b), "network:") {
		t.Fatalf("network field must be omitted when nil; got:\n%s", string(b))
	}
}

func TestCellRoundTripV1Beta1(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata: ext.CellMetadata{
			Name:   "cell0",
			Labels: map[string]string{"g": "h"},
		},
		Spec: ext.CellSpec{
			ID:              "cell-id-0",
			RealmID:         "realm0",
			SpaceID:         "space0",
			StackID:         "stack0",
			RootContainerID: "",
			Containers:      []ext.ContainerSpec{},
		},
		Status: ext.CellStatus{
			State:      ext.CellStatePending,
			CgroupPath: "/sys/fs/cgroup/cell0",
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "cell0" || internal.Spec.ID != "cell-id-0" || internal.Spec.RealmName != "realm0" ||
		internal.Spec.SpaceName != "space0" ||
		internal.Spec.StackName != "stack0" {
		t.Fatalf("unexpected internal cell: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.CellStateReady

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindCell {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.CellStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
}

// TestCellRoundTripV1Beta1_NetworkBridgeName covers AC for #168: the cell
// status persists Network.BridgeName through the external→internal→external
// round-trip so daemon restarts can recover the iface mapping from the
// cell metadata file alone.
func TestCellRoundTripV1Beta1_NetworkBridgeName(t *testing.T) {
	const wantBridge = "k-1a2b3c4d"

	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata: ext.CellMetadata{
			Name:   "cell-net",
			Labels: map[string]string{},
		},
		Spec: ext.CellSpec{
			ID:         "cell-id-net",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
		},
		Status: ext.CellStatus{
			State:      ext.CellStateReady,
			CgroupPath: "/sys/fs/cgroup/cell-net",
			Network: ext.CellNetworkStatus{
				BridgeName: wantBridge,
			},
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Status.Network.BridgeName != wantBridge {
		t.Errorf("internal bridge = %q, want %q", internal.Status.Network.BridgeName, wantBridge)
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if output.Status.Network.BridgeName != wantBridge {
		t.Errorf("external bridge = %q, want %q", output.Status.Network.BridgeName, wantBridge)
	}

	// The YAML rendering must include the bridgeName line so `kuke get
	// cell -o yaml` surfaces it for operators.
	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "bridgeName: "+wantBridge) {
		t.Errorf("rendered YAML missing bridgeName entry; got:\n%s", string(rendered))
	}
}

// TestCellRoundTripV1Beta1_ReadyObserved covers the persistence side of
// the AutoDelete Ready-gate from #269: the latch must round-trip
// external→internal→external so it survives daemon restarts. Without
// it, a `kuke run --rm` cell that was Ready at shutdown would lose the
// latch on restart and miss its cleanup tick.
func TestCellRoundTripV1Beta1_ReadyObserved(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata: ext.CellMetadata{
			Name:   "cell-rm",
			Labels: map[string]string{},
		},
		Spec: ext.CellSpec{
			ID:         "cell-id-rm",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
			AutoDelete: true,
		},
		Status: ext.CellStatus{
			State:         ext.CellStateReady,
			CgroupPath:    "/sys/fs/cgroup/cell-rm",
			ReadyObserved: true,
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if !internal.Status.ReadyObserved {
		t.Errorf("internal ReadyObserved = false, want true")
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !output.Status.ReadyObserved {
		t.Errorf("external ReadyObserved = false, want true")
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "readyObserved: true") {
		t.Errorf("rendered YAML missing readyObserved entry; got:\n%s", string(rendered))
	}
}

// TestCellRoundTripV1Beta1_AutoDelete locks down the AC for `kuke run --rm`:
// the AutoDelete bool must survive the external→internal→external round-trip
// and serialize as YAML so a daemon restart can re-read the auto-delete intent
// from the cell metadata file (the future #161 reconciliation loop's hook).
func TestCellRoundTripV1Beta1_AutoDelete(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata: ext.CellMetadata{
			Name:   "cell-rm",
			Labels: map[string]string{},
		},
		Spec: ext.CellSpec{
			ID:         "cell-id-rm",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			AutoDelete: true,
			Containers: []ext.ContainerSpec{},
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if !internal.Spec.AutoDelete {
		t.Errorf("internal AutoDelete = false, want true after Normalize")
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !output.Spec.AutoDelete {
		t.Errorf("external AutoDelete = false, want true after Build")
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "autoDelete: true") {
		t.Errorf("rendered YAML missing autoDelete entry; got:\n%s", string(rendered))
	}
}

// TestCellRoundTripV1Beta1_AutoDeleteOmitted ensures the field stays omitted
// from the YAML when unset — keeps existing manifests visually clean.
func TestCellRoundTripV1Beta1_AutoDeleteOmitted(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell-noop"},
		Spec: ext.CellSpec{
			ID:         "cell-id-noop",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
		},
	}

	rendered, err := yaml.Marshal(input)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "autoDelete:") {
		t.Errorf("autoDelete must be omitted when false; got:\n%s", string(rendered))
	}
}

// TestCellRoundTripV1Beta1_NestedCgroupRuntime locks down issue #314: the
// NestedCgroupRuntime opt-in must survive the external→internal→external
// round-trip and serialize as YAML so the daemon can re-derive the
// full-controller-delegation intent from the persisted cell metadata after
// a restart and re-apply it on the ensure-pass.
func TestCellRoundTripV1Beta1_NestedCgroupRuntime(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata: ext.CellMetadata{
			Name:   "cell-nested",
			Labels: map[string]string{},
		},
		Spec: ext.CellSpec{
			ID:                  "cell-id-nested",
			RealmID:             "realm0",
			SpaceID:             "space0",
			StackID:             "stack0",
			NestedCgroupRuntime: true,
			Containers:          []ext.ContainerSpec{},
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if !internal.Spec.NestedCgroupRuntime {
		t.Errorf("internal NestedCgroupRuntime = false, want true after Normalize")
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !output.Spec.NestedCgroupRuntime {
		t.Errorf("external NestedCgroupRuntime = false, want true after Build")
	}

	rendered, err := yaml.Marshal(output)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if !strings.Contains(string(rendered), "nestedCgroupRuntime: true") {
		t.Errorf("rendered YAML missing nestedCgroupRuntime entry; got:\n%s", string(rendered))
	}
}

// TestCellRoundTripV1Beta1_NestedCgroupRuntimeOmitted ensures the field is
// omitted from the YAML when unset — every existing cell manifest keeps the
// same on-disk shape and the opt-in is invisible to operators that do not
// need it.
func TestCellRoundTripV1Beta1_NestedCgroupRuntimeOmitted(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell-default"},
		Spec: ext.CellSpec{
			ID:         "cell-id-default",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			Containers: []ext.ContainerSpec{},
		},
	}

	rendered, err := yaml.Marshal(input)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	if strings.Contains(string(rendered), "nestedCgroupRuntime:") {
		t.Errorf("nestedCgroupRuntime must be omitted when false; got:\n%s", string(rendered))
	}
}

func TestContainerRoundTripV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata: ext.ContainerMetadata{
			Name:   "container0",
			Labels: map[string]string{"i": "j"},
		},
		Spec: ext.ContainerSpec{
			ID:      "container-id-0",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			CellID:  "cell0",
			Image:   "alpine:latest",
			Command: "sh",
			Args:    []string{"-c", "echo hello"},
			Env:     []string{"ENV_VAR=value"},
			Ports:   []string{"8080:80"},
			Volumes: []ext.VolumeMount{
				{Source: "/host/src", Target: "/container/dst"},
				{Source: "/host/ro", Target: "/container/ro", ReadOnly: true},
			},
			Networks: []string{"network0"},
		},
		Status: ext.ContainerStatus{
			State: ext.ContainerStatePending,
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "container0" || internal.Spec.ID != "container-id-0" ||
		internal.Spec.RealmName != "realm0" ||
		internal.Spec.SpaceName != "space0" ||
		internal.Spec.StackName != "stack0" ||
		internal.Spec.CellName != "cell0" ||
		internal.Spec.Image != "alpine:latest" {
		t.Fatalf("unexpected internal container: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.ContainerStateReady

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindContainer {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.ContainerStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
	if len(output.Spec.Volumes) != len(input.Spec.Volumes) {
		t.Fatalf("volumes len = %d, want %d", len(output.Spec.Volumes), len(input.Spec.Volumes))
	}
	for i, v := range input.Spec.Volumes {
		if output.Spec.Volumes[i] != v {
			t.Errorf("volume[%d] = %+v, want %+v", i, output.Spec.Volumes[i], v)
		}
	}
}

// TestContainerRoundTripReposV1Beta1 pins the repos[] spec and the per-repo
// status round trip: a YAML author's containers[].repos[] must survive
// Normalize → controller → Build with no drops, and the controller-populated
// ContainerStatus.Repos must survive Build out to the external doc. Issue #617.
func TestContainerRoundTripReposV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "work"},
		Spec: ext.ContainerSpec{
			ID:         "work",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			CellID:     "cell0",
			Image:      "alpine:latest",
			Attachable: true,
			Repos: []ext.ContainerRepo{
				{
					Name:     "project",
					Target:   "/home/claude/project",
					Branch:   "main",
					URL:      "git@example.com:org/p.git",
					Required: true,
				},
				{Name: "docs", Target: "/home/claude/docs", URL: "https://example.com/docs.git"},
			},
		},
		Status: ext.ContainerStatus{State: ext.ContainerStatePending},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer failed: %v", err)
	}
	if len(internal.Spec.Repos) != 2 {
		t.Fatalf("internal repos len = %d, want 2", len(internal.Spec.Repos))
	}
	if internal.Spec.Repos[0] != (intmodel.ContainerRepo{
		Name: "project", Target: "/home/claude/project", Branch: "main", URL: "git@example.com:org/p.git", Required: true,
	}) {
		t.Errorf("internal repo[0] = %+v", internal.Spec.Repos[0])
	}

	// Simulate the controller stamping per-repo status read back from kuketty.
	internal.Status.Repos = []intmodel.RepoStatus{
		{Name: "project", Target: "/home/claude/project", State: "cloned", Commit: "deadbeef"},
		{Name: "docs", Target: "/home/claude/docs", State: "failed", Error: "boom"},
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal failed: %v", err)
	}
	if len(output.Spec.Repos) != 2 {
		t.Fatalf("output repos len = %d, want 2", len(output.Spec.Repos))
	}
	for i := range input.Spec.Repos {
		if output.Spec.Repos[i] != input.Spec.Repos[i] {
			t.Errorf("repo[%d] = %+v, want %+v", i, output.Spec.Repos[i], input.Spec.Repos[i])
		}
	}
	if len(output.Status.Repos) != 2 {
		t.Fatalf("output status repos len = %d, want 2", len(output.Status.Repos))
	}
	if output.Status.Repos[0].State != "cloned" || output.Status.Repos[0].Commit != "deadbeef" {
		t.Errorf("status repo[0] = %+v", output.Status.Repos[0])
	}
	if output.Status.Repos[1].State != "failed" || output.Status.Repos[1].Error != "boom" {
		t.Errorf("status repo[1] = %+v", output.Status.Repos[1])
	}
}

// TestContainerRoundTripVolumeKindTmpfsV1Beta1 pins the tmpfs path on
// VolumeMount: a YAML author who writes `kind: tmpfs` with size/mode must see
// those fields survive Normalize → controller → Build with no drops, alongside
// a plain bind entry (empty Kind preserves bind back-compat).
func TestContainerRoundTripVolumeKindTmpfsV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata: ext.ContainerMetadata{
			Name: "container-tmpfs",
		},
		Spec: ext.ContainerSpec{
			ID:      "container-tmpfs",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			CellID:  "cell0",
			Image:   "alpine:latest",
			Volumes: []ext.VolumeMount{
				{Source: "/host/data", Target: "/data"},
				{
					Kind:      ext.VolumeKindTmpfs,
					Target:    "/var/lib/containerd",
					SizeBytes: 1 << 30,
					Mode:      0o0755,
				},
				{
					Kind:     ext.VolumeKindBind,
					Source:   "/host/ro",
					Target:   "/ro",
					ReadOnly: true,
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if got, want := len(internal.Spec.Volumes), len(input.Spec.Volumes); got != want {
		t.Fatalf("internal volumes len = %d, want %d", got, want)
	}
	if internal.Spec.Volumes[1].Kind != intmodel.VolumeKindTmpfs {
		t.Errorf("internal volume[1].Kind = %q, want %q",
			internal.Spec.Volumes[1].Kind, intmodel.VolumeKindTmpfs)
	}
	if internal.Spec.Volumes[1].SizeBytes != 1<<30 {
		t.Errorf("internal volume[1].SizeBytes = %d, want %d",
			internal.Spec.Volumes[1].SizeBytes, 1<<30)
	}
	if internal.Spec.Volumes[1].Mode != 0o0755 {
		t.Errorf("internal volume[1].Mode = %o, want %o",
			internal.Spec.Volumes[1].Mode, 0o0755)
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if got, want := len(output.Spec.Volumes), len(input.Spec.Volumes); got != want {
		t.Fatalf("output volumes len = %d, want %d", got, want)
	}
	for i, want := range input.Spec.Volumes {
		if output.Spec.Volumes[i] != want {
			t.Errorf("volume[%d] = %+v, want %+v",
				i, output.Spec.Volumes[i], want)
		}
	}
}

// TestCellRoundTripVolumeKindTmpfsV1Beta1 mirrors the standalone Container
// test for the nested-container path that `apply -f cell.yaml` and the
// CellProfile materializer travel: a tmpfs entry inside CellSpec.Containers[]
// must keep Kind / SizeBytes / Mode through Normalize → controller → Build.
func TestCellRoundTripVolumeKindTmpfsV1Beta1(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell-tmpfs"},
		Spec: ext.CellSpec{
			ID:      "cell-tmpfs",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			Containers: []ext.ContainerSpec{
				{
					ID:      "work",
					Image:   "registry.eminwux.com/busybox:latest",
					Command: "/bin/sh",
					Volumes: []ext.VolumeMount{
						{
							Kind:      ext.VolumeKindTmpfs,
							Target:    "/var/lib/containerd",
							SizeBytes: 512 * 1024 * 1024,
							Mode:      0o0700,
						},
					},
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if len(internal.Spec.Containers) != 1 || len(internal.Spec.Containers[0].Volumes) != 1 {
		t.Fatalf("internal nested volumes not carried: %+v", internal.Spec.Containers)
	}
	got := internal.Spec.Containers[0].Volumes[0]
	if got.Kind != intmodel.VolumeKindTmpfs ||
		got.Target != "/var/lib/containerd" ||
		got.SizeBytes != 512*1024*1024 ||
		got.Mode != 0o0700 {
		t.Errorf("internal nested tmpfs mismatch: %+v", got)
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if len(output.Spec.Containers) != 1 || len(output.Spec.Containers[0].Volumes) != 1 {
		t.Fatalf("nested volumes did not round-trip: %+v", output.Spec.Containers)
	}
	if output.Spec.Containers[0].Volumes[0] != input.Spec.Containers[0].Volumes[0] {
		t.Errorf("nested volume = %+v, want %+v",
			output.Spec.Containers[0].Volumes[0],
			input.Spec.Containers[0].Volumes[0])
	}
}

// TestCellRoundTripWorkingDirV1Beta1 covers the nested-ContainerSpec path:
// `apply -f cell.yaml` lands here (containers live inside CellDoc), so the
// per-container WorkingDir must survive Normalize → controller → Build with
// no fields dropped, just like the standalone Container round-trip.
func TestCellRoundTripWorkingDirV1Beta1(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell-wd"},
		Spec: ext.CellSpec{
			ID:      "cell-wd",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			Containers: []ext.ContainerSpec{
				{
					ID:         "work",
					Image:      "registry.eminwux.com/busybox:latest",
					Command:    "/bin/sh",
					WorkingDir: "/workspace",
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if len(internal.Spec.Containers) != 1 ||
		internal.Spec.Containers[0].WorkingDir != "/workspace" {
		t.Fatalf("internal nested WorkingDir not carried: %+v", internal.Spec.Containers)
	}

	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if len(output.Spec.Containers) != 1 ||
		output.Spec.Containers[0].WorkingDir != "/workspace" {
		t.Fatalf("nested WorkingDir did not round-trip: %+v", output.Spec.Containers)
	}
}

// TestContainerRoundTripWorkingDirV1Beta1 guards the apply -f round-trip for
// the workingDir field — a yaml/JSON producer must see the same value on the
// way back out so an `apply -f cell.yaml | get -o yaml` cycle does not silently
// drop it. Empty-in/empty-out is asserted in the Volumes round-trip above; this
// test pins the non-empty path.
func TestContainerRoundTripWorkingDirV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata: ext.ContainerMetadata{
			Name: "container-wd",
		},
		Spec: ext.ContainerSpec{
			ID:         "container-wd",
			RealmID:    "realm0",
			SpaceID:    "space0",
			StackID:    "stack0",
			CellID:     "cell0",
			Image:      "alpine:latest",
			WorkingDir: "/workspace",
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if internal.Spec.WorkingDir != "/workspace" {
		t.Fatalf("internal WorkingDir = %q, want %q", internal.Spec.WorkingDir, "/workspace")
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if output.Spec.WorkingDir != input.Spec.WorkingDir {
		t.Fatalf("WorkingDir did not round-trip: got %q, want %q",
			output.Spec.WorkingDir, input.Spec.WorkingDir)
	}
}

func TestContainerRoundTripSecurityFieldsV1Beta1(t *testing.T) {
	mem := int64(4 * 1024 * 1024 * 1024)
	shares := int64(512)
	pids := int64(256)
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata: ext.ContainerMetadata{
			Name: "container-sec",
		},
		Spec: ext.ContainerSpec{
			ID:                     "container-sec",
			RealmID:                "realm0",
			SpaceID:                "space0",
			StackID:                "stack0",
			CellID:                 "cell0",
			Image:                  "alpine:latest",
			User:                   "1000:1000",
			ReadOnlyRootFilesystem: true,
			Capabilities: &ext.ContainerCapabilities{
				Drop: []string{"ALL"},
				Add:  []string{"NET_ADMIN"},
			},
			SecurityOpts: []string{"no-new-privileges", "seccomp=unconfined"},
			Tmpfs: []ext.ContainerTmpfsMount{
				{Path: "/tmp", SizeBytes: 64 * 1024 * 1024, Options: []string{"mode=1777"}},
			},
			Resources: &ext.ContainerResources{
				MemoryLimitBytes: &mem,
				CPUShares:        &shares,
				PidsLimit:        &pids,
			},
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if internal.Spec.User != "1000:1000" || !internal.Spec.ReadOnlyRootFilesystem {
		t.Fatalf("user/readOnly not carried: %+v", internal.Spec)
	}
	if internal.Spec.Capabilities == nil ||
		len(internal.Spec.Capabilities.Drop) != 1 ||
		internal.Spec.Capabilities.Drop[0] != "ALL" ||
		internal.Spec.Capabilities.Add[0] != "NET_ADMIN" {
		t.Fatalf("capabilities not carried: %+v", internal.Spec.Capabilities)
	}
	if len(internal.Spec.SecurityOpts) != 2 || internal.Spec.SecurityOpts[0] != "no-new-privileges" {
		t.Fatalf("securityOpts not carried: %+v", internal.Spec.SecurityOpts)
	}
	if len(internal.Spec.Tmpfs) != 1 || internal.Spec.Tmpfs[0].Path != "/tmp" ||
		internal.Spec.Tmpfs[0].SizeBytes != 64*1024*1024 {
		t.Fatalf("tmpfs not carried: %+v", internal.Spec.Tmpfs)
	}
	if internal.Spec.Resources == nil || internal.Spec.Resources.MemoryLimitBytes == nil ||
		*internal.Spec.Resources.MemoryLimitBytes != mem {
		t.Fatalf("resources not carried: %+v", internal.Spec.Resources)
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if output.Spec.User != input.Spec.User ||
		output.Spec.ReadOnlyRootFilesystem != input.Spec.ReadOnlyRootFilesystem {
		t.Fatalf("user/readOnly did not round-trip: %+v", output.Spec)
	}
	if output.Spec.Capabilities == nil || output.Spec.Capabilities.Drop[0] != "ALL" ||
		output.Spec.Capabilities.Add[0] != "NET_ADMIN" {
		t.Fatalf("capabilities did not round-trip: %+v", output.Spec.Capabilities)
	}
	if len(output.Spec.SecurityOpts) != 2 {
		t.Fatalf("securityOpts did not round-trip: %+v", output.Spec.SecurityOpts)
	}
	if len(output.Spec.Tmpfs) != 1 || output.Spec.Tmpfs[0].Path != "/tmp" ||
		output.Spec.Tmpfs[0].SizeBytes != 64*1024*1024 {
		t.Fatalf("tmpfs did not round-trip: %+v", output.Spec.Tmpfs)
	}
	if output.Spec.Resources == nil || output.Spec.Resources.MemoryLimitBytes == nil ||
		*output.Spec.Resources.MemoryLimitBytes != mem {
		t.Fatalf("resources did not round-trip: %+v", output.Spec.Resources)
	}
}

func TestContainerSecretsRoundTripV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "container-secrets"},
		Spec: ext.ContainerSpec{
			ID:      "container-secrets",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			CellID:  "cell0",
			Image:   "alpine:latest",
			Secrets: []ext.ContainerSecret{
				{Name: "ANTHROPIC_API_KEY", FromFile: "/etc/kukeon/secrets/anthropic.key"},
				{Name: "GITHUB_TOKEN", FromEnv: "GITHUB_TOKEN_SCOPED"},
				{
					Name:      "tls.crt",
					FromFile:  "/etc/kukeon/secrets/tls.crt",
					MountPath: "/run/secrets/tls.crt",
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if len(internal.Spec.Secrets) != 3 {
		t.Fatalf("expected 3 internal secrets, got %d", len(internal.Spec.Secrets))
	}
	if internal.Spec.Secrets[0].Name != "ANTHROPIC_API_KEY" ||
		internal.Spec.Secrets[0].FromFile != "/etc/kukeon/secrets/anthropic.key" ||
		internal.Spec.Secrets[0].FromEnv != "" ||
		internal.Spec.Secrets[0].MountPath != "" {
		t.Fatalf("secret[0] did not normalize: %+v", internal.Spec.Secrets[0])
	}
	if internal.Spec.Secrets[2].MountPath != "/run/secrets/tls.crt" {
		t.Fatalf("secret[2] mountPath did not normalize: %+v", internal.Spec.Secrets[2])
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if len(output.Spec.Secrets) != len(input.Spec.Secrets) {
		t.Fatalf(
			"secrets len = %d, want %d",
			len(output.Spec.Secrets),
			len(input.Spec.Secrets),
		)
	}
	for i, want := range input.Spec.Secrets {
		got := output.Spec.Secrets[i]
		if got != want {
			t.Errorf("secret[%d] round-trip = %+v, want %+v", i, got, want)
		}
	}
}

// TestContainerSecretRefRoundTripV1Beta1 ensures the secretRef source survives
// external→internal→external conversion, including a deep-copy of the pointer
// (not a shared referent). Issue #623.
func TestContainerSecretRefRoundTripV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "container-secretref"},
		Spec: ext.ContainerSpec{
			ID:      "container-secretref",
			RealmID: "realm0",
			SpaceID: "space0",
			StackID: "stack0",
			CellID:  "cell0",
			Image:   "alpine:latest",
			Secrets: []ext.ContainerSecret{
				{
					Name:      "ANTHROPIC_AUTH_TOKEN",
					SecretRef: &ext.ContainerSecretRef{Name: "anthropic-token", Realm: "kuke-system"},
				},
				{
					Name: "tls.crt",
					SecretRef: &ext.ContainerSecretRef{
						Name:  "tls-cert",
						Realm: "default",
						Space: "ai",
						Stack: "agents",
						Cell:  "claude",
					},
					MountPath: "/run/secrets/tls.crt",
				},
			},
		},
	}

	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if internal.Spec.Secrets[0].SecretRef == nil ||
		internal.Spec.Secrets[0].SecretRef.Name != "anthropic-token" ||
		internal.Spec.Secrets[0].SecretRef.Realm != "kuke-system" {
		t.Fatalf("secret[0] secretRef did not normalize: %+v", internal.Spec.Secrets[0].SecretRef)
	}
	if internal.Spec.Secrets[1].SecretRef == nil ||
		internal.Spec.Secrets[1].SecretRef.Cell != "claude" {
		t.Fatalf("secret[1] secretRef did not normalize: %+v", internal.Spec.Secrets[1].SecretRef)
	}

	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	for i, want := range input.Spec.Secrets {
		got := output.Spec.Secrets[i]
		if got.Name != want.Name || got.MountPath != want.MountPath {
			t.Errorf("secret[%d] round-trip = %+v, want %+v", i, got, want)
		}
		if got.SecretRef == nil || *got.SecretRef != *want.SecretRef {
			t.Errorf("secret[%d] secretRef round-trip = %+v, want %+v", i, got.SecretRef, want.SecretRef)
		}
		if got.SecretRef == want.SecretRef {
			t.Errorf("secret[%d] secretRef pointer not deep-copied", i)
		}
	}
}

// TestContainerAttachableRoundTrips ensures the new Attachable field
// survives both directions of conversion (external→internal→external) and
// across the nested cell-spec converters used when a Container appears
// inside a CellSpec.
func TestContainerAttachableRoundTrips(t *testing.T) {
	cases := []bool{false, true}
	for _, want := range cases {
		input := ext.ContainerDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindContainer,
			Metadata:   ext.ContainerMetadata{Name: "c"},
			Spec: ext.ContainerSpec{
				ID:         "c",
				RealmID:    "r",
				SpaceID:    "s",
				StackID:    "st",
				CellID:     "cl",
				Image:      "alpine:latest",
				Attachable: want,
			},
		}
		internal, version, err := apischeme.NormalizeContainer(input)
		if err != nil {
			t.Fatalf("NormalizeContainer(%v): %v", want, err)
		}
		if internal.Spec.Attachable != want {
			t.Errorf("internal.Spec.Attachable = %v, want %v", internal.Spec.Attachable, want)
		}
		out, err := apischeme.BuildContainerExternalFromInternal(internal, version)
		if err != nil {
			t.Fatalf("BuildContainerExternalFromInternal: %v", err)
		}
		if out.Spec.Attachable != want {
			t.Errorf("round-trip ext.Spec.Attachable = %v, want %v", out.Spec.Attachable, want)
		}
	}
}

// TestContainerTtyRoundTripV1Beta1 covers the AC that a populated tty block
// on an attachable container survives ConvertContainerDocToInternal +
// BuildContainerExternalFromInternal with no fields dropped.
func TestContainerTtyRoundTripV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "c"},
		Spec: ext.ContainerSpec{
			ID:      "c",
			RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
			Image:      "alpine:latest",
			Attachable: true,
			// Bind mount satisfies the persistence guard for the
			// runOn: create stage below (issue #738).
			Volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindBind, Source: "/srv/cache", Target: "/cache"},
			},
			Tty: &ext.ContainerTty{
				Prompt: `"\[\e[1;36m\]claude \u@\h\[\e[0m\]:\w\$ "`,
				OnInit: []ext.TtyStage{
					{Script: "git pull"},
					{Script: "npm ci", RunOn: ext.RunOnCreate},
					{Script: "claude", RunOn: ext.RunOnStart},
				},
				LogFile:  "/run/kukeon/tty/custom.log",
				LogLevel: "debug",
			},
		},
	}
	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	if internal.Spec.Tty == nil {
		t.Fatalf("internal.Spec.Tty = nil, want populated")
	}
	if internal.Spec.Tty.Prompt != input.Spec.Tty.Prompt {
		t.Errorf("internal prompt = %q, want %q", internal.Spec.Tty.Prompt, input.Spec.Tty.Prompt)
	}
	if internal.Spec.Tty.LogFile != input.Spec.Tty.LogFile {
		t.Errorf("internal logFile = %q, want %q", internal.Spec.Tty.LogFile, input.Spec.Tty.LogFile)
	}
	if internal.Spec.Tty.LogLevel != input.Spec.Tty.LogLevel {
		t.Errorf("internal logLevel = %q, want %q", internal.Spec.Tty.LogLevel, input.Spec.Tty.LogLevel)
	}
	if len(internal.Spec.Tty.OnInit) != 3 ||
		internal.Spec.Tty.OnInit[0].Script != "git pull" ||
		internal.Spec.Tty.OnInit[1].Script != "npm ci" ||
		internal.Spec.Tty.OnInit[1].RunOn != ext.RunOnCreate ||
		internal.Spec.Tty.OnInit[2].Script != "claude" ||
		internal.Spec.Tty.OnInit[2].RunOn != ext.RunOnStart {
		t.Errorf("internal onInit = %+v, want [git pull, npm ci/create, claude/start]", internal.Spec.Tty.OnInit)
	}
	out, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if out.Spec.Tty == nil {
		t.Fatalf("round-trip dropped tty block: %+v", out.Spec)
	}
	if out.Spec.Tty.Prompt != input.Spec.Tty.Prompt {
		t.Errorf("round-trip prompt = %q, want %q", out.Spec.Tty.Prompt, input.Spec.Tty.Prompt)
	}
	if out.Spec.Tty.LogFile != input.Spec.Tty.LogFile {
		t.Errorf("round-trip logFile = %q, want %q", out.Spec.Tty.LogFile, input.Spec.Tty.LogFile)
	}
	if out.Spec.Tty.LogLevel != input.Spec.Tty.LogLevel {
		t.Errorf("round-trip logLevel = %q, want %q", out.Spec.Tty.LogLevel, input.Spec.Tty.LogLevel)
	}
	if len(out.Spec.Tty.OnInit) != len(input.Spec.Tty.OnInit) {
		t.Fatalf("round-trip onInit len = %d, want %d", len(out.Spec.Tty.OnInit), len(input.Spec.Tty.OnInit))
	}
	for i, s := range input.Spec.Tty.OnInit {
		if out.Spec.Tty.OnInit[i].Script != s.Script {
			t.Errorf("round-trip onInit[%d].Script = %q, want %q", i, out.Spec.Tty.OnInit[i].Script, s.Script)
		}
		if out.Spec.Tty.OnInit[i].RunOn != s.RunOn {
			t.Errorf("round-trip onInit[%d].RunOn = %q, want %q", i, out.Spec.Tty.OnInit[i].RunOn, s.RunOn)
		}
	}
}

// TestCellTtyRoundTripV1Beta1 covers the AC that a populated cell-level tty
// block round-trips intact, with cell.tty.default referencing an attachable
// container that exists in the same cell.
func TestCellTtyRoundTripV1Beta1(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell-tty"},
		Spec: ext.CellSpec{
			ID:      "cell-tty",
			RealmID: "r", SpaceID: "s", StackID: "st",
			Tty: &ext.CellTty{Default: "work"},
			Containers: []ext.ContainerSpec{
				{
					ID:         "work",
					RealmID:    "r",
					SpaceID:    "s",
					StackID:    "st",
					CellID:     "cell-tty",
					Image:      "alpine:latest",
					Attachable: true,
					Tty: &ext.ContainerTty{
						Prompt: `"\u@\h:\w\$ "`,
						OnInit: []ext.TtyStage{{Script: "echo hi"}},
					},
				},
			},
		},
	}
	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Spec.Tty == nil || internal.Spec.Tty.Default != "work" {
		t.Fatalf("internal cell tty = %+v, want default=work", internal.Spec.Tty)
	}
	if len(internal.Spec.Containers) != 1 || internal.Spec.Containers[0].Tty == nil {
		t.Fatalf("internal nested container tty dropped: %+v", internal.Spec.Containers)
	}
	if internal.Spec.Containers[0].Tty.Prompt == "" ||
		len(internal.Spec.Containers[0].Tty.OnInit) != 1 {
		t.Errorf("internal nested container tty fields wrong: %+v", internal.Spec.Containers[0].Tty)
	}
	out, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if out.Spec.Tty == nil || out.Spec.Tty.Default != "work" {
		t.Fatalf("round-trip cell tty = %+v, want default=work", out.Spec.Tty)
	}
	if len(out.Spec.Containers) != 1 || out.Spec.Containers[0].Tty == nil ||
		out.Spec.Containers[0].Tty.Prompt != input.Spec.Containers[0].Tty.Prompt {
		t.Fatalf("round-trip nested container tty did not survive: %+v", out.Spec.Containers[0].Tty)
	}
}

// TestCellTtyZeroBlockOmittedOnRoundTrip confirms an absent cell.tty does
// not turn into an empty `tty: {}` block on the output side. Distinguishes
// the user-supplied "block omitted" case from "block present but empty".
func TestCellTtyZeroBlockOmittedOnRoundTrip(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell"},
		Spec: ext.CellSpec{
			ID:      "cell",
			RealmID: "r", SpaceID: "s", StackID: "st",
		},
	}
	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Spec.Tty != nil {
		t.Errorf("internal cell tty = %+v, want nil for absent block", internal.Spec.Tty)
	}
	out, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if out.Spec.Tty != nil {
		t.Errorf("round-trip injected cell tty = %+v, want nil", out.Spec.Tty)
	}
}

// TestContainerTtyRejectedWithoutAttachable enforces the AC that any tty
// field set with attachable=false is a validation error.
func TestContainerTtyRejectedWithoutAttachable(t *testing.T) {
	cases := []struct {
		name string
		tty  *ext.ContainerTty
	}{
		{"prompt only", &ext.ContainerTty{Prompt: `"\u\$ "`}},
		{"onInit only", &ext.ContainerTty{OnInit: []ext.TtyStage{{Script: "echo"}}}},
		{"logFile only", &ext.ContainerTty{LogFile: "/run/kukeon/tty/log"}},
		{"logLevel only", &ext.ContainerTty{LogLevel: "debug"}},
		{"all set", &ext.ContainerTty{
			Prompt:   `"\u\$ "`,
			OnInit:   []ext.TtyStage{{Script: "echo"}},
			LogFile:  "/run/kukeon/tty/log",
			LogLevel: "debug",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec: ext.ContainerSpec{
					ID:      "c",
					RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
					Image:      "alpine:latest",
					Attachable: false,
					Tty:        tc.tty,
				},
			}
			if _, _, err := apischeme.NormalizeContainer(input); err == nil {
				t.Fatalf("NormalizeContainer accepted tty with attachable=false; want error")
			}
		})
	}
}

// TestContainerTtyLogLevelEnumValidation enforces the AC that
// Tty.LogLevel only accepts the empty string (defaults to "info" daemon-
// side) or one of debug/info/warn/error. Unknown values are rejected at
// apply time rather than silently coerced — the kuketty wrapper's debug
// log is the operator's primary diagnostic when an attach session
// misbehaves and a typo'd level must not silently lose verbosity. Issue
// #599.
func TestContainerTtyLogLevelEnumValidation(t *testing.T) {
	accepted := []string{"", "debug", "info", "warn", "error"}
	for _, level := range accepted {
		t.Run("accepted/"+level, func(t *testing.T) {
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec: ext.ContainerSpec{
					ID:      "c",
					RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
					Image:      "alpine:latest",
					Attachable: true,
					Tty:        &ext.ContainerTty{LogLevel: level},
				},
			}
			if _, _, err := apischeme.NormalizeContainer(input); err != nil {
				t.Fatalf("NormalizeContainer rejected level %q: %v", level, err)
			}
		})
	}
	rejected := []string{"trace", "DEBUG", "verbose", "fatal", "warning", "nope"}
	for _, level := range rejected {
		t.Run("rejected/"+level, func(t *testing.T) {
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec: ext.ContainerSpec{
					ID:      "c",
					RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
					Image:      "alpine:latest",
					Attachable: true,
					Tty:        &ext.ContainerTty{LogLevel: level},
				},
			}
			if _, _, err := apischeme.NormalizeContainer(input); err == nil {
				t.Fatalf("NormalizeContainer accepted bogus level %q; want validation error", level)
			}
		})
	}
}

// TestContainerTtyStageRunOnEnumValidation enforces the AC that a stage's
// runOn only accepts the empty string (treated as "start"), "start", or
// "create". An unknown value (typically a typo like "craete") is rejected at
// apply time rather than silently routed to the start lane and re-run on every
// boot. Issue #635.
func TestContainerTtyStageRunOnEnumValidation(t *testing.T) {
	accepted := []string{"", ext.RunOnStart, ext.RunOnCreate}
	for _, runOn := range accepted {
		t.Run("accepted/"+runOn, func(t *testing.T) {
			spec := ext.ContainerSpec{
				ID:      "c",
				RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
				Image:      "alpine:latest",
				Attachable: true,
				Tty:        &ext.ContainerTty{OnInit: []ext.TtyStage{{Script: "echo hi", RunOn: runOn}}},
			}
			// runOn: create requires a persistent writable mount per the
			// phase-C3 validation guard (issue #738); add one so this
			// subtest exercises only the enum check.
			if runOn == ext.RunOnCreate {
				spec.Volumes = []ext.VolumeMount{
					{Kind: ext.VolumeKindBind, Source: "/srv/cache", Target: "/cache"},
				}
			}
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec:       spec,
			}
			if _, _, err := apischeme.NormalizeContainer(input); err != nil {
				t.Fatalf("NormalizeContainer rejected runOn %q: %v", runOn, err)
			}
		})
	}
	rejected := []string{"craete", "START", "Create", "boot", "once", "delete"}
	for _, runOn := range rejected {
		t.Run("rejected/"+runOn, func(t *testing.T) {
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec: ext.ContainerSpec{
					ID:      "c",
					RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
					Image:      "alpine:latest",
					Attachable: true,
					Tty:        &ext.ContainerTty{OnInit: []ext.TtyStage{{Script: "echo hi", RunOn: runOn}}},
				},
			}
			if _, _, err := apischeme.NormalizeContainer(input); err == nil {
				t.Fatalf("NormalizeContainer accepted bogus runOn %q; want validation error", runOn)
			}
		})
	}
}

// TestContainerStatusStagesRoundTrip pins the ContainerStatus.Stages schema
// (#635): stage status stamped on the internal model survives the build back
// to v1beta1. Schema only this phase; phase B (#689) populates it over RPC.
func TestContainerStatusStagesRoundTrip(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "c"},
		Spec: ext.ContainerSpec{
			ID:      "c",
			RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
			Image: "alpine:latest",
		},
	}
	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	internal.Status.Stages = []intmodel.StageStatus{
		{Index: 1, State: "ran", Hash: "abc1234567890def"},
		{Index: 3, State: "failed", Error: "boom"},
	}
	out, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if len(out.Status.Stages) != 2 {
		t.Fatalf("output status stages len = %d, want 2", len(out.Status.Stages))
	}
	// Hash round-trips end-to-end (phase C1, #690): the durable run-once key
	// the controller stamps must survive the internal -> external build so
	// `kuke get container -o yaml` renders it.
	if out.Status.Stages[0] != (ext.StageStatus{Index: 1, State: "ran", Hash: "abc1234567890def"}) {
		t.Errorf("status stage[0] = %+v", out.Status.Stages[0])
	}
	if out.Status.Stages[1] != (ext.StageStatus{Index: 3, State: "failed", Error: "boom"}) {
		t.Errorf("status stage[1] = %+v", out.Status.Stages[1])
	}
}

// TestContainerTtyEmptyBlockAcceptedWithoutAttachable confirms that an
// explicitly empty tty block (`tty: {}`) on a non-attachable container is
// equivalent to omitting the block — the validator must not reject it.
func TestContainerTtyEmptyBlockAcceptedWithoutAttachable(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "c"},
		Spec: ext.ContainerSpec{
			ID:      "c",
			RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
			Image:      "alpine:latest",
			Attachable: false,
			Tty:        &ext.ContainerTty{},
		},
	}
	if _, _, err := apischeme.NormalizeContainer(input); err != nil {
		t.Fatalf("NormalizeContainer rejected empty tty block: %v", err)
	}
}

// TestCellTtyDefaultValidation enforces the AC that CellTty.Default must
// reference an existing attachable container in the same cell. Three cases:
// missing reference, non-attachable reference, valid reference.
func TestCellTtyDefaultValidation(t *testing.T) {
	makeCell := func(defaultName string, containers []ext.ContainerSpec) ext.CellDoc {
		return ext.CellDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindCell,
			Metadata:   ext.CellMetadata{Name: "cell"},
			Spec: ext.CellSpec{
				ID:      "cell",
				RealmID: "r", SpaceID: "s", StackID: "st",
				Tty:        &ext.CellTty{Default: defaultName},
				Containers: containers,
			},
		}
	}

	t.Run("default refs missing container", func(t *testing.T) {
		doc := makeCell("ghost", []ext.ContainerSpec{
			{ID: "work", Image: "alpine:latest", Attachable: true},
		})
		if _, _, err := apischeme.NormalizeCell(doc); err == nil {
			t.Fatalf("NormalizeCell accepted unknown tty.default; want error")
		}
	})

	t.Run("default refs non-attachable container", func(t *testing.T) {
		doc := makeCell("plain", []ext.ContainerSpec{
			{ID: "plain", Image: "alpine:latest", Attachable: false},
		})
		if _, _, err := apischeme.NormalizeCell(doc); err == nil {
			t.Fatalf("NormalizeCell accepted tty.default on non-attachable; want error")
		}
	})

	t.Run("default refs attachable container", func(t *testing.T) {
		doc := makeCell("work", []ext.ContainerSpec{
			{ID: "work", Image: "alpine:latest", Attachable: true},
		})
		if _, _, err := apischeme.NormalizeCell(doc); err != nil {
			t.Fatalf("NormalizeCell rejected valid tty.default: %v", err)
		}
	})

	t.Run("default empty is allowed", func(t *testing.T) {
		doc := makeCell("", []ext.ContainerSpec{
			{ID: "work", Image: "alpine:latest", Attachable: true},
		})
		if _, _, err := apischeme.NormalizeCell(doc); err != nil {
			t.Fatalf("NormalizeCell rejected empty tty.default: %v", err)
		}
	})
}

// TestCellRejectsNestedContainerTtyWithoutAttachable confirms the
// per-container validation also fires for containers carried inside a
// CellSpec (not just a standalone ContainerDoc).
func TestCellRejectsNestedContainerTtyWithoutAttachable(t *testing.T) {
	doc := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell"},
		Spec: ext.CellSpec{
			ID:      "cell",
			RealmID: "r", SpaceID: "s", StackID: "st",
			Containers: []ext.ContainerSpec{
				{
					ID:         "broken",
					Image:      "alpine:latest",
					Attachable: false,
					Tty:        &ext.ContainerTty{Prompt: `"\u\$ "`},
				},
			},
		},
	}
	if _, _, err := apischeme.NormalizeCell(doc); err == nil {
		t.Fatalf("NormalizeCell accepted nested tty with attachable=false; want error")
	}
}

// TestContainerSecretYAMLNeverLeaksValues ensures that a round-trip through
// the external doc + YAML marshal path only serializes the reference fields,
// never a resolved secret value. The internal model has no value field, so a
// serialized container doc can only ever contain name + source metadata.
func TestContainerSecretYAMLNeverLeaksValues(t *testing.T) {
	internal := intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "c"},
		Spec: intmodel.ContainerSpec{
			ID:        "c",
			RealmName: "r", SpaceName: "s", StackName: "k", CellName: "cl",
			Image: "alpine:latest",
			Secrets: []intmodel.ContainerSecret{
				{Name: "API_KEY", FromEnv: "API_KEY_SRC"},
				{Name: "tls.crt", FromFile: "/etc/kukeon/secrets/tls.crt", MountPath: "/run/s/tls.crt"},
			},
		},
	}

	doc, err := apischeme.BuildContainerExternalFromInternal(internal, ext.APIVersionV1Beta1)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	rendered := string(out)

	// The external secret struct only has name/fromFile/fromEnv/mountPath —
	// any value-carrying field would appear here. Fail loudly if that
	// invariant ever regresses. "data:" is omitted because "metadata:"
	// contains it as a substring; "value:" and "contents:" are the keys we
	// would expect to see if someone added a value-bearing field.
	for _, forbidden := range []string{"value:", "contents:"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("rendered YAML contains forbidden key %q; full doc:\n%s", forbidden, rendered)
		}
	}
}

// TestSubtreeControllersRoundTripV1Beta1 pins the issue #328 plumbing: the
// new Status.SubtreeControllers field must round-trip in both directions on
// realm/space/stack/cell. Covered together because the field has identical
// semantics on every level.
func TestSubtreeControllersRoundTripV1Beta1(t *testing.T) {
	want := []string{"cpu", "memory", "io", "pids"}

	t.Run("realm", func(t *testing.T) {
		ext2int, err := apischeme.ConvertRealmDocToInternal(ext.RealmDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata:   ext.RealmMetadata{Name: "r0"},
			Status:     ext.RealmStatus{SubtreeControllers: want},
		})
		if err != nil {
			t.Fatalf("ConvertRealmDocToInternal: %v", err)
		}
		if !reflect.DeepEqual(ext2int.Status.SubtreeControllers, want) {
			t.Errorf("ext→int realm SubtreeControllers = %v, want %v",
				ext2int.Status.SubtreeControllers, want)
		}
		out, err := apischeme.BuildRealmExternalFromInternal(ext2int, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildRealmExternalFromInternal: %v", err)
		}
		if !reflect.DeepEqual(out.Status.SubtreeControllers, want) {
			t.Errorf("int→ext realm SubtreeControllers = %v, want %v",
				out.Status.SubtreeControllers, want)
		}
		// Aliasing guard: mutating the converted slice must not bleed into
		// the source. cloneStringSlice is what enforces this.
		out.Status.SubtreeControllers[0] = "MUTATED"
		if ext2int.Status.SubtreeControllers[0] != "cpu" {
			t.Errorf("realm SubtreeControllers aliased internal slice (got %q)",
				ext2int.Status.SubtreeControllers[0])
		}
	})

	t.Run("space", func(t *testing.T) {
		ext2int, err := apischeme.ConvertSpaceDocToInternal(ext.SpaceDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindSpace,
			Metadata:   ext.SpaceMetadata{Name: "s0"},
			Spec:       ext.SpaceSpec{RealmID: "r0"},
			Status:     ext.SpaceStatus{SubtreeControllers: want},
		})
		if err != nil {
			t.Fatalf("ConvertSpaceDocToInternal: %v", err)
		}
		if !reflect.DeepEqual(ext2int.Status.SubtreeControllers, want) {
			t.Errorf("ext→int space SubtreeControllers = %v, want %v",
				ext2int.Status.SubtreeControllers, want)
		}
		out, err := apischeme.BuildSpaceExternalFromInternal(ext2int, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
		}
		if !reflect.DeepEqual(out.Status.SubtreeControllers, want) {
			t.Errorf("int→ext space SubtreeControllers = %v, want %v",
				out.Status.SubtreeControllers, want)
		}
	})

	t.Run("stack", func(t *testing.T) {
		ext2int, err := apischeme.ConvertStackDocToInternal(ext.StackDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindStack,
			Metadata:   ext.StackMetadata{Name: "st0"},
			Spec:       ext.StackSpec{ID: "st0", RealmID: "r0", SpaceID: "s0"},
			Status:     ext.StackStatus{SubtreeControllers: want},
		})
		if err != nil {
			t.Fatalf("ConvertStackDocToInternal: %v", err)
		}
		if !reflect.DeepEqual(ext2int.Status.SubtreeControllers, want) {
			t.Errorf("ext→int stack SubtreeControllers = %v, want %v",
				ext2int.Status.SubtreeControllers, want)
		}
		out, err := apischeme.BuildStackExternalFromInternal(ext2int, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildStackExternalFromInternal: %v", err)
		}
		if !reflect.DeepEqual(out.Status.SubtreeControllers, want) {
			t.Errorf("int→ext stack SubtreeControllers = %v, want %v",
				out.Status.SubtreeControllers, want)
		}
	})

	t.Run("cell", func(t *testing.T) {
		ext2int, err := apischeme.ConvertCellDocToInternal(ext.CellDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindCell,
			Metadata:   ext.CellMetadata{Name: "c0"},
			Spec:       ext.CellSpec{ID: "c0", RealmID: "r0", SpaceID: "s0", StackID: "st0"},
			Status:     ext.CellStatus{SubtreeControllers: want},
		})
		if err != nil {
			t.Fatalf("ConvertCellDocToInternal: %v", err)
		}
		if !reflect.DeepEqual(ext2int.Status.SubtreeControllers, want) {
			t.Errorf("ext→int cell SubtreeControllers = %v, want %v",
				ext2int.Status.SubtreeControllers, want)
		}
		out, err := apischeme.BuildCellExternalFromInternal(ext2int, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildCellExternalFromInternal: %v", err)
		}
		if !reflect.DeepEqual(out.Status.SubtreeControllers, want) {
			t.Errorf("int→ext cell SubtreeControllers = %v, want %v",
				out.Status.SubtreeControllers, want)
		}
	})

	// nil-on-empty: an empty-input Status must produce a nil slice in the
	// converted form so `omitempty` keeps the field out of YAML/JSON.
	t.Run("empty stays nil", func(t *testing.T) {
		ext2int, err := apischeme.ConvertRealmDocToInternal(ext.RealmDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata:   ext.RealmMetadata{Name: "r0"},
		})
		if err != nil {
			t.Fatalf("ConvertRealmDocToInternal: %v", err)
		}
		if ext2int.Status.SubtreeControllers != nil {
			t.Errorf("empty input produced non-nil internal slice: %v",
				ext2int.Status.SubtreeControllers)
		}
		out, err := apischeme.BuildRealmExternalFromInternal(ext2int, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildRealmExternalFromInternal: %v", err)
		}
		if out.Status.SubtreeControllers != nil {
			t.Errorf("empty input produced non-nil external slice: %v",
				out.Status.SubtreeControllers)
		}
	})
}

// TestCellRootContainerIDAutoDerivedFromRootFlag covers issue #349: a Cell
// whose YAML places root: true on a container in spec.containers[] but does
// not set spec.rootContainerId must have RootContainerID auto-populated by
// normalization. Without this, the runner's empty-RootContainerID branch in
// ensureCellRootContainerSpec builds a default root spec and the user's
// declared volumes (and any other container fields) silently disappear.
func TestCellRootContainerIDAutoDerivedFromRootFlag(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "tmpfs-smoke"},
		Spec: ext.CellSpec{
			ID:      "tmpfs-smoke",
			RealmID: "default",
			SpaceID: "default",
			StackID: "default",
			Containers: []ext.ContainerSpec{
				{
					ID:    "root",
					Root:  true,
					Image: "registry.eminwux.com/busybox:latest",
					Volumes: []ext.VolumeMount{
						{Kind: "tmpfs", Target: "/var/lib/cache", SizeBytes: 16 * 1024 * 1024},
					},
				},
			},
		},
	}

	internal, _, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if got, want := internal.Spec.RootContainerID, "root"; got != want {
		t.Fatalf("RootContainerID = %q, want %q (auto-derive from Root:true)", got, want)
	}
	if len(internal.Spec.Containers) != 1 {
		t.Fatalf("containers len = %d, want 1", len(internal.Spec.Containers))
	}
	c := internal.Spec.Containers[0]
	if !c.Root {
		t.Errorf("Root flag dropped from container after normalize")
	}
	if len(c.Volumes) != 1 || c.Volumes[0].Target != "/var/lib/cache" {
		t.Errorf("user volume dropped during normalize: %+v", c.Volumes)
	}
}

// TestCellRootContainerIDLeftEmptyWhenNoRootFlag confirms the auto-derive
// path is opt-in: a cell with no rootContainerId and no container marked
// root: true keeps RootContainerID empty so the runner builds a default
// root spec. This matches the kuke-system / system-realm pattern.
func TestCellRootContainerIDLeftEmptyWhenNoRootFlag(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "no-root"},
		Spec: ext.CellSpec{
			ID:      "no-root",
			RealmID: "default",
			SpaceID: "default",
			StackID: "default",
			Containers: []ext.ContainerSpec{
				{ID: "worker", Image: "registry.eminwux.com/busybox:latest"},
			},
		},
	}

	internal, _, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Spec.RootContainerID != "" {
		t.Fatalf("RootContainerID = %q, want \"\"", internal.Spec.RootContainerID)
	}
}

// TestCellRejectsMultipleRootTrueContainers asserts the multi-Root rule
// from #349: at most one container in a cell may have root: true.
// Otherwise either the second one's Root intent silently drops or we
// pick a winner the user did not name — both are foot-guns.
func TestCellRejectsMultipleRootTrueContainers(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "two-roots"},
		Spec: ext.CellSpec{
			ID:      "two-roots",
			RealmID: "default",
			SpaceID: "default",
			StackID: "default",
			Containers: []ext.ContainerSpec{
				{ID: "a", Root: true, Image: "img"},
				{ID: "b", Root: true, Image: "img"},
			},
		},
	}

	_, _, err := apischeme.NormalizeCell(input)
	if err == nil {
		t.Fatalf("NormalizeCell: want error, got nil")
	}
	if !errors.Is(err, errdefs.ErrMultipleRootContainers) {
		t.Fatalf("err = %v, want wrapped ErrMultipleRootContainers", err)
	}
}

// TestCellRejectsRootContainerIDMismatch asserts the explicit/Root-flag
// agreement rule: if rootContainerId is set and a different container
// carries root: true, that's a misconfiguration the user must resolve.
// The Root-flagged peer would otherwise silently end up as a non-root
// container.
func TestCellRejectsRootContainerIDMismatch(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "mismatch"},
		Spec: ext.CellSpec{
			ID:              "mismatch",
			RealmID:         "default",
			SpaceID:         "default",
			StackID:         "default",
			RootContainerID: "a",
			Containers: []ext.ContainerSpec{
				{ID: "a", Image: "img"},
				{ID: "b", Root: true, Image: "img"},
			},
		},
	}

	_, _, err := apischeme.NormalizeCell(input)
	if err == nil {
		t.Fatalf("NormalizeCell: want error, got nil")
	}
	if !errors.Is(err, errdefs.ErrRootContainerMismatch) {
		t.Fatalf("err = %v, want wrapped ErrRootContainerMismatch", err)
	}
}

// TestCellRootContainerIDAndRootFlagAgree confirms the no-op case:
// rootContainerId set and the named container also carries root: true
// is accepted (the explicit branch's existing happy path).
func TestCellRootContainerIDAndRootFlagAgree(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "agree"},
		Spec: ext.CellSpec{
			ID:              "agree",
			RealmID:         "default",
			SpaceID:         "default",
			StackID:         "default",
			RootContainerID: "root",
			Containers: []ext.ContainerSpec{
				{ID: "root", Root: true, Image: "img"},
				{ID: "worker", Image: "img"},
			},
		},
	}

	internal, _, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	if internal.Spec.RootContainerID != "root" {
		t.Fatalf("RootContainerID = %q, want %q", internal.Spec.RootContainerID, "root")
	}
}

// TestBuildCellExternalRejectsMultipleRootTrue covers the defensive
// validation on the outbound path: a malformed internal cell that
// carries two Root:true containers must not silently round-trip out.
func TestBuildCellExternalRejectsMultipleRootTrue(t *testing.T) {
	internal := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: "two-roots"},
		Spec: intmodel.CellSpec{
			ID:        "two-roots",
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "a", Root: true, Image: "img"},
				{ID: "b", Root: true, Image: "img"},
			},
		},
	}

	_, err := apischeme.BuildCellExternalFromInternal(internal, ext.APIVersionV1Beta1)
	if err == nil {
		t.Fatalf("BuildCellExternalFromInternal: want error, got nil")
	}
	if !errors.Is(err, errdefs.ErrMultipleRootContainers) {
		t.Fatalf("err = %v, want wrapped ErrMultipleRootContainers", err)
	}
}

// TestStatusLifecycleFieldsRoundTripV1Beta1 pins the issue #166 plumbing:
// every kind's new Status lifecycle/probe fields must round-trip in both
// directions. Realm additionally carries ContainerdNamespaceReady; the
// other three kinds carry CgroupReady but no namespace probe. Bundled
// because the contract is shared even if the field sets differ.
func TestStatusLifecycleFieldsRoundTripV1Beta1(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	readyAt := createdAt.Add(30 * time.Minute)

	t.Run("realm", func(t *testing.T) {
		in := ext.RealmDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata:   ext.RealmMetadata{Name: "r0"},
			Status: ext.RealmStatus{
				CreatedAt:                createdAt,
				UpdatedAt:                updatedAt,
				ReadyAt:                  readyAt,
				Reason:                   "RealmReady",
				Message:                  "namespace + cgroup present",
				CgroupReady:              true,
				ContainerdNamespaceReady: true,
			},
		}
		intRealm, err := apischeme.ConvertRealmDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertRealmDocToInternal: %v", err)
		}
		if !intRealm.Status.CreatedAt.Equal(createdAt) {
			t.Errorf("ext→int CreatedAt = %v, want %v", intRealm.Status.CreatedAt, createdAt)
		}
		if !intRealm.Status.ReadyAt.Equal(readyAt) {
			t.Errorf("ext→int ReadyAt = %v, want %v", intRealm.Status.ReadyAt, readyAt)
		}
		if intRealm.Status.Reason != "RealmReady" {
			t.Errorf("ext→int Reason = %q, want RealmReady", intRealm.Status.Reason)
		}
		if !intRealm.Status.CgroupReady || !intRealm.Status.ContainerdNamespaceReady {
			t.Errorf("ext→int probes = %v/%v, want true/true",
				intRealm.Status.CgroupReady, intRealm.Status.ContainerdNamespaceReady)
		}

		out, err := apischeme.BuildRealmExternalFromInternal(intRealm, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildRealmExternalFromInternal: %v", err)
		}
		if !out.Status.UpdatedAt.Equal(updatedAt) {
			t.Errorf("int→ext UpdatedAt = %v, want %v", out.Status.UpdatedAt, updatedAt)
		}
		if out.Status.Message != "namespace + cgroup present" {
			t.Errorf("int→ext Message = %q, lost in round trip", out.Status.Message)
		}
		if !out.Status.ContainerdNamespaceReady {
			t.Errorf("int→ext ContainerdNamespaceReady = false, lost in round trip")
		}
	})

	t.Run("space", func(t *testing.T) {
		in := ext.SpaceDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindSpace,
			Metadata:   ext.SpaceMetadata{Name: "s0"},
			Spec:       ext.SpaceSpec{RealmID: "r0"},
			Status: ext.SpaceStatus{
				CreatedAt:   createdAt,
				UpdatedAt:   updatedAt,
				ReadyAt:     readyAt,
				Reason:      "CNIReady",
				Message:     "cni conf installed",
				CgroupReady: true,
			},
		}
		intSpace, err := apischeme.ConvertSpaceDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertSpaceDocToInternal: %v", err)
		}
		out, err := apischeme.BuildSpaceExternalFromInternal(intSpace, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildSpaceExternalFromInternal: %v", err)
		}
		if !out.Status.CreatedAt.Equal(createdAt) || !out.Status.ReadyAt.Equal(readyAt) {
			t.Errorf("space lifecycle timestamps lost in round trip: got %+v", out.Status)
		}
		if out.Status.Reason != "CNIReady" || !out.Status.CgroupReady {
			t.Errorf("space reason/probe lost: got reason=%q cgroupReady=%v",
				out.Status.Reason, out.Status.CgroupReady)
		}
	})

	t.Run("stack", func(t *testing.T) {
		in := ext.StackDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindStack,
			Metadata:   ext.StackMetadata{Name: "st0"},
			Spec:       ext.StackSpec{ID: "st0", RealmID: "r0", SpaceID: "s0"},
			Status: ext.StackStatus{
				CreatedAt:   createdAt,
				UpdatedAt:   updatedAt,
				ReadyAt:     readyAt,
				Reason:      "CgroupReady",
				CgroupReady: true,
			},
		}
		intStack, err := apischeme.ConvertStackDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertStackDocToInternal: %v", err)
		}
		out, err := apischeme.BuildStackExternalFromInternal(intStack, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildStackExternalFromInternal: %v", err)
		}
		if !out.Status.UpdatedAt.Equal(updatedAt) || !out.Status.CgroupReady {
			t.Errorf("stack lifecycle/probe lost: %+v", out.Status)
		}
	})

	t.Run("cell", func(t *testing.T) {
		in := ext.CellDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindCell,
			Metadata:   ext.CellMetadata{Name: "c0"},
			Spec:       ext.CellSpec{ID: "c0", RealmID: "r0", SpaceID: "s0", StackID: "st0"},
			Status: ext.CellStatus{
				CreatedAt:   createdAt,
				UpdatedAt:   updatedAt,
				ReadyAt:     readyAt,
				Reason:      "RootRunning",
				Message:     "root task active",
				CgroupReady: true,
			},
		}
		intCell, err := apischeme.ConvertCellDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertCellDocToInternal: %v", err)
		}
		out, err := apischeme.BuildCellExternalFromInternal(intCell, ext.APIVersionV1Beta1)
		if err != nil {
			t.Fatalf("BuildCellExternalFromInternal: %v", err)
		}
		if !out.Status.CreatedAt.Equal(createdAt) || !out.Status.ReadyAt.Equal(readyAt) {
			t.Errorf("cell lifecycle timestamps lost in round trip: got %+v", out.Status)
		}
		if out.Status.Message != "root task active" || !out.Status.CgroupReady {
			t.Errorf("cell message/probe lost: msg=%q cgroupReady=%v",
				out.Status.Message, out.Status.CgroupReady)
		}
	})
}

// TestStatusLifecycleFieldsZeroDefault covers the AC bullet "existing
// metadata.json files load with zero-value defaults (no migration)".
// An external doc with no lifecycle fields set must convert to an
// internal model with the same zero values, and round-trip back to
// zero-valued external. This is what lets a daemon upgrade onto a
// previously-written /opt/kukeon tree without rewriting every
// metadata.json.
func TestStatusLifecycleFieldsZeroDefault(t *testing.T) {
	t.Run("realm zero defaults", func(t *testing.T) {
		in := ext.RealmDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata:   ext.RealmMetadata{Name: "r0"},
		}
		intRealm, err := apischeme.ConvertRealmDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertRealmDocToInternal: %v", err)
		}
		if !intRealm.Status.CreatedAt.IsZero() ||
			!intRealm.Status.UpdatedAt.IsZero() ||
			!intRealm.Status.ReadyAt.IsZero() {
			t.Errorf("non-zero timestamps from zero-value input: %+v", intRealm.Status)
		}
		if intRealm.Status.Reason != "" || intRealm.Status.Message != "" {
			t.Errorf("non-empty reason/message from zero-value input: %+v", intRealm.Status)
		}
		if intRealm.Status.CgroupReady || intRealm.Status.ContainerdNamespaceReady {
			t.Errorf("non-false probes from zero-value input: %+v", intRealm.Status)
		}
	})
}

// TestNormalizeSecret_FlatFieldCopy pins the issue #619 conversion: the scope
// coordinates and the material copy across verbatim, and an unsupported
// apiVersion is rejected.
func TestNormalizeSecret_FlatFieldCopy(t *testing.T) {
	in := ext.SecretDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSecret,
		Metadata: ext.SecretMetadata{
			Name:  "anthropic-token",
			Realm: "default",
			Space: "team-a",
		},
		Spec: ext.SecretSpec{Data: "s3cr3t"},
	}

	got, version, err := apischeme.NormalizeSecret(in)
	if err != nil {
		t.Fatalf("NormalizeSecret() error = %v", err)
	}
	if version != apischeme.VersionV1Beta1 {
		t.Errorf("version = %q, want %q", version, apischeme.VersionV1Beta1)
	}
	want := intmodel.Secret{
		Metadata: intmodel.SecretMetadata{Name: "anthropic-token", Realm: "default", Space: "team-a"},
		Spec:     intmodel.SecretSpec{Data: "s3cr3t"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeSecret() = %+v, want %+v", got, want)
	}
}

func TestNormalizeSecret_UnsupportedVersion(t *testing.T) {
	_, _, err := apischeme.NormalizeSecret(ext.SecretDoc{APIVersion: "v0alpha9"})
	if err == nil {
		t.Fatal("NormalizeSecret() error = nil, want unsupported-version error")
	}
	if !strings.Contains(err.Error(), "unsupported apiVersion for Secret") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestContainerCreateStagePersistenceValidation covers the AC for #738
// (phase C3): a container declaring runOn: create stages must carry at least
// one persistent writable mount, otherwise the side effects evaporate on the
// next recreate while the run-once gate continues to report State == "done".
//
// The matrix mirrors the AC checkboxes: all-tmpfs, readonly-rootfs without a
// persistent mount, readonly-rootfs *with* a persistent mount, a mixed-mount
// container (tmpfs + bind), and a persistent-only container.
func TestContainerCreateStagePersistenceValidation(t *testing.T) {
	createStage := []ext.TtyStage{{Script: "npm ci", RunOn: ext.RunOnCreate}}

	cases := []struct {
		name       string
		volumes    []ext.VolumeMount
		readOnly   bool
		wantReject bool
	}{
		{
			// AC: runOn: create on an all-tmpfs container is rejected.
			name: "all-tmpfs/rejected",
			volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindTmpfs, Target: "/scratch"},
				{Kind: ext.VolumeKindTmpfs, Target: "/cache"},
			},
			wantReject: true,
		},
		{
			// AC: runOn: create on a readonly-rootfs container with no
			// persistent volume mount is rejected.
			name:       "readonly-rootfs-no-volumes/rejected",
			volumes:    nil,
			readOnly:   true,
			wantReject: true,
		},
		{
			// Sibling of the all-tmpfs case: a writable rootfs with no
			// volume declarations is also ephemeral under kukeon's recreate
			// model and must be rejected.
			name:       "no-volumes-writable-rootfs/rejected",
			volumes:    nil,
			wantReject: true,
		},
		{
			// AC complement: readonly-rootfs paired with a persistent bind
			// is accepted — the bind covers the stage's write target.
			name: "readonly-rootfs-with-bind/accepted",
			volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindBind, Source: "/srv/cache", Target: "/cache"},
			},
			readOnly: true,
		},
		{
			// AC: at least one persistent writable mount (bind here) is
			// accepted unchanged, even alongside tmpfs siblings.
			name: "mixed-mounts/accepted",
			volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindTmpfs, Target: "/scratch"},
				{Kind: ext.VolumeKindBind, Source: "/srv/data", Target: "/data"},
			},
		},
		{
			// AC: persistent-only is accepted unchanged.
			name: "persistent-only/accepted",
			volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindBind, Source: "/srv/data", Target: "/data"},
			},
		},
		{
			// Empty Kind back-compat (defaults to bind) is treated as
			// persistent, mirroring VolumeMount's documented default.
			name: "empty-kind-default-bind/accepted",
			volumes: []ext.VolumeMount{
				{Source: "/srv/data", Target: "/data"},
			},
		},
		{
			// A bind volume marked ReadOnly is not a writable persistent
			// target — the stage would fail at runtime instead of just
			// silently evaporating, but the validation guards against it
			// the same way.
			name: "readonly-bind-only/rejected",
			volumes: []ext.VolumeMount{
				{Kind: ext.VolumeKindBind, Source: "/srv/cache", Target: "/cache", ReadOnly: true},
			},
			wantReject: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := ext.ContainerDoc{
				APIVersion: ext.APIVersionV1Beta1,
				Kind:       ext.KindContainer,
				Metadata:   ext.ContainerMetadata{Name: "c"},
				Spec: ext.ContainerSpec{
					ID:      "c",
					RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
					Image:                  "alpine:latest",
					Attachable:             true,
					ReadOnlyRootFilesystem: tc.readOnly,
					Volumes:                tc.volumes,
					Tty:                    &ext.ContainerTty{OnInit: createStage},
				},
			}
			_, _, err := apischeme.NormalizeContainer(input)
			if tc.wantReject {
				if err == nil {
					t.Fatalf("NormalizeContainer accepted %s; want validation error", tc.name)
				}
				if !strings.Contains(err.Error(), "persistent writable mount") {
					t.Errorf("error %q does not mention persistent writable mount", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeContainer rejected %s: %v", tc.name, err)
			}
		})
	}

	t.Run("no-create-stage/start-only-accepted-on-ephemeral", func(t *testing.T) {
		// A container with only runOn: start (or empty) stages is
		// unaffected by the persistence guard, even with no mounts at all.
		input := ext.ContainerDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindContainer,
			Metadata:   ext.ContainerMetadata{Name: "c"},
			Spec: ext.ContainerSpec{
				ID:      "c",
				RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
				Image:      "alpine:latest",
				Attachable: true,
				Tty: &ext.ContainerTty{OnInit: []ext.TtyStage{
					{Script: "echo boot"},
					{Script: "echo boot2", RunOn: ext.RunOnStart},
				}},
			},
		}
		if _, _, err := apischeme.NormalizeContainer(input); err != nil {
			t.Fatalf("NormalizeContainer rejected ephemeral container with no create stage: %v", err)
		}
	})

	t.Run("cell-doc-path/rejected", func(t *testing.T) {
		// The same guard fires for ContainerSpecs embedded in a CellDoc.
		cell := ext.CellDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindCell,
			Metadata:   ext.CellMetadata{Name: "cl"},
			Spec: ext.CellSpec{
				ID:      "cl",
				RealmID: "r", SpaceID: "s", StackID: "st",
				Containers: []ext.ContainerSpec{
					{
						ID:      "c",
						RealmID: "r", SpaceID: "s", StackID: "st", CellID: "cl",
						Image:      "alpine:latest",
						Attachable: true,
						Tty:        &ext.ContainerTty{OnInit: createStage},
					},
				},
			},
		}
		if _, _, err := apischeme.NormalizeCell(cell); err == nil {
			t.Fatal("NormalizeCell accepted ephemeral runOn: create container; want validation error")
		}
	})
}
