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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
	sbshserver "github.com/eminwux/sbsh/pkg/terminal/server"
)

// TestParseArgs_Default locks in the no-flags contract: with zero argv
// the resolver returns the bind-mount path the OCI injection delivers
// (`/.kukeon/kuketty/container.json`). This is the only invocation shape
// kukeond uses, so a regression in the default would silently break every
// attachable container start.
func TestParseArgs_Default(t *testing.T) {
	got, err := parseArgs(nil)
	if err != nil {
		t.Fatalf("parseArgs(nil): %v", err)
	}
	if got != defaultConfigPath {
		t.Errorf("config = %q, want %q", got, defaultConfigPath)
	}
}

// TestParseArgs_ConfigOverride exercises the only flag the wrapper exposes
// — the optional `--config` for test/debug ergonomics. The OCI injection
// path never sets it, but local smoke / e2e setups rely on it.
func TestParseArgs_ConfigOverride(t *testing.T) {
	want := "/tmp/kuketty-test.json"
	got, err := parseArgs([]string{"--config", want})
	if err != nil {
		t.Fatalf("parseArgs(--config %s): %v", want, err)
	}
	if got != want {
		t.Errorf("config = %q, want %q", got, want)
	}
}

// TestParseArgs_RejectsPositional locks the "no positional argv" rule.
// kuketty's earlier (phase 1) form took `-- <workload>`; phase 1b moves
// the workload into Spec.Command/CommandArgs, so any leftover positional
// argument from a stale OCI args wrap is a hard error rather than a
// silently-ignored leak.
func TestParseArgs_RejectsPositional(t *testing.T) {
	_, err := parseArgs([]string{"--", "/bin/sh", "-c", "echo hello"})
	if err == nil {
		t.Fatalf("parseArgs returned nil, want usage error for positional")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("parseArgs error = %T, want *usageError", err)
	}
}

// TestParseArgs_UnknownFlag confirms a stray flag (e.g., a leftover
// `--socket` from the sbsh wrapper) is rejected with a usageError. The
// builder API is the *only* runtime surface — any flag is a regression.
func TestParseArgs_UnknownFlag(t *testing.T) {
	_, err := parseArgs([]string{"--socket", "/run/kukeon/tty/terminals/main/control.sock"})
	if err == nil {
		t.Fatalf("parseArgs returned nil, want usage error for unknown flag")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("parseArgs error = %T, want *usageError", err)
	}
}

// TestLoadContainerDoc_Happy locks the on-disk schema the daemon's render
// path commits to after issue #641: a kukeon ContainerDoc with
// APIVersion=v1beta1 and Kind=Container. Round-trips a minimal doc and
// confirms the decoded spec values survive verbatim.
func TestLoadContainerDoc_Happy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	want := v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata:   v1beta1.ContainerMetadata{Name: "c1"},
		Spec: v1beta1.ContainerSpec{
			ID:      "c1",
			Command: "/bin/sh",
			Args:    []string{"-c", "echo hello"},
		},
	}
	writeDoc(t, path, want)

	got, err := loadContainerDoc(path)
	if err != nil {
		t.Fatalf("loadContainerDoc: %v", err)
	}
	if got.Spec.Command != want.Spec.Command {
		t.Errorf("Spec.Command = %q, want %q", got.Spec.Command, want.Spec.Command)
	}
	if len(got.Spec.Args) != len(want.Spec.Args) {
		t.Fatalf("Spec.Args = %v, want %v", got.Spec.Args, want.Spec.Args)
	}
	if got.Metadata.Name != want.Metadata.Name {
		t.Errorf("Metadata.Name = %q, want %q", got.Metadata.Name, want.Metadata.Name)
	}
}

// TestLoadContainerDoc_WrongAPIVersion locks the schema discriminator: a
// document tagged with the wrong apiVersion (e.g., a stale kukeon-side
// schema rendered by a daemon that hasn't been re-built) must fail loudly
// rather than being silently interpreted.
func TestLoadContainerDoc_WrongAPIVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	doc := v1beta1.ContainerDoc{
		APIVersion: "kuketty.kukeon.io/v1alpha1",
		Kind:       v1beta1.KindContainer,
		Spec:       v1beta1.ContainerSpec{ID: "c1"},
	}
	writeDoc(t, path, doc)
	_, err := loadContainerDoc(path)
	if err == nil {
		t.Fatalf("loadContainerDoc returned nil, want apiVersion error")
	}
}

// TestLoadContainerDoc_WrongKind catches the case where the apiVersion is
// correct (v1beta1) but the kind is e.g. Cell — kuketty must refuse rather
// than apply Container semantics to a different-shaped doc.
func TestLoadContainerDoc_WrongKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	doc := v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Spec:       v1beta1.ContainerSpec{ID: "c1"},
	}
	writeDoc(t, path, doc)
	_, err := loadContainerDoc(path)
	if err == nil {
		t.Fatalf("loadContainerDoc returned nil, want kind error")
	}
}

// TestLoadContainerDoc_Malformed locks the json-parse error path so a
// half-written file (crashed renderer) produces a clean failure rather
// than a silently-zero ContainerDoc.
func TestLoadContainerDoc_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadContainerDoc(path)
	if err == nil {
		t.Fatalf("loadContainerDoc returned nil, want parse error")
	}
}

// TestClaimSocketListener_BindsAndUnlinksStale exercises the
// unlink-then-listen sequence: a stale file (from a prior crash on the
// same in-container path) must be removed before Listen so the wrapper
// does not hit EADDRINUSE on every restart.
func TestClaimSocketListener_BindsAndUnlinksStale(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	// Pre-create a stale plain file at the path; ensures the helper
	// unlinks it before Listen rather than failing with EADDRINUSE.
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatalf("pre-create stale: %v", err)
	}
	l, err := claimSocketListener(context.Background(), sock, 0, nil)
	if err != nil {
		t.Fatalf("claimSocketListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("path %s is not a socket: mode=%v", sock, info.Mode())
	}
}

// TestClaimSocketListener_AppliesModeAndGID locks the bug fix for issue
// #916: the on-disk socket mode and group must reflect the configured
// SocketMode + SocketGID immediately after the helper returns —
// *before* any sbsh involvement. Pre-fix, the inode was born at
// 0o777 & ~umask (typically 0o755 under the container's 0o022 umask)
// and stayed there for the duration of processRepos + processStages,
// reopening the EACCES window for any group-member client that dialed
// after the daemon reported "containers: started" but before
// sbshserver.Serve ran applySocketPerms. The fix binds under a
// temporary umask matching the spec mode and follows with
// belt-and-braces chmod + chown so the socket is dial-ready by
// matching peers the instant Listen returns.
//
// The test runs against the process's own primary GID so it does not
// need elevated privileges. The umask path normally gets us there;
// the explicit chmod is the belt that backs it up.
func TestClaimSocketListener_AppliesModeAndGID(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	gid := os.Getgid()
	mode := os.FileMode(0o660)

	l, err := claimSocketListener(context.Background(), sock, mode, &gid)
	if err != nil {
		t.Fatalf("claimSocketListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("path %s is not a socket: mode=%v", sock, info.Mode())
	}
	if got := info.Mode().Perm(); got != mode {
		t.Errorf("socket mode = %#o, want %#o", got, mode)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("stat.Sys() not *syscall.Stat_t: %T", info.Sys())
	}
	if int(stat.Gid) != gid {
		t.Errorf("socket gid = %d, want %d", stat.Gid, gid)
	}
}

// TestClaimSocketListener_NilGIDLeavesGroupUnchanged exercises the
// no-kukeon-group path: when SocketGID is nil (the cell has no
// kukeon group configured, mirroring buildTerminalSpec's gate on a
// non-zero GID), the helper must not chown. mode still applies — the
// owner-only 0o600 default lands when the caller passes a zero mode.
func TestClaimSocketListener_NilGIDLeavesGroupUnchanged(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	l, err := claimSocketListener(context.Background(), sock, 0, nil)
	if err != nil {
		t.Fatalf("claimSocketListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("socket mode = %#o, want 0o600 (defaultSocketMode)", got)
	}
}

func TestIsCleanShutdown(t *testing.T) {
	if !isCleanShutdown(nil) {
		t.Errorf("nil err: not clean")
	}
	if !isCleanShutdown(context.Canceled) {
		t.Errorf("context.Canceled: not clean")
	}
	if isCleanShutdown(errors.New("something went wrong")) {
		t.Errorf("opaque err: reported clean")
	}
}

// TestWorkloadExitCode pins the mapping from sbsh's typed workload exit
// (*sbshserver.ExitError, issue #1721) to kuketty's exit code. No error text
// is inspected: an error that merely reads like sbsh's old exit carriers is
// not a workload exit and maps to exitCodeInternal (ok=false).
//
// The #1273 cases: a clean exit is code 0 and a non-zero exit propagates its
// code. The #1282 case: a raw PTY-read EIO is not a workload exit (sbsh
// resolves the EIO/exit race itself, eminwux/sbsh#439).
func TestWorkloadExitCode(t *testing.T) {
	zero, three := 0, 3
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantOK   bool
	}{
		{name: "nil is clean", err: nil, wantCode: 0, wantOK: true},
		{name: "clean exit", err: &sbshserver.ExitError{Code: &zero}, wantCode: 0, wantOK: true},
		{name: "non-zero exit", err: &sbshserver.ExitError{Code: &three}, wantCode: 3, wantOK: true},
		{
			name:     "wrapped exit",
			err:      fmt.Errorf("server.Serve: %w", &sbshserver.ExitError{Code: &three}),
			wantCode: 3,
			wantOK:   true,
		},
		{name: "killed by SIGKILL", err: &sbshserver.ExitError{Signal: "SIGKILL"}, wantCode: 137, wantOK: true},
		{name: "killed by SIGTERM", err: &sbshserver.ExitError{Signal: "SIGTERM"}, wantCode: 143, wantOK: true},
		{name: "unknown signal name", err: &sbshserver.ExitError{Signal: "SIGBOGUS"}, wantCode: 128, wantOK: true},
		{name: "exit with neither code nor signal", err: &sbshserver.ExitError{}, wantCode: 0, wantOK: false},
		{
			name:     "old exit text is not a workload exit",
			err:      errors.New("shell process exited: code=3"),
			wantCode: 0,
			wantOK:   false,
		},
		{
			name:     "internal failure is not a workload exit",
			err:      errors.New("server.New: bind failed"),
			wantCode: 0,
			wantOK:   false,
		},
		{
			name: "raw pty-read EIO is an internal failure",
			err: fmt.Errorf(
				"terminalManagerReader pty read error: could not read pipe->pipe: %w",
				errors.New("read /dev/ptmx: input/output error"),
			),
			wantCode: 0,
			wantOK:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, ok := workloadExitCode(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if code != tc.wantCode {
				t.Errorf("code = %d, want %d", code, tc.wantCode)
			}
		})
	}
}

func writeDoc(t *testing.T, path string, doc v1beta1.ContainerDoc) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestDefaultConfigPath pins kuketty's input mount point (issue #1721) to the
// path the daemon bind-mounts (ctr.AttachableMetadataPath).
func TestDefaultConfigPath(t *testing.T) {
	if defaultConfigPath != "/.kukeon/kuketty/container.json" {
		t.Errorf("defaultConfigPath = %q", defaultConfigPath)
	}
}

// TestRefuseLegacyLayout covers a container created before issue #1721: its
// ContainerDoc is mounted at the old path, so kuketty reports that it must be
// recreated instead of failing to read container.json.
func TestRefuseLegacyLayout(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "container.json")
	legacy := filepath.Join(dir, "metadata.json")

	if err := refuseLegacyLayout(current, legacy); err != nil {
		t.Fatalf("neither file: %v, want nil (loadContainerDoc reports the missing file)", err)
	}
	if err := os.WriteFile(legacy, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := refuseLegacyLayout(current, legacy)
	if !errors.Is(err, errLegacyLayout) || !strings.Contains(err.Error(), "recreate") {
		t.Fatalf("legacy only: %v, want errLegacyLayout", err)
	}
	if exitCode(err, io.Discard) != exitCodeInternal {
		t.Errorf("legacy layout exit code = %d, want %d", exitCode(err, io.Discard), exitCodeInternal)
	}
	if err = os.WriteFile(current, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = refuseLegacyLayout(current, legacy); err != nil {
		t.Fatalf("both files: %v, want nil", err)
	}
}

// TestEnsureTerminalDir pins the guard that creates tty/terminals/main/ when
// the daemon did not (issue #1721), and that it is a no-op when it did.
func TestEnsureTerminalDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "terminals", "main")
	for range 2 {
		if err := ensureTerminalDir(dir); err != nil {
			t.Fatalf("ensureTerminalDir: %v", err)
		}
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Fatalf("stat %s: %v", dir, err)
		}
	}
}

// startServeTerminal runs serveTerminal on a temp layout with the given shell
// script as the workload, against a real sbsh server, and returns the
// reporter, the document path and serveTerminal's result channel.
func startServeTerminal(
	ctx context.Context, t *testing.T, script string,
) (*supervisorReporter, string, <-chan error) {
	t.Helper()
	tmp := t.TempDir()
	terminalDir := filepath.Join(tmp, "terminals", "main")
	if err := os.MkdirAll(terminalDir, 0o700); err != nil {
		t.Fatal(err)
	}
	spec := &sbshapi.TerminalSpec{
		ID:            sbshapi.ID("kuketty-" + filepath.Base(tmp)),
		Name:          "kuketty-test",
		Labels:        map[string]string{},
		Command:       "/bin/sh",
		CommandArgs:   []string{"-c", script},
		EnvInherit:    true,
		RunPath:       tmp,
		MetadataDir:   terminalDir,
		MetadataFile:  "terminal.json",
		SocketFile:    filepath.Join(terminalDir, "control.sock"),
		CaptureFile:   filepath.Join(terminalDir, "capture.raw"),
		ShutdownGrace: 500 * time.Millisecond,
	}
	listener, err := claimSocketListener(ctx, spec.SocketFile, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, path := newTestReporter(t)
	done := make(chan error, 1)
	go func() { done <- serveTerminal(ctx, spec, listener, discardLogger(), r) }()
	return r, path, done
}

// waitServeTerminal waits for serveTerminal to return.
func waitServeTerminal(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		t.Fatal("serveTerminal did not return")
		return nil
	}
}

// TestServeTerminal_WorkloadExit drives a real sbsh terminal whose workload
// exits 3 or is killed, and checks kuketty's result and the supervisor
// document's terminal exit come from sbsh's typed exit and status (#1721).
func TestServeTerminal_WorkloadExit(t *testing.T) {
	cases := []struct {
		name       string
		script     string
		wantCode   int
		wantSignal string
	}{
		{name: "exit 0", script: "sleep 0.2; exit 0", wantCode: 0},
		{name: "exit 3", script: "sleep 0.2; exit 3", wantCode: 3},
		{name: "killed by SIGKILL", script: "sleep 0.2; kill -KILL $$", wantCode: 137, wantSignal: "SIGKILL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, path, done := startServeTerminal(ctx, t, tc.script)
			err := waitServeTerminal(t, done)
			if code := exitCode(err, io.Discard); code != tc.wantCode {
				t.Errorf("exit code = %d (err %v), want %d", code, err, tc.wantCode)
			}
			doc := readDoc(t, path)
			if doc.Status.Phase != v1beta1.SupervisorPhaseExited {
				t.Errorf("phase = %q, want Exited", doc.Status.Phase)
			}
			term := doc.Status.Terminals[0]
			if term.State != sbshapi.Exited.String() {
				t.Errorf("terminal state = %q, want Exited", term.State)
			}
			if term.Exit == nil || term.Exit.Code != tc.wantCode || term.Exit.Signal != tc.wantSignal ||
				term.Exit.At.IsZero() {
				t.Errorf("terminal exit = %+v, want code %d signal %q with a time",
					term.Exit, tc.wantCode, tc.wantSignal)
			}
		})
	}
}

// TestServeTerminal_ObserverReportsReady checks that the running terminal's
// status reaches the supervisor document through sbsh's observer (kuketty no
// longer polls), and that an operator shutdown is a clean exit.
func TestServeTerminal_ObserverReportsReady(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	r, path, done := startServeTerminal(ctx, t, "sleep 30")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if terms := r.snapshot().Status.Terminals; len(terms) == 1 && terms[0].State == sbshapi.Ready.String() {
			if terms[0].PID <= 0 {
				t.Errorf("ready terminal has pid %d", terms[0].PID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal did not reach Ready: %+v", r.snapshot().Status.Terminals)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := waitServeTerminal(t, done); err != nil {
		t.Errorf("serveTerminal after cancel = %v, want nil", err)
	}
	if doc := readDoc(t, path); doc.Status.Phase != v1beta1.SupervisorPhaseExited ||
		doc.Status.Terminals[0].State != sbshapi.Exited.String() {
		t.Errorf("after shutdown: phase %q, terminal %+v", doc.Status.Phase, doc.Status.Terminals[0])
	}
}
