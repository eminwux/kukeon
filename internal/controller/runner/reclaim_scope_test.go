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

//nolint:testpackage // tests the unexported reclaimScopeMetadata cascade path
package runner

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// retainSpec / deleteSpec build a Volume carrier with the two reclaim policies.
func retainVol(md intmodel.VolumeMetadata) intmodel.Volume {
	return intmodel.Volume{Metadata: md, Spec: intmodel.VolumeSpec{ReclaimPolicy: intmodel.ReclaimRetain}}
}

func deleteVol(md intmodel.VolumeMetadata) intmodel.Volume {
	return intmodel.Volume{Metadata: md} // empty policy == Delete
}

// seedScopeMetadataFile writes a scope's own metadata.json so a test can assert
// the cascade removes it (the marker list/get gate on to report the scope gone).
func seedScopeMetadataFile(t *testing.T, scopeDir string) {
	t.Helper()
	if err := os.MkdirAll(scopeDir, 0o755); err != nil {
		t.Fatalf("mkdir scope dir %q: %v", scopeDir, err)
	}
	if err := os.WriteFile(filepath.Join(scopeDir, consts.KukeonMetadataFile), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write scope metadata.json: %v", err)
	}
}

// seedNonVolumeContent writes a child-scope dir and a secrets/ entry under the
// scope so a test can assert non-volume content is fully reclaimed.
func seedNonVolumeContent(t *testing.T, scopeDir string) (childDir, secretFile string) {
	t.Helper()
	childDir = filepath.Join(scopeDir, "child")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatalf("mkdir child scope: %v", err)
	}
	if err := os.WriteFile(filepath.Join(childDir, consts.KukeonMetadataFile), []byte("{}"), 0o644); err != nil {
		t.Fatalf("write child metadata.json: %v", err)
	}
	secretsDir := filepath.Join(scopeDir, consts.KukeonSecretsSubdir)
	if err := os.MkdirAll(secretsDir, 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	secretFile = filepath.Join(secretsDir, "sec1")
	if err := os.WriteFile(secretFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return childDir, secretFile
}

func assertGone(t *testing.T, path, label string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s survived cascade reclaim (%q): stat err = %v", label, path, err)
	}
}

func assertExists(t *testing.T, path, label string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s was reclaimed but should survive (%q): %v", label, path, err)
	}
}

// TestReclaimScopeMetadata_PreservesRetainedAtScope drives the exact selective
// reclaim every one of the three purge paths (purge_stack/space/realm) runs —
// they all delegate to reclaimScopeMetadata — and confirms a reclaimPolicy:
// Retain volume at the purged scope survives while the scope's own metadata,
// child scopes, secrets, and a sibling default-policy volume are all reclaimed.
func TestReclaimScopeMetadata_PreservesRetainedAtScope(t *testing.T) {
	cases := []struct {
		name     string
		retained intmodel.VolumeMetadata
		deflt    intmodel.VolumeMetadata
		scopeDir func(runPath string) string
	}{
		{
			name:     "stack",
			retained: intmodel.VolumeMetadata{Name: "keep", Realm: "r1", Space: "s1", Stack: "st1"},
			deflt:    intmodel.VolumeMetadata{Name: "drop", Realm: "r1", Space: "s1", Stack: "st1"},
			scopeDir: func(rp string) string { return fs.StackMetadataDir(rp, "r1", "s1", "st1") },
		},
		{
			name:     "space",
			retained: intmodel.VolumeMetadata{Name: "keep", Realm: "r1", Space: "s1"},
			deflt:    intmodel.VolumeMetadata{Name: "drop", Realm: "r1", Space: "s1"},
			scopeDir: func(rp string) string { return fs.SpaceMetadataDir(rp, "r1", "s1") },
		},
		{
			name:     "realm",
			retained: intmodel.VolumeMetadata{Name: "keep", Realm: "r1"},
			deflt:    intmodel.VolumeMetadata{Name: "drop", Realm: "r1"},
			scopeDir: func(rp string) string { return fs.RealmMetadataDir(rp, "r1") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runPath := t.TempDir()
			r := newMetadataTestExec(t, runPath, time.Now())
			scopeDir := tc.scopeDir(runPath)

			seedScopeMetadataFile(t, scopeDir)
			childDir, secretFile := seedNonVolumeContent(t, scopeDir)
			if _, err := r.WriteVolume(retainVol(tc.retained)); err != nil {
				t.Fatalf("WriteVolume(retained) error = %v", err)
			}
			if _, err := r.WriteVolume(deleteVol(tc.deflt)); err != nil {
				t.Fatalf("WriteVolume(default) error = %v", err)
			}

			if err := r.reclaimScopeMetadata(scopeDir); err != nil {
				t.Fatalf("reclaimScopeMetadata error = %v", err)
			}

			md := tc.retained
			retainedPath := fs.VolumePath(runPath, md.Realm, md.Space, md.Stack, md.Name)
			retainedManifest := fs.VolumeMetaPath(runPath, md.Realm, md.Space, md.Stack, md.Name)
			dropPath := fs.VolumePath(runPath, tc.deflt.Realm, tc.deflt.Space, tc.deflt.Stack, tc.deflt.Name)

			assertExists(t, scopeDir, "scope skeleton")
			assertExists(t, retainedPath, "retained volume dir")
			assertExists(t, retainedManifest, "retained volume manifest")
			assertGone(t, dropPath, "default-policy volume")
			assertGone(t, filepath.Join(scopeDir, consts.KukeonMetadataFile), "scope metadata.json")
			assertGone(t, childDir, "child scope")
			assertGone(t, secretFile, "secret")

			// The retained volume is still discoverable and reports its policy.
			got, err := r.GetVolume(intmodel.Volume{Metadata: md})
			if err != nil {
				t.Fatalf("GetVolume(retained) after reclaim error = %v", err)
			}
			if got.Spec.ReclaimPolicy != intmodel.ReclaimRetain {
				t.Errorf("retained volume reclaimPolicy = %q, want %q", got.Spec.ReclaimPolicy, intmodel.ReclaimRetain)
			}
			vols, err := r.ListVolumes(md.Realm, md.Space, md.Stack)
			if err != nil {
				t.Fatalf("ListVolumes after reclaim error = %v", err)
			}
			if len(vols) != 1 || vols[0].Metadata.Name != md.Name {
				t.Errorf("ListVolumes = %+v, want only retained %q", vols, md.Name)
			}
			// And still deletable, leaving no orphan manifest.
			if err := r.DeleteVolume(intmodel.Volume{Metadata: md}); err != nil {
				t.Fatalf("DeleteVolume(retained) after reclaim error = %v", err)
			}
			assertGone(t, retainedPath, "retained volume dir after delete")
			assertGone(t, retainedManifest, "retained volume manifest after delete")
		})
	}
}

// TestReclaimScopeMetadata_PreservesDeeplyNestedRetained confirms a stack-scoped
// Retain volume survives a *realm* cascade purge: the realm, space, and stack
// metadata.json files are all reclaimed (the scopes report gone), the skeleton
// dirs down to the volume survive, and the volume itself plus its manifest are
// preserved and remain discoverable under the realm filter.
func TestReclaimScopeMetadata_PreservesDeeplyNestedRetained(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	realmDir := fs.RealmMetadataDir(runPath, "r1")
	spaceDir := fs.SpaceMetadataDir(runPath, "r1", "s1")
	stackDir := fs.StackMetadataDir(runPath, "r1", "s1", "st1")
	seedScopeMetadataFile(t, realmDir)
	seedScopeMetadataFile(t, spaceDir)
	seedScopeMetadataFile(t, stackDir)

	md := intmodel.VolumeMetadata{Name: "deep", Realm: "r1", Space: "s1", Stack: "st1"}
	if _, err := r.WriteVolume(retainVol(md)); err != nil {
		t.Fatalf("WriteVolume(deep retained) error = %v", err)
	}
	// A sibling default volume at the realm level must still be reclaimed.
	shallow := intmodel.VolumeMetadata{Name: "drop", Realm: "r1"}
	if _, err := r.WriteVolume(deleteVol(shallow)); err != nil {
		t.Fatalf("WriteVolume(shallow default) error = %v", err)
	}

	if err := r.reclaimScopeMetadata(realmDir); err != nil {
		t.Fatalf("reclaimScopeMetadata(realm) error = %v", err)
	}

	assertExists(t, fs.VolumePath(runPath, "r1", "s1", "st1", "deep"), "deep retained volume")
	assertExists(t, fs.VolumeMetaPath(runPath, "r1", "s1", "st1", "deep"), "deep retained manifest")
	assertExists(t, stackDir, "stack skeleton")
	assertExists(t, spaceDir, "space skeleton")
	assertExists(t, realmDir, "realm skeleton")
	assertGone(t, filepath.Join(realmDir, consts.KukeonMetadataFile), "realm metadata.json")
	assertGone(t, filepath.Join(spaceDir, consts.KukeonMetadataFile), "space metadata.json")
	assertGone(t, filepath.Join(stackDir, consts.KukeonMetadataFile), "stack metadata.json")
	assertGone(t, fs.VolumePath(runPath, "r1", "", "", "drop"), "shallow default volume")

	vols, err := r.ListVolumes("r1", "", "")
	if err != nil {
		t.Fatalf("ListVolumes(r1) error = %v", err)
	}
	if len(vols) != 1 || vols[0].Metadata.Name != "deep" {
		t.Errorf("ListVolumes(r1) = %+v, want only deep retained volume", vols)
	}
}

// TestReclaimScopeMetadata_NoRetainedRemovesWholeScope confirms the fast path:
// a scope with only default-policy volumes (no manifests) is removed wholesale,
// byte-identical to the step-1 blunt os.RemoveAll — no skeleton left behind.
func TestReclaimScopeMetadata_NoRetainedRemovesWholeScope(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	scopeDir := fs.StackMetadataDir(runPath, "r1", "s1", "st1")
	seedScopeMetadataFile(t, scopeDir)
	md := intmodel.VolumeMetadata{Name: "drop", Realm: "r1", Space: "s1", Stack: "st1"}
	if _, err := r.WriteVolume(deleteVol(md)); err != nil {
		t.Fatalf("WriteVolume(default) error = %v", err)
	}

	if err := r.reclaimScopeMetadata(scopeDir); err != nil {
		t.Fatalf("reclaimScopeMetadata error = %v", err)
	}
	assertGone(t, scopeDir, "scope dir (no retained volume)")
}

// TestReclaimScopeMetadata_RetainOutsideScopeUntouched confirms the reclaim only
// touches the named scope: a Retain volume in a sibling scope is irrelevant and
// the purge of one scope never reaches another.
func TestReclaimScopeMetadata_RetainOutsideScopeUntouched(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())

	purged := fs.StackMetadataDir(runPath, "r1", "s1", "st1")
	seedScopeMetadataFile(t, purged)
	if _, err := r.WriteVolume(deleteVol(intmodel.VolumeMetadata{Name: "drop", Realm: "r1", Space: "s1", Stack: "st1"})); err != nil {
		t.Fatalf("WriteVolume error = %v", err)
	}
	// A retained volume in a different stack.
	other := intmodel.VolumeMetadata{Name: "keep", Realm: "r1", Space: "s1", Stack: "st2"}
	if _, err := r.WriteVolume(retainVol(other)); err != nil {
		t.Fatalf("WriteVolume(other) error = %v", err)
	}

	if err := r.reclaimScopeMetadata(purged); err != nil {
		t.Fatalf("reclaimScopeMetadata error = %v", err)
	}
	assertGone(t, purged, "purged stack")
	assertExists(t, fs.VolumePath(runPath, "r1", "s1", "st2", "keep"), "retained volume in sibling stack")
}

// TestWriteVolume_RetainPersistsManifest pins the persistence contract: a Retain
// volume writes a root-only manifest GetVolume echoes back, re-applying with the
// policy flipped to Delete drops the manifest, and a corrupt manifest surfaces
// as an error rather than a silent downgrade.
func TestWriteVolume_RetainPersistsManifest(t *testing.T) {
	runPath := t.TempDir()
	r := newMetadataTestExec(t, runPath, time.Now())
	md := intmodel.VolumeMetadata{Name: "data", Realm: "r1"}

	if _, err := r.WriteVolume(retainVol(md)); err != nil {
		t.Fatalf("WriteVolume(retain) error = %v", err)
	}
	manifestPath := fs.VolumeMetaPath(runPath, "r1", "", "", "data")
	info, err := os.Stat(manifestPath)
	if err != nil {
		t.Fatalf("stat manifest: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("manifest mode = %o, want 600 (root-only)", perm)
	}
	if dirInfo, statErr := os.Stat(filepath.Dir(manifestPath)); statErr != nil {
		t.Errorf("stat volume-meta dir: %v", statErr)
	} else if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("volume-meta dir mode = %o, want 700 (root-only)", perm)
	}

	got, err := r.GetVolume(intmodel.Volume{Metadata: md})
	if err != nil {
		t.Fatalf("GetVolume error = %v", err)
	}
	if got.Spec.ReclaimPolicy != intmodel.ReclaimRetain {
		t.Errorf("GetVolume reclaimPolicy = %q, want Retain", got.Spec.ReclaimPolicy)
	}

	// Re-apply with Delete drops the manifest.
	if _, err := r.WriteVolume(intmodel.Volume{
		Metadata: md,
		Spec:     intmodel.VolumeSpec{ReclaimPolicy: intmodel.ReclaimDelete},
	}); err != nil {
		t.Fatalf("WriteVolume(delete re-apply) error = %v", err)
	}
	if _, err := os.Stat(manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("manifest survived Delete re-apply: stat err = %v", err)
	}
	got, err = r.GetVolume(intmodel.Volume{Metadata: md})
	if err != nil {
		t.Fatalf("GetVolume after delete re-apply error = %v", err)
	}
	if got.Spec.ReclaimPolicy != "" {
		t.Errorf("reclaimPolicy after Delete re-apply = %q, want empty", got.Spec.ReclaimPolicy)
	}

	// A corrupt manifest is an error, not a silent Delete downgrade.
	if _, err := r.WriteVolume(retainVol(md)); err != nil {
		t.Fatalf("WriteVolume(retain again) error = %v", err)
	}
	if err := os.WriteFile(manifestPath, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt manifest: %v", err)
	}
	if _, err := r.GetVolume(intmodel.Volume{Metadata: md}); !errors.Is(err, errdefs.ErrGetVolume) {
		t.Errorf("GetVolume over corrupt manifest = %v, want ErrGetVolume", err)
	}
}
