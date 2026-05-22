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
	"net"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// containerLogTaskSpec returns a TaskSpec with cio.LogFile IO pointed at the
// per-container log path for a non-Attachable container. Returns the zero
// TaskSpec for Attachable containers (sbsh's capture file already covers
// them) and for Root containers (pause-style — no useful stdout). Bytes flow
// from the runtime shim into the file; `kuke log` later reads from the same
// path. Centralised here so all three StartContainer call sites pick up the
// same policy.
func (r *Exec) containerLogTaskSpec(spec intmodel.ContainerSpec) ctr.TaskSpec {
	if spec.Attachable || spec.Root {
		return ctr.TaskSpec{}
	}
	return ctr.TaskSpec{
		IO: &ctr.TaskIO{
			LogFilePath: fs.ContainerLogPath(
				r.opts.RunPath,
				spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
			),
		},
	}
}

// rootContainerWantsCNI returns true when StartCell should run the CNI attach
// path for the cell's root container. Host-network containers (kukeond and
// any future host-scope cell) have no per-container veth, so attaching them
// to a CNI bridge would either error or — worse — create a host-side bridge
// inside the daemon's own netns, hiding it from real host-scope tooling.
func rootContainerWantsCNI(spec intmodel.ContainerSpec) bool {
	return !spec.HostNetwork
}

// cellWantsHostNetworkRoot reports whether any container in the cell asked
// for HostNetwork. The default-root path uses this to flip the auto-default
// busybox root onto the host's netns, which the non-root containers then
// join via JoinContainerNamespaces — so a single container marked
// HostNetwork=true puts the whole cell on the host's network.
func cellWantsHostNetworkRoot(cell intmodel.Cell) bool {
	for _, c := range cell.Spec.Containers {
		if c.HostNetwork {
			return true
		}
	}
	return false
}

// validateExplicitRootHostNetwork enforces, for cells using explicit
// rootContainerId, that the chosen root has HostNetwork=true whenever any
// peer container has HostNetwork=true. The auto-default-root branch in
// ensureCellRootContainerSpec already propagates host-network onto the
// generated root, but the explicit-root branch reads the user's spec
// verbatim — so a peer with HostNetwork=true alongside an explicit non-host
// root would silently lose its host-network intent (peers join the root's
// netns via JoinContainerNamespaces).
func validateExplicitRootHostNetwork(cell intmodel.Cell, rootSpec intmodel.ContainerSpec) error {
	if cellWantsHostNetworkRoot(cell) && !rootSpec.HostNetwork {
		return fmt.Errorf(
			"%w: rootContainerId %q",
			internalerrdefs.ErrExplicitRootHostNetworkMismatch,
			cell.Spec.RootContainerID,
		)
	}
	return nil
}

// cellTasksAllRunningFn is StartCell's idempotency guard, decoupled from
// any specific containerd client so it can be unit-tested without a live
// runtime. statusFn is called once for the root containerd ID and once
// per non-root container; the cell is considered already running only if
// every call returns containerd.Running. Issue #149: without this guard,
// CreateCell→StartCell on an already-Ready cell tears the running root
// container down and re-runs CNI ADD against its recreated peer, which
// host-local IPAM rejects as a duplicate allocation under the same
// container ID (we never ran CNI DEL on the teardown path).
func cellTasksAllRunningFn(
	cell intmodel.Cell,
	rootContainerdID string,
	statusFn func(id string) (containerd.Status, error),
) bool {
	rootStatus, err := statusFn(rootContainerdID)
	if err != nil || rootStatus.Status != containerd.Running {
		return false
	}
	for _, c := range cell.Spec.Containers {
		if c.Root {
			continue
		}
		cid := strings.TrimSpace(c.ContainerdID)
		if cid == "" {
			return false
		}
		s, statusErr := statusFn(cid)
		if statusErr != nil || s.Status != containerd.Running {
			return false
		}
	}
	return true
}

// teardownRootContainerCNI enforces issue #630's release-before-recreate
// ordering: it runs CNI DEL against the old root container (releaseCNI) *before*
// deleteContainer removes it, then runs the best-effort IPAM-file safety net
// (purgeCNI) *after* the delete. Both StartCell's teardown-before-recreate
// branch and RecreateCell rebuild the root under the *same* deterministic
// containerd ID (naming.BuildRootContainerdID) and re-run CNI ADD against it;
// without releasing the prior reservation first, host-local IPAM rejects the
// re-ADD as a "duplicate allocation". releaseCNI must run while the old
// container's netns is still valid (i.e. before its task is deleted), so it is
// sequenced first; purgeCNI always runs, even when deleteContainer fails, so a
// leaked allocation file is still scrubbed. deleteContainer's error is returned
// for the caller to log (both call sites warn-and-continue on it).
//
// Decoupled from *Exec's concrete containerd/CNI clients — same rationale as
// cellTasksAllRunningFn (issue #149) — so the ordering invariant is
// unit-testable without a live runtime.
func teardownRootContainerCNI(releaseCNI func(), deleteContainer func() error, purgeCNI func()) error {
	releaseCNI()
	delErr := deleteContainer()
	purgeCNI()
	return delErr
}

// purgeStaleRootContainerCNI is the create-fresh-path safety net for the
// root-container CNI re-ADD collision (issue #649). After a clean stop/kill the
// root container is *deleted*, so StartCell's container-exists teardown branch
// (teardownRootContainerCNI) never runs on the next start — but a best-effort
// stop/kill purge that failed (netns already gone, mismatched network name, or
// a crash mid-teardown) can leave a stale /var/lib/cni/networks/<network>/<ip>
// reservation keyed to the deterministic root-container ID. host-local then
// rejects the re-ADD as a duplicate allocation. This scrubs that reservation
// before CNI ADD on the ErrContainerNotFound branch, giving the create-fresh
// path the same second line of defence the teardown branch already has.
//
// Best-effort and a no-op when networkName is "" (resolveRootCNINetworkName
// couldn't derive a name), so a clean start with no stale reservation is
// unaffected. Decoupled from the concrete CNI client — same rationale as
// teardownRootContainerCNI — so the run/no-op decision is unit-testable.
func purgeStaleRootContainerCNI(networkName string, purge func()) {
	if networkName == "" {
		return
	}
	purge()
}

// maxFailureMessageLen caps Status.Message so a long, multi-line wrap from
// `fmt.Errorf("%w: %w", …)` chains doesn't blow up `kuke get cell -o yaml`.
// 256 bytes is comfortably wider than any error sentinel string in the repo
// while still rendering as a single readable line.
const maxFailureMessageLen = 256

// truncateFailureMessage flattens cause's text into the single-line, bounded
// form Status.Message expects: newlines/tabs collapsed to spaces, trimmed of
// runs of whitespace, and capped at maxFailureMessageLen with a trailing
// "..." indicator. Returns "" when cause is nil so callers don't need to
// pre-guard.
func truncateFailureMessage(cause error) string {
	if cause == nil {
		return ""
	}
	msg := cause.Error()
	// Collapse any control-ish whitespace to a single space so the message
	// always renders on one YAML/JSON line.
	msg = strings.ReplaceAll(msg, "\n", " ")
	msg = strings.ReplaceAll(msg, "\r", " ")
	msg = strings.ReplaceAll(msg, "\t", " ")
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > maxFailureMessageLen {
		// Truncate on a valid UTF-8 boundary so a multi-byte rune isn't
		// split. Error strings are dominantly ASCII but registry tags
		// and image refs can carry non-ASCII content.
		msg = strings.ToValidUTF8(msg[:maxFailureMessageLen-3], "") + "..."
	}
	return msg
}

// markCellFailed transitions the cell to the terminal CellStateFailed state
// (issue #407 for the Start*-stage paths; issue #504 for the CreateCell-stage
// path). The cleanup itself is:
//
//   - reload from metadata and stamp State=Failed plus Status.Reason /
//     Status.Message *before* KillCell runs, so the on-disk state is sticky
//     from the start of the cleanup (issue #409). cellStateIsSticky(Failed)
//     short-circuits ReconcileCell, so any reconcile tick that lands during
//     KillCell — including the one tripped by `--rm`'s AutoDelete + a cell
//     that already reached Ready — observes Failed and refuses to reap the
//     cell.
//   - best-effort KillCell on the cell so any container that did start (e.g.,
//     the root container) is torn down — the cell holds no running processes
//     once it enters Failed. KillCell internally writes CellStateStopped at
//     the end of its run, briefly re-exposing the pre-#409 race window;
//   - reload again and re-stamp State=Failed + Reason + Message so the
//     operator-facing breadcrumb survives KillCell's Stopped-write. `kuke get
//     cell -o yaml` then shows the failing stage and the wrapped cause.
//
// The original startup error is returned unmodified by the caller; this helper
// only writes the new state so the reconciler can later observe it as terminal
// (cellStateAutoDeleteTriggers excludes Failed, and ReconcileCell treats
// originalState==Failed as sticky). Errors from the kill/persist sub-steps are
// logged at WARN — propagating them would mask the original startup cause.
//
// reason is a stable PascalCase label (e.g. "CreateCellFailed",
// "StartCellFailed", "StartContainerFailed") so machine consumers can switch
// on it without parsing the human-readable Message.
func (r *Exec) markCellFailed(cell intmodel.Cell, reason string, cause error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	message := truncateFailureMessage(cause)

	// Pre-kill Failed stamp (issue #409). Reload first so we don't overwrite
	// any concurrent updates from earlier in the failure path, then mutate to
	// Failed and persist before invoking KillCell.
	if pre, preErr := r.GetCell(cell); preErr == nil {
		cell = pre
	} else {
		r.logger.DebugContext(r.ctx,
			"failed to reload cell metadata before pre-kill Failed stamp",
			"cell", cellName, "error", preErr.Error())
	}
	cell.Status.State = intmodel.CellStateFailed
	cell.Status.Reason = reason
	cell.Status.Message = message
	if preStampErr := r.UpdateCellMetadata(cell); preStampErr != nil {
		r.logger.WarnContext(r.ctx,
			"failed to pre-stamp Failed state before killing cell",
			"cell", cellName, "cause", cause.Error(), "error", preStampErr.Error())
	}

	if _, killErr := r.killCellLocked(cell); killErr != nil {
		r.logger.WarnContext(r.ctx,
			"failed to kill siblings while transitioning cell to Failed",
			"cell", cellName, "cause", cause.Error(), "error", killErr.Error())
	}

	reloaded, getErr := r.GetCell(cell)
	if getErr == nil {
		cell = reloaded
	} else {
		r.logger.DebugContext(r.ctx,
			"failed to reload cell metadata after KillCell while transitioning to Failed",
			"cell", cellName, "error", getErr.Error())
	}

	cell.Status.State = intmodel.CellStateFailed
	cell.Status.Reason = reason
	cell.Status.Message = message
	if updErr := r.UpdateCellMetadata(cell); updErr != nil {
		r.logger.WarnContext(r.ctx,
			"failed to persist Failed state after cell startup failure",
			"cell", cellName, "cause", cause.Error(), "error", updErr.Error())
		return
	}
	r.logger.InfoContext(r.ctx,
		"cell transitioned to Failed after startup failure",
		"cell", cellName, "reason", reason, "cause", cause.Error())
}

// StartCell starts the root container and all containers defined in the CellDoc.
// The root container is started first, then all containers in doc.Spec.Containers are started.
//
// If a containerd-touching step fails (CreateContainer, StartContainer, CNI
// attach, attachable chown), the cell is transitioned to CellStateFailed and
// any containers that did start are killed — issue #407. Errors raised before
// the provisioning phase (input validation, realm lookup, idempotent-skip
// path) leave the cell's persisted state alone.
func (r *Exec) StartCell(cell intmodel.Cell) (intmodel.Cell, error) {
	defer r.lockCell(cell)()
	return r.startCellLocked(cell)
}

// startCellLocked is the body of StartCell. The caller must hold the per-cell
// lifecycle lock (the StartCell wrapper, or a nesting op such as RecreateCell
// that already holds it). It must not be called without the lock held.
func (r *Exec) startCellLocked(cell intmodel.Cell) (_ intmodel.Cell, retErr error) {
	// provisionStarted gates the defer below: only flip the cell to Failed
	// when we've already entered the destructive recreate path. Validation
	// errors (missing cell name, missing realm) and the idempotent-skip
	// return predate this flip and must not stamp Failed onto a healthy
	// cell. cellForCleanup is captured by the closure and overwritten with
	// the fully-resolved internalCell after GetCell succeeds so KillCell's
	// internal GetCell has the realm/space/stack identifiers it needs.
	var provisionStarted bool
	cellForCleanup := cell
	defer func() {
		if retErr != nil && provisionStarted {
			r.markCellFailed(cellForCleanup, "StartCellFailed", retErr)
		}
	}()
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellNameRequired
	}
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrStackNameRequired
	}

	// Get the cell document to access all containers
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: stackName,
		},
	}
	internalCell, err := r.GetCell(lookupCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrGetCell, err)
	}
	cellForCleanup = internalCell

	cellSpec := internalCell.Spec
	cellID := cellSpec.ID
	if cellID == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellIDRequired
	}

	realmID := cellSpec.RealmName
	spaceID := cellSpec.SpaceName
	stackID := cellSpec.StackName

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmID, spaceID)
	if cniErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values

	if err = r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmID,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return intmodel.Cell{}, fmt.Errorf("realm %q has no namespace", realmID)
	}

	creds := ctr.ConvertRealmCredentials(internalRealm.Spec.RegistryCredentials)

	// Generate containerd ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerdID(spaceID, stackID, cellID)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to build root container containerd ID: %w", err)
	}

	// Idempotency guard (issue #149): if every container's task is already
	// running, the cell is up. Skip the destructive teardown-and-recreate
	// cycle below; otherwise we'd kill the running root, recreate it, and
	// re-run CNI ADD against the same container ID — which host-local IPAM
	// refuses as a duplicate allocation (we never run CNI DEL when we
	// delete the old container, so the reservation persists).
	if cellTasksAllRunningFn(internalCell, containerID, func(id string) (containerd.Status, error) {
		return r.ctrClient.TaskStatus(namespace, id)
	}) {
		markCellReady(&internalCell)
		if err = r.PopulateAndPersistCellContainerStatuses(&internalCell); err != nil {
			r.logger.WarnContext(r.ctx, "failed to populate container statuses after idempotent StartCell skip",
				"cell", cellName,
				"error", err)
			// best-effort, fall through with the Ready state we already set
		}
		skipFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		skipFields = append(skipFields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"cell tasks already running, StartCell is a no-op",
			skipFields...,
		)
		return internalCell, nil
	}

	// Past the idempotent-skip path: any subsequent error means we crashed
	// mid-provisioning. Arm the defer above so the cell flips to Failed and
	// any siblings already started get killed (issue #407).
	provisionStarted = true

	// Check if container exists and clean it up
	container, err := r.ctrClient.GetContainer(namespace, containerID)
	if err != nil {
		// Container doesn't exist, will create fresh
		if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID)
			r.logger.DebugContext(
				r.ctx,
				"root container does not exist, will create fresh",
				fields...,
			)
			// Create-fresh safety net (issue #649): the root container is gone,
			// so the teardown branch below never runs — but a prior stop/kill
			// (or a crash mid-teardown) may have left a stale host-local IPAM
			// reservation keyed to this deterministic root-container ID. Scrub
			// it before CNI ADD so the re-ADD isn't rejected as a duplicate
			// allocation. Best-effort and a no-op when no stale file exists.
			networkName := r.resolveRootCNINetworkName(realmID, spaceID)
			purgeStaleRootContainerCNI(networkName, func() {
				_ = r.purgeCNIForContainer(containerID, "", networkName)
			})
		} else {
			// Other errors are unexpected
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.WarnContext(
				r.ctx,
				"failed to check if root container exists, will attempt to create",
				fields...,
			)
		}
	} else {
		// Container exists: release its CNI/IPAM reservation, then delete it.
		// The root is recreated below under the same deterministic containerd
		// ID and re-attached via CNI ADD; without the release-before-delete
		// ordering here, host-local IPAM rejects that re-ADD as a duplicate
		// allocation (issue #630). releaseCNI must run while the task's netns
		// is still valid, so it is sequenced before the task delete; purgeCNI
		// scrubs the residual allocation file afterward as a safety net.
		networkName := r.resolveRootCNINetworkName(realmID, spaceID)
		_ = teardownRootContainerCNI(
			func() {
				r.detachRootContainerFromNetwork(
					containerID, cniConfigPath, namespace, cellID, cellName, spaceID, realmID,
				)
			},
			func() error {
				// Delete the task (if any) then the container to remove stale spec.
				nsCtx := namespaces.WithNamespace(r.ctx, namespace)
				if task, taskErr := container.Task(nsCtx, nil); taskErr == nil {
					if _, deleteTaskErr := task.Delete(nsCtx, containerd.WithProcessKill); deleteTaskErr != nil {
						fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
						fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", deleteTaskErr))
						r.logger.WarnContext(r.ctx, "failed to delete existing task, continuing", fields...)
					}
				}

				delErr := r.ctrClient.DeleteContainer(namespace, containerID, ctr.ContainerDeleteOptions{
					SnapshotCleanup: true,
				})
				switch {
				case delErr == nil:
					fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
					fields = append(fields, "space", spaceID, "realm", realmID)
					r.logger.InfoContext(r.ctx, "deleted existing root container for recreation", fields...)
				case errors.Is(delErr, internalerrdefs.ErrContainerNotFound):
					// Might have been deleted between check and delete.
					fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
					fields = append(fields, "space", spaceID, "realm", realmID)
					r.logger.DebugContext(r.ctx, "root container already deleted, will create fresh", fields...)
				default:
					fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
					fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", delErr))
					r.logger.WarnContext(r.ctx, "failed to delete existing container, continuing", fields...)
				}
				return delErr
			},
			func() {
				if networkName != "" {
					_ = r.purgeCNIForContainer(containerID, "", networkName)
				}
			},
		)
	}

	// Recreate root container fresh
	rootContainerSpec, err := r.ensureCellRootContainerSpec(internalCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get root container spec: %w", err)
	}

	// CellCgroupPath: runtime-only injection — see setCellCgroupPath docs.
	// The destructive recreate done by StartCell here would otherwise reset
	// the container's OCI Linux.CgroupsPath to the runc-shim default
	// (issue #312).
	setCellCgroupPath(&rootContainerSpec, &internalCell)

	// Per-cell /etc/hostname / initial /etc/hosts — render before the root's
	// OCI spec is built so the bind-mount sources exist when runc consumes
	// them, and stamp source paths onto rootContainerSpec so the OCI spec
	// emits the bind-mount entries (issue #345). The cell-wide stamp below
	// the CNI-attach block reaches non-root containers; the root needs the
	// per-spec stamp because the local rootContainerSpec value is what feeds
	// BuildRootContainerSpec on this fresh-recreate path.
	if etcErr := r.renderCellEtcFilesPreCNI(&internalCell); etcErr != nil {
		return intmodel.Cell{}, fmt.Errorf("render cell etc files: %w", etcErr)
	}
	r.stampContainerRecreateRuntimeFields(&rootContainerSpec, &internalCell)

	rootLabels := buildRootContainerLabels(internalCell)
	ctrContainerSpec := ctr.BuildRootContainerSpec(rootContainerSpec, rootLabels, r.daemonDefaultBuildOpts()...)

	_, err = r.ctrClient.CreateContainer(namespace, ctrContainerSpec, creds)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to create root container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to create root container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	fields = append(fields, "space", spaceID, "realm", realmID)
	r.logger.InfoContext(
		r.ctx,
		"created root container",
		fields...,
	)

	// Start root container
	rootTask, err := r.ctrClient.StartContainer(namespace, ctr.ContainerSpec{ID: containerID}, ctr.TaskSpec{})
	if err != nil {
		fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to start root container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to start root container %s: %w", containerID, err)
	}

	rootPID := rootTask.Pid()
	if rootPID == 0 {
		return intmodel.Cell{}, fmt.Errorf("root container %s has invalid pid (0)", containerID)
	}

	namespacePaths := ctr.NamespacePaths{
		Net: fmt.Sprintf("/proc/%d/ns/net", rootPID),
		IPC: fmt.Sprintf("/proc/%d/ns/ipc", rootPID),
		UTS: fmt.Sprintf("/proc/%d/ns/uts", rootPID),
	}

	// CNI ADD's IPv4 result, if any. Captured here so the post-attach
	// /etc/hosts re-render below can read it after the if/else block. nil
	// for host-network cells (CNI skipped) and on the idempotent-skip path
	// when the libcni cache lookup also fails. Issue #345.
	var cellIP net.IP

	// Host-netns root containers (e.g. kukeond) have no per-container veth to
	// wire up — CNI attach would create a host-side bridge inside the daemon's
	// own netns, exactly the divergence we're avoiding. Skip the whole CNI
	// dance for them.
	if !rootContainerWantsCNI(rootContainerSpec) {
		skipFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		skipFields = append(skipFields, "space", spaceID, "realm", realmID, "pid", rootPID, "hostNetwork", rootContainerSpec.HostNetwork)
		r.logger.InfoContext(
			r.ctx,
			"skipping CNI attach for host-network root container",
			skipFields...,
		)
	} else {
		// Log CNI paths being used for debugging
		// Note: NewManager applies defaults AFTER creating the CNI config,
		// so if cniBinDir is empty, the CNI config will have an empty path array
		cniBinDir := r.cniConf.CniBinDir
		cniConfigDir := r.cniConf.CniConfigDir
		cniCacheDir := r.cniConf.CniCacheDir
		debugFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		debugFields = append(
			debugFields,
			"space",
			spaceID,
			"realm",
			realmID,
			"stack",
			stackID,
			"cniBinDir",
			cniBinDir,
			"cniConfigDir",
			cniConfigDir,
			"cniCacheDir",
			cniCacheDir,
		)
		if cniBinDir == "" {
			debugFields = append(debugFields, "cniBinDirDefault", "/opt/cni/bin")
		}
		if cniConfigDir == "" {
			debugFields = append(debugFields, "cniConfigDirDefault", "/opt/cni/net.d")
		}
		if cniCacheDir == "" {
			debugFields = append(debugFields, "cniCacheDirDefault", "/opt/cni/cache")
		}
		r.logger.DebugContext(
			r.ctx,
			"creating CNI manager",
			debugFields...,
		)

		cniMgr, mgrErr := cni.NewManager(
			r.cniConf.CniBinDir,
			r.cniConf.CniConfigDir,
			r.cniConf.CniCacheDir,
		)
		if mgrErr != nil {
			return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrInitCniManager, mgrErr)
		}

		if loadErr := cniMgr.LoadNetworkConfigList(cniConfigPath); loadErr != nil {
			fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"cniConfig",
				cniConfigPath,
				"err",
				fmt.Sprintf("%v", loadErr),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to load CNI config",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to load CNI config %s: %w", cniConfigPath, loadErr)
		}

		netnsPath := namespacePaths.Net
		var addErr error
		cellIP, addErr = cniMgr.AddContainerToNetwork(r.ctx, containerID, netnsPath)
		if addErr != nil {
			// The bridge plugin's "container veth name … already exists" is
			// the one genuinely idempotent failure — a prior ADD reached veth
			// setup before crashing, so eth0 and its IPAM record are intact
			// and the retry can proceed. Match it via the typed sentinel so
			// unrelated "already exists" / "file exists" plugin errors (IP
			// conflicts, route duplicates, IPAM duplicate allocation,
			// iptables) surface instead of being silently swallowed.
			if errors.Is(addErr, internalerrdefs.ErrCNIVethExists) {
				fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(
					fields,
					"space",
					spaceID,
					"realm",
					realmID,
					"cniConfig",
					cniConfigPath,
					"netns",
					netnsPath,
					"err",
					addErr.Error(),
				)
				// INFO so the idempotent-skip is visible without
				// --log-level debug; the original error message goes in the
				// "err" field so a real failure misclassified as idempotent
				// is still traceable.
				r.logger.InfoContext(
					r.ctx,
					"root container already attached to network, skipping CNI ADD",
					fields...,
				)
				// Idempotent-skip path: the CNI ADD didn't run, so no fresh
				// result was returned. The IPAM allocation persisted in the
				// libcni cache from the prior successful run — recover it so
				// /etc/hosts can still carry the cell IP. Issue #345.
				if cellIP == nil {
					cellIP = cniMgr.CachedIPv4ForContainer(containerID, netnsPath)
				}
			} else {
				// Log the actual CNI bin dir value being used (may be empty, which causes the error)
				// Note: NewManager creates CNI config with this value BEFORE applying defaults,
				// so if empty, the CNI config will search in an empty path array
				cniBinDirValue := r.cniConf.CniBinDir
				fields = appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(
					fields,
					"space",
					spaceID,
					"realm",
					realmID,
					"cniConfig",
					cniConfigPath,
					"netns",
					netnsPath,
					"cniBinDir",
					cniBinDirValue,
					"err",
					fmt.Sprintf("%v", addErr),
				)
				if cniBinDirValue == "" {
					fields = append(
						fields,
						"cniBinDirNote",
						"empty path - CNI config was created with empty plugin search path, default /opt/cni/bin not applied to CNI config",
					)
				}
				r.logger.ErrorContext(
					r.ctx,
					"failed to attach root container to network",
					fields...,
				)
				return intmodel.Cell{}, fmt.Errorf("failed to attach root container %s to network: %w", containerID, addErr)
			}
		}
	}

	infoFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	infoFields = append(infoFields, "space", spaceID, "realm", realmID, "pid", rootPID, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"started root container",
		infoFields...,
	)

	// Re-render the cell's /etc/hosts now that CNI assigned the cell IP. The
	// container's bind-mount points to this file's inode, so an in-place
	// rewrite (truncate + write) propagates the new content to the running
	// root container without a remount. cellIP is nil for host-network cells
	// or if the IPAM cache was unreadable on the idempotent-skip path; the
	// renderer no-ops in either case. Stamp source paths onto every non-root
	// containerSpec next so the bind-mount entries make it onto their OCI
	// specs as the loop below recreates them. Issue #345.
	if cellIP != nil {
		if etcErr := r.renderCellEtcHostsWithIP(&internalCell, cellIP); etcErr != nil {
			r.logger.WarnContext(r.ctx,
				"failed to re-render cell /etc/hosts with cell IP",
				"cell", cellName, "cellIP", cellIP.String(), "err", etcErr.Error())
			// best-effort — non-fatal: pre-CNI render already produced a
			// valid file with the localhost block, which is enough for
			// non-self-resolution use cases. Surface as a warning so it's
			// visible without breaking StartCell.
		}
	}
	r.stampCellEtcFilePathsOnContainers(&internalCell)
	stampCellProfileNameOnContainers(&internalCell)
	cellSpec = internalCell.Spec

	// Start all containers defined in the CellDoc
	for _, containerSpec := range cellSpec.Containers {
		// Skip root container - it's already created and started above
		if containerSpec.Root {
			continue
		}

		// Use ContainerdID from spec
		ctrContainerID := containerSpec.ContainerdID
		if ctrContainerID == "" {
			return intmodel.Cell{}, fmt.Errorf("container %q has empty ContainerdID", containerSpec.ID)
		}

		// Log which container we're attempting to start
		startFields := appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		startFields = append(startFields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.DebugContext(
			r.ctx,
			"attempting to start container from CellDoc",
			startFields...,
		)

		// Delete container if it exists (idempotent - DeleteContainer handles non-existent containers gracefully)
		// This ensures any stale container specs and tasks are cleaned up before recreation
		err = r.ctrClient.DeleteContainer(namespace, ctrContainerID, ctr.ContainerDeleteOptions{
			SnapshotCleanup: true,
		})
		if err != nil {
			// Log warning but continue - DeleteContainer is idempotent, so errors here are unexpected
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"containerName",
				containerSpec.ID,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.WarnContext(
				r.ctx,
				"failed to delete existing container, continuing with recreation",
				fields...,
			)
		}

		// CellCgroupPath: runtime-only injection — see the comment at the
		// root-container recreate above. Issue #312.
		setCellCgroupPath(&containerSpec, &internalCell)

		// NestedCgroupRuntime: runtime-only injection — same shape as
		// CellCgroupPath. The per-container field is not persisted (see
		// modelhub.ContainerSpec.NestedCgroupRuntime contract), so the
		// reload-via-GetCell that StartCell does above strips the value
		// that applyCellContainerDefaults set at create time. Repopulate
		// from cell.Spec.NestedCgroupRuntime here, otherwise
		// BuildContainerSpec skips the cgroup2 mount on the destructive
		// recreate and #322 re-emerges for non-root containers after a
		// daemon restart.
		containerSpec.NestedCgroupRuntime = internalCell.Spec.NestedCgroupRuntime

		// Recreate container fresh
		attachOpts, attachErr := r.attachableBuildOpts(namespace, containerSpec, creds)
		if attachErr != nil {
			return intmodel.Cell{}, fmt.Errorf("failed to prepare attachable container %s: %w", ctrContainerID, attachErr)
		}
		buildOpts := append(r.daemonDefaultBuildOpts(), attachOpts...)
		_, err = r.ctrClient.CreateContainerFromSpec(namespace, containerSpec, creds, buildOpts...)
		if err != nil {
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceID,
				"realm",
				realmID,
				"containerName",
				containerSpec.ID,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to create container",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to create container %s: %w", ctrContainerID, err)
		}

		fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID, "containerName", containerSpec.ID)
		r.logger.InfoContext(
			r.ctx,
			"created container",
			fields...,
		)

		if err = r.attachablePostCreateChown(namespace, containerSpec); err != nil {
			return intmodel.Cell{}, fmt.Errorf(
				"failed to chown attachable tty dir for %s: %w", ctrContainerID, err,
			)
		}

		// Use container name with UUID for containerd operations
		specWithNamespaces := ctr.JoinContainerNamespaces(
			ctr.ContainerSpec{ID: ctrContainerID},
			namespacePaths,
		)

		_, err = r.ctrClient.StartContainer(namespace, specWithNamespaces, r.containerLogTaskSpec(containerSpec))
		if err != nil {
			fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
			fields = append(fields, "space", spaceID, "realm", realmID, "err", fmt.Sprintf("%v", err))
			r.logger.ErrorContext(
				r.ctx,
				"failed to start container from CellDoc",
				fields...,
			)
			return intmodel.Cell{}, fmt.Errorf("failed to start container %s: %w", ctrContainerID, err)
		}

		fields = appendCellLogFields([]any{"id", ctrContainerID}, cellID, cellName)
		fields = append(fields, "space", spaceID, "realm", realmID)
		r.logger.InfoContext(
			r.ctx,
			"started container",
			fields...,
		)
	}

	markCellReady(&internalCell)

	// Populate container statuses after starting cell and persist them
	if err = r.PopulateAndPersistCellContainerStatuses(&internalCell); err != nil {
		r.logger.WarnContext(r.ctx, "failed to populate container statuses",
			"cell", cellName,
			"error", err)
		// Continue anyway - status population is best-effort
	}

	return internalCell, nil
}

// StartContainer starts a specific container in a cell. Mirrors StartCell's
// issue-#407 behavior: a containerd-touching failure (CreateContainer,
// StartContainer, attachable chown) marks the cell as CellStateFailed and
// kills any siblings the cell still holds, so a single non-root container
// that cannot be brought up no longer leaves the cell wedged in Unknown.
func (r *Exec) StartContainer(cell intmodel.Cell, containerID string) (_ intmodel.Cell, retErr error) {
	var provisionStarted bool
	cellForCleanup := cell
	defer func() {
		if retErr != nil && provisionStarted {
			r.markCellFailed(cellForCleanup, "StartContainerFailed", retErr)
		}
	}()
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return intmodel.Cell{}, errors.New("container ID is required")
	}

	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellNameRequired
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return intmodel.Cell{}, internalerrdefs.ErrCellIDRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, internalerrdefs.ErrSpaceNameRequired
	}

	if err := r.ensureClientConnected(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", internalerrdefs.ErrConnectContainerd, err)
	}

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return intmodel.Cell{}, fmt.Errorf("realm %q has no namespace", realmName)
	}

	creds := ctr.ConvertRealmCredentials(internalRealm.Spec.RegistryCredentials)

	// Find container in cell spec by ID (base name)
	var foundContainerSpec *intmodel.ContainerSpec
	for i := range cell.Spec.Containers {
		if cell.Spec.Containers[i].ID == containerID {
			foundContainerSpec = &cell.Spec.Containers[i]
			break
		}
	}

	if foundContainerSpec == nil {
		return intmodel.Cell{}, fmt.Errorf("container %q not found in cell %q", containerID, cellName)
	}

	// Root container cannot be started directly - it must be started by starting the cell
	if foundContainerSpec.Root {
		return intmodel.Cell{}, fmt.Errorf(
			"root container cannot be started directly, start the cell instead using 'kuke start cell %s'",
			cellName,
		)
	}

	// Use ContainerdID from spec
	containerdID := foundContainerSpec.ContainerdID
	if containerdID == "" {
		return intmodel.Cell{}, fmt.Errorf("container %q has empty ContainerdID", containerID)
	}

	// Get root container to get namespace paths
	rootContainerID, err := r.getRootContainerContainerdID(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Get root container's namespace paths
	rootContainer, err := r.ctrClient.GetContainer(namespace, rootContainerID)
	if err != nil {
		if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
			return intmodel.Cell{}, fmt.Errorf(
				"root container %q does not exist, start the cell first using 'kuke start cell %s': %w",
				rootContainerID,
				cellName,
				err,
			)
		}
		return intmodel.Cell{}, fmt.Errorf("failed to get root container: %w", err)
	}

	nsCtx := namespaces.WithNamespace(r.ctx, namespace)
	rootTask, err := rootContainer.Task(nsCtx, nil)
	if err != nil {
		// Check if task doesn't exist
		if errdefs.IsNotFound(err) {
			return intmodel.Cell{}, fmt.Errorf(
				"root container %q exists but has no task, start the cell first using 'kuke start cell %s': %w",
				rootContainerID,
				cellName,
				err,
			)
		}
		return intmodel.Cell{}, fmt.Errorf("root container task not found, ensure root container is started: %w", err)
	}

	rootPID := rootTask.Pid()
	if rootPID == 0 {
		return intmodel.Cell{}, errors.New("root container has invalid pid (0)")
	}

	namespacePaths := ctr.NamespacePaths{
		Net: fmt.Sprintf("/proc/%d/ns/net", rootPID),
		IPC: fmt.Sprintf("/proc/%d/ns/ipc", rootPID),
		UTS: fmt.Sprintf("/proc/%d/ns/uts", rootPID),
	}

	// Delete container if it exists (idempotent - DeleteContainer handles non-existent containers gracefully)
	// This ensures any stale container specs and tasks are cleaned up before recreation
	err = r.ctrClient.DeleteContainer(namespace, containerdID, ctr.ContainerDeleteOptions{
		SnapshotCleanup: true,
	})
	if err != nil {
		// Log warning but continue - DeleteContainer is idempotent, so errors here are unexpected
		fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.WarnContext(
			r.ctx,
			"failed to delete existing container, continuing with recreation",
			fields...,
		)
	}

	// CellCgroupPath: runtime-only injection — fill from cell.Status.CgroupPath
	// so the recreated container task lands under <cell>/<id>. Issue #312.
	setCellCgroupPath(foundContainerSpec, &cell)

	// NestedCgroupRuntime: runtime-only injection — see the matching block
	// in StartCell. The per-container field is not persisted, so a cell
	// loaded post-restart drops the value that applyCellContainerDefaults
	// set at create time; repopulate from cell.Spec.NestedCgroupRuntime
	// before BuildContainerSpec runs in the destructive recreate below.
	foundContainerSpec.NestedCgroupRuntime = cell.Spec.NestedCgroupRuntime

	// EtcHostsPath / EtcHostnamePath and CellProfileName: runtime-only
	// stamps the cell-wide StartCell / ensureCellContainers paths apply via
	// stampCellEtcFilePathsOnContainers + stampCellProfileNameOnContainers.
	// The single-container StartContainer recreate path holds a local spec
	// pointer instead, so use the per-spec variants to match. Without these,
	// the recreated container drops its /etc/hosts + /etc/hostname
	// bind-mounts and the KUKEON_CELL_PROFILE_NAME env var. Issue #354.
	r.stampContainerRecreateRuntimeFields(foundContainerSpec, &cell)

	// Past the idempotent recreate-prep: any subsequent error means we
	// crashed mid-provisioning, so arm the defer to flip the cell to
	// Failed (issue #407).
	provisionStarted = true

	// Recreate container fresh
	attachOpts, attachErr := r.attachableBuildOpts(namespace, *foundContainerSpec, creds)
	if attachErr != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to prepare attachable container %s: %w", containerID, attachErr)
	}
	buildOpts := append(r.daemonDefaultBuildOpts(), attachOpts...)
	_, err = r.ctrClient.CreateContainerFromSpec(namespace, *foundContainerSpec, creds, buildOpts...)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to create container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to create container %s: %w", containerID, err)
	}

	fields := appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"created container",
		fields...,
	)

	if err = r.attachablePostCreateChown(namespace, *foundContainerSpec); err != nil {
		return intmodel.Cell{}, fmt.Errorf(
			"failed to chown attachable tty dir for %s: %w", containerdID, err,
		)
	}

	// Start container with namespace paths
	specWithNamespaces := ctr.JoinContainerNamespaces(
		ctr.ContainerSpec{ID: containerdID},
		namespacePaths,
	)

	_, err = r.ctrClient.StartContainer(namespace, specWithNamespaces, r.containerLogTaskSpec(*foundContainerSpec))
	if err != nil {
		fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
		fields = append(
			fields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerID,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to start container",
			fields...,
		)
		return intmodel.Cell{}, fmt.Errorf("failed to start container %s: %w", containerID, err)
	}

	fields = appendCellLogFields([]any{"id", containerdID}, cellID, cellName)
	fields = append(fields, "space", spaceName, "realm", realmName, "containerName", containerID)
	r.logger.InfoContext(
		r.ctx,
		"started container",
		fields...,
	)

	// Get the cell again to ensure we have the latest state
	lookupCell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: cellName,
		},
		Spec: intmodel.CellSpec{
			RealmName: realmName,
			SpaceName: spaceName,
			StackName: cell.Spec.StackName,
		},
	}
	updatedCell, err := r.GetCell(lookupCell)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("failed to retrieve cell after starting container: %w", err)
	}

	markCellReady(&updatedCell)

	// Populate container statuses after starting cell and persist them
	if err = r.PopulateAndPersistCellContainerStatuses(&updatedCell); err != nil {
		r.logger.WarnContext(r.ctx, "failed to populate container statuses",
			"cell", cellName,
			"error", err)
		// Continue anyway - status population is best-effort
	}

	return updatedCell, nil
}
