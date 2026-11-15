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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
)

func mkVolume(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
}

// TestCheckStateVolumePerms_FlagsMissingGroupWrite seeds volume dirs at the
// realm, space, and stack scope and confirms only the ones missing group rwx
// are named in a WARN row (issue #1416).
func TestCheckStateVolumePerms_FlagsMissingGroupWrite(t *testing.T) {
	runPath := t.TempDir()
	data := filepath.Join(runPath, consts.KukeonMetadataSubdir)
	realm := filepath.Join(data, "default")
	space := filepath.Join(realm, "default")
	stack := filepath.Join(space, "default")

	good := []string{
		filepath.Join(realm, consts.KukeonVolumesSubdir, "realm-ok"),
		filepath.Join(stack, consts.KukeonVolumesSubdir, "stack-ok"),
	}
	for _, p := range good {
		mkVolume(t, p, os.ModeSetgid|0o770)
	}
	broken := filepath.Join(space, consts.KukeonVolumesSubdir, "space-swept")
	mkVolume(t, broken, os.ModeSetgid|0o750)
	// A volumes/ dir inside a cell is not a volume scope and is not walked.
	mkVolume(t, filepath.Join(stack, "cell", consts.KukeonVolumesSubdir, "ignored"), 0o700)

	r := checkStateVolumePerms(&runCtx{runPath: runPath})
	if r.Status != StatusWARN {
		t.Fatalf("expected WARN; got %s (%s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, broken) {
		t.Errorf("Detail should name %s; got %q", broken, r.Detail)
	}
	if !strings.Contains(r.Detail, "1 of 3") {
		t.Errorf("Detail should count 1 of 3 volume dirs; got %q", r.Detail)
	}
	for _, p := range good {
		if strings.Contains(r.Detail, p) {
			t.Errorf("Detail should not name healthy %s; got %q", p, r.Detail)
		}
	}
	if r.Remediation == "" {
		t.Error("WARN row should carry a remediation hint")
	}
}

// TestCheckStateVolumePerms_HealthyIsOK covers the clean case, including the
// root:root 0o770 fallback mode the runner uses without a kukeon GID.
func TestCheckStateVolumePerms_HealthyIsOK(t *testing.T) {
	runPath := t.TempDir()
	realm := filepath.Join(runPath, consts.KukeonMetadataSubdir, "default")
	mkVolume(t, filepath.Join(realm, consts.KukeonVolumesSubdir, "a"), os.ModeSetgid|0o770)
	mkVolume(t, filepath.Join(realm, consts.KukeonVolumesSubdir, "b"), 0o770)

	r := checkStateVolumePerms(&runCtx{runPath: runPath})
	if r.Status != StatusOK {
		t.Fatalf("expected OK; got %s (%s)", r.Status, r.Detail)
	}
	if !strings.Contains(r.Detail, "2 volume dirs") {
		t.Errorf("Detail should count 2 volume dirs; got %q", r.Detail)
	}
}

// TestCheckStateVolumePerms_AbsentDataRootIsOK covers a host that never ran
// `kuke init`.
func TestCheckStateVolumePerms_AbsentDataRootIsOK(t *testing.T) {
	r := checkStateVolumePerms(&runCtx{runPath: filepath.Join(t.TempDir(), "never-existed")})
	if r.Status != StatusOK {
		t.Fatalf("expected OK; got %s (%s)", r.Status, r.Detail)
	}
}
