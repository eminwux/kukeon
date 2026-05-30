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

package lifecycle_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
)

// stubOwnership swaps the root-requiring ownership step for one that records
// the directory it was handed, so the test runs without root or a real kukeon
// group. Returns a pointer to the captured dir and registers the restore.
func stubOwnership(t *testing.T, ret error) *string {
	t.Helper()
	var got string
	restore := lifecycle.SetRunDirOwnershipForTesting(func(dir string) error {
		got = dir
		return ret
	})
	t.Cleanup(restore)
	return &got
}

func TestEnsureSocketDir_CreatesMissingParent(t *testing.T) {
	// Parent does not exist yet — mirrors a tmpfs clear after reboot.
	socketPath := filepath.Join(t.TempDir(), "kukeon", "kukeond.sock")
	wantDir := filepath.Dir(socketPath)
	gotDir := stubOwnership(t, nil)

	if err := lifecycle.EnsureSocketDir(socketPath); err != nil {
		t.Fatalf("EnsureSocketDir: %v", err)
	}

	info, err := os.Stat(wantDir)
	if err != nil {
		t.Fatalf("stat created dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("%q is not a directory", wantDir)
	}
	if *gotDir != wantDir {
		t.Fatalf("ownership applied to %q, want %q", *gotDir, wantDir)
	}
}

func TestEnsureSocketDir_IdempotentOnExistingDir(t *testing.T) {
	// Directory already present (healthy host) — re-running must succeed.
	dir := filepath.Join(t.TempDir(), "kukeon")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("pre-create dir: %v", err)
	}
	socketPath := filepath.Join(dir, "kukeond.sock")
	stubOwnership(t, nil)

	for i := range 2 {
		if err := lifecycle.EnsureSocketDir(socketPath); err != nil {
			t.Fatalf("EnsureSocketDir (pass %d): %v", i, err)
		}
	}
}

func TestEnsureSocketDir_PropagatesOwnershipError(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "kukeon", "kukeond.sock")
	sentinel := errors.New("chown boom")
	stubOwnership(t, sentinel)

	err := lifecycle.EnsureSocketDir(socketPath)
	if !errors.Is(err, sentinel) {
		t.Fatalf("EnsureSocketDir error = %v, want wrap of %v", err, sentinel)
	}
}
