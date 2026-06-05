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
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
)

// TestCheckStorage_PerRealmRow pins the happy-path contract: one row
// per realm, each carrying the structured StorageStats payload and a
// human Detail string that names the containerd namespace plus the
// figures the AC requires (snapshots / leases / blobs + bytes).
func TestCheckStorage_PerRealmRow(t *testing.T) {
	rc := &runCtx{
		daemonClient: newFakeClient().withDefaultRealms(),
		ctrClient: &fakeCtrClient{
			namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io"},
			storage: map[string]ctr.StorageStats{
				"default.kukeon.io":     {Snapshots: 12, Leases: 49, Blobs: 24, BlobsBytes: 5 * 1024 * 1024},
				"kuke-system.kukeon.io": {Snapshots: 157, Leases: 266, Blobs: 180, BlobsBytes: 700 * 1024 * 1024},
			},
		},
	}

	results := checkStorage(context.Background(), rc)
	if len(results) != 2 {
		t.Fatalf("expected one row per realm; got %d (%+v)", len(results), results)
	}

	byName := map[string]Result{}
	for _, r := range results {
		if r.Section != sectionStorage {
			t.Errorf("row %q has section %q; want %q", r.Name, r.Section, sectionStorage)
		}
		byName[r.Name] = r
	}

	def, ok := byName["default"]
	if !ok {
		t.Fatalf("expected row for default realm; got %+v", byName)
	}
	if def.Status != StatusOK {
		t.Errorf("default realm row should be OK; got %s (%s)", def.Status, def.Detail)
	}
	if def.Storage == nil {
		t.Fatalf("default realm row missing Storage payload")
	}
	if def.Storage.Snapshots != 12 || def.Storage.Leases != 49 || def.Storage.Blobs != 24 {
		t.Errorf("default storage figures mismatch: %+v", def.Storage)
	}
	if !strings.Contains(def.Detail, "default.kukeon.io") {
		t.Errorf("default Detail should name the containerd namespace; got %q", def.Detail)
	}
	for _, want := range []string{"12 snapshots", "49 leases", "24 blobs"} {
		if !strings.Contains(def.Detail, want) {
			t.Errorf("default Detail missing %q; got %q", want, def.Detail)
		}
	}

	sys, ok := byName["kuke-system"]
	if !ok {
		t.Fatalf("expected row for kuke-system realm; got %+v", byName)
	}
	if !strings.Contains(sys.Detail, "700.0 MiB") {
		t.Errorf("kuke-system Detail should include MiB bytes total; got %q", sys.Detail)
	}
}

// TestCheckStorage_DaemonDownFallsBackToNamespaces pins the degraded
// path: a daemon-down run still surfaces per-namespace figures by
// deriving realm names from the ctr namespace list. The section stays
// populated so the JSON shape is stable.
func TestCheckStorage_DaemonDownFallsBackToNamespaces(t *testing.T) {
	rc := &runCtx{
		daemonClient: nil,
		ctrClient: &fakeCtrClient{
			namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io", "some-other.namespace"},
			storage: map[string]ctr.StorageStats{
				"default.kukeon.io":     {Snapshots: 1, Leases: 2, Blobs: 3, BlobsBytes: 4},
				"kuke-system.kukeon.io": {Snapshots: 5, Leases: 6, Blobs: 7, BlobsBytes: 8},
			},
		},
	}

	results := checkStorage(context.Background(), rc)
	if len(results) != 2 {
		t.Fatalf("expected two rows (kukeon namespaces only); got %d", len(results))
	}
	names := []string{results[0].Name, results[1].Name}
	for _, want := range []string{"default", "kuke-system"} {
		if !containsStr(names, want) {
			t.Errorf("expected fallback to include realm %q; got %v", want, names)
		}
	}
	for _, r := range results {
		if r.Status != StatusOK {
			t.Errorf("fallback row %q should be OK; got %s", r.Name, r.Status)
		}
	}
}

// TestCheckStorage_CtrUnreachableIsWARN documents the dial-failure
// path: a single WARN row at the section head rather than a per-realm
// fan-out of WARNs, matching the host section's containerd-down
// signal.
func TestCheckStorage_CtrUnreachableIsWARN(t *testing.T) {
	rc := &runCtx{
		daemonClient: newFakeClient().withDefaultRealms(),
		ctrClient:    &fakeCtrClient{connectErr: errFakeDialFailed},
	}

	results := checkStorage(context.Background(), rc)
	if len(results) != 1 {
		t.Fatalf("expected single WARN row on dial failure; got %d", len(results))
	}
	if results[0].Status != StatusWARN {
		t.Errorf("expected WARN; got %s (%s)", results[0].Status, results[0].Detail)
	}
	if !strings.Contains(results[0].Detail, "ctr unreachable") {
		t.Errorf("Detail should name dial failure; got %q", results[0].Detail)
	}
}

// TestCheckStorage_ProbeErrorDemotesRow pins the per-realm WARN path:
// when NamespaceStorage returns an error for a realm, the row demotes
// to WARN without taking down the rest of the section.
func TestCheckStorage_ProbeErrorDemotesRow(t *testing.T) {
	rc := &runCtx{
		daemonClient: newFakeClient().withDefaultRealms(),
		ctrClient: &fakeCtrClient{
			namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io"},
			storageErr: errFakeProbeFailed,
		},
	}

	results := checkStorage(context.Background(), rc)
	if len(results) != 2 {
		t.Fatalf("expected per-realm rows even on probe error; got %d", len(results))
	}
	for _, r := range results {
		if r.Status != StatusWARN {
			t.Errorf("row %q should be WARN on probe error; got %s", r.Name, r.Status)
		}
		if !strings.Contains(r.Detail, "probe failed") {
			t.Errorf("row %q Detail should name probe failure; got %q", r.Name, r.Detail)
		}
	}
}

// TestCheckStorage_JSONShape confirms the JSON output surfaces the
// structured Storage payload (the AC's "same figures in JSON output
// shape" requirement) — not just the formatted Detail string.
func TestCheckStorage_JSONShape(t *testing.T) {
	rc := &runCtx{
		daemonClient: newFakeClient().withDefaultRealms(),
		ctrClient: &fakeCtrClient{
			namespaces: []string{"default.kukeon.io", "kuke-system.kukeon.io"},
			storage: map[string]ctr.StorageStats{
				"default.kukeon.io":     {Snapshots: 12, Leases: 49, Blobs: 24, BlobsBytes: 5 * 1024 * 1024},
				"kuke-system.kukeon.io": {Snapshots: 157, Leases: 266, Blobs: 180, BlobsBytes: 700 * 1024 * 1024},
			},
		},
	}

	report := Report{OK: true, Checks: checkStorage(context.Background(), rc)}

	var buf bytes.Buffer
	if err := renderJSON(&buf, report); err != nil {
		t.Fatalf("renderJSON: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("JSON malformed: %v\n%s", err, buf.String())
	}
	checks, _ := parsed["checks"].([]any)
	if len(checks) != 2 {
		t.Fatalf("expected 2 storage checks in JSON; got %d", len(checks))
	}
	for _, raw := range checks {
		row, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("storage row is not an object: %v", raw)
		}
		storage, ok := row["storage"].(map[string]any)
		if !ok {
			t.Fatalf("storage row missing structured `storage` payload: %v", row)
		}
		for _, field := range []string{"snapshots", "leases", "blobs", "blobsBytes"} {
			if _, present := storage[field]; !present {
				t.Errorf("storage payload missing %q field: %v", field, storage)
			}
		}
	}
}

// TestFmtBytes_UnitPicker spans the unit boundaries fmtBytes prints —
// the rounding shape matters because operators eyeball the section to
// catch slow growth, and a wrong unit on a borderline value would
// hide a small leak under a rounded zero.
func TestFmtBytes_UnitPicker(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB"},
		{5 * 1024 * 1024, "5.0 MiB"},
		{2 * 1024 * 1024 * 1024, "2.0 GiB"},
		{3 * int64(1024) * 1024 * 1024 * 1024, "3.0 TiB"},
	}
	for _, c := range cases {
		if got := fmtBytes(c.n); got != c.want {
			t.Errorf("fmtBytes(%d): got %q, want %q", c.n, got, c.want)
		}
	}
}

// errFake* are sentinel errors for the fake ctr client — wired up here
// rather than via errors.New("...") inline so the tests assert the
// detail surfaces the wrapped cause without depending on the exact
// string literal in each helper.
var (
	errFakeDialFailed  = fakeError("dial failed: simulated")
	errFakeProbeFailed = fakeError("metadata store unreachable")
)

type fakeError string

func (e fakeError) Error() string { return string(e) }
