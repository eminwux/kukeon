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

package status

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
)

// Section names — kept as constants so the renderer's section grouping and
// the JSON `section` field are spelled the same way everywhere.
const (
	sectionDaemon = "daemon"
	sectionHost   = "host"
	sectionState  = "state"
	sectionParity = "parity"
)

// checkDaemon reports the daemon's reachability. The single row carries the
// dial outcome, the round-trip latency, and the daemon's reported build
// version — the three things the gaps doc's `kuke ping` proposal wanted.
// FAIL on dial failure or Ping error; OK otherwise.
func checkDaemon(ctx context.Context, rc *runCtx) []Result {
	r := Result{
		Section: sectionDaemon,
		Name:    "socket",
	}

	if rc.daemonClient == nil {
		r.Status = StatusFAIL
		r.Detail = fmt.Sprintf("%s (dial failed)", rc.daemonHost)
		r.Remediation = "start kukeond or set --host; `kuke init` brings the daemon up"
		return []Result{r}
	}

	start := time.Now()
	version, err := rc.daemonClient.PingVersion(ctx)
	rtt := time.Since(start)
	if err != nil {
		r.Status = StatusFAIL
		r.Detail = fmt.Sprintf("%s (ping failed: %v)", rc.daemonHost, err)
		r.Remediation = "check `kuke daemon reset` and `kuke init` to re-bootstrap kukeond"
		return []Result{r}
	}

	if version == "" {
		// Daemon answered but didn't report a version (older build, or
		// linker omitted the -X). Still OK — the dial worked — but the
		// detail call-out is honest.
		r.Status = StatusOK
		r.Detail = fmt.Sprintf("%s (rtt %s, version unknown)", rc.daemonHost, fmtRTT(rtt))
		return []Result{r}
	}

	r.Status = StatusOK
	r.Detail = fmt.Sprintf("%s (rtt %s, version %s)", rc.daemonHost, fmtRTT(rtt), version)
	return []Result{r}
}

// checkHost runs the three host-environment checks the gaps doc's
// `kuke doctor` proposal absorbed into this single section: containerd
// reachability, cgroup-v2 mountedness, and the presence of the CNI plugin
// binaries `kuke init` would otherwise lay down. Each emits one Result.
//
// No ctx: the host probes are filesystem stats and a ctr.Client.Connect()
// whose cancellation surface is the client's own context. Adding a ctx
// param "in case" would invite the unused-parameter lint.
func checkHost(rc *runCtx) []Result {
	return []Result{
		checkHostContainerd(rc),
		checkHostCgroupV2(rc),
		checkHostCNIPlugins(rc),
	}
}

// checkHostContainerd dials the configured containerd socket via the
// in-process ctr client (the same wrapper kuke build / kuke init use, so
// reachability here is exactly what those commands would see). On
// success, ListNamespaces is what `verifyConnection` round-trips inside
// ctr.Client.Connect — no extra read needed.
func checkHostContainerd(rc *runCtx) Result {
	r := Result{
		Section: sectionHost,
		Name:    "containerd",
	}

	if rc.ctrClient == nil {
		r.Status = StatusFAIL
		r.Detail = "ctr client not constructed"
		r.Remediation = "internal: status invoked without a containerd wrapper"
		return r
	}

	if err := rc.ctrClient.Connect(); err != nil {
		r.Status = StatusFAIL
		r.Detail = fmt.Sprintf("%s (dial failed: %v)", rc.containerdSocket, err)
		r.Remediation = "start containerd: `service containerd start` (see project CLAUDE.md)"
		return r
	}

	r.Status = StatusOK
	r.Detail = fmt.Sprintf("%s (reachable)", rc.containerdSocket)
	return r
}

// checkHostCgroupV2 reads cgroup.controllers at the configured cgroup
// root — that file's existence is the canonical cgroup-v2 mount probe
// the cgroupcheck package already wraps; the host advertising any
// controllers means v2 is mounted unified at that path. FAIL on read
// error (no v2 mount, or the directory is wrong); WARN when the file is
// readable but reports zero controllers (a bare-kernel build, no
// delegation done yet).
func checkHostCgroupV2(rc *runCtx) Result {
	r := Result{
		Section: sectionHost,
		Name:    "cgroup-v2",
	}

	controllersPath := filepath.Join(rc.cgroupRoot, "cgroup.controllers")
	data, err := os.ReadFile(controllersPath)
	if err != nil {
		r.Status = StatusFAIL
		r.Detail = fmt.Sprintf("%s (not mounted: %v)", rc.cgroupRoot, err)
		r.Remediation = "boot with cgroup-v2 unified mode; see `kuke doctor cgroups` for the full pre-flight"
		return r
	}

	controllers := strings.Fields(strings.TrimSpace(string(data)))
	if len(controllers) == 0 {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%s (mounted, no controllers advertised)", rc.cgroupRoot)
		r.Remediation = "verify the kernel build enables cgroup-v2 controllers"
		return r
	}

	r.Status = StatusOK
	r.Detail = fmt.Sprintf("%s (mounted, %d controllers)", rc.cgroupRoot, len(controllers))
	return r
}

// checkHostCNIPlugins stats each requiredCNIPlugins under the configured
// CNI bin dir. FAIL when any are missing — without bridge/loopback the
// per-container netns wiring `kuke create container` does cannot
// complete. Surfacing the specific missing plugin name in Detail keeps
// the operator from having to ls the directory themselves.
func checkHostCNIPlugins(rc *runCtx) Result {
	r := Result{
		Section: sectionHost,
		Name:    "cni-plugins",
	}

	plugins := requiredCNIPlugins()
	var missing []string
	for _, plugin := range plugins {
		path := filepath.Join(rc.cniBinDir, plugin)
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, plugin)
		}
	}

	if len(missing) > 0 {
		r.Status = StatusFAIL
		r.Detail = fmt.Sprintf("%s (missing: %s)", rc.cniBinDir, strings.Join(missing, ", "))
		r.Remediation = "install the CNI plugin binaries; `kuke init` lays them down on a fresh host"
		return r
	}

	r.Status = StatusOK
	r.Detail = fmt.Sprintf("%s (%s present)", rc.cniBinDir, strings.Join(plugins, ", "))
	return r
}

// checkState runs the consistency probes the gaps doc's `kuke selftest`
// proposal called for: stale orphan files in the run dir, and residual
// containerd namespaces that no realm claims. WARN by default — neither
// surfaces a serving regression (a daemon can run fine alongside an
// orphan from a prior reset that wasn't fully cleaned), but the operator
// wants to know.
func checkState(ctx context.Context, rc *runCtx) []Result {
	return []Result{
		checkStateRunDir(rc),
		checkStateNamespaces(ctx, rc),
	}
}

// checkStateRunDir enumerates entries directly under rc.runPath/.. that
// the kukeon runtime knows about — the canonical sock, pid, and
// well-known subdirs — and reports any other top-level entry under the
// `/run/kukeon` parent dir as an orphan. Only checks the run-socket
// directory (parent of the kukeond socket), not the data dir, because
// the data dir is partitioned by realm and the parity walk covers
// divergence there.
//
// Heuristic only — the check can't reliably distinguish a live socket
// from a dead one without an extra `lsof`-style probe, which the
// AC doesn't budget for. The role is to surface "the run dir looks
// unexpectedly populated" so the operator inspects, not to enforce.
func checkStateRunDir(rc *runCtx) Result {
	r := Result{
		Section: sectionState,
		Name:    "run-dir",
	}

	// The kukeond socket usually lives at /run/kukeon/kukeond.sock;
	// when --run-path is set, the socket auto-derives via
	// applyRunPathImpliesKukeondSocket. Either way we inspect the dir
	// the socket sits in, not the run-path data dir.
	socketDir := canonicalRunSocketDir(rc.runPath)

	entries, err := os.ReadDir(socketDir)
	if err != nil {
		// An absent run-socket dir on a non-init'd host is not a state
		// inconsistency — it's the documented bare-host state. Surface
		// as OK with a "not present" detail rather than FAIL.
		if os.IsNotExist(err) {
			r.Status = StatusOK
			r.Detail = fmt.Sprintf("%s (not present; daemon not initialized)", socketDir)
			return r
		}
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%s (read failed: %v)", socketDir, err)
		return r
	}

	expected := map[string]bool{
		"kukeond.sock":                   true,
		"kukeond.pid":                    true,
		consts.KukeonSocketSymlinkSubdir: true, // "s"
		consts.KukeonContainerTTYDir:     true, // "tty"
	}

	var orphans []string
	for _, e := range entries {
		if expected[e.Name()] {
			continue
		}
		orphans = append(orphans, e.Name())
	}

	if len(orphans) == 0 {
		r.Status = StatusOK
		r.Detail = fmt.Sprintf("%s (no orphan entries)", socketDir)
		return r
	}

	sort.Strings(orphans)
	r.Status = StatusWARN
	r.Detail = fmt.Sprintf("%s (orphan entries: %s)", socketDir, strings.Join(orphans, ", "))
	r.Remediation = "`kuke daemon reset` clears stale run-dir state before re-init"
	return r
}

// canonicalRunSocketDir returns the directory the kukeond socket lives
// in. Default runs use /run/kukeon; bespoke --run-path setups put the
// socket under <runPath>/kukeond.sock per applyRunPathImpliesKukeondSocket
// in cmd/kuke/kuke.go.
//
// The runPath default (/opt/kukeon) is NOT the same directory as the
// run-socket default (/run/kukeon) — runPath is the data tree, the
// socket lives in /run by convention. So when runPath looks like the
// default data path, we point at /run/kukeon explicitly.
func canonicalRunSocketDir(runPath string) string {
	// Bare-default case: data dir is /opt/kukeon, socket lives in
	// /run/kukeon. The kukeond.sock default in cmd/config/env.go:134
	// hardcodes /run/kukeon/kukeond.sock for exactly this reason.
	if runPath == "" || runPath == "/opt/kukeon" {
		return "/run/kukeon"
	}
	// Non-default run-path: applyRunPathImpliesKukeondSocket derives
	// the socket as <runPath>/kukeond.sock, so the run-socket dir is
	// runPath itself.
	return runPath
}

// checkStateNamespaces lists containerd namespaces and flags any that
// don't end in the kukeon suffix or whose realm prefix doesn't match a
// known realm. Requires the daemon path to enumerate realms — if the
// daemon is down, we skip the cross-reference and report just the raw
// namespace count (the user already has the daemon-down signal from the
// daemon section).
func checkStateNamespaces(ctx context.Context, rc *runCtx) Result {
	r := Result{
		Section: sectionState,
		Name:    "containerd-ns",
	}

	if rc.ctrClient == nil {
		r.Status = StatusWARN
		r.Detail = "ctr client not constructed"
		return r
	}

	// Connect() is idempotent (verifyConnection short-circuits when the
	// client already dialed) — the host check above usually already
	// connected, but on the daemon-down path the host check may have
	// failed and we need a fresh attempt here.
	if err := rc.ctrClient.Connect(); err != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("ctr unreachable: %v", err)
		return r
	}

	nsList, err := rc.ctrClient.ListNamespaces()
	if err != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("list namespaces failed: %v", err)
		return r
	}

	// Filter to namespaces under the kukeon suffix — that's what
	// `kuke init` provisions and what `kuke uninstall` removes. Other
	// namespaces on the same containerd (e.g. user docker namespaces)
	// are not ours to police.
	var kukeonNS []string
	for _, ns := range nsList {
		if consts.IsKukeonNamespace(ns) {
			kukeonNS = append(kukeonNS, ns)
		}
	}

	if rc.daemonClient == nil {
		// Daemon is down — we can't enumerate realms to cross-check.
		// Report what we see without a verdict beyond the count.
		sort.Strings(kukeonNS)
		r.Status = StatusOK
		r.Detail = fmt.Sprintf("%d kukeon namespaces (daemon down, no cross-check)", len(kukeonNS))
		return r
	}

	realms, err := rc.daemonClient.ListRealms(ctx)
	if err != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("list realms failed: %v", err)
		return r
	}

	expectedNS := make(map[string]bool, len(realms))
	for i := range realms {
		expectedNS[consts.RealmNamespace(realms[i].Metadata.Name)] = true
	}

	var residual []string
	for _, ns := range kukeonNS {
		if !expectedNS[ns] {
			residual = append(residual, ns)
		}
	}

	if len(residual) == 0 {
		r.Status = StatusOK
		r.Detail = fmt.Sprintf("%d kukeon namespaces, all claimed by a realm", len(kukeonNS))
		return r
	}

	sort.Strings(residual)
	r.Status = StatusWARN
	r.Detail = fmt.Sprintf("residual namespaces: %s", strings.Join(residual, ", "))
	r.Remediation = "`kuke uninstall` or hand-cleanup with `ctr -n <ns> namespace remove`"
	return r
}

// fmtRTT formats a ping round-trip duration with sub-millisecond
// resolution. time.Duration.String() picks units inconsistently for
// short durations (e.g. "1.234ms" but also "500µs") — fixing to one
// fractional milli-digit keeps the daemon row visually aligned.
func fmtRTT(d time.Duration) string {
	if d < time.Microsecond {
		return "0ms"
	}
	// time.Duration is integer nanoseconds; dividing by Millisecond's
	// float yields ms with sub-ms resolution without the mnd-tripping
	// `1000` literal a Microseconds/1000 ratio would otherwise carry.
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}
