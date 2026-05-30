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
	"testing"
	"time"
)

// TestReapZombiesTerminatesWithNoChildren guards the drain loop's exit
// condition: Wait4 returns -ECHILD (or pid 0) when there is nothing to reap, and
// reapZombies must return on that rather than spin. A regression here would hang
// PID 1 inside the SIGCHLD handler — the kind of bug that only shows up as a
// wedged pause container, so assert termination directly. The test process has
// no reapable children of its own (the Go runtime owns and reaps any it spawns),
// so this exercises the empty-children path.
func TestReapZombiesTerminatesWithNoChildren(t *testing.T) {
	done := make(chan struct{})
	go func() {
		reapZombies()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("reapZombies did not return with no reapable children — drain loop may be spinning")
	}
}
