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

	"github.com/eminwux/kukeon/internal/consts"
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

// nonZeroTestGID returns a gid suitable for the
// attachableEnsureLogFiles tests. The helper short-circuits when gid<=0
// (the legacy fallback path), so a process whose primary gid is 0 (the
// CI image runs the suite as root) needs a positive gid plumbed in
// explicitly. Non-root processes carry a non-zero gid already and use
// it directly so os.Chown does not EPERM.
func nonZeroTestGID() int {
	if gid := os.Getgid(); gid != 0 {
		return gid
	}
	// As root we can chown to any gid; pick 1 (typically `daemon`) — the
	// numeric value does not matter to the helper, only that it is
	// positive.
	return 1
}

// TestAttachableEnsureLogFiles_PreCreatesAt0640 locks the regression fix
// for issue #366: when a kukeon group GID is configured, the per-container
// capture and log files must be pre-created at 0640 so sbsh's later
// `OpenFile(O_CREATE|...)` inside the container inherits the host-
// readable mode instead of dropping back to 0o600 owner-only — which had
// blocked non-root kukeon-group operators from tailing peer cells.
func TestAttachableEnsureLogFiles_PreCreatesAt0640(t *testing.T) {
	ttyDir := t.TempDir()
	if err := attachableEnsureLogFiles(ttyDir, os.Getuid(), nonZeroTestGID()); err != nil {
		t.Fatalf("attachableEnsureLogFiles: %v", err)
	}
	for _, name := range []string{
		consts.KukeonContainerCaptureFile,
		consts.KukeonContainerLogFile,
	} {
		path := filepath.Join(ttyDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("%s mode = %#o, want 0o640", name, got)
		}
	}
}

// TestAttachableEnsureLogFiles_ReappliesModeOnPreExisting covers the
// daemon-restart path: a prior run left capture/log inodes at the legacy
// 0o600, and the new pre-create helper must tighten them up so the
// regression doesn't survive a restart.
func TestAttachableEnsureLogFiles_ReappliesModeOnPreExisting(t *testing.T) {
	ttyDir := t.TempDir()
	for _, name := range []string{
		consts.KukeonContainerCaptureFile,
		consts.KukeonContainerLogFile,
	} {
		path := filepath.Join(ttyDir, name)
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatalf("seed %q: %v", path, err)
		}
	}
	if err := attachableEnsureLogFiles(ttyDir, os.Getuid(), nonZeroTestGID()); err != nil {
		t.Fatalf("attachableEnsureLogFiles: %v", err)
	}
	for _, name := range []string{
		consts.KukeonContainerCaptureFile,
		consts.KukeonContainerLogFile,
	} {
		path := filepath.Join(ttyDir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0o640 {
			t.Errorf("%s mode = %#o, want 0o640", name, got)
		}
		// Pre-existing content must survive — sbsh's later append must not
		// observe a truncated transcript.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %q: %v", path, err)
		}
		if string(data) != "stale" {
			t.Errorf("%s content = %q, want %q (pre-create must not truncate)", name, data, "stale")
		}
	}
}

// TestAttachableEnsureLogFiles_NoGroupGIDIsNoOp covers the legacy fallback
// path: when the kukeon group GID is unset (e.g., older `kuke init` runs
// or `--no-daemon` smoke tests), the helper does nothing and leaves
// sbsh's hard-coded 0o600 default in effect — matching the surrounding
// tty/ directory's 0o0700 root-only mode in the same branch. Widening the
// files would not buy anything (no group exists to grant access to) and
// would change the legacy contract.
func TestAttachableEnsureLogFiles_NoGroupGIDIsNoOp(t *testing.T) {
	ttyDir := t.TempDir()
	if err := attachableEnsureLogFiles(ttyDir, os.Getuid(), 0); err != nil {
		t.Fatalf("attachableEnsureLogFiles: %v", err)
	}
	for _, name := range []string{
		consts.KukeonContainerCaptureFile,
		consts.KukeonContainerLogFile,
	} {
		path := filepath.Join(ttyDir, name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists with err=%v; want IsNotExist (helper must skip when gid=0)", name, err)
		}
	}
}

// TestAttachableLogFileMode_Is0640 belt-and-braces guard for the constant
// itself: a future refactor that loosens it to 0o660 (group-writable)
// would let the host-side kukeon group append to the in-container
// transcript, which is not the behaviour the issue asks for; tightening
// it back to 0o600 reintroduces the regression.
func TestAttachableLogFileMode_Is0640(t *testing.T) {
	if attachableLogFileMode != 0o0640 {
		t.Errorf("attachableLogFileMode = %#o, want 0o0640", attachableLogFileMode)
	}
}
