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

	apitypes "github.com/containerd/containerd/api/types"
	containerd "github.com/containerd/containerd/v2/client"
)

// TaskStatus returns the current status of a task.
func (c *client) TaskStatus(id string) (containerd.Status, error) {
	if id == "" {
		return containerd.Status{}, ErrEmptyContainerID
	}

	task, err := c.loadTask(id)
	if err != nil {
		return containerd.Status{}, err
	}

	nsCtx := c.namespaceCtx()
	status, err := task.Status(nsCtx)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to get task status", "id", id, "err", formatError(err))
		return containerd.Status{}, fmt.Errorf("failed to get task status: %w", err)
	}

	return status, nil
}

// TaskMetrics returns the metrics for a task.
func (c *client) TaskMetrics(id string) (*apitypes.Metric, error) {
	if id == "" {
		return nil, ErrEmptyContainerID
	}

	task, err := c.loadTask(id)
	if err != nil {
		return nil, err
	}

	nsCtx := c.namespaceCtx()
	metrics, err := task.Metrics(nsCtx)
	if err != nil {
		c.logger.ErrorContext(c.ctx, "failed to get task metrics", "id", id, "err", formatError(err))
		return nil, fmt.Errorf("failed to get task metrics: %w", err)
	}

	return metrics, nil
}
