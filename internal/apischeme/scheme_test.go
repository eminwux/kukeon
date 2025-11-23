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
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
			ID:            "cell-id-0",
			RealmID:       "realm0",
			SpaceID:       "space0",
			StackID:       "stack0",
			Containers:    []ext.ContainerSpec{},
			RootContainer: nil,
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
}
