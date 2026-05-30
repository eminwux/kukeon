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

package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// TestServeChownsSocketToSocketGID guards the regression that motivated #173:
// a kukeond restart must self-apply the socket group/mode so a non-root
// operator does not lose access until kuke init is re-run.
func TestServeChownsSocketToSocketGID(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kukeond.sock")

	// Use the test process's own GID so the chown succeeds without root.
	gid := os.Getgid()

	srv := NewServer(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		SocketPath: socketPath,
		SocketMode: 0o660,
		SocketGID:  gid,
	})

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Stop")
		}
	})

	// Wait for the socket to exist.
	deadline := time.Now().Add(2 * time.Second)
	var info os.FileInfo
	for {
		var err error
		info, err = os.Stat(socketPath)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat socket: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not appear within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("syscall.Stat_t not available on this platform")
	}
	if int(stat.Gid) != gid {
		t.Errorf("socket gid: got %d want %d", stat.Gid, gid)
	}
	if got := info.Mode().Perm(); got != 0o660 {
		t.Errorf("socket mode: got %#o want 0o660", got)
	}
}

// TestServeWithoutSocketGIDLeavesGroupUnchanged confirms the chown step is
// gated on SocketGID > 0 — without a kukeon group provisioned, kukeond must
// fall back to root:root mode 0o600 instead of erroring out.
func TestServeWithoutSocketGIDLeavesGroupUnchanged(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kukeond.sock")

	srv := NewServer(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		SocketPath: socketPath,
		SocketMode: 0o600,
	})

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Stop")
		}
	})

	deadline := time.Now().Add(2 * time.Second)
	var info os.FileInfo
	for {
		var err error
		info, err = os.Stat(socketPath)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat socket: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not appear within deadline")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("socket mode: got %#o want 0o600", got)
	}
}

// TestServeReassertsForwardAdmissionOnStartup guards the reboot regression: a
// host reboot flushes the kernel's iptables, stripping the KUKEON-FORWARD
// admission chain that `kuke init` installed. The daemon, which restarts on
// its own, must re-assert that chain on Serve so kukeon-bridge cells regain
// egress without a re-run of init. A failure of that step is best-effort and
// must never block the daemon from serving.
func TestServeReassertsForwardAdmissionOnStartup(t *testing.T) {
	dir := t.TempDir()
	socketPath := filepath.Join(dir, "kukeond.sock")

	srv := NewServer(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), Options{
		SocketPath: socketPath,
		SocketMode: 0o600,
	})

	called := make(chan struct{}, 1)
	srv.forwardAdmissionFn = func() error {
		called <- struct{}{}
		// Best-effort contract: even when the re-assert fails, Serve must
		// still bring the socket up rather than aborting.
		return errors.New("iptables unavailable")
	}

	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Stop")
		}
	})

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not re-assert forward admission on startup")
	}

	// Despite the hook returning an error, the daemon must still come up.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket did not appear after forward-admission error (Serve aborted?)")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
