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
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
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

	if err := r.writeKukettyDoc(path, spec, kukeonGID, workload, nil); err != nil {
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

	if err := r.writeKukettyDoc(path, spec, 0, nil, nil); err != nil {
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
			if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, nil); err != nil {
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

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, nil); err != nil {
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

// TestChownAttachableStaleChildren_CoversRestartLayout locks the #850 fix:
// the post-create chown sweep visits every kuketty/sbsh-written leaf under
// the tty bind-mount source so a non-root image USER (e.g. claude:latest →
// 1000) can re-open them on a restart. Without the sweep, kuketty's
// openTerminalLogger hit EACCES on the prior run's root-owned kuketty.log
// and exited before claiming the socket listener.
//
// The test seeds the tty dir with the inode set the issue enumerates —
// kuketty.log, capture, capture.001.gz (sbsh's rotated transcript),
// metadata.json (sbsh's atomic doc after #672), .meta-*.tmp (the
// create-temp half of that atomic write), plus a stale pre-#672
// terminals/<id>/ subtree to lock the recursion contract. When the test
// runs as root the sweep retargets every inode to a non-root uid and the
// assertion is end-to-end (syscall.Stat_t.Uid); when the test runs unpriv
// the sweep is a self-chown no-op and we assert the walk completes without
// error and leaves every inode in place. Either way regressing the walk
// (dropping recursion, raising an error on a stale file, skipping a known
// basename) fails the test.
func TestChownAttachableStaleChildren_CoversRestartLayout(t *testing.T) {
	ttyDir := t.TempDir()

	seedFiles := []string{
		consts.KukeonContainerKukettyLogFile, // kuketty.log
		consts.KukeonContainerCaptureFile,    // capture
		"capture.001.gz",                     // sbsh rotated transcript
		"metadata.json",                      // sbsh's atomic doc (#672)
		".meta-12345.tmp",                    // sbsh's atomic write tmp
	}
	for _, name := range seedFiles {
		path := filepath.Join(ttyDir, name)
		if err := os.WriteFile(path, []byte("stale\n"), 0o640); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	// Pre-#672 terminals/<id>/ subtree — the sweep must recurse into it
	// even though #672 moved sbsh's MetadataDir up to attachableTTYDir, so
	// a daemon upgraded across that boundary doesn't leak a root-owned
	// subdir into the restart path.
	legacyDir := filepath.Join(ttyDir, "terminals", "abc123")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	legacyFile := filepath.Join(legacyDir, "metadata.json")
	if err := os.WriteFile(legacyFile, []byte("stale legacy\n"), 0o640); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	targetUID, targetGID := os.Geteuid(), os.Getegid()
	if targetUID == 0 {
		// Root path: pick a non-root target so the chown is observable in
		// Stat_t.Uid. 1000 is the canonical non-root container UID the
		// issue's claude:latest repro hits.
		targetUID = 1000
	}

	if err := chownAttachableStaleChildren(ttyDir, targetUID, targetGID); err != nil {
		t.Fatalf("chownAttachableStaleChildren: %v", err)
	}

	// All seeded files (and the recursed-into legacy subtree's file and
	// directory) must end up owned by targetUID. Skip the per-inode Uid
	// assertion when not root — the self-chown is a no-op and the
	// pre-chown owner already equals targetUID, so it would tautologically
	// pass.
	wantAssertUID := os.Geteuid() == 0
	walkPaths := append([]string{}, seedFiles...)
	for _, name := range walkPaths {
		assertOwnedBy(t, filepath.Join(ttyDir, name), targetUID, wantAssertUID)
	}
	assertOwnedBy(t, legacyDir, targetUID, wantAssertUID)
	assertOwnedBy(t, legacyFile, targetUID, wantAssertUID)

	// The root dir is intentionally skipped — caller chowned it
	// explicitly. Regressing the skip would re-chown it, which is harmless
	// today but would change the function's contract; the doc-comment
	// names it explicitly so lock that here.
	if !wantAssertUID {
		return
	}
	rootStat := statSys(t, ttyDir)
	if int(rootStat.Uid) == targetUID {
		t.Errorf("ttyDir uid = %d (want unchanged, function is children-only)", rootStat.Uid)
	}
}

// TestChownAttachableStaleChildren_FreshProvision_NoOp pins the no-op
// contract on the fresh-create path: MkdirAll just produced an empty
// ttyDir, the sweep visits zero children, returns nil. Regressing this
// (e.g. requiring at least one seeded file) would surface as a noisy
// error on every first provision.
func TestChownAttachableStaleChildren_FreshProvision_NoOp(t *testing.T) {
	ttyDir := t.TempDir()
	if err := chownAttachableStaleChildren(ttyDir, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatalf("chownAttachableStaleChildren on empty dir: %v", err)
	}
}

// TestChownAttachableStaleChildren_TolerantENOENT covers the
// concurrent-unlink race the doc comment promises: an entry that
// disappears between Walk's readdir and our per-entry Lchown is not an
// error. The simplest synthesis is a nonexistent root — filepath.Walk
// surfaces the initial Lstat ENOENT through the callback, which our
// errors.Is(_, os.ErrNotExist) branch swallows. Same code path that
// guards a real mid-walk unlink, exercised without a separate goroutine.
func TestChownAttachableStaleChildren_TolerantENOENT(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if err := chownAttachableStaleChildren(missing, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatalf("chownAttachableStaleChildren on missing root: %v", err)
	}
}

// statSys returns the syscall.Stat_t for path or fails the test. Wrapper
// around os.Stat + the platform-specific Sys() type assertion so the
// per-test ownership assertions stay readable.
func statSys(t *testing.T, path string) *syscall.Stat_t {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skipf("syscall.Stat_t not available on this platform (path=%s)", path)
	}
	return stat
}

// assertOwnedBy verifies path's uid equals wantUID when assertUID is set.
// The two-mode shape lets the same call site cover both the privileged
// (real chown observable) and unprivileged (self-chown no-op, presence-
// only) test runs without forking the assertion list.
func assertOwnedBy(t *testing.T, path string, wantUID int, assertUID bool) {
	t.Helper()
	stat := statSys(t, path)
	if assertUID && int(stat.Uid) != wantUID {
		t.Errorf("%s uid = %d, want %d", path, stat.Uid, wantUID)
	}
}

// TestWriteKukettyDoc_RenderGate_OmitsDoneCreateStagesOnRestart locks AC #1:
// on a subsequent boot, the rendered ContainerDoc gates already-done runOn:
// create stages so kuketty's pre-Serve executor no-ops their execution. The
// gate clears the Script of each gated stage (preserving the TtyStage entry
// at its OnInit position so kuketty's createStages emits Stage.Index aligned
// with the live spec.Tty.OnInit position the daemon-side merge anchors on);
// non-create stages and create stages with no prior done record are
// untouched. Phase C2 (#737).
func TestWriteKukettyDoc_RenderGate_OmitsDoneCreateStagesOnRestart(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	doneStage := intmodel.TtyStage{Script: "npm ci", RunOn: extmodel.RunOnCreate}
	startStage := intmodel.TtyStage{Script: "echo welcome", RunOn: extmodel.RunOnStart}
	freshStage := intmodel.TtyStage{Script: "db-seed", RunOn: extmodel.RunOnCreate}
	doneHash := stageContentHash(toV1beta1Stage(doneStage))

	spec := intmodel.ContainerSpec{
		ID:         "c1",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{doneStage, startStage, freshStage},
		},
	}
	priorStages := []intmodel.StageStatus{
		{Index: 0, State: setupstatus.StageDone, Hash: doneHash},
		// freshStage at Index 2 has no prior record — must render unchanged.
	}

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, priorStages); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Tty == nil {
		t.Fatalf("Spec.Tty is nil")
	}
	if len(doc.Spec.Tty.OnInit) != 3 {
		t.Fatalf("len(Spec.Tty.OnInit) = %d, want 3 (slice length must be preserved so kuketty's Stage.Index aligns with spec position)", len(doc.Spec.Tty.OnInit))
	}
	// Gated create stage: Script cleared, RunOn preserved so kuketty's
	// createStages still picks it up at the correct Index. The on-disk
	// evidence the gate fired is the empty Script alongside an unchanged
	// RunOn.
	if doc.Spec.Tty.OnInit[0].Script != "" {
		t.Errorf("OnInit[0].Script = %q, want empty (gate must clear done create stage's Script)", doc.Spec.Tty.OnInit[0].Script)
	}
	if doc.Spec.Tty.OnInit[0].RunOn != extmodel.RunOnCreate {
		t.Errorf("OnInit[0].RunOn = %q, want %q (gate must not touch RunOn — preserving createStages selection)", doc.Spec.Tty.OnInit[0].RunOn, extmodel.RunOnCreate)
	}
	// Non-create stages are never gated.
	if doc.Spec.Tty.OnInit[1].Script != startStage.Script {
		t.Errorf("OnInit[1].Script = %q, want %q (runOn: start must not be touched)", doc.Spec.Tty.OnInit[1].Script, startStage.Script)
	}
	if doc.Spec.Tty.OnInit[1].RunOn != extmodel.RunOnStart {
		t.Errorf("OnInit[1].RunOn = %q, want %q", doc.Spec.Tty.OnInit[1].RunOn, extmodel.RunOnStart)
	}
	// Create stage with no prior done record: unchanged.
	if doc.Spec.Tty.OnInit[2].Script != freshStage.Script {
		t.Errorf("OnInit[2].Script = %q, want %q (fresh create stage with no prior done must render unchanged)", doc.Spec.Tty.OnInit[2].Script, freshStage.Script)
	}
}

// TestWriteKukettyDoc_RenderGate_EditedStageReRuns locks AC #2: an edited
// runOn: create stage produces a new content hash that no longer matches the
// prior done record, so the gate does not fire and the stage's Script is
// rendered verbatim — kuketty's pre-Serve executor re-runs it. Phase C2
// (#737).
func TestWriteKukettyDoc_RenderGate_EditedStageReRuns(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	priorStage := intmodel.TtyStage{Script: "npm ci", RunOn: extmodel.RunOnCreate}
	editedStage := intmodel.TtyStage{Script: "npm ci --omit=dev", RunOn: extmodel.RunOnCreate}
	priorHash := stageContentHash(toV1beta1Stage(priorStage))
	editedHash := stageContentHash(toV1beta1Stage(editedStage))
	if priorHash == editedHash {
		t.Fatalf("test premise violated: edited stage hashes identically to prior")
	}

	spec := intmodel.ContainerSpec{
		ID:         "c1",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{editedStage},
		},
	}
	priorStages := []intmodel.StageStatus{
		{Index: 0, State: setupstatus.StageDone, Hash: priorHash},
	}

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, priorStages); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Tty == nil || len(doc.Spec.Tty.OnInit) != 1 {
		t.Fatalf("Spec.Tty.OnInit = %+v, want single-entry", doc.Spec.Tty)
	}
	if doc.Spec.Tty.OnInit[0].Script != editedStage.Script {
		t.Errorf("OnInit[0].Script = %q, want %q (edited hash must not match prior done — gate must not fire)",
			doc.Spec.Tty.OnInit[0].Script, editedStage.Script)
	}
}

// TestWriteKukettyDoc_RenderGate_ReconcileRollback locks AC #3 (#625 phase
// 4.3 interaction): when reconcile drives a container back to a prior spec
// version, the done-set's hashes either still match (gate fires — skip) or
// no longer match (gate does not fire — re-run). The done-set is keyed on
// content Hash, not OnInit position, so a rollback that re-orders or
// reintroduces a stage with the same content still hits the gate at its new
// position.
//
// Scenario: container originally had OnInit = [A_create]. Operator added a
// new pre-step B_create at position 0 (OnInit = [B_create, A_create]), let
// it run, then reconcile rolled the spec back to [A_create]. A's hash is in
// the done-set; the rolled-back spec must render A's Script cleared at its
// new position 0.
func TestWriteKukettyDoc_RenderGate_ReconcileRollback(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	stageA := intmodel.TtyStage{Script: "setup-env", RunOn: extmodel.RunOnCreate}
	stageB := intmodel.TtyStage{Script: "pre-step", RunOn: extmodel.RunOnCreate}
	hashA := stageContentHash(toV1beta1Stage(stageA))
	hashB := stageContentHash(toV1beta1Stage(stageB))

	// Rolled-back spec: only stageA remains (at the now-shifted position 0).
	spec := intmodel.ContainerSpec{
		ID:         "c1",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{stageA},
		},
	}
	// Pre-rollback persisted done set carried done records for both stages.
	// On the next populate after rollback, mergeStageStatuses (anchored on
	// spec.Tty.OnInit positions) would have dropped stageB's done record
	// because the rolled-back spec has no stage at that Index. The render
	// gate consumes the merged set, but exercising the gate's hash-keyed
	// semantics directly here keeps this test focused on AC #3 rather than
	// re-testing mergeStageStatuses: we pass the post-merge priorStages
	// stageA carries forward at its new Index, and confirm the rendered
	// Script is cleared.
	priorStages := []intmodel.StageStatus{
		{Index: 0, State: setupstatus.StageDone, Hash: hashA},
	}

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, priorStages); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Tty == nil || len(doc.Spec.Tty.OnInit) != 1 {
		t.Fatalf("Spec.Tty.OnInit = %+v, want single-entry (rolled-back spec)", doc.Spec.Tty)
	}
	if doc.Spec.Tty.OnInit[0].Script != "" {
		t.Errorf("OnInit[0].Script = %q, want empty (stageA's hash %s is in done-set; gate must fire at its new position)",
			doc.Spec.Tty.OnInit[0].Script, hashA)
	}

	// Second sub-scenario: a rolled-back spec whose stages no longer have
	// matching hashes in the prior done-set (e.g., reconcile rolled forward
	// instead and introduced a fresh stage) must render every create stage
	// verbatim. The hash for stageB is in the prior done-set but stageB
	// itself is not in the rolled-back OnInit — there is nothing to gate.
	freshStage := intmodel.TtyStage{Script: "fresh-step", RunOn: extmodel.RunOnCreate}
	specFresh := intmodel.ContainerSpec{
		ID:         "c1",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{freshStage},
		},
	}
	priorFresh := []intmodel.StageStatus{
		// hashB belongs to stageB which no longer exists in spec.
		{Index: 0, State: setupstatus.StageDone, Hash: hashB},
	}
	pathFresh := filepath.Join(dir, "fresh.json")
	if err := r.writeKukettyDoc(pathFresh, specFresh, 0, []string{"/bin/sh"}, priorFresh); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	docFresh := readDoc(t, pathFresh)
	if docFresh.Spec.Tty == nil || len(docFresh.Spec.Tty.OnInit) != 1 {
		t.Fatalf("fresh: Spec.Tty.OnInit = %+v, want single-entry", docFresh.Spec.Tty)
	}
	if docFresh.Spec.Tty.OnInit[0].Script != freshStage.Script {
		t.Errorf("fresh: OnInit[0].Script = %q, want %q (no hash overlap with done-set — gate must not fire)",
			docFresh.Spec.Tty.OnInit[0].Script, freshStage.Script)
	}
}

// TestWriteKukettyDoc_RenderGate_IgnoresFailedAndMissingHash guards two
// edges of the done-set derivation: a State == "failed" record must not gate
// (a failed stage gets a fresh chance on restart per phase B's exit-before-
// Serve contract), and a record with an empty Hash must not gate (the
// run-once key is the content hash; a missing key is unparseable). The
// merge upstream (mergeStageStatuses) already enforces these invariants on
// the persisted snapshot, but the gate itself must be defensive: a future
// caller that bypasses the merge (e.g., a test fixture, or a #625 reconcile
// path that builds the done-set differently) cannot accidentally gate on
// a failed or hash-less record.
func TestWriteKukettyDoc_RenderGate_IgnoresFailedAndMissingHash(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	stage := intmodel.TtyStage{Script: "boot-it", RunOn: extmodel.RunOnCreate}
	hash := stageContentHash(toV1beta1Stage(stage))

	spec := intmodel.ContainerSpec{
		ID:         "c1",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{stage},
		},
	}
	// Two records that look like they cover Index 0 but neither qualifies
	// to gate: failed (not done) and done-without-hash (key-less).
	priorStages := []intmodel.StageStatus{
		{Index: 0, State: setupstatus.StageFailed, Error: "boom", Hash: hash},
		{Index: 0, State: setupstatus.StageDone}, // Hash empty
	}

	if err := r.writeKukettyDoc(path, spec, 0, []string{"/bin/sh"}, priorStages); err != nil {
		t.Fatalf("writeKukettyDoc: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Tty.OnInit[0].Script != stage.Script {
		t.Errorf("OnInit[0].Script = %q, want %q (failed/key-less records must never gate)",
			doc.Spec.Tty.OnInit[0].Script, stage.Script)
	}
}

func newTestRunner(t *testing.T) *Exec {
	t.Helper()
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// TestRemoveAttachableSocketRuntimeArtifacts_RemovesBothInodes covers
// the #852 teardown hook: on every clean stop / kill / wind-down, both
// the SUN_PATH-safe symlink at <RunPath>/s/<hash> and the deep socket
// inode at <ContainerTTYDir>/socket must be unlinked so the next
// `kuke attach` that slips past the cell-state liveness guard gets
// ENOENT instead of `connection refused` from a dead inode.
func TestRemoveAttachableSocketRuntimeArtifacts_RemovesBothInodes(t *testing.T) {
	runPath := t.TempDir()
	spec := intmodel.ContainerSpec{
		ID:         "work",
		RealmName:  "r1",
		SpaceName:  "s1",
		StackName:  "st1",
		CellName:   "c1",
		Attachable: true,
	}

	// Stage the symlink the way ensureAttachableSocketSymlink would on
	// provision: the helper this test exercises is the symmetric
	// teardown counterpart, so the seed must mirror what's actually on
	// disk after a healthy boot.
	if err := ensureAttachableSocketSymlink(runPath, spec); err != nil {
		t.Fatalf("ensureAttachableSocketSymlink: %v", err)
	}

	// Stage the deep socket inode kuketty would bind inside the
	// container. A plain file is fine for the unlink test — the kernel
	// distinguishes socket inodes at connect(2) time, not at unlink(2)
	// time, so a regular file standing in for the bound socket is
	// indistinguishable to os.Remove.
	socketPath := fs.ContainerSocketPath(
		runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("mkdir socket parent: %v", err)
	}
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("seed socket inode: %v", err)
	}

	if err := removeAttachableSocketRuntimeArtifacts(runPath, spec); err != nil {
		t.Fatalf("removeAttachableSocketRuntimeArtifacts: %v", err)
	}

	symlinkPath := fs.ContainerSocketSymlinkPath(
		runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if _, err := os.Lstat(symlinkPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink %q still exists (lstat err = %v), want ErrNotExist", symlinkPath, err)
	}
	if _, err := os.Lstat(socketPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket inode %q still exists (lstat err = %v), want ErrNotExist", socketPath, err)
	}
}

// TestRemoveAttachableSocketRuntimeArtifacts_Idempotent locks the
// best-effort contract: a second call against the same spec (or a
// first call against a never-provisioned spec) must succeed silently.
// killCellLocked / stopCellLocked call this on every workload
// container per teardown, and the wind-down path can race them — a
// non-NotExist error on a missing path would otherwise spam warnings
// and obscure real failures.
func TestRemoveAttachableSocketRuntimeArtifacts_Idempotent(t *testing.T) {
	runPath := t.TempDir()
	spec := intmodel.ContainerSpec{
		ID:         "work",
		RealmName:  "r1",
		SpaceName:  "s1",
		StackName:  "st1",
		CellName:   "c1",
		Attachable: true,
	}

	// First call against a never-provisioned spec: both paths absent →
	// must return nil.
	if err := removeAttachableSocketRuntimeArtifacts(runPath, spec); err != nil {
		t.Fatalf("first call against absent paths returned %v, want nil", err)
	}

	if err := ensureAttachableSocketSymlink(runPath, spec); err != nil {
		t.Fatalf("ensureAttachableSocketSymlink: %v", err)
	}
	if err := removeAttachableSocketRuntimeArtifacts(runPath, spec); err != nil {
		t.Fatalf("call after symlink-only seed returned %v, want nil", err)
	}
	// Repeat: nothing on disk now, still must return nil.
	if err := removeAttachableSocketRuntimeArtifacts(runPath, spec); err != nil {
		t.Fatalf("second call against absent paths returned %v, want nil", err)
	}
}

// TestRemoveAttachableSocketRuntimeArtifacts_SkipsNonAttachable
// locks the !Attachable short-circuit: kill/stopCellLocked iterate
// over every container in the cell spec and call this unconditionally,
// so the helper must no-op for root and non-Attachable workloads.
// Otherwise it would surface confusing remove-errors on cells whose
// containers were never sbsh-wrapped.
func TestRemoveAttachableSocketRuntimeArtifacts_SkipsNonAttachable(t *testing.T) {
	runPath := t.TempDir()
	spec := intmodel.ContainerSpec{
		ID:         "root",
		RealmName:  "r1",
		SpaceName:  "s1",
		StackName:  "st1",
		CellName:   "c1",
		Root:       true,
		Attachable: false,
	}

	// Seed a file at the deep socket path even though !Attachable: a
	// future refactor that drops the short-circuit must not unlink
	// state that doesn't belong to it.
	socketPath := fs.ContainerSocketPath(
		runPath, spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName, spec.ID,
	)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("mkdir socket parent: %v", err)
	}
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatalf("seed socket inode: %v", err)
	}

	if err := removeAttachableSocketRuntimeArtifacts(runPath, spec); err != nil {
		t.Fatalf("removeAttachableSocketRuntimeArtifacts returned %v, want nil", err)
	}
	if _, err := os.Stat(socketPath); err != nil {
		t.Errorf("unrelated socket inode %q was removed (stat err = %v); helper must short-circuit on !Attachable",
			socketPath, err)
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
