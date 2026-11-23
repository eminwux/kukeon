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

package harness_test

import (
	"maps"
	"os"
	"strings"
	"testing"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/api/version"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// TestBundledHarnessesDeclareBothModes locks the shipped contract: every
// bundled harness starts either attached or dispatched, the prompt belongs to
// the headless mode alone, and no startup is left outside a mode.
func TestBundledHarnessesDeclareBothModes(t *testing.T) {
	for _, name := range []string{"claude", "codex", "opencode"} {
		h, err := harness.Load(os.DirFS("../../../team"), "harnesses/"+name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(h.Spec.OnInit) != 0 || len(h.Spec.Modes) != 2 {
			t.Fatalf("%s: onInit=%d modes=%d", name, len(h.Spec.OnInit), len(h.Spec.Modes))
		}
		interactive, headless := h.Spec.Modes[api.ModeInteractive], h.Spec.Modes[api.ModeHeadless]
		if len(interactive.OnInit) == 0 || len(headless.OnInit) == 0 {
			t.Fatalf("%s: a mode has no startup", name)
		}
		if len(interactive.Parameters) != 0 || len(interactive.Secrets) != 0 {
			t.Fatalf("%s: interactive mode must need no prompt and no API key: %+v", name, interactive)
		}
		if len(headless.Parameters) != 1 || headless.Parameters[0].Name != "PROMPT" ||
			!headless.Parameters[0].Required {
			t.Fatalf("%s: headless parameters = %+v", name, headless.Parameters)
		}
		for _, stage := range interactive.OnInit {
			if strings.Contains(stage.Script, "PROMPT") {
				t.Fatalf("%s: interactive startup still branches on the prompt", name)
			}
		}
		var dispatched strings.Builder
		for _, stage := range headless.OnInit {
			dispatched.WriteString(stage.Script)
		}
		if !strings.Contains(dispatched.String(), "PROMPT") || strings.Contains(dispatched.String(), "${PROMPT}") {
			t.Fatalf("%s: headless startup must read $PROMPT from the environment", name)
		}
	}
}

func validHarness() *harness.Harness {
	return &harness.Harness{
		APIVersion: version.V1Beta1, Kind: "Harness",
		Spec: harness.Spec{
			SkillPath: "$HOME/.agent/skills", Renderer: harness.Renderer, Shell: "/bin/sh",
			Environment: map[string]string{"AGENT_HOME": "${HOME}/.agent"},
			Parameters:  []api.CellBlueprintParameter{{Name: "MODEL"}},
			Secrets:     []api.BlueprintSecretSlot{{Name: "agent-login", EnvName: "AGENT_LOGIN"}},
			Modes: map[string]harness.Mode{
				"interactive": {OnInit: []harness.Script{{Script: "exec agent\n"}}},
				"headless": {
					OnInit:      []harness.Script{{Script: "exec agent run \"$PROMPT\"\n"}},
					Parameters:  []api.CellBlueprintParameter{{Name: "PROMPT", Required: true}},
					Environment: map[string]string{"AGENT_BATCH": "1"},
					Secrets: []api.BlueprintSecretSlot{
						{Name: "agent-api-key", EnvName: "AGENT_API_KEY", Required: true},
					},
				},
			},
			Images: []harness.Image{{
				Name:         "agent",
				Build:        harness.Build{Context: "harnesses/agent/context", Dockerfile: "Dockerfile"},
				Capabilities: []string{"agent", "git"},
			}},
		},
	}
}

func TestModeAndSecretValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*harness.Harness)
		want   string // empty: valid
	}{
		{"modes-without-onInit", func(*harness.Harness) {}, ""},
		{"shared-prefix-with-modes", func(h *harness.Harness) {
			h.Spec.OnInit = []harness.Script{{Script: "set -eu\n"}}
		}, ""},
		{"onInit-without-modes", func(h *harness.Harness) {
			h.Spec.Modes = nil
			h.Spec.OnInit = []harness.Script{{Script: "exec agent\n"}}
		}, ""},
		{"neither", func(h *harness.Harness) { h.Spec.Modes = nil }, "spec.onInit scripts or spec.modes"},
		{"empty-shared-stage", func(h *harness.Harness) { h.Spec.OnInit = []harness.Script{{Script: " \n"}} }, "spec.onInit: script must not be empty"},
		{"mode-without-onInit", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.OnInit = nil
			h.Spec.Modes["headless"] = mode
		}, "spec.modes.headless.onInit: at least one script"},
		{"mode-empty-stage", func(h *harness.Harness) {
			mode := h.Spec.Modes["interactive"]
			mode.OnInit = []harness.Script{{}}
			h.Spec.Modes["interactive"] = mode
		}, "spec.modes.interactive.onInit: script must not be empty"},
		{"invalid-mode-name", func(h *harness.Harness) {
			h.Spec.Modes["Head Less"] = h.Spec.Modes["headless"]
		}, "invalid mode name"},
		// PROMPT is built in only for a harness that has one startup.
		{"prompt-reserved-without-modes", func(h *harness.Harness) {
			h.Spec.Modes = nil
			h.Spec.OnInit = []harness.Script{{Script: "exec agent\n"}}
			h.Spec.Parameters = append(h.Spec.Parameters, api.CellBlueprintParameter{Name: "PROMPT"})
		}, "spec.parameters: invalid, reserved or duplicate"},
		{"prompt-on-harness-with-modes", func(h *harness.Harness) {
			h.Spec.Parameters = append(h.Spec.Parameters, api.CellBlueprintParameter{Name: "PROMPT"})
			mode := h.Spec.Modes["headless"]
			mode.Parameters = nil
			h.Spec.Modes["headless"] = mode
		}, ""},
		{"agent-name-always-reserved", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.Parameters = append(mode.Parameters, api.CellBlueprintParameter{Name: "AGENT_NAME"})
			h.Spec.Modes["headless"] = mode
		}, "spec.modes.headless.parameters"},
		{"mode-redeclares-harness-parameter", func(h *harness.Harness) {
			mode := h.Spec.Modes["interactive"]
			mode.Parameters = []api.CellBlueprintParameter{{Name: "MODEL"}}
			h.Spec.Modes["interactive"] = mode
		}, "spec.modes.interactive.parameters"},
		{"modes-may-repeat-each-other", func(h *harness.Harness) {
			mode := h.Spec.Modes["interactive"]
			mode.Parameters = []api.CellBlueprintParameter{{Name: "PROMPT"}}
			mode.Secrets = []api.BlueprintSecretSlot{{Name: "agent-api-key", EnvName: "AGENT_API_KEY"}}
			h.Spec.Modes["interactive"] = mode
		}, ""},
		{"mode-env-collides-with-harness-env", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.Environment = map[string]string{"AGENT_HOME": "/tmp"}
			h.Spec.Modes["headless"] = mode
		}, "spec.modes.headless.environment: AGENT_HOME is already set"},
		{"mode-env-reserved", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.Environment = map[string]string{"GIT_CONFIG_SYSTEM": "/tmp/config"}
			h.Spec.Modes["headless"] = mode
		}, "spec.modes.headless.environment: invalid or reserved"},
		{"git-ssh-command-reserved", func(h *harness.Harness) {
			h.Spec.Environment["GIT_SSH_COMMAND"] = "ssh -i /key"
		}, "spec.environment: invalid or reserved"},
		{"duplicate-secret-name", func(h *harness.Harness) {
			h.Spec.Secrets = append(h.Spec.Secrets, api.BlueprintSecretSlot{Name: "agent-login", EnvName: "OTHER"})
		}, `spec.secrets: invalid or duplicate secret name "agent-login"`},
		{"invalid-secret-name", func(h *harness.Harness) {
			h.Spec.Secrets[0].Name = "Agent Login"
		}, "spec.secrets: invalid or duplicate secret name"},
		{"duplicate-secret-destination", func(h *harness.Harness) {
			h.Spec.Secrets = append(h.Spec.Secrets, api.BlueprintSecretSlot{Name: "second", EnvName: "AGENT_LOGIN"})
		}, `secret "second": invalid or duplicate envName`},
		{"invalid-secret-destination", func(h *harness.Harness) {
			h.Spec.Secrets[0].EnvName = "lower-case"
		}, "invalid or duplicate envName"},
		{"file-secret", func(h *harness.Harness) {
			h.Spec.Secrets[0].MountPath = "/run/secret"
		}, "only environment destinations"},
		{"file-mode-secret", func(h *harness.Harness) {
			h.Spec.Secrets[0].Mode = api.BlueprintSecretModeFile
		}, "only environment destinations"},
		{"mode-secret-repeats-harness-name", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.Secrets = append(mode.Secrets, api.BlueprintSecretSlot{Name: "agent-login", EnvName: "SECOND_LOGIN"})
			h.Spec.Modes["headless"] = mode
		}, `spec.modes.headless.secrets: invalid or duplicate secret name "agent-login"`},
		{"mode-secret-repeats-harness-destination", func(h *harness.Harness) {
			mode := h.Spec.Modes["headless"]
			mode.Secrets = append(mode.Secrets, api.BlueprintSecretSlot{Name: "second", EnvName: "AGENT_LOGIN"})
			h.Spec.Modes["headless"] = mode
		}, `spec.modes.headless.secrets: secret "second": invalid or duplicate envName`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := validHarness()
			h.Metadata.Name = "agent"
			h.Spec.Modes = maps.Clone(h.Spec.Modes)
			tc.mutate(h)
			err := h.Validate()
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("valid harness rejected: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}
