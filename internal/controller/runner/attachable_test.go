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

//nolint:testpackage // exercises private writeKukettyDoc and attachableTTYDirInitialPerms.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	extmodel "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
// directory without the setgid bit — and kuketty's later-created socket /
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

// TestWriteKukettyDoc_EmitsContainerDoc locks the post-#641 mounted artifact:
// the daemon writes a kukeon ContainerDoc (not a pre-rendered sbsh TerminalDoc)
// with the spec populated, the status left zero, and the cell-context labels
// stamped on Metadata. The three daemon-only resolved values travel in the
// doc — the resolved workload argv in Spec.Command/Spec.Args, the kukeon-group
// GID in Spec.KukeonGroupGID, and the resolved log level in Spec.Tty.LogLevel —
// so kuketty's transform stays a pure ContainerSpec -> TerminalSpec mapping.
func TestWriteKukettyDoc_EmitsContainerDoc(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{
		ID:        "c1",
		RealmName: "rA",
		SpaceName: "sB",
		StackName: "kC",
		CellName:  "lD",
	}
	const kukeonGID = 986
	workload := []string{"/bin/sh", "-c", "echo hello"}

	if err := r.writeKukettyDoc(path, spec, kukeonGID, workload); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}

	doc := readDoc(t, path)
	if doc.APIVersion != extmodel.APIVersionV1Beta1 {
		t.Errorf("APIVersion = %q, want %q", doc.APIVersion, extmodel.APIVersionV1Beta1)
	}
	if doc.Kind != extmodel.KindContainer {
		t.Errorf("Kind = %q, want %q", doc.Kind, extmodel.KindContainer)
	}
	if doc.Metadata.Name != "c1" {
		t.Errorf("Metadata.Name = %q, want c1", doc.Metadata.Name)
	}
	wantLabels := map[string]string{
		"kukeon.io/realm":        "rA",
		"kukeon.io/space":        "sB",
		"kukeon.io/stack":        "kC",
		"kukeon.io/cell":         "lD",
		"kukeon.io/container-id": "c1",
	}
	for k, want := range wantLabels {
		if got := doc.Metadata.Labels[k]; got != want {
			t.Errorf("Metadata.Labels[%q] = %q, want %q", k, got, want)
		}
	}
	// Resolved workload argv travels in Command/Args.
	if doc.Spec.Command != "/bin/sh" {
		t.Errorf("Spec.Command = %q, want /bin/sh", doc.Spec.Command)
	}
	wantArgs := []string{"-c", "echo hello"}
	if len(doc.Spec.Args) != len(wantArgs) {
		t.Fatalf("Spec.Args = %v, want %v", doc.Spec.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if doc.Spec.Args[i] != want {
			t.Errorf("Spec.Args[%d] = %q, want %q", i, doc.Spec.Args[i], want)
		}
	}
	// Kukeon-group GID travels in Spec.KukeonGroupGID.
	if doc.Spec.KukeonGroupGID != kukeonGID {
		t.Errorf("Spec.KukeonGroupGID = %d, want %d", doc.Spec.KukeonGroupGID, kukeonGID)
	}
	// Resolved log level travels in Spec.Tty.LogLevel (allocated even though
	// the cell omitted the Tty block).
	if doc.Spec.Tty == nil {
		t.Fatalf("Spec.Tty is nil; want allocated to carry the resolved log level")
	}
	if doc.Spec.Tty.LogLevel != "info" {
		t.Errorf("Spec.Tty.LogLevel = %q, want info (default)", doc.Spec.Tty.LogLevel)
	}
	// Status is left zero (AC #1). Compare field-wise rather than with == :
	// time.Time zero values do not round-trip equal through JSON (the decoded
	// loc is UTC, the zero-value loc is nil), so a struct == would spuriously
	// fail on the timestamp fields.
	if doc.Status.State != extmodel.ContainerStatePending {
		t.Errorf("Status.State = %v, want zero (Pending)", doc.Status.State)
	}
	if doc.Status.Name != "" || doc.Status.ID != "" || doc.Status.ExitCode != 0 ||
		doc.Status.RestartCount != 0 || doc.Status.ExitSignal != "" {
		t.Errorf("Status carries non-zero scalar fields: %+v", doc.Status)
	}
	if !doc.Status.StartTime.IsZero() || !doc.Status.FinishTime.IsZero() || !doc.Status.RestartTime.IsZero() {
		t.Errorf("Status timestamps not zero: %+v", doc.Status)
	}
	// File permissions: daemon-private (0o600).
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat metadata: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("metadata file mode = %v, want 0o600", perm)
	}
}

// TestWriteKukettyDoc_EmptyWorkloadClearsCommand locks the empty-argv branch:
// when the args-wrap captured no resolved Process.Args (image with no
// ENTRYPOINT/CMD and no override), the daemon clears Command/Args so kuketty
// falls through to sbsh's inline-builder default rather than carrying a stale
// user-authored command.
func TestWriteKukettyDoc_EmptyWorkloadClearsCommand(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{ID: "c1", Command: "/bin/leftover", Args: []string{"x"}}

	if err := r.writeKukettyDoc(path, spec, 0, nil); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Command != "" {
		t.Errorf("Spec.Command = %q, want empty (no resolved argv)", doc.Spec.Command)
	}
	if len(doc.Spec.Args) != 0 {
		t.Errorf("Spec.Args = %v, want empty", doc.Spec.Args)
	}
}

// TestWriteKukettyDoc_LogLevelResolution locks the per-container → server-config
// → "info" precedence the daemon owns (the server-config tier is not visible
// inside the container, so kuketty cannot resolve it). The resolved value is
// stamped onto Spec.Tty.LogLevel; kuketty reads it verbatim.
func TestWriteKukettyDoc_LogLevelResolution(t *testing.T) {
	cases := []struct {
		name         string
		tty          *intmodel.ContainerTty
		serverConfig string
		wantLevel    string
	}{
		{"per-container wins over server-config", &intmodel.ContainerTty{LogLevel: "debug"}, "warn", "debug"},
		{"server-config wins when per-container empty", nil, "warn", "warn"},
		{"server-config wins with non-LogLevel Tty fields", &intmodel.ContainerTty{Prompt: "x> "}, "error", "error"},
		{"hardcoded info when both empty", nil, "", "info"},
		{"per-container wins over empty server-config", &intmodel.ContainerTty{LogLevel: "debug"}, "", "debug"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			r.opts.KukettyLogLevel = tc.serverConfig
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{ID: "c1", Tty: tc.tty}
			if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyDoc: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.Tty == nil {
				t.Fatalf("Spec.Tty is nil; want resolved log level")
			}
			if doc.Spec.Tty.LogLevel != tc.wantLevel {
				t.Errorf("Spec.Tty.LogLevel = %q, want %q", doc.Spec.Tty.LogLevel, tc.wantLevel)
			}
		})
	}
}

// TestWriteKukettyDoc_LogFileOverridePreserved confirms the daemon carries an
// operator-pinned Tty.LogFile through to the doc verbatim (kuketty applies the
// override at transform time). The default (no Tty) leaves LogFile empty in the
// doc — kuketty resolves the daemon-controlled default path from its own
// contract constant.
func TestWriteKukettyDoc_LogFileOverridePreserved(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	const override = "/var/log/sbsh-debug.log"
	spec := intmodel.ContainerSpec{ID: "c1", Tty: &intmodel.ContainerTty{LogFile: override}}

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Tty == nil || doc.Spec.Tty.LogFile != override {
		t.Errorf("Spec.Tty.LogFile = %v, want %q", doc.Spec.Tty, override)
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

// TestEnsureAttachableSocketSymlink_RefusesOverflow locks the provision-
// time fail-fast added in #521 AC #2: when the resolved symlink path would
// exceed consts.KukeonMaxSocketPath bytes, ensureAttachableSocketSymlink
// must return errdefs.ErrSocketPathTooLong and stage no on-disk state.
// Without this guard, a future refactor that drops or inverts the length
// check would silently defer the failure to first `kuke attach`, where it
// surfaces as an opaque connect(2) ENAMETOOLONG instead of a provision-
// time error. The existing TestContainerSocketSymlinkPath_FitsSUNPath in
// internal/util/fs only verifies that normal-shaped inputs stay under the
// limit — it does not assert that the runner *refuses to provision* when
// they do not.
func TestEnsureAttachableSocketSymlink_RefusesOverflow(t *testing.T) {
	// Pad a real tmp dir so len(runPath)+len("/s/")+16hex exceeds the
	// budget by one byte. Using a real (existing) base path matters: the
	// "no on-disk state" assertion below is only meaningful when the
	// function *could* have created the symlink dir had the fail-fast
	// regressed.
	base := t.TempDir()
	const sep = "/s/"
	const shortIDLen = 16 // sha256[:8] hex == 16 chars (see containerSocketShortID)
	padBytes := consts.KukeonMaxSocketPath + 1 - len(base) - len(sep) - shortIDLen
	if padBytes < 1 {
		padBytes = 1
	}
	runPath := filepath.Join(base, strings.Repeat("a", padBytes))
	spec := intmodel.ContainerSpec{
		ID:        "c1",
		RealmName: "rA",
		SpaceName: "sB",
		StackName: "kC",
		CellName:  "lD",
	}

	symlinkPath := fs.ContainerSocketSymlinkPath(
		runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if len(symlinkPath) <= consts.KukeonMaxSocketPath {
		t.Fatalf("test setup wrong: symlinkPath len=%d <= limit=%d (path=%q)",
			len(symlinkPath), consts.KukeonMaxSocketPath, symlinkPath)
	}

	err := ensureAttachableSocketSymlink(runPath, spec)
	if !errors.Is(err, errdefs.ErrSocketPathTooLong) {
		t.Fatalf("err = %v, want errors.Is(_, errdefs.ErrSocketPathTooLong)", err)
	}

	// On the fail-fast path, no on-disk state should land — neither the
	// shallow <runPath>/s symlink directory nor the per-container symlink
	// inode. If a future refactor reorders MkdirAll above the length check
	// the dir assertion catches it; the symlink-inode assertion guards
	// against a regression that fires the length check but still proceeds
	// to os.Symlink after.
	symlinkDir := fs.ContainerSocketSymlinkDir(runPath)
	if _, statErr := os.Stat(symlinkDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("symlink dir %q exists (stat err = %v), want ErrNotExist — fail-fast must not stage on-disk state",
			symlinkDir, statErr)
	}
	if _, statErr := os.Lstat(symlinkPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("symlink %q exists (lstat err = %v), want ErrNotExist", symlinkPath, statErr)
	}
}

func newTestRunner(t *testing.T) *Exec {
	t.Helper()
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func readDoc(t *testing.T, path string) extmodel.ContainerDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc extmodel.ContainerDoc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return doc
}
