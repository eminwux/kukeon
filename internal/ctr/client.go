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
	"io"
	"log/slog"
	"sync"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

type client struct {
	ctx                  context.Context
	logger               *slog.Logger
	socket               string
	cClientMu            sync.Mutex
	cClient              *containerd.Client
	cgroupsMu            sync.RWMutex
	cgroups              map[string]*cgroup2.Manager
	containersMu         sync.RWMutex
	containers           map[string]containerd.Container
	tasksMu              sync.RWMutex
	tasks                map[string]containerd.Task
	cgroupMountpointOnce sync.Once
	cgroupMountpoint     string
	cgroupMountpointErr  error
}

type Client interface {
	Connect() error
	Close() error

	CreateNamespace(namespace string) error
	DeleteNamespace(namespace string) error
	ListNamespaces() ([]string, error)
	GetNamespace(namespace string) (string, error)
	ExistsNamespace(namespace string) (bool, error)
	CleanupNamespaceResources(namespace, snapshotter string) error

	GetCgroupMountpoint() string
	GetCurrentCgroupPath() (string, error)
	CgroupPath(group, mountpoint string) (string, error)
	NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error)
	LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error)
	DeleteCgroup(group, mountpoint string) error
	// EnsureSubtreeControllers writes "+<ctrl>" to the named group's own
	// cgroup.subtree_control AND to every ancestor up to the unified cgroup
	// mount, so the group's children inherit the controllers. The level-
	// agnostic primitive used by every realm/space/stack ensure path (issue
	// #327) and by the cell wrappers below (issues #312, #314). Filters the
	// requested set against what the host root advertises and returns the
	// effective set actually written. Idempotent — re-running on an already-
	// delegated subtree is a no-op.
	EnsureSubtreeControllers(group, mountpoint string, controllers []string) ([]string, error)
	// EnableCellSubtreeControllers enables the named cgroup-v2 controllers in
	// the cell cgroup's own subtree_control AND in every ancestor's
	// subtree_control up to the unified cgroup mount, so child cgroups (the
	// per-container task cgroups runc creates under Linux.CgroupsPath)
	// inherit the controllers and cell-level resource accounting / limits
	// become effective. Returns the effective controller set actually
	// written (the requested set filtered against the host root's
	// cgroup.controllers) so the runner can persist it on
	// CellStatus.SubtreeControllers (issue #328). Thin wrapper around
	// EnsureSubtreeControllers kept for the cell call sites' readability.
	// Issue #312.
	EnableCellSubtreeControllers(group, mountpoint string, controllers []string) ([]string, error)
	// EnableCellAllSubtreeControllers is the cell/profile=NestedCgroupRuntime
	// counterpart: it delegates the full host-available cgroup-v2 controller
	// set on the cell's subtree_control (and every ancestor's), rather than
	// the kukeon resource subset. Returns the effective controller set
	// actually written so the runner can persist it on
	// CellStatus.SubtreeControllers (issue #328). Used by cells that host
	// an inner cgroup runtime (an embedded containerd or systemd) which
	// needs to in turn delegate arbitrary controllers to its own children.
	// Issue #314.
	EnableCellAllSubtreeControllers(group, mountpoint string) ([]string, error)
	// RelocateProcessesToLeaf drains every PID currently in <group>/cgroup.procs
	// into a freshly-mkdir'd leaf cgroup at <group>/<leaf>. Used to satisfy
	// cgroup-v2's no-internal-process rule (issue #336): subtree_control
	// widening for non-thread-aware controllers (memory, io, ...) is rejected
	// by the kernel when the target cgroup hosts processes directly. The
	// leaf inherits the parent's controllers via the parent's subtree_control,
	// so resource accounting at <group> still applies — the PIDs just live
	// one level deeper. Idempotent: re-running on an already-drained group
	// is a no-op.
	RelocateProcessesToLeaf(group, mountpoint, leaf string) error
	CreateContainerFromSpec(namespace string, spec intmodel.ContainerSpec, creds []RegistryCredentials, opts ...BuildOption) (containerd.Container, error)

	CreateContainer(namespace string, spec ContainerSpec, creds []RegistryCredentials) (containerd.Container, error)
	GetContainer(namespace, id string) (containerd.Container, error)
	ListContainers(namespace string, filters ...string) ([]containerd.Container, error)
	ExistsContainer(namespace, id string) (bool, error)
	DeleteContainer(namespace, id string, opts ContainerDeleteOptions) error
	StartContainer(namespace string, spec ContainerSpec, taskSpec TaskSpec) (containerd.Task, error)
	StopContainer(namespace, id string, opts StopContainerOptions) (*containerd.ExitStatus, error)

	TaskStatus(namespace, id string) (containerd.Status, error)
	TaskMetrics(namespace, id string) (*apitypes.Metric, error)

	// ContainerProcessUID returns the resolved process.User.UID from the
	// given container's OCI runtime spec. Used after CreateContainerFromSpec
	// to chown the host-side per-container Attachable tty directory to the
	// runtime uid the container will execute as — which can be non-root
	// when the image carries a USER directive (or the cell profile sets
	// container.user). Without this, kuketty inside the container fails to
	// create its socket/log/capture files in the bind-mounted dir.
	ContainerProcessUID(namespace string, container containerd.Container) (uint32, error)

	// LoadImage imports an OCI/docker image tarball into the specified
	// containerd namespace and returns the names of the imported images.
	LoadImage(namespace string, reader io.Reader) ([]string, error)

	// ListImages enumerates images in the specified containerd namespace.
	ListImages(namespace string) ([]ImageInfo, error)

	// GetImage returns metadata for the named image ref in the specified
	// containerd namespace. Returns errdefs.ErrImageNotFound if
	// the ref is absent.
	GetImage(namespace, ref string) (ImageInfo, error)

	// ImageChainID returns the chainID the image at ref would unpack to
	// today, computed from the current rootfs DiffIDs in the namespace's
	// content store. Pair with ContainerRootChainID to detect that an
	// image tag has been re-pointed since a container was created (the
	// digest-drift signal missing from bootstrapCell's image comparison
	// in issue #915 defect 2). Returns errdefs.ErrImageNotFound if the
	// ref is absent.
	ImageChainID(namespace, ref string) (string, error)

	// ContainerRootChainID returns the chainID of the parent layer stack
	// the container's root snapshot was committed against — i.e. the
	// image content the container is anchored on at the moment of the
	// call, regardless of what the image ref by the same name resolves
	// to today. Returns errdefs.ErrContainerNotFound if the container is
	// absent.
	ContainerRootChainID(namespace, containerID string) (string, error)

	// DeleteImage removes the named image ref from the specified
	// containerd namespace. Returns errdefs.ErrImageNotFound if the ref
	// is absent so callers can distinguish missing from operational
	// failures.
	DeleteImage(namespace, ref string) error

	// PruneImages reclaims dangling image layers and the orphaned leases
	// pinning them in the specified containerd namespace, leaving tagged
	// images and the snapshots backing live containers untouched. Returns
	// the count of leases released vs. retained.
	PruneImages(namespace string) (PruneResult, error)

	// NamespaceStorage returns the per-namespace storage footprint —
	// snapshot count (across every registered snapshotter in
	// KukeonKnownSnapshotters), lease count, and content-blob count plus
	// summed byte size. Used by `kuke status` to surface accumulation
	// before the data volume fills (issue #1039); the figures come from
	// containerd's metadata stores (boltdb iterators), not an on-disk
	// du, so the call stays cheap enough for the status command's
	// budget. Per-snapshot disk usage is intentionally omitted — it
	// would require walking the snapshotter's filesystem.
	NamespaceStorage(namespace string) (StorageStats, error)
}

func NewClient(ctx context.Context, logger *slog.Logger, socket string) Client {
	return &client{
		ctx:        ctx,
		logger:     logger,
		socket:     socket,
		cgroups:    make(map[string]*cgroup2.Manager),
		containers: make(map[string]containerd.Container),
		tasks:      make(map[string]containerd.Task),
	}
}

// verifyConnection checks if the containerd client connection is still valid
// by performing a lightweight operation (listing namespaces).
func (c *client) verifyConnection() error {
	if c.cClient == nil {
		return errors.New("client is nil")
	}
	// Use a simple operation to verify connection - list namespaces
	// This is lightweight and will fail if connection is broken
	namespaces := c.cClient.NamespaceService()
	_, err := namespaces.List(c.ctx)
	return err
}

func (c *client) Connect() error {
	// cClientMu serializes the read-modify-write of c.cClient so concurrent
	// first-use callers (the first RPC handler and the first reconcile tick,
	// per issue #684) can't each observe nil and each dial containerd —
	// leaking N-1 connections and racing on the pointer. The sync.Once in the
	// runner serializes wrapper construction; this lock serializes the
	// connection the wrapper owns.
	c.cClientMu.Lock()
	defer c.cClientMu.Unlock()

	// If already connected, verify the connection is still valid
	if c.cClient != nil {
		err := c.verifyConnection()
		if err == nil {
			// Connection is valid, reuse it
			c.logger.DebugContext(c.ctx, "containerd client already connected, reusing connection", "socket", c.socket)
			return nil
		}
		// Connection is invalid, close it and create a new one
		c.logger.DebugContext(
			c.ctx,
			"containerd connection invalid, reconnecting",
			"socket",
			c.socket,
			"error",
			err,
		)
		_ = c.closeLocked() // Close the invalid connection (lock already held)
	}

	// Create new connection
	var err error
	c.cClient, err = containerd.New(c.socket)
	if err != nil {
		c.logger.Error("failed to connect to containerd: %v", "err", fmt.Sprintf("%v", err))
		return err
	}

	// Verify the new connection works
	if err = c.verifyConnection(); err != nil {
		_ = c.closeLocked() // lock already held
		return fmt.Errorf("failed to verify new connection: %w", err)
	}

	c.logger.InfoContext(c.ctx, "connected to containerd", "socket", c.socket)
	return nil
}

func (c *client) Close() error {
	c.cClientMu.Lock()
	defer c.cClientMu.Unlock()
	return c.closeLocked()
}

// conn returns the current containerd client snapshotted under cClientMu, so a
// reader observes a consistent pointer even while Connect()'s reconnect path is
// concurrently swapping c.cClient (issue #709 — #695 guarded only the writer).
// The lock is held just for the pointer read; the snapshot is then used for the
// whole operation, so the reader path is never serialized against an in-flight
// RPC and a concurrent reconnect cannot tear an operation already dereferencing
// the prior pointer. Returns nil if the client has not connected yet — callers
// that have not already gated on Connect() must nil-check.
func (c *client) conn() *containerd.Client {
	c.cClientMu.Lock()
	defer c.cClientMu.Unlock()
	return c.cClient
}

// closeLocked tears down c.cClient. The caller must hold c.cClientMu — it is
// invoked both by the public Close() (which takes the lock) and by Connect()'s
// reconnect path (which already holds the lock), so the teardown stays guarded
// without re-entrant locking.
func (c *client) closeLocked() error {
	if c.cClient != nil {
		err := c.cClient.Close()
		if err != nil {
			c.logger.Error("failed to close containerd client: %v", "err", fmt.Sprintf("%v", err))
			// Still set to nil even if close failed
			c.cClient = nil
			return err
		}
		c.logger.InfoContext(c.ctx, "closed containerd client")
		c.cClient = nil
	}
	return nil
}
