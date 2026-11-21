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
	"strings"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// cellTeardownMode selects how teardownCellLocked ends each container task.
// The two modes differ only in the first signal sent; both wait for the task
// to exit and delete it, so stop and kill leave the same end state (#1557).
type cellTeardownMode int

const (
	// teardownGraceful sends SIGTERM, then SIGKILL once cellTeardownTimeout
	// expires. Used by `kuke stop` and the reconciler's wind-down.
	teardownGraceful cellTeardownMode = iota
	// teardownKill sends SIGKILL straight away and still waits (bounded by
	// cellTeardownTimeout) for the task to exit before deleting it. Used by
	// `kuke kill`, auto-delete, start-failure cleanup and root-death teardown.
	teardownKill
)

// signalSIGKILL is the ctr.StopContainerOptions.Signal value for SIGKILL.
const signalSIGKILL = "SIGKILL"

// cellTeardownTimeout bounds how long teardownCellLocked waits for each task
// to exit after its first signal.
const cellTeardownTimeout = 5 * time.Second

// stopOptions maps the mode onto the ctr.StopContainer options.
func (m cellTeardownMode) stopOptions() ctr.StopContainerOptions {
	timeout := cellTeardownTimeout
	opts := ctr.StopContainerOptions{Force: true, Timeout: &timeout}
	if m == teardownKill {
		opts.Signal = signalSIGKILL
	}
	return opts
}

// verb is the past-tense verb used in log lines for this mode.
func (m cellTeardownMode) verb() string {
	if m == teardownKill {
		return "killed"
	}
	return "stopped"
}

// teardownCellLocked ends every task in the cell and releases its network,
// leaving no containerd task behind (#1557). The caller must hold the per-cell
// lifecycle lock.
//
// Order: workloads first, then the CNI detach (it needs the live root's
// netns), then the root, then purgeCNIForContainer (releases the IPAM lease
// even when the root's netns is already gone), then the attach-socket
// cleanup. A task that had already exited is deleted too, and every exit the
// stop observes is written into cell.Status.Containers before the final
// populate, so a container whose task is gone still carries its last exit
// code (populateCellContainerStatuses preserves the prior exit code when it
// finds no task).
//
// The core works on the cell it is handed: it does not re-read metadata, never
// writes Status.State, Reason or Message, and never persists. Callers decide
// the label and persist once. Container records and snapshots stay in place
// (#867): delete owns their teardown.
func (r *Exec) teardownCellLocked(cell intmodel.Cell, mode cellTeardownMode) (intmodel.Cell, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		cellID = cellName
	}

	cniConfigPath, cniErr := r.ResolveSpaceCNIConfigPath(realmName, spaceName)
	if cniErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}

	internalRealm, err := r.GetRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: realmName},
	})
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}
	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return intmodel.Cell{}, fmt.Errorf("realm %q has no namespace", realmName)
	}

	logFields := func(id string, extra ...any) []any {
		fields := appendCellLogFields([]any{"id", id}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName)
		return append(fields, extra...)
	}

	// Workload containers first.
	for _, containerSpec := range cell.Spec.Containers {
		if containerSpec.Root {
			continue
		}
		if containerSpec.ContainerdID == "" {
			r.logger.WarnContext(r.ctx, "container has empty ContainerdID, skipping",
				"container", containerSpec.ID)
			continue
		}
		r.teardownContainerTask(&cell, namespace, containerSpec.ID, containerSpec.ContainerdID, mode,
			logFields(containerSpec.ContainerdID))
	}

	// Root container last.
	rootContainerID, err := r.getRootContainerContainerdID(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Resolve the network name deterministically from realm+space so the
	// post-stop CNI/IPAM purge below runs even when space metadata is gone or
	// corrupt (issue #685). The name is a pure function of (realm, space).
	networkName := r.buildRootCNINetworkName(realmName, spaceName)

	// Detach the root from the CNI network before ending its task (the DEL
	// needs the live netns). Skip it for a host-network root: that cell shares
	// the daemon's own netns (no per-veth, no IPAM lease — CNI ADD was skipped
	// by rootContainerWantsCNI), so issuing the conflist DEL there would delete
	// eth0 / down lo in the runner's netns (issue #1219).
	if rootContainerHostNetwork(cell) {
		r.logger.InfoContext(r.ctx, "skipping CNI detach for host-network root cell",
			logFields(rootContainerID)...)
	} else {
		r.detachRootContainerFromNetwork(rootContainerID, cniConfigPath, namespace,
			cellID, cellName, spaceName, realmName)
	}

	r.teardownContainerTask(&cell, namespace, cell.Spec.RootContainerID, rootContainerID, mode,
		logFields(rootContainerID))

	// Always run the comprehensive CNI cleanup after the root task is gone as a
	// safety net: it releases the IPAM reservation whether or not the DEL
	// above succeeded. Without it the next StartCell's CNI ADD would be
	// rejected as a duplicate allocation. The netns path is empty because the
	// root task is gone.
	if networkName != "" {
		if purgeErr := r.purgeCNIForContainer(rootContainerID, "", networkName); purgeErr != nil {
			r.logger.WarnContext(r.ctx, "final CNI cleanup had errors, but continuing",
				logFields(rootContainerID, "network", networkName, "err", fmt.Sprintf("%v", purgeErr))...)
		} else {
			r.logger.DebugContext(r.ctx, "completed final CNI cleanup after container stop",
				logFields(rootContainerID, "network", networkName)...)
		}
	} else {
		r.logger.WarnContext(r.ctx, "cannot perform final CNI cleanup: network name not resolved",
			logFields(rootContainerID)...)
	}

	r.removeCellAttachSockets(cell, cellName)

	// Refresh container statuses so the caller persists the post-teardown
	// view. Best-effort, like the rest of the teardown.
	if popErr := r.populateCellContainerStatuses(&cell); popErr != nil {
		r.logger.WarnContext(r.ctx, "failed to populate container statuses",
			"cell", cellName, "error", popErr)
	}
	return cell, nil
}

// removeCellAttachSockets unlinks each Attachable container's host-side socket
// artifacts (the SUN_PATH-safe symlink under <RunPath>/s/ and the deep socket
// inode kuketty bound). Defense-in-depth for #852: without this the next
// `kuke attach` that slips past the cell-state liveness guard would dial a
// now-orphan inode and surface `connection refused`. Removing here means the
// next connect(2) sees ENOENT instead. Best-effort.
func (r *Exec) removeCellAttachSockets(cell intmodel.Cell, cellName string) {
	for _, containerSpec := range cell.Spec.Containers {
		if symlinkErr := removeAttachableSocketRuntimeArtifacts(r.opts.RunPath, containerSpec); symlinkErr != nil {
			r.logger.WarnContext(r.ctx, "failed to remove attach socket runtime artifacts",
				"container", containerSpec.ID,
				"cell", cellName,
				"error", symlinkErr)
		}
	}
}

// teardownContainerTask ends one container's task through ctr.StopContainer
// and records the exit it reports on cell. A missing task or container is
// nothing to do; any other error is logged and the teardown continues with the
// next container.
func (r *Exec) teardownContainerTask(
	cell *intmodel.Cell,
	namespace, containerID, containerdID string,
	mode cellTeardownMode,
	fields []any,
) {
	exit, err := r.ctrClient.StopContainer(namespace, containerdID, mode.stopOptions())
	switch {
	case err == nil:
		recordContainerExit(cell, containerID, exit)
		r.logger.InfoContext(r.ctx, mode.verb()+" container", fields...)
	case errors.Is(err, errdefs.ErrTaskNotFound), errors.Is(err, errdefs.ErrContainerNotFound):
		r.logger.DebugContext(r.ctx, "no task to end for container", append(fields, "err", err.Error())...)
	default:
		r.logger.WarnContext(r.ctx, "failed to end container task, continuing",
			append(fields, "err", fmt.Sprintf("%v", err))...)
	}
}

// recordContainerExit writes the exit a stop observed into the container's
// entry in cell.Status.Containers. The task is deleted right after, and
// populateCellContainerStatuses then finds no task and keeps the previous exit
// code, so this is what makes a killed container read Stopped with exit 137
// rather than a blank exit. An exit status without an exit time carries no
// observation and is ignored.
func recordContainerExit(cell *intmodel.Cell, containerID string, es *containerd.ExitStatus) {
	if es == nil || es.ExitTime().IsZero() || containerID == "" {
		return
	}
	code := int(es.ExitCode())
	for i := range cell.Status.Containers {
		if cell.Status.Containers[i].ID != containerID {
			continue
		}
		cell.Status.Containers[i].ExitCode = code
		cell.Status.Containers[i].FinishTime = es.ExitTime()
		cell.Status.Containers[i].ExitSignal = exitSignalName(code)
		return
	}
	cell.Status.Containers = append(cell.Status.Containers, intmodel.ContainerStatus{
		ID:         containerID,
		Name:       containerID,
		ExitCode:   code,
		FinishTime: es.ExitTime(),
		ExitSignal: exitSignalName(code),
	})
}

// operatorTeardownLocked is the shared body of stopCellLocked and
// killCellLocked. It re-reads the cell (GetCell returns the persisted label
// plus fresh container statuses), decides the label from that view before
// anything is torn down, runs the teardown core, applies the label and
// persists once; a failed persist is returned. The caller must hold the
// per-cell lifecycle lock.
func (r *Exec) operatorTeardownLocked(cell intmodel.Cell, mode cellTeardownMode) (intmodel.Cell, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, errdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
	}

	internalCell, err := r.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
	})
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Decide the label from the pre-teardown view. Keeping GetCell's fresh
	// container statuses on the same value handed to the core is also what
	// preserves the exit code of a workload that exited before this verb ran.
	label := classifyOperatorStop(internalCell)

	torn, err := r.teardownCellLocked(internalCell, mode)
	if err != nil {
		return intmodel.Cell{}, err
	}
	applyOperatorStopLabel(&torn, label)

	// This is the only persist of the verb's outcome: the controller no
	// longer writes the cell a second time after the runner returns (issue
	// #1569), so a failed write must surface instead of being logged away.
	if err = r.UpdateCellMetadata(torn); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}
	return torn, nil
}
