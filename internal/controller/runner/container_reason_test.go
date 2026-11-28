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

package runner

import (
	"strings"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestDeriveContainerReason covers every row of the reason table (issue
// #1711), plus the states that carry no reason.
func TestDeriveContainerReason(t *testing.T) {
	valid := supervisorDocResult{State: supervisorDocValid}
	exited := func(code int) *v1beta1.SupervisorExit { return &v1beta1.SupervisorExit{Code: code} }
	failedRepos := []intmodel.RepoStatus{
		{Name: "optional", State: v1beta1.RepoStateFailed, Error: "optional boom"},
		{Name: "role-source", State: v1beta1.RepoStateFailed, Error: "could not read from remote"},
	}

	cases := []struct {
		name        string
		status      intmodel.ContainerStatus
		attachable  bool
		doc         supervisorDocResult
		wantReason  string
		wantMessage []string
	}{
		{
			name: "repo failed",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateError, ExitCode: 70, Repos: failedRepos,
				Supervisor: &v1beta1.SupervisorStatus{
					Phase:   v1beta1.SupervisorPhaseRepos,
					Current: "role-source",
					Exit:    exited(70),
				},
			},
			attachable: true, doc: valid,
			wantReason:  v1beta1.ContainerReasonRepoFailed,
			wantMessage: []string{`required repo "role-source" failed`, "could not read from remote"},
		},
		{
			name: "stage failed",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateError, ExitCode: 70,
				Stages: []intmodel.StageStatus{
					{Index: 0, State: v1beta1.StageStateDone},
					{Index: 2, State: v1beta1.StageStateFailed, Error: "exit status 3: boom"},
				},
				Supervisor: &v1beta1.SupervisorStatus{
					Phase:   v1beta1.SupervisorPhaseStages,
					Current: "2",
					Exit:    exited(70),
				},
			},
			attachable: true, doc: valid,
			wantReason:  v1beta1.ContainerReasonStageFailed,
			wantMessage: []string{"create stage 2 failed", "boom"},
		},
		{
			name: "supervisor failed",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateError, ExitCode: 70,
				Supervisor: &v1beta1.SupervisorStatus{
					Phase: v1beta1.SupervisorPhaseConfig, Error: "listen on /run/kukeon/tty/terminals/main/control.sock: denied", Exit: exited(70),
				},
			},
			attachable: true, doc: valid,
			wantReason:  v1beta1.ContainerReasonSupervisorFailed,
			wantMessage: []string{"phase Config", "listen on /run/kukeon/tty/terminals/main/control.sock"},
		},
		{
			name: "supervisor terminated",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateError, ExitCode: 137, ExitSignal: "SIGKILL",
				Supervisor: &v1beta1.SupervisorStatus{Phase: v1beta1.SupervisorPhaseRepos, Current: "role-source"},
			},
			attachable: true, doc: valid,
			wantReason:  v1beta1.ContainerReasonSupervisorTerminated,
			wantMessage: []string{"phase Repos", "SIGKILL"},
		},
		{
			name: "workload failed (attached terminal)",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateError, ExitCode: 2,
				Supervisor: &v1beta1.SupervisorStatus{
					Phase: v1beta1.SupervisorPhaseExited, AttachedTerminal: "main", Exit: exited(2),
					Terminals: []v1beta1.SupervisorTerminalStatus{{ID: "main", Exit: &v1beta1.TerminalExit{Code: 2}}},
				},
			},
			attachable: true, doc: valid,
			wantReason:  v1beta1.ContainerReasonWorkloadFailed,
			wantMessage: []string{"exited with code 2"},
		},
		{
			name:        "workload failed (non-attachable)",
			status:      intmodel.ContainerStatus{State: intmodel.ContainerStateError, ExitCode: 1},
			wantReason:  v1beta1.ContainerReasonWorkloadFailed,
			wantMessage: []string{"exited with code 1"},
		},
		{
			name:        "supervisor status unavailable: missing",
			status:      intmodel.ContainerStatus{State: intmodel.ContainerStateError, ExitCode: 70},
			attachable:  true,
			doc:         supervisorDocResult{State: supervisorDocMissing},
			wantReason:  v1beta1.ContainerReasonSupervisorStatusUnavailable,
			wantMessage: []string{"no supervisor status document", "exited with code 70"},
		},
		{
			name:       "supervisor status unavailable: unsupported",
			status:     intmodel.ContainerStatus{State: intmodel.ContainerStateError, ExitCode: 70},
			attachable: true,
			doc:        supervisorDocResult{State: supervisorDocUnsupported, Detail: `apiVersion "v2", want "v1beta1"`},
			wantReason: v1beta1.ContainerReasonSupervisorStatusUnavailable,
			wantMessage: []string{
				"unsupported supervisor status document", "recreate the container", "exited with code 70",
			},
		},
		{
			name:        "supervisor status unavailable: invalid",
			status:      intmodel.ContainerStatus{State: intmodel.ContainerStateError, ExitCode: 70},
			attachable:  true,
			doc:         supervisorDocResult{State: supervisorDocInvalid, Detail: "not a regular file"},
			wantReason:  v1beta1.ContainerReasonSupervisorStatusUnavailable,
			wantMessage: []string{"invalid supervisor status document (not a regular file)"},
		},
		{
			name: "clean exit carries no reason",
			status: intmodel.ContainerStatus{
				State:      intmodel.ContainerStateExited,
				Supervisor: &v1beta1.SupervisorStatus{Phase: v1beta1.SupervisorPhaseExited, Exit: exited(0)},
			},
			attachable: true, doc: valid,
		},
		{
			name: "operator stop carries no reason",
			status: intmodel.ContainerStatus{
				State: intmodel.ContainerStateStopped, ExitCode: 137, ExitSignal: "SIGKILL",
				Supervisor: &v1beta1.SupervisorStatus{Phase: v1beta1.SupervisorPhaseRunning},
			},
			attachable: true, doc: valid,
		},
		{
			name: "running carries no reason",
			status: intmodel.ContainerStatus{
				State:      intmodel.ContainerStateReady,
				Supervisor: &v1beta1.SupervisorStatus{Phase: v1beta1.SupervisorPhaseRunning},
			},
			attachable: true, doc: valid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, msg := deriveContainerReason(tc.status, tc.attachable, tc.doc)
			if reason != tc.wantReason {
				t.Errorf("reason = %q, want %q (message %q)", reason, tc.wantReason, msg)
			}
			if tc.wantReason == "" && msg != "" {
				t.Errorf("message = %q, want empty", msg)
			}
			for _, want := range tc.wantMessage {
				if !strings.Contains(msg, want) {
					t.Errorf("message = %q, want it to contain %q", msg, want)
				}
			}
		})
	}
}

func TestDeriveContainerReason_MessageTruncated(t *testing.T) {
	status := intmodel.ContainerStatus{
		State: intmodel.ContainerStateError, ExitCode: 70,
		Repos: []intmodel.RepoStatus{{Name: "r", State: v1beta1.RepoStateFailed, Error: strings.Repeat("x\n", 2000)}},
		Supervisor: &v1beta1.SupervisorStatus{
			Phase: v1beta1.SupervisorPhaseRepos, Current: "r", Exit: &v1beta1.SupervisorExit{Code: 70},
		},
	}
	_, msg := deriveContainerReason(status, true, supervisorDocResult{State: supervisorDocValid})
	if len(msg) > maxFailureMessageLen || strings.Contains(msg, "\n") {
		t.Errorf("message not bounded to one line of %d bytes: %d bytes", maxFailureMessageLen, len(msg))
	}
}

// The cell's reason and message are its failing container's; without one,
// the historical WorkloadFailed breadcrumb stays.
func TestCellFailureBreadcrumb_UsesContainerReason(t *testing.T) {
	cell := intmodel.Cell{
		Spec: intmodel.CellSpec{Containers: []intmodel.ContainerSpec{
			{ID: "root", Root: true},
			{ID: "work", Attachable: true},
		}},
		Status: intmodel.CellStatus{Containers: []intmodel.ContainerStatus{
			{ID: "root", State: intmodel.ContainerStateReady},
			{
				ID: "work", State: intmodel.ContainerStateError, ExitCode: 70,
				Reason: v1beta1.ContainerReasonStageFailed, Message: "create stage 1 failed: boom",
			},
		}},
	}
	reason, msg := cellFailureBreadcrumb(cell)
	if reason != v1beta1.ContainerReasonStageFailed || msg != `container "work": create stage 1 failed: boom` {
		t.Errorf("breadcrumb = %q/%q", reason, msg)
	}

	cell.Status.Containers[1].Reason, cell.Status.Containers[1].Message = "", ""
	reason, msg = cellFailureBreadcrumb(cell)
	if reason != reasonWorkloadFailed || msg != `container "work" exited with code 70` {
		t.Errorf("fallback breadcrumb = %q/%q", reason, msg)
	}
}
