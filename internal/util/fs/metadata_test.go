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

package fs_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestMetadataRoot(t *testing.T) {
	got := fs.MetadataRoot("/opt/kukeon")
	want := "/opt/kukeon/" + consts.KukeonMetadataSubdir
	if got != want {
		t.Errorf("MetadataRoot = %q, want %q", got, want)
	}
}

func TestRealmMetadataDir_UnderMetadataRoot(t *testing.T) {
	// The realm dir must live under the metadata root, not directly under
	// the RunPath: the reconcile loop (#473) walks MetadataRoot to enumerate
	// realms, so anything outside it is invisible to the walker.
	got := fs.RealmMetadataDir("/opt/kukeon", "realm")
	want := "/opt/kukeon/" + consts.KukeonMetadataSubdir + "/realm"
	if got != want {
		t.Errorf("RealmMetadataDir = %q, want %q", got, want)
	}
}

func TestContainerTTYDir(t *testing.T) {
	got := fs.ContainerTTYDir("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	want := "/opt/kukeon/" + consts.KukeonMetadataSubdir +
		"/realm/space/stack/cell/c1/" + consts.KukeonContainerTTYDir
	if got != want {
		t.Errorf("ContainerTTYDir = %q, want %q", got, want)
	}
}

func TestContainerSocketPath(t *testing.T) {
	got := fs.ContainerSocketPath("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	want := "/opt/kukeon/" + consts.KukeonMetadataSubdir + "/realm/space/stack/cell/c1/" +
		consts.KukeonContainerTTYDir + "/" + consts.KukeonContainerSocketFile
	if got != want {
		t.Errorf("ContainerSocketPath = %q, want %q", got, want)
	}
}

func TestContainerSocketSymlinkPath_Layout(t *testing.T) {
	// The SUN_PATH-safe symlink lives under <RunPath>/s/<short> — a single-
	// letter parent so the budget goes to the per-container short id, not
	// the directory. Anchored here so a refactor that drifts the layout
	// (e.g. promotes the symlink dir to a per-realm subtree) breaks the
	// test instead of silently violating the SUN_PATH bound.
	got := fs.ContainerSocketSymlinkPath("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	wantPrefix := "/opt/kukeon/" + consts.KukeonSocketSymlinkSubdir + "/"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("symlink path = %q, want prefix %q", got, wantPrefix)
	}
}

func TestContainerSocketSymlinkPath_Deterministic(t *testing.T) {
	// The short id is sha256[:8]-derived, so the same tuple must round-trip
	// to the same path across calls. `kuke attach` re-derives the path from
	// the container doc rather than reading the symlink dirent, so a
	// process restart that changed the hash would silently break attach.
	a := fs.ContainerSocketSymlinkPath("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	b := fs.ContainerSocketSymlinkPath("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	if a != b {
		t.Errorf("symlink path is not deterministic: a=%q b=%q", a, b)
	}
}

func TestContainerSocketSymlinkPath_DistinctPerContainer(t *testing.T) {
	// Two distinct identity tuples must map to distinct short ids; sharing
	// would collapse two containers onto the same dirent and the second
	// provision would silently rewrite the first's target. Vary exactly
	// one position per row so a coverage gap surfaces clearly.
	//
	// The "pipe-in-name" row is the regression guard for the separator
	// choice itself: "|" is a legal name byte (only "_" and "/" are
	// rejected by naming.ValidateRealmName / naming.ValidateHierarchyName),
	// so any non-"_"/"/" separator would let realm "a|b" + space "c"
	// collide with realm "a" + space "b|c". The "concat" row is the
	// classic split-shift case ("ab"+"c" vs "a"+"bc") that any
	// fixed-byte separator catches.
	type pair struct {
		name string
		a, b [5]string
	}
	pairs := []pair{
		{"container", [5]string{"r", "s", "k", "c", "c1"}, [5]string{"r", "s", "k", "c", "c2"}},
		{"cell", [5]string{"r", "s", "k", "cA", "c1"}, [5]string{"r", "s", "k", "cB", "c1"}},
		{"stack", [5]string{"r", "s", "kA", "c", "c1"}, [5]string{"r", "s", "kB", "c", "c1"}},
		{"space", [5]string{"r", "sA", "k", "c", "c1"}, [5]string{"r", "sB", "k", "c", "c1"}},
		{"realm", [5]string{"rA", "s", "k", "c", "c1"}, [5]string{"rB", "s", "k", "c", "c1"}},
		{"concat", [5]string{"ab", "c", "k", "c", "c1"}, [5]string{"a", "bc", "k", "c", "c1"}},
		{"pipe-in-name", [5]string{"a|b", "c", "k", "c", "c1"}, [5]string{"a", "b|c", "k", "c", "c1"}},
	}
	for _, p := range pairs {
		a := fs.ContainerSocketSymlinkPath("/opt/kukeon", p.a[0], p.a[1], p.a[2], p.a[3], p.a[4])
		b := fs.ContainerSocketSymlinkPath("/opt/kukeon", p.b[0], p.b[1], p.b[2], p.b[3], p.b[4])
		if a == b {
			t.Errorf("[%s] distinct tuples collided: a=%q b=%q", p.name, a, b)
		}
	}
}

func TestContainerSocketSymlinkPath_FitsSUNPath(t *testing.T) {
	// AC #1 of issue #521: for any well-formed tuple under a RunPath
	// ≤ 60 bytes, the resolved host-side socket path must stay under
	// consts.KukeonMaxSocketPath so `connect(2)` never trips SUN_PATH
	// overflow at `kuke attach` time. The symlink path budget is
	// independent of how deep the metadata layout becomes — that is the
	// whole point of routing the dial through it.
	longRunPath := "/" + strings.Repeat("x", 59) // 60 bytes total
	longName := strings.Repeat("z", 64)          // operator-supplied names can be long
	got := fs.ContainerSocketSymlinkPath(longRunPath, longName, longName, longName, longName, longName)
	if len(got) > consts.KukeonMaxSocketPath {
		t.Errorf("symlink path overflows SUN_PATH: %d > %d (path=%q)",
			len(got), consts.KukeonMaxSocketPath, got)
	}
}

func TestContainerMetadataDir_AnchorsContainerTTYDir(t *testing.T) {
	// The tty dir (and the socket inside it) must live under the
	// container's metadata dir, not anywhere else — `kuke attach` (#66)
	// and the OCI bind-mount source rely on this being the single source
	// of truth.
	dir := fs.ContainerMetadataDir("/opt/kukeon", "r", "s", "st", "c", "co")
	ttyDir := fs.ContainerTTYDir("/opt/kukeon", "r", "s", "st", "c", "co")
	wantTTY := dir + "/" + consts.KukeonContainerTTYDir
	if ttyDir != wantTTY {
		t.Errorf("tty dir = %q, want it under container metadata dir = %q", ttyDir, wantTTY)
	}
	sock := fs.ContainerSocketPath("/opt/kukeon", "r", "s", "st", "c", "co")
	wantSock := wantTTY + "/" + consts.KukeonContainerSocketFile
	if sock != wantSock {
		t.Errorf("socket path = %q, want it under tty dir = %q", sock, wantSock)
	}
}

func TestDetectMetadataVersion(t *testing.T) {
	tests := []struct {
		name        string
		raw         []byte
		wantVersion v1beta1.Version
		wantErr     bool
	}{
		{
			name: "valid v1beta1 metadata",
			raw: func() []byte {
				doc := map[string]interface{}{
					"apiVersion": "v1beta1",
					"kind":       "Realm",
				}
				data, _ := json.Marshal(doc)
				return data
			}(),
			wantVersion: v1beta1.APIVersionV1Beta1,
			wantErr:     false,
		},
		{
			name: "empty apiVersion defaults to v1beta1",
			raw: func() []byte {
				doc := map[string]interface{}{
					"apiVersion": "",
					"kind":       "Realm",
				}
				data, _ := json.Marshal(doc)
				return data
			}(),
			wantVersion: apischeme.VersionV1Beta1,
			wantErr:     false,
		},
		{
			name:    "invalid JSON",
			raw:     []byte("{invalid json}"),
			wantErr: true,
		},
		{
			name:    "empty bytes",
			raw:     []byte(""),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := fs.DetectMetadataVersion(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("DetectMetadataVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && version != tt.wantVersion {
				t.Errorf("DetectMetadataVersion() version = %v, want %v", version, tt.wantVersion)
			}
		})
	}
}
