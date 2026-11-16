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

package init

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func inode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// TestStageKukeClient guards issue #1483: the staged binary sits in the
// directory cells mount, and the shim cells file-mount at /usr/local/bin/kuke
// keeps its inode across re-stages so it never goes stale.
func TestStageKukeClient(t *testing.T) {
	runPath := t.TempDir()
	paths, err := stageKukeClient(runPath)
	if err != nil {
		t.Fatal(err)
	}
	if paths.Dir != filepath.Join(runPath, "bin", "client") ||
		paths.Binary != filepath.Join(paths.Dir, "kuke") ||
		paths.Shim != filepath.Join(runPath, "bin", "kuke-shim") {
		t.Fatalf("paths = %+v", paths)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(paths.Binary)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("staged binary differs from the running executable (err %v)", err)
	}
	shim, err := os.ReadFile(paths.Shim)
	if err != nil || string(shim) != kukeClientShim {
		t.Fatalf("shim = %q (err %v)", shim, err)
	}
	for _, path := range []string{paths.Binary, paths.Shim} {
		if info, _ := os.Stat(path); info.Mode().Perm() != kukeClientStagedMode {
			t.Fatalf("%s mode = %#o", path, info.Mode().Perm())
		}
	}

	shimIno, binIno := inode(t, paths.Shim), inode(t, paths.Binary)
	if _, err = stageKukeClient(runPath); err != nil {
		t.Fatal(err)
	}
	if inode(t, paths.Shim) != shimIno || inode(t, paths.Binary) != binIno {
		t.Fatal("unchanged content re-staged under a new inode")
	}

	// A rebuilt kuke is re-staged under a new inode; the shim is untouched.
	if err = os.WriteFile(paths.Binary, []byte("old build"), kukeClientStagedMode); err != nil {
		t.Fatal(err)
	}
	binIno = inode(t, paths.Binary)
	if _, err = stageKukeClient(runPath); err != nil {
		t.Fatal(err)
	}
	if got, _ = os.ReadFile(paths.Binary); !bytes.Equal(got, want) || inode(t, paths.Binary) == binIno {
		t.Fatal("changed binary not re-staged by rename")
	}
	if inode(t, paths.Shim) != shimIno {
		t.Fatal("shim replaced")
	}
}
