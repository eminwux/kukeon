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

// attach-smoke drives an interactive subprocess attached to a freshly
// allocated pseudoterminal, then sends sbsh's Ctrl+] Ctrl+] detach
// sequence and waits for the child to exit. It exists so scripts/dev-init.sh
// can smoke-test `kuke attach` without an expect(1) prereq; the helper
// reuses github.com/creack/pty (already a direct dependency).
//
// Behaviour mirrors the prior expect block:
//   - allocate a PTY and spawn the child connected to it
//   - sleep --connect-grace (default 2s) so the pkg/attach raw-mode
//     keyboard filter wires up before keystrokes arrive
//   - write 0x1d 0x1d (Ctrl+] Ctrl+]) to the PTY master
//   - wait up to --exit-timeout (default 20s) for the child to exit
//   - exit non-zero on timeout or non-zero child exit; the child's
//     transcript is always captured to --log
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"

	"github.com/creack/pty"
)

// detachSequence is the keystroke pair the sbsh client filter recognises
// as a request to detach (Ctrl+] Ctrl+], adjacent). It matches the
// constant the e2e suite uses; see e2e/e2e_kuke_attach_test.go.
var detachSequence = []byte{0x1d, 0x1d}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var (
		logPath      string
		connectGrace time.Duration
		exitTimeout  time.Duration
	)
	flag.StringVar(&logPath, "log", "", "path to write the child's combined output (required)")
	flag.DurationVar(&connectGrace, "connect-grace", 2*time.Second,
		"delay after spawn before sending the detach sequence")
	flag.DurationVar(&exitTimeout, "exit-timeout", 20*time.Second,
		"overall deadline for the child to exit after spawn")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: %s --log PATH [--connect-grace D] [--exit-timeout D] -- CMD [ARGS...]\n",
			os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if logPath == "" {
		flag.Usage()
		return errors.New("--log is required")
	}
	if flag.NArg() == 0 {
		flag.Usage()
		return errors.New("missing CMD to spawn under the PTY")
	}

	logFile, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("open log %s: %w", logPath, err)
	}
	defer logFile.Close()

	args := flag.Args()
	cmd := exec.Command(args[0], args[1:]...)

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return fmt.Errorf("pty.Start %s: %w", args[0], err)
	}
	defer ptmx.Close()

	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(logFile, ptmx)
		close(copyDone)
	}()

	waitDone := make(chan error, 1)
	go func() { waitDone <- cmd.Wait() }()

	var detach <-chan time.Time = time.After(connectGrace)
	overall := time.After(exitTimeout)

	for {
		select {
		case <-detach:
			detach = nil
			if _, werr := ptmx.Write(detachSequence); werr != nil {
				fmt.Fprintf(os.Stderr,
					"warning: write detach sequence: %v (child may have already exited)\n",
					werr)
			}
		case waitErr := <-waitDone:
			_ = ptmx.Close()
			<-copyDone
			if waitErr != nil {
				var exitErr *exec.ExitError
				if errors.As(waitErr, &exitErr) {
					return fmt.Errorf("child exited with code %d (transcript: %s)",
						exitErr.ExitCode(), logPath)
				}
				return fmt.Errorf("child wait: %w", waitErr)
			}
			return nil
		case <-overall:
			_ = cmd.Process.Kill()
			<-waitDone
			_ = ptmx.Close()
			<-copyDone
			return fmt.Errorf("child did not exit within %s (transcript: %s)",
				exitTimeout, logPath)
		}
	}
}
