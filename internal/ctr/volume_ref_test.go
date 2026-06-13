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
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// provisionVolume creates the on-disk directory WriteVolume would, so the
// resolver's stat-based lookup finds it.
func provisionVolume(t *testing.T, runPath, realm, space, stack, name string) string {
	t.Helper()
	path := fs.VolumePath(runPath, realm, space, stack, name)
	if err := os.MkdirAll(path, 0o770); err != nil {
		t.Fatalf("provision volume %q: %v", path, err)
	}
	return path
}

func TestResolveVolumeMount_SameScopeWalk_MostSpecificWins(t *testing.T) {
	runPath := t.TempDir()
	scope := VolumeScope{Realm: "default", Space: "app", Stack: "web"}

	// Only a realm-scoped volume exists → the walk falls back to it.
	realmPath := provisionVolume(t, runPath, "default", "", "", "data")
	got, err := ResolveVolumeMount(runPath, scope, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data",
	})
	if err != nil {
		t.Fatalf("resolve realm-scoped: %v", err)
	}
	if got.HostPath != realmPath {
		t.Errorf("HostPath = %q, want realm-scoped %q", got.HostPath, realmPath)
	}
	if got.Realm != "default" || got.Space != "" || got.Stack != "" || got.Name != "data" {
		t.Errorf("coords = %+v, want realm-scoped data", got)
	}

	// Now a stack-scoped volume of the same name appears → most-specific wins.
	stackPath := provisionVolume(t, runPath, "default", "app", "web", "data")
	got, err = ResolveVolumeMount(runPath, scope, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data",
	})
	if err != nil {
		t.Fatalf("resolve stack-scoped: %v", err)
	}
	if got.HostPath != stackPath {
		t.Errorf("HostPath = %q, want stack-scoped %q", got.HostPath, stackPath)
	}
	if got.Stack != "web" {
		t.Errorf("coords stack = %q, want web (most-specific)", got.Stack)
	}
}

func TestResolveVolumeMount_CrossScopeRef(t *testing.T) {
	runPath := t.TempDir()
	// A volume owned by a different realm than the resolving container's scope.
	refPath := provisionVolume(t, runPath, "shared", "", "", "cache")

	got, err := ResolveVolumeMount(runPath, VolumeScope{Realm: "default", Space: "app", Stack: "web"},
		intmodel.VolumeMount{
			Kind:      intmodel.VolumeKindVolume,
			Target:    "/cache",
			VolumeRef: &intmodel.VolumeRef{Name: "cache", Realm: "shared"},
		})
	if err != nil {
		t.Fatalf("resolve cross-scope ref: %v", err)
	}
	if got.HostPath != refPath {
		t.Errorf("HostPath = %q, want %q", got.HostPath, refPath)
	}
	if got.Realm != "shared" || got.Name != "cache" {
		t.Errorf("coords = %+v, want shared/cache", got)
	}
}

func TestResolveVolumeMount_NotFound(t *testing.T) {
	runPath := t.TempDir()
	scope := VolumeScope{Realm: "default"}

	if _, err := ResolveVolumeMount(runPath, scope, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Source: "missing", Target: "/x",
	}); !errors.Is(err, internalerrdefs.ErrVolumeNotFound) {
		t.Errorf("same-scope miss err = %v, want ErrVolumeNotFound", err)
	}

	if _, err := ResolveVolumeMount(runPath, scope, intmodel.VolumeMount{
		Kind:      intmodel.VolumeKindVolume,
		Target:    "/x",
		VolumeRef: &intmodel.VolumeRef{Name: "missing", Realm: "default"},
	}); !errors.Is(err, internalerrdefs.ErrVolumeNotFound) {
		t.Errorf("cross-scope miss err = %v, want ErrVolumeNotFound", err)
	}
}

func TestResolveVolumeMount_RejectsNonVolumeKind(t *testing.T) {
	if _, err := ResolveVolumeMount(t.TempDir(), VolumeScope{Realm: "default"},
		intmodel.VolumeMount{Kind: intmodel.VolumeKindBind, Source: "/a", Target: "/b"}); err == nil {
		t.Error("resolve of a bind-kind mount should error, got nil")
	}
}

func TestResolveVolumeMount_FileSquatIsNotAVolume(t *testing.T) {
	runPath := t.TempDir()
	// A file (not a directory) on the volume path must read as absent.
	p := fs.VolumePath(runPath, "default", "", "", "data")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveVolumeMount(runPath, VolumeScope{Realm: "default"}, intmodel.VolumeMount{
		Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data",
	}); !errors.Is(err, internalerrdefs.ErrVolumeNotFound) {
		t.Errorf("file-squat err = %v, want ErrVolumeNotFound", err)
	}
}

func TestResolveVolumeMounts_RewritesVolumeKeepsOthers(t *testing.T) {
	runPath := t.TempDir()
	volPath := provisionVolume(t, runPath, "default", "", "", "data")
	scope := VolumeScope{Realm: "default"}

	in := []intmodel.VolumeMount{
		{Kind: intmodel.VolumeKindBind, Source: "/host/a", Target: "/a"},
		{Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data", ReadOnly: true},
		{Kind: intmodel.VolumeKindTmpfs, Target: "/tmp"},
	}
	out, err := resolveVolumeMounts(runPath, scope, in)
	if err != nil {
		t.Fatalf("resolveVolumeMounts: %v", err)
	}
	// Bind + tmpfs untouched.
	if out[0] != in[0] {
		t.Errorf("bind mount mutated: %+v", out[0])
	}
	if out[2] != in[2] {
		t.Errorf("tmpfs mount mutated: %+v", out[2])
	}
	// Volume rewritten: Source becomes the resolved host dir, ReadOnly kept,
	// Kind unchanged so buildVolumeMounts emits it via the bind path.
	if out[1].Source != volPath {
		t.Errorf("volume Source = %q, want resolved %q", out[1].Source, volPath)
	}
	if !out[1].ReadOnly {
		t.Errorf("volume ReadOnly flag dropped during resolution")
	}
	if out[1].VolumeRef != nil {
		t.Errorf("VolumeRef should be cleared after resolution, got %+v", out[1].VolumeRef)
	}
	// Input slice never mutated.
	if in[1].Source != "data" {
		t.Errorf("input slice mutated: in[1].Source = %q", in[1].Source)
	}
}

func TestResolveVolumeMounts_NoVolumeKind_PassThrough(t *testing.T) {
	in := []intmodel.VolumeMount{{Kind: intmodel.VolumeKindBind, Source: "/a", Target: "/a"}}
	// Empty runPath must be tolerated when no volume-kind entry is present.
	out, err := resolveVolumeMounts("", VolumeScope{}, in)
	if err != nil {
		t.Fatalf("pass-through resolve: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("pass-through mutated slice: %+v", out)
	}
}

// TestVolume_SurvivesContainerRecreate pins AC #6: a sentinel written into a
// referenced Volume survives a container recreate. The Volume directory lives
// in the daemon's persistent volumes/ tree, not the container's writable
// layer, so a recreate simply re-resolves the same reference to the same dir —
// the resolver returns the identical HostPath and the sentinel is intact.
func TestVolume_SurvivesContainerRecreate(t *testing.T) {
	runPath := t.TempDir()
	scope := VolumeScope{Realm: "default", Space: "app", Stack: "web"}
	mount := intmodel.VolumeMount{Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data"}

	volPath := provisionVolume(t, runPath, "default", "app", "web", "data")

	// First create: resolve and write a sentinel into the resolved dir.
	first, err := ResolveVolumeMount(runPath, scope, mount)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	sentinel := filepath.Join(first.HostPath, "sentinel")
	if err := os.WriteFile(sentinel, []byte("survived"), 0o600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	// Recreate: a new container spec re-resolves the same reference. The volume
	// dir is untouched by the (simulated) rootfs teardown.
	second, err := ResolveVolumeMount(runPath, scope, mount)
	if err != nil {
		t.Fatalf("re-resolve after recreate: %v", err)
	}
	if second.HostPath != first.HostPath || second.HostPath != volPath {
		t.Errorf("recreate resolved to %q, want stable %q", second.HostPath, volPath)
	}
	got, err := os.ReadFile(filepath.Join(second.HostPath, "sentinel"))
	if err != nil {
		t.Fatalf("read sentinel after recreate: %v", err)
	}
	if string(got) != "survived" {
		t.Errorf("sentinel = %q, want %q", got, "survived")
	}
}

// TestBuildVolumeMounts_VolumeKindEmitsBind pins AC #4 + the byte-identical
// emission: a resolved volume-kind mount (Source already the on-disk dir)
// produces the exact same OCI bind mount as the equivalent bind-kind entry.
func TestBuildVolumeMounts_VolumeKindEmitsBind(t *testing.T) {
	resolvedDir := "/run/kukeon/data/default/volumes/data"
	volume := buildVolumeMounts([]intmodel.VolumeMount{
		{Kind: intmodel.VolumeKindVolume, Source: resolvedDir, Target: "/data"},
	})
	bind := buildVolumeMounts([]intmodel.VolumeMount{
		{Kind: intmodel.VolumeKindBind, Source: resolvedDir, Target: "/data"},
	})
	if !reflect.DeepEqual(volume, bind) {
		t.Errorf("volume-kind emission %+v != bind-kind emission %+v", volume, bind)
	}
	if len(volume) != 1 || volume[0].Type != "bind" || volume[0].Source != resolvedDir ||
		volume[0].Destination != "/data" {
		t.Errorf("unexpected volume-kind mount: %+v", volume)
	}
}
