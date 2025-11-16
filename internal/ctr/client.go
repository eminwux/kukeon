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
	containerd "github.com/containerd/containerd/v2/client"
)

type client struct {
	ctx                  context.Context
	logger               *slog.Logger
	socket               string
	cClient              *containerd.Client
	cgroups              map[string]*cgroup2.Manager
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
	NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error)
	LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error)
}

func NewClient(ctx context.Context, logger *slog.Logger, socket string) Client {
	return &client{
		ctx:     ctx,
		logger:  logger,
		socket:  socket,
		cgroups: make(map[string]*cgroup2.Manager),
	}
}

func (c *client) Connect() error {
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
			return err
		}
		c.logger.InfoContext(c.ctx, "closed containerd client")
	}
	return nil
}
