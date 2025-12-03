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
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	cgroupstats "github.com/containerd/cgroups/v2/cgroup2/stats"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// TODO(eminwux): add cgroup integration tests once CI exposes a writable cgroup v2 hierarchy.

// NewCgroup provisions a new cgroup for the provided spec.
func (c *client) NewCgroup(spec CgroupSpec) (*cgroup2.Manager, error) {
	if err := validateGroupPath(spec.Group); err != nil {
		return nil, err
	}
	resources, err := spec.Resources.toResources()
	if err != nil {
		return nil, err
	}
	mp := c.effectiveMountpoint(spec.Mountpoint)
	manager, err := cgroup2.NewManager(mp, spec.Group, resources)
	if err != nil {
		return nil, err
	}
	c.storeManager(spec.Group, manager)

	c.logger.InfoContext(c.ctx, "created cgroup", "group", spec.Group, "mountpoint", mp)
	return manager, nil
}

// LoadCgroup attaches to an existing cgroup path.
func (c *client) LoadCgroup(group string, mountpoint string) (*cgroup2.Manager, error) {
	if err := validateGroupPath(group); err != nil {
		return nil, err
	}
	mp := c.effectiveMountpoint(mountpoint)

	// Verify the cgroup directory actually exists
	cgroupPath := filepath.Join(mp, strings.TrimPrefix(group, "/"))
	if _, err := os.Stat(cgroupPath); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("cgroup path does not exist")
		}
		return nil, err
	}

	// Verify it's actually a cgroup by checking for cgroup.controllers file
	controllersPath := filepath.Join(cgroupPath, "cgroup.controllers")
	if _, err := os.Stat(controllersPath); err != nil {
		if os.IsNotExist(err) {
			return nil, errors.New("cgroup.controllers file not found, path is not a valid cgroup")
		}
		return nil, err
	}

	manager, err := cgroup2.LoadManager(mp, group)
	if err != nil {
		return nil, err
	}
	c.storeManager(group, manager)

	c.logger.InfoContext(c.ctx, "loaded cgroup", "group", group, "mountpoint", mp)
	return manager, nil
}

// GetCgroupMountpoint returns the discovered cgroup mountpoint.
func (c *client) GetCgroupMountpoint() string {
	return c.effectiveMountpoint("")
}

// GetCurrentCgroupPath returns the current process's cgroup path from /proc/self/cgroup.
func (c *client) GetCurrentCgroupPath() (string, error) {
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: hierarchy:controllers:path
		// For cgroup2 (unified hierarchy): 0::<path> where controllers is empty
		parts := strings.Split(line, ":")
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			return parts[2], nil
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		return "", scanErr
	}

	return "", errors.New("cgroup2 entry not found in /proc/self/cgroup")
}

// CgroupPath returns the absolute path under the unified hierarchy for this cgroup.
func (c *client) CgroupPath(group, mountpoint string) (string, error) {
	if err := validateGroupPath(group); err != nil {
		return "", err
	}
	return filepath.Join(c.effectiveMountpoint(mountpoint), strings.TrimPrefix(group, "/")), nil
}

// UpdateCgroup applies the provided resource changes.
func (c *client) UpdateCgroup(group, mountpoint string, resources CgroupResources) error {
	res, err := resources.toResources()
	if err != nil {
		return err
	}
	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return err
	}
	return manager.Update(res)
}

// AddProcessToCgroup adds the pid into cgroup.procs.
func (c *client) AddProcessToCgroup(group, mountpoint string, pid int) error {
	if pid <= 0 {
		return errdefs.ErrInvalidPID
	}
	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return err
	}
	return manager.AddProc(uint64(pid))
}

// DeleteCgroup removes the cgroup. It will fail if processes are still attached.
// If the cgroup doesn't exist, it returns nil (idempotent operation).
func (c *client) DeleteCgroup(group, mountpoint string) error {
	if err := validateGroupPath(group); err != nil {
		return err
	}

	// Check if cgroup exists before trying to delete it (idempotent operation)
	mp := c.effectiveMountpoint(mountpoint)
	cgroupPath := filepath.Join(mp, strings.TrimPrefix(group, "/"))
	if _, err := os.Stat(cgroupPath); err != nil {
		if os.IsNotExist(err) {
			// Cgroup doesn't exist, treat as success (already deleted)
			return nil
		}
		return err
	}

	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return err
	}
	err = manager.Delete()
	if err != nil {
		return err
	}
	c.dropManager(group)
	return nil
}

// CgroupMetrics returns the live controller metrics gathered from the cgroup hierarchy.
func (c *client) CgroupMetrics(group, mountpoint string) (*cgroupstats.Metrics, error) {
	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return nil, err
	}
	return manager.Stat()
}

func (c *client) effectiveMountpoint(mountpoint string) string {
	if mountpoint != "" {
		return mountpoint
	}
	// Use sync.Once to discover and cache the mountpoint
	c.cgroupMountpointOnce.Do(func() {
		// Ensure we have a valid context and logger for discovery
		ctx := c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		logger := c.logger

		c.cgroupMountpoint, c.cgroupMountpointErr = discoverCgroupMountpoint(ctx, logger)
		// discoverCgroupMountpoint always returns a mountpoint (never an error),
		// but ensure we have a valid mountpoint as a safety check
		if c.cgroupMountpoint == "" {
			if logger != nil {
				logger.WarnContext(
					ctx,
					"cgroup mountpoint discovery returned empty, using fallback",
					"fallback",
					consts.CgroupFilesystemPath,
					"error",
					c.cgroupMountpointErr,
				)
			}
			c.cgroupMountpoint = consts.CgroupFilesystemPath
		}
		// Log the discovered mountpoint for debugging
		if logger != nil {
			if c.cgroupMountpointErr != nil {
				logger.WarnContext(
					ctx,
					"cgroup mountpoint discovery encountered an error but using result anyway",
					"mountpoint",
					c.cgroupMountpoint,
					"error",
					c.cgroupMountpointErr,
				)
			} else {
				logger.DebugContext(
					ctx,
					"cgroup mountpoint discovered and cached",
					"mountpoint",
					c.cgroupMountpoint,
				)
			}
		}
	})
	// Final safety check: ensure we never return an empty mountpoint
	if c.cgroupMountpoint == "" {
		return consts.CgroupFilesystemPath
	}
	return c.cgroupMountpoint
}

func (c *client) storeManager(group string, manager *cgroup2.Manager) {
	if c.cgroups == nil {
		c.cgroups = make(map[string]*cgroup2.Manager)
	}
	c.cgroups[group] = manager
}

func (c *client) dropManager(group string) {
	if c.cgroups == nil {
		return
	}
	delete(c.cgroups, group)
}

func (c *client) managerFor(group, mountpoint string) (*cgroup2.Manager, error) {
	if err := validateGroupPath(group); err != nil {
		return nil, err
	}
	if manager, ok := c.cgroups[group]; ok {
		return manager, nil
	}
	mp := c.effectiveMountpoint(mountpoint)
	manager, err := cgroup2.LoadManager(mp, group)
	if err != nil {
		return nil, err
	}
	c.storeManager(group, manager)
	return manager, nil
}

func validateGroupPath(group string) error {
	if group == "" {
		return errdefs.ErrEmptyGroupPath
	}
	return cgroup2.VerifyGroupPath(group)
}
