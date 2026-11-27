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

package v1beta1

import "time"

// TerminalSupervisorDoc is the status document kuketty, an attachable
// container's terminal supervisor, writes to tty/supervisor.json (kind:
// TerminalSupervisor, issue #1711). kuketty rewrites it through a temporary
// file and a rename on every change, so it is both the live view and the
// final record of a run. kukeond reads it on every status refresh and copies
// it into ContainerStatus; it is never stored as a resource of its own.
//
// The workload can write to the tty directory, so a reader treats the
// document as display data: size-capped, checked for apiVersion, kind and
// schema, and never used for control decisions.
type TerminalSupervisorDoc struct {
	APIVersion Version                    `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind                       `json:"kind"       yaml:"kind"`
	Metadata   TerminalSupervisorMetadata `json:"metadata"   yaml:"metadata"`
	Status     TerminalSupervisorStatus   `json:"status"     yaml:"status"`
}

// TerminalSupervisorMetadata names the container the supervisor runs in.
type TerminalSupervisorMetadata struct {
	Name string `json:"name" yaml:"name"`
}

// TerminalSupervisorStatus is the supervisor's full report: the part
// ContainerStatus.Supervisor carries, plus the per-repo and per-stage
// outcomes that land in ContainerStatus.Repos and .Stages. Embedding
// SupervisorStatus keeps the two shapes from drifting.
type TerminalSupervisorStatus struct {
	SupervisorStatus `json:",inline" yaml:",inline"`

	Repos  []RepoStatus  `json:"repos,omitempty"  yaml:"repos,omitempty"`
	Stages []StageStatus `json:"stages,omitempty" yaml:"stages,omitempty"`
}

// SupervisorPhase is the last phase a terminal supervisor entered.
type SupervisorPhase string

const (
	// SupervisorPhaseConfig means reading the ContainerDoc, building the terminal
	// spec, claiming the control socket and opening the log.
	SupervisorPhaseConfig SupervisorPhase = "Config"
	// SupervisorPhaseRepos means cloning or fetching the container's repos.
	SupervisorPhaseRepos SupervisorPhase = "Repos"
	// SupervisorPhaseStages means running the container's runOn: create stages.
	SupervisorPhaseStages SupervisorPhase = "Stages"
	// SupervisorPhaseRunning means the terminals are served.
	SupervisorPhaseRunning SupervisorPhase = "Running"
	// SupervisorPhaseExited means the attached terminal ended. A supervisor that
	// fails in an earlier phase stays at that phase and records Exit.
	SupervisorPhaseExited SupervisorPhase = "Exited"
)

// SupervisorStatus is what a terminal supervisor reports about itself and
// its terminals. ContainerStatus.Supervisor carries it.
type SupervisorStatus struct {
	// Version is the supervisor's build version.
	Version string `json:"version,omitempty"          yaml:"version,omitempty"`
	// Phase is the last phase the supervisor entered.
	Phase SupervisorPhase `json:"phase,omitempty"            yaml:"phase,omitempty"`
	// PhaseSince is when the supervisor entered Phase.
	PhaseSince time.Time `json:"phaseSince,omitempty"       yaml:"phaseSince,omitempty"`
	// Current names the repo or the stage index in progress.
	Current string `json:"current,omitempty"          yaml:"current,omitempty"`
	// Error is a failure outside any repo or stage: the config, the control
	// socket, the log, the git plan or the terminal start.
	Error string `json:"error,omitempty"            yaml:"error,omitempty"`
	// AttachedTerminal is the terminal whose workload exit becomes the
	// supervisor's exit code. Today it is always "main".
	AttachedTerminal string                     `json:"attachedTerminal,omitempty" yaml:"attachedTerminal,omitempty"`
	Terminals        []SupervisorTerminalStatus `json:"terminals,omitempty"        yaml:"terminals,omitempty"`
	// Exit is the code the supervisor exits with, recorded immediately
	// before it exits. Nil while it runs, and after it was killed.
	Exit      *SupervisorExit `json:"exit,omitempty"             yaml:"exit,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt,omitempty"        yaml:"updatedAt,omitempty"`
}

// SupervisorTerminalStatus is one terminal the supervisor serves. The
// fields are kukeon's own, copied from sbsh's terminal status, so the API
// does not depend on sbsh types.
type SupervisorTerminalStatus struct {
	ID string `json:"id"                       yaml:"id"`
	// State is the terminal state as sbsh names it (Initializing, Starting,
	// Ready, Exited, …).
	State          string        `json:"state,omitempty"          yaml:"state,omitempty"`
	PID            int           `json:"pid,omitempty"            yaml:"pid,omitempty"`
	Attachers      []string      `json:"attachers,omitempty"      yaml:"attachers,omitempty"`
	LastAttachedAt time.Time     `json:"lastAttachedAt,omitempty" yaml:"lastAttachedAt,omitempty"`
	Exit           *TerminalExit `json:"exit,omitempty"           yaml:"exit,omitempty"`
}

// TerminalExit is how a terminal's workload exited.
type TerminalExit struct {
	Code   int       `json:"code"             yaml:"code"`
	Signal string    `json:"signal,omitempty" yaml:"signal,omitempty"`
	At     time.Time `json:"at,omitempty"     yaml:"at,omitempty"`
}

// SupervisorExit is the code a supervisor exited with.
type SupervisorExit struct {
	Code int       `json:"code"         yaml:"code"`
	At   time.Time `json:"at,omitempty" yaml:"at,omitempty"`
}

// Per-repo states reported in RepoStatus.State.
const (
	// RepoStateCloned means the target had no checkout and was cloned.
	RepoStateCloned = "cloned"
	// RepoStateFetched means the target had a checkout and was fetched and
	// fast-forwarded.
	RepoStateFetched = "fetched"
	// RepoStateFailed means the clone or fetch failed; Error carries git's error.
	RepoStateFailed = "failed"
)

// Per-stage states reported in StageStatus.State.
const (
	// StageStateDone means the stage's script ran to completion.
	StageStateDone = "done"
	// StageStateFailed means the stage's script failed; Error carries the detail.
	StageStateFailed = "failed"
)

// Container failure reasons, set in ContainerStatus.Reason when a container
// ended on its own with a non-zero exit (issue #1711). The cell's failure
// reason is that of its failing container.
const (
	// ContainerReasonRepoFailed means the supervisor exited while cloning or
	// fetching a repo.
	ContainerReasonRepoFailed = "RepoFailed"
	// ContainerReasonStageFailed means the supervisor exited while running a
	// runOn: create stage.
	ContainerReasonStageFailed = "StageFailed"
	// ContainerReasonSupervisorFailed means the supervisor failed outside any
	// repo or stage.
	ContainerReasonSupervisorFailed = "SupervisorFailed"
	// ContainerReasonSupervisorTerminated means the task stopped and the
	// supervisor recorded no exit, so it was killed.
	ContainerReasonSupervisorTerminated = "SupervisorTerminated"
	// ContainerReasonWorkloadFailed means the attached terminal's workload, or a
	// non-attachable container, exited non-zero.
	ContainerReasonWorkloadFailed = "WorkloadFailed"
	// ContainerReasonSupervisorStatusUnavailable means an attachable container
	// exited non-zero and its supervisor document is missing, of an
	// unsupported version, or invalid.
	ContainerReasonSupervisorStatusUnavailable = "SupervisorStatusUnavailable"
)

// DeepCopy returns a copy of s that shares no memory with it.
func (s *SupervisorStatus) DeepCopy() *SupervisorStatus {
	if s == nil {
		return nil
	}
	out := *s
	if s.Exit != nil {
		exit := *s.Exit
		out.Exit = &exit
	}
	if s.Terminals != nil {
		out.Terminals = make([]SupervisorTerminalStatus, len(s.Terminals))
		for i, t := range s.Terminals {
			if t.Attachers != nil {
				t.Attachers = append([]string(nil), t.Attachers...)
			}
			if t.Exit != nil {
				exit := *t.Exit
				t.Exit = &exit
			}
			out.Terminals[i] = t
		}
	}
	return &out
}
