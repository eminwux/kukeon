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
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	cgroupstats "github.com/containerd/cgroups/v2/cgroup2/stats"
)

// TODO(eminwux): add cgroup integration tests once CI exposes a writable cgroup v2 hierarchy.

var (
	errEmptyGroupPath   = errors.New("ctr: cgroup group path is required")
	errInvalidPID       = errors.New("ctr: pid must be greater than zero")
	errInvalidCPUWeight = errors.New("ctr: cpu weight must be within [1, 10000]")
	errInvalidIOWeight  = errors.New("ctr: io weight must be within [1, 1000]")
	errInvalidThrottle  = errors.New("ctr: io throttle entries require type, major, minor and rate")
)

// CgroupSpec describes how to create a new cgroup.
type CgroupSpec struct {
	// Group is the target cgroup path, e.g. /kukeon/workloads/runner.
	Group string
	// Mountpoint overrides the default cgroup mount (/sys/fs/cgroup) when non-empty.
	Mountpoint string
	// Resources defines the controller knobs that should be configured for the cgroup.
	Resources CgroupResources
}

// CgroupResources represents the subset of controllers we expose.
type CgroupResources struct {
	CPU    *CPUResources
	Memory *MemoryResources
	IO     *IOResources
}

// CPUResources maps to cpu*, cpuset* controllers.
type CPUResources struct {
	Weight *uint64
	Quota  *int64
	Period *uint64
	Cpus   string
	Mems   string
}

// MemoryResources maps to memory controller knobs.
type MemoryResources struct {
	Min  *int64
	Max  *int64
	Low  *int64
	High *int64
	Swap *int64
}

// IOResources exposes IO weight + throttling.
type IOResources struct {
	Weight   uint16
	Throttle []IOThrottleEntry
}

// IOThrottleType identifies the throttle file to target.
type IOThrottleType string

const (
	IOTypeReadBPS   IOThrottleType = IOThrottleType(cgroup2.ReadBPS)
	IOTypeWriteBPS  IOThrottleType = IOThrottleType(cgroup2.WriteBPS)
	IOTypeReadIOPS  IOThrottleType = IOThrottleType(cgroup2.ReadIOPS)
	IOTypeWriteIOPS IOThrottleType = IOThrottleType(cgroup2.WriteIOPS)
)

// IOThrottleEntry represents a single io.max entry.
type IOThrottleEntry struct {
	Type  IOThrottleType
	Major int64
	Minor int64
	Rate  uint64
}

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
		return errInvalidPID
	}
	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return err
	}
	return manager.AddProc(uint64(pid))
}

// DeleteCgroup removes the cgroup. It will fail if processes are still attached.
func (c *client) DeleteCgroup(group, mountpoint string) error {
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

func (r CgroupResources) toResources() (*cgroup2.Resources, error) {
	resources := &cgroup2.Resources{}

	if r.CPU != nil && !r.CPU.isZero() {
		cpu, err := r.CPU.toResource()
		if err != nil {
			return nil, err
		}
		resources.CPU = cpu
	}

	if r.Memory != nil && !r.Memory.isZero() {
		resources.Memory = r.Memory.toResource()
	}

	if r.IO != nil && !r.IO.isZero() {
		io, err := r.IO.toResource()
		if err != nil {
			return nil, err
		}
		resources.IO = io
	}

	return resources, nil
}

func (c *CPUResources) isZero() bool {
	if c == nil {
		return true
	}
	return c.Weight == nil &&
		c.Quota == nil &&
		c.Period == nil &&
		c.Cpus == "" &&
		c.Mems == ""
}

func (c *CPUResources) toResource() (*cgroup2.CPU, error) {
	if c.Weight != nil {
		if *c.Weight < 1 || *c.Weight > 10000 {
			return nil, errInvalidCPUWeight
		}
	}
	cpu := &cgroup2.CPU{
		Weight: c.Weight,
		Cpus:   c.Cpus,
		Mems:   c.Mems,
	}
	if c.Quota != nil || c.Period != nil {
		cpu.Max = cgroup2.NewCPUMax(c.Quota, c.Period)
	}
	return cpu, nil
}

func (m *MemoryResources) isZero() bool {
	if m == nil {
		return true
	}
	return m.Min == nil &&
		m.Max == nil &&
		m.Low == nil &&
		m.High == nil &&
		m.Swap == nil
}

func (m *MemoryResources) toResource() *cgroup2.Memory {
	return &cgroup2.Memory{
		Min:  m.Min,
		Max:  m.Max,
		Low:  m.Low,
		High: m.High,
		Swap: m.Swap,
	}
}

func (io *IOResources) isZero() bool {
	if io == nil {
		return true
	}
	return io.Weight == 0 && len(io.Throttle) == 0
}

func (io *IOResources) toResource() (*cgroup2.IO, error) {
	if io.Weight != 0 && (io.Weight < 1 || io.Weight > 1000) {
		return nil, errInvalidIOWeight
	}
	maxEntries := make([]cgroup2.Entry, 0, len(io.Throttle))
	for _, entry := range io.Throttle {
		if entry.Type == "" || entry.Rate == 0 || entry.Major < 0 || entry.Minor < 0 {
			return nil, errInvalidThrottle
		}
		maxEntries = append(maxEntries, cgroup2.Entry{
			Type:  cgroup2.IOType(entry.Type),
			Major: entry.Major,
			Minor: entry.Minor,
			Rate:  entry.Rate,
		})
	}
	return &cgroup2.IO{
		BFQ: cgroup2.BFQ{Weight: io.Weight},
		Max: maxEntries,
	}, nil
}

func validateGroupPath(group string) error {
	if group == "" {
		return errEmptyGroupPath
	}
	return cgroup2.VerifyGroupPath(group)
}

func parseMountinfo(ctx context.Context, logger *slog.Logger) (string, error) {
	// Read /proc/self/mountinfo to find cgroup2 mount point
	if logger != nil {
		logger.DebugContext(ctx, "parsing /proc/self/mountinfo to find cgroup2 mount")
	}
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		if logger != nil {
			logger.DebugContext(ctx, "failed to open /proc/self/mountinfo", "error", err)
		}
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: space-separated fields
		// Fields: mountID parentID major:minor root mountpoint mountopts - fstype source superopts
		// Example: 36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
		// We need to find the "-" separator, then fstype is the next field
		fields := strings.Fields(line)
		const minFieldsForMountinfo = 10
		const mountpointFieldIndex = 4
		if len(fields) < minFieldsForMountinfo {
			continue
		}

		// Find the "-" separator (optional fields marker)
		separatorIdx := -1
		for i, field := range fields {
			if field == "-" {
				separatorIdx = i
				break
			}
		}
		if separatorIdx == -1 || separatorIdx+1 >= len(fields) {
			continue
		}

		// Filesystem type is the field after "-"
		fstype := fields[separatorIdx+1]
		if fstype == "cgroup2" {
			// Mount point is field 4 (0-indexed: fields[4])
			if len(fields) > mountpointFieldIndex {
				mountpoint := fields[mountpointFieldIndex]
				if logger != nil {
					logger.DebugContext(ctx, "found cgroup2 mount in mountinfo", "mountpoint", mountpoint)
				}
				return mountpoint, nil
			}
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		if logger != nil {
			logger.DebugContext(ctx, "error scanning /proc/self/mountinfo", "error", scanErr)
		}
		return "", scanErr
	}

	if logger != nil {
		logger.DebugContext(ctx, "cgroup2 mount not found in mountinfo")
	}
	return "", errors.New("cgroup2 mount not found in mountinfo")
}

func discoverCgroupMountpoint(ctx context.Context, logger *slog.Logger) (string, error) {
	const fallbackMountpoint = "/sys/fs/cgroup"

	if logger != nil {
		logger.DebugContext(ctx, "discovering cgroup mountpoint from /proc/self/cgroup")
	}

	// Read /proc/self/cgroup to find the cgroup2 entry
	file, err := os.Open("/proc/self/cgroup")
	if err != nil {
		if logger != nil {
			logger.DebugContext(
				ctx,
				"failed to open /proc/self/cgroup, using fallback",
				"error",
				err,
				"fallback",
				fallbackMountpoint,
			)
		}
		// Fallback to default if we can't read the file
		return fallbackMountpoint, nil
	}
	defer file.Close()

	var cgroupPath string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: hierarchy:controllers:path
		// For cgroup2 (unified hierarchy): 0::<path> where controllers is empty
		parts := strings.Split(line, ":")
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			cgroupPath = parts[2]
			if logger != nil {
				logger.DebugContext(ctx, "found cgroup2 entry in /proc/self/cgroup", "cgroup_path", cgroupPath)
			}
			break
		}
	}

	if scanErr := scanner.Err(); scanErr != nil {
		if logger != nil {
			logger.DebugContext(
				ctx,
				"error scanning /proc/self/cgroup, using fallback",
				"error",
				scanErr,
				"fallback",
				fallbackMountpoint,
			)
		}
		// Fallback to default on scan error
		return fallbackMountpoint, nil
	}

	// If we didn't find a cgroup2 entry, fallback to default
	if cgroupPath == "" {
		if logger != nil {
			logger.DebugContext(
				ctx,
				"no cgroup2 entry found in /proc/self/cgroup, using fallback",
				"fallback",
				fallbackMountpoint,
			)
		}
		return fallbackMountpoint, nil
	}

	// Get cgroup2 mount point from mountinfo
	mountpoint, mountErr := parseMountinfo(ctx, logger)
	if mountErr != nil {
		if logger != nil {
			logger.DebugContext(
				ctx,
				"failed to find cgroup2 mountpoint in mountinfo, using fallback",
				"error",
				mountErr,
				"fallback",
				fallbackMountpoint,
			)
		}
		// Fallback to default if we can't find the mountpoint
		return fallbackMountpoint, nil
	}

	if logger != nil {
		logger.DebugContext(ctx, "discovered cgroup mountpoint", "mountpoint", mountpoint, "cgroup_path", cgroupPath)
	}
	return mountpoint, nil
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
					"/sys/fs/cgroup",
					"error",
					c.cgroupMountpointErr,
				)
			}
			c.cgroupMountpoint = "/sys/fs/cgroup"
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
		return "/sys/fs/cgroup"
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
