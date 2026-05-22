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

package main

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
)

// buildSpec runs the transform under test against a discard logger and fails
// the test on error. The transform moved into kuketty in issue #641 (it used
// to live daemon-side as the runner's writeKukettyMetadata); these tests are
// the ported coverage the AC requires.
func buildSpec(t *testing.T, spec v1beta1.ContainerSpec) *sbshapi.TerminalSpec {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	out, err := buildTerminalSpec(context.Background(), logger, spec)
	if err != nil {
		t.Fatalf("buildTerminalSpec: %v", err)
	}
	return out
}

// TestBuildTerminalSpec_KukeonGroupSet locks the TerminalSpec the transform
// produces when the daemon stamped a kukeon-group GID into Spec.KukeonGroupGID:
// socket/capture/log mode + gid are filled in so kuketty applies the
// kukeon-group ownership the daemon used to fold into the rendered spec, and
// the resolved workload argv lands in Spec.Command / Spec.CommandArgs.
func TestBuildTerminalSpec_KukeonGroupSet(t *testing.T) {
	const kukeonGID = 986
	spec := v1beta1.ContainerSpec{
		ID:             "c1",
		Command:        "/bin/sh",
		Args:           []string{"-c", "echo hello"},
		KukeonGroupGID: kukeonGID,
		Tty:            &v1beta1.ContainerTty{LogLevel: "info"},
	}

	out := buildSpec(t, spec)

	if out.SocketFile != attachableSocketPath {
		t.Errorf("SocketFile = %q, want %q", out.SocketFile, attachableSocketPath)
	}
	if out.RunPath != attachableTTYDir {
		t.Errorf("RunPath = %q, want %q", out.RunPath, attachableTTYDir)
	}
	if out.Command != "/bin/sh" {
		t.Errorf("Command = %q, want /bin/sh", out.Command)
	}
	wantArgs := []string{"-c", "echo hello"}
	if !reflect.DeepEqual(out.CommandArgs, wantArgs) {
		t.Errorf("CommandArgs = %v, want %v", out.CommandArgs, wantArgs)
	}
	if out.SocketMode.Perm() != 0o660 {
		t.Errorf("SocketMode = %v, want perm 0660", out.SocketMode)
	}
	if out.SocketGID == nil || *out.SocketGID != kukeonGID {
		t.Errorf("SocketGID = %v, want pointer to %d", out.SocketGID, kukeonGID)
	}
	if out.CaptureFile != attachableCapturePath {
		t.Errorf("CaptureFile = %q, want %q", out.CaptureFile, attachableCapturePath)
	}
	if out.CaptureMode.Perm() != 0o640 {
		t.Errorf("CaptureMode = %v, want perm 0640", out.CaptureMode)
	}
	if out.CaptureGID == nil || *out.CaptureGID != kukeonGID {
		t.Errorf("CaptureGID = %v, want pointer to %d", out.CaptureGID, kukeonGID)
	}
	if out.LogFile != attachableKukettyLogPath {
		t.Errorf("LogFile = %q, want %q", out.LogFile, attachableKukettyLogPath)
	}
	if out.LogFileMode.Perm() != 0o640 {
		t.Errorf("LogFileMode perm = %#o, want 0640", out.LogFileMode.Perm())
	}
	if out.LogFileGID == nil || *out.LogFileGID != kukeonGID {
		t.Errorf("LogFileGID = %v, want pointer to %d", out.LogFileGID, kukeonGID)
	}
	if out.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want info", out.LogLevel)
	}
	if out.SetPrompt {
		t.Errorf("SetPrompt = true, want false (no prompt)")
	}
}

// TestBuildTerminalSpec_NoKukeonGroup locks the legacy fallback: with no
// kukeon group (GID 0), neither mode nor gid is set on the socket / capture /
// log inodes so sbsh's runner leaves OS-default (umask-clipped) permissions —
// matching the sbsh wrapper's behavior on a host with no kukeon group. The
// capture/log file paths are still anchored to the kukeon-controlled tty dir.
func TestBuildTerminalSpec_NoKukeonGroup(t *testing.T) {
	out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh"})

	if out.SocketMode.Perm() != 0 {
		t.Errorf("SocketMode perm = %#o, want 0 (no kukeon group)", out.SocketMode.Perm())
	}
	if out.SocketGID != nil {
		t.Errorf("SocketGID = %v, want nil (no kukeon group)", out.SocketGID)
	}
	if out.CaptureFile != attachableCapturePath {
		t.Errorf("CaptureFile = %q, want %q", out.CaptureFile, attachableCapturePath)
	}
	if out.CaptureMode.Perm() != 0 {
		t.Errorf("CaptureMode perm = %#o, want 0 (no kukeon group)", out.CaptureMode.Perm())
	}
	if out.CaptureGID != nil {
		t.Errorf("CaptureGID = %v, want nil (no kukeon group)", out.CaptureGID)
	}
	if out.LogFile != attachableKukettyLogPath {
		t.Errorf("LogFile = %q, want %q (always-on)", out.LogFile, attachableKukettyLogPath)
	}
	if out.LogFileMode.Perm() != 0 {
		t.Errorf("LogFileMode perm = %#o, want 0 (no kukeon group)", out.LogFileMode.Perm())
	}
	if out.LogFileGID != nil {
		t.Errorf("LogFileGID = %v, want nil (no kukeon group)", out.LogFileGID)
	}
}

// TestBuildTerminalSpec_KukettyLogAlwaysOn locks issue #599's always-on
// kuketty-process log: regardless of cell YAML, every Attachable container
// renders Spec.LogFile pointing at the daemon-controlled default path (peer to
// socket/capture inside the per-container tty bind mount). The mode + gid track
// the kukeon-group config.
func TestBuildTerminalSpec_KukettyLogAlwaysOn(t *testing.T) {
	cases := []struct {
		name      string
		kukeonGID int
		tty       *v1beta1.ContainerTty
		wantMode  uint32
		wantGID   *int
	}{
		{"no Tty block: log still rendered", 986, nil, 0o640, intPtr(986)},
		{
			"Tty without LogLevel: log still rendered",
			986,
			&v1beta1.ContainerTty{Prompt: `claude> `},
			0o640,
			intPtr(986),
		},
		{"no kukeon group: log path set, mode+gid zero", 0, nil, 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildSpec(
				t,
				v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh", KukeonGroupGID: tc.kukeonGID, Tty: tc.tty},
			)
			if out.LogFile != attachableKukettyLogPath {
				t.Errorf("LogFile = %q, want %q (always-on)", out.LogFile, attachableKukettyLogPath)
			}
			if uint32(out.LogFileMode.Perm()) != tc.wantMode {
				t.Errorf("LogFileMode perm = %#o, want %#o", out.LogFileMode.Perm(), tc.wantMode)
			}
			switch {
			case tc.wantGID != nil && (out.LogFileGID == nil || *out.LogFileGID != *tc.wantGID):
				t.Errorf("LogFileGID = %v, want pointer to %d", out.LogFileGID, *tc.wantGID)
			case tc.wantGID == nil && out.LogFileGID != nil:
				t.Errorf("LogFileGID = %v, want nil", out.LogFileGID)
			}
		})
	}
}

// TestBuildTerminalSpec_LogLevelVerbatim locks the post-#641 contract that
// kuketty reads the level the daemon resolved and stamped onto Tty.LogLevel
// verbatim — no defaulting of its own beyond the defensive empty→"info" guard
// for a malformed doc. The per-container → server-config → "info" resolution
// chain now lives daemon-side (see the runner test).
func TestBuildTerminalSpec_LogLevelVerbatim(t *testing.T) {
	cases := []struct {
		name      string
		tty       *v1beta1.ContainerTty
		wantLevel string
	}{
		{"nil Tty: defensive info", nil, "info"},
		{"empty LogLevel: defensive info", &v1beta1.ContainerTty{Prompt: "x> "}, "info"},
		{"debug verbatim", &v1beta1.ContainerTty{LogLevel: "debug"}, "debug"},
		{"warn verbatim", &v1beta1.ContainerTty{LogLevel: "warn"}, "warn"},
		{"error verbatim", &v1beta1.ContainerTty{LogLevel: "error"}, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh", Tty: tc.tty})
			if out.LogLevel != tc.wantLevel {
				t.Errorf("LogLevel = %q, want %q", out.LogLevel, tc.wantLevel)
			}
		})
	}
}

// TestBuildTerminalSpec_LogFileOverride locks #599's operator-override path:
// when a cell pins Tty.LogFile to a custom in-container location, the transform
// stamps it verbatim — no rewriting, no anchoring to the bind mount. Default
// (LogFile empty) resolves to the daemon-controlled default.
func TestBuildTerminalSpec_LogFileOverride(t *testing.T) {
	cases := []struct {
		name    string
		tty     *v1beta1.ContainerTty
		wantLog string
	}{
		{"no Tty: daemon default", nil, attachableKukettyLogPath},
		{"empty LogFile: daemon default", &v1beta1.ContainerTty{Prompt: "x> "}, attachableKukettyLogPath},
		{
			"override inside bind mount",
			&v1beta1.ContainerTty{LogFile: attachableTTYDir + "/custom.log"},
			attachableTTYDir + "/custom.log",
		},
		{
			"override outside bind mount",
			&v1beta1.ContainerTty{LogFile: "/var/log/sbsh-debug.log"},
			"/var/log/sbsh-debug.log",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh", KukeonGroupGID: 986, Tty: tc.tty})
			if out.LogFile != tc.wantLog {
				t.Errorf("LogFile = %q, want %q", out.LogFile, tc.wantLog)
			}
			if out.LogFile == "" {
				t.Errorf("LogFile is empty; always-on invariant from #599 broken")
			}
		})
	}
}

// TestBuildTerminalSpec_MetadataDirAnchored locks #672: metadata.json is always
// written into the per-container tty bind mount (attachableTTYDir), the dir the
// daemon owns and attachablePostCreateChown re-owns to the resolved container
// uid — never the legacy single-owner RunPath/terminals/<id>/ subtree whose
// 0700/creating-uid ownership denied the close-time write. The anchor must hold
// regardless of where the operator points Tty.LogFile: leaning on sbsh's
// implicit "MetadataDir = dirname(LogFile)" derivation would let an
// out-of-bind-mount log path (the "override outside bind mount" case) drag
// metadata.json out of the kukeon-owned, host-visible dir.
func TestBuildTerminalSpec_MetadataDirAnchored(t *testing.T) {
	cases := []struct {
		name string
		tty  *v1beta1.ContainerTty
	}{
		{"no Tty: daemon default log", nil},
		{"log inside bind mount", &v1beta1.ContainerTty{LogFile: attachableTTYDir + "/custom.log"}},
		{"log outside bind mount", &v1beta1.ContainerTty{LogFile: "/var/log/sbsh-debug.log"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh", KukeonGroupGID: 986, Tty: tc.tty})
			if out.MetadataDir != attachableTTYDir {
				t.Errorf("MetadataDir = %q, want %q (anchored in tty bind mount)", out.MetadataDir, attachableTTYDir)
			}
		})
	}
}

// ttyFieldsCoveredByMapping is the structural half of #493's "every Tty field
// must map" guard, ported to kuketty (which now owns the transform and reads
// the v1beta1 schema). It enumerates v1beta1.ContainerTty via reflection and
// fails when a declared field is missing from mapperedFields, or when
// mapperedFields carries a stale entry the struct no longer declares.
//
// New fields landing on v1beta1.ContainerTty must touch two places to keep the
// test green: add the name to mapperedFields here, and wire it through
// buildTerminalSpec with an assertion in TestBuildTerminalSpec_AllTtyFieldsMap.
func ttyFieldsCoveredByMapping(t *testing.T) {
	t.Helper()
	mapperedFields := map[string]struct{}{
		"LogFile":  {},
		"LogLevel": {},
		"OnInit":   {},
		"Prompt":   {},
	}
	typ := reflect.TypeOf(v1beta1.ContainerTty{})
	declared := make(map[string]struct{}, typ.NumField())
	for i := range typ.NumField() {
		name := typ.Field(i).Name
		declared[name] = struct{}{}
		if _, ok := mapperedFields[name]; !ok {
			t.Errorf(
				"v1beta1.ContainerTty.%s is declared on the schema but missing from "+
					"mapperedFields. Decide whether buildTerminalSpec should stamp it onto "+
					"the TerminalSpec; if yes, wire it through and extend "+
					"TestBuildTerminalSpec_AllTtyFieldsMap with an assertion; if no, document "+
					"the deliberate drop in a code comment and still add the field here.",
				name,
			)
		}
	}
	for name := range mapperedFields {
		if _, ok := declared[name]; !ok {
			t.Errorf(
				"mapperedFields references %q but v1beta1.ContainerTty no longer declares it — "+
					"drop the stale entry.",
				name,
			)
		}
	}
}

// TestBuildTerminalSpec_AllTtyFieldsMap is the comprehensive transform guard
// #493 calls for, ported to kuketty. It exercises reflective coverage, the
// per-field behavioral mapping, and the transform-default invariants the cell
// schema does not surface but the transform must stamp on every spec
// (EnvInherit, SocketFile, CaptureFile, RunPath).
func TestBuildTerminalSpec_AllTtyFieldsMap(t *testing.T) {
	ttyFieldsCoveredByMapping(t)

	const wantPrompt = `"\u@\h:\w\$ "`
	wantOnInit := []v1beta1.TtyStage{{Script: "echo hello"}, {Script: "echo world"}}
	const wantLogLevel = "debug"
	const wantLogFile = "/var/log/kuketty-override.log"
	workload := []string{"/bin/sh", "-c", "exec /workload"}

	spec := v1beta1.ContainerSpec{
		ID:             "c1",
		Command:        workload[0],
		Args:           workload[1:],
		Attachable:     true,
		KukeonGroupGID: 986,
		Tty: &v1beta1.ContainerTty{
			Prompt:   wantPrompt,
			OnInit:   wantOnInit,
			LogFile:  wantLogFile,
			LogLevel: wantLogLevel,
		},
	}

	out := buildSpec(t, spec)

	if out.Prompt != wantPrompt {
		t.Errorf("Tty.Prompt: Prompt = %q, want %q", out.Prompt, wantPrompt)
	}
	if !out.SetPrompt {
		t.Errorf("Tty.Prompt: SetPrompt = false, want true (non-empty inline prompt)")
	}
	if len(out.Stages.OnInit) != len(wantOnInit) {
		t.Fatalf("Tty.OnInit: Stages.OnInit len = %d, want %d", len(out.Stages.OnInit), len(wantOnInit))
	}
	for i, want := range wantOnInit {
		if out.Stages.OnInit[i].Script != want.Script {
			t.Errorf("Tty.OnInit[%d].Script = %q, want %q", i, out.Stages.OnInit[i].Script, want.Script)
		}
	}
	if len(out.Stages.PostAttach) != 0 {
		t.Errorf("Stages.PostAttach = %+v, want empty (not surfaced on cell schema)", out.Stages.PostAttach)
	}
	if out.LogLevel != wantLogLevel {
		t.Errorf("Tty.LogLevel: LogLevel = %q, want %q", out.LogLevel, wantLogLevel)
	}
	if !out.EnvInherit {
		t.Errorf("EnvInherit = false, want true — kuketty must forward its os.Environ() " +
			"(== OCI Process.Env) to the workload; without it sbsh strips all but HOME + SBSH_*. #494.")
	}
	if out.SocketFile != attachableSocketPath {
		t.Errorf("SocketFile = %q, want %q", out.SocketFile, attachableSocketPath)
	}
	if out.CaptureFile != attachableCapturePath {
		t.Errorf("CaptureFile = %q, want %q", out.CaptureFile, attachableCapturePath)
	}
	if out.LogFile != wantLogFile {
		t.Errorf("Tty.LogFile: LogFile = %q, want %q (override stamped verbatim)", out.LogFile, wantLogFile)
	}
	if out.RunPath != attachableTTYDir {
		t.Errorf("RunPath = %q, want %q", out.RunPath, attachableTTYDir)
	}
	if out.Command != workload[0] {
		t.Errorf("Command = %q, want %q", out.Command, workload[0])
	}
	if !reflect.DeepEqual(out.CommandArgs, workload[1:]) {
		t.Errorf("CommandArgs = %v, want %v", out.CommandArgs, workload[1:])
	}
}

// TestBuildTerminalSpec_PromptConfigured locks the inline-Prompt lane (#494):
// a non-empty Tty.Prompt stamps Spec.Prompt and flips SetPrompt on.
func TestBuildTerminalSpec_PromptConfigured(t *testing.T) {
	const wantPrompt = `"\[\e[1;36m\]claude \u@\h\[\e[0m\]:\w\$ "`
	out := buildSpec(t, v1beta1.ContainerSpec{
		ID:      "c1",
		Command: "/bin/sh",
		Tty:     &v1beta1.ContainerTty{Prompt: wantPrompt},
	})
	if out.Prompt != wantPrompt {
		t.Errorf("Prompt = %q, want %q", out.Prompt, wantPrompt)
	}
	if !out.SetPrompt {
		t.Errorf("SetPrompt = false, want true (non-empty inline Tty.Prompt flips it on)")
	}
}

// TestBuildTerminalSpec_OnInitConfigured locks the inline-OnInit lane (#494):
// each TtyStage.Script lands as an api.ExecStep in order.
func TestBuildTerminalSpec_OnInitConfigured(t *testing.T) {
	out := buildSpec(t, v1beta1.ContainerSpec{
		ID:      "c1",
		Command: "/bin/sh",
		Tty: &v1beta1.ContainerTty{
			OnInit: []v1beta1.TtyStage{{Script: "echo hello"}, {Script: "echo world"}},
		},
	})
	if len(out.Stages.OnInit) != 2 {
		t.Fatalf("Stages.OnInit len = %d, want 2", len(out.Stages.OnInit))
	}
	if out.Stages.OnInit[0].Script != "echo hello" || out.Stages.OnInit[1].Script != "echo world" {
		t.Errorf("Stages.OnInit = %+v, want [echo hello, echo world]", out.Stages.OnInit)
	}
	if len(out.Stages.PostAttach) != 0 {
		t.Errorf("Stages.PostAttach = %+v, want empty", out.Stages.PostAttach)
	}
}

// TestBuildTerminalSpec_EmptyTtyKeepsSafeDefault locks the safe-default branch
// for an unconfigured Tty (#494): no Prompt or OnInit means SetPrompt stays
// false (DisableSetPrompt(true) is forced) and Stages stays zero, so a
// non-shell workload never receives a literal PS1 injection on stdin.
func TestBuildTerminalSpec_EmptyTtyKeepsSafeDefault(t *testing.T) {
	out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1", Command: "/bin/sh"})
	if out.SetPrompt {
		t.Errorf("SetPrompt = true, want false (no inline Prompt → DisableSetPrompt(true))")
	}
	if out.Prompt != "" {
		t.Errorf("Prompt = %q, want empty", out.Prompt)
	}
	if len(out.Stages.OnInit) != 0 {
		t.Errorf("Stages.OnInit = %+v, want empty", out.Stages.OnInit)
	}
	if len(out.Stages.PostAttach) != 0 {
		t.Errorf("Stages.PostAttach = %+v, want empty", out.Stages.PostAttach)
	}
}

// TestBuildTerminalSpec_EmptyWorkloadFallsBackToBuilderDefault: when the doc
// carries no resolved argv (empty Command — image with no ENTRYPOINT/CMD and no
// override), the transform leaves Spec.Command at sbsh's hardcoded default
// (/bin/bash -i) rather than rendering an empty Command, which server.New
// rejects.
func TestBuildTerminalSpec_EmptyWorkloadFallsBackToBuilderDefault(t *testing.T) {
	out := buildSpec(t, v1beta1.ContainerSpec{ID: "c1"})
	if out.Command == "" {
		t.Errorf("Command is empty; expected sbsh builder's hardcoded fallback")
	}
}

// intPtr is a one-off helper for the always-on log table-test.
func intPtr(v int) *int { return &v }
