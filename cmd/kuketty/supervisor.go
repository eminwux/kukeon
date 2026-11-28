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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
)

// buildVersion is kuketty's build version as the Go toolchain stamped it (the
// main module's version, else the VCS revision), reported in the supervisor
// document. "dev" when the build carries neither.
//
//nolint:gochecknoglobals // computed once at start
var buildVersion = readBuildVersion()

func readBuildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	var revision, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if revision == "" {
		return "dev"
	}
	const shortRevision = 12
	if len(revision) > shortRevision {
		revision = revision[:shortRevision]
	}
	if modified == "true" {
		revision += "-dirty"
	}
	return revision
}

// supervisorDocPath is where kuketty writes its status document (kind:
// TerminalSupervisor, issue #1711), inside the tty bind mount so kukeond reads
// it from the host. A variable so tests can point it at a temp dir. The
// host-side peer is fs.ContainerSupervisorPath.
//
//nolint:gochecknoglobals // overridden by tests
var supervisorDocPath = attachableTTYDir + "/supervisor.json"

// attachedTerminalID is the terminal whose workload exit becomes kuketty's
// exit code. kuketty serves exactly one terminal today.
const attachedTerminalID = "main"

// supervisorDocMode is the document's file mode: readable by the kukeon group,
// like the log next to it.
const supervisorDocMode os.FileMode = 0o640

// supervisorReporter owns kuketty's TerminalSupervisor document and rewrites
// it, through a temporary file and a rename, after every change: every phase
// change, the start and end of every repo and stage, every terminal status
// change and kuketty's exit. kukeond reads it in any container state, so the
// document is both the live view and the final record (issue #1711).
//
// A failed write is logged and never changes kuketty's exit code. All methods
// are safe for concurrent use and are no-ops on a nil receiver, so the setup
// helpers can run without one in tests.
type supervisorReporter struct {
	mu     sync.Mutex
	path   string
	doc    v1beta1.TerminalSupervisorDoc
	now    func() time.Time
	logger *slog.Logger
	// terminalFinal is set once the attached terminal's final document has
	// been recorded; sbsh observer calls that arrive later are ignored.
	terminalFinal bool
}

// newSupervisorReporter returns a reporter for the container name that writes
// to path. Nothing is written until the first phase is set.
func newSupervisorReporter(path, name string) *supervisorReporter {
	return &supervisorReporter{
		path: path,
		doc: v1beta1.TerminalSupervisorDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindTerminalSupervisor,
			Metadata:   v1beta1.TerminalSupervisorMetadata{Name: name},
			Status: v1beta1.TerminalSupervisorStatus{
				SupervisorStatus: v1beta1.SupervisorStatus{Version: buildVersion},
			},
		},
		now: func() time.Time { return time.Now().UTC() },
		// kuketty's stderr is discarded by the runtime, but it is the only
		// sink until the file logger opens.
		logger: slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
}

// setLogger routes write failures to kuketty's file logger once it is open.
func (r *supervisorReporter) setLogger(logger *slog.Logger) {
	if r == nil || logger == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logger = logger
}

// setName records the container name once the ContainerDoc has been read.
func (r *supervisorReporter) setName(name string) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		if s.Metadata.Name == name {
			return false
		}
		s.Metadata.Name = name
		return true
	})
}

// setPhase enters phase. The repo or stage in progress is cleared.
func (r *supervisorReporter) setPhase(phase v1beta1.SupervisorPhase) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Phase = phase
		s.Status.PhaseSince = r.now()
		s.Status.Current = ""
		return true
	})
}

// setCurrent names the repo or stage the supervisor is on.
func (r *supervisorReporter) setCurrent(current string) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		if s.Status.Current == current {
			return false
		}
		s.Status.Current = current
		return true
	})
}

// setError records a failure outside any repo or stage.
func (r *supervisorReporter) setError(err error) {
	if err == nil {
		return
	}
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Error = err.Error()
		return true
	})
}

// onRepoStart implements repoObserver.
func (r *supervisorReporter) onRepoStart(name string) { r.setCurrent(name) }

// onRepoDone implements repoObserver. A failed repo stays current, so the
// document names it; a resolved one clears it.
func (r *supervisorReporter) onRepoDone(status v1beta1.RepoStatus) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Repos = append(s.Status.Repos, status)
		if status.State != v1beta1.RepoStateFailed {
			s.Status.Current = ""
		}
		return true
	})
}

// onStageStart implements stageObserver.
func (r *supervisorReporter) onStageStart(index int) { r.setCurrent(strconv.Itoa(index)) }

// onStageDone implements stageObserver. Like a repo, a failed stage stays
// current.
func (r *supervisorReporter) onStageDone(status v1beta1.StageStatus) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Stages = append(s.Status.Stages, status)
		if status.State != v1beta1.StageStateFailed {
			s.Status.Current = ""
		}
		return true
	})
}

// startTerminals enters phase Running with the single attached terminal.
func (r *supervisorReporter) startTerminals() {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Phase = v1beta1.SupervisorPhaseRunning
		s.Status.PhaseSince = r.now()
		s.Status.Current = ""
		s.Status.AttachedTerminal = attachedTerminalID
		s.Status.Terminals = []v1beta1.SupervisorTerminalStatus{{ID: attachedTerminalID}}
		return true
	})
}

// replaceTerminal replaces the terminal entry with t's ID, or appends t, and
// reports whether the document changed. A recorded exit is kept when t
// carries none.
func replaceTerminal(s *v1beta1.TerminalSupervisorDoc, t v1beta1.SupervisorTerminalStatus) bool {
	for i := range s.Status.Terminals {
		if s.Status.Terminals[i].ID != t.ID {
			continue
		}
		if t.Exit == nil {
			t.Exit = s.Status.Terminals[i].Exit
		}
		if reflect.DeepEqual(s.Status.Terminals[i], t) {
			return false
		}
		s.Status.Terminals[i] = t
		return true
	}
	s.Status.Terminals = append(s.Status.Terminals, t)
	return true
}

// terminalsExited enters phase Exited: the attached terminal ended.
func (r *supervisorReporter) terminalsExited() {
	r.setPhase(v1beta1.SupervisorPhaseExited)
}

// exit records the code kuketty is about to exit with. Called on every exit
// path, immediately before os.Exit.
func (r *supervisorReporter) exit(code int) {
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		s.Status.Exit = &v1beta1.SupervisorExit{Code: code, At: r.now()}
		return true
	})
}

// snapshot returns a copy of the document, for tests.
func (r *supervisorReporter) snapshot() v1beta1.TerminalSupervisorDoc {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := r.doc
	out.Status.SupervisorStatus = *r.doc.Status.SupervisorStatus.DeepCopy()
	out.Status.Repos = append([]v1beta1.RepoStatus(nil), r.doc.Status.Repos...)
	out.Status.Stages = append([]v1beta1.StageStatus(nil), r.doc.Status.Stages...)
	return out
}

// update applies mutate under the lock and, when it reports a change, writes
// the document.
func (r *supervisorReporter) update(mutate func(*v1beta1.TerminalSupervisorDoc) bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !mutate(&r.doc) {
		return
	}
	r.doc.Status.UpdatedAt = r.now()
	if err := r.write(); err != nil {
		r.logger.Warn("write supervisor status", "path", r.path, "error", err)
	}
}

// write replaces the document through a temporary file in the same directory
// and a rename, so a reader sees either the previous version or this one.
func (r *supervisorReporter) write() error {
	data, err := json.Marshal(&r.doc)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	dir := filepath.Dir(r.path)
	tmp, err := os.CreateTemp(dir, ".supervisor.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err = os.Chmod(tmpName, supervisorDocMode); err != nil {
		cleanup()
		return err
	}
	if err = os.Rename(tmpName, r.path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// observeTerminal is kuketty's sbsh status observer: sbsh calls it with the
// terminal's document after every committed status change (state, attach,
// detach, exit), so the attached terminal's entry follows it without polling
// (issue #1721). Calls after finalTerminal are ignored.
func (r *supervisorReporter) observeTerminal(doc sbshapi.TerminalDoc) {
	if r == nil {
		return
	}
	t := terminalStatusFromSbsh(attachedTerminalID, doc.Status)
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		if r.terminalFinal {
			return false
		}
		return replaceTerminal(s, t)
	})
}

// finalTerminal records the attached terminal's final document, read after
// Serve returned, and stops observeTerminal from overwriting it: sbsh may
// still deliver an earlier document after this one. A nil doc (sbsh never
// started the terminal) only stops the observer.
func (r *supervisorReporter) finalTerminal(doc *sbshapi.TerminalDoc) {
	if r == nil {
		return
	}
	r.update(func(s *v1beta1.TerminalSupervisorDoc) bool {
		r.terminalFinal = true
		if doc == nil {
			return false
		}
		return replaceTerminal(s, terminalStatusFromSbsh(attachedTerminalID, doc.Status))
	})
}

// terminalStatusFromSbsh copies the fields kukeon reports from sbsh's
// terminal status. kukeon's API carries its own fields, not sbsh's type.
// Once sbsh has recorded the workload's exit, Exit carries its code, or the
// signal that killed it with the shell's 128+N code, and the time.
func terminalStatusFromSbsh(id string, st sbshapi.TerminalStatus) v1beta1.SupervisorTerminalStatus {
	out := v1beta1.SupervisorTerminalStatus{
		ID:             id,
		State:          st.State.String(),
		PID:            st.Pid,
		LastAttachedAt: st.LastAttachedAt,
	}
	if len(st.Attachers) > 0 {
		out.Attachers = append([]string(nil), st.Attachers...)
	}
	switch {
	case st.ExitCode != nil:
		out.Exit = &v1beta1.TerminalExit{Code: *st.ExitCode, At: st.ExitedAt}
	case st.ExitSignal != "":
		out.Exit = &v1beta1.TerminalExit{
			Code:   signalExitCode(st.ExitSignal),
			Signal: st.ExitSignal,
			At:     st.ExitedAt,
		}
	}
	return out
}
