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
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
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
		t.Fatalf(
			"Spec.CommandArgs len = %d, want %d (full=%v)",
			len(doc.Spec.CommandArgs),
			len(wantArgs),
			doc.Spec.CommandArgs,
		)
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
	// Kuketty log fields are always-on at the daemon-controlled
	// AttachableKukettyLogPath (issue #599 reverses #289 phase 3 for the
	// kuketty-process log specifically). The kukeon group is configured
	// here, so mode + gid track the socket/capture pattern.
	if doc.Spec.LogFile != ctr.AttachableKukettyLogPath {
		t.Errorf("Spec.LogFile = %q, want %q (always-on kuketty log)", doc.Spec.LogFile, ctr.AttachableKukettyLogPath)
	}
	if doc.Spec.LogFileMode.Perm() != 0o640 {
		t.Errorf("Spec.LogFileMode perm = %#o, want 0640 (kukeon group set)", doc.Spec.LogFileMode.Perm())
	}
	if doc.Spec.LogFileGID == nil || *doc.Spec.LogFileGID != kukeonGID {
		t.Errorf("Spec.LogFileGID = %v, want pointer to %d", doc.Spec.LogFileGID, kukeonGID)
	}
	// LogLevel defaults to "info" when the cell omits it (sbsh's
	// NewFileLogger rejects an empty level, so the daemon pins the
	// default rather than threading the fallback through kuketty).
	if doc.Spec.LogLevel != "info" {
		t.Errorf("Spec.LogLevel = %q, want %q (default when Tty.LogLevel is empty)", doc.Spec.LogLevel, "info")
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
	// Kuketty log: always-on path even without a kukeon group, but
	// mode + GID stay zero so sbsh's runner applies its OS-default
	// (umask-clipped) mode and leaves the group untouched (matches
	// the socket/capture treatment when no kukeon group is configured).
	if doc.Spec.LogFile != ctr.AttachableKukettyLogPath {
		t.Errorf("Spec.LogFile = %q, want %q (always-on kuketty log)", doc.Spec.LogFile, ctr.AttachableKukettyLogPath)
	}
	if doc.Spec.LogFileMode.Perm() != 0 {
		t.Errorf("Spec.LogFileMode perm = %#o, want 0 (no kukeon group)", doc.Spec.LogFileMode.Perm())
	}
	if doc.Spec.LogFileGID != nil {
		t.Errorf("Spec.LogFileGID = %v, want nil (no kukeon group)", doc.Spec.LogFileGID)
	}
	if doc.Spec.LogLevel != "info" {
		t.Errorf("Spec.LogLevel = %q, want %q (default)", doc.Spec.LogLevel, "info")
	}
}

// TestWriteKukettyMetadata_KukettyLogAlwaysOn locks issue #599's reversal of
// #289 phase 3 for the kuketty-process log specifically: regardless of cell
// YAML, every Attachable container renders Spec.LogFile pointing at the
// daemon-controlled AttachableKukettyLogPath (peer to socket/capture inside
// the per-container tty bind mount). Operators do not pick the path — the
// only knob the cell schema surfaces is verbosity, validated separately.
func TestWriteKukettyMetadata_KukettyLogAlwaysOn(t *testing.T) {
	cases := []struct {
		name      string
		kukeonGID int
		tty       *intmodel.ContainerTty
		wantMode  os.FileMode
		wantGID   *int
	}{
		{
			// No Tty block at all → still produces a log writer at the
			// fixed daemon path. The pre-#599 design left Spec.LogFile
			// empty here and operators had no diagnostic to read.
			name:      "no Tty block: log still rendered",
			kukeonGID: 986,
			tty:       nil,
			wantMode:  0o640,
			wantGID:   intPtr(986),
		},
		{
			// Tty block carries unrelated fields (prompt) → log stays
			// always-on.
			name:      "Tty without LogLevel: log still rendered",
			kukeonGID: 986,
			tty:       &intmodel.ContainerTty{Prompt: `claude> `},
			wantMode:  0o640,
			wantGID:   intPtr(986),
		},
		{
			// No kukeon group → log path still set, but mode + GID
			// stay zero so sbsh's runner applies OS-default perms.
			name:      "no kukeon group: log path set, mode+gid zero",
			kukeonGID: 0,
			tty:       nil,
			wantMode:  0,
			wantGID:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{ID: "c1", Tty: tc.tty}
			if err := r.writeKukettyMetadata(path, spec, tc.kukeonGID, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyMetadata: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.LogFile != ctr.AttachableKukettyLogPath {
				t.Errorf("Spec.LogFile = %q, want %q (always-on)", doc.Spec.LogFile, ctr.AttachableKukettyLogPath)
			}
			if doc.Spec.LogFileMode.Perm() != tc.wantMode {
				t.Errorf("Spec.LogFileMode perm = %#o, want %#o", doc.Spec.LogFileMode.Perm(), tc.wantMode)
			}
			switch {
			case tc.wantGID != nil && (doc.Spec.LogFileGID == nil || *doc.Spec.LogFileGID != *tc.wantGID):
				t.Errorf("Spec.LogFileGID = %v, want pointer to %d", doc.Spec.LogFileGID, *tc.wantGID)
			case tc.wantGID == nil && doc.Spec.LogFileGID != nil:
				t.Errorf("Spec.LogFileGID = %v, want nil", doc.Spec.LogFileGID)
			}
		})
	}
}

// TestWriteKukettyMetadata_LogLevelHonored locks the LogLevel plumbing
// added in issue #599: when the cell sets Tty.LogLevel, the renderer
// stamps it onto Spec.LogLevel verbatim; when the cell omits it (or
// passes "" explicitly), the renderer pins the "info" default daemon-side
// so kuketty's sbshlogging.NewFileLogger (which rejects an empty level)
// always sees a usable value.
func TestWriteKukettyMetadata_LogLevelHonored(t *testing.T) {
	cases := []struct {
		name      string
		tty       *intmodel.ContainerTty
		wantLevel string
	}{
		{"empty Tty: default info", nil, "info"},
		{"empty LogLevel: default info", &intmodel.ContainerTty{Prompt: "x> "}, "info"},
		{"debug", &intmodel.ContainerTty{LogLevel: "debug"}, "debug"},
		{"warn", &intmodel.ContainerTty{LogLevel: "warn"}, "warn"},
		{"error", &intmodel.ContainerTty{LogLevel: "error"}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{ID: "c1", Tty: tc.tty}
			if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyMetadata: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.LogLevel != tc.wantLevel {
				t.Errorf("Spec.LogLevel = %q, want %q", doc.Spec.LogLevel, tc.wantLevel)
			}
		})
	}
}

// TestWriteKukettyMetadata_LogFileOverride locks the operator-override path
// that #599's reviewer asked us to preserve: when a cell pins Tty.LogFile to
// a custom in-container location (e.g., a fixed external mount), the renderer
// stamps it verbatim — no daemon-side rewriting, no anchoring to the bind
// mount. Default (LogFile empty) still resolves to the daemon-controlled
// AttachableKukettyLogPath so the always-on contract from issue #599 holds.
func TestWriteKukettyMetadata_LogFileOverride(t *testing.T) {
	cases := []struct {
		name    string
		tty     *intmodel.ContainerTty
		wantLog string
	}{
		{
			"no Tty: daemon default",
			nil,
			ctr.AttachableKukettyLogPath,
		},
		{
			"empty LogFile: daemon default",
			&intmodel.ContainerTty{Prompt: "x> "},
			ctr.AttachableKukettyLogPath,
		},
		{
			"operator override inside bind mount",
			&intmodel.ContainerTty{LogFile: ctr.AttachableTTYDir + "/custom.log"},
			ctr.AttachableTTYDir + "/custom.log",
		},
		{
			"operator override outside bind mount",
			&intmodel.ContainerTty{LogFile: "/var/log/sbsh-debug.log"},
			"/var/log/sbsh-debug.log",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{ID: "c1", Tty: tc.tty}
			if err := r.writeKukettyMetadata(path, spec, 986, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyMetadata: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.LogFile != tc.wantLog {
				t.Errorf("Spec.LogFile = %q, want %q", doc.Spec.LogFile, tc.wantLog)
			}
			// Always-on invariant: regardless of override or default,
			// Spec.LogFile is non-empty so kuketty's openTerminalLogger
			// never falls through to the discard logger.
			if doc.Spec.LogFile == "" {
				t.Errorf("Spec.LogFile is empty; always-on invariant from #599 broken")
			}
		})
	}
}

// TestWriteKukettyMetadata_LogLevelGlobalDefault locks #599's reviewer-asked
// precedence chain for the kuketty log level: per-container Tty.LogLevel →
// server-config kuketty.logLevel (plumbed via runner.Options.KukettyLogLevel)
// → hardcoded "info". The daemon-wide knob lets operators flip every
// attachable cell on the host without editing each cell YAML; the per-
// container knob still wins so cell authors retain a local override.
func TestWriteKukettyMetadata_LogLevelGlobalDefault(t *testing.T) {
	cases := []struct {
		name             string
		tty              *intmodel.ContainerTty
		serverConfig     string
		wantLevel        string
		whyCommentForLog string
	}{
		{
			name:             "per-container wins over server-config",
			tty:              &intmodel.ContainerTty{LogLevel: "debug"},
			serverConfig:     "warn",
			wantLevel:        "debug",
			whyCommentForLog: "cell-supplied LogLevel must override the daemon-wide default",
		},
		{
			name:             "server-config wins when per-container empty",
			tty:              nil,
			serverConfig:     "warn",
			wantLevel:        "warn",
			whyCommentForLog: "daemon-wide knob applies when the cell omits LogLevel",
		},
		{
			name:             "server-config wins with non-LogLevel Tty fields",
			tty:              &intmodel.ContainerTty{Prompt: "x> "},
			serverConfig:     "error",
			wantLevel:        "error",
			whyCommentForLog: "Tty present but LogLevel empty still falls through to server-config",
		},
		{
			name:             "hardcoded info when both empty",
			tty:              nil,
			serverConfig:     "",
			wantLevel:        "info",
			whyCommentForLog: "no per-container, no server-config — last-resort 'info' from the renderer",
		},
		{
			name:             "per-container wins over empty server-config",
			tty:              &intmodel.ContainerTty{LogLevel: "debug"},
			serverConfig:     "",
			wantLevel:        "debug",
			whyCommentForLog: "cell wins regardless of whether the server-config tier is populated",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRunner(t)
			r.opts.KukettyLogLevel = tc.serverConfig
			dir := t.TempDir()
			path := filepath.Join(dir, "kuketty-metadata.json")
			spec := intmodel.ContainerSpec{ID: "c1", Tty: tc.tty}
			if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
				t.Fatalf("writeKukettyMetadata: %v", err)
			}
			doc := readDoc(t, path)
			if doc.Spec.LogLevel != tc.wantLevel {
				t.Errorf(
					"Spec.LogLevel = %q, want %q (%s)",
					doc.Spec.LogLevel, tc.wantLevel, tc.whyCommentForLog,
				)
			}
		})
	}
}

// intPtr is a one-off helper for the always-on log table-test to avoid the
// awkward "&literal" pattern inside struct initializers.
func intPtr(v int) *int { return &v }

// ttyFieldsCoveredByMapping is the structural half of #493's "every Tty
// field must map" guard: it enumerates intmodel.ContainerTty via
// reflection and fails the test when a declared field is missing from
// mapperedFields, or when mapperedFields carries a stale entry the
// struct no longer declares. The comprehensive test below supplies the
// behavioral half (each mapped field has a TerminalDoc.Spec assertion).
//
// New fields landing on intmodel.ContainerTty must touch two places to
// keep the test green:
//
//  1. Add the field name to mapperedFields here.
//  2. Wire it through writeKukettyMetadata and add an assertion to
//     TestWriteKukettyMetadata_AllTtyFieldsMap.
//
// Skipping step 1 fails the structural check; skipping step 2 fails the
// per-field behavioral check. The pre-#494 silent-drop pattern (Tty.OnInit
// declared on the schema but never read by the renderer) fails (2).
func ttyFieldsCoveredByMapping(t *testing.T) {
	t.Helper()
	// Local map so the linter's gochecknoglobals rule stays clean and the
	// list reads as part of the test contract rather than a package
	// secret. Keep alphabetized.
	mapperedFields := map[string]struct{}{
		"LogFile":  {},
		"LogLevel": {},
		"OnInit":   {},
		"Prompt":   {},
	}
	typ := reflect.TypeOf(intmodel.ContainerTty{})
	declared := make(map[string]struct{}, typ.NumField())
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		declared[name] = struct{}{}
		if _, ok := mapperedFields[name]; !ok {
			t.Errorf(
				"intmodel.ContainerTty.%s is declared on the cell schema but missing from "+
					"mapperedFields. Decide whether writeKukettyMetadata should stamp it onto "+
					"TerminalDoc.Spec; if yes, wire it through and extend "+
					"TestWriteKukettyMetadata_AllTtyFieldsMap with an assertion; if no, document "+
					"the deliberate drop in a code comment and still add the field here so the "+
					"reflect check sees it.",
				name,
			)
		}
	}
	for name := range mapperedFields {
		if _, ok := declared[name]; !ok {
			t.Errorf(
				"mapperedFields references %q but intmodel.ContainerTty no longer declares it — "+
					"drop the stale entry.",
				name,
			)
		}
	}
}

// TestWriteKukettyMetadata_AllTtyFieldsMap is the comprehensive renderer
// guard #493 calls for. It exercises three concentric invariants:
//
//  1. Reflective coverage (ttyFieldsCoveredByMapping): every field declared
//     on intmodel.ContainerTty is listed in mapperedFields. New fields
//     must touch this file or the test fails.
//  2. Per-field behavior: each Tty.* knob set on the input has a non-zero
//     counterpart on the rendered TerminalDoc.Spec. The pre-#494 Tty.OnInit
//     drop (declared on the schema, never read by the renderer) is the
//     canonical failure mode this catches.
//  3. Renderer-default invariants the cell schema does NOT surface but the
//     renderer is responsible for stamping on every TerminalDoc:
//     EnvInherit, SocketFile, CaptureFile, RunPath. EnvInherit=true is the
//     bug fix from #494 that this test pins — without it, sbsh's terminal
//     runner spawns the workload with HOME + SBSH_* only, stripping the
//     user-supplied env and the KUKEON_* identity vars the OCI Process.Env
//     already carries (sbsh@v0.11.2/internal/terminal/terminalrunner/
//     terminal.go:54). Pre-#494 the profile loader's no-profile fallback
//     defaulted EnvInherit=true (sbsh@v0.11.1/internal/profile/
//     profile.go:454), so the inline-builder migration silently regressed
//     env passthrough.
//
// When a new field lands on ContainerTty, this is the test that fails
// until the renderer is taught to forward it and the mapping list is
// updated.
func TestWriteKukettyMetadata_AllTtyFieldsMap(t *testing.T) {
	ttyFieldsCoveredByMapping(t)

	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")

	const wantPrompt = `"\u@\h:\w\$ "`
	wantOnInit := []intmodel.TtyStage{
		{Script: "echo hello"},
		{Script: "echo world"},
	}
	const wantLogLevel = "debug"
	// Exercise the LogFile override (#599 reviewer ask): the renderer must
	// stamp the operator-supplied path verbatim instead of the daemon
	// default at ctr.AttachableKukettyLogPath. Picked outside the bind-
	// mount root to prove the daemon does not anchor or rewrite the path.
	const wantLogFile = "/var/log/kuketty-override.log"
	const kukeonGID = 986
	workload := []string{"/bin/sh", "-c", "exec /workload"}

	spec := intmodel.ContainerSpec{
		ID:         "c1",
		RealmName:  "rA",
		SpaceName:  "sB",
		StackName:  "kC",
		CellName:   "lD",
		Attachable: true,
		Tty: &intmodel.ContainerTty{
			Prompt:   wantPrompt,
			OnInit:   wantOnInit,
			LogFile:  wantLogFile,
			LogLevel: wantLogLevel,
		},
	}

	if err := r.writeKukettyMetadata(path, spec, kukeonGID, workload); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)

	// Tty.Prompt → Spec.Prompt + Spec.SetPrompt (sbsh's `SetPrompt =
	// !DisableSetPrompt` rule flips SetPrompt on for a non-empty prompt).
	if doc.Spec.Prompt != wantPrompt {
		t.Errorf("Tty.Prompt: Spec.Prompt = %q, want %q", doc.Spec.Prompt, wantPrompt)
	}
	if !doc.Spec.SetPrompt {
		t.Errorf("Tty.Prompt: Spec.SetPrompt = false, want true (non-empty inline prompt)")
	}

	// Tty.OnInit → Spec.Stages.OnInit. The pre-#494 silent-drop fix: each
	// TtyStage.Script lands as an api.ExecStep.Script in order. Stages.PostAttach
	// is not surfaced on the cell schema and must stay zero.
	if len(doc.Spec.Stages.OnInit) != len(wantOnInit) {
		t.Fatalf(
			"Tty.OnInit: Spec.Stages.OnInit len = %d, want %d (full=%+v)",
			len(doc.Spec.Stages.OnInit), len(wantOnInit), doc.Spec.Stages.OnInit,
		)
	}
	for i, want := range wantOnInit {
		if doc.Spec.Stages.OnInit[i].Script != want.Script {
			t.Errorf(
				"Tty.OnInit[%d]: Spec.Stages.OnInit[%d].Script = %q, want %q",
				i, i, doc.Spec.Stages.OnInit[i].Script, want.Script,
			)
		}
	}
	if len(doc.Spec.Stages.PostAttach) != 0 {
		t.Errorf("Spec.Stages.PostAttach = %+v, want empty (not surfaced on cell schema)", doc.Spec.Stages.PostAttach)
	}

	// Tty.LogLevel → Spec.LogLevel (verbatim). The wrapper-log path
	// itself is daemon-controlled and asserted via the always-on
	// Spec.LogFile invariant a few lines below — operators only pick
	// verbosity (issue #599).
	if doc.Spec.LogLevel != wantLogLevel {
		t.Errorf("Tty.LogLevel: Spec.LogLevel = %q, want %q", doc.Spec.LogLevel, wantLogLevel)
	}

	// Renderer-default invariants the cell schema does NOT surface. These
	// are kukeon-side contracts the renderer must stamp regardless of cell
	// YAML — a regression in any of them silently changes runtime behavior.
	if !doc.Spec.EnvInherit {
		t.Errorf(
			"Spec.EnvInherit = false, want true. kuketty must forward its os.Environ() " +
				"(== OCI Process.Env, which contains user env + KUKEON_*) to the workload; " +
				"without WithEnvInherit(true), sbsh's runner strips everything but HOME + SBSH_* " +
				"(sbsh@v0.11.2/internal/terminal/terminalrunner/terminal.go:54). #494 regression.",
		)
	}
	if doc.Spec.SocketFile != ctr.AttachableSocketPath {
		t.Errorf(
			"Spec.SocketFile = %q, want %q (kukeon-controlled bind-mount path)",
			doc.Spec.SocketFile,
			ctr.AttachableSocketPath,
		)
	}
	if doc.Spec.CaptureFile != ctr.AttachableCapturePath {
		t.Errorf(
			"Spec.CaptureFile = %q, want %q (kukeon-controlled bind-mount path)",
			doc.Spec.CaptureFile,
			ctr.AttachableCapturePath,
		)
	}
	// Issue #599: Tty.LogFile override is stamped verbatim. Always-on
	// behavior (Spec.LogFile non-empty regardless of cell YAML) is
	// pinned by TestWriteKukettyMetadata_KukettyLogAlwaysOn; here we
	// prove the override path round-trips without daemon-side rewriting.
	if doc.Spec.LogFile != wantLogFile {
		t.Errorf(
			"Tty.LogFile: Spec.LogFile = %q, want %q (operator override stamped verbatim)",
			doc.Spec.LogFile,
			wantLogFile,
		)
	}
	if doc.Spec.RunPath != ctr.AttachableTTYDir {
		t.Errorf("Spec.RunPath = %q, want %q (sbsh's per-terminal root)", doc.Spec.RunPath, ctr.AttachableTTYDir)
	}

	// workloadArgv → Spec.Command + Spec.CommandArgs. Sanity-checked here
	// alongside the Tty assertions because the comprehensive test is the
	// single place a contributor can read off "what the renderer produces
	// from a fully-populated input" without chasing per-field tests.
	if doc.Spec.Command != workload[0] {
		t.Errorf("workloadArgv: Spec.Command = %q, want %q", doc.Spec.Command, workload[0])
	}
	if len(doc.Spec.CommandArgs) != len(workload)-1 {
		t.Fatalf("workloadArgv: Spec.CommandArgs len = %d, want %d", len(doc.Spec.CommandArgs), len(workload)-1)
	}
	for i, want := range workload[1:] {
		if doc.Spec.CommandArgs[i] != want {
			t.Errorf("workloadArgv: Spec.CommandArgs[%d] = %q, want %q", i, doc.Spec.CommandArgs[i], want)
		}
	}
}

// TestWriteKukettyMetadata_PromptConfigured locks the inline-Prompt lane
// (#494): when the cell sets Tty.Prompt, the renderer stamps it onto
// Spec.Prompt and flips Spec.SetPrompt on via sbsh's WithPrompt +
// WithDisableSetPrompt(false). No profile YAML is loaded — the renderer
// uses sbsh's inline BuildTerminalSpec entry point (sbsh v0.11.2 #209).
// Replaces the pre-#494 phase-4 profile-coupling test that round-tripped a
// TerminalProfile YAML through sbsh's pkg/discovery loader.
func TestWriteKukettyMetadata_PromptConfigured(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	const wantPrompt = `"\[\e[1;36m\]claude \u@\h\[\e[0m\]:\w\$ "`
	spec := intmodel.ContainerSpec{
		ID:  "c1",
		Tty: &intmodel.ContainerTty{Prompt: wantPrompt},
	}

	if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.Prompt != wantPrompt {
		t.Errorf("Spec.Prompt = %q, want %q", doc.Spec.Prompt, wantPrompt)
	}
	if !doc.Spec.SetPrompt {
		t.Errorf("Spec.SetPrompt = false, want true (non-empty inline Tty.Prompt flips it on)")
	}
}

// TestWriteKukettyMetadata_OnInitConfigured locks the inline-OnInit lane
// (#494): when the cell sets Tty.OnInit, the renderer forwards each
// TtyStage.Script to sbsh's WithOnInit as an api.ExecStep, in order, and
// the rendered Spec.Stages.OnInit carries the same scripts. Closes the
// pre-#494 silent drop where OnInit set on a cell with no profile was
// never stamped onto the TerminalDoc (problem 1 in #494's Context).
func TestWriteKukettyMetadata_OnInitConfigured(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{
		ID: "c1",
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{
				{Script: "echo hello"},
				{Script: "echo world"},
			},
		},
	}

	if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)
	if len(doc.Spec.Stages.OnInit) != 2 {
		t.Fatalf("Spec.Stages.OnInit len = %d, want 2", len(doc.Spec.Stages.OnInit))
	}
	if doc.Spec.Stages.OnInit[0].Script != "echo hello" ||
		doc.Spec.Stages.OnInit[1].Script != "echo world" {
		t.Errorf("Spec.Stages.OnInit = %+v, want [echo hello, echo world]", doc.Spec.Stages.OnInit)
	}
	if len(doc.Spec.Stages.PostAttach) != 0 {
		t.Errorf("Spec.Stages.PostAttach = %+v, want empty (cell did not set it)", doc.Spec.Stages.PostAttach)
	}
}

// TestWriteKukettyMetadata_EmptyTtyKeepsSafeDefault locks the safe-default
// branch for an unconfigured Tty (#494): no cell-level Prompt or OnInit
// means Spec.SetPrompt stays false (DisableSetPrompt(true) is forced) and
// Spec.Stages stays zero. Without the DisableSetPrompt(true) belt, sbsh's
// inline builder would flip SetPrompt on by default (`SetPrompt =
// !DisableSetPrompt`), which would inject a literal PS1 onto a non-shell
// workload's stdin. Same invariant the pre-#494 NoProfileKeepsSafeDefault
// test enforced, now on the profile-free renderer.
func TestWriteKukettyMetadata_EmptyTtyKeepsSafeDefault(t *testing.T) {
	r := newTestRunner(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "kuketty-metadata.json")
	spec := intmodel.ContainerSpec{ID: "c1"}

	if err := r.writeKukettyMetadata(path, spec, 0, []string{"/bin/sh"}); err != nil {
		t.Fatalf("writeKukettyMetadata: %v", err)
	}
	doc := readDoc(t, path)
	if doc.Spec.SetPrompt {
		t.Errorf("Spec.SetPrompt = true, want false (no inline Prompt → DisableSetPrompt(true))")
	}
	if doc.Spec.Prompt != "" {
		t.Errorf("Spec.Prompt = %q, want empty (no inline Tty.Prompt)", doc.Spec.Prompt)
	}
	if len(doc.Spec.Stages.OnInit) != 0 {
		t.Errorf("Spec.Stages.OnInit = %+v, want empty (no inline Tty.OnInit)", doc.Spec.Stages.OnInit)
	}
	if len(doc.Spec.Stages.PostAttach) != 0 {
		t.Errorf("Spec.Stages.PostAttach = %+v, want empty (no inline Tty.OnInit)", doc.Spec.Stages.PostAttach)
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
