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
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestParseArgs_Default locks in the no-flags contract: with zero argv
// the resolver returns the bind-mount path the OCI injection delivers
// (`/.kukeon/kuketty/metadata.json`). This is the only invocation shape
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
	_, err := parseArgs([]string{"--socket", "/run/kukeon/tty/socket"})
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

// TestWorkloadExitCode covers the carriers sbsh v0.13.0 embeds the workload
// child's exit code in (issue #1273): the *exec.ExitError on the non-init
// cmd.Wait path, the "(code 0)" literal on a non-init clean exit, the
// init-mode "code=N" string from the PID-1 reaper, and the abnormal
// no-recoverable-code teardown paths. A genuine kuketty-internal failure must
// report ok=false so it maps to exitCodeInternal.
func TestWorkloadExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode int
		wantOK   bool
	}{
		{name: "nil is clean", err: nil, wantCode: 0, wantOK: true},
		{
			name:     "non-init clean exit literal",
			err:      errors.New("shell process exited (code 0)"),
			wantCode: 0,
			wantOK:   true,
		},
		{
			name:     "init-mode clean exit",
			err:      errors.New("shell process exited: code=0"),
			wantCode: 0,
			wantOK:   true,
		},
		{
			name:     "init-mode non-zero exit",
			err:      errors.New("shell process exited: code=7"),
			wantCode: 7,
			wantOK:   true,
		},
		{
			name:     "init-mode wraps via fmt.Errorf",
			err:      fmt.Errorf("server.Serve: %w", errors.New("shell process exited: code=137")),
			wantCode: 137,
			wantOK:   true,
		},
		{
			name:     "no recoverable code is treated clean",
			err:      errors.New("shell process exited"),
			wantCode: 0,
			wantOK:   true,
		},
		{
			name:     "close-cancelled tracked-exit wait is treated clean",
			err:      errors.New("shell process exited (close cancelled tracked-exit wait)"),
			wantCode: 0,
			wantOK:   true,
		},
		{
			name:     "internal failure is not a workload exit",
			err:      errors.New("server.New: bind failed"),
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

// TestWorkloadExitCode_ExecExitError pins the non-init cmd.Wait carrier: sbsh
// wraps the *exec.ExitError from cmd.Wait with %w, so errors.As must recover
// the child's real exit code (here a non-zero status).
func TestWorkloadExitCode_ExecExitError(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	runErr := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %v", runErr)
	}
	wrapped := fmt.Errorf("shell process exited: %w", exitErr)
	code, ok := workloadExitCode(wrapped)
	if !ok {
		t.Fatalf("ok = false, want true")
	}
	if code != 3 {
		t.Errorf("code = %d, want 3", code)
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
