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
)

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
func (c *client) loadContainer(id string) (containerd.Container, error) {
	c.containersMu.RLock()
	if container, ok := c.containers[id]; ok {
		c.containersMu.RUnlock()
		return container, nil
	}
	c.containersMu.RUnlock()

	nsCtx := c.namespaceCtx()
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
func (c *client) loadTask(id string) (containerd.Task, error) {
	c.tasksMu.RLock()
	if task, ok := c.tasks[id]; ok {
		c.tasksMu.RUnlock()
		return task, nil
	}
	c.tasksMu.RUnlock()

	container, err := c.loadContainer(id)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx()
	task, err := container.Task(nsCtx, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTaskNotFound, err)
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
