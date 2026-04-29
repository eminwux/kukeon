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

package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStopDaemonByPIDFile_EmptyPath is the trivial case the Uninstall caller
// can land in if SocketDir is empty: the stopper must short-circuit with no
// error and an empty report rather than try to read "".
func TestStopDaemonByPIDFile_EmptyPath(t *testing.T) {
	report, err := stopDaemonByPIDFile(context.Background(), "", time.Second)
	if err != nil {
		t.Fatalf("expected nil error for empty pidFile, got %v", err)
	}
	if report.PIDFilePresent {
		t.Errorf("report.PIDFilePresent=true with empty pidFile; got %+v", report)
	}
	if report.Signalled || report.ForceKilled {
		t.Errorf("nothing should have been signalled with empty pidFile; got %+v", report)
	}
}

// TestStopDaemonByPIDFile_MissingFile covers the partial-uninstall path
// from #193: pidFile is configured but the file is gone. ENOENT is "no live
// daemon to stop" — must be a clean no-op.
func TestStopDaemonByPIDFile_MissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "kukeond.pid")
	report, err := stopDaemonByPIDFile(context.Background(), missing, time.Second)
	if err != nil {
		t.Fatalf("expected nil error when pid file is absent, got %v", err)
	}
	if report.PIDFilePresent {
		t.Errorf("report.PIDFilePresent=true for absent file; got %+v", report)
	}
	if report.PIDFile != missing {
		t.Errorf("report.PIDFile=%q, want %q", report.PIDFile, missing)
	}
}

// TestStopDaemonByPIDFile_GarbagePID covers operator-corrupted state: a PID
// file that contains something the daemon would never write. The function
// must not error (the rest of uninstall has to keep going) and must not have
// signalled anything.
func TestStopDaemonByPIDFile_GarbagePID(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "kukeond.pid")
	if err := os.WriteFile(pidFile, []byte("not-a-number\n"), 0o644); err != nil {
		t.Fatalf("seed pid file: %v", err)
	}
	report, err := stopDaemonByPIDFile(context.Background(), pidFile, time.Second)
	if err != nil {
		t.Fatalf("expected nil error for garbage PID, got %v", err)
	}
	if !report.PIDFilePresent {
		t.Errorf("report.PIDFilePresent=false; want true (file existed); got %+v", report)
	}
	if report.PID != 0 {
		t.Errorf("report.PID=%d, want 0 (parse failed)", report.PID)
	}
	if report.Signalled {
		t.Errorf("report.Signalled=true for garbage PID; got %+v", report)
	}
}

// TestStopDaemonByPIDFile_RefusesPID1 is the safety guard: even if a corrupted
// pid file said "1", the stopper must refuse to signal init. This is the
// "PID <= 1" branch — treated as garbage just like "not-a-number".
func TestStopDaemonByPIDFile_RefusesPID1(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "kukeond.pid")
	if err := os.WriteFile(pidFile, []byte("1\n"), 0o644); err != nil {
		t.Fatalf("seed pid file: %v", err)
	}
	report, err := stopDaemonByPIDFile(context.Background(), pidFile, time.Second)
	if err != nil {
		t.Fatalf("expected nil error for PID 1, got %v", err)
	}
	if report.Signalled {
		t.Errorf("report.Signalled=true for PID 1; refusal to signal init was bypassed: %+v", report)
	}
}
