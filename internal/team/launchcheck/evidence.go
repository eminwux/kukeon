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

// Package launchcheck contains evidence readers for the opt-in remote-image check.
package launchcheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
)

const probeMarker = "KUKETEAMS_PROBE_V1="

type probeResult struct {
	ThreadID           string `json:"thread_id"`
	SourceRevision     string `json:"source_revision"`
	CodexVersion       string `json:"codex_version"`
	PolicyLink         bool   `json:"policy_link"`
	SkillLink          bool   `json:"skill_link"`
	InstructionsLoaded bool   `json:"instructions_loaded"`
	SkillDiscovered    bool   `json:"skill_discovered"`
	SkillRead          bool   `json:"skill_read"`
	ReferenceRead      bool   `json:"reference_read"`
	ConfigLoaded       bool   `json:"config_loaded"`
	CodexHome          bool   `json:"codex_home"`
	WorkingDirectory   bool   `json:"working_directory"`
	RequirementsSHA256 string `json:"requirements_sha256"`
	HookSHA256         string `json:"hook_sha256"`
}

// inspectCapture accepts evidence only from a successful Codex command item in
// the current thread, followed by a completed authenticated turn. Echoed prompts
// and model claims alone cannot pass. Raw capture and error messages are private.
func inspectCapture(raw []byte) (*probeResult, bool, error) {
	var thread string
	var proof *probeResult
	for _, line := range bytes.Split(raw, []byte{'\n'}) {
		var event struct {
			Type     string `json:"type"`
			ThreadID string `json:"thread_id"`
			Item     struct {
				Type     string `json:"type"`
				Status   string `json:"status"`
				ExitCode *int   `json:"exit_code"`
				Output   string `json:"aggregated_output"`
			} `json:"item"`
		}
		// The PTY may prefix an event with bracketed-paste disable followed
		// by CR. Strip only this known leading framing; never search prose
		// for embedded JSON or remove control bytes inside event payloads.
		line = bytes.TrimSpace(line)
		line = bytes.TrimPrefix(line, []byte("\x1b[?2004l\r"))
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		switch event.Type {
		case "thread.started":
			if thread != "" {
				return nil, false, errors.New("multiple Codex threads in capture")
			}
			thread = event.ThreadID
		case "error", "turn.failed":
			return nil, false, errors.New(
				"codex interaction failed; verify the preconfigured authentication and model access",
			)
		case "item.completed":
			if thread == "" || event.Item.Type != "command_execution" || event.Item.Status != "completed" ||
				event.Item.ExitCode == nil ||
				*event.Item.ExitCode != 0 {
				continue
			}
			for _, outputLine := range strings.Split(event.Item.Output, "\n") {
				if !strings.HasPrefix(outputLine, probeMarker) {
					continue
				}
				var candidate probeResult
				if err := json.Unmarshal([]byte(strings.TrimPrefix(outputLine, probeMarker)), &candidate); err != nil {
					return nil, false, errors.New("invalid probe result")
				}
				if candidate.ThreadID != thread {
					return nil, false, errors.New("probe thread does not match Codex thread")
				}
				proof = &candidate
			}
		case "turn.completed":
			if proof != nil {
				return proof, true, nil
			}
		}
	}
	return proof, false, nil
}

func (p *probeResult) validate(revision, requirements, hook string) error {
	if !p.PolicyLink || !p.SkillLink || !p.InstructionsLoaded || !p.SkillDiscovered || !p.SkillRead ||
		!p.ReferenceRead ||
		!p.ConfigLoaded ||
		!p.CodexHome ||
		!p.WorkingDirectory {
		return errors.New("operator instruction, skill, or effective configuration evidence is incomplete")
	}
	if p.SourceRevision != revision || p.RequirementsSHA256 != requirements || p.HookSHA256 != hook ||
		!strings.HasPrefix(p.CodexVersion, "codex-cli ") {
		return errors.New("operator source revision or image-owned harness assets do not match")
	}
	return nil
}
