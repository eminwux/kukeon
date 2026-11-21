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
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/util/fs"
)

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return data
}

func wantMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %q = %o, want %o", path, got, want)
	}
}

func wantNoTmp(t *testing.T, dst string) {
	t.Helper()
	if _, err := os.Stat(dst + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("staged tmp %q left behind (stat err = %v)", dst+".tmp", err)
	}
}

// TestWriteFileAtomic_SetsModeDespiteUmaskAndStaleTmp: the staged file gets
// the requested mode even under a restrictive umask and over a tmp a crashed
// earlier attempt left with another mode, and replaces an existing dst.
func TestWriteFileAtomic_SetsModeDespiteUmaskAndStaleTmp(t *testing.T) {
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	dst := filepath.Join(t.TempDir(), "kuketty")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst+".tmp", []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := fs.WriteFileAtomic(dst, strings.NewReader("new"), 0o755, "kuketty"); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if got := readFile(t, dst); string(got) != "new" {
		t.Errorf("content = %q, want %q", got, "new")
	}
	wantMode(t, dst, 0o755)
	wantNoTmp(t, dst)
}

// TestWriteFileAtomic_ErrorNamesLabel: a failed stage names what was staged.
func TestWriteFileAtomic_ErrorNamesLabel(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "missing-dir", "kukepause")
	err := fs.WriteFileAtomic(dst, strings.NewReader("x"), 0o755, "kukepause")
	if err == nil || !strings.Contains(err.Error(), "kukepause staged tmp") {
		t.Fatalf("err = %v, want one naming the kukepause staged tmp", err)
	}
}

// TestCopyFileAtomic copies the source's bytes and reports a missing source
// with the label.
func TestCopyFileAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	content := bytes.Repeat([]byte("kukepause"), 4096)
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := fs.CopyFileAtomic(src, dst, 0o750, "kukepause"); err != nil {
		t.Fatalf("CopyFileAtomic: %v", err)
	}
	if !bytes.Equal(readFile(t, dst), content) {
		t.Error("staged copy differs from its source")
	}
	wantMode(t, dst, 0o750)
	wantNoTmp(t, dst)

	err := fs.CopyFileAtomic(filepath.Join(dir, "absent"), dst, 0o750, "kukepause")
	if err == nil || !strings.Contains(err.Error(), "open kukepause source") {
		t.Fatalf("err = %v, want one naming the kukepause source", err)
	}
}

// TestWriteFileIfChanged leaves a file that already holds the content alone —
// same inode — and replaces one that does not.
func TestWriteFileIfChanged(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "kuke")

	wrote, err := fs.WriteFileIfChanged(dst, []byte("v1"), 0o755, "kuke client")
	if err != nil || !wrote {
		t.Fatalf("first stage = (%v, %v), want (true, nil)", wrote, err)
	}
	before, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}

	wrote, err = fs.WriteFileIfChanged(dst, []byte("v1"), 0o755, "kuke client")
	if err != nil || wrote {
		t.Fatalf("unchanged stage = (%v, %v), want (false, nil)", wrote, err)
	}
	after, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("an unchanged stage replaced the file")
	}

	wrote, err = fs.WriteFileIfChanged(dst, []byte("v2"), 0o755, "kuke client")
	if err != nil || !wrote {
		t.Fatalf("changed stage = (%v, %v), want (true, nil)", wrote, err)
	}
	if got := readFile(t, dst); string(got) != "v2" {
		t.Errorf("content = %q, want v2", got)
	}
	wantMode(t, dst, 0o755)
	wantNoTmp(t, dst)
}
