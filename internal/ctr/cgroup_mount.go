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
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
)

// parseMountinfo reads /proc/self/mountinfo to find the cgroup2 mount point.
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

// discoverCgroupMountpoint discovers the cgroup2 mountpoint by reading /proc/self/cgroup
// and /proc/self/mountinfo. Falls back to the default mountpoint if discovery fails.
func discoverCgroupMountpoint(ctx context.Context, logger *slog.Logger) (string, error) {
	const fallbackMountpoint = consts.CgroupFilesystemPath

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
