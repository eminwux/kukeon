// Copyright 2026 Emiliano Spinella (eminwux)
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

package gitremote

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteURLForms(t *testing.T) {
	for _, raw := range []string{"git@github.com:eminwux/kukeon.git", "git@work-alias:owner/repo", "work-alias:owner/repo", "ssh://git@work-alias:2222/owner/repo", "https://example.test/owner/repo", "github.com/owner/repo"} {
		got, err := URL(raw)
		if err != nil || (got != raw && got != "https://"+raw) {
			t.Fatalf("valid URL rejected: %v", err)
		}
	}
	for _, raw := range []string{"git@github.com/owner/repo", "user:canary@host/repo", "user:canary@host:repo", "ssh://git:canary@host/repo", "https://canary@host/repo", "https://user:canary@host/repo", "https://host/repo?access_token=canary", "https://host/repo#canary", "git@host:repo?token=canary", "ftp://host/repo", "file:///tmp/repo", "/tmp/repo", "--upload-pack=canary", "ssh://host:bad/repo", "https://host/", "ssh://host/repo\ncanary"} {
		_, err := URL(raw)
		if err == nil || strings.Contains(err.Error(), "canary") {
			t.Fatal("invalid or credential URL not safely rejected")
		}
	}
}

func TestDefaultBranchRejectsBeforeSubprocess(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	if _, err := DefaultBranch(t.Context(), "https://user:canary@host/repo"); err == nil {
		t.Fatal("accepted credentials")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git invoked before URL rejection")
	}
}

func TestGitCancellationDoesNotWaitForTransport(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	t.Setenv("GIT_SSH_COMMAND", "ssh")
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, "ls-remote", "--symref", "--", "https://example.test/repo", "HEAD")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Fatal("Git/transport ignored cancellation")
	}
}
