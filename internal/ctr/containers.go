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
	"strings"
	"syscall"
	"time"

	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/remotes/docker"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/containerd/typeurl/v2"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	cniConfigAnnotation = "io.kukeon.cni.config"
)

// RegistryCredentials contains authentication information for a container registry.
// This type matches the modelhub RegistryCredentials structure for use in the ctr package.
type RegistryCredentials struct {
	// Username is the registry username.
	Username string
	// Password is the registry password or token.
	Password string
	// ServerAddress is the registry server address (e.g., "docker.io", "registry.example.com").
	// If empty, credentials apply to the registry extracted from the image reference.
	ServerAddress string
}

var (
	errEmptyContainerID  = errors.New("ctr: container id is required")
	errEmptyCellID       = errors.New("ctr: cell id is required")
	errEmptySpaceID      = errors.New("ctr: space id is required")
	errEmptyRealmID      = errors.New("ctr: realm id is required")
	errEmptyStackID      = errors.New("ctr: stack id is required")
	errContainerExists   = errors.New("ctr: container already exists")
	ErrContainerNotFound = errors.New("ctr: container not found")
	errTaskNotFound      = errors.New("ctr: task not found")
	errTaskNotRunning    = errors.New("ctr: task is not running")
	errInvalidImage      = errors.New("ctr: image reference is required")
)

// formatError recursively unwraps errors and formats the full error chain.
// Returns a string in the format "error1: error2: error3" showing all nested errors.
func formatError(err error) string {
	if err == nil {
		return "<nil>"
	}

	var parts []string
	current := err

	for current != nil {
		parts = append(parts, current.Error())
		current = errors.Unwrap(current)
	}

	// Join all error messages with ": " separator
	result := ""
	var resultSb61 strings.Builder
	for i, part := range parts {
		if i > 0 {
			resultSb61.WriteString(": ")
		}
		resultSb61.WriteString(part)
	}
	result += resultSb61.String()

	return result
}

// buildResolver creates a remotes.Resolver with optional credentials.
// If creds is empty, returns a default resolver without authentication (supports anonymous pulls).
// If creds are provided, returns a resolver with Docker authorizer configured to match credentials by host.
func buildResolver(creds []RegistryCredentials) remotes.Resolver {
	if len(creds) == 0 {
		// Return default resolver without authentication (anonymous pulls)
		return docker.NewResolver(docker.ResolverOptions{})
	}

	// Create resolver with credentials that match by host
	return docker.NewResolver(docker.ResolverOptions{
		Authorizer: docker.NewDockerAuthorizer(
			docker.WithAuthCreds(func(host string) (string, string, error) {
				// First, try to find exact match by ServerAddress
				for _, cred := range creds {
					if cred.ServerAddress != "" && host == cred.ServerAddress {
						return cred.Username, cred.Password, nil
					}
				}
				// If no exact match, try credentials with empty ServerAddress (default/fallback)
				for _, cred := range creds {
					if cred.ServerAddress == "" {
						return cred.Username, cred.Password, nil
					}
				}
				// No matching credentials found for this host
				return "", "", nil
			}),
		),
	})
}

// ConvertRealmCredentials converts modelhub RegistryCredentials slice to ctr RegistryCredentials slice.
func ConvertRealmCredentials(creds []intmodel.RegistryCredentials) []RegistryCredentials {
	if len(creds) == 0 {
		return nil
	}
	result := make([]RegistryCredentials, len(creds))
	for i, cred := range creds {
		result[i] = RegistryCredentials{
			Username:      cred.Username,
			Password:      cred.Password,
			ServerAddress: cred.ServerAddress,
		}
	}
	return result
}

// ContainerSpec describes how to create a new container.
type ContainerSpec struct {
	// ID is the unique identifier for the container.
	ID string
	// Image is the image reference to use for the container.
	Image string
	// SnapshotKey is the key for the snapshot. If empty, defaults to ID.
	SnapshotKey string
	// Snapshotter is the snapshotter to use. If empty, uses default.
	Snapshotter string
	// Runtime is the runtime configuration.
	Runtime *ContainerRuntime
	// SpecOpts are OCI spec options to apply.
	SpecOpts []oci.SpecOpts
	// Labels are key-value pairs to attach to the container.
	Labels map[string]string
	// CNIConfigPath is the path to the CNI configuration to use for this container.
	CNIConfigPath string
}

// JoinContainerNamespaces returns a copy of spec with namespace spec options applied.
func JoinContainerNamespaces(spec ContainerSpec, ns NamespacePaths) ContainerSpec {
	specCopy := spec
	specCopy.SpecOpts = cloneSpecOpts(spec.SpecOpts)
	specCopy.SpecOpts = append(specCopy.SpecOpts, namespaceSpecOpts(ns)...)
	return specCopy
}

func cloneSpecOpts(opts []oci.SpecOpts) []oci.SpecOpts {
	if len(opts) == 0 {
		return nil
	}
	cloned := make([]oci.SpecOpts, len(opts))
	copy(cloned, opts)
	return cloned
}

func namespaceSpecOpts(ns NamespacePaths) []oci.SpecOpts {
	var opts []oci.SpecOpts
	if ns.Net != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.NetworkNamespace, ns.Net))
	}
	if ns.IPC != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.IPCNamespace, ns.IPC))
	}
	if ns.UTS != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.UTSNamespace, ns.UTS))
	}
	if ns.PID != "" {
		opts = append(opts, withNamespacePathOpt(runtimespec.PIDNamespace, ns.PID))
	}
	return opts
}

func withNamespacePathOpt(nsType runtimespec.LinuxNamespaceType, path string) oci.SpecOpts {
	return func(_ context.Context, _ oci.Client, _ *containers.Container, s *runtimespec.Spec) error {
		if s.Linux == nil {
			s.Linux = &runtimespec.Linux{}
		}

		for i := range s.Linux.Namespaces {
			if s.Linux.Namespaces[i].Type == nsType {
				s.Linux.Namespaces[i].Path = path
				return nil
			}
		}

		s.Linux.Namespaces = append(s.Linux.Namespaces, runtimespec.LinuxNamespace{
			Type: nsType,
			Path: path,
		})
		return nil
	}
}

func (c *client) applySpecOpts(ctx context.Context, container containerd.Container, opts []oci.SpecOpts) error {
	if len(opts) == 0 {
		return nil
	}

	ociSpec, err := container.Spec(ctx)
	if err != nil {
		return fmt.Errorf("failed to load container spec: %w", err)
	}

	for _, opt := range opts {
		if err = opt(ctx, c.cClient, nil, ociSpec); err != nil {
			return fmt.Errorf("failed to apply spec option: %w", err)
		}
	}

	if err = container.Update(ctx, withUpdatedSpec(ociSpec)); err != nil {
		return fmt.Errorf("failed to persist updated spec: %w", err)
	}
	return nil
}

// ContainerRuntime describes the runtime configuration.
type ContainerRuntime struct {
	// Name is the runtime name (e.g., "io.containerd.runc.v2").
	Name string
	// Options are runtime-specific options.
	Options interface{}
}

// TaskSpec describes how to create a new task.
type TaskSpec struct {
	// IO is the IO configuration for the task.
	IO *TaskIO
	// Options are task creation options.
	Options []containerd.NewTaskOpts
}

// TaskIO describes the IO configuration for a task.
type TaskIO struct {
	// Stdin is the path to stdin (if any).
	Stdin string
	// Stdout is the path to stdout (if any).
	Stdout string
	// Stderr is the path to stderr (if any).
	Stderr string
	// Terminal indicates if the task should have a TTY.
	Terminal bool
}

// ContainerDeleteOptions describes options for deleting a container.
type ContainerDeleteOptions struct {
	// SnapshotCleanup indicates whether to clean up snapshots.
	SnapshotCleanup bool
}

// StopContainerOptions describes options for stopping a container.
type StopContainerOptions struct {
	// Signal is the signal to send (defaults to SIGTERM).
	Signal string
	// Timeout is the timeout for graceful shutdown.
	Timeout *time.Duration
	// Force indicates whether to force kill if timeout is exceeded.
	Force bool
}

func withUpdatedSpec(spec *oci.Spec) containerd.UpdateContainerOpts {
	return func(_ context.Context, _ *containerd.Client, c *containers.Container) error {
		if spec == nil {
			return errors.New("oci spec is nil")
		}
		anySpec, err := typeurl.MarshalAnyToProto(spec)
		if err != nil {
			return err
		}
		c.Spec = anySpec
		return nil
	}
}

// namespaceCtx returns a context with the namespace set.
func (c *client) namespaceCtx(ctx context.Context) context.Context {
	c.namespaceMu.RLock()
	defer c.namespaceMu.RUnlock()
	return namespaces.WithNamespace(ctx, c.namespace)
}

// SetNamespace sets the namespace for subsequent operations.
// This clears any previously set registry credentials.
func (c *client) SetNamespace(namespace string) {
	c.namespaceMu.Lock()
	defer c.namespaceMu.Unlock()
	c.namespace = namespace
	c.logger.DebugContext(c.ctx, "set namespace", "namespace", namespace)

	// Clear credentials when namespace is set without credentials
	c.registryCredentialsMu.Lock()
	defer c.registryCredentialsMu.Unlock()
	c.registryCredentials = nil
}

// SetNamespaceWithCredentials sets the namespace and associated registry credentials.
// This should be called when switching to a realm's namespace.
func (c *client) SetNamespaceWithCredentials(namespace string, creds []RegistryCredentials) {
	c.namespaceMu.Lock()
	defer c.namespaceMu.Unlock()
	c.namespace = namespace
	c.logger.DebugContext(c.ctx, "set namespace with credentials", "namespace", namespace, "creds_count", len(creds))

	c.registryCredentialsMu.Lock()
	defer c.registryCredentialsMu.Unlock()
	c.registryCredentials = creds
}

// GetRegistryCredentials returns the current registry credentials for the namespace.
func (c *client) GetRegistryCredentials() []RegistryCredentials {
	c.registryCredentialsMu.RLock()
	defer c.registryCredentialsMu.RUnlock()
	return c.registryCredentials
}

// Namespace returns the current namespace.
func (c *client) Namespace() string {
	c.namespaceMu.RLock()
	defer c.namespaceMu.RUnlock()
	return c.namespace
}

// storeContainer stores a container in the cache.
func (c *client) storeContainer(id string, container containerd.Container) {
	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	if c.containers == nil {
		c.containers = make(map[string]containerd.Container)
	}
	c.containers[id] = container
}

// loadContainer loads a container from cache or containerd.
func (c *client) loadContainer(ctx context.Context, id string) (containerd.Container, error) {
	c.containersMu.RLock()
	if container, ok := c.containers[id]; ok {
		c.containersMu.RUnlock()
		return container, nil
	}
	c.containersMu.RUnlock()

	nsCtx := c.namespaceCtx(ctx)
	container, err := c.cClient.LoadContainer(nsCtx, id)
	if err != nil {
		// Only wrap "not found" errors with ErrContainerNotFound
		// Other errors (connection failures, permission errors, etc.) should be returned as-is
		if errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", ErrContainerNotFound, err)
		}
		return nil, err
	}
	c.storeContainer(id, container)
	return container, nil
}

// dropContainer removes a container from the cache.
func (c *client) dropContainer(id string) {
	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	if c.containers != nil {
		delete(c.containers, id)
	}
}

// storeTask stores a task in the cache.
func (c *client) storeTask(id string, task containerd.Task) {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()
	if c.tasks == nil {
		c.tasks = make(map[string]containerd.Task)
	}
	c.tasks[id] = task
}

// loadTask loads a task from cache or container.
func (c *client) loadTask(ctx context.Context, id string) (containerd.Task, error) {
	c.tasksMu.RLock()
	if task, ok := c.tasks[id]; ok {
		c.tasksMu.RUnlock()
		return task, nil
	}
	c.tasksMu.RUnlock()

	container, err := c.loadContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(ctx)
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errTaskNotFound, err)
	}
	c.storeTask(id, task)
	return task, nil
}

// dropTask removes a task from the cache.
func (c *client) dropTask(id string) {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()
	if c.tasks != nil {
		delete(c.tasks, id)
	}
}

// ensureImageUnpacked ensures that an image is unpacked for the given snapshotter.
// If the image is not unpacked, it will be unpacked. Returns an error if unpacking fails.
func (c *client) ensureImageUnpacked(ctx context.Context, image containerd.Image, snapshotter string) error {
	nsCtx := c.namespaceCtx(ctx)

	// Check if image is already unpacked
	unpacked, err := image.IsUnpacked(nsCtx, snapshotter)
	if err != nil {
		c.logger.WarnContext(
			ctx,
			"failed to check if image is unpacked",
			"image",
			image.Name(),
			"snapshotter",
			snapshotter,
			"err",
			formatError(err),
		)
		// Continue to attempt unpack even if check failed
	} else if unpacked {
		c.logger.DebugContext(ctx, "image already unpacked", "image", image.Name(), "snapshotter", snapshotter)
		return nil
	}

	// Image is not unpacked, unpack it
	c.logger.DebugContext(ctx, "unpacking image", "image", image.Name(), "snapshotter", snapshotter)
	err = image.Unpack(nsCtx, snapshotter)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
			"failed to unpack image",
			"image",
			image.Name(),
			"snapshotter",
			snapshotter,
			"err",
			formatError(err),
		)
		return fmt.Errorf("failed to unpack image %s: %w", image.Name(), err)
	}

	c.logger.DebugContext(ctx, "image unpacked successfully", "image", image.Name(), "snapshotter", snapshotter)
	return nil
}

// CreateContainer creates a new container with the provided spec.
func (c *client) CreateContainer(ctx context.Context, spec ContainerSpec) (containerd.Container, error) {
	if err := validateContainerSpec(spec); err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(ctx)

	// Check if container already exists
	_, err := c.cClient.LoadContainer(nsCtx, spec.ID)
	if err == nil {
		c.logger.WarnContext(ctx, "container already exists", "id", spec.ID)
		return nil, errContainerExists
	}

	// Pull the image if needed
	image, err := c.cClient.GetImage(nsCtx, spec.Image)
	if err != nil {
		c.logger.DebugContext(ctx, "image not found locally, pulling", "image", spec.Image)
		// Create a lease for the pull operation to avoid lease management issues
		// The lease will be automatically cleaned up when the context is done
		leaseManager := c.cClient.LeasesService()
		lease, leaseErr := leaseManager.Create(
			nsCtx,
			leases.WithID(fmt.Sprintf("pull-%s-%d", spec.Image, time.Now().UnixNano())),
		)
		if leaseErr != nil {
			c.logger.WarnContext(
				ctx,
				"failed to create lease for image pull, continuing without lease",
				"image",
				spec.Image,
				"err",
				formatError(leaseErr),
			)
			// Continue without lease - some containerd setups may not require it
		} else {
			// Use lease context for pull
			leaseCtx := leases.WithLease(nsCtx, lease.ID)
			defer func() {
				// Clean up lease after pull
				if deleteErr := leaseManager.Delete(nsCtx, lease); deleteErr != nil {
					c.logger.WarnContext(ctx, "failed to delete lease after image pull", "lease", lease.ID, "err", formatError(deleteErr))
				}
			}()
			nsCtx = leaseCtx
		}
		// Use default platform for pull
		// The image will be unpacked separately after pull
		platform := platforms.DefaultSpec()

		// Build pull options with resolver if credentials are available
		pullOpts := []containerd.RemoteOpt{
			containerd.WithPlatform(platforms.Format(platform)),
		}

		// Get credentials from client (set when namespace was configured)
		creds := c.GetRegistryCredentials()
		if len(creds) > 0 {
			resolver := buildResolver(creds)
			pullOpts = append(pullOpts, containerd.WithResolver(resolver))
			c.logger.DebugContext(ctx, "pulling image with credentials", "image", spec.Image, "creds_count", len(creds))
		} else {
			c.logger.DebugContext(ctx, "pulling image anonymously", "image", spec.Image)
		}

		image, err = c.cClient.Pull(nsCtx, spec.Image, pullOpts...)
		if err != nil {
			c.logger.ErrorContext(ctx, "failed to pull image", "image", spec.Image, "err", formatError(err))
			return nil, fmt.Errorf("failed to pull image %s: %w", spec.Image, err)
		}
	}

	// Ensure image is unpacked before creating container
	// Use spec.Snapshotter if provided, otherwise empty string for default snapshotter
	if err = c.ensureImageUnpacked(ctx, image, spec.Snapshotter); err != nil {
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
		c.logger.ErrorContext(ctx, "failed to create container", "id", spec.ID, "err", formatError(err))
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	c.storeContainer(spec.ID, container)
	c.logger.InfoContext(ctx, "created container", "id", spec.ID, "image", spec.Image)
	return container, nil
}

// GetContainer retrieves a container by ID.
func (c *client) GetContainer(ctx context.Context, id string) (containerd.Container, error) {
	if id == "" {
		return nil, errEmptyContainerID
	}
	return c.loadContainer(ctx, id)
}

// ListContainers lists all containers matching the provided filters.
func (c *client) ListContainers(ctx context.Context, filters ...string) ([]containerd.Container, error) {
	nsCtx := c.namespaceCtx(ctx)

	containers, err := c.cClient.Containers(nsCtx, filters...)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to list containers", "err", formatError(err))
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Update cache
	for _, container := range containers {
		c.storeContainer(container.ID(), container)
	}

	c.logger.DebugContext(ctx, "listed containers", "count", len(containers))
	return containers, nil
}

// ExistsContainer checks if a container exists.
func (c *client) ExistsContainer(ctx context.Context, id string) (bool, error) {
	if id == "" {
		return false, errEmptyContainerID
	}

	nsCtx := c.namespaceCtx(ctx)
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
func (c *client) DeleteContainer(ctx context.Context, id string, opts ContainerDeleteOptions) error {
	if id == "" {
		return errEmptyContainerID
	}

	c.logger.DebugContext(ctx, "starting to delete container", "id", id, "snapshot_cleanup", opts.SnapshotCleanup)
	container, err := c.loadContainer(ctx, id)
	if err != nil {
		c.logger.DebugContext(ctx, "failed to load container for deletion", "id", id, "error", err)
		return err
	}

	nsCtx := c.namespaceCtx(ctx)

	// Try to get and delete the task if it exists
	c.logger.DebugContext(ctx, "checking for task to delete", "id", id)
	task, err := container.Task(nsCtx, nil)
	if err == nil {
		// Task exists, delete it first
		c.logger.DebugContext(ctx, "deleting task", "id", id)
		_, err = task.Delete(nsCtx, containerd.WithProcessKill)
		if err != nil {
			c.logger.WarnContext(ctx, "failed to delete task", "id", id, "err", formatError(err))
		} else {
			c.logger.DebugContext(ctx, "deleted task", "id", id)
		}
		c.dropTask(id)
	} else {
		c.logger.DebugContext(ctx, "no task found for container", "id", id)
	}

	// Delete container
	c.logger.DebugContext(ctx, "deleting container from containerd", "id", id)
	deleteOpts := []containerd.DeleteOpts{}
	if opts.SnapshotCleanup {
		deleteOpts = append(deleteOpts, containerd.WithSnapshotCleanup)
	}

	err = container.Delete(nsCtx, deleteOpts...)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to delete container", "id", id, "err", formatError(err))
		return fmt.Errorf("failed to delete container: %w", err)
	}

	c.dropContainer(id)
	c.logger.InfoContext(ctx, "deleted container", "id", id)
	return nil
}

// StartContainer creates and starts a task for the container.
func (c *client) StartContainer(
	ctx context.Context,
	containerSpec ContainerSpec,
	taskSpec TaskSpec,
) (containerd.Task, error) {
	if containerSpec.ID == "" {
		return nil, errEmptyContainerID
	}

	container, err := c.loadContainer(ctx, containerSpec.ID)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(ctx)

	if len(containerSpec.SpecOpts) > 0 {
		if err = c.applySpecOpts(nsCtx, container, containerSpec.SpecOpts); err != nil {
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
			c.logger.WarnContext(ctx, "task already running", "id", containerSpec.ID)
			c.storeTask(containerSpec.ID, existingTask)
			return existingTask, nil
		}
		// Task exists but is not running (stopped), delete it before creating a new one
		c.logger.DebugContext(ctx, "deleting stopped task", "id", containerSpec.ID)
		_, err = existingTask.Delete(nsCtx, containerd.WithProcessKill)
		if err != nil {
			c.logger.WarnContext(ctx, "failed to delete stopped task", "id", containerSpec.ID, "err", formatError(err))
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
		c.logger.ErrorContext(ctx, "failed to create task", "id", containerSpec.ID, "err", formatError(err))
		return nil, fmt.Errorf("failed to create task: %w", err)
	}

	// Start the task
	err = task.Start(nsCtx)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to start task", "id", containerSpec.ID, "err", formatError(err))
		// Clean up task on failure
		_, _ = task.Delete(nsCtx, containerd.WithProcessKill)
		return nil, fmt.Errorf("failed to start task: %w", err)
	}

	c.storeTask(containerSpec.ID, task)
	c.logger.InfoContext(ctx, "started container", "id", containerSpec.ID)
	return task, nil
}

// StopContainer stops a running container task.
func (c *client) StopContainer(
	ctx context.Context,
	id string,
	opts StopContainerOptions,
) (*containerd.ExitStatus, error) {
	if id == "" {
		return nil, errEmptyContainerID
	}

	c.logger.DebugContext(ctx, "starting to stop container", "id", id)
	task, err := c.loadTask(ctx, id)
	if err != nil {
		c.logger.DebugContext(ctx, "failed to load task for container", "id", id, "error", err)
		return nil, err
	}

	nsCtx := c.namespaceCtx(ctx)

	// Check task status
	c.logger.DebugContext(ctx, "checking task status", "id", id)
	status, err := task.Status(nsCtx)
	if err != nil {
		c.logger.DebugContext(ctx, "failed to get task status", "id", id, "error", err)
		return nil, fmt.Errorf("failed to get task status: %w", err)
	}

	if status.Status != containerd.Running {
		c.logger.WarnContext(ctx, "task is not running", "id", id, "status", status.Status)
		return nil, errTaskNotRunning
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
		c.logger.ErrorContext(ctx, "failed to kill task", "id", id, "signal", signal, "err", formatError(err))
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
				c.logger.WarnContext(ctx, "timeout exceeded, force killing", "id", id)
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

	c.logger.InfoContext(ctx, "stopped container", "id", id, "exit_code", exitStatus.ExitCode())

	// Verify task is actually stopped
	finalStatus, err := task.Status(nsCtx)
	if err != nil {
		c.logger.WarnContext(ctx, "failed to verify task status after stop", "id", id, "err", formatError(err))
		// Continue anyway - we got exit status
	} else if finalStatus.Status == containerd.Running {
		c.logger.WarnContext(ctx, "task still running after stop, force killing again", "id", id)
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
	c.logger.DebugContext(ctx, "deleting stopped task", "id", id)
	_, err = task.Delete(nsCtx, containerd.WithProcessKill)
	if err != nil {
		c.logger.WarnContext(ctx, "failed to delete stopped task", "id", id, "err", formatError(err))
		// Continue anyway - task is stopped even if deletion failed
	} else {
		c.logger.DebugContext(ctx, "deleted stopped task", "id", id)
	}
	c.dropTask(id)

	return &exitStatus, nil
}

// TaskStatus returns the current status of a task.
func (c *client) TaskStatus(ctx context.Context, id string) (containerd.Status, error) {
	if id == "" {
		return containerd.Status{}, errEmptyContainerID
	}

	task, err := c.loadTask(ctx, id)
	if err != nil {
		return containerd.Status{}, err
	}

	nsCtx := c.namespaceCtx(ctx)
	status, err := task.Status(nsCtx)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to get task status", "id", id, "err", formatError(err))
		return containerd.Status{}, fmt.Errorf("failed to get task status: %w", err)
	}

	return status, nil
}

// TaskMetrics returns the metrics for a task.
func (c *client) TaskMetrics(ctx context.Context, id string) (*apitypes.Metric, error) {
	if id == "" {
		return nil, errEmptyContainerID
	}

	task, err := c.loadTask(ctx, id)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(ctx)
	metrics, err := task.Metrics(nsCtx)
	if err != nil {
		c.logger.ErrorContext(ctx, "failed to get task metrics", "id", id, "err", formatError(err))
		return nil, fmt.Errorf("failed to get task metrics: %w", err)
	}

	return metrics, nil
}

// validateContainerSpec validates a container spec.
func validateContainerSpec(spec ContainerSpec) error {
	if spec.ID == "" {
		return errEmptyContainerID
	}
	if spec.Image == "" {
		return errInvalidImage
	}
	return nil
}

// BuildContainerSpec converts an internal ContainerSpec to ctr.ContainerSpec
// with the expected defaults applied.
// Uses ContainerdID if available, otherwise falls back to ID.
func BuildContainerSpec(
	containerSpec intmodel.ContainerSpec,
) ContainerSpec {
	// Use ContainerdID if available, otherwise fall back to ID
	containerdID := containerSpec.ContainerdID
	if containerdID == "" {
		containerdID = containerSpec.ID
	}

	cellID := containerSpec.CellName
	spaceID := containerSpec.SpaceName
	realmID := containerSpec.RealmName
	stackID := containerSpec.StackName

	// Build labels
	labels := make(map[string]string)
	// Add kukeon-specific labels
	labels["kukeon.io/container-type"] = "container"
	labels["kukeon.io/cell"] = cellID
	labels["kukeon.io/space"] = spaceID
	labels["kukeon.io/realm"] = realmID
	labels["kukeon.io/stack"] = stackID

	// Build OCI spec options
	specOpts := []oci.SpecOpts{
		oci.WithDefaultPathEnv,
	}

	// Set hostname to containerd ID if not empty
	if containerdID != "" {
		specOpts = append(specOpts, oci.WithHostname(containerdID))
	}

	// Set command and args
	if containerSpec.Command != "" {
		args := []string{containerSpec.Command}
		if len(containerSpec.Args) > 0 {
			args = append(args, containerSpec.Args...)
		}
		specOpts = append(specOpts, oci.WithProcessArgs(args...))
	} else if len(containerSpec.Args) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(containerSpec.Args...))
	}

	// Set environment variables
	if len(containerSpec.Env) > 0 {
		specOpts = append(specOpts, oci.WithEnv(containerSpec.Env))
	}

	// Set privileged mode if specified
	if containerSpec.Privileged {
		specOpts = append(specOpts, oci.WithPrivileged)
	}

	return ContainerSpec{
		ID:            containerdID,
		Image:         containerSpec.Image,
		Labels:        labels,
		SpecOpts:      specOpts,
		CNIConfigPath: containerSpec.CNIConfigPath,
	}
}

// CreateContainerFromSpec converts an internal ContainerSpec to ctr.ContainerSpec and creates the container.
func (c *client) CreateContainerFromSpec(
	ctx context.Context,
	containerSpec intmodel.ContainerSpec,
) (containerd.Container, error) {
	if containerSpec.ID == "" {
		return nil, errEmptyContainerID
	}

	if containerSpec.Image == "" {
		return nil, errInvalidImage
	}

	if containerSpec.CellName == "" {
		return nil, errEmptyCellID
	}

	if containerSpec.SpaceName == "" {
		return nil, errEmptySpaceID
	}

	if containerSpec.RealmName == "" {
		return nil, errEmptyRealmID
	}

	if containerSpec.StackName == "" {
		return nil, errEmptyStackID
	}

	cellID := containerSpec.CellName

	// Convert to ctr.ContainerSpec using BuildContainerSpec
	ctrSpec := BuildContainerSpec(containerSpec)

	// Create the container
	container, err := c.CreateContainer(ctx, ctrSpec)
	if err != nil {
		c.logger.ErrorContext(
			ctx,
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
		ctx,
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
