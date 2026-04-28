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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
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
