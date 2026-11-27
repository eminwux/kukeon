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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// repoFailedDoc is the document kuketty leaves when a required repo cannot be
// cloned (the 2026-11-26 case).
func repoFailedDoc() v1beta1.TerminalSupervisorDoc {
	at := time.Date(2026, 11, 26, 19, 50, 24, 0, time.UTC)
	return v1beta1.TerminalSupervisorDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindTerminalSupervisor,
		Metadata:   v1beta1.TerminalSupervisorMetadata{Name: "work"},
		Status: v1beta1.TerminalSupervisorStatus{
			SupervisorStatus: v1beta1.SupervisorStatus{
				Version:    "v0.10.0-test",
				Phase:      v1beta1.SupervisorPhaseRepos,
				PhaseSince: at.Add(-23 * time.Second),
				Current:    "role-source",
				Exit:       &v1beta1.SupervisorExit{Code: 70, At: at},
				UpdatedAt:  at,
			},
			Repos: []v1beta1.RepoStatus{{
				Name: "role-source", Target: "/work/role", State: v1beta1.RepoStateFailed,
				Error: "fatal: could not read from remote repository",
			}},
		},
	}
}

func writeSupervisorFile(t *testing.T, dir string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, consts.KukeonContainerSupervisorFile), data, 0o640); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestReadSupervisorDoc(t *testing.T) {
	valid := mustJSON(t, repoFailedDoc())
	wrongKind := repoFailedDoc()
	wrongKind.Kind = v1beta1.KindContainer
	wrongVersion := repoFailedDoc()
	wrongVersion.APIVersion = "v2"
	badPhase := repoFailedDoc()
	badPhase.Status.Phase = "Dancing"

	cases := []struct {
		name   string
		setup  func(t *testing.T, dir string)
		want   supervisorDocState
		detail string
	}{
		{"no tty dir", func(*testing.T, string) {}, supervisorDocMissing, ""},
		{"no document", func(t *testing.T, dir string) {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}, supervisorDocMissing, ""},
		{"valid", func(t *testing.T, dir string) { writeSupervisorFile(t, dir, valid) }, supervisorDocValid, ""},
		{"unknown fields allowed", func(t *testing.T, dir string) {
			var m map[string]any
			_ = json.Unmarshal(valid, &m)
			m["status"].(map[string]any)["futureField"] = true
			writeSupervisorFile(t, dir, mustJSON(t, m))
		}, supervisorDocValid, ""},
		{
			"wrong kind", func(t *testing.T, dir string) { writeSupervisorFile(t, dir, mustJSON(t, wrongKind)) },
			supervisorDocInvalid, "kind",
		},
		{
			"wrong apiVersion", func(t *testing.T, dir string) { writeSupervisorFile(t, dir, mustJSON(t, wrongVersion)) },
			supervisorDocUnsupported, "apiVersion",
		},
		{
			"unknown phase", func(t *testing.T, dir string) { writeSupervisorFile(t, dir, mustJSON(t, badPhase)) },
			supervisorDocInvalid, "phase",
		},
		{
			"not JSON", func(t *testing.T, dir string) { writeSupervisorFile(t, dir, []byte("{not json")) },
			supervisorDocInvalid, "decode",
		},
		{"oversized", func(t *testing.T, dir string) {
			writeSupervisorFile(t, dir, []byte(strings.Repeat(" ", maxSupervisorDocBytes+1)))
		}, supervisorDocInvalid, "limit"},
		{"symlink", func(t *testing.T, dir string) {
			target := filepath.Join(t.TempDir(), "elsewhere.json")
			if err := os.WriteFile(target, valid, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(dir, consts.KukeonContainerSupervisorFile)); err != nil {
				t.Fatal(err)
			}
		}, supervisorDocInvalid, "regular file"},
		{"directory", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, consts.KukeonContainerSupervisorFile), 0o755); err != nil {
				t.Fatal(err)
			}
		}, supervisorDocInvalid, "regular file"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "tty")
			tc.setup(t, dir)
			got := readSupervisorDoc(dir)
			if got.State != tc.want {
				t.Fatalf("state = %v (%s), want %v", got.State, got.Detail, tc.want)
			}
			if tc.detail != "" && !strings.Contains(got.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to mention %q", got.Detail, tc.detail)
			}
			if (got.Doc != nil) != (tc.want == supervisorDocValid) {
				t.Errorf("doc = %v for state %v", got.Doc, got.State)
			}
		})
	}
}

func TestRemoveSupervisorDoc(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, consts.KukeonContainerSupervisorFile)
	if err := removeSupervisorDoc(filepath.Join(dir, "absent")); err != nil {
		t.Fatalf("missing directory: %v", err)
	}
	if err := removeSupervisorDoc(dir); err != nil {
		t.Fatalf("missing document: %v", err)
	}
	writeSupervisorFile(t, dir, mustJSON(t, repoFailedDoc()))
	if err := removeSupervisorDoc(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("document still present: %v", err)
	}
}

// A document left by the previous run is removed when the next start builds
// its options, so it cannot be credited to the new run.
func TestAttachableBuildOpts_RemovesLeftoverSupervisorDoc(t *testing.T) {
	runPath := t.TempDir()
	stub := filepath.Join(runPath, kukettyBinaryStagedSubdir, "kuketty")
	if err := os.MkdirAll(filepath.Dir(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stub, []byte("\x7fELF stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newDeleteCellTestExec(t, &deleteCellFakeClient{})
	r.opts.RunPath = runPath
	spec := intmodel.ContainerSpec{
		ID: "work", RealmName: "default", SpaceName: "s", StackName: "k", CellName: "c", Attachable: true,
	}
	ttyDir := fs.ContainerTTYDir(runPath, "default", "s", "k", "c", "work")
	writeSupervisorFile(t, ttyDir, mustJSON(t, repoFailedDoc()))

	if _, err := r.attachableBuildOpts("default.kukeon.io", spec, nil, nil); err != nil {
		t.Fatalf("attachableBuildOpts: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ttyDir, consts.KukeonContainerSupervisorFile)); !os.IsNotExist(err) {
		t.Fatalf("leftover supervisor document survived the start: %v", err)
	}
}

// supervisorTestCell is an attachable single-container cell for populate.
func supervisorTestCell(runPath string) (intmodel.Cell, string) {
	realm, space, stack, cellName := "default", "kukeon", "kukeon", "agents"
	cell := containerStateCell(realm, space, stack, cellName, "work", "kukeon_kukeon_agents_work")
	cell.Spec.Containers[0].Attachable = true
	cell.Spec.Containers[0].Repos = []intmodel.ContainerRepo{
		{Name: "role-source", Target: "/work/role", URL: "https://invalid.example/role.git", Required: true},
	}
	return cell, fs.ContainerTTYDir(runPath, realm, space, stack, cellName, "work")
}

// The 2026-11-26 case through populate: the container exited 70 and the
// document says a required repo failed. ContainerStatus carries the repos,
// the supervisor view and reason RepoFailed.
func TestPopulateCellContainerStatuses_RepoFailureFromSupervisorDoc(t *testing.T) {
	exitAt := time.Date(2026, 11, 26, 19, 50, 24, 0, time.UTC)
	taskGone := false
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			if taskGone {
				return containerd.Status{}, fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
			}
			return containerd.Status{Status: containerd.Stopped, ExitStatus: 70, ExitTime: exitAt}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, "default")
	cell, ttyDir := supervisorTestCell(r.opts.RunPath)
	writeSupervisorFile(t, ttyDir, mustJSON(t, repoFailedDoc()))

	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatal(err)
	}
	st := cell.Status.Containers[0]
	if st.State != intmodel.ContainerStateError || st.ExitCode != 70 {
		t.Fatalf("state/exit = %v/%d, want Error/70", st.State, st.ExitCode)
	}
	if len(st.Repos) != 1 || st.Repos[0].State != v1beta1.RepoStateFailed || st.Repos[0].Error == "" {
		t.Errorf("repos = %+v", st.Repos)
	}
	if st.Supervisor == nil || st.Supervisor.Phase != v1beta1.SupervisorPhaseRepos ||
		st.Supervisor.Current != "role-source" || st.Supervisor.Exit == nil || st.Supervisor.Exit.Code != 70 {
		t.Fatalf("supervisor = %+v", st.Supervisor)
	}
	if st.Reason != v1beta1.ContainerReasonRepoFailed || !strings.Contains(st.Message, `"role-source"`) ||
		!strings.Contains(st.Message, "could not read from remote") {
		t.Errorf("reason/message = %q/%q", st.Reason, st.Message)
	}

	// The cell takes its failing container's reason.
	reason, msg := cellFailureBreadcrumb(cell)
	if reason != v1beta1.ContainerReasonRepoFailed || !strings.Contains(msg, `container "work"`) ||
		!strings.Contains(msg, "role-source") {
		t.Errorf("cell breadcrumb = %q/%q", reason, msg)
	}

	// A second pass with the task and the document gone (a reboot empties
	// /run) keeps what was reported.
	taskGone = true
	if err := os.Remove(filepath.Join(ttyDir, consts.KukeonContainerSupervisorFile)); err != nil {
		t.Fatal(err)
	}
	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatal(err)
	}
	st = cell.Status.Containers[0]
	if len(st.Repos) != 1 || st.Supervisor == nil || st.Reason != v1beta1.ContainerReasonRepoFailed {
		t.Errorf(
			"after the document went missing: repos=%+v supervisor=%+v reason=%q",
			st.Repos,
			st.Supervisor,
			st.Reason,
		)
	}
}

// A restart deletes the previous run's document. If the new kuketty writes
// none (its writes are best effort), the new run must not inherit the
// previous run's repos, supervisor or reason: a failure of the new task reads
// SupervisorStatusUnavailable, and a running one shows no supervisor.
func TestPopulateCellContainerStatuses_RestartWithoutDocDropsPriorRun(t *testing.T) {
	cases := []struct {
		name       string
		next       containerd.Status
		wantState  intmodel.ContainerState
		wantReason string
	}{
		{
			"new run fails",
			containerd.Status{Status: containerd.Stopped, ExitStatus: 70, ExitTime: time.Now()},
			intmodel.ContainerStateError,
			v1beta1.ContainerReasonSupervisorStatusUnavailable,
		},
		{"new run is running", containerd.Status{Status: containerd.Running}, intmodel.ContainerStateReady, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			next := containerd.Status{Status: containerd.Stopped, ExitStatus: 70, ExitTime: time.Now()}
			fake := &deleteCellFakeClient{
				existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
				taskStatusFn:      func(_, _ string) (containerd.Status, error) { return next, nil },
			}
			r := newDeleteCellTestExec(t, fake)
			seedDeleteCellRealm(t, r, "default")
			cell, ttyDir := supervisorTestCell(r.opts.RunPath)
			writeSupervisorFile(t, ttyDir, mustJSON(t, repoFailedDoc()))
			if err := r.populateCellContainerStatuses(&cell); err != nil {
				t.Fatal(err)
			}
			if st := cell.Status.Containers[0]; st.Reason != v1beta1.ContainerReasonRepoFailed {
				t.Fatalf("first run: reason = %q, want RepoFailed", st.Reason)
			}

			// Restart: the start path removes the document (see
			// TestAttachableBuildOpts_RemovesLeftoverSupervisorDoc) and the
			// new task runs without ever writing one.
			if err := removeSupervisorDoc(ttyDir); err != nil {
				t.Fatal(err)
			}
			next = tc.next
			if err := r.populateCellContainerStatuses(&cell); err != nil {
				t.Fatal(err)
			}
			st := cell.Status.Containers[0]
			if st.State != tc.wantState {
				t.Fatalf("state = %v, want %v", st.State, tc.wantState)
			}
			if st.Supervisor != nil || len(st.Repos) != 0 {
				t.Errorf("previous run carried into the new one: supervisor=%+v repos=%+v", st.Supervisor, st.Repos)
			}
			if st.Reason != tc.wantReason {
				t.Errorf("reason = %q (%q), want %q", st.Reason, st.Message, tc.wantReason)
			}
			if tc.wantReason != "" && !strings.Contains(st.Message, "no supervisor status document") {
				t.Errorf("message = %q", st.Message)
			}
		})
	}
}

// containerd wins: a document whose exit code differs from the task's is
// not trusted for the exit, and the container reads as terminated.
func TestPopulateCellContainerStatuses_ExitMismatchDropsDocExit(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Stopped, ExitStatus: 137, ExitTime: time.Now()}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, "default")
	cell, ttyDir := supervisorTestCell(r.opts.RunPath)
	writeSupervisorFile(t, ttyDir, mustJSON(t, repoFailedDoc())) // records exit 70

	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatal(err)
	}
	st := cell.Status.Containers[0]
	if st.Supervisor == nil || st.Supervisor.Exit != nil {
		t.Fatalf("supervisor exit = %+v, want dropped", st.Supervisor)
	}
	if st.Reason != v1beta1.ContainerReasonSupervisorTerminated || !strings.Contains(st.Message, "SIGKILL") {
		t.Errorf("reason/message = %q/%q", st.Reason, st.Message)
	}
}

// A healthy attachable container shows phase Running and its terminal Ready.
func TestPopulateCellContainerStatuses_RunningSupervisor(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Running}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, "default")
	cell, ttyDir := supervisorTestCell(r.opts.RunPath)
	doc := repoFailedDoc()
	doc.Status.Phase = v1beta1.SupervisorPhaseRunning
	doc.Status.Current = ""
	doc.Status.Exit = nil
	doc.Status.AttachedTerminal = "main"
	doc.Status.Terminals = []v1beta1.SupervisorTerminalStatus{{ID: "main", State: "Ready", PID: 12}}
	doc.Status.Repos[0] = v1beta1.RepoStatus{
		Name:   "role-source",
		Target: "/work/role",
		State:  v1beta1.RepoStateCloned,
		Commit: "abc",
	}
	writeSupervisorFile(t, ttyDir, mustJSON(t, doc))

	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatal(err)
	}
	st := cell.Status.Containers[0]
	if st.State != intmodel.ContainerStateReady || st.Reason != "" {
		t.Fatalf("state/reason = %v/%q, want Ready with no reason", st.State, st.Reason)
	}
	sup := st.Supervisor
	if sup == nil || sup.Phase != v1beta1.SupervisorPhaseRunning || sup.AttachedTerminal != "main" ||
		len(sup.Terminals) != 1 || sup.Terminals[0].State != "Ready" {
		t.Errorf("supervisor = %+v", sup)
	}
	if len(st.Repos) != 1 || st.Repos[0].State != v1beta1.RepoStateCloned {
		t.Errorf("repos = %+v", st.Repos)
	}
}

// An invalid document is ignored: the prior values stay, and an attachable
// container with no supervisor status that exits non-zero says why.
func TestPopulateCellContainerStatuses_InvalidDocIgnored(t *testing.T) {
	fake := &deleteCellFakeClient{
		existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
		taskStatusFn: func(_, _ string) (containerd.Status, error) {
			return containerd.Status{Status: containerd.Stopped, ExitStatus: 70, ExitTime: time.Now()}, nil
		},
	}
	r := newDeleteCellTestExec(t, fake)
	seedDeleteCellRealm(t, r, "default")
	cell, ttyDir := supervisorTestCell(r.opts.RunPath)
	bad := repoFailedDoc()
	bad.APIVersion = "v9"
	writeSupervisorFile(t, ttyDir, mustJSON(t, bad))

	if err := r.populateCellContainerStatuses(&cell); err != nil {
		t.Fatal(err)
	}
	st := cell.Status.Containers[0]
	if st.Supervisor != nil || len(st.Repos) != 0 {
		t.Errorf("an unsupported document was applied: supervisor=%+v repos=%+v", st.Supervisor, st.Repos)
	}
	if st.Reason != v1beta1.ContainerReasonSupervisorStatusUnavailable ||
		!strings.Contains(st.Message, "unsupported") || !strings.Contains(st.Message, "70") {
		t.Errorf("reason/message = %q/%q", st.Reason, st.Message)
	}
}

func TestContainerStatusesEqual_ComparesReportedFields(t *testing.T) {
	base := func() []intmodel.ContainerStatus {
		doc := repoFailedDoc()
		return []intmodel.ContainerStatus{{
			ID: "work", State: intmodel.ContainerStateError, ExitCode: 70,
			Reason: v1beta1.ContainerReasonRepoFailed, Message: "m",
			Repos:      supervisorRepoStatusesToInternal(doc.Status.Repos),
			Stages:     []intmodel.StageStatus{{Index: 0, State: "done", Hash: "h"}},
			Supervisor: doc.Status.SupervisorStatus.DeepCopy(),
		}}
	}
	if !containerStatusesEqual(base(), base()) {
		t.Fatal("identical snapshots compare unequal")
	}
	mutations := map[string]func(*intmodel.ContainerStatus){
		"reason":     func(s *intmodel.ContainerStatus) { s.Reason = v1beta1.ContainerReasonWorkloadFailed },
		"message":    func(s *intmodel.ContainerStatus) { s.Message = "other" },
		"exit code":  func(s *intmodel.ContainerStatus) { s.ExitCode = 1 },
		"repo":       func(s *intmodel.ContainerStatus) { s.Repos[0].Error = "other" },
		"stage":      func(s *intmodel.ContainerStatus) { s.Stages[0].State = "failed" },
		"phase":      func(s *intmodel.ContainerStatus) { s.Supervisor.Phase = v1beta1.SupervisorPhaseStages },
		"supervisor": func(s *intmodel.ContainerStatus) { s.Supervisor = nil },
		"terminal": func(s *intmodel.ContainerStatus) {
			s.Supervisor.Terminals = []v1beta1.SupervisorTerminalStatus{{ID: "main", State: "Ready"}}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			b := base()
			mutate(&b[0])
			if containerStatusesEqual(base(), b) {
				t.Errorf("a %s change does not trigger a persist", name)
			}
		})
	}
	t.Run("updatedAt ignored", func(t *testing.T) {
		b := base()
		b[0].Supervisor.UpdatedAt = b[0].Supervisor.UpdatedAt.Add(time.Hour)
		if !containerStatusesEqual(base(), b) {
			t.Error("an UpdatedAt-only change triggers a persist")
		}
	})
	t.Run("same instant in another location", func(t *testing.T) {
		b := base()
		b[0].Supervisor.PhaseSince = b[0].Supervisor.PhaseSince.In(time.FixedZone("X", 3600))
		if !containerStatusesEqual(base(), b) {
			t.Error("the same instant in another location compares unequal")
		}
	})
}

// Once the task record is deleted (wind-down, teardown, reboot) the container
// reads Stopped and keeps the reason it ended with; a container stopped while
// it was running gets none.
func TestPopulateCellContainerStatuses_StoppedKeepsPriorReason(t *testing.T) {
	taskGone := fmt.Errorf("%w: %w", errdefs.ErrTaskNotFound, errors.New("task: not found"))
	cases := []struct {
		name       string
		first      containerd.Status
		wantReason string
	}{
		{
			"failed then reaped",
			containerd.Status{Status: containerd.Stopped, ExitStatus: 70, ExitTime: time.Now()},
			v1beta1.ContainerReasonRepoFailed,
		},
		{"running then stopped", containerd.Status{Status: containerd.Running}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pull := 0
			fake := &deleteCellFakeClient{
				existsContainerFn: func(_, _ string) (bool, error) { return true, nil },
				taskStatusFn: func(_, _ string) (containerd.Status, error) {
					pull++
					if pull == 1 {
						return tc.first, nil
					}
					return containerd.Status{}, taskGone
				},
			}
			r := newDeleteCellTestExec(t, fake)
			seedDeleteCellRealm(t, r, "default")
			cell, ttyDir := supervisorTestCell(r.opts.RunPath)
			writeSupervisorFile(t, ttyDir, mustJSON(t, repoFailedDoc()))
			for range 2 {
				if err := r.populateCellContainerStatuses(&cell); err != nil {
					t.Fatal(err)
				}
			}
			st := cell.Status.Containers[0]
			if st.State != intmodel.ContainerStateStopped {
				t.Fatalf("state = %v, want Stopped", st.State)
			}
			if st.Reason != tc.wantReason {
				t.Errorf("reason = %q (%q), want %q", st.Reason, st.Message, tc.wantReason)
			}
		})
	}
}
