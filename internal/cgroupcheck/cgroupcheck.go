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

// Package cgroupcheck implements the host-cgroup pre-flight that catches
// missing or undelegated cgroup-v2 controllers before kukeon's bootstrap
// path tries (and fails) to enable them. Used by the `kuke doctor cgroups`
// subcommand and reused as the single source of truth for the cell
// resource-controller set kukeon enables on every cell's subtree.
//
// File-based classification reads <root>/cgroup.controllers (controllers
// the kernel exposes here) and <root>/cgroup.subtree_control (controllers
// already enabled into the subtree). A controller missing from
// subtree_control but present in cgroup.controllers usually only needs a
// "+<ctrl>" write to subtree_control; in cgroup-namespace contexts, that
// write can still fail with EOPNOTSUPP because the namespace's parent
// hasn't delegated the controller. The optional probe write disambiguates.
package cgroupcheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/eminwux/kukeon/internal/consts"
)

// CellResourceControllers names the cgroup-v2 controllers kukeon enables on
// a cell's subtree by default so per-container cgroups inherit them and
// cell-level UpdateCgroup limits are effective. Cells that opt into
// CellSpec.NestedCgroupRuntime delegate the full host-advertised set
// instead — see EnableCellAllSubtreeControllers in internal/ctr.
//
// Single source of truth: provision.go and the doctor pre-flight both
// consume this slice; mutating it would silently change the cell-creation
// path. Returning a fresh copy keeps callers honest.
func CellResourceControllers() []string {
	return []string{"cpu", "memory", "io", "pids"}
}

// Status classifies a single controller's availability at the host root.
type Status int

const (
	// StatusEnabled — already listed in cgroup.subtree_control. Nothing to do.
	StatusEnabled Status = iota
	// StatusKernelMissing — not listed in cgroup.controllers. Either the
	// kernel was built without the controller, or the parent in the cgroup
	// hierarchy hasn't delegated it down to this scope.
	StatusKernelMissing
	// StatusNotDelegated — listed in cgroup.controllers but the probe write
	// to cgroup.subtree_control returned EOPNOTSUPP. Diagnostic of the
	// cgroup-namespace trap: cgroup.controllers advertises kernel support
	// but the namespace's actual parent never delegated the controller.
	StatusNotDelegated
	// StatusThreadedSubtree — the probe write returned EOPNOTSUPP and
	// cgroup.type at this scope is "domain threaded" or "threaded", so the
	// kernel forbids enabling domain-only controllers (memory, io, ...) in
	// this subtree_control regardless of delegation. Distinct from
	// StatusNotDelegated because escalating to the parent runtime will not
	// help — the fix lives at the threaded descendant or in this cgroup's
	// own cgroup.type.
	StatusThreadedSubtree
	// StatusNeedsDelegation — listed in cgroup.controllers, missing from
	// cgroup.subtree_control, and we did not probe (or probing wasn't
	// permitted). The fix is "+<ctrl>" to cgroup.subtree_control, but the
	// pre-flight cannot promise the write will succeed.
	StatusNeedsDelegation
	// StatusEnabledByProbe — the probe write succeeded and the controller
	// is now in cgroup.subtree_control. Idempotent: re-running is a no-op.
	StatusEnabledByProbe
	// StatusPermissionDenied — the probe write returned EACCES/EPERM. The
	// pre-flight cannot tell whether the kernel would accept the write
	// from a privileged caller; re-run with sudo for a definitive answer.
	StatusPermissionDenied
)

// String returns the short label used in human-readable output.
func (s Status) String() string {
	switch s {
	case StatusEnabled:
		return "enabled"
	case StatusKernelMissing:
		return "kernel-missing"
	case StatusNotDelegated:
		return "not-delegated"
	case StatusThreadedSubtree:
		return "threaded-subtree"
	case StatusNeedsDelegation:
		return "needs-delegation"
	case StatusEnabledByProbe:
		return "enabled-by-probe"
	case StatusPermissionDenied:
		return "permission-denied"
	default:
		return fmt.Sprintf("status(%d)", int(s))
	}
}

// Result captures the outcome of a controller-set check, keyed by
// controller name. Required preserves the caller's input order so the
// remediation message lists controllers in the same sequence the
// caller asked about.
type Result struct {
	HostRoot string
	Required []string
	Status   map[string]Status
	// ProbeErr records the raw error from each probe attempt, when one
	// was made. Useful for verbose diagnostics; ignore when reading
	// Status alone is enough.
	ProbeErr map[string]error
}

// OK reports whether every required controller ended in a "good" terminal
// state — already enabled, or enabled by the probe.
func (r Result) OK() bool {
	return len(r.Unresolved()) == 0
}

// Unresolved returns the required controllers that still need operator
// attention after the check. Order matches Required.
func (r Result) Unresolved() []string {
	out := make([]string, 0, len(r.Required))
	for _, c := range r.Required {
		switch r.Status[c] {
		case StatusEnabled, StatusEnabledByProbe:
			// Good terminal state.
		case StatusKernelMissing,
			StatusNotDelegated,
			StatusThreadedSubtree,
			StatusNeedsDelegation,
			StatusPermissionDenied:
			out = append(out, c)
		}
	}
	return out
}

// ByStatus groups Required controllers by their classification, returning
// only statuses that have at least one entry. Slice order within each
// group matches Required.
func (r Result) ByStatus() map[Status][]string {
	groups := map[Status][]string{}
	for _, c := range r.Required {
		s := r.Status[c]
		groups[s] = append(groups[s], c)
	}
	return groups
}

// RequiredForKukeond returns the controllers the host root's
// subtree_control must include for `kuke init` to provision the kukeond
// cell. When the kukeond cell opts into NestedCgroupRuntime (issue #314),
// the daemon enables the full host-advertised set on the cell's subtree;
// the pre-flight then needs every one of those at the root, too.
// Otherwise the resource subset is enough.
//
// hostAdvertised is the set read from <root>/cgroup.controllers; pass
// only when nested is true (otherwise it is ignored).
func RequiredForKukeond(nested bool, hostAdvertised []string) []string {
	if !nested {
		return CellResourceControllers()
	}
	out := make([]string, len(hostAdvertised))
	copy(out, hostAdvertised)
	return out
}

// Prober attempts to enable a controller in <hostRoot>/cgroup.subtree_control
// and returns a syscall-level error so the caller can distinguish EOPNOTSUPP
// (parent didn't delegate) from EACCES/EPERM (need root) from kernel
// errors. Returning nil means the controller is now enabled.
type Prober func(hostRoot, controller string) error

// DefaultProber writes "+<ctrl>" to <hostRoot>/cgroup.subtree_control
// using O_WRONLY without O_TRUNC — cgroupfs files do not honor truncation
// and interpret the write additively, matching the rest of internal/ctr.
func DefaultProber(hostRoot, controller string) error {
	path := filepath.Join(hostRoot, "cgroup.subtree_control")
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, werr := f.WriteString("+" + controller); werr != nil {
		return werr
	}
	return nil
}

// Check classifies each required controller against the host root cgroup at
// hostRoot (typically /sys/fs/cgroup). When probe is non-nil, controllers
// missing from subtree_control but present in cgroup.controllers are
// disambiguated by calling probe — a successful call leaves the host in a
// strictly better state (controller now enabled), so re-runs are idempotent.
// Pass probe=nil for a strictly read-only check.
func Check(hostRoot string, required []string, probe Prober) (Result, error) {
	advertised, err := readControllers(filepath.Join(hostRoot, "cgroup.controllers"))
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", filepath.Join(hostRoot, "cgroup.controllers"), err)
	}
	enabled, err := readControllers(filepath.Join(hostRoot, "cgroup.subtree_control"))
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", filepath.Join(hostRoot, "cgroup.subtree_control"), err)
	}

	advertisedSet := toSet(advertised)
	enabledSet := toSet(enabled)

	res := Result{
		HostRoot: hostRoot,
		Required: append([]string(nil), required...),
		Status:   make(map[string]Status, len(required)),
		ProbeErr: map[string]error{},
	}

	for _, c := range required {
		switch {
		case enabledSet[c]:
			res.Status[c] = StatusEnabled
		case !advertisedSet[c]:
			res.Status[c] = StatusKernelMissing
		default:
			if probe == nil {
				res.Status[c] = StatusNeedsDelegation
				continue
			}
			perr := probe(hostRoot, c)
			res.ProbeErr[c] = perr
			res.Status[c] = classifyProbeErr(hostRoot, c, perr)
		}
	}
	return res, nil
}

// classifyProbeErr maps the result of a probe write to a Status. The
// distinction between StatusNotDelegated, StatusThreadedSubtree, and
// StatusPermissionDenied is the whole point of probing — getting it right
// keeps the remediation suggestion correct. EOPNOTSUPP on a domain-only
// controller in a "domain threaded" / "threaded" cgroup is the
// threaded-subtree trap, not the cgroup-namespace trap; reading
// cgroup.type at hostRoot is what disambiguates them.
func classifyProbeErr(hostRoot, controller string, err error) Status {
	if err == nil {
		return StatusEnabledByProbe
	}
	switch {
	case errors.Is(err, syscall.EOPNOTSUPP), errors.Is(err, syscall.ENOTSUP):
		if isThreadedSubtreeRoot(hostRoot) && isDomainOnlyController(controller) {
			return StatusThreadedSubtree
		}
		return StatusNotDelegated
	case errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
		return StatusPermissionDenied
	}
	// Fall back to the cgroup-ns trap class when the kernel surfaced an
	// unexpected error: the controller was advertised but the write did
	// not land, which is the same operator-visible outcome.
	return StatusNotDelegated
}

// isDomainOnlyController reports whether a cgroup-v2 controller is
// forbidden in a threaded subtree's cgroup.subtree_control. The kernel
// permits only thread-aware controllers (cpu, cpuset, perf_event, pids
// per cgroup-v2.rst) in a threaded subtree; everything else is
// domain-only and will EOPNOTSUPP on the +<ctrl> write. Adding to this
// set without kernel support would silently reclassify a real namespace
// trap as threaded-subtree, so the list is pinned narrowly.
func isDomainOnlyController(c string) bool {
	switch c {
	case "cpu", "cpuset", "perf_event", "pids":
		return false
	}
	return true
}

// isThreadedSubtreeRoot reports whether the cgroup at hostRoot is in a
// threaded state where domain-only controllers cannot be enabled. The
// kernel exposes two relevant cgroup.type values: "domain threaded" (a
// domain that has a threaded child, so its subtree_control is restricted
// to thread-aware controllers) and "threaded" (the cgroup is itself a
// threaded child). A read failure conservatively returns false so the
// classifier falls through to the existing namespace-trap diagnosis
// rather than mislabeling on hosts where cgroup.type is not present.
func isThreadedSubtreeRoot(hostRoot string) bool {
	data, err := os.ReadFile(filepath.Join(hostRoot, "cgroup.type"))
	if err != nil {
		return false
	}
	switch strings.TrimSpace(string(data)) {
	case "domain threaded", "threaded":
		return true
	}
	return false
}

// FormatRemediation returns a human-readable, multi-line message describing
// the missing controllers and how to fix them. Returns an empty string
// when r.OK() — callers can print unconditionally.
func FormatRemediation(r Result) string {
	if r.OK() {
		return ""
	}

	groups := r.ByStatus()
	unresolved := r.Unresolved()

	var b strings.Builder
	fmt.Fprintf(&b, "host root cgroup is missing controllers required by kukeond: %s\n",
		strings.Join(unresolved, ", "))

	if g, ok := groups[StatusNotDelegated]; ok {
		fmt.Fprintf(&b, "\n  parent did not delegate (cgroup-namespace trap): %s\n", strings.Join(g, ", "))
		b.WriteString("    the namespace this shell runs in advertises these controllers\n")
		b.WriteString("    in cgroup.controllers but the actual parent never delegated\n")
		b.WriteString("    them. escalate to the host / container runtime above this\n")
		b.WriteString("    shell to delegate the missing controllers.\n")
	}
	if g, ok := groups[StatusThreadedSubtree]; ok {
		fmt.Fprintf(&b, "\n  threaded-subtree forbids domain-only controllers: %s\n", strings.Join(g, ", "))
		b.WriteString("    cgroup.type at this scope is \"domain threaded\" or \"threaded\",\n")
		b.WriteString("    so the kernel only permits thread-aware controllers (cpu,\n")
		b.WriteString("    cpuset, perf_event, pids) in this subtree_control. escalating\n")
		b.WriteString("    to the parent runtime will not help — fix the threaded\n")
		b.WriteString("    descendant that promoted this cgroup, or change this scope's\n")
		b.WriteString("    cgroup.type back to \"domain\" before retrying.\n")
	}
	if g, ok := groups[StatusKernelMissing]; ok {
		fmt.Fprintf(&b, "\n  kernel does not support: %s\n", strings.Join(g, ", "))
		b.WriteString("    the running kernel was built without these controllers, or\n")
		b.WriteString("    this scope's parent has not delegated them and the\n")
		b.WriteString("    advertised set is already trimmed. rebuild with the matching\n")
		b.WriteString("    CONFIG_* options or run on a host that exposes them.\n")
	}
	if g, ok := groups[StatusNeedsDelegation]; ok {
		fmt.Fprintf(&b, "\n  not enabled in cgroup.subtree_control: %s\n", strings.Join(g, ", "))
		b.WriteString("    these controllers are advertised but not yet delegated to\n")
		b.WriteString("    children. enable them with the fix below.\n")
	}
	if g, ok := groups[StatusPermissionDenied]; ok {
		fmt.Fprintf(&b, "\n  permission denied during probe: %s\n", strings.Join(g, ", "))
		b.WriteString("    re-run the pre-flight with sudo so the probe write to\n")
		b.WriteString("    cgroup.subtree_control can succeed.\n")
	}

	subtree := filepath.Join(r.HostRoot, "cgroup.subtree_control")
	parts := make([]string, 0, len(unresolved))
	for _, c := range unresolved {
		// "kernel-missing" controllers cannot be enabled by an operator
		// write; "threaded-subtree" controllers will EOPNOTSUPP on the
		// same +<ctrl> write the fix suggests. Omit both from the fix
		// line so the suggestion is correct — the threaded-subtree
		// stanza above already explains the right remediation.
		if r.Status[c] == StatusKernelMissing || r.Status[c] == StatusThreadedSubtree {
			continue
		}
		parts = append(parts, "+"+c)
	}
	if len(parts) > 0 {
		fmt.Fprintf(&b, "\n  fix: echo \"%s\" | sudo tee %s\n", strings.Join(parts, " "), subtree)
		b.WriteString("\n  if that returns \"Operation not supported\", the parent of\n")
		b.WriteString("  this cgroup namespace did not delegate those controllers —\n")
		b.WriteString("  escalate to the host / container runtime above this shell.\n")
	}
	return b.String()
}

// HostAdvertised returns the controller names in <hostRoot>/cgroup.controllers,
// i.e. the set the kernel says can be enabled at this scope. Used by the
// nested-cgroup-runtime path to derive "the full host-advertised set" without
// duplicating the file-read logic outside this package.
func HostAdvertised(hostRoot string) ([]string, error) {
	path := filepath.Join(hostRoot, "cgroup.controllers")
	out, err := readControllers(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return out, nil
}

// readControllers reads a cgroup.controllers / cgroup.subtree_control file
// and returns the space-separated controller names. Empty file returns
// an empty slice (not nil) so callers can still compute a set from it.
func readControllers(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		// Return a more actionable error for the common "kuke doctor
		// cgroups" misuse where the operator pointed --root at something
		// that isn't a cgroup-v2 hierarchy.
		var pathErr *fs.PathError
		if errors.As(err, &pathErr) {
			return nil, err
		}
		return nil, err
	}
	out := strings.Fields(string(data))
	sort.Strings(out)
	return out, nil
}

func toSet(in []string) map[string]bool {
	m := make(map[string]bool, len(in))
	for _, v := range in {
		m[v] = true
	}
	return m
}

// DefaultHostRoot returns the cgroup-v2 mountpoint kukeon assumes when
// the caller did not pass --root. Mirrors consts.CgroupFilesystemPath so
// the pre-flight and the rest of the codebase agree on the same path.
func DefaultHostRoot() string { return consts.CgroupFilesystemPath }
