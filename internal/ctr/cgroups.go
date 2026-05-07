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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cgroup2 "github.com/containerd/cgroups/v2/cgroup2"
	cgroupstats "github.com/containerd/cgroups/v2/cgroup2/stats"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// bootstrapLeafName is the leaf cgroup the defensive drain in
// applySubtreeControllers relocates the cgroup-namespace root's processes
// into when widening subtree_control would otherwise hit the no-internal-
// process rule. Kept distinct from _payload (the cell-startup leaf) so
// post-mortem inspection of /sys/fs/cgroup can tell the two layers apart.
const bootstrapLeafName = "__bootstrap"

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

// GetCurrentCgroupPath returns the kukeon root cgroup path, which is the base
// under which all realms (and the full kukeon hierarchy) live.
func (c *client) GetCurrentCgroupPath() (string, error) {
	return consts.KukeonCgroupRoot, nil
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

// RelocateProcessesToLeaf drains every PID currently in <group>/cgroup.procs
// into a freshly-mkdir'd leaf cgroup at <group>/<leaf>. Used by both the
// cell-startup structural fix and the applySubtreeControllers defensive
// drain to satisfy cgroup-v2's no-internal-process rule before the kernel
// is asked to enable a non-thread-aware controller in <group>'s
// subtree_control. Idempotent: re-running on an already-drained group is a
// no-op (mkdir tolerates existing leaf, the procs read returns no PIDs).
//
// The leaf scope inherits the parent's controllers via the parent's
// subtree_control walk that runs after the drain, so resource accounting
// applied at <group> still constrains the leaf — the relocation moves
// where PIDs live in the cgroup tree, not which limits apply to them.
func (c *client) RelocateProcessesToLeaf(group, mountpoint, leaf string) error {
	if err := validateGroupPath(group); err != nil {
		return err
	}
	if leaf == "" || strings.ContainsAny(leaf, "/") || leaf == "." || leaf == ".." {
		return errdefs.ErrInvalidLeafName
	}

	mp := c.effectiveMountpoint(mountpoint)
	groupPath := filepath.Join(mp, strings.TrimPrefix(group, "/"))
	leafPath := filepath.Join(groupPath, leaf)

	if err := os.Mkdir(leafPath, 0o750); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create leaf cgroup %s: %w", leafPath, err)
	}

	pids, err := readCgroupProcs(filepath.Join(groupPath, "cgroup.procs"))
	if err != nil {
		return fmt.Errorf("read cgroup.procs in %s: %w", groupPath, err)
	}
	if len(pids) == 0 {
		return nil
	}

	leafProcs := filepath.Join(leafPath, "cgroup.procs")
	if writeErr := writeCgroupProcs(leafProcs, pids); writeErr != nil {
		return fmt.Errorf("write cgroup.procs in %s: %w", leafPath, writeErr)
	}

	c.logger.InfoContext(c.ctx, "relocated processes to leaf cgroup",
		"group", group, "leaf", leaf, "pids", len(pids))
	return nil
}

// EnsureSubtreeControllers writes "+<ctrl>" to every ancestor's
// cgroup.subtree_control file (root → group's parent) AND to the group's own
// cgroup.subtree_control. The library's Manager.ToggleControllers handles the
// ancestor walk by design but skips the group itself ("the leaf does not need
// it") — for kukeon, every level (realm, space, stack, cell) is *not* a leaf,
// since each has descendants nested under it (issue #327, generalising the
// cell-only case from issue #312), so the group's own subtree_control must
// also be populated for those children to inherit the controllers.
//
// Filters the requested set against what the host root cgroup advertises so
// callers can pass the desired superset without worrying about kernel
// configuration variance (e.g. the io controller is missing on hosts without
// blk-cgroup compiled in). Returns the effective set actually written.
//
// The behaviour is idempotent — re-running on an already-delegated subtree
// is a no-op for the kernel (additive "+ctrl" writes), so callers can use
// it both on first provision and on every ensure-pass.
//
// An empty `controllers` slice short-circuits with (nil, nil): no validation
// work, no kernel writes. Callers in provision.go always pass
// cgroupcheck.CellResourceControllers() so this branch only triggers on the
// degenerate empty-slice input.
func (c *client) EnsureSubtreeControllers(group, mountpoint string, controllers []string) ([]string, error) {
	if err := validateGroupPath(group); err != nil {
		return nil, err
	}
	if len(controllers) == 0 {
		return nil, nil
	}

	mp := c.effectiveMountpoint(mountpoint)
	available, err := readRootControllers(mp)
	if err != nil {
		return nil, fmt.Errorf("read root cgroup.controllers: %w", err)
	}
	enable := intersectControllers(controllers, available)
	if applyErr := c.applySubtreeControllers(group, mp, enable); applyErr != nil {
		return nil, applyErr
	}
	return enable, nil
}

// EnableCellSubtreeControllers is a thin wrapper around
// EnsureSubtreeControllers for the cell create/ensure call sites that pass
// the kukeon resource subset (cgroupcheck.CellResourceControllers). Issue
// #312. Returns the effective set EnsureSubtreeControllers wrote so the
// runner can stash it on CellStatus.SubtreeControllers (issue #328).
func (c *client) EnableCellSubtreeControllers(group, mountpoint string, controllers []string) ([]string, error) {
	return c.EnsureSubtreeControllers(group, mountpoint, controllers)
}

// EnableCellAllSubtreeControllers is the cell/profile=NestedCgroupRuntime
// path: it delegates the *full* host-available cgroup-v2 controller set on
// the cell's subtree_control (and every ancestor's), so a nested cgroup
// runtime running inside the cell — e.g. an inner containerd or systemd
// hosting its own children in sub-cgroups — can in turn delegate any
// controller it wants. Issue #314.
//
// The ordinary cell path (EnableCellSubtreeControllers with the kukeon
// resource subset) is what every kukeon-managed cell wants by default; the
// "all" variant is the explicit opt-in cells request when they host a
// nested runtime that needs more than the resource subset.
func (c *client) EnableCellAllSubtreeControllers(group, mountpoint string) ([]string, error) {
	if err := validateGroupPath(group); err != nil {
		return nil, err
	}
	mp := c.effectiveMountpoint(mountpoint)
	available, err := readRootControllers(mp)
	if err != nil {
		return nil, fmt.Errorf("read root cgroup.controllers: %w", err)
	}
	if applyErr := c.applySubtreeControllers(group, mp, available); applyErr != nil {
		return nil, applyErr
	}
	return available, nil
}

// applySubtreeControllers is the shared body of EnsureSubtreeControllers and
// EnableCellAllSubtreeControllers. It assumes group has already been
// validated and that controllers has been pre-filtered against the host
// root's cgroup.controllers, so every entry is known-supported by the
// running kernel.
func (c *client) applySubtreeControllers(group, mountpoint string, enable []string) error {
	if len(enable) == 0 {
		return nil
	}

	// Defensive no-internal-process drain (issue #336 scenario B). When kuke
	// is invoked from inside a cgroup-namespace whose root cgroup carries
	// processes (the shell, the kuke binary, anything else spawned by
	// runc-exec into the cell's container-root cgroup directly), the
	// ancestor walk below would EBUSY at the cgroup-ns root because
	// cgroup-v2 forbids enabling non-thread-aware controllers in the
	// subtree_control of a cgroup that hosts processes. Both clauses are
	// required: /proc/self/cgroup == 0::/ guards against accidentally
	// draining systemd's processes on a non-nested host (where the path
	// shows the systemd slice, not the bare 0::/), and the populated check
	// avoids the no-op mkdir+read on an empty root.
	if drainErr := c.maybeDrainCgroupNsRoot(mountpoint); drainErr != nil {
		return fmt.Errorf("drain cgroup-ns root before subtree_control widening: %w", drainErr)
	}

	manager, err := c.managerFor(group, mountpoint)
	if err != nil {
		return err
	}
	if toggleErr := manager.ToggleControllers(enable, cgroup2.Enable); toggleErr != nil {
		return fmt.Errorf("enable controllers in cgroup ancestors: %w", toggleErr)
	}

	groupPath := filepath.Join(mountpoint, strings.TrimPrefix(group, "/"))
	if writeErr := writeSubtreeEnable(filepath.Join(groupPath, "cgroup.subtree_control"), enable); writeErr != nil {
		return fmt.Errorf("enable controllers in cgroup subtree_control: %w", writeErr)
	}

	c.logger.InfoContext(c.ctx, "enabled cgroup subtree controllers",
		"group", group, "mountpoint", mountpoint, "controllers", enable)
	return nil
}

func readRootControllers(mountpoint string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(mountpoint, "cgroup.controllers"))
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}

func intersectControllers(want, have []string) []string {
	haveSet := make(map[string]struct{}, len(have))
	for _, h := range have {
		haveSet[h] = struct{}{}
	}
	out := make([]string, 0, len(want))
	for _, w := range want {
		if _, ok := haveSet[w]; ok {
			out = append(out, w)
		}
	}
	return out
}

// readCgroupProcs reads the PIDs from a cgroup.procs file. Empty lines are
// skipped. Returns nil + nil if the file is empty.
func readCgroupProcs(path string) ([]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var pids []int
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var pid int
		if _, parseErr := fmt.Sscanf(line, "%d", &pid); parseErr != nil {
			return nil, fmt.Errorf("parse pid %q in %s: %w", line, path, parseErr)
		}
		pids = append(pids, pid)
	}
	if scanErr := scanner.Err(); scanErr != nil {
		return nil, scanErr
	}
	return pids, nil
}

// writeCgroupProcs writes each PID into the target cgroup.procs file. The
// kernel accepts one PID per write; batching them into a single write would
// be rejected. Failures on individual PIDs are returned immediately so the
// caller can decide whether the partial state is acceptable.
func writeCgroupProcs(path string, pids []int) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, pid := range pids {
		if _, writeErr := fmt.Fprintf(f, "%d\n", pid); writeErr != nil {
			return fmt.Errorf("write pid %d: %w", pid, writeErr)
		}
	}
	return nil
}

// maybeDrainCgroupNsRoot drains the cgroup-namespace root cgroup's procs
// into a __bootstrap leaf when both gating clauses hold:
//   - /proc/self/cgroup reports 0::/ (we are at the cgroup-ns root, not on
//     a non-nested host where systemd's slice path would show)
//   - the mountpoint root cgroup.events shows populated 1 (there are
//     processes whose presence would EBUSY a subtree_control widening)
//
// Either clause failing short-circuits with nil — this is a defensive
// primitive that must be a no-op on every call site that does not match
// the cgroup-namespace-trap fingerprint.
func (c *client) maybeDrainCgroupNsRoot(mountpoint string) error {
	atRoot, err := procSelfCgroupAtRoot()
	if err != nil || !atRoot {
		return err
	}
	mp := c.effectiveMountpoint(mountpoint)
	populated, err := cgroupPopulated(mp)
	if err != nil || !populated {
		return err
	}
	return c.RelocateProcessesToLeaf("/", mp, bootstrapLeafName)
}

// procSelfCgroupAtRoot reports whether the calling process's
// /proc/self/cgroup is the unified-hierarchy root entry "0::/", i.e. the
// process sits at its cgroup namespace's root. A non-zero hierarchy id or
// any path past "/" means the process is in a deeper cgroup and the
// no-internal-process trap does not apply at the mountpoint root.
func procSelfCgroupAtRoot() (bool, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return false, fmt.Errorf("read /proc/self/cgroup: %w", err)
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "0::/" {
			return true, nil
		}
	}
	return false, nil
}

// cgroupPopulated reports whether the cgroup at the given path has its
// "populated 1" flag set in cgroup.events. Returns false (no error) when
// the file does not exist, so non-cgroupfs paths short-circuit cleanly.
func cgroupPopulated(cgroupPath string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(cgroupPath, "cgroup.events"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) == "populated 1" {
			return true, nil
		}
	}
	return false, nil
}

func writeSubtreeEnable(path string, controllers []string) error {
	parts := make([]string, len(controllers))
	for i, c := range controllers {
		parts[i] = "+" + c
	}
	// Mirror cgroup2.Manager.writeSubtreeControl: O_WRONLY without O_TRUNC.
	// cgroupfs files don't honor truncation; the write itself is interpreted
	// additively (kernel applies "+cpu -io" line-by-line).
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(strings.Join(parts, " "))
	return err
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
