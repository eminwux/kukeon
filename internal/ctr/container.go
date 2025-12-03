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
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// StartContainer creates and starts a task for the container.
func (c *client) StartContainer(
	containerSpec ContainerSpec,
	taskSpec TaskSpec,
) (containerd.Task, error) {
	if containerSpec.ID == "" {
		return nil, ErrEmptyContainerID
	}

	container, err := c.loadContainer(containerSpec.ID)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx()

	if len(containerSpec.SpecOpts) > 0 {
		if err = c.applySpecOpts(container, containerSpec.SpecOpts); err != nil {
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
			c.storeTask(containerSpec.ID, existingTask)
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
		c.dropTask(containerSpec.ID)
	}

	// Build IO creator
	var ioCreator cio.Creator
	if taskSpec.IO != nil {
		if taskSpec.IO.Terminal {
			ioCreator = cio.NewCreator(cio.WithStreams(nil, nil, nil), cio.WithTerminal)
		} else {
			ioCreator = cio.NewCreator(cio.WithStreams(nil, nil, nil))
		}
	} else {
		// Default: no IO streams
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

	c.storeTask(containerSpec.ID, task)
	c.logger.InfoContext(c.ctx, "started container", "id", containerSpec.ID)
	return task, nil
}

// StopContainer stops a running container task.
func (c *client) StopContainer(
	id string,
	opts StopContainerOptions,
) (*containerd.ExitStatus, error) {
	if id == "" {
		return nil, ErrEmptyContainerID
	}

	c.logger.DebugContext(c.ctx, "starting to stop container", "id", id)
	task, err := c.loadTask(id)
	if err != nil {
		c.logger.DebugContext(c.ctx, "failed to load task for container", "id", id, "error", err)
		return nil, err
	}

	nsCtx := c.namespaceCtx()

	// Check task status
	c.logger.DebugContext(c.ctx, "checking task status", "id", id)
	status, err := task.Status(nsCtx)
	if err != nil {
		c.logger.DebugContext(c.ctx, "failed to get task status", "id", id, "error", err)
		return nil, fmt.Errorf("failed to get task status: %w", err)
	}

	if status.Status != containerd.Running {
		c.logger.WarnContext(c.ctx, "task is not running", "id", id, "status", status.Status)
		return nil, ErrTaskNotRunning
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

	c.logger.InfoContext(c.ctx, "stopped container", "id", id, "exit_code", exitStatus.ExitCode())

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
	c.dropTask(id)

	return &exitStatus, nil
}

// CreateContainer creates a new container with the provided spec.
func (c *client) CreateContainer(spec ContainerSpec) (containerd.Container, error) {
	if err := validateContainerSpec(spec); err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx()

	// Check if container already exists
	_, err := c.cClient.LoadContainer(nsCtx, spec.ID)
	if err == nil {
		c.logger.WarnContext(c.ctx, "container already exists", "id", spec.ID)
		return nil, ErrContainerExists
	}

	// Pull the image if needed
	image, err := c.pullImage(spec.Image)
	if err != nil {
		return nil, err
	}

	// Ensure image is unpacked before creating container
	// Use spec.Snapshotter if provided, otherwise empty string for default snapshotter
	if err = c.ensureImageUnpacked(image, spec.Snapshotter); err != nil {
		return nil, fmt.Errorf("failed to ensure image is unpacked: %w", err)
	}

	// Determine snapshot key
	snapshotKey := spec.SnapshotKey
	if snapshotKey == "" {
		snapshotKey = spec.ID
	}

	// Build OCI spec options, injecting annotations when needed
	specOpts := make([]oci.SpecOpts, 0, len(spec.SpecOpts)+1)
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

	container, err := c.cClient.NewContainer(nsCtx, spec.ID, opts...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to create container", "id", spec.ID, "err", formatError(err))
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	c.storeContainer(spec.ID, container)
	c.logger.InfoContext(c.ctx, "created container", "id", spec.ID, "image", spec.Image)
	return container, nil
}

// GetContainer retrieves a container by ID.
func (c *client) GetContainer(id string) (containerd.Container, error) {
	if id == "" {
		return nil, ErrEmptyContainerID
	}
	return c.loadContainer(id)
}

// ListContainers lists all containers matching the provided filters.
func (c *client) ListContainers(filters ...string) ([]containerd.Container, error) {
	nsCtx := c.namespaceCtx()

	containers, err := c.cClient.Containers(nsCtx, filters...)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to list containers", "err", formatError(err))
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Update cache
	for _, container := range containers {
		c.storeContainer(container.ID(), container)
	}

	c.logger.DebugContext(c.ctx, "listed containers", "count", len(containers))
	return containers, nil
}

// ExistsContainer checks if a container exists.
func (c *client) ExistsContainer(id string) (bool, error) {
	if id == "" {
		return false, ErrEmptyContainerID
	}

	nsCtx := c.namespaceCtx()
	_, err := c.cClient.LoadContainer(nsCtx, id)
	if err != nil {
		// Only wrap "not found" errors with ErrContainerNotFound so callers can use errors.Is to check
		// Other errors (connection failures, permission errors, etc.) should be returned as-is
		if errdefs.IsNotFound(err) {
			return false, fmt.Errorf("%w: %w", ErrContainerNotFound, err)
		}
		return false, err
	}
	return true, nil
}

// DeleteContainer deletes a container.
// It is idempotent - if the container doesn't exist, it returns nil (treating it as already deleted).
// It automatically deletes any associated task before deleting the container.
func (c *client) DeleteContainer(id string, opts ContainerDeleteOptions) error {
	if id == "" {
		return ErrEmptyContainerID
	}

	c.logger.DebugContext(c.ctx, "starting to delete container", "id", id, "snapshot_cleanup", opts.SnapshotCleanup)
	container, err := c.loadContainer(id)
	if err != nil {
		// Container doesn't exist - treat as idempotent (already deleted)
		if errors.Is(err, ErrContainerNotFound) {
			c.logger.DebugContext(c.ctx, "container not found", "id", id)
			return nil
		}
		c.logger.DebugContext(c.ctx, "failed to load container for deletion", "id", id, "error", err)
		return err
	}

	nsCtx := c.namespaceCtx()

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
		c.dropTask(id)
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
			c.logger.DebugContext(c.ctx, "container not found", "id", id)
			c.dropContainer(id)
			return nil
		}
		c.logger.ErrorContext(c.ctx, "failed to delete container", "id", id, "err", formatError(err))
		return fmt.Errorf("failed to delete container: %w", err)
	}

	c.dropContainer(id)
	c.logger.InfoContext(c.ctx, "deleted container", "id", id)
	return nil
}

// CreateContainerFromSpec converts an internal ContainerSpec to ctr.ContainerSpec and creates the container.
func (c *client) CreateContainerFromSpec(
	containerSpec intmodel.ContainerSpec,
) (containerd.Container, error) {
	if containerSpec.ID == "" {
		return nil, ErrEmptyContainerID
	}

	if containerSpec.Image == "" {
		return nil, ErrInvalidImage
	}

	if containerSpec.CellName == "" {
		return nil, ErrEmptyCellID
	}

	if containerSpec.SpaceName == "" {
		return nil, ErrEmptySpaceID
	}

	if containerSpec.RealmName == "" {
		return nil, ErrEmptyRealmID
	}

	if containerSpec.StackName == "" {
		return nil, ErrEmptyStackID
	}

	cellID := containerSpec.CellName

	// Convert to ctr.ContainerSpec using BuildContainerSpec
	ctrSpec := BuildContainerSpec(containerSpec)

	// Create the container
	container, err := c.CreateContainer(ctrSpec)
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
	)
	return container, nil
}

// validateContainerSpec validates a container spec.
func validateContainerSpec(spec ContainerSpec) error {
	if spec.ID == "" {
		return ErrEmptyContainerID
	}
	if spec.Image == "" {
		return ErrInvalidImage
	}
	return nil
}
