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
