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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
)

// newTestReporter returns a reporter writing into a temp dir, with a fixed
// clock.
func newTestReporter(t *testing.T) (*supervisorReporter, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "supervisor.json")
	r := newSupervisorReporter(path, "work")
	r.logger = discardLogger()
	at := time.Date(2026, 11, 26, 19, 50, 0, 0, time.UTC)
	r.now = func() time.Time { return at }
	return r, path
}

// readDoc decodes the document on disk.
func readDoc(t *testing.T, path string) v1beta1.TerminalSupervisorDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc v1beta1.TerminalSupervisorDoc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode %s: %v\n%s", path, err, data)
	}
	return doc
}

func TestSupervisorReporter_WritesDocumentOnEveryChange(t *testing.T) {
	r, path := newTestReporter(t)
	r.setPhase(v1beta1.SupervisorPhaseConfig)

	doc := readDoc(t, path)
	if doc.APIVersion != v1beta1.APIVersionV1Beta1 || doc.Kind != v1beta1.KindTerminalSupervisor {
		t.Fatalf("apiVersion/kind = %q/%q", doc.APIVersion, doc.Kind)
	}
	if doc.Metadata.Name != "work" {
		t.Errorf("metadata.name = %q, want work", doc.Metadata.Name)
	}
	if doc.Status.Phase != v1beta1.SupervisorPhaseConfig || doc.Status.Version != buildVersion {
		t.Errorf("status = %+v", doc.Status.SupervisorStatus)
	}
	if doc.Status.PhaseSince.IsZero() || doc.Status.UpdatedAt.IsZero() {
		t.Errorf("phaseSince/updatedAt not stamped: %+v", doc.Status.SupervisorStatus)
	}
	if doc.Status.Exit != nil {
		t.Errorf("exit recorded before exit: %+v", doc.Status.Exit)
	}

	r.setPhase(v1beta1.SupervisorPhaseRepos)
	if got := readDoc(t, path).Status.Phase; got != v1beta1.SupervisorPhaseRepos {
		t.Errorf("phase after setPhase(Repos) = %q", got)
	}
}

func TestSupervisorReporter_AtomicReplaceLeavesNoTempFiles(t *testing.T) {
	r, path := newTestReporter(t)
	for i := range 5 {
		r.onRepoStart("repo")
		r.onRepoDone(v1beta1.RepoStatus{Name: "repo", State: v1beta1.RepoStateCloned, Commit: strings.Repeat("a", i+1)})
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "supervisor.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("directory holds %v, want only supervisor.json", names)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != supervisorDocMode {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), supervisorDocMode)
	}
}

func TestSupervisorReporter_WriteFailureIsNotFatal(t *testing.T) {
	r := newSupervisorReporter(filepath.Join(t.TempDir(), "missing", "supervisor.json"), "work")
	r.logger = discardLogger()
	r.setPhase(v1beta1.SupervisorPhaseConfig)
	r.exit(70)
	if got := r.snapshot().Status.Exit; got == nil || got.Code != 70 {
		t.Fatalf("in-memory exit = %+v, want code 70", got)
	}
}

func TestSupervisorReporter_NilIsNoOp(_ *testing.T) {
	var r *supervisorReporter
	r.setPhase(v1beta1.SupervisorPhaseRepos)
	r.onRepoStart("x")
	r.onRepoDone(v1beta1.RepoStatus{Name: "x"})
	r.onStageStart(0)
	r.onStageDone(v1beta1.StageStatus{})
	r.setError(errors.New("boom"))
	r.exit(1)
}

func TestSupervisorReporter_RepoAndStageCurrent(t *testing.T) {
	r, path := newTestReporter(t)
	r.setPhase(v1beta1.SupervisorPhaseRepos)

	r.onRepoStart("ok")
	if got := readDoc(t, path).Status.Current; got != "ok" {
		t.Errorf("current while cloning = %q, want ok", got)
	}
	r.onRepoDone(v1beta1.RepoStatus{Name: "ok", State: v1beta1.RepoStateCloned})
	if got := readDoc(t, path).Status.Current; got != "" {
		t.Errorf("current after a resolved repo = %q, want empty", got)
	}
	r.onRepoStart("bad")
	r.onRepoDone(v1beta1.RepoStatus{Name: "bad", State: v1beta1.RepoStateFailed, Error: "boom"})
	doc := readDoc(t, path)
	if doc.Status.Current != "bad" {
		t.Errorf("current after a failed repo = %q, want bad", doc.Status.Current)
	}
	if len(doc.Status.Repos) != 2 || doc.Status.Repos[1].Error != "boom" {
		t.Errorf("repos = %+v", doc.Status.Repos)
	}

	r.setPhase(v1beta1.SupervisorPhaseStages)
	r.onStageStart(3)
	r.onStageDone(v1beta1.StageStatus{Index: 3, State: v1beta1.StageStateFailed, Error: "exit 1"})
	doc = readDoc(t, path)
	if doc.Status.Phase != v1beta1.SupervisorPhaseStages || doc.Status.Current != "3" {
		t.Errorf("phase/current = %q/%q, want Stages/3", doc.Status.Phase, doc.Status.Current)
	}
	if len(doc.Status.Stages) != 1 || doc.Status.Stages[0].State != v1beta1.StageStateFailed {
		t.Errorf("stages = %+v", doc.Status.Stages)
	}
}

func TestSupervisorReporter_TerminalLifecycle(t *testing.T) {
	r, path := newTestReporter(t)
	r.startTerminals()
	doc := readDoc(t, path)
	if doc.Status.Phase != v1beta1.SupervisorPhaseRunning || doc.Status.AttachedTerminal != "main" {
		t.Fatalf("after startTerminals: %+v", doc.Status.SupervisorStatus)
	}
	if len(doc.Status.Terminals) != 1 || doc.Status.Terminals[0].ID != "main" {
		t.Fatalf("terminals = %+v", doc.Status.Terminals)
	}

	var calls atomic.Int32
	md := func() (*sbshapi.TerminalDoc, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("server: not started")
		}
		return &sbshapi.TerminalDoc{Status: sbshapi.TerminalStatus{
			State: sbshapi.Ready, Pid: 42, Attachers: []string{"client-1"},
		}}, nil
	}
	r.observeTerminal("main", md) // not started: ignored
	if got := readDoc(t, path).Status.Terminals[0]; got.State != "" {
		t.Errorf("state recorded from a failed read: %+v", got)
	}
	before := readDoc(t, path).Status.UpdatedAt
	r.now = func() time.Time { return before.Add(time.Minute) }
	r.observeTerminal("main", md)
	term := readDoc(t, path).Status.Terminals[0]
	if term.State != sbshapi.Ready.String() || term.PID != 42 || len(term.Attachers) != 1 {
		t.Errorf("terminal = %+v", term)
	}
	updated := readDoc(t, path).Status.UpdatedAt
	r.now = func() time.Time { return before.Add(2 * time.Minute) }
	r.observeTerminal("main", md) // unchanged: no rewrite
	if got := readDoc(t, path).Status.UpdatedAt; !got.Equal(updated) {
		t.Errorf("an unchanged terminal rewrote the document (%v → %v)", updated, got)
	}

	// sbsh's PID-1 carrier for a workload that exited 3.
	var wlErr *workloadExitError
	if err := serveResult(errors.New("shell process exited: code=3"), r); !errors.As(err, &wlErr) || wlErr.code != 3 {
		t.Fatalf("serveResult = %v, want a workload exit with code 3", err)
	}
	doc = readDoc(t, path)
	if doc.Status.Phase != v1beta1.SupervisorPhaseExited {
		t.Errorf("phase after Serve = %q, want Exited", doc.Status.Phase)
	}
	if e := doc.Status.Terminals[0].Exit; e == nil || e.Code != 3 {
		t.Errorf("terminal exit = %+v, want code 3", e)
	}
}

func TestSupervisorReporter_PollTerminalStopsOnCancel(t *testing.T) {
	r, path := newTestReporter(t)
	r.startTerminals()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	md := func() (*sbshapi.TerminalDoc, error) {
		return &sbshapi.TerminalDoc{Status: sbshapi.TerminalStatus{State: sbshapi.Ready, Pid: 7}}, nil
	}
	go func() {
		defer close(done)
		r.pollTerminal(ctx, "main", md, time.Millisecond)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pollTerminal did not return after cancel")
	}
	if got := readDoc(t, path).Status.Terminals[0].PID; got != 7 {
		t.Errorf("pid = %d, want 7", got)
	}
}

// The 2026-11-26 case: a required repo cannot be cloned. The document ends at
// phase Repos, names the repo, carries git's error and records exit 70.
func TestRunSetup_RequiredRepoFailureRecorded(t *testing.T) {
	r, path := newTestReporter(t)
	dir := t.TempDir()
	cspec := v1beta1.ContainerSpec{Repos: []v1beta1.ContainerRepo{
		{Name: "optional", Target: filepath.Join(dir, "o"), URL: filepath.Join(dir, "nope-o")},
		{Name: "role-source", Target: filepath.Join(dir, "r"), URL: filepath.Join(dir, "nope-r"), Required: true},
		{Name: "after", Target: filepath.Join(dir, "a"), URL: makeSourceRepo(t, "README")},
	}}
	r.setPhase(v1beta1.SupervisorPhaseConfig)
	err := runSetup(context.Background(), cspec, nil, discardLogger(), r)
	if !errors.Is(err, errRequiredRepoFailed) {
		t.Fatalf("runSetup = %v, want errRequiredRepoFailed", err)
	}
	recordRunError(r, err)
	code := exitCode(err, &bytes.Buffer{})
	r.exit(code)

	doc := readDoc(t, path)
	st := doc.Status
	if st.Phase != v1beta1.SupervisorPhaseRepos || st.Current != "role-source" {
		t.Errorf("phase/current = %q/%q, want Repos/role-source", st.Phase, st.Current)
	}
	if st.Error != "" {
		t.Errorf("error = %q, want empty (the failure is the repo's)", st.Error)
	}
	if len(st.Repos) != 3 {
		t.Fatalf("repos = %+v, want all three", st.Repos)
	}
	if st.Repos[1].Name != "role-source" || st.Repos[1].State != v1beta1.RepoStateFailed || st.Repos[1].Error == "" {
		t.Errorf("role-source = %+v, want failed with git's error", st.Repos[1])
	}
	if st.Repos[2].State != v1beta1.RepoStateCloned {
		t.Errorf("after = %+v, want cloned", st.Repos[2])
	}
	if st.Exit == nil || st.Exit.Code != exitCodeInternal {
		t.Errorf("exit = %+v, want code %d", st.Exit, exitCodeInternal)
	}
}

func TestRunSetup_FailedCreateStageRecorded(t *testing.T) {
	r, path := newTestReporter(t)
	cspec := v1beta1.ContainerSpec{Tty: &v1beta1.ContainerTty{OnInit: []v1beta1.TtyStage{
		{Script: "true", RunOn: v1beta1.RunOnCreate},
		{Script: "echo start-stage", RunOn: v1beta1.RunOnStart},
		{Script: "echo boom >&2; exit 3", RunOn: v1beta1.RunOnCreate},
	}}}
	err := runSetup(context.Background(), cspec, nil, discardLogger(), r)
	if !errors.Is(err, errRequiredStageFailed) {
		t.Fatalf("runSetup = %v, want errRequiredStageFailed", err)
	}
	st := readDoc(t, path).Status
	if st.Phase != v1beta1.SupervisorPhaseStages || st.Current != "2" {
		t.Errorf("phase/current = %q/%q, want Stages/2", st.Phase, st.Current)
	}
	if len(st.Stages) != 2 || st.Stages[0].State != v1beta1.StageStateDone ||
		st.Stages[1].Index != 2 || st.Stages[1].State != v1beta1.StageStateFailed ||
		!strings.Contains(st.Stages[1].Error, "boom") {
		t.Errorf("stages = %+v", st.Stages)
	}
	if len(st.Repos) != 0 {
		t.Errorf("repos = %+v, want none", st.Repos)
	}
}

func TestRunSetup_NoWorkEntersNoPhase(t *testing.T) {
	r, path := newTestReporter(t)
	r.setPhase(v1beta1.SupervisorPhaseConfig)
	if err := runSetup(context.Background(), v1beta1.ContainerSpec{}, nil, discardLogger(), r); err != nil {
		t.Fatal(err)
	}
	if got := readDoc(t, path).Status.Phase; got != v1beta1.SupervisorPhaseConfig {
		t.Errorf("phase = %q, want Config", got)
	}
}

func TestExitCodeAndRecordRunError(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantCode  int
		wantError bool
	}{
		{"clean", nil, 0, false},
		{"usage", &usageError{msg: "bad flag"}, exitCodeUsage, true},
		{"workload", &workloadExitError{code: 9}, 9, false},
		{"required repo", errRequiredRepoFailed, exitCodeInternal, false},
		{"required stage", errRequiredStageFailed, exitCodeInternal, false},
		{
			"internal",
			errors.New("listen on /run/kukeon/tty/terminals/main/control.sock: denied"),
			exitCodeInternal,
			true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, path := newTestReporter(t)
			r.setPhase(v1beta1.SupervisorPhaseConfig)
			recordRunError(r, tc.err)
			var stderr bytes.Buffer
			code := exitCode(tc.err, &stderr)
			r.exit(code)
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
			st := readDoc(t, path).Status
			if (st.Error != "") != tc.wantError {
				t.Errorf("status.error = %q, want set=%v", st.Error, tc.wantError)
			}
			if st.Exit == nil || st.Exit.Code != tc.wantCode {
				t.Errorf("exit = %+v, want code %d", st.Exit, tc.wantCode)
			}
		})
	}
}
