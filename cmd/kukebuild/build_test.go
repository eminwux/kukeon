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

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/leases"
)

// newSolveOptFixture writes a minimal build context (an empty Dockerfile) into
// a temp dir and returns a buildConfig pointed at it. Callers add secrets /
// cache entries before calling newSolveOpt.
func newSolveOptFixture(t *testing.T) *buildConfig {
	t.Helper()
	dir := t.TempDir()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	return &buildConfig{contextDir: dir, dockerfile: dockerfile, tag: "x:1"}
}

func TestNewSolveOptWiresCache(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.cacheExports = []cacheSpec{{typ: "local", attrs: map[string]string{"dest": "/c/out"}}}
	cfg.cacheImports = []cacheSpec{{typ: "local", attrs: map[string]string{"src": "/c/in"}}}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if len(so.CacheExports) != 1 || so.CacheExports[0].Type != "local" ||
		so.CacheExports[0].Attrs["dest"] != "/c/out" {
		t.Errorf("CacheExports = %+v, want one local dest=/c/out", so.CacheExports)
	}
	if len(so.CacheImports) != 1 || so.CacheImports[0].Type != "local" ||
		so.CacheImports[0].Attrs["src"] != "/c/in" {
		t.Errorf("CacheImports = %+v, want one local src=/c/in", so.CacheImports)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 when no secrets", len(so.Session))
	}
}

func TestNewSolveOptWiresSecrets(t *testing.T) {
	cfg := newSolveOptFixture(t)
	secretFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(secretFile, []byte("s3cr3t"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	cfg.secrets = []secretSpec{{id: "tok", src: secretFile}}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	// One secretsprovider attachable is added to the session.
	if len(so.Session) != 1 {
		t.Errorf("Session = %d entries, want 1 secrets provider", len(so.Session))
	}
}

func TestNewSolveOptSecretFileMissing(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.secrets = []secretSpec{{id: "tok", src: filepath.Join(t.TempDir(), "does-not-exist")}}

	if _, err := newSolveOpt(cfg, nil); err == nil {
		t.Fatal("newSolveOpt: expected error for missing secret file, got nil")
	}
}

func TestNewSolveOptWiresPush(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.tag = "registry:5000/app:dev"
	cfg.push = true
	// A real auth attachable, resolved with the env fallback so the test needs
	// no on-disk docker config.
	ap, _, err := newAuthProvider(fakeEnv(map[string]string{"HOME": t.TempDir()}), "registry:5000")
	if err != nil {
		t.Fatalf("newAuthProvider: %v", err)
	}

	so, err := newSolveOpt(cfg, ap)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if got := so.Exports[0].Attrs["push"]; got != "true" {
		t.Errorf(`Exports[0].Attrs["push"] = %q, want "true"`, got)
	}
	if len(so.Session) != 1 {
		t.Errorf("Session = %d entries, want 1 (the auth provider)", len(so.Session))
	}
}

func TestNewSolveOptNoPushByDefault(t *testing.T) {
	cfg := newSolveOptFixture(t) // push defaults to false
	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if _, ok := so.Exports[0].Attrs["push"]; ok {
		t.Error(`Exports[0].Attrs["push"] is set, want absent when --push is off`)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 when not pushing", len(so.Session))
	}
}

// A push build with a nil auth provider (anonymous push) still sets the push
// attr and must not panic appending to the session.
func TestNewSolveOptPushNilAuth(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.tag = "registry:5000/app:dev"
	cfg.push = true
	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if so.Exports[0].Attrs["push"] != "true" {
		t.Error(`Exports[0].Attrs["push"] != "true" for an anonymous push`)
	}
	if len(so.Session) != 0 {
		t.Errorf("Session = %d entries, want 0 for nil auth provider", len(so.Session))
	}
}

func TestNewSolveOptWiresPlatform(t *testing.T) {
	// Multiple platforms set the comma-separated `platform` frontend attr; the
	// Dockerfile frontend turns that into a manifest-list result.
	cfg := newSolveOptFixture(t)
	cfg.platforms = []string{"linux/amd64", "linux/arm64"}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if got := so.FrontendAttrs["platform"]; got != "linux/amd64,linux/arm64" {
		t.Errorf(`FrontendAttrs["platform"] = %q, want "linux/amd64,linux/arm64"`, got)
	}
}

func TestNewSolveOptSinglePlatform(t *testing.T) {
	cfg := newSolveOptFixture(t)
	cfg.platforms = []string{"linux/arm64"}

	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if got := so.FrontendAttrs["platform"]; got != "linux/arm64" {
		t.Errorf(`FrontendAttrs["platform"] = %q, want "linux/arm64"`, got)
	}
}

func TestNewSolveOptNoPlatformByDefault(t *testing.T) {
	cfg := newSolveOptFixture(t) // platforms unset
	so, err := newSolveOpt(cfg, nil)
	if err != nil {
		t.Fatalf("newSolveOpt: %v", err)
	}
	if _, ok := so.FrontendAttrs["platform"]; ok {
		t.Error(`FrontendAttrs["platform"] is set, want absent when --platform is off`)
	}
}

func TestResolveBuildRoot(t *testing.T) {
	cases := []struct {
		name      string
		root      string
		explicit  bool
		namespace string
		want      string
	}{
		{
			name:      "default root scoped per namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.kukeon.io",
			want:      "/var/lib/kukebuild/default.kukeon.io",
		},
		{
			name:      "default root scoped per different namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "kuke-system.kukeon.io",
			want:      "/var/lib/kukebuild/kuke-system.kukeon.io",
		},
		{
			name:      "default root scoped per custom-suffix namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.dev.kukeon.io",
			want:      "/var/lib/kukebuild/default.dev.kukeon.io",
		},
		{
			name:      "explicit root honored verbatim",
			root:      "/tmp/freshroot",
			explicit:  true,
			namespace: "default.kukeon.io",
			want:      "/tmp/freshroot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBuildRoot(tc.root, tc.explicit, tc.namespace); got != tc.want {
				t.Errorf("resolveBuildRoot(%q, %v, %q) = %q, want %q",
					tc.root, tc.explicit, tc.namespace, got, tc.want)
			}
		})
	}
}

// Two consecutive default-root builds into different namespaces must resolve to
// distinct BuildKit state roots — the isolation that prevents the cross-
// namespace cache reuse in issue #663.
func TestResolveBuildRootIsolatesNamespaces(t *testing.T) {
	first := resolveBuildRoot(defaultBuildRoot, false, "default.kukeon.io")
	second := resolveBuildRoot(defaultBuildRoot, false, "kuke-system.kukeon.io")
	if first == second {
		t.Errorf("default roots for distinct namespaces collide: %q == %q", first, second)
	}
}

// fakeLeaseManager satisfies leases.Manager with an in-memory store, capturing
// the filter strings List was called with and the lease IDs Delete touched —
// enough to drive drainBuildkitTemporaryLeases and assert it issued the same
// filter base.NewWorker does at startup (worker/base/worker.go:214) and
// synchronously deleted only the matching leases.
type fakeLeaseManager struct {
	leases       []leases.Lease
	listFilters  []string
	deletedIDs   []string
	deleteSynced []bool
	listErr      error
}

func (f *fakeLeaseManager) Create(_ context.Context, _ ...leases.Opt) (leases.Lease, error) {
	return leases.Lease{}, errors.New("not implemented")
}

func (f *fakeLeaseManager) Delete(_ context.Context, l leases.Lease, opts ...leases.DeleteOpt) error {
	do := &leases.DeleteOptions{}
	for _, o := range opts {
		_ = o(context.Background(), do)
	}
	f.deletedIDs = append(f.deletedIDs, l.ID)
	f.deleteSynced = append(f.deleteSynced, do.Synchronous)
	remaining := f.leases[:0]
	for _, kept := range f.leases {
		if kept.ID != l.ID {
			remaining = append(remaining, kept)
		}
	}
	f.leases = remaining
	return nil
}

func (f *fakeLeaseManager) List(_ context.Context, filters ...string) ([]leases.Lease, error) {
	f.listFilters = append(f.listFilters, filters...)
	if f.listErr != nil {
		return nil, f.listErr
	}
	if len(filters) == 0 {
		out := append([]leases.Lease(nil), f.leases...)
		return out, nil
	}
	// The drain only ever passes the single buildkit/lease.temporary label
	// filter; the fake mirrors a `labels."X"` predicate so a wrong filter
	// string yields zero results and fails the assertion below.
	const want = `labels."buildkit/lease.temporary"`
	var out []leases.Lease
	for _, l := range f.leases {
		match := false
		for _, fl := range filters {
			if fl != want {
				continue
			}
			if _, ok := l.Labels["buildkit/lease.temporary"]; ok {
				match = true
				break
			}
		}
		if match {
			out = append(out, l)
		}
	}
	return out, nil
}

func (f *fakeLeaseManager) AddResource(_ context.Context, _ leases.Lease, _ leases.Resource) error {
	return nil
}

func (f *fakeLeaseManager) DeleteResource(_ context.Context, _ leases.Lease, _ leases.Resource) error {
	return nil
}

func (f *fakeLeaseManager) ListResources(_ context.Context, _ leases.Lease) ([]leases.Resource, error) {
	return nil, nil
}

// drainBuildkitTemporaryLeases mirrors base.NewWorker's startup sweep
// (worker/base/worker.go:214-220) at shutdown so kukebuild's one-build-per-
// process model (#522) doesn't leak temp leases across builds (#1038). The
// fake covers four invariants: the exact label filter is used, only matching
// leases are deleted, gc.flat (a legit BuildKit cache pin) is preserved, and
// SynchronousDelete is passed so containerd's GC scheduler runs the sweep
// before Delete returns — matching internal/ctr/namespaces.go drainLeases.
func TestDrainBuildkitTemporaryLeasesScopesToLabelAndSynchronous(t *testing.T) {
	fake := &fakeLeaseManager{
		leases: []leases.Lease{
			{ID: "tmp-1", Labels: map[string]string{"buildkit/lease.temporary": "2026-06-04"}},
			{ID: "tmp-2", Labels: map[string]string{"buildkit/lease.temporary": "2026-06-04"}},
			{ID: "cache-1", Labels: map[string]string{"containerd.io/gc.flat": "2026-06-04"}},
			{ID: "unrelated", Labels: nil},
		},
	}

	drainBuildkitTemporaryLeases(context.Background(), fake)

	if len(fake.listFilters) != 1 || fake.listFilters[0] != `labels."buildkit/lease.temporary"` {
		t.Fatalf("listFilters = %q, want a single `labels.\"buildkit/lease.temporary\"`", fake.listFilters)
	}
	wantDeleted := map[string]bool{"tmp-1": true, "tmp-2": true}
	if len(fake.deletedIDs) != len(wantDeleted) {
		t.Fatalf("deletedIDs = %v, want exactly %v", fake.deletedIDs, wantDeleted)
	}
	for i, id := range fake.deletedIDs {
		if !wantDeleted[id] {
			t.Errorf("deleted unexpected lease %q", id)
		}
		if !fake.deleteSynced[i] {
			t.Errorf("delete %q was async, want SynchronousDelete so containerd's GC sweep runs before return", id)
		}
	}
	for _, l := range fake.leases {
		if l.ID == "cache-1" || l.ID == "unrelated" {
			continue
		}
		t.Errorf("leftover lease %q after drain", l.ID)
	}
}

// A failed List must not panic and must not delete anything — the cleanup runs
// best-effort. The session-survival rationale is the same as
// internal/ctr/namespaces.go drainLeases's warn-and-return path.
func TestDrainBuildkitTemporaryLeasesIgnoresListError(t *testing.T) {
	fake := &fakeLeaseManager{listErr: errors.New("transient")}
	drainBuildkitTemporaryLeases(context.Background(), fake)
	if len(fake.deletedIDs) != 0 {
		t.Errorf("deletedIDs = %v on list failure, want none", fake.deletedIDs)
	}
}

// The cleanup defer in newController may fire with a cancelled ctx (SIGINT /
// SIGTERM through runBuild's signal handler) — the drain must still reach
// containerd. context.WithoutCancel decouples the request from the parent
// cancellation chain.
func TestDrainBuildkitTemporaryLeasesSurvivesCancelledCtx(t *testing.T) {
	fake := &fakeLeaseManager{
		leases: []leases.Lease{
			{ID: "tmp-1", Labels: map[string]string{"buildkit/lease.temporary": "2026-06-04"}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	drainBuildkitTemporaryLeases(ctx, fake)
	if len(fake.deletedIDs) != 1 || fake.deletedIDs[0] != "tmp-1" {
		t.Errorf("deletedIDs = %v, want [tmp-1] (drain must survive cancellation)", fake.deletedIDs)
	}
}
