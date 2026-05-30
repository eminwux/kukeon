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

package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
)

// TestStageKukepauseBinary_ReusesExisting locks the idempotent-stage path:
// when the destination is already a usable executable, the helper returns
// without re-copying. Load-bearing because every CreateCell trip hits this
// code path — re-copying the binary per cell would be wasted work.
func TestStageKukepauseBinary_ReusesExisting(t *testing.T) {
	runPath := t.TempDir()
	dstDir := filepath.Join(runPath, kukettyBinaryStagedSubdir)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dst := filepath.Join(dstDir, ctr.RootContainerPauseBinaryName)
	if err := os.WriteFile(dst, []byte("\x7fELF stub"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	got, err := stageKukepauseBinary(runPath)
	if err != nil {
		t.Fatalf("stageKukepauseBinary: %v", err)
	}
	if got != dst {
		t.Fatalf("stageKukepauseBinary = %q, want %q", got, dst)
	}

	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "\x7fELF stub" {
		t.Fatalf("dst contents = %q, want stub (helper re-copied)", data)
	}
}
