// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// Check sections, spelled as `kuke status` spells them.
const (
	SectionHost  = "host"
	SectionState = "state"
)

// DefaultCNIBinDir is where CNI plugins are executed from.
const DefaultCNIBinDir = "/opt/cni/bin"

// requiredCNIPlugins are the binaries cell networking needs: bridge lays the
// realm bridge network and loopback populates a container's lo interface.
func requiredCNIPlugins() []string { return []string{"bridge", "loopback"} }

// Probe is what the checks read. It is evaluated in the kukeond process, whose
// cell bind-mounts the host's cgroup hierarchy, containerd socket and run
// directory, so the answers describe the host and not the caller: a client in
// a cell gets the same digest as one on the host.
type Probe struct {
	// ContainerdSocket is shown in the containerd check's detail.
	ContainerdSocket string
	// CgroupRoot is the cgroup-v2 mount point.
	CgroupRoot string
	// CNIBinDir is where kukeond executes CNI plugins from: its own image's
	// copy when kukeond runs in its cell.
	CNIBinDir string
	// SocketDir is the directory holding the kukeond socket. Empty skips the
	// run-dir check.
	SocketDir string
	// Namespaces lists containerd's namespaces.
	Namespaces func() ([]string, error)
	// RealmNamespaces lists the containerd namespace of every realm.
	RealmNamespaces func() ([]string, error)
}

// Checks evaluates the host and state set `kuke status` reports client-side:
// containerd, cgroup-v2, cni-plugins, run-dir and containerd-ns.
func (p Probe) Checks() []kukeonv1.HealthCheck {
	namespaces, nsErr := p.Namespaces()
	return []kukeonv1.HealthCheck{
		p.containerd(nsErr),
		p.cgroupV2(),
		p.cniPlugins(),
		p.runDir(),
		p.containerdNamespaces(namespaces, nsErr),
	}
}

func (p Probe) containerd(nsErr error) kukeonv1.HealthCheck {
	check := kukeonv1.HealthCheck{Section: SectionHost, Name: "containerd"}
	if nsErr != nil {
		check.Status = kukeonv1.HealthStatusFail
		check.Detail = fmt.Sprintf("%s (unreachable: %v)", p.ContainerdSocket, nsErr)
		check.Remediation = "start containerd on the host: `systemctl start containerd`"
		return check
	}
	check.Status = kukeonv1.HealthStatusOK
	check.Detail = fmt.Sprintf("%s (reachable)", p.ContainerdSocket)
	return check
}

func (p Probe) cgroupV2() kukeonv1.HealthCheck {
	check := kukeonv1.HealthCheck{Section: SectionHost, Name: "cgroup-v2"}
	data, err := os.ReadFile(filepath.Join(p.CgroupRoot, "cgroup.controllers"))
	if err != nil {
		check.Status = kukeonv1.HealthStatusFail
		check.Detail = fmt.Sprintf("%s (not mounted: %v)", p.CgroupRoot, err)
		check.Remediation = "boot the host with cgroup-v2 unified mode; see `kuke doctor cgroups`"
		return check
	}
	controllers := strings.Fields(string(data))
	if len(controllers) == 0 {
		check.Status = kukeonv1.HealthStatusWarn
		check.Detail = fmt.Sprintf("%s (mounted, no controllers advertised)", p.CgroupRoot)
		check.Remediation = "verify the kernel build enables cgroup-v2 controllers"
		return check
	}
	check.Status = kukeonv1.HealthStatusOK
	check.Detail = fmt.Sprintf("%s (mounted, %d controllers)", p.CgroupRoot, len(controllers))
	return check
}

// cniPlugins looks where kukeond executes CNI from. Unlike the client-side
// `kuke status` row, a missing plugin here is a failure: there is no other
// copy for cell networking to fall back on.
func (p Probe) cniPlugins() kukeonv1.HealthCheck {
	check := kukeonv1.HealthCheck{Section: SectionHost, Name: "cni-plugins"}
	var missing []string
	for _, plugin := range requiredCNIPlugins() {
		if _, err := os.Stat(filepath.Join(p.CNIBinDir, plugin)); err != nil {
			missing = append(missing, plugin)
		}
	}
	if len(missing) > 0 {
		check.Status = kukeonv1.HealthStatusFail
		check.Detail = fmt.Sprintf("%s (missing: %s)", p.CNIBinDir, strings.Join(missing, ", "))
		check.Remediation = "redeploy kukeond from an image that bundles the CNI plugins: `kuke init --kukeond-image <image>`"
		return check
	}
	check.Status = kukeonv1.HealthStatusOK
	check.Detail = fmt.Sprintf("%s (%s present)", p.CNIBinDir, strings.Join(requiredCNIPlugins(), ", "))
	return check
}

func (p Probe) runDir() kukeonv1.HealthCheck {
	check := kukeonv1.HealthCheck{Section: SectionState, Name: "run-dir"}
	if p.SocketDir == "" {
		check.Status = kukeonv1.HealthStatusOK
		check.Detail = "socket directory unknown; not inspected"
		return check
	}
	entries, err := os.ReadDir(p.SocketDir)
	if err != nil {
		check.Status = kukeonv1.HealthStatusWarn
		check.Detail = fmt.Sprintf("%s (read failed: %v)", p.SocketDir, err)
		return check
	}
	expected := map[string]bool{
		"kukeond.sock":                   true,
		"kukeond.pid":                    true,
		consts.KukeonSocketSymlinkSubdir: true,
		consts.KukeonContainerTTYDir:     true,
		consts.KukeondSocketShareSubdir:  true,
	}
	var orphans []string
	for _, entry := range entries {
		if !expected[entry.Name()] {
			orphans = append(orphans, entry.Name())
		}
	}
	if len(orphans) == 0 {
		check.Status = kukeonv1.HealthStatusOK
		check.Detail = fmt.Sprintf("%s (no orphan entries)", p.SocketDir)
		return check
	}
	sort.Strings(orphans)
	check.Status = kukeonv1.HealthStatusWarn
	check.Detail = fmt.Sprintf("%s (orphan entries: %s)", p.SocketDir, strings.Join(orphans, ", "))
	check.Remediation = "`kuke daemon reset` clears stale run-dir state before re-init"
	return check
}

func (p Probe) containerdNamespaces(namespaces []string, nsErr error) kukeonv1.HealthCheck {
	check := kukeonv1.HealthCheck{Section: SectionState, Name: "containerd-ns"}
	if nsErr != nil {
		check.Status = kukeonv1.HealthStatusWarn
		check.Detail = fmt.Sprintf("list namespaces failed: %v", nsErr)
		return check
	}
	realms, err := p.RealmNamespaces()
	if err != nil {
		check.Status = kukeonv1.HealthStatusWarn
		check.Detail = fmt.Sprintf("list realms failed: %v", err)
		return check
	}
	// A live realm owns its namespace and, once built into, that namespace's
	// BuildKit history companion.
	claimed := map[string]bool{}
	for _, ns := range realms {
		claimed[ns] = true
		claimed[consts.BuildKitHistoryNamespace(ns)] = true
	}
	var ours, residual []string
	for _, ns := range namespaces {
		if !consts.IsKukeonNamespace(ns) && !consts.IsBuildKitHistoryNamespace(ns) {
			continue
		}
		ours = append(ours, ns)
		if !claimed[ns] {
			residual = append(residual, ns)
		}
	}
	if len(residual) == 0 {
		check.Status = kukeonv1.HealthStatusOK
		check.Detail = fmt.Sprintf("%d kukeon namespaces, all claimed by a realm", len(ours))
		return check
	}
	sort.Strings(residual)
	check.Status = kukeonv1.HealthStatusWarn
	check.Detail = "residual namespaces: " + strings.Join(residual, ", ")
	check.Remediation = "`kuke uninstall` or hand-cleanup with `ctr -n <ns> namespace remove`"
	return check
}
