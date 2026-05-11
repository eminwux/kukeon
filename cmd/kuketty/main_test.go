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
	"os"
	"path/filepath"
	"testing"

	sbshapi "github.com/eminwux/sbsh/pkg/api"
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

// TestLoadTerminalDoc_Happy locks the on-disk schema kukeon's render path
// commits to: an api.TerminalDoc with APIVersion=sbsh/v1beta1 and
// Kind=Terminal. Round-trips a minimal doc and confirms the decoded
// spec values survive verbatim.
func TestLoadTerminalDoc_Happy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	want := sbshapi.TerminalDoc{
		APIVersion: sbshapi.APIVersionV1Beta1,
		Kind:       sbshapi.KindTerminal,
		Metadata:   sbshapi.TerminalMetadata{Name: "c1"},
		Spec: sbshapi.TerminalSpec{
			Command:     "/bin/sh",
			CommandArgs: []string{"-c", "echo hello"},
			SocketFile:  "/run/kukeon/tty/socket",
			RunPath:     "/run/kukeon/tty",
		},
	}
	writeDoc(t, path, want)

	got, err := loadTerminalDoc(path)
	if err != nil {
		t.Fatalf("loadTerminalDoc: %v", err)
	}
	if got.Spec.Command != want.Spec.Command {
		t.Errorf("Spec.Command = %q, want %q", got.Spec.Command, want.Spec.Command)
	}
	if got.Spec.SocketFile != want.Spec.SocketFile {
		t.Errorf("Spec.SocketFile = %q, want %q", got.Spec.SocketFile, want.Spec.SocketFile)
	}
	if got.Metadata.Name != want.Metadata.Name {
		t.Errorf("Metadata.Name = %q, want %q", got.Metadata.Name, want.Metadata.Name)
	}
}

// TestLoadTerminalDoc_WrongAPIVersion locks the schema discriminator: a
// document tagged with the wrong apiVersion (e.g., a stale kukeon-side
// schema rendered by a daemon that hasn't been re-built) must fail loudly
// rather than being silently interpreted as a Terminal.
func TestLoadTerminalDoc_WrongAPIVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	doc := sbshapi.TerminalDoc{
		APIVersion: "kuketty.kukeon.io/v1alpha1",
		Kind:       sbshapi.KindTerminal,
		Spec:       sbshapi.TerminalSpec{SocketFile: "/run/kukeon/tty/socket"},
	}
	writeDoc(t, path, doc)
	_, err := loadTerminalDoc(path)
	if err == nil {
		t.Fatalf("loadTerminalDoc returned nil, want apiVersion error")
	}
}

// TestLoadTerminalDoc_WrongKind catches the case where the apiVersion is
// correct (sbsh/v1beta1) but the kind is e.g. TerminalProfile — kuketty
// must refuse rather than apply profile semantics to a Terminal-shaped
// runner.
func TestLoadTerminalDoc_WrongKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	doc := sbshapi.TerminalDoc{
		APIVersion: sbshapi.APIVersionV1Beta1,
		Kind:       sbshapi.KindTerminalProfile,
		Spec:       sbshapi.TerminalSpec{SocketFile: "/run/kukeon/tty/socket"},
	}
	writeDoc(t, path, doc)
	_, err := loadTerminalDoc(path)
	if err == nil {
		t.Fatalf("loadTerminalDoc returned nil, want kind error")
	}
}

// TestLoadTerminalDoc_MissingSocket rejects a doc whose spec carries no
// SocketFile — without it kuketty has nothing to bind, and a default
// fallback would silently bind a path the host has no bind-mount for.
func TestLoadTerminalDoc_MissingSocket(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	doc := sbshapi.TerminalDoc{
		APIVersion: sbshapi.APIVersionV1Beta1,
		Kind:       sbshapi.KindTerminal,
	}
	writeDoc(t, path, doc)
	_, err := loadTerminalDoc(path)
	if err == nil {
		t.Fatalf("loadTerminalDoc returned nil, want spec.socketIO error")
	}
}

// TestLoadTerminalDoc_Malformed locks the json-parse error path so a
// half-written file (crashed renderer) produces a clean failure rather
// than a silently-zero TerminalDoc.
func TestLoadTerminalDoc_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := loadTerminalDoc(path)
	if err == nil {
		t.Fatalf("loadTerminalDoc returned nil, want parse error")
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
	l, err := claimSocketListener(sock)
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

// TestApplySocketPerms_ChmodsAndChowns covers the SCM-perm dance kuketty
// has to run after claimSocketListener. sbsh's runner does this in
// OpenSocketCtrl on the bind path it owns, but UseListener (the public
// server facade's escape hatch) bypasses it — so kuketty must replicate
// the chmod and (when set) chown. A regression here surfaces as a socket
// the host-side kuke attach (running under the kukeon group) can no
// longer connect to.
func TestApplySocketPerms_ChmodsAndChowns(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	l, err := claimSocketListener(sock)
	if err != nil {
		t.Fatalf("claimSocketListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	if err := applySocketPerms(sock, 0o660, nil); err != nil {
		t.Fatalf("applySocketPerms: %v", err)
	}
	info, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o660 {
		t.Errorf("socket perm = %#o, want 0o660", perm)
	}
}

// TestApplySocketPerms_ZeroModeNoChange covers the "fall through to the
// runner default" semantics for unset SocketMode: no chmod call, so the
// inode's umask-clipped permissions stand. Mirrors sbsh's own default
// when Spec.SocketMode is the zero value.
func TestApplySocketPerms_ZeroModeNoChange(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "socket")
	l, err := claimSocketListener(sock)
	if err != nil {
		t.Fatalf("claimSocketListener: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	before, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}
	if err := applySocketPerms(sock, 0, nil); err != nil {
		t.Fatalf("applySocketPerms: %v", err)
	}
	after, err := os.Stat(sock)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}
	if before.Mode().Perm() != after.Mode().Perm() {
		t.Errorf("perm changed from %#o to %#o on zero mode",
			before.Mode().Perm(), after.Mode().Perm())
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

func writeDoc(t *testing.T, path string, doc sbshapi.TerminalDoc) {
	t.Helper()
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
