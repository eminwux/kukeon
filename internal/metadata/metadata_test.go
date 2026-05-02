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

package metadata_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/metadata"
)

// TestWriteMetadata_ParentDirModeIsGroupTraversable locks the dir mode
// applied to the metadata file's parent: 02750 (setgid + rwx-r-x---) so
// the kukeon group can traverse newly-created cell and per-container
// metadata directories on the host. The pre-fix mode was 0700 root-only,
// which combined with the parent's setgid bit produced 02700 — blocking
// kukeon-group operators from reaching the per-container sbsh socket even
// when the leaf tty/ dir itself was group-traversable (issue #260 gate 2).
//
// Linux's mkdir(2) silently strips an explicit S_ISGID from `mode` unless
// the parent directory itself has setgid (and /tmp on test hosts does not),
// so WriteMetadata applies the bit via an explicit chmod after MkdirAll.
// Each metadata-write level (realm → space → stack → cell → container)
// fixes its own immediate parent that way; in production, repeated writes
// up the tree ensure every intermediate dir ends up group-traversable.
func TestWriteMetadata_ParentDirModeIsGroupTraversable(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "cell", "metadata.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	doc := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}{APIVersion: "kukeon/v1beta1", Kind: "Cell"}

	if err := metadata.WriteMetadata(context.Background(), logger, doc, target); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	parent := filepath.Join(tmp, "cell")
	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat %q: %v", parent, err)
	}
	mode := info.Mode()
	if mode&os.ModeSetgid == 0 {
		t.Errorf("dir %q mode %v missing setgid bit", parent, mode)
	}
	if mode.Perm() != 0o0750 {
		t.Errorf("dir %q perm bits = %#o, want 0o750", parent, mode.Perm())
	}
}

// TestWriteMetadata_ChmodIsIdempotent_OnReuse covers the upgrade path: a
// host whose pre-#260 daemon left a cell directory at the legacy 0o2700
// (or 0o700) gets self-healed when the daemon writes the cell metadata
// again — the chmod runs unconditionally, not just on fresh MkdirAll.
func TestWriteMetadata_ChmodIsIdempotent_OnReuse(t *testing.T) {
	tmp := t.TempDir()
	parent := filepath.Join(tmp, "cell")
	if err := os.MkdirAll(parent, 0o0700); err != nil {
		t.Fatalf("seed MkdirAll: %v", err)
	}
	target := filepath.Join(parent, "metadata.json")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	doc := struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
	}{APIVersion: "kukeon/v1beta1", Kind: "Cell"}

	if err := metadata.WriteMetadata(context.Background(), logger, doc, target); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}

	info, err := os.Stat(parent)
	if err != nil {
		t.Fatalf("stat %q: %v", parent, err)
	}
	if info.Mode().Perm() != 0o0750 {
		t.Errorf("perm bits = %#o, want 0o750 after self-heal", info.Mode().Perm())
	}
	if info.Mode()&os.ModeSetgid == 0 {
		t.Errorf("setgid bit missing after self-heal: mode %v", info.Mode())
	}
}
