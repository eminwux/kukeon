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

package launchcheck

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func proofFixture() probeResult {
	return probeResult{
		ThreadID:           "thread-1",
		SourceRevision:     "revision",
		CodexVersion:       "codex-cli 0.163.0",
		PolicyLink:         true,
		SkillLink:          true,
		InstructionsLoaded: true,
		SkillDiscovered:    true,
		SkillRead:          true,
		ReferenceRead:      true,
		ConfigLoaded:       true,
		CodexHome:          true,
		WorkingDirectory:   true,
		RequirementsSHA256: "requirements",
		HookSHA256:         "hook",
	}
}

func evidenceEvent(t *testing.T, itemType, status string, code int, proof probeResult) string {
	t.Helper()
	raw, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	event := map[string]any{
		"type": "item.completed",
		"item": map[string]any{
			"type":              itemType,
			"status":            status,
			"exit_code":         code,
			"aggregated_output": probeMarker + string(raw),
		},
	}
	raw, err = json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw) + "\n"
}

func TestCaptureRequiresCompletedCommandAndTurn(t *testing.T) {
	start := "{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n"
	complete := "{\"type\":\"turn.completed\"}\n"
	valid := evidenceEvent(t, "command_execution", "completed", 0, proofFixture())
	tests := []struct {
		name, capture   string
		pass, wantError bool
	}{
		{"completed", "terminal setup output\r\n" + start + valid + complete, true, false},
		{"observed first-event framing", "\x1b[?2004l\r" + start + valid + complete, true, false},
		{"prompt before event", "prompt> " + start + valid + complete, false, false},
		{"unsupported escape", "\x1b[31m" + start + valid + complete, false, false},
		{"text before framed event", "prompt> \x1b[?2004l\r" + start + valid + complete, false, false},
		{"without turn", start + valid, false, false},
		{"without thread", valid + complete, false, false},
		{"echoed marker", start + probeMarker + `{"thread_id":"thread-1"}` + "\n" + complete, false, false},
		{
			"model claim",
			start + evidenceEvent(t, "agent_message", "completed", 0, proofFixture()) + complete,
			false,
			false,
		},
		{
			"failed command",
			start + evidenceEvent(t, "command_execution", "completed", 1, proofFixture()) + complete,
			false,
			false,
		},
		{
			"incomplete command",
			start + evidenceEvent(t, "command_execution", "in_progress", 0, proofFixture()) + complete,
			false,
			false,
		},
		{
			"failed turn",
			start + valid + "{\"type\":\"turn.failed\",\"error\":\"sensitive remote detail\"}\n",
			false,
			true,
		},
		{"different thread", strings.Replace(start, "thread-1", "thread-2", 1) + valid + complete, false, true},
		{"multiple threads", start + start + valid + complete, false, true},
	}
	for _, tt := range tests {
		for _, framed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/framed=%v", tt.name, framed), func(t *testing.T) {
				capture := []byte(tt.capture)
				if framed {
					capture = bytes.ReplaceAll(capture, []byte("\n"), []byte("\r\n\x1b[?2004l\r"))
					if !bytes.HasPrefix(capture, []byte("\x1b[?2004l\r")) {
						capture = append([]byte("\x1b[?2004l\r"), capture...)
					}
				}
				proof, done, err := inspectCapture(capture)
				if done != tt.pass || (err != nil) != tt.wantError {
					t.Fatalf("done=%v error=%v", done, err)
				}
				if tt.pass && (proof == nil || proof.validate("revision", "requirements", "hook") != nil) {
					t.Fatal("valid evidence rejected")
				}
				if err != nil && strings.Contains(err.Error(), "sensitive remote detail") {
					t.Fatal("remote error leaked")
				}
			})
		}
	}
}

func TestProbeRequiresEveryObservation(t *testing.T) {
	for _, field := range []string{"policy_link", "skill_link", "instructions_loaded", "skill_discovered", "skill_read", "reference_read", "config_loaded", "codex_home", "working_directory", "source_revision", "requirements_sha256", "hook_sha256", "codex_version"} {
		t.Run(field, func(t *testing.T) {
			raw, err := json.Marshal(proofFixture())
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err = json.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, field)
			raw, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var proof probeResult
			if err = json.Unmarshal(raw, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.validate("revision", "requirements", "hook") == nil {
				t.Fatal("incomplete observation accepted")
			}
		})
	}
}

func TestReservedRootIdentities(t *testing.T) {
	for _, tt := range []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{resource, nil, true},
		{"old-operator-role", map[string]string{"kukeon.io/team": "kukeon"}, true},
		{"other-team-role", map[string]string{"kukeon.io/team": "other"}, false},
	} {
		if got := reserved(tt.name, tt.labels); got != tt.want {
			t.Errorf("reserved(%s)=%v", tt.name, got)
		}
	}
}

// TestProbeCatalog checks the actual alias-based developer message format seen
// in Codex 0.163, plus direct paths and symlink resolution.
func TestProbeCatalog(t *testing.T) {
	interpreter, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for probe encoding tests")
	}
	output, err := exec.CommandContext(t.Context(), interpreter, "-m", "unittest", "-v", "test_probe").CombinedOutput()
	if err != nil {
		t.Fatalf("probe tests: %v\n%s", err, output)
	}
}
