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
	"fmt"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
)

// cacheKey returns a unique key for a (namespace, id) pair.
func cacheKey(namespace, id string) string {
	return namespace + "/" + id
}

// storeContainer stores a container in the cache.
func (c *client) storeContainer(namespace, id string, container containerd.Container) {
	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	if c.containers == nil {
		c.containers = make(map[string]containerd.Container)
	}
	c.containers[cacheKey(namespace, id)] = container
}

// loadContainer loads a container from cache or containerd.
func (c *client) loadContainer(namespace, id string) (containerd.Container, error) {
	key := cacheKey(namespace, id)
	c.containersMu.RLock()
	if container, ok := c.containers[key]; ok {
		c.containersMu.RUnlock()
		return container, nil
	}
	c.containersMu.RUnlock()

	nsCtx := c.namespaceCtx(namespace)
	container, err := c.conn().LoadContainer(nsCtx, id)
	if err != nil {
		// Only wrap "not found" errors with ErrContainerNotFound
		// Other errors (connection failures, permission errors, etc.) should be returned as-is
		if errdefs.IsNotFound(err) {
			return nil, fmt.Errorf("%w: %w", internalerrdefs.ErrContainerNotFound, err)
		}
		return nil, err
	}
	c.storeContainer(namespace, id, container)
	return container, nil
}

// dropContainer removes a container from the cache.
func (c *client) dropContainer(namespace, id string) {
	c.containersMu.Lock()
	defer c.containersMu.Unlock()
	if c.containers != nil {
		delete(c.containers, cacheKey(namespace, id))
	}
}

// storeTask stores a task in the cache.
func (c *client) storeTask(namespace, id string, task containerd.Task) {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()
	if c.tasks == nil {
		c.tasks = make(map[string]containerd.Task)
	}
	c.tasks[cacheKey(namespace, id)] = task
}

// loadTask loads a task from cache or container.
func (c *client) loadTask(namespace, id string) (containerd.Task, error) {
	key := cacheKey(namespace, id)
	c.tasksMu.RLock()
	if task, ok := c.tasks[key]; ok {
		c.tasksMu.RUnlock()
		return task, nil
	}
	c.tasksMu.RUnlock()

	container, err := c.loadContainer(namespace, id)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx(namespace)
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", internalerrdefs.ErrTaskNotFound, err)
	}
	c.storeTask(namespace, id, task)
	return task, nil
}

// dropTask removes a task from the cache.
func (c *client) dropTask(namespace, id string) {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()
	if c.tasks != nil {
		delete(c.tasks, cacheKey(namespace, id))
	}
}
