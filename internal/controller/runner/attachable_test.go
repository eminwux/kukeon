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
	"testing"
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
