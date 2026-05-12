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

//nolint:testpackage // exercises private writeKukettyMetadata and attachableTTYDirInitialPerms.
package runner

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
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

// TestWriteKukettyMetadata_KukeonGroupSet locks the phase-1b TerminalDoc
// rendering when the daemon has a kukeon group configured: socket mode +
// gid are filled in so kuketty applies the kukeon-group ownership the sbsh
// wrapper used to apply via flags. APIVersion/Kind use sbsh's public
// discriminator (api.APIVersionV1Beta1 + api.KindTerminal), and the
// resolved workload argv lands in Spec.Command / Spec.CommandArgs (no
// trailing argv on the OCI side).
//
// Phase 2 (#288) extends the same case to cover the capture-file fields:
// CaptureFile is anchored to the per-container tty dir so `kuke log` and
// sbsh's in-container writer see the same inode through the bind mount,
// and CaptureMode/CaptureGID mirror the socket pattern (gated on kukeon
// group configured).
func TestWriteKukettyMetadata_KukeonGroupSet(t *testing.T) {
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

	if err := r.writeKukettyMetadata(path, spec, kukeonGID, workload); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}

	doc := readDoc(t, path)
	if doc.APIVersion != sbshapi.APIVersionV1Beta1 {
		t.Errorf("APIVersion = %q, want %q", doc.APIVersion, sbshapi.APIVersionV1Beta1)
	}
	if doc.Kind != sbshapi.KindTerminal {
		t.Errorf("Kind = %q, want %q", doc.Kind, sbshapi.KindTerminal)
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
	if doc.Spec.SocketFile != ctr.AttachableSocketPath {
		t.Errorf("Spec.SocketFile = %q, want %q", doc.Spec.SocketFile, ctr.AttachableSocketPath)
	}
	if doc.Spec.RunPath != ctr.AttachableTTYDir {
		t.Errorf("Spec.RunPath = %q, want %q", doc.Spec.RunPath, ctr.AttachableTTYDir)
	}
	if doc.Spec.Command != "/bin/sh" {
		t.Errorf("Spec.Command = %q, want /bin/sh", doc.Spec.Command)
	}
	wantArgs := []string{"-c", "echo hello"}
	if len(doc.Spec.CommandArgs) != len(wantArgs) {
		t.Fatalf("Spec.CommandArgs len = %d, want %d (full=%v)", len(doc.Spec.CommandArgs), len(wantArgs), doc.Spec.CommandArgs)
	}
	for i, want := range wantArgs {
		if doc.Spec.CommandArgs[i] != want {
			t.Errorf("Spec.CommandArgs[%d] = %q, want %q", i, doc.Spec.CommandArgs[i], want)
		}
	}
	// Permission fields: sbsh's builder accepts the canonical octal
	// string "0660" via WithSocketMode and parses it into os.FileMode at
	// build time. The doc round-trips it as the FileMode (perm bits).
	if doc.Spec.SocketMode.Perm() != 0o660 {
		t.Errorf("Spec.SocketMode = %v, want perm 0660", doc.Spec.SocketMode)
	}
	if doc.Spec.SocketGID == nil || *doc.Spec.SocketGID != kukeonGID {
		t.Errorf("Spec.SocketGID = %v, want pointer to %d", doc.Spec.SocketGID, kukeonGID)
	}
	// Capture fields (phase 2 #288): path is anchored on the kukeon-
	// controlled in-container tty dir so the host-side ContainerCapturePath
	// `kuke log` tails resolves to the same inode through the bind mount;
	// mode + gid mirror the socket pattern.
	if doc.Spec.CaptureFile != ctr.AttachableCapturePath {
		t.Errorf("Spec.CaptureFile = %q, want %q", doc.Spec.CaptureFile, ctr.AttachableCapturePath)
	}
	if doc.Spec.CaptureMode.Perm() != 0o640 {
		t.Errorf("Spec.CaptureMode = %v, want perm 0640", doc.Spec.CaptureMode)
	}
	if doc.Spec.CaptureGID == nil || *doc.Spec.CaptureGID != kukeonGID {
		t.Errorf("Spec.CaptureGID = %v, want pointer to %d", doc.Spec.CaptureGID, kukeonGID)
	}
	// Log fields (phase 3 #289) stay zero when the spec carries no
	// Tty.LogFile, even with the kukeon group configured: the log writer
	// is opt-in per-container and a kukeon-group-configured host must
	// not silently start writing sbsh's runtime log on every workload.
	if doc.Spec.LogFile != "" {
		t.Errorf("Spec.LogFile = %q, want empty (no Tty.LogFile)", doc.Spec.LogFile)
	}
	if doc.Spec.LogFileMode.Perm() != 0 {
		t.Errorf("Spec.LogFileMode perm = %#o, want 0 (no Tty.LogFile)", doc.Spec.LogFileMode.Perm())
	}
	if doc.Spec.LogFileGID != nil {
		t.Errorf("Spec.LogFileGID = %v, want nil (no Tty.LogFile)", doc.Spec.LogFileGID)
	}
	// SetPrompt off in phase 1b: arbitrary workloads (nginx, python)
	// would receive a literal `export PS1=…` injection into stdin
	// otherwise. Phase 4 (#290) wires Tty.Prompt through the builder.
	if doc.Spec.SetPrompt {
		t.Errorf("Spec.SetPrompt = true, want false in phase 1b")
	}
	// File permissions: daemon-private (0o600). A future loosening of
	// the parent dir must not silently expose the file.
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat metadata: %v", statErr)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("metadata file mode = %v, want 0o600", perm)
	}
}

// TestWriteKukettyMetadata_NoKukeonGroup locks the legacy fallback: when
// no kukeon group is configured (GID 0), neither SocketMode nor SocketGID
// is set on the spec so the sbsh server leaves the OS-default
// (umask-clipped) permissions on the socket inode — matching the sbsh
// wrapper's behavior on a host with no kukeon group.
//
// Phase 2 (#288) extends the same case to cover the capture-file fields:
// CaptureFile is still anchored to the kukeon-controlled per-container tty
// dir so `kuke log` resolves to the same inode regardless of group config;
// CaptureMode + CaptureGID stay zero so sbsh's runner falls through to its
// OS-default mode and leaves the group unchanged.
func TestWriteKukettyMetadata_NoKukeonGroup(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{ID: "c1"}

	if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.SocketMode.Perm() != 0 {
		t.Errorf("Spec.SocketMode perm = %#o, want 0 (no kukeon group)", doc.Spec.SocketMode.Perm())
	}
	if doc.Spec.SocketGID != nil {
		t.Errorf("Spec.SocketGID = %v, want nil (no kukeon group)", doc.Spec.SocketGID)
	}
	// CaptureFile is set regardless of group config: kuketty must write
	// the transcript at the kukeon-controlled bind-mount path so `kuke log`
	// keeps tailing the host-side peer of the same inode. Mode + GID stay
	// zero so sbsh's runner defaults apply.
	if doc.Spec.CaptureFile != ctr.AttachableCapturePath {
		t.Errorf("Spec.CaptureFile = %q, want %q", doc.Spec.CaptureFile, ctr.AttachableCapturePath)
	}
	if doc.Spec.CaptureMode.Perm() != 0 {
		t.Errorf("Spec.CaptureMode perm = %#o, want 0 (no kukeon group)", doc.Spec.CaptureMode.Perm())
	}
	if doc.Spec.CaptureGID != nil {
		t.Errorf("Spec.CaptureGID = %v, want nil (no kukeon group)", doc.Spec.CaptureGID)
	}
	// Log fields (phase 3 #289) stay zero with no Tty.LogFile and no
	// kukeon group — the same opt-in invariant as the group-set case.
	if doc.Spec.LogFile != "" {
		t.Errorf("Spec.LogFile = %q, want empty (no Tty.LogFile)", doc.Spec.LogFile)
	}
	if doc.Spec.LogFileMode.Perm() != 0 {
		t.Errorf("Spec.LogFileMode perm = %#o, want 0 (no Tty.LogFile)", doc.Spec.LogFileMode.Perm())
	}
	if doc.Spec.LogFileGID != nil {
		t.Errorf("Spec.LogFileGID = %v, want nil (no Tty.LogFile)", doc.Spec.LogFileGID)
	}
}

// TestWriteKukettyMetadata_LogFileSet locks phase 3 (#289) rendering when
// the cell's container-tty config opts into the sbsh runner log writer:
// Spec.LogFile carries the user-supplied path verbatim (no daemon-side
// path rewriting — operators choose where it lands); LogFileMode and
// LogFileGID mirror the socket/capture treatment, gated on the kukeon
// group being configured. The two subcases fix the exhaustive matrix
// against silent regressions.
func TestWriteKukettyMetadata_LogFileSet(t *testing.T) {
	cases := []struct {
		name        string
		kukeonGID   int
		wantMode    os.FileMode
		wantGIDPtr  bool
		wantGIDVal  int
		logFilePath string
	}{
		{
			// Kukeon group configured: mode + gid filled in so a
			// non-root operator in the kukeon group can read the log.
			name:        "kukeon group set: mode+gid populated",
			kukeonGID:   986,
			wantMode:    0o640,
			wantGIDPtr:  true,
			wantGIDVal:  986,
			logFilePath: ctr.AttachableLogfilePath,
		},
		{
			// No kukeon group: mode+gid stay zero so sbsh's runner
			// applies its OS-default umask-clipped permissions and
			// leaves the group untouched (matches socket/capture).
			name:        "no kukeon group: mode+gid stay zero",
			kukeonGID:   0,
			wantMode:    0,
			wantGIDPtr:  false,
			logFilePath: ctr.AttachableLogfilePath,
		},
		{
			// User-supplied path outside the bind-mount: passed
			// through verbatim. The operator owns the choice;
			// the daemon does not rewrite or anchor it.
			name:        "non-bind-mount path passed through verbatim",
			kukeonGID:   986,
			wantMode:    0o640,
			wantGIDPtr:  true,
			wantGIDVal:  986,
			logFilePath: "/var/log/sbsh.log",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{
				ID:  "c1",
				Tty: &intmodel.ContainerTty{LogFile: tc.logFilePath},
			}
			if err := r.writeKukettyMetadata(path, spec, tc.kukeonGID, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyMetadata: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.LogFile != tc.logFilePath {
				t.Errorf("Spec.LogFile = %q, want %q", doc.Spec.LogFile, tc.logFilePath)
			}
			if doc.Spec.LogFileMode.Perm() != tc.wantMode {
				t.Errorf("Spec.LogFileMode perm = %#o, want %#o", doc.Spec.LogFileMode.Perm(), tc.wantMode)
			}
			switch {
			case tc.wantGIDPtr && (doc.Spec.LogFileGID == nil || *doc.Spec.LogFileGID != tc.wantGIDVal):
				t.Errorf("Spec.LogFileGID = %v, want pointer to %d", doc.Spec.LogFileGID, tc.wantGIDVal)
			case !tc.wantGIDPtr && doc.Spec.LogFileGID != nil:
				t.Errorf("Spec.LogFileGID = %v, want nil", doc.Spec.LogFileGID)
			}
		})
	}
}

// TestWriteKukettyMetadata_EmptyWorkloadFallsBackToBuilderDefault: when
// the OCI args-wrap captures an empty Process.Args (image with no
// ENTRYPOINT/CMD and no user override), the renderer leaves Spec.Command
// at sbsh's hardcoded default (/bin/bash -i) rather than rendering an
// empty Command — server.New rejects empty Command, which would brick
// every container with an unset entrypoint.
func TestWriteKukettyMetadata_EmptyWorkloadFallsBackToBuilderDefault(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	if err := r.writeKukettyMetadata(path, intmodel.ContainerSpec{ID: "c1"}, 0, nil); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Command == "" {
		t.Errorf("Spec.Command is empty; expected the sbsh builder's hardcoded fallback")
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

func newTestRunner(t *testing.T) *Exec {
	t.Helper()
	return &Exec{
		ctx:    context.Background(),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func readDoc(t *testing.T, path string) sbshapi.TerminalDoc {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc sbshapi.TerminalDoc
	if err = json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	return doc
}
