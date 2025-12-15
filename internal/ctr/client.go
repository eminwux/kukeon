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
	"log/slog"
	"sync"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

type client struct {
	ctx                   context.Context
	logger                *slog.Logger
	socket                string
	cClient               *containerd.Client
	namespace             string
	namespaceMu           sync.RWMutex
	registryCredentials   []RegistryCredentials
	registryCredentialsMu sync.RWMutex
	cgroups               map[string]*cgroup2.Manager
	containersMu          sync.RWMutex
	containers            map[string]containerd.Container
	tasksMu               sync.RWMutex
	tasks                 map[string]containerd.Task
	cgroupMountpointOnce  sync.Once
	cgroupMountpoint      string
	cgroupMountpointErr   error
}

type Client interface {
	Connect() error
	Close() error

	Namespace() string
	CreateNamespace(namespace string) error
	DeleteNamespace(namespace string) error
	ListNamespaces() ([]string, error)
	GetNamespace(namespace string) (string, error)
	ExistsNamespace(namespace string) (bool, error)
	SetNamespace(namespace string)
	SetNamespaceWithCredentials(namespace string, creds []RegistryCredentials)
	CleanupNamespaceResources(namespace, snapshotter string) error
	GetRegistryCredentials() []RegistryCredentials

	GetCgroupMountpoint() string
	GetCurrentCgroupPath() (string, error)
	CgroupPath(group, mountpoint string) (string, error)
	NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error)
	LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error)
	DeleteCgroup(group, mountpoint string) error
	CreateContainerFromSpec(intmodel.ContainerSpec) (containerd.Container, error)

	CreateContainer(spec ContainerSpec) (containerd.Container, error)
	GetContainer(id string) (containerd.Container, error)
	ListContainers(filters ...string) ([]containerd.Container, error)
	ExistsContainer(id string) (bool, error)
	DeleteContainer(id string, opts ContainerDeleteOptions) error
	StartContainer(spec ContainerSpec, taskSpec TaskSpec) (containerd.Task, error)
	StopContainer(id string, opts StopContainerOptions) (*containerd.ExitStatus, error)

	TaskStatus(id string) (containerd.Status, error)
	TaskMetrics(id string) (*apitypes.Metric, error)
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
