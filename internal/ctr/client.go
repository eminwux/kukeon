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
	"slices"

	containerd "github.com/containerd/containerd/v2/client"
)

type client struct {
	ctx     context.Context
	logger  *slog.Logger
	socket  string
	cClient *containerd.Client
}

type Client interface {
	Connect() error
	Close() error
	CreateNamespace(namespace string) error
	DeleteNamespace(namespace string) error
	ListNamespaces() ([]string, error)
	GetNamespace(namespace string) (string, error)
	ExistsNamespace(namespace string) (bool, error)
}

func NewClient(ctx context.Context, logger *slog.Logger, socket string) Client {
	return &client{
		ctx:    ctx,
		logger: logger,
		socket: socket,
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

func (c *client) CreateNamespace(namespace string) error {
	c.logger.DebugContext(c.ctx, "creating namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	err := namespaces.Create(c.ctx, namespace, nil)
	if err != nil {
		c.logger.ErrorContext(
			c.ctx,
			"failed to create containerd namespace",
			"namespace",
			namespace,
			"err",
			fmt.Sprintf("%v", err),
		)
		return err
	}

	c.logger.InfoContext(c.ctx, "created containerd namespace", "namespace", namespace)
	return nil
}

func (c *client) DeleteNamespace(_ string) error {
	return nil
}

func (c *client) ListNamespaces() ([]string, error) {
	c.logger.DebugContext(c.ctx, "listing namespaces")

	namespaces := c.cClient.NamespaceService()
	// containerd requires a namespace for most API calls.
	// But listing namespaces does not require entering one.
	ctx := context.Background()

	nsList, err := namespaces.List(ctx)
	if err != nil {
		c.logger.Error("failed to list containerd namespaces: %v", "err", fmt.Sprintf("%v", err))
		return nil, err
	}

	return nsList, nil
}

func (c *client) ExistsNamespace(namespace string) (bool, error) {
	ns, err := c.GetNamespace(namespace)
	if err != nil {
		return false, err
	}
	return ns == namespace, nil
}

func (c *client) GetNamespace(namespace string) (string, error) {
	c.logger.DebugContext(c.ctx, "getting namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	nsList, err := namespaces.List(c.ctx)
	if err != nil {
		c.logger.Error("failed to list containerd namespaces: %v", "err", fmt.Sprintf("%v", err))
		return "", err
	}

	if slices.Contains(nsList, namespace) {
		c.logger.InfoContext(c.ctx, "namespace found", "namespace", namespace)
		return namespace, nil
	}

	c.logger.InfoContext(c.ctx, "namespace not found", "namespace", namespace)
	return "", nil
}

func (c *client) GetNetwork(namespace string, network string) (string, error) {
	c.logger.DebugContext(c.ctx, "getting network", "namespace", namespace, "network", network)

	return "", nil
}
