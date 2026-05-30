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

// kukepause is the minimal PID 1 for every kukeon cell's root (pause)
// container, replacing the previous `sleep infinity` from busybox (issue #931).
//
// `sleep` makes a poor PID 1 for two reasons: it installs no SIGTERM handler,
// so the kernel's default-ignore-fatal-signals rule for PID 1 means a SIGTERM
// sent during cell deletion is dropped and `StopContainer` burns its full
// 10-second graceful-shutdown timeout before escalating to SIGKILL; and it does
// not reap orphaned children, so any process re-parented to the cell's PID 1
// lingers as a zombie.
//
// kukepause fixes both: it installs SIGTERM/SIGINT handlers that exit 0 (so cell
// teardown completes in well under a second) and a SIGCHLD reaper that harvests
// every re-parented child via wait4(2), then blocks on pause(2) so it consumes
// no CPU while idle.
//
// It is built CGO_ENABLED=0 (no libc dependency) and bind-mounted into each root
// container at /pause; it cannot be staged from the kukeond image the way
// kuketty is, because root containers — including kukeond's own — must exist
// before kukeond is running, so `kuke init` pre-stages it on the host instead.
package main

import (
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// SIGTERM/SIGINT → graceful exit 0. Buffer size covers a burst of both
	// signals arriving before the loop drains them; we exit on the first.
	term := make(chan os.Signal, 2)
	signal.Notify(term, syscall.SIGTERM, syscall.SIGINT)

	// SIGCHLD → reap. Buffered so a coalesced delivery (the kernel collapses
	// multiple pending SIGCHLDs into one) still wakes the reaper; reapZombies
	// drains every exited child per wake, so a single slot is sufficient.
	chld := make(chan os.Signal, 1)
	signal.Notify(chld, syscall.SIGCHLD)

	for {
		select {
		case <-term:
			os.Exit(0)
		case <-chld:
			reapZombies()
		}
	}
}

// reapZombies harvests every child that has exited since the last wake. wait4
// with WNOHANG returns pid 0 (no more reapable children) or -ECHILD (no
// children at all); either ends the drain. A coalesced SIGCHLD can stand in for
// several exits, so the loop must continue until one of those terminal results
// rather than reaping a single child per signal.
func reapZombies() {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if pid <= 0 || err != nil {
			return
		}
	}
}
