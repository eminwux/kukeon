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
	"fmt"
	"log/slog"
	"sync"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type client struct {
	ctx                  context.Context
	logger               *slog.Logger
	socket               string
	cClient              *containerd.Client
	namespace            string
	namespaceMu          sync.RWMutex
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
	GetCgroupMountpoint() string
	GetCurrentCgroupPath() (string, error)
	CgroupPath(group, mountpoint string) (string, error)
	SetNamespace(namespace string)
	Namespace() string
	CreateContainer(ctx context.Context, spec ContainerSpec) (containerd.Container, error)
	GetContainer(ctx context.Context, id string) (containerd.Container, error)
	ListContainers(ctx context.Context, filters ...string) ([]containerd.Container, error)
	ExistsContainer(ctx context.Context, id string) (bool, error)
	DeleteContainer(ctx context.Context, id string, opts ContainerDeleteOptions) error
	StartContainer(ctx context.Context, spec ContainerSpec, taskSpec TaskSpec) (containerd.Task, error)
	StopContainer(ctx context.Context, id string, opts StopContainerOptions) (*containerd.ExitStatus, error)
	TaskStatus(ctx context.Context, id string) (containerd.Status, error)
	TaskMetrics(ctx context.Context, id string) (*apitypes.Metric, error)
	NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error)
	LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error)
	CreateContainerFromSpec(
		ctx context.Context,
		containerSpec *v1beta1.ContainerSpec,
	) (containerd.Container, error)
}

// NamespacePaths describes the namespace file paths a container should join.
type NamespacePaths struct {
	Net string
	IPC string
	UTS string
	PID string
}

func NewClient(ctx context.Context, logger *slog.Logger, socket string) Client {
	return &client{
		ctx:        ctx,
		logger:     logger,
		socket:     socket,
		namespace:  namespaces.Default,
		cgroups:    make(map[string]*cgroup2.Manager),
		containers: make(map[string]containerd.Container),
		tasks:      make(map[string]containerd.Task),
	}
}

func (c *client) Connect() error {
	// If already connected, check if connection is still valid
	// Only create a new connection if needed to avoid closing active connections
	if c.cClient != nil {
		// Try to use the existing connection - if it's closed, we'll get an error
		// and can then create a new one. For now, just reuse it.
		c.logger.DebugContext(c.ctx, "containerd client already connected, reusing connection", "socket", c.socket)
		return nil
	}

	var err error
	c.cClient, err = containerd.New(c.socket)
	if err != nil {
		c.logger.Error("failed to connect to containerd: %v", "err", fmt.Sprintf("%v", err))
		return err
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
