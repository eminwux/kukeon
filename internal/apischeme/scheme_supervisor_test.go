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
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// reportedContainerStatus is a container status carrying everything issue
// #1711 adds: repos, stages, reason, message and the supervisor view.
func reportedContainerStatus() ext.ContainerStatus {
	at := time.Date(2026, 11, 26, 19, 50, 24, 0, time.UTC)
	return ext.ContainerStatus{
		Name: "work", ID: "work", State: ext.ContainerStateError, ExitCode: 70,
		Reason:  ext.ContainerReasonRepoFailed,
		Message: `required repo "role-source" failed: fatal: could not read`,
		Repos: []ext.RepoStatus{
			{Name: "role-source", Target: "/work/role", State: ext.RepoStateFailed, Error: "fatal: could not read"},
		},
		Stages: []ext.StageStatus{{Index: 0, State: ext.StageStateDone, Hash: "h0"}},
		Supervisor: &ext.SupervisorStatus{
			Version: "v0.10.0-test", Phase: ext.SupervisorPhaseRepos, PhaseSince: at.Add(-time.Minute),
			Current: "role-source", AttachedTerminal: "main",
			Terminals: []ext.SupervisorTerminalStatus{{
				ID: "main", State: "Initializing", Attachers: []string{"a"},
				Exit: &ext.TerminalExit{Code: 3, Signal: "SIGTERM", At: at},
			}},
			Exit:      &ext.SupervisorExit{Code: 70, At: at},
			UpdatedAt: at,
		},
	}
}

func TestContainerRoundTripSupervisorStatusV1Beta1(t *testing.T) {
	input := ext.ContainerDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindContainer,
		Metadata:   ext.ContainerMetadata{Name: "work"},
		Spec:       ext.ContainerSpec{ID: "work", Image: "alpine:latest", Attachable: true},
		Status:     reportedContainerStatus(),
	}
	internal, version, err := apischeme.NormalizeContainer(input)
	if err != nil {
		t.Fatalf("NormalizeContainer: %v", err)
	}
	output, err := apischeme.BuildContainerExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildContainerExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(output.Status, input.Status) {
		t.Errorf("status round trip:\n got %+v\nwant %+v", output.Status, input.Status)
	}
	// The conversion copies: mutating the output leaves the input intact.
	output.Status.Supervisor.Terminals[0].Attachers[0] = "changed"
	if input.Status.Supervisor.Terminals[0].Attachers[0] != "a" {
		t.Error("the supervisor status is shared between the input and the output")
	}
}

// The cell document is what kukeond persists; the container statuses in it
// must keep repos, stages, reason, message and supervisor (they used to keep
// only the scalar fields, so repos and stage done-records never reached disk).
func TestCellRoundTripContainerStatusesKeepReportedFieldsV1Beta1(t *testing.T) {
	input := ext.CellDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCell,
		Metadata:   ext.CellMetadata{Name: "cell0"},
		Spec: ext.CellSpec{
			ID:         "cell0",
			RealmID:    "r",
			SpaceID:    "s",
			StackID:    "k",
			Containers: []ext.ContainerSpec{},
		},
		Status: ext.CellStatus{
			State:      ext.CellStateError,
			Containers: []ext.ContainerStatus{reportedContainerStatus()},
		},
	}
	internal, version, err := apischeme.NormalizeCell(input)
	if err != nil {
		t.Fatalf("NormalizeCell: %v", err)
	}
	output, err := apischeme.BuildCellExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildCellExternalFromInternal: %v", err)
	}
	if !reflect.DeepEqual(output.Status.Containers, input.Status.Containers) {
		t.Errorf("container statuses round trip:\n got %+v\nwant %+v",
			output.Status.Containers, input.Status.Containers)
	}
}

// The supervisor document's embedded status is flattened in JSON and YAML, as
// the issue's document shape shows.
func TestTerminalSupervisorDocEncoding(t *testing.T) {
	st := reportedContainerStatus()
	doc := ext.TerminalSupervisorDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindTerminalSupervisor,
		Metadata:   ext.TerminalSupervisorMetadata{Name: "work"},
		Status: ext.TerminalSupervisorStatus{
			SupervisorStatus: *st.Supervisor, Repos: st.Repos, Stages: st.Stages,
		},
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Status map[string]any `json:"status"`
	}
	if err = json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"phase", "current", "attachedTerminal", "terminals", "exit", "repos", "stages"} {
		if _, ok := raw.Status[key]; !ok {
			t.Errorf("JSON status has no %q: %s", key, data)
		}
	}
	var back ext.TerminalSupervisorDoc
	if err = json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, doc) {
		t.Errorf("JSON round trip:\n got %+v\nwant %+v", back, doc)
	}

	y, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(y), "\n    phase: Repos\n") {
		t.Errorf("YAML status is not flattened:\n%s", y)
	}
}
