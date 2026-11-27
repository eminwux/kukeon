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
	"io"
	"io/fs"
	"os"

	"github.com/eminwux/kukeon/internal/consts"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// maxSupervisorDocBytes caps how much of tty/supervisor.json the daemon reads.
// The workload can write to the tty directory, so the document is untrusted
// display data; a real one is a few KiB.
const maxSupervisorDocBytes = 256 << 10

// supervisorDocState classifies one read of a container's supervisor document.
type supervisorDocState int

const (
	// supervisorDocMissing: no document (not yet written, deleted before a
	// start, or written by no supervisor at all — a kuketty that predates
	// #1711).
	supervisorDocMissing supervisorDocState = iota
	// supervisorDocValid: a well-formed v1beta1 TerminalSupervisor document.
	supervisorDocValid
	// supervisorDocUnsupported: a document of another apiVersion, e.g. from a
	// kuketty newer or older than the daemon. The container must be recreated
	// to run the daemon's kuketty.
	supervisorDocUnsupported
	// supervisorDocInvalid: anything else — not a regular file, oversized,
	// undecodable, the wrong kind or an unknown phase.
	supervisorDocInvalid
)

func (s supervisorDocState) String() string {
	switch s {
	case supervisorDocMissing:
		return "missing"
	case supervisorDocValid:
		return "valid"
	case supervisorDocUnsupported:
		return "unsupported"
	case supervisorDocInvalid:
		return "invalid"
	}
	return "unknown"
}

// supervisorDocResult is the outcome of readSupervisorDoc. Doc is set only
// when State is supervisorDocValid; Detail says why a document was rejected.
type supervisorDocResult struct {
	State  supervisorDocState
	Doc    *v1beta1.TerminalSupervisorDoc
	Detail string
}

// readSupervisorDoc reads the TerminalSupervisor document kuketty keeps in a
// container's host-side tty directory (issue #1711).
//
// The read never leaves ttyDir (os.Root), refuses anything but a regular file
// so a symlink planted by the workload cannot redirect it, and stops after
// maxSupervisorDocBytes. A document is accepted only with apiVersion v1beta1,
// kind TerminalSupervisor and a known phase. Unknown fields are allowed, so
// additions within v1beta1 stay readable.
func readSupervisorDoc(ttyDir string) supervisorDocResult {
	root, err := os.OpenRoot(ttyDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return supervisorDocResult{State: supervisorDocMissing}
		}
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("open tty directory: %v", err)}
	}
	defer root.Close()

	name := consts.KukeonContainerSupervisorFile
	info, err := root.Lstat(name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return supervisorDocResult{State: supervisorDocMissing}
		}
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("stat: %v", err)}
	}
	if !info.Mode().IsRegular() {
		return supervisorDocResult{
			State:  supervisorDocInvalid,
			Detail: "not a regular file (" + info.Mode().Type().String() + ")",
		}
	}
	if info.Size() > maxSupervisorDocBytes {
		return supervisorDocResult{
			State:  supervisorDocInvalid,
			Detail: fmt.Sprintf("%d bytes, over the %d-byte limit", info.Size(), maxSupervisorDocBytes),
		}
	}
	f, err := root.Open(name)
	if err != nil {
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("open: %v", err)}
	}
	defer f.Close()
	// Re-check the opened file: the name may have been swapped after Lstat.
	if opened, statErr := f.Stat(); statErr != nil || !opened.Mode().IsRegular() {
		return supervisorDocResult{State: supervisorDocInvalid, Detail: "not a regular file"}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxSupervisorDocBytes+1))
	if err != nil {
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("read: %v", err)}
	}
	if len(data) > maxSupervisorDocBytes {
		return supervisorDocResult{
			State:  supervisorDocInvalid,
			Detail: fmt.Sprintf("over the %d-byte limit", maxSupervisorDocBytes),
		}
	}
	return parseSupervisorDoc(data)
}

// parseSupervisorDoc validates and decodes a supervisor document's bytes.
func parseSupervisorDoc(data []byte) supervisorDocResult {
	var head struct {
		APIVersion v1beta1.Version `json:"apiVersion"`
		Kind       v1beta1.Kind    `json:"kind"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("decode: %v", err)}
	}
	if head.Kind != v1beta1.KindTerminalSupervisor {
		return supervisorDocResult{
			State:  supervisorDocInvalid,
			Detail: fmt.Sprintf("kind %q, want %q", head.Kind, v1beta1.KindTerminalSupervisor),
		}
	}
	if head.APIVersion != v1beta1.APIVersionV1Beta1 {
		return supervisorDocResult{
			State:  supervisorDocUnsupported,
			Detail: fmt.Sprintf("apiVersion %q, want %q", head.APIVersion, v1beta1.APIVersionV1Beta1),
		}
	}
	var doc v1beta1.TerminalSupervisorDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return supervisorDocResult{State: supervisorDocInvalid, Detail: fmt.Sprintf("decode: %v", err)}
	}
	switch doc.Status.Phase {
	case v1beta1.SupervisorPhaseConfig, v1beta1.SupervisorPhaseRepos, v1beta1.SupervisorPhaseStages,
		v1beta1.SupervisorPhaseRunning, v1beta1.SupervisorPhaseExited:
	default:
		return supervisorDocResult{
			State:  supervisorDocInvalid,
			Detail: fmt.Sprintf("unknown phase %q", doc.Status.Phase),
		}
	}
	return supervisorDocResult{State: supervisorDocValid, Doc: &doc}
}

// removeSupervisorDoc deletes a container's leftover supervisor document
// before its task starts, so a record from the previous run is never credited
// to the new one. A missing document is not an error.
func removeSupervisorDoc(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove leftover supervisor status %q: %w", path, err)
	}
	return nil
}

// supervisorRepoStatusesToInternal copies the document's per-repo outcomes.
func supervisorRepoStatusesToInternal(in []v1beta1.RepoStatus) []intmodel.RepoStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.RepoStatus, len(in))
	for i, s := range in {
		out[i] = intmodel.RepoStatus{Name: s.Name, Target: s.Target, State: s.State, Commit: s.Commit, Error: s.Error}
	}
	return out
}

// supervisorStageStatusesToInternal copies the document's per-stage outcomes.
// kuketty does not stamp Hash; mergeStageStatuses restamps it from the spec.
func supervisorStageStatusesToInternal(in []v1beta1.StageStatus) []intmodel.StageStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.StageStatus, len(in))
	for i, s := range in {
		out[i] = intmodel.StageStatus{Index: s.Index, State: s.State, Error: s.Error}
	}
	return out
}

// containerTaskStopped reports whether the observed state means the task is no
// longer running.
func containerTaskStopped(s intmodel.ContainerState) bool {
	switch s {
	case intmodel.ContainerStateStopped, intmodel.ContainerStateExited,
		intmodel.ContainerStateError, intmodel.ContainerStateFailed:
		return true
	case intmodel.ContainerStatePending, intmodel.ContainerStateReady, intmodel.ContainerStatePaused,
		intmodel.ContainerStatePausing, intmodel.ContainerStateUnknown, intmodel.ContainerStateNotCreated:
	}
	return false
}

// applySupervisorDoc folds a valid document into status: the repos, the
// supervisor view and the live stages to merge. containerd wins: when the task
// has stopped, the document's exit is kept only if its code matches the one
// containerd recorded.
func applySupervisorDoc(status *intmodel.ContainerStatus, doc *v1beta1.TerminalSupervisorDoc) []intmodel.StageStatus {
	status.Repos = supervisorRepoStatusesToInternal(doc.Status.Repos)
	sup := doc.Status.SupervisorStatus.DeepCopy()
	if sup.Exit != nil && containerTaskStopped(status.State) && sup.Exit.Code != status.ExitCode {
		sup.Exit = nil
	}
	status.Supervisor = sup
	return supervisorStageStatusesToInternal(doc.Status.Stages)
}
