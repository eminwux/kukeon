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

package ctr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// StartContainer creates and starts a task for the container.
func (c *client) StartContainer(
	namespace string,
	containerSpec ContainerSpec,
	taskSpec TaskSpec,
) (containerd.Task, error) {
	if containerSpec.ID == "" {
		return nil, internalerrdefs.ErrEmptyContainerID
	}

	container, err := c.loadContainer(namespace, containerSpec.ID)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(namespace)

	if len(containerSpec.SpecOpts) > 0 {
		if err = c.applySpecOpts(namespace, container, containerSpec.SpecOpts); err != nil {
			return nil, err
		}
	}

	// Check if task already exists
	existingTask, err := container.Task(nsCtx, nil)
	if err == nil {
		// Task exists, check if it's running
		var status containerd.Status
		status, err = existingTask.Status(nsCtx)
		if err == nil && status.Status == containerd.Running {
			c.logger.WarnContext(c.ctx, "task already running", "id", containerSpec.ID)
			c.storeTask(namespace, containerSpec.ID, existingTask)
			return existingTask, nil
		}
		// Task exists but is not running (stopped), delete it before creating a new one
		c.logger.DebugContext(c.ctx, "deleting stopped task", "id", containerSpec.ID)
		_, err = existingTask.Delete(nsCtx, containerd.WithProcessKill)
		if err != nil {
			c.logger.WarnContext(
				c.ctx,
				"failed to delete stopped task",
				"id",
				containerSpec.ID,
				"err",
				formatError(err),
			)
		}
		c.dropTask(namespace, containerSpec.ID)
	}

	// Build IO creator. Selection order:
	//   1. LogFilePath  — cio.LogFile, shim appends stdout+stderr to a host
	//      file kuke can tail (used for non-Attachable containers including
	//      kukeond — see internal/util/fs/metadata.go ContainerLogPath).
	//   2. Terminal     — TTY-attached IO with no streams wired (sbsh later
	//      claims stdio inside the container).
	//   3. IO non-nil   — bare IO creator with no streams wired.
	//   4. default      — cio.NullIO (output discarded).
	var ioCreator cio.Creator
	switch {
	case taskSpec.IO != nil && taskSpec.IO.LogFilePath != "":
		// The shim opens the path on first task write; create the parent
		// dir up front so that creation fails loudly here rather than
		// silently inside the shim.
		if err = os.MkdirAll(filepath.Dir(taskSpec.IO.LogFilePath), 0o750); err != nil {
			return nil, fmt.Errorf("create container log dir: %w", err)
		}
		ioCreator = cio.LogFile(taskSpec.IO.LogFilePath)
	case taskSpec.IO != nil && taskSpec.IO.Terminal:
		ioCreator = cio.NewCreator(cio.WithStreams(nil, nil, nil), cio.WithTerminal)
	case taskSpec.IO != nil:
		ioCreator = cio.NewCreator(cio.WithStreams(nil, nil, nil))
	default:
		ioCreator = cio.NullIO
	}

	// Build task options
	taskOpts := taskSpec.Options

	// Create new task
	task, err := container.NewTask(nsCtx, ioCreator, taskOpts...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to create task", "id", containerSpec.ID, "err", formatError(err))
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Start the task
	err = task.Start(nsCtx)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to start task", "id", containerSpec.ID, "err", formatError(err))
		// Clean up task on failure
		_, _ = task.Delete(nsCtx, containerd.WithProcessKill)
		return nil, fmt.Errorf("failed to start task: %w", err)
	}

	// Drain the container's task PID into a _payload leaf cgroup so the
	// container-root cgroup has no internal processes (issue #336 scenario
	// A). The cgroup-namespace pin is sticky to the original placement, so
	// moving the PID into a child after task.Start does not reshape the
	// inside-cell view of /sys/fs/cgroup — but it does empty the cgroup-ns
	// root cgroup.procs, which is what cgroup-v2's no-internal-process rule
	// requires before any inner runtime (kuke init, dockerd, podman, an
	// inner containerd) can widen subtree_control for non-thread-aware
	// controllers like memory or io. Gated on the OCI spec carrying a
	// non-empty Linux.CgroupsPath: cellCgroupsPath sets it whenever the
	// container's CellCgroupPath is non-empty (cell containers, including
	// HostCgroup ones whose CellCgroupPath is filled from cell.Status), and
	// leaves it empty for raw containerd / non-cell containers.
	if relocateErr := c.relocateContainerTaskToLeaf(nsCtx, container); relocateErr != nil {
		c.logger.WarnContext(c.ctx,
			"failed to relocate container task to _payload leaf",
			"id", containerSpec.ID, "namespace", namespace, "err", formatError(relocateErr))
	}

	c.storeTask(namespace, containerSpec.ID, task)
	c.logger.InfoContext(c.ctx, "started container", "id", containerSpec.ID, "namespace", namespace)
	return task, nil
}

// relocateContainerTaskToLeaf moves the just-started container's PIDs into
// a _payload leaf cgroup under its OCI Linux.CgroupsPath. See StartContainer
// for the rationale (issue #336 scenario A). Returns nil when the container
// has no CgroupsPath set — raw containerd / non-cell containers whose
// CellCgroupPath is empty. Cell containers with HostCgroup=true still relocate
// because cellCgroupsPath fills Linux.CgroupsPath from their CellCgroupPath.
func (c *client) relocateContainerTaskToLeaf(nsCtx context.Context, container containerd.Container) error {
	ociSpec, err := container.Spec(nsCtx)
	if err != nil {
		return fmt.Errorf("load container spec: %w", err)
	}
	if ociSpec.Linux == nil || ociSpec.Linux.CgroupsPath == "" {
		return nil
	}
	return c.RelocateProcessesToLeaf(ociSpec.Linux.CgroupsPath, "", "_payload")
}

// StopContainer stops a running container task.
func (c *client) StopContainer(
	namespace string,
	id string,
	opts StopContainerOptions,
) (*containerd.ExitStatus, error) {
	if id == "" {
		return nil, internalerrdefs.ErrEmptyContainerID
	}

	c.logger.DebugContext(c.ctx, "starting to stop container", "id", id, "namespace", namespace)
	task, err := c.loadTask(namespace, id)
	if err != nil {
		c.logger.DebugContext(c.ctx, "failed to load task for container", "id", id, "error", err)
		return nil, err
	}

	nsCtx := c.namespaceCtx(namespace)

	// Check task status
	c.logger.DebugContext(c.ctx, "checking task status", "id", id)
	status, err := task.Status(nsCtx)
	if err != nil {
		c.logger.DebugContext(c.ctx, "failed to get task status", "id", id, "error", err)
		return nil, fmt.Errorf("failed to get task status: %w", err)
	}

	if status.Status != containerd.Running {
		c.logger.WarnContext(c.ctx, "task is not running", "id", id, "status", status.Status)
		return nil, internalerrdefs.ErrTaskNotRunning
	}

	// Determine signal
	signal := syscall.SIGTERM
	if opts.Signal != "" {
		// Parse signal string to syscall.Signal
		// For now, support common signals
		switch opts.Signal {
		case "SIGTERM", "TERM":
			signal = syscall.SIGTERM
		case "SIGKILL", "KILL":
			signal = syscall.SIGKILL
		case "SIGINT", "INT":
			signal = syscall.SIGINT
		case "SIGSTOP", "STOP":
			signal = syscall.SIGSTOP
		default:
			signal = syscall.SIGTERM
		}
	}

	// Send signal to task
	err = task.Kill(nsCtx, signal)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to kill task", "id", id, "signal", signal, "err", formatError(err))
		return nil, fmt.Errorf("failed to kill task: %w", err)
	}

	// Wait for task to exit
	timeout := opts.Timeout
	if timeout == nil {
		defaultTimeout := 10 * time.Second
		timeout = &defaultTimeout
	}

	waitCtx, cancel := context.WithTimeout(nsCtx, *timeout)
	defer cancel()

	exitChan, err := task.Wait(waitCtx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for task: %w", err)
	}

	var exitStatus containerd.ExitStatus
	select {
	case exitStatus = <-exitChan:
		// Task exited
	case <-waitCtx.Done():
		if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
			if opts.Force {
				// Force kill
				c.logger.WarnContext(c.ctx, "timeout exceeded, force killing", "id", id)
				err = task.Kill(nsCtx, syscall.SIGKILL)
				if err != nil {
					return nil, fmt.Errorf("failed to force kill task: %w", err)
				}
				// Wait again after force kill with timeout
				forceKillTimeout := 5 * time.Second
				forceKillCtx, forceKillCancel := context.WithTimeout(nsCtx, forceKillTimeout)
				defer forceKillCancel()
				var exitChan2 <-chan containerd.ExitStatus
				exitChan2, err = task.Wait(forceKillCtx)
				if err != nil {
					return nil, fmt.Errorf("failed to wait for task after force kill: %w", err)
				}
				select {
				case exitStatus = <-exitChan2:
					// Task exited
				case <-forceKillCtx.Done():
					// Still didn't exit after force kill, verify status
					finalStatus, statusErr := task.Status(nsCtx)
					if statusErr == nil && finalStatus.Status == containerd.Running {
						return nil, fmt.Errorf("task %s is still running after force kill", id)
					}
					// Task might have exited between wait and status check
					// Return a default exit status
					exitStatus = containerd.ExitStatus{}
				}
			} else {
				return nil, fmt.Errorf("timeout waiting for task to stop: %w", waitCtx.Err())
			}
		} else {
			return nil, fmt.Errorf("context cancelled: %w", waitCtx.Err())
		}
	}

	c.logger.InfoContext(c.ctx, "stopped container", "id", id, "namespace", namespace, "exit_code", exitStatus.ExitCode())

	// Verify task is actually stopped
	finalStatus, err := task.Status(nsCtx)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to verify task status after stop", "id", id, "err", formatError(err))
		// Continue anyway - we got exit status
	} else if finalStatus.Status == containerd.Running {
		c.logger.WarnContext(c.ctx, "task still running after stop, force killing again", "id", id)
		// Force kill again
		_ = task.Kill(nsCtx, syscall.SIGKILL)
		// Wait a bit and check again
		time.Sleep(100 * time.Millisecond)
		finalStatus, _ = task.Status(nsCtx)
		if finalStatus.Status == containerd.Running {
			return nil, fmt.Errorf("task %s is still running after stop attempt", id)
		}
	}

	// Delete the stopped task - stopped tasks are cleaned up, and StartContainer
	// will create a new task when starting again
	c.logger.DebugContext(c.ctx, "deleting stopped task", "id", id)
	_, err = task.Delete(nsCtx, containerd.WithProcessKill)
	if err != nil {
		c.logger.WarnContext(c.ctx, "failed to delete stopped task", "id", id, "err", formatError(err))
		// Continue anyway - task is stopped even if deletion failed
	} else {
		c.logger.DebugContext(c.ctx, "deleted stopped task", "id", id)
	}
	c.dropTask(namespace, id)

	return &exitStatus, nil
}

// CreateContainer creates a new container with the provided spec.
func (c *client) CreateContainer(namespace string, spec ContainerSpec, creds []RegistryCredentials) (containerd.Container, error) {
	if err := validateContainerSpec(spec); err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(namespace)
	cc := c.conn()

	// Check if container already exists
	_, err := cc.LoadContainer(nsCtx, spec.ID)
	if err == nil {
		c.logger.WarnContext(c.ctx, "container already exists", "id", spec.ID)
		return nil, internalerrdefs.ErrContainerExists
	}

	// Pull the image if needed
	image, err := c.pullImage(namespace, spec.Image, creds)
	if err != nil {
		return nil, err
	}

	// Ensure image is unpacked before creating container
	// Use spec.Snapshotter if provided, otherwise empty string for default snapshotter
	if err = c.ensureImageUnpacked(namespace, image, spec.Snapshotter); err != nil {
		return nil, fmt.Errorf("failed to ensure image is unpacked: %w", err)
	}

	// Determine snapshot key
	snapshotKey := spec.SnapshotKey
	if snapshotKey == "" {
		snapshotKey = spec.ID
	}

	// Build OCI spec options, injecting annotations when needed.
	// Prepend WithImageConfig so the image's Entrypoint/Cmd, env, cwd, and user
	// populate the spec when the caller did not provide an explicit command/args.
	// Caller-supplied SpecOpts (e.g. WithProcessArgs, WithEnv) run after and override.
	//nolint:mnd // Magic number of 2 for the two options we prepend (WithImageConfig and optionally WithAnnotations)
	specOpts := make([]oci.SpecOpts, 0, len(spec.SpecOpts)+2)
	specOpts = append(specOpts, oci.WithImageConfig(image))
	specOpts = append(specOpts, spec.SpecOpts...)
	if spec.CNIConfigPath != "" {
		specOpts = append(specOpts, oci.WithAnnotations(map[string]string{
			cniConfigAnnotation: spec.CNIConfigPath,
		}))
	}

	// Build container options
	opts := []containerd.NewContainerOpts{
		containerd.WithImage(image),
		containerd.WithNewSnapshot(snapshotKey, image),
		containerd.WithNewSpec(specOpts...),
	}

	if spec.Snapshotter != "" {
		opts = append(opts, containerd.WithSnapshotter(spec.Snapshotter))
	}

	if spec.Runtime != nil && spec.Runtime.Name != "" {
		opts = append(opts, containerd.WithRuntime(spec.Runtime.Name, spec.Runtime.Options))
	}

	if spec.Labels != nil {
		opts = append(opts, containerd.WithContainerLabels(spec.Labels))
	}

	container, err := cc.NewContainer(nsCtx, spec.ID, opts...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to create container", "id", spec.ID, "err", formatError(err))
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	c.storeContainer(namespace, spec.ID, container)
	c.logger.InfoContext(c.ctx, "created container", "id", spec.ID, "namespace", namespace, "image", spec.Image)
	return container, nil
}

// GetContainer retrieves a container by ID.
func (c *client) GetContainer(namespace, id string) (containerd.Container, error) {
	if id == "" {
		return nil, internalerrdefs.ErrEmptyContainerID
	}
	return c.loadContainer(namespace, id)
}

// ListContainers lists all containers matching the provided filters.
func (c *client) ListContainers(namespace string, filters ...string) ([]containerd.Container, error) {
	nsCtx := c.namespaceCtx(namespace)

	containers, err := c.conn().Containers(nsCtx, filters...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to list containers", "err", formatError(err))
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Update cache
	for _, container := range containers {
		c.storeContainer(namespace, container.ID(), container)
	}

	c.logger.DebugContext(c.ctx, "listed containers", "namespace", namespace, "count", len(containers))
	return containers, nil
}

// ExistsContainer checks if a container exists.
func (c *client) ExistsContainer(namespace, id string) (bool, error) {
	if id == "" {
		return false, internalerrdefs.ErrEmptyContainerID
	}

	nsCtx := c.namespaceCtx(namespace)
	_, err := c.conn().LoadContainer(nsCtx, id)
	if err != nil {
		// "Not found" is not an error for Exists - it's a valid result (container doesn't exist)
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		// Only real errors (connection failures, permission errors, etc.) should be returned
		return false, err
	}
	return true, nil
}

// DeleteContainer deletes a container.
// It is idempotent - if the container doesn't exist, it returns nil (treating it as already deleted).
// It automatically deletes any associated task before deleting the container.
func (c *client) DeleteContainer(namespace, id string, opts ContainerDeleteOptions) error {
	if id == "" {
		return internalerrdefs.ErrEmptyContainerID
	}

	c.logger.DebugContext(c.ctx, "starting to delete container", "id", id, "namespace", namespace, "snapshot_cleanup", opts.SnapshotCleanup)
	container, err := c.loadContainer(namespace, id)
	if err != nil {
		// Container doesn't exist - treat as idempotent (already deleted)
		if errors.Is(err, internalerrdefs.ErrContainerNotFound) {
			c.logger.DebugContext(c.ctx, "container not found", "id", id, "namespace", namespace)
			return nil
		}
		c.logger.DebugContext(c.ctx, "failed to load container for deletion", "id", id, "namespace", namespace, "error", err)
		return err
	}

	nsCtx := c.namespaceCtx(namespace)

	// Try to get and delete the task if it exists
	c.logger.DebugContext(c.ctx, "checking for task to delete", "id", id)
	task, err := container.Task(nsCtx, nil)
	if err == nil {
		// Task exists, delete it first
		c.logger.DebugContext(c.ctx, "deleting task", "id", id)
		_, err = task.Delete(nsCtx, containerd.WithProcessKill)
		if err != nil {
			c.logger.WarnContext(c.ctx, "failed to delete task", "id", id, "err", formatError(err))
			// Continue with container deletion even if task deletion failed
		} else {
			c.logger.DebugContext(c.ctx, "deleted task", "id", id)
		}
		c.dropTask(namespace, id)
	} else {
		c.logger.DebugContext(c.ctx, "no task found for container", "id", id)
	}

	// Delete container
	c.logger.DebugContext(c.ctx, "deleting container from containerd", "id", id)
	deleteOpts := []containerd.DeleteOpts{}
	if opts.SnapshotCleanup {
		deleteOpts = append(deleteOpts, containerd.WithSnapshotCleanup)
	}

	err = container.Delete(nsCtx, deleteOpts...)
	if err != nil {
		// Check if container was already deleted (race condition)
		if errdefs.IsNotFound(err) {
			c.logger.DebugContext(c.ctx, "container not found", "id", id, "namespace", namespace)
			c.dropContainer(namespace, id)
			return nil
		}
		c.logger.ErrorContext(c.ctx, "failed to delete container", "id", id, "namespace", namespace, "err", formatError(err))
		return fmt.Errorf("failed to delete container: %w", err)
	}

	c.dropContainer(namespace, id)
	c.logger.InfoContext(c.ctx, "deleted container", "id", id, "namespace", namespace)
	return nil
}

// CreateContainerFromSpec converts an internal ContainerSpec to ctr.ContainerSpec and creates the container.
// Variadic opts forward to BuildContainerSpec — used today only by the
// runner when it needs to inject host-side paths for an Attachable spec.
func (c *client) CreateContainerFromSpec(
	namespace string,
	containerSpec intmodel.ContainerSpec,
	creds []RegistryCredentials,
	opts ...BuildOption,
) (containerd.Container, error) {
	if containerSpec.ID == "" {
		return nil, internalerrdefs.ErrEmptyContainerID
	}

	if containerSpec.Image == "" {
		return nil, internalerrdefs.ErrInvalidImage
	}

	if containerSpec.CellName == "" {
		return nil, internalerrdefs.ErrEmptyCellID
	}

	if containerSpec.SpaceName == "" {
		return nil, internalerrdefs.ErrEmptySpaceID
	}

	if containerSpec.RealmName == "" {
		return nil, internalerrdefs.ErrEmptyRealmID
	}

	if containerSpec.StackName == "" {
		return nil, internalerrdefs.ErrEmptyStackID
	}

	cellID := containerSpec.CellName

	// Resolve declared secrets before building the OCI spec. Env entries are
	// appended to containerSpec.Env (containerd stores these in its own
	// runtime spec, not in kukeon metadata); file-mounted secrets are staged
	// on the host at 0400 and added as read-only bind mounts. The daemon's
	// RunPath (forwarded via WithSecretRunPath) lets a secretRef resolve from
	// the referenced scope's secrets tree (issue #623).
	containerdID := containerSpec.ContainerdID
	if containerdID == "" {
		containerdID = containerSpec.ID
	}
	var bo buildOpts
	for _, apply := range opts {
		apply(&bo)
	}
	resolved, err := resolveSecrets(containerdID, containerSpec.Secrets, DefaultSecretsStagingDir, bo.secretRunPath)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to resolve container secrets",
			"id", containerSpec.ID,
			"cell", cellID,
			"err", formatError(err),
		)
		return nil, fmt.Errorf("failed to resolve secrets: %w", err)
	}
	if len(resolved.EnvAdds) > 0 {
		containerSpec.Env = append(containerSpec.Env, resolved.EnvAdds...)
	}
	if len(resolved.MountAdds) > 0 {
		containerSpec.Volumes = append(containerSpec.Volumes, resolved.MountAdds...)
	}

	// Convert to ctr.ContainerSpec using BuildContainerSpec
	ctrSpec := BuildContainerSpec(containerSpec, opts...)

	// Create the container
	container, err := c.CreateContainer(namespace, ctrSpec, creds)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to create container from spec",
			"id",
			containerSpec.ID,
			"cell",
			cellID,
			"err",
			formatError(err),
		)
		return nil, fmt.Errorf("failed to create container from spec: %w", err)
	}

	c.logger.InfoContext(
		c.ctx,
		"created container from spec",
		"id",
		ctrSpec.ID,
		"cell",
		cellID,
		"space",
		containerSpec.SpaceName,
		"realm",
		containerSpec.RealmName,
		"namespace",
		namespace,
	)
	return container, nil
}

// validateContainerSpec validates a container spec.
func validateContainerSpec(spec ContainerSpec) error {
	if spec.ID == "" {
		return internalerrdefs.ErrEmptyContainerID
	}
	if spec.Image == "" {
		return internalerrdefs.ErrInvalidImage
	}
	return nil
}
