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

package run

import (
	"strings"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestFocusedSliceDiffMultiLineScriptOneTokenChange is the AC's headline case
// (issue #1193): a one-token change inside a multi-line `sh -c` script must
// surface the changed token without the operator hand-diffing two walls of
// escaped shell text.
func TestFocusedSliceDiffMultiLineScriptOneTokenChange(t *testing.T) {
	script := func(port string) string {
		return "set -e\n" +
			"while true; do\n" +
			"  echo serving on " + port + "\n" +
			"  nc -l -p " + port + " -e /bin/cat\n" +
			"  sleep 1\n" +
			"done\n"
	}
	actual := []string{"-c", script("8080")}
	desired := []string{"-c", script("8081")}

	got := focusedSliceDiff("args", actual, desired)

	// The changed token on both sides must be visible.
	if !strings.Contains(got, "8080") || !strings.Contains(got, "8081") {
		t.Errorf("focused diff omits the changed token:\n%s", got)
	}
	// The common leading element ("-c") must be summarized, not dumped.
	if !strings.Contains(got, "1 leading") {
		t.Errorf("focused diff does not summarize the identical leading element:\n%s", got)
	}
	// Truncation around the diff must have kicked in for the long element.
	if !strings.Contains(got, "…") {
		t.Errorf("focused diff did not window the long element:\n%s", got)
	}
	// And the result must be dramatically shorter than dumping both scripts
	// verbatim, which is the wall-of-text the issue is about.
	fullDump := sliceDiff("args", actual, desired)
	if len(got) >= len(fullDump) {
		t.Errorf("focused diff (%d bytes) is not shorter than the full both-sides dump (%d bytes):\n%s",
			len(got), len(fullDump), got)
	}
}

// TestFocusedSliceDiffElidesCommonFraming covers AC item 2: identical
// leading/trailing elements are elided/summarized and only the differing
// index is named.
func TestFocusedSliceDiffElidesCommonFraming(t *testing.T) {
	actual := []string{"sh", "-c", "x", "--port", "8080"}
	desired := []string{"sh", "-c", "x", "--port", "8081"}

	got := focusedSliceDiff("args", actual, desired)

	if !strings.Contains(got, "4 leading, 0 trailing identical") {
		t.Errorf("expected leading/trailing summary, got:\n%s", got)
	}
	if !strings.Contains(got, "[4]") {
		t.Errorf("expected the differing index [4] to be named, got:\n%s", got)
	}
	if !strings.Contains(got, `"8080"`) || !strings.Contains(got, `"8081"`) {
		t.Errorf("expected both sides of the changed element, got:\n%s", got)
	}
	// The unchanged framing elements must not be repeated verbatim.
	if strings.Contains(got, `"sh"`) {
		t.Errorf("focused diff repeated an elided framing element, got:\n%s", got)
	}
}

// TestFocusedSliceDiffAddRemove names the index range and contents when the
// slices differ in length (an element added or removed).
func TestFocusedSliceDiffAddRemove(t *testing.T) {
	actual := []string{"a", "b", "c"}
	desired := []string{"a", "c"}

	got := focusedSliceDiff("args", actual, desired)

	if !strings.Contains(got, "actual[1:2]") || !strings.Contains(got, "desired[1:1]") {
		t.Errorf("expected index ranges for the add/remove case, got:\n%s", got)
	}
	if !strings.Contains(got, `"b"`) {
		t.Errorf("expected the removed element in the actual window, got:\n%s", got)
	}
}

// TestFocusedScalarDiffShortVerbatim keeps the verbatim both-sides shape for
// short scalar fields (command) so the common case is unchanged.
func TestFocusedScalarDiffShortVerbatim(t *testing.T) {
	got := focusedScalarDiff("command", "/bin/sh", "/bin/bash")
	if !strings.Contains(got, `"/bin/sh"`) || !strings.Contains(got, `"/bin/bash"`) {
		t.Errorf("short scalar diff should print both sides verbatim, got:\n%s", got)
	}
	if strings.Contains(got, "…") {
		t.Errorf("short scalar diff should not truncate, got:\n%s", got)
	}
}

// TestFocusedScalarDiffLongTruncates windows a long scalar around the first
// differing rune.
func TestFocusedScalarDiffLongTruncates(t *testing.T) {
	prefix := strings.Repeat("a", 80)
	got := focusedScalarDiff("command", prefix+"X"+prefix, prefix+"Y"+prefix)
	if !strings.Contains(got, "…") {
		t.Errorf("long scalar diff should truncate, got:\n%s", got)
	}
	if !strings.Contains(got, "X") || !strings.Contains(got, "Y") {
		t.Errorf("long scalar diff should keep the changed rune visible, got:\n%s", got)
	}
}

// TestDivergedContainerFieldsUsesFocusedArgs is the end-to-end assertion that
// the divergence notice (the surface the operator actually sees) carries the
// focused diff for args while still naming the field.
func TestDivergedContainerFieldsUsesFocusedArgs(t *testing.T) {
	long := strings.Repeat("echo step; ", 30)
	actual := v1beta1.ContainerSpec{ID: "web", Args: []string{"-c", long + "-p 8080"}}
	desired := v1beta1.ContainerSpec{ID: "web", Args: []string{"-c", long + "-p 8081"}}

	fields := divergedContainerFields(actual, desired)
	if len(fields) != 1 {
		t.Fatalf("expected exactly the args field to diverge, got %v", fields)
	}
	if !strings.HasPrefix(fields[0], "args (") {
		t.Errorf("field should still be named args, got:\n%s", fields[0])
	}
	if !strings.Contains(fields[0], "8080") || !strings.Contains(fields[0], "8081") {
		t.Errorf("focused args diff should keep the changed token, got:\n%s", fields[0])
	}
	if !strings.Contains(fields[0], "…") {
		t.Errorf("focused args diff should window the long element, got:\n%s", fields[0])
	}
}
