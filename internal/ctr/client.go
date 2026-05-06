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
	cClient              *containerd.Client
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
	CreateContainerFromSpec(namespace string, spec intmodel.ContainerSpec, opts ...BuildOption) (containerd.Container, error)

	CreateContainer(namespace string, spec ContainerSpec) (containerd.Container, error)
	GetContainer(namespace, id string) (containerd.Container, error)
	ListContainers(namespace string, filters ...string) ([]containerd.Container, error)
	ExistsContainer(namespace, id string) (bool, error)
	DeleteContainer(namespace, id string, opts ContainerDeleteOptions) error
	StartContainer(namespace string, spec ContainerSpec, taskSpec TaskSpec) (containerd.Task, error)
	StopContainer(namespace, id string, opts StopContainerOptions) (*containerd.ExitStatus, error)

	TaskStatus(namespace, id string) (containerd.Status, error)
	TaskMetrics(namespace, id string) (*apitypes.Metric, error)

	ResolveSbshCachePath(namespace, imageRef, baseRunPath string) (string, error)

	// ContainerProcessUID returns the resolved process.User.UID from the
	// given container's OCI runtime spec. Used after CreateContainerFromSpec
	// to chown the host-side per-container Attachable tty directory to the
	// runtime uid the container will execute as — which can be non-root
	// when the image carries a USER directive (or the cell profile sets
	// container.user). Without this, sbsh inside the container fails to
	// create its socket/log/capture files in the bind-mounted dir.
	ContainerProcessUID(container containerd.Container) (uint32, error)

	// LoadImage imports an OCI/docker image tarball into the specified
	// containerd namespace and returns the names of the imported images.
	LoadImage(namespace string, reader io.Reader) ([]string, error)

	// ListImages enumerates images in the specified containerd namespace.
	ListImages(namespace string) ([]ImageInfo, error)

	// GetImage returns metadata for the named image ref in the specified
	// containerd namespace. Returns errdefs.ErrImageNotFound if
	// the ref is absent.
	GetImage(namespace, ref string) (ImageInfo, error)

	// DeleteImage removes the named image ref from the specified
	// containerd namespace. Returns errdefs.ErrImageNotFound if the ref
	// is absent so callers can distinguish missing from operational
	// failures.
	DeleteImage(namespace, ref string) error
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
		_ = c.Close() // Close the invalid connection
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
		_ = c.Close()
		return fmt.Errorf("failed to verify new connection: %w", err)
	}

	c.logger.InfoContext(c.ctx, "connected to containerd", "socket", c.socket)
	return nil
}

func (c *client) Close() error {
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
