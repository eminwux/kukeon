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

package ctr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// newTestClient builds a minimal client that talks to no socket. The
// cgroup-file primitives operate on plain os.File operations against the
// caller-supplied mountpoint, so a fake mountpoint under a tmpdir is enough
// to exercise the relocate / drain logic without a real cgroup-v2 hierarchy.
func newTestClient(t *testing.T) *client {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &client{
		ctx:    context.Background(),
		logger: logger,
		socket: "/test/socket",
	}
}

func TestRelocateProcessesToLeafValidation(t *testing.T) {
	c := newTestClient(t)

	if err := c.RelocateProcessesToLeaf("", "/sys/fs/cgroup", "_payload"); !errors.Is(err, errdefs.ErrEmptyGroupPath) {
		t.Errorf("empty group: got %v, want ErrEmptyGroupPath", err)
	}

	cases := []string{"", "/", "..", ".", "with/slash", "/leading"}
	for _, leaf := range cases {
		err := c.RelocateProcessesToLeaf("/kukeon/test", "/sys/fs/cgroup", leaf)
		if !errors.Is(err, errdefs.ErrInvalidLeafName) {
			t.Errorf("leaf %q: got %v, want ErrInvalidLeafName", leaf, err)
		}
	}
}

// writeCgroupTree builds a fake cgroup directory tree for the relocate
// path. Both the group's cgroup.procs (populated with pids) and the leaf's
// cgroup.procs (empty) are pre-created — on a real cgroup-v2 mountpoint
// the kernel auto-creates cgroup.procs when a cgroup directory is mkdir'd,
// so production code opens it read/write without O_CREATE. The pre-create
// here keeps the tmpdir simulation faithful to that contract while also
// covering the mkdir-already-exists branch of the relocate helper.
func writeCgroupTree(t *testing.T, root, group, leaf string, pids []int) {
	t.Helper()
	dir := filepath.Join(root, group)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	procs := filepath.Join(dir, "cgroup.procs")
	f, err := os.Create(procs)
	if err != nil {
		t.Fatalf("create %s: %v", procs, err)
	}
	for _, pid := range pids {
		if _, writeErr := f.WriteString(itoaln(pid)); writeErr != nil {
			t.Fatalf("write pid %d: %v", pid, writeErr)
		}
	}
	_ = f.Close()
	if leaf != "" {
		leafDir := filepath.Join(dir, leaf)
		if mkErr := os.MkdirAll(leafDir, 0o755); mkErr != nil {
			t.Fatalf("mkdir leaf %s: %v", leafDir, mkErr)
		}
		if writeErr := os.WriteFile(filepath.Join(leafDir, "cgroup.procs"), nil, 0o644); writeErr != nil {
			t.Fatalf("create leaf procs: %v", writeErr)
		}
	}
}

func itoaln(n int) string {
	// fmt.Sprintf would pull fmt; small inline implementation keeps the test
	// helper allocation-free and obvious.
	if n == 0 {
		return "0\n"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:]) + "\n"
}

func readPids(t *testing.T, path string) []int {
	t.Helper()
	pids, err := readCgroupProcs(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sort.Ints(pids)
	return pids
}

func TestRelocateProcessesToLeafDrains(t *testing.T) {
	root := t.TempDir()
	c := newTestClient(t)

	pids := []int{4242, 9999, 12345}
	writeCgroupTree(t, root, "kukeon/test/cell", "_payload", pids)

	if err := c.RelocateProcessesToLeaf("/kukeon/test/cell", root, "_payload"); err != nil {
		t.Fatalf("RelocateProcessesToLeaf: %v", err)
	}

	leafProcs := filepath.Join(root, "kukeon/test/cell/_payload/cgroup.procs")
	if _, err := os.Stat(filepath.Dir(leafProcs)); err != nil {
		t.Fatalf("leaf dir missing: %v", err)
	}

	got := readPids(t, leafProcs)
	want := []int{4242, 9999, 12345}
	if len(got) != len(want) {
		t.Fatalf("leaf pids: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("leaf pids[%d]: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestRelocateProcessesToLeafIdempotent(t *testing.T) {
	root := t.TempDir()
	c := newTestClient(t)

	writeCgroupTree(t, root, "cell", "_payload", []int{42})

	if err := c.RelocateProcessesToLeaf("/cell", root, "_payload"); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Empty cgroup.procs (the production drain leaves the source readable
	// but no longer hosting tasks; in the kernel, the write-side would have
	// moved the PIDs out, so a fresh read returns nothing).
	if err := os.WriteFile(filepath.Join(root, "cell/cgroup.procs"), nil, 0o644); err != nil {
		t.Fatalf("truncate source procs: %v", err)
	}

	if err := c.RelocateProcessesToLeaf("/cell", root, "_payload"); err != nil {
		t.Fatalf("second call (no-op): %v", err)
	}
}

func TestRelocateProcessesToLeafMissingSourceProcs(t *testing.T) {
	root := t.TempDir()
	c := newTestClient(t)

	// Mkdir the group dir but do not create cgroup.procs — the relocate
	// helper should surface the read error so callers see the broken state
	// instead of silently swallowing it.
	if err := os.MkdirAll(filepath.Join(root, "cell"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	err := c.RelocateProcessesToLeaf("/cell", root, "_payload")
	if err == nil {
		t.Fatal("expected error reading missing cgroup.procs, got nil")
	}
}

func TestCgroupPopulated(t *testing.T) {
	root := t.TempDir()

	// No cgroup.events file → returns false, no error.
	got, err := cgroupPopulated(root)
	if err != nil {
		t.Fatalf("missing events: unexpected err %v", err)
	}
	if got {
		t.Error("missing events: got true, want false")
	}

	// populated 0 → false.
	if writeErr := os.WriteFile(filepath.Join(root, "cgroup.events"), []byte("populated 0\nfrozen 0\n"), 0o644); writeErr != nil {
		t.Fatalf("write events: %v", writeErr)
	}
	got, err = cgroupPopulated(root)
	if err != nil {
		t.Fatalf("populated 0: unexpected err %v", err)
	}
	if got {
		t.Error("populated 0: got true, want false")
	}

	// populated 1 → true.
	if writeErr := os.WriteFile(filepath.Join(root, "cgroup.events"), []byte("populated 1\nfrozen 0\n"), 0o644); writeErr != nil {
		t.Fatalf("rewrite events: %v", writeErr)
	}
	got, err = cgroupPopulated(root)
	if err != nil {
		t.Fatalf("populated 1: unexpected err %v", err)
	}
	if !got {
		t.Error("populated 1: got false, want true")
	}
}

func TestReadCgroupProcs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cgroup.procs")

	// Empty file → no PIDs, no error.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	pids, err := readCgroupProcs(path)
	if err != nil {
		t.Fatalf("empty read: %v", err)
	}
	if len(pids) != 0 {
		t.Errorf("empty file: got %v, want []", pids)
	}

	// Mixed content (PIDs, blank lines).
	if writeErr := os.WriteFile(path, []byte("1\n\n42\n\n\n9001\n"), 0o644); writeErr != nil {
		t.Fatalf("rewrite: %v", writeErr)
	}
	pids, err = readCgroupProcs(path)
	if err != nil {
		t.Fatalf("mixed read: %v", err)
	}
	want := []int{1, 42, 9001}
	if len(pids) != len(want) {
		t.Fatalf("mixed: got %v, want %v", pids, want)
	}
	for i := range want {
		if pids[i] != want[i] {
			t.Errorf("mixed[%d]: got %d, want %d", i, pids[i], want[i])
		}
	}

	// Garbage content surfaces a parse error.
	if writeErr := os.WriteFile(path, []byte("not-a-pid\n"), 0o644); writeErr != nil {
		t.Fatalf("write garbage: %v", writeErr)
	}
	if _, parseErr := readCgroupProcs(path); parseErr == nil {
		t.Error("garbage parse: expected error, got nil")
	}
}
