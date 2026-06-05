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

package diskpressure

import (
	"testing"
	"time"
)

func TestSample_RealPath(t *testing.T) {
	// statfs on a path that always exists. We can't assert exact numbers
	// (they depend on the host volume) but the invariants must hold.
	u, err := Sample(t.TempDir())
	if err != nil {
		t.Fatalf("Sample returned error: %v", err)
	}
	if u.TotalBytes == 0 {
		t.Error("TotalBytes = 0, want > 0 for a mounted filesystem")
	}
	if u.UsedPercent < 0 || u.UsedPercent > 100 {
		t.Errorf("UsedPercent = %v, want in [0,100]", u.UsedPercent)
	}
	if u.AvailBytes > u.TotalBytes {
		t.Errorf("AvailBytes (%d) > TotalBytes (%d)", u.AvailBytes, u.TotalBytes)
	}
}

func TestSample_MissingPathErrors(t *testing.T) {
	if _, err := Sample("/no/such/path/should/exist/for/kukeon/test"); err == nil {
		t.Fatal("Sample on a non-existent path: got nil error, want non-nil")
	}
}

func TestWarner_RateLimitsPerKey(t *testing.T) {
	base := time.Unix(1_700_000_000, 0)
	now := base
	w := NewWarner(5 * time.Minute)
	w.nowFn = func() time.Time { return now }

	if !w.ShouldWarn("default") {
		t.Fatal("first ShouldWarn(default) = false, want true")
	}
	// Within the window: suppressed.
	now = base.Add(4 * time.Minute)
	if w.ShouldWarn("default") {
		t.Error("ShouldWarn(default) within window = true, want false")
	}
	// A different key is tracked independently.
	if !w.ShouldWarn("kuke-system") {
		t.Error("first ShouldWarn(kuke-system) = false, want true")
	}
	// After the window elapses: allowed again.
	now = base.Add(6 * time.Minute)
	if !w.ShouldWarn("default") {
		t.Error("ShouldWarn(default) after window = false, want true")
	}
}

func TestWarner_ZeroWindowAlwaysWarns(t *testing.T) {
	w := NewWarner(0)
	for i := range 3 {
		if !w.ShouldWarn("default") {
			t.Errorf("ShouldWarn call %d with zero window = false, want true", i)
		}
	}
}
