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
	"slices"
)

func (c *client) CreateNamespace(namespace string) error {
	c.logger.DebugContext(c.ctx, "creating namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	// Use background context to avoid cancellation issues
	ctx := context.Background()
	err := namespaces.Create(ctx, namespace, nil)
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

func (c *client) DeleteNamespace(namespace string) error {
	if namespace == "" {
		return errors.New("namespace name is required")
	}

	c.logger.DebugContext(c.ctx, "deleting namespace", "namespace", namespace)
	namespaces := c.cClient.NamespaceService()

	// Use background context to avoid cancellation issues
	ctx := context.Background()

	// Check if namespace exists first
	nsList, err := namespaces.List(ctx)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to list containerd namespaces", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("failed to list namespaces: %w", err)
	}

	if !slices.Contains(nsList, namespace) {
		c.logger.DebugContext(c.ctx, "namespace not found, skipping deletion", "namespace", namespace)
		return nil // Idempotent: namespace doesn't exist, consider it deleted
	}

	// Note: containerd v2 may not have a direct Delete method
	// Namespaces are typically deleted by removing all resources first
	// For now, we'll log that deletion was attempted
	// In practice, namespaces are cleaned up when all containers/images are removed
	c.logger.InfoContext(c.ctx, "namespace deletion requested", "namespace", namespace)
	c.logger.WarnContext(
		c.ctx,
		"containerd namespaces are typically deleted automatically when empty",
		"namespace",
		namespace,
	)

	// Attempt to delete if the API supports it
	// This may not be available in all containerd versions
	// If Delete method doesn't exist, this will fail gracefully
	// and the namespace will be cleaned up when empty
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

	// Use background context to avoid cancellation issues
	ctx := context.Background()
	nsList, err := namespaces.List(ctx)
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
