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

package cgroups

import "github.com/eminwux/kukeon/internal/cgroupcheck"

// SetActiveProberForTest swaps the prober runCheck uses when --probe is
// set and returns a restore closure. Used by unit tests that need to
// simulate EBUSY (the no-internal-process trap) without writing to a
// real cgroupfs.
func SetActiveProberForTest(fn cgroupcheck.Prober) func() {
	prev := activeProber
	activeProber = fn
	return func() { activeProber = prev }
}

// SetSelfHealGateForTest swaps the self-heal fingerprint gate and returns
// a restore closure. Used by unit tests to simulate matching/non-matching
// hosts without reaching /proc/self/cgroup.
func SetSelfHealGateForTest(fn func(string) bool) func() {
	prev := selfHealGate
	selfHealGate = fn
	return func() { selfHealGate = prev }
}

// SetHostProbesForTest swaps both host probes and returns a restore closure.
// TestMain calls this to neutralize the production /proc readers so the
// existing test corpus does not depend on the runner's actual swap or
// userspace-OOM configuration; individual tests call it again locally to
// simulate specific bad-config hosts. Either argument may be nil to leave
// that probe untouched.
func SetHostProbesForTest(swap, oom func() string) func() {
	prevSwap, prevOOM := probeSwap, probeUserspaceOOM
	if swap != nil {
		probeSwap = swap
	}
	if oom != nil {
		probeUserspaceOOM = oom
	}
	return func() {
		probeSwap = prevSwap
		probeUserspaceOOM = prevOOM
	}
}

// ProbeSwapAtForTest exposes the path-parameterized swap probe so unit
// tests can exercise its file-parsing branches against a tmpdir-backed
// fake /proc/swaps.
func ProbeSwapAtForTest(path string) string { return probeSwapAt(path) }

// ProbeUserspaceOOMAtForTest exposes the path-parameterized OOM-guard
// probe so unit tests can exercise its /proc-walking logic against a
// tmpdir-backed fake /proc populated with synthetic <pid>/comm files.
func ProbeUserspaceOOMAtForTest(procDir string) string { return probeUserspaceOOMAt(procDir) }
