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
	"sort"

	"github.com/eminwux/kukeon/internal/consts"
)

// checkStorage emits one row per realm reporting its containerd
// namespace's storage footprint — snapshot / lease / content-blob
// counts plus summed blob bytes. The figures surface accumulation
// before the data volume fills (issue #1039); the prior failure mode
// was the leak surfacing only at ENOSPC several layers downstream.
//
// Realm enumeration prefers the daemon (ListRealms) for the realm-name
// labels, with the ctr namespace list as a fallback so the section
// stays populated when the daemon is down. Each probe is a
// metadata-store read (boltdb iterator), so the call is cheap enough
// for the status budget — see ctr.NamespaceStorage's doc for the
// per-snapshot-disk-usage carve-out.
func checkStorage(ctx context.Context, rc *runCtx) []Result {
	if rc.ctrClient == nil {
		return []Result{{
			Section:     sectionStorage,
			Name:        "ctr",
			Status:      StatusWARN,
			Detail:      "ctr client not constructed",
			Remediation: "internal: status invoked without a containerd wrapper",
		}}
	}

	// Connect() is idempotent (the host check above already dialed on
	// the happy path); a dial failure here surfaces as a single WARN
	// row rather than per-realm WARNs.
	if err := rc.ctrClient.Connect(); err != nil {
		return []Result{{
			Section:     sectionStorage,
			Name:        "ctr",
			Status:      StatusWARN,
			Detail:      fmt.Sprintf("ctr unreachable: %v", err),
			Remediation: "the host containerd row above carries the underlying cause",
		}}
	}

	realms := enumerateRealmsForStorage(ctx, rc)
	if len(realms) == 0 {
		return []Result{{
			Section: sectionStorage,
			Name:    "realms",
			Status:  StatusOK,
			Detail:  "no realms (host not initialized)",
		}}
	}

	results := make([]Result, 0, len(realms))
	for _, realm := range realms {
		results = append(results, storageRowForRealm(rc, realm))
	}
	return results
}

// enumerateRealmsForStorage returns the realm names to probe. Prefers
// the daemon's ListRealms (canonical source) and falls back to deriving
// realm names from the ctr namespace list when the daemon is down or
// listing fails — the section still has something to report, with the
// daemon-down rationale surfacing on the daemon row above.
func enumerateRealmsForStorage(ctx context.Context, rc *runCtx) []string {
	if rc.daemonClient != nil {
		realms, err := rc.daemonClient.ListRealms(ctx)
		if err == nil {
			out := make([]string, 0, len(realms))
			for i := range realms {
				out = append(out, realms[i].Metadata.Name)
			}
			sort.Strings(out)
			return out
		}
	}

	nsList, err := rc.ctrClient.ListNamespaces()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(nsList))
	for _, ns := range nsList {
		if realm := consts.RealmFromNamespace(ns); realm != "" {
			out = append(out, realm)
		}
	}
	sort.Strings(out)
	return out
}

// storageRowForRealm probes one realm's containerd namespace and
// formats the figures. Probe failures demote the row to WARN — a stuck
// containerd metadata read shouldn't be a regression signal alongside
// the parity walk's FAIL, and the operator already has the host
// section's signal.
func storageRowForRealm(rc *runCtx, realm string) Result {
	r := Result{
		Section: sectionStorage,
		Name:    realm,
	}

	ns := consts.RealmNamespace(realm)
	stats, err := rc.ctrClient.NamespaceStorage(ns)
	if err != nil {
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%s (probe failed: %v)", ns, err)
		return r
	}

	r.Storage = &StorageStats{
		Snapshots:  stats.Snapshots,
		Leases:     stats.Leases,
		Blobs:      stats.Blobs,
		BlobsBytes: stats.BlobsBytes,
	}
	r.Status = StatusOK
	r.Detail = fmt.Sprintf(
		"%s (%d snapshots, %d leases, %d blobs, %s)",
		ns, stats.Snapshots, stats.Leases, stats.Blobs, fmtBytes(stats.BlobsBytes),
	)
	return r
}

// fmtBytes renders a byte count with a binary-IEC unit suffix, picking
// the largest unit at which the value reads >= 1. Kept here rather
// than in a shared helper because the status report is the only call
// site; pulling humanize-style helpers in for one row would be over-
// engineering. Negative inputs are not expected (Size from
// content.Info is unsigned in practice) but render through the same
// path so a wrap doesn't crash the row.
func fmtBytes(n int64) string {
	const (
		kib = int64(1024)
		mib = kib * 1024
		gib = mib * 1024
		tib = gib * 1024
	)
	switch {
	case n >= tib:
		return fmt.Sprintf("%.1f TiB", float64(n)/float64(tib))
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
