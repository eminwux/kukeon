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

//nolint:testpackage // exercises private attachableTTYDirInitialPerms.
package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/pkg/kuketty"
)

// TestAttachableTTYDirInitialPerms locks the (mode, gid) tuple for the per-
// container tty directory:
//
//   - When the kukeon group GID is configured (>0), the dir is 02750
//     root:kukeon — same group-traversal layout `kuke init` applies to the
//     rest of /opt/kukeon. This is the regression fix for #258 repro A,
//     where 0o700 root-owned blocked non-root members of the kukeon group
//     from dialing the per-container sbsh socket.
//   - When the kukeon group GID is unset (0), the dir falls back to 0o700.
//     Used by `--no-daemon` smoke tests under tmp run-paths and by hosts
//     that pre-date sysuser.EnsureUserGroup; matches pre-#258 behavior.
func TestAttachableTTYDirInitialPerms(t *testing.T) {
	cases := []struct {
		name     string
		gid      int
		wantMode os.FileMode
		wantGID  int
	}{
		{
			name:     "with kukeon group: 02750 root:kukeon",
			gid:      986,
			wantMode: os.ModeSetgid | 0o0750,
			wantGID:  986,
		},
		{
			name:     "kukeon group unset: legacy 0700 root-only",
			gid:      0,
			wantMode: 0o0700,
			wantGID:  0,
		},
		{
			name:     "negative gid (defensive): treated as unset",
			gid:      -1,
			wantMode: 0o0700,
			wantGID:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMode, gotGID := attachableTTYDirInitialPerms(tc.gid)
			if gotMode != tc.wantMode {
				t.Errorf("mode = %v, want %v", gotMode, tc.wantMode)
			}
			if gotGID != tc.wantGID {
				t.Errorf("gid = %d, want %d", gotGID, tc.wantGID)
			}
		})
	}
}

// TestAttachableTTYDirRootMode_SetsSGIDBit is a belt-and-braces guard for
// the constant itself: `os.FileMode(0o2750)` does NOT carry os.ModeSetgid
// (the FileMode flag bits live above the perm bits), so a future refactor
// that drops the explicit `os.ModeSetgid |` could silently produce a
// directory without the setgid bit — and sbsh's later-created socket /
// log / capture siblings would land as root:root instead of inheriting the
// kukeon group, breaking host-side group access on every restart.
func TestAttachableTTYDirRootMode_SetsSGIDBit(t *testing.T) {
	if attachableTTYDirRootMode&os.ModeSetgid == 0 {
		t.Errorf("attachableTTYDirRootMode = %v missing os.ModeSetgid bit", attachableTTYDirRootMode)
	}
	if attachableTTYDirRootMode.Perm() != 0o0750 {
		t.Errorf("attachableTTYDirRootMode perm bits = %#o, want 0o750", attachableTTYDirRootMode.Perm())
	}
}

// TestWriteKukettyMetadata_KukeonGroupSet locks the phase-1 metadata
// rendering when the daemon has a kukeon group configured: socket/capture/
// log mode + gid are filled in so kuketty applies the kukeon-group
// ownership the sbsh wrapper used to apply via flags.
func TestWriteKukettyMetadata_KukeonGroupSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{
		ID: "c1",
		Tty: &intmodel.ContainerTty{
			Prompt: "$ ",
			OnInit: []intmodel.TtyStage{{Script: "echo init"}},
		},
	}
	const kukeonGID = 986

	if err := writeKukettyMetadata(path, spec, kukeonGID); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	md, err := kuketty.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if md.Meta.ContainerID != "c1" {
		t.Errorf("ContainerID = %q, want c1", md.Meta.ContainerID)
	}
	if md.Spec.RunPath != ctr.AttachableTTYDir {
		t.Errorf("RunPath = %q, want %q", md.Spec.RunPath, ctr.AttachableTTYDir)
	}
	if md.Spec.Socket.Path != ctr.AttachableSocketPath {
		t.Errorf("Socket.Path = %q, want %q", md.Spec.Socket.Path, ctr.AttachableSocketPath)
	}
	if md.Spec.Socket.Mode != ctr.AttachableSocketMode {
		t.Errorf("Socket.Mode = %q, want %q", md.Spec.Socket.Mode, ctr.AttachableSocketMode)
	}
	if md.Spec.Socket.GID != kukeonGID {
		t.Errorf("Socket.GID = %d, want %d", md.Spec.Socket.GID, kukeonGID)
	}
	if md.Spec.Shell.Prompt != "$ " {
		t.Errorf("Shell.Prompt = %q, want %q", md.Spec.Shell.Prompt, "$ ")
	}
	if len(md.Spec.Shell.OnInit) != 1 || md.Spec.Shell.OnInit[0].Script != "echo init" {
		t.Errorf("Shell.OnInit = %v, want [{Script: \"echo init\"}]", md.Spec.Shell.OnInit)
	}

	// Permissions on the staged file: daemon-private (0o600). A future
	// loosening of the parent dir must not silently expose the file.
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat metadata: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("metadata file mode = %v, want 0o600", perm)
	}
}

// TestWriteKukettyMetadata_NoKukeonGroup locks the legacy fallback: when
// no kukeon group is configured (GID 0), mode strings are empty so kuketty
// leaves the OS-default permissions on the socket inode — matching the
// sbsh wrapper's behavior on a host with no kukeon group.
func TestWriteKukettyMetadata_NoKukeonGroup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{ID: "c1"}

	if err := writeKukettyMetadata(path, spec, 0); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	md, err := kuketty.Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if md.Spec.Socket.Mode != "" {
		t.Errorf("Socket.Mode = %q, want \"\" (no kukeon group)", md.Spec.Socket.Mode)
	}
	if md.Spec.Socket.GID != 0 {
		t.Errorf("Socket.GID = %d, want 0", md.Spec.Socket.GID)
	}
}

// TestStageKukettyBinary_ReusesExisting locks the idempotent-stage path:
// when the destination is already a usable executable, the helper returns
// without re-copying — load-bearing because every attachable container
// start hits this code path.
func TestStageKukettyBinary_ReusesExisting(t *testing.T) {
	runPath := t.TempDir()
	dstDir := filepath.Join(runPath, kukettyBinaryStagedSubdir)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dst := filepath.Join(dstDir, "kuketty")
	if err := os.WriteFile(dst, []byte("\x7fELF stub"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	got, err := stageKukettyBinary(runPath)
	if err != nil {
		t.Fatalf("stageKukettyBinary: %v", err)
	}
	if got != dst {
		t.Fatalf("stageKukettyBinary = %q, want %q", got, dst)
	}

	// Re-stat to confirm the helper did not re-copy: an actual copy would
	// have overwritten the file at a new mtime. Bytes match the stub.
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(data) != "\x7fELF stub" {
		t.Fatalf("dst contents = %q, want stub (helper re-copied)", data)
	}
}
