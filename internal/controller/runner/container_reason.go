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
	"errors"
	"fmt"
	"strconv"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// deriveContainerReason returns the Reason and Message of a container that
// ended on its own with a non-zero exit (issue #1711). The supervisor reports
// facts; the daemon derives the reason from them:
//
//  1. the task stopped and the supervisor recorded no exit → SupervisorTerminated;
//  2. it exited in phase Repos with a failed repo → RepoFailed;
//  3. it exited in phase Stages with a failed stage → StageFailed;
//  4. it recorded an error outside any repo or stage → SupervisorFailed;
//  5. otherwise, the workload exited non-zero → WorkloadFailed;
//  6. an attachable container with no supervisor status at all →
//     SupervisorStatusUnavailable, naming why (missing, unsupported, invalid);
//  7. a non-attachable container → WorkloadFailed.
//
// Only a task that ended on its own (State Error or Failed) gets a reason;
// nothing is derived while the container runs or after a clean exit. Once the
// task record is deleted (State Stopped: wind-down, teardown, an operator
// stop, a reboot) populateCellContainerStatuses keeps the previous pass's
// reason instead, so a container stopped while it was running gets none.
// status.Supervisor must already hold what was read (or carried forward) for
// this pass; docResult is the outcome of this pass's read.
func deriveContainerReason(
	status intmodel.ContainerStatus, attachable bool, docResult supervisorDocResult,
) (string, string) {
	if status.State != intmodel.ContainerStateError && status.State != intmodel.ContainerStateFailed {
		return "", ""
	}
	if status.ExitCode == 0 {
		return "", ""
	}
	if !attachable {
		return v1beta1.ContainerReasonWorkloadFailed, exitDescription(status)
	}
	sup := status.Supervisor
	if sup == nil {
		return v1beta1.ContainerReasonSupervisorStatusUnavailable,
			supervisorUnavailableMessage(docResult, status)
	}
	if sup.Exit == nil {
		return v1beta1.ContainerReasonSupervisorTerminated, truncateMessage(fmt.Sprintf(
			"supervisor terminated in phase %s: %s", sup.Phase, exitDescription(status)))
	}
	switch sup.Phase {
	case v1beta1.SupervisorPhaseRepos:
		if repo, ok := failedRepo(status.Repos, sup.Current); ok {
			return v1beta1.ContainerReasonRepoFailed, truncateMessage(fmt.Sprintf(
				"required repo %q failed: %s", repo.Name, repo.Error))
		}
	case v1beta1.SupervisorPhaseStages:
		if stage, ok := failedStage(status.Stages, sup.Current); ok {
			return v1beta1.ContainerReasonStageFailed, truncateMessage(fmt.Sprintf(
				"create stage %d failed: %s", stage.Index, stage.Error))
		}
	case v1beta1.SupervisorPhaseConfig, v1beta1.SupervisorPhaseRunning, v1beta1.SupervisorPhaseExited:
	}
	if sup.Error != "" {
		return v1beta1.ContainerReasonSupervisorFailed, truncateMessage(fmt.Sprintf(
			"supervisor failed in phase %s: %s", sup.Phase, sup.Error))
	}
	return v1beta1.ContainerReasonWorkloadFailed, exitDescription(status)
}

// failedRepo returns the failed repo named current, or the first failed one.
func failedRepo(repos []intmodel.RepoStatus, current string) (intmodel.RepoStatus, bool) {
	var first *intmodel.RepoStatus
	for i := range repos {
		if repos[i].State != v1beta1.RepoStateFailed {
			continue
		}
		if repos[i].Name == current {
			return repos[i], true
		}
		if first == nil {
			first = &repos[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return intmodel.RepoStatus{}, false
}

// failedStage returns the failed stage whose index is current, or the first
// failed one.
func failedStage(stages []intmodel.StageStatus, current string) (intmodel.StageStatus, bool) {
	idx, err := strconv.Atoi(current)
	if err != nil {
		idx = -1
	}
	var first *intmodel.StageStatus
	for i := range stages {
		if stages[i].State != v1beta1.StageStateFailed {
			continue
		}
		if stages[i].Index == idx {
			return stages[i], true
		}
		if first == nil {
			first = &stages[i]
		}
	}
	if first != nil {
		return *first, true
	}
	return intmodel.StageStatus{}, false
}

// exitDescription renders the container's exit code and signal.
func exitDescription(status intmodel.ContainerStatus) string {
	if status.ExitSignal != "" {
		return fmt.Sprintf("terminated by %s (exit code %d)", status.ExitSignal, status.ExitCode)
	}
	return fmt.Sprintf("exited with code %d", status.ExitCode)
}

// supervisorUnavailableMessage says why no supervisor status could be read.
func supervisorUnavailableMessage(docResult supervisorDocResult, status intmodel.ContainerStatus) string {
	why := "no supervisor status document"
	switch docResult.State {
	case supervisorDocUnsupported:
		why = "unsupported supervisor status document (" + docResult.Detail + "); recreate the container"
	case supervisorDocInvalid:
		why = "invalid supervisor status document (" + docResult.Detail + ")"
	case supervisorDocMissing, supervisorDocValid:
	}
	return truncateMessage(why + "; " + exitDescription(status))
}

// truncateMessage bounds a reason message the way failure messages are
// bounded elsewhere (truncateFailureMessage).
func truncateMessage(msg string) string {
	return truncateFailureMessage(errors.New(msg))
}

// supervisorSetupReason reports whether reason names a failure of the
// supervisor's own setup (a repo, a stage, or the supervisor itself) rather
// than of the workload. A cell that fails to start because of one takes it as
// its reason (see markCellFailed).
func supervisorSetupReason(reason string) bool {
	switch reason {
	case v1beta1.ContainerReasonRepoFailed, v1beta1.ContainerReasonStageFailed,
		v1beta1.ContainerReasonSupervisorFailed:
		return true
	}
	return false
}
