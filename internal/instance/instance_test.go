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

package instance_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/instance"
)

func TestVerifyOrWriteFreshRunPathWritesMetadata(t *testing.T) {
	runPath := t.TempDir()

	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon"); err != nil {
		t.Fatalf("VerifyOrWrite on fresh runPath: %v", err)
	}

	got, found, err := instance.Load(runPath)
	if err != nil {
		t.Fatalf("Load after VerifyOrWrite: %v", err)
	}
	if !found {
		t.Fatalf("Load: found=false, want true after VerifyOrWrite wrote the file")
	}
	if got.ContainerdNamespaceSuffix != "kukeon.io" {
		t.Errorf("ContainerdNamespaceSuffix: got %q, want %q",
			got.ContainerdNamespaceSuffix, "kukeon.io")
	}
	if got.CgroupRoot != "/kukeon" {
		t.Errorf("CgroupRoot: got %q, want %q", got.CgroupRoot, "/kukeon")
	}
}

func TestVerifyOrWriteMatchingMetadataIsNoOp(t *testing.T) {
	runPath := t.TempDir()
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon"); err != nil {
		t.Fatalf("first VerifyOrWrite: %v", err)
	}

	infoBefore, statErr := os.Stat(instance.Path(runPath))
	if statErr != nil {
		t.Fatalf("stat: %v", statErr)
	}

	// Re-running with the same values must succeed without changing the
	// stored payload.
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon"); err != nil {
		t.Fatalf("second VerifyOrWrite: %v", err)
	}

	got, found, loadErr := instance.Load(runPath)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if !found {
		t.Fatalf("Load: found=false, want true after second VerifyOrWrite")
	}
	if got.ContainerdNamespaceSuffix != "kukeon.io" || got.CgroupRoot != "/kukeon" {
		t.Errorf("payload mutated by no-op rewrite: got %+v", got)
	}
	// Size is a stable invariant: the JSON encoding is deterministic.
	infoAfter, statAfterErr := os.Stat(instance.Path(runPath))
	if statAfterErr != nil {
		t.Fatalf("stat after: %v", statAfterErr)
	}
	if infoBefore.Size() != infoAfter.Size() {
		t.Errorf("metadata file size changed: before=%d after=%d",
			infoBefore.Size(), infoAfter.Size())
	}
}

func TestVerifyOrWriteMismatchedSuffixRefuses(t *testing.T) {
	runPath := t.TempDir()
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := instance.VerifyOrWrite(runPath, "dev.kukeon.io", "/kukeon")
	if err == nil {
		t.Fatalf("VerifyOrWrite with conflicting suffix: err = nil, want ErrInstanceMismatch")
	}
	if !errors.Is(err, errdefs.ErrInstanceMismatch) {
		t.Errorf("error: got %v, want wrapped %v", err, errdefs.ErrInstanceMismatch)
	}
}

func TestVerifyOrWriteMismatchedCgroupRootRefuses(t *testing.T) {
	runPath := t.TempDir()
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon-dev")
	if err == nil {
		t.Fatalf("VerifyOrWrite with conflicting cgroup root: err = nil, want ErrInstanceMismatch")
	}
	if !errors.Is(err, errdefs.ErrInstanceMismatch) {
		t.Errorf("error: got %v, want wrapped %v", err, errdefs.ErrInstanceMismatch)
	}
}

func TestVerifyOrWriteCanonicalizesTrailingSlashAndWhitespace(t *testing.T) {
	// A first start with "/kukeon-dev/" (operator habit) must compare
	// equal to a second start with "/kukeon-dev" (post-canonicalization
	// inside consts.ConfigureRuntime). Same for surrounding whitespace
	// on either input. Without canonicalization in VerifyOrWrite, the
	// raw viper-string call sites would trip a spurious ErrInstanceMismatch.
	runPath := t.TempDir()
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon-dev/"); err != nil {
		t.Fatalf("seed with trailing slash: %v", err)
	}
	if err := instance.VerifyOrWrite(runPath, "kukeon.io", "/kukeon-dev"); err != nil {
		t.Errorf("re-verify with canonical form: got %v, want nil", err)
	}
	if err := instance.VerifyOrWrite(runPath, "  kukeon.io  ", "  /kukeon-dev///  "); err != nil {
		t.Errorf("re-verify with surrounding whitespace + redundant slashes: got %v, want nil", err)
	}

	// And the stored payload should be the canonical form, not the raw
	// trailing-slash input — so a `cat /opt/kukeon/.kukeon-instance.json`
	// matches what consts.KukeonCgroupRoot holds in-process.
	got, found, loadErr := instance.Load(runPath)
	if loadErr != nil {
		t.Fatalf("Load: %v", loadErr)
	}
	if !found {
		t.Fatalf("Load: found=false, want true")
	}
	if got.CgroupRoot != "/kukeon-dev" {
		t.Errorf("stored CgroupRoot: got %q, want %q (canonical form)", got.CgroupRoot, "/kukeon-dev")
	}
	if got.ContainerdNamespaceSuffix != "kukeon.io" {
		t.Errorf("stored ContainerdNamespaceSuffix: got %q, want %q",
			got.ContainerdNamespaceSuffix, "kukeon.io")
	}
}

func TestLoadAbsentReturnsNotFoundNoError(t *testing.T) {
	runPath := t.TempDir()
	_, found, err := instance.Load(runPath)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if found {
		t.Errorf("Load on empty dir: found=true, want false")
	}
}

func TestLoadInvalidJSONIsServerConfigurationInvalid(t *testing.T) {
	runPath := t.TempDir()
	// Write garbage to the metadata path.
	if err := os.WriteFile(
		filepath.Join(runPath, instance.MetadataFile),
		[]byte("not-json"),
		0o600,
	); err != nil {
		t.Fatalf("seed bad metadata: %v", err)
	}

	_, _, err := instance.Load(runPath)
	if err == nil {
		t.Fatalf("Load on garbage: err = nil, want ErrServerConfigurationInvalid")
	}
	if !errors.Is(err, errdefs.ErrServerConfigurationInvalid) {
		t.Errorf("error: got %v, want wrapped %v",
			err, errdefs.ErrServerConfigurationInvalid)
	}
}

func TestPathPlacesFileUnderRunPath(t *testing.T) {
	got := instance.Path("/opt/kukeon")
	want := "/opt/kukeon/" + instance.MetadataFile
	if got != want {
		t.Errorf("Path: got %q, want %q", got, want)
	}
}
