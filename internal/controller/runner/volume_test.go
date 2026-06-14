//go:build !integration

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

//nolint:testpackage // tests the unexported WriteVolume path against a temp RunPath
package runner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// TestWriteVolume_CreatesContainerWritableDir pins the issue #1018 storage
// contract: the volume lands at <RunPath>/data/<scope>/volumes/<name> as a
// directory (not a file), the per-scope volumes/ container dir is 0o755
// (world-traversable), the volume dir itself is group-writable (0o770 in the
// no-kukeon-group fallback the test Exec uses), and the first write reports
// created=true.
func TestWriteVolume_CreatesContainerWritableDir(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	vol := intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	}

	created, err := r.WriteVolume(vol)
	if err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first write")
	}

	path := fs.VolumePath(runPath, "default", "", "", "data")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat volume dir: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("volume path is not a directory")
	}
	if perm := info.Mode().Perm(); perm != 0o770 {
		t.Errorf("volume dir mode = %o, want 770 (group-writable, no world access)", perm)
	}

	dirInfo, err := os.Stat(fs.VolumesDir(runPath, "default", "", ""))
	if err != nil {
		t.Fatalf("stat volumes dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o755 {
		t.Errorf("volumes/ container dir mode = %o, want 755 (world-traversable)", perm)
	}
}

// TestWriteVolume_OverwriteReportsUpdated confirms a re-apply of an existing
// volume is idempotent (the directory persists) and reports created=false.
func TestWriteVolume_OverwriteReportsUpdated(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	vol := intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"}}

	if _, err := r.WriteVolume(vol); err != nil {
		t.Fatalf("first WriteVolume() error = %v", err)
	}
	// Write a sentinel file inside the volume to prove the re-apply does not
	// wipe container-written contents.
	sentinel := fs.VolumePath(runPath, "default", "", "", "data") + "/keep"
	if err := os.WriteFile(sentinel, []byte("x"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	created, err := r.WriteVolume(vol)
	if err != nil {
		t.Fatalf("second WriteVolume() error = %v", err)
	}
	if created {
		t.Errorf("created = true on re-apply, want false")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("re-apply wiped container contents: %v", err)
	}
}

// TestWriteVolume_RejectsNonDirectorySquatter confirms a stray file at the
// volume path is refused rather than silently treated as a volume.
func TestWriteVolume_RejectsNonDirectorySquatter(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	dir := fs.VolumesDir(runPath, "default", "", "")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir volumes dir: %v", err)
	}
	if err := os.WriteFile(fs.VolumePath(runPath, "default", "", "", "data"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write squatter file: %v", err)
	}

	_, err := r.WriteVolume(intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"}})
	if !errors.Is(err, errdefs.ErrWriteVolume) {
		t.Errorf("WriteVolume() over a non-dir = %v, want ErrWriteVolume", err)
	}
}

// TestGetVolume_RoundTrip confirms WriteVolume then GetVolume returns the same
// scope+name metadata, and that an absent volume reports ErrVolumeNotFound.
func TestGetVolume_RoundTrip(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	md := intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "s1", Stack: "st1"}
	if _, err := r.WriteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}

	got, err := r.GetVolume(intmodel.Volume{Metadata: md})
	if err != nil {
		t.Fatalf("GetVolume() error = %v", err)
	}
	if got.Metadata != md {
		t.Errorf("GetVolume() metadata = %+v, want %+v", got.Metadata, md)
	}

	_, err = r.GetVolume(intmodel.Volume{Metadata: intmodel.VolumeMetadata{Name: "missing", Realm: "r1"}})
	if !errors.Is(err, errdefs.ErrVolumeNotFound) {
		t.Errorf("GetVolume(missing) = %v, want ErrVolumeNotFound", err)
	}
}

// TestListVolumes_SubtreeFilter confirms the prefix-filter semantics: a
// realm-scoped listing surfaces volumes bound to the realm and every deeper
// scope nested within it, matching the ListBlueprints contract.
func TestListVolumes_SubtreeFilter(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	mds := []intmodel.VolumeMetadata{
		{Name: "realm-vol", Realm: "r1"},
		{Name: "space-vol", Realm: "r1", Space: "s1"},
		{Name: "stack-vol", Realm: "r1", Space: "s1", Stack: "st1"},
		{Name: "other-realm", Realm: "r2"},
	}
	for _, md := range mds {
		if _, err := r.WriteVolume(intmodel.Volume{Metadata: md}); err != nil {
			t.Fatalf("WriteVolume(%q) error = %v", md.Name, err)
		}
	}

	got, err := r.ListVolumes("r1", "", "")
	if err != nil {
		t.Fatalf("ListVolumes() error = %v", err)
	}
	names := make([]string, 0, len(got))
	for _, v := range got {
		names = append(names, v.Metadata.Name)
	}
	sort.Strings(names)
	want := []string{"realm-vol", "space-vol", "stack-vol"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("ListVolumes(r1) names = %v, want %v (r2's volume must not surface)", names, want)
	}
}

// TestDeleteVolume_RemovesDirAndReportsNotFound confirms DeleteVolume removes
// the directory (and any container-written contents) and reports
// ErrVolumeNotFound for an absent volume.
func TestDeleteVolume_RemovesDirAndReportsNotFound(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	md := intmodel.VolumeMetadata{Name: "data", Realm: "default"}
	if _, err := r.WriteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}
	path := fs.VolumePath(runPath, "default", "", "", "data")
	if err := os.WriteFile(path+"/keep", []byte("x"), 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	if err := r.DeleteVolume(intmodel.Volume{Metadata: md}); err != nil {
		t.Fatalf("DeleteVolume() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("volume dir still present after delete: stat err = %v", err)
	}

	if err := r.DeleteVolume(intmodel.Volume{Metadata: md}); !errors.Is(err, errdefs.ErrVolumeNotFound) {
		t.Errorf("DeleteVolume(absent) = %v, want ErrVolumeNotFound", err)
	}
}

// TestVolume_ReclaimedByScopeCascade pins the AC's implicit-reclaim invariant
// (issue #1018): a volume's directory lives *inside* its owning scope's
// metadata dir, so the same os.RemoveAll that `purge stack/space/realm
// --cascade` already runs on the scope metadata dir
// (internal/controller/runner/purge_stack.go:120, purge_space.go:112,
// purge_realm.go:181) reclaims the volume with no new mechanism. The test
// asserts containment structurally, then drives the exact RemoveAll the purge
// paths use and confirms the volume is gone.
func TestVolume_ReclaimedByScopeCascade(t *testing.T) {
	cases := []struct {
		name      string
		md        intmodel.VolumeMetadata
		scopeDir  func(runPath string) string
		otherKept intmodel.VolumeMetadata // a volume outside the purged scope
	}{
		{
			name: "stack",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "s1", Stack: "st1"},
			scopeDir: func(runPath string) string {
				return fs.StackMetadataDir(runPath, "r1", "s1", "st1")
			},
			otherKept: intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "s1"},
		},
		{
			name: "space",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: "r1", Space: "s1"},
			scopeDir: func(runPath string) string {
				return fs.SpaceMetadataDir(runPath, "r1", "s1")
			},
			otherKept: intmodel.VolumeMetadata{Name: "data", Realm: "r1"},
		},
		{
			name: "realm",
			md:   intmodel.VolumeMetadata{Name: "data", Realm: "r1"},
			scopeDir: func(runPath string) string {
				return fs.RealmMetadataDir(runPath, "r1")
			},
			otherKept: intmodel.VolumeMetadata{Name: "data", Realm: "r2"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPath := t.TempDir()
			r := newMetadataTestExec(t, runPath, time.Now())

			if _, err := r.WriteVolume(intmodel.Volume{Metadata: tc.md}); err != nil {
				t.Fatalf("WriteVolume(target) error = %v", err)
			}
			if _, err := r.WriteVolume(intmodel.Volume{Metadata: tc.otherKept}); err != nil {
				t.Fatalf("WriteVolume(other) error = %v", err)
			}

			volPath := fs.VolumePath(runPath, tc.md.Realm, tc.md.Space, tc.md.Stack, tc.md.Name)
			scopeDir := tc.scopeDir(runPath)
			if !strings.HasPrefix(volPath, scopeDir+string(os.PathSeparator)) {
				t.Fatalf("volume %q is not under its scope dir %q — cascade purge would not reclaim it", volPath, scopeDir)
			}

			// The exact reclaim the three purge paths perform.
			if err := os.RemoveAll(scopeDir); err != nil {
				t.Fatalf("RemoveAll(scopeDir) error = %v", err)
			}

			if _, err := os.Stat(volPath); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("volume survived scope cascade reclaim: stat err = %v", err)
			}
			// A volume outside the purged scope must be untouched.
			otherPath := fs.VolumePath(runPath, tc.otherKept.Realm, tc.otherKept.Space, tc.otherKept.Stack, tc.otherKept.Name)
			if _, err := os.Stat(otherPath); err != nil {
				t.Errorf("volume outside purged scope was reclaimed: %v", err)
			}
		})
	}
}

// TestVolumeDirInitialPerms pins the two-mode ownership choice (issue #1018):
// when a kukeon group GID is configured the volume dir is setgid root:kukeon
// (group-writable, files inside inherit the group — mirroring
// attachableTTYDirInitialPerms); otherwise it is root:root 0o770 with no
// setgid. The syscall-free pure function is asserted directly so the gid>0
// branch is covered without a root-only chown.
func TestVolumeDirInitialPerms(t *testing.T) {
	mode, gid := volumeDirInitialPerms(4242)
	if gid != 4242 {
		t.Errorf("gid = %d, want 4242", gid)
	}
	if mode != volumeDirRootMode {
		t.Errorf("mode = %v, want %v (setgid root:kukeon)", mode, volumeDirRootMode)
	}
	if mode&os.ModeSetgid == 0 {
		t.Errorf("configured-gid mode missing setgid bit: %v", mode)
	}

	mode, gid = volumeDirInitialPerms(0)
	if gid != 0 {
		t.Errorf("fallback gid = %d, want 0", gid)
	}
	if mode != volumeDirFallbackMode {
		t.Errorf("fallback mode = %v, want %v", mode, volumeDirFallbackMode)
	}
	if mode&os.ModeSetgid != 0 {
		t.Errorf("fallback mode must not set setgid: %v", mode)
	}
}

// TestVolume_NotMistakenForChildScope confirms a scope's volumes/ subdir is in
// the shared reservedScopeSubdirs set, so no childScopeNames consumer mistakes
// it for a phantom child space/stack. A realm-scoped volume creates
// default/volumes/; ListBlueprints walks the realm subtree via childScopeNames
// and must not recurse into volumes/ as a phantom space (which would read the
// daemon into the container-writable volume tree). Same invariant the configs/
// omission tripped in #734 — exercised here for the volumes/ subdir added in
// #1018.
func TestVolume_NotMistakenForChildScope(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	seedBlueprint(t, r, "realm-bp", "default", "", "")
	// A realm-scoped volume creates default/volumes/; it must not surface as a
	// child space when the blueprint walker enumerates the realm subtree.
	if _, err := r.WriteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	}); err != nil {
		t.Fatalf("seed WriteVolume error = %v", err)
	}

	got := listedKeys(t, r, "", "", "")
	want := []string{"default///realm-bp"}
	if !equalStrings(got, want) {
		t.Errorf("ListBlueprints(all) = %v, want %v (volumes/ subdir must be ignored, not traversed as a phantom space)", got, want)
	}
}

// volumeChownFakeClient makes ContainerProcessUID configurable on top of the
// near-empty specHashFakeClient so volumePostCreateChown's uid-resolution hop
// can be driven without a real containerd container. getContainerFn (set by the
// caller) returns a nil Container — the fake's ContainerProcessUID ignores the
// container argument, so a nil value is sufficient.
type volumeChownFakeClient struct {
	*specHashFakeClient

	uid uint32
}

func (c *volumeChownFakeClient) ContainerProcessUID(string, containerd.Container) (uint32, error) {
	return c.uid, nil
}

var _ ctr.Client = (*volumeChownFakeClient)(nil)

func newVolumeChownExec(runPath string, kukeonGID int, uid uint32) *Exec {
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		opts:   Options{RunPath: runPath, KukeonGroupGID: kukeonGID},
		ctrClient: &volumeChownFakeClient{
			specHashFakeClient: &specHashFakeClient{
				getContainerFn: func(string, string) (containerd.Container, error) {
					return nil, nil
				},
			},
			uid: uid,
		},
	}
}

// TestVolumePostCreateChown_GrantsOwnerWriteToNonRootUID locks AC #1/#4 (#1291):
// a kind: volume mount's resolved directory is chowned to the mounting
// container's resolved process uid at container create, so a non-root workload
// with no kukeon-group membership owns — and can write into — the Volume. Runs
// in the no-kukeon-group fallback (0o770) the test Exec uses so it needs no
// privilege: when the suite runs as root the chown to a non-root uid is
// observable; otherwise it self-chowns and the uid assertion is skipped (the
// path still exercises end-to-end), mirroring the attachable chown tests.
func TestVolumePostCreateChown_GrantsOwnerWriteToNonRootUID(t *testing.T) {
	runPath := t.TempDir()

	// Provision the volume dir owned by the current user; WriteVolume's
	// chown-to-root path is gid-gated and a no-op in this fallback config.
	seed := newMetadataTestExec(t, runPath, time.Now())
	if _, err := seed.WriteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	}); err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}
	volPath := fs.VolumePath(runPath, "default", "", "", "data")

	root := os.Geteuid() == 0
	targetUID := os.Geteuid()
	if root {
		targetUID = 1000
	}

	exec := newVolumeChownExec(runPath, 0, uint32(targetUID))
	spec := intmodel.ContainerSpec{
		ID:        "c1",
		RealmName: "default",
		Volumes: []intmodel.VolumeMount{
			{Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data"},
		},
	}
	if err := exec.volumePostCreateChown("default.kukeon.io", spec); err != nil {
		t.Fatalf("volumePostCreateChown() error = %v", err)
	}

	// Owner is the mounting container's uid (observable only as root); the
	// directory stays group-writable 0o770 so the owner-write grant is real.
	assertOwnedBy(t, volPath, targetUID, root)
	info, err := os.Stat(volPath)
	if err != nil {
		t.Fatalf("stat volume dir: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o770 {
		t.Errorf("volume dir mode = %o, want 770 (owner+group writable)", perm)
	}
}

// TestVolumePostCreateChown_PreservesKukeonGroupWrite locks AC #2 (#1291): the
// owner flip must not regress the kukeon-group-write path — the setgid bit and
// the kukeon group survive the chown, so attachable cells (and any other
// kukeon-group member) keep group-write on a shared Volume. Requires root to
// provision a setgid root:<gid> dir and to chown across uids.
func TestVolumePostCreateChown_PreservesKukeonGroupWrite(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to provision a setgid root:kukeon volume dir and chown across uids")
	}
	const kukeonGID = 1000
	const targetUID = 1001
	runPath := t.TempDir()

	r := newVolumeChownExec(runPath, kukeonGID, targetUID)
	if _, err := r.WriteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	}); err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}
	volPath := fs.VolumePath(runPath, "default", "", "", "data")

	if err := r.chownVolumeDirToUID(volPath, targetUID); err != nil {
		t.Fatalf("chownVolumeDirToUID() error = %v", err)
	}

	stat := statSys(t, volPath)
	if int(stat.Uid) != targetUID {
		t.Errorf("owner uid = %d, want %d", stat.Uid, targetUID)
	}
	if int(stat.Gid) != kukeonGID {
		t.Errorf("group gid = %d, want %d (kukeon group must survive the owner flip)", stat.Gid, kukeonGID)
	}
	info, err := os.Stat(volPath)
	if err != nil {
		t.Fatalf("stat volume dir: %v", err)
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Errorf("setgid bit lost after owner chown: %v (group inheritance regressed)", info.Mode())
	}
	if perm := info.Mode().Perm(); perm != 0o770 {
		t.Errorf("volume dir mode = %o, want 770 (group-write preserved)", perm)
	}
}

// TestVolumePostCreateChown_Skips locks the two no-op branches (#1291): a spec
// with no kind: volume mount never reaches containerd (so a failing GetContainer
// would be a bug surfacing as an error), and a root-uid container leaves the
// root-owned dir untouched — the early return means chownVolumeDirToUID(path, 0)
// (which a non-root daemon could not even perform) never runs.
func TestVolumePostCreateChown_Skips(t *testing.T) {
	runPath := t.TempDir()
	seed := newMetadataTestExec(t, runPath, time.Now())
	if _, err := seed.WriteVolume(intmodel.Volume{
		Metadata: intmodel.VolumeMetadata{Name: "data", Realm: "default"},
	}); err != nil {
		t.Fatalf("WriteVolume() error = %v", err)
	}

	t.Run("no volume mount: no containerd round-trip", func(t *testing.T) {
		// GetContainer is left at the specHashFakeClient default (NotFound); if
		// the function reached it the call would error.
		exec := &Exec{
			ctx:       context.Background(),
			logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			opts:      Options{RunPath: runPath},
			ctrClient: &volumeChownFakeClient{specHashFakeClient: &specHashFakeClient{}, uid: 1000},
		}
		spec := intmodel.ContainerSpec{
			ID:        "c1",
			RealmName: "default",
			Volumes: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindBind, Source: "/host", Target: "/in"},
			},
		}
		if err := exec.volumePostCreateChown("default.kukeon.io", spec); err != nil {
			t.Errorf("want nil (no kind: volume mount, ctrClient untouched), got %v", err)
		}
	})

	t.Run("root container uid: dir left root-owned", func(t *testing.T) {
		exec := newVolumeChownExec(runPath, 0, 0)
		spec := intmodel.ContainerSpec{
			ID:        "c1",
			RealmName: "default",
			Volumes: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindVolume, Source: "data", Target: "/data"},
			},
		}
		if err := exec.volumePostCreateChown("default.kukeon.io", spec); err != nil {
			t.Errorf("want nil (root uid short-circuits the chown), got %v", err)
		}
	})
}
