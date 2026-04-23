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
			ID:       "container-id-0",
			RealmID:  "realm0",
			SpaceID:  "space0",
			StackID:  "stack0",
			CellID:   "cell0",
			Image:    "alpine:latest",
			Command:  "sh",
			Args:     []string{"-c", "echo hello"},
			Env:      []string{"ENV_VAR=value"},
			Ports:    []string{"8080:80"},
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
