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

package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ptySession runs a binary attached to a freshly allocated pseudoterminal.
// Stdin/stdout/stderr of the child are wired to the PTY slave; the test
// drives the child by calling Write (master side) and reads captured
// output via the Wait return value or Output. Exists for end-to-end
// scenarios that need to drive interactive subcommands like `kuke attach`,
// where CombinedOutput-style helpers cannot deliver detach keystrokes.
type ptySession struct {
	t        *testing.T
	cmd      *exec.Cmd
	pty      *os.File
	waitDone chan error

	mu  sync.Mutex
	out bytes.Buffer
}

// startPTY launches a binary inside a fresh PTY and returns a session handle.
// If the binary is missing, the test is skipped to match the convention used
// by runReturningBinary. The caller MUST eventually call Wait or Close to
// reap the child and release the PTY.
func startPTY(t *testing.T, env []string, command string, args ...string) *ptySession {
	t.Helper()

	dir := os.Getenv("E2E_BIN_DIR")
	if dir == "" {
		dir = ".."
	}
	bin := filepath.Join(dir, command)
	if _, err := os.Stat(bin); os.IsNotExist(err) {
		t.Skipf("binary %s not found, skipping", bin)
	}

	cmd := exec.Command(bin, args...)
	if env != nil {
		cmd.Env = env
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start %s: %v", bin, err)
	}

	s := &ptySession{
		t:        t,
		cmd:      cmd,
		pty:      ptmx,
		waitDone: make(chan error, 1),
	}

	// Drain master into the buffer so the child does not block on a full
	// pipe. Returns when the slave side is closed (child has exited or we
	// closed the PTY ourselves).
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				s.mu.Lock()
				s.out.Write(buf[:n])
				s.mu.Unlock()
			}
			if readErr != nil {
				return
			}
		}
	}()

	go func() {
		s.waitDone <- cmd.Wait()
	}()

	return s
}

// Write sends raw bytes to the PTY master so they arrive on the child's
// stdin as if typed by a user.
func (s *ptySession) Write(p []byte) error {
	s.t.Helper()
	_, err := s.pty.Write(p)
	return err
}

// Wait blocks until the child exits or the timeout expires. Returns the
// exit code (0 on success, -1 if the timeout fires or the child died of
// a signal), the captured output up to that point, and any wait error.
func (s *ptySession) Wait(timeout time.Duration) (int, []byte, error) {
	s.t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case waitErr := <-s.waitDone:
		_ = s.pty.Close()
		exitCode := 0
		if waitErr != nil {
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		return exitCode, s.snapshotOutput(), waitErr
	case <-timer.C:
		return -1, s.snapshotOutput(), fmt.Errorf("pty session %s did not exit within %s",
			filepath.Base(s.cmd.Path), timeout)
	}
}

// Close kills the child if it has not exited and releases the PTY. Safe to
// call after Wait. The drain goroutine returns shortly afterwards.
func (s *ptySession) Close() {
	s.t.Helper()
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
	_ = s.pty.Close()
}

// snapshotOutput returns a copy of the output captured so far.
func (s *ptySession) snapshotOutput() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.out.Bytes()...)
}
