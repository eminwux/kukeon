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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
	sbshserver "github.com/eminwux/sbsh/pkg/terminal/server"
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

// TestSupervisorReporter_ObserverUpdatesTerminal checks that each sbsh
// observer call updates terminals[0] on its own, with no poll (issue #1721),
// and that an unchanged document is not rewritten.
func TestSupervisorReporter_ObserverUpdatesTerminal(t *testing.T) {
	r, path := newTestReporter(t)
	r.startTerminals()
	doc := readDoc(t, path)
	if doc.Status.Phase != v1beta1.SupervisorPhaseRunning || doc.Status.AttachedTerminal != "main" {
		t.Fatalf("after startTerminals: %+v", doc.Status.SupervisorStatus)
	}
	if len(doc.Status.Terminals) != 1 || doc.Status.Terminals[0].ID != "main" {
		t.Fatalf("terminals = %+v", doc.Status.Terminals)
	}

	attachedAt := time.Date(2026, 11, 26, 19, 51, 0, 0, time.UTC)
	steps := []struct {
		name   string
		status sbshapi.TerminalStatus
	}{
		{"ready", sbshapi.TerminalStatus{State: sbshapi.Ready, Pid: 42}},
		{"attach", sbshapi.TerminalStatus{
			State: sbshapi.Ready, Pid: 42, Attachers: []string{"client-1"}, LastAttachedAt: attachedAt,
		}},
		{"detach", sbshapi.TerminalStatus{State: sbshapi.Ready, Pid: 42, LastAttachedAt: attachedAt}},
	}
	for i, step := range steps {
		r.now = func() time.Time { return attachedAt.Add(time.Duration(i+1) * time.Minute) }
		r.observeTerminal(sbshapi.TerminalDoc{Status: step.status})
		term := readDoc(t, path).Status.Terminals[0]
		want := terminalStatusFromSbsh("main", step.status)
		if term.State != want.State || term.PID != 42 || len(term.Attachers) != len(want.Attachers) ||
			!term.LastAttachedAt.Equal(want.LastAttachedAt) {
			t.Errorf("%s: terminal = %+v, want %+v", step.name, term, want)
		}
	}

	updated := readDoc(t, path).Status.UpdatedAt
	r.now = func() time.Time { return updated.Add(time.Hour) }
	r.observeTerminal(sbshapi.TerminalDoc{Status: steps[len(steps)-1].status})
	if got := readDoc(t, path).Status.UpdatedAt; !got.Equal(updated) {
		t.Errorf("an unchanged terminal rewrote the document (%v → %v)", updated, got)
	}
}

// TestSupervisorReporter_FinalTerminalWinsOverLateObserver checks that the
// final document read after Serve is kept, with its exit, when sbsh delivers
// an older document afterwards.
func TestSupervisorReporter_FinalTerminalWinsOverLateObserver(t *testing.T) {
	r, path := newTestReporter(t)
	r.startTerminals()
	exitedAt := time.Date(2026, 11, 26, 19, 55, 0, 0, time.UTC)
	three := 3
	r.finalTerminal(&sbshapi.TerminalDoc{Status: sbshapi.TerminalStatus{
		State: sbshapi.Exited, Pid: 42, ExitCode: &three, ExitedAt: exitedAt,
	}})
	r.observeTerminal(sbshapi.TerminalDoc{Status: sbshapi.TerminalStatus{State: sbshapi.Ready, Pid: 42}})

	term := readDoc(t, path).Status.Terminals[0]
	if term.State != sbshapi.Exited.String() {
		t.Errorf("state = %q, want Exited", term.State)
	}
	if term.Exit == nil || term.Exit.Code != 3 || term.Exit.Signal != "" || !term.Exit.At.Equal(exitedAt) {
		t.Errorf("exit = %+v, want code 3 at %v", term.Exit, exitedAt)
	}
}

// TestSupervisorReporter_FinalTerminalNilStopsObserver covers a terminal sbsh
// never started: there is no final document, and later observer calls are
// still ignored.
func TestSupervisorReporter_FinalTerminalNilStopsObserver(t *testing.T) {
	r, path := newTestReporter(t)
	r.startTerminals()
	r.finalTerminal(nil)
	r.observeTerminal(sbshapi.TerminalDoc{Status: sbshapi.TerminalStatus{State: sbshapi.Ready, Pid: 42}})
	if term := readDoc(t, path).Status.Terminals[0]; term.State != "" || term.Exit != nil {
		t.Errorf("terminal = %+v, want the empty entry startTerminals wrote", term)
	}
}

func TestTerminalStatusFromSbsh_Exit(t *testing.T) {
	exitedAt := time.Date(2026, 11, 26, 19, 55, 0, 0, time.UTC)
	zero, three := 0, 3
	cases := []struct {
		name string
		st   sbshapi.TerminalStatus
		want *v1beta1.TerminalExit
	}{
		{"running", sbshapi.TerminalStatus{State: sbshapi.Ready}, nil},
		{
			"clean exit",
			sbshapi.TerminalStatus{State: sbshapi.Exited, ExitCode: &zero, ExitedAt: exitedAt},
			&v1beta1.TerminalExit{Code: 0, At: exitedAt},
		},
		{
			"exit 3",
			sbshapi.TerminalStatus{State: sbshapi.Exited, ExitCode: &three, ExitedAt: exitedAt},
			&v1beta1.TerminalExit{Code: 3, At: exitedAt},
		},
		{
			"killed by SIGKILL",
			sbshapi.TerminalStatus{State: sbshapi.Exited, ExitSignal: "SIGKILL", ExitedAt: exitedAt},
			&v1beta1.TerminalExit{Code: 137, Signal: "SIGKILL", At: exitedAt},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := terminalStatusFromSbsh("main", tc.st).Exit
			if (got == nil) != (tc.want == nil) {
				t.Fatalf("exit = %+v, want %+v", got, tc.want)
			}
			if got != nil && (got.Code != tc.want.Code || got.Signal != tc.want.Signal || !got.At.Equal(tc.want.At)) {
				t.Errorf("exit = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestServeResult maps Serve's terminating cause and records the final
// terminal document.
func TestServeResult(t *testing.T) {
	three := 3
	exited := func(st sbshapi.TerminalStatus) terminalMetadataFunc {
		return func() (*sbshapi.TerminalDoc, error) { return &sbshapi.TerminalDoc{Status: st}, nil }
	}
	notStarted := func() (*sbshapi.TerminalDoc, error) { return nil, errors.New("server: not started") }
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name     string
		ctx      context.Context
		serveErr error
		metadata terminalMetadataFunc
		wantCode int // kuketty's exit code for run()'s result
		wantExit *v1beta1.TerminalExit
	}{
		{
			name:     "workload exit 3",
			ctx:      context.Background(),
			serveErr: &sbshserver.ExitError{Code: &three},
			metadata: exited(sbshapi.TerminalStatus{State: sbshapi.Exited, ExitCode: &three}),
			wantCode: 3,
			wantExit: &v1beta1.TerminalExit{Code: 3},
		},
		{
			name:     "workload killed",
			ctx:      context.Background(),
			serveErr: &sbshserver.ExitError{Signal: "SIGKILL"},
			metadata: exited(sbshapi.TerminalStatus{State: sbshapi.Exited, ExitSignal: "SIGKILL"}),
			wantCode: 137,
			wantExit: &v1beta1.TerminalExit{Code: 137, Signal: "SIGKILL"},
		},
		{
			name:     "operator shutdown",
			ctx:      cancelled,
			serveErr: context.Canceled,
			metadata: exited(sbshapi.TerminalStatus{State: sbshapi.Exited}),
			wantCode: 0,
		},
		{
			// sbsh's teardown ends the wait on the workload without a status
			// once kuketty's context is cancelled.
			name:     "teardown without exit status after cancel",
			ctx:      cancelled,
			serveErr: errors.New("shell process exited (close cancelled tracked-exit wait)"),
			metadata: exited(sbshapi.TerminalStatus{State: sbshapi.Exited}),
			wantCode: 0,
		},
		{
			name:     "internal failure",
			ctx:      context.Background(),
			serveErr: errors.New("server: StartTerminal: fork failed"),
			metadata: notStarted,
			wantCode: exitCodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, path := newTestReporter(t)
			r.startTerminals()
			err := serveResult(tc.ctx, tc.serveErr, tc.metadata, r)
			if code := exitCode(err, io.Discard); code != tc.wantCode {
				t.Errorf("exit code = %d (err %v), want %d", code, err, tc.wantCode)
			}
			doc := readDoc(t, path)
			if doc.Status.Phase != v1beta1.SupervisorPhaseExited {
				t.Errorf("phase after Serve = %q, want Exited", doc.Status.Phase)
			}
			got := doc.Status.Terminals[0].Exit
			if (got == nil) != (tc.wantExit == nil) ||
				(got != nil && (got.Code != tc.wantExit.Code || got.Signal != tc.wantExit.Signal)) {
				t.Errorf("terminal exit = %+v, want %+v", got, tc.wantExit)
			}
		})
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
