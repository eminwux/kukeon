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

package controller

import (
	"reflect"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func modeBlueprint() v1beta1.CellBlueprintDoc {
	return v1beta1.CellBlueprintDoc{Spec: v1beta1.CellBlueprintSpec{
		Components: []v1beta1.BlueprintComponent{{
			Kind: v1beta1.ComponentKindHarness, Name: "claude",
			Modes: map[string]v1beta1.BlueprintComponentMode{
				v1beta1.ModeInteractive: {},
				v1beta1.ModeHeadless: {Parameters: []v1beta1.CellBlueprintParameter{
					{Name: "PROMPT", Required: true},
				}},
			},
		}},
	}}
}

// TestInferMode locks the transition contract of #1472 step 11: a cell that
// supplies a parameter only one mode declares is created in that mode, so the
// pre-modes dispatch (`--env PROMPT=…`) and a bare `--param PROMPT=…` both
// still start a headless run; anything explicit or ambiguous is left alone.
func TestInferMode(t *testing.T) {
	both := modeBlueprint()
	comp := both.Spec.Components[0]
	comp.Modes = map[string]v1beta1.BlueprintComponentMode{
		"a": {Parameters: []v1beta1.CellBlueprintParameter{{Name: "PROMPT"}}},
		"b": {Parameters: []v1beta1.CellBlueprintParameter{{Name: "PROMPT"}}},
	}
	both.Spec.Components = []v1beta1.BlueprintComponent{comp}

	for _, tc := range []struct {
		name       string
		bp         v1beta1.CellBlueprintDoc
		cfg        v1beta1.CellConfigDoc
		in         cellBinding
		wantMode   string
		wantParams map[string]string
	}{
		{
			name: "param names a headless parameter", bp: modeBlueprint(),
			in:       cellBinding{Params: map[string]string{"PROMPT": "hi"}},
			wantMode: v1beta1.ModeHeadless, wantParams: map[string]string{"PROMPT": "hi"},
		},
		{
			name: "pre-modes env dispatch", bp: modeBlueprint(),
			in:       cellBinding{EnvOverrides: []string{"OTHER=1", "PROMPT=hi"}},
			wantMode: v1beta1.ModeHeadless, wantParams: map[string]string{"PROMPT": "hi"},
		},
		{
			name: "explicit mode wins", bp: modeBlueprint(),
			in:       cellBinding{Mode: v1beta1.ModeInteractive, EnvOverrides: []string{"PROMPT=hi"}},
			wantMode: v1beta1.ModeInteractive,
		},
		{
			name: "empty env value selects nothing", bp: modeBlueprint(),
			in: cellBinding{EnvOverrides: []string{"PROMPT="}},
		},
		{
			name: "unrelated inputs select nothing", bp: modeBlueprint(),
			in: cellBinding{EnvOverrides: []string{"OTHER=1"}},
		},
		{
			name: "ambiguous across modes", bp: both,
			in:         cellBinding{Params: map[string]string{"PROMPT": "hi"}},
			wantParams: map[string]string{"PROMPT": "hi"},
		},
		{
			name: "mode the config disables is not inferred", bp: modeBlueprint(),
			cfg: v1beta1.CellConfigDoc{Spec: v1beta1.CellConfigSpec{
				Modes: &v1beta1.CellConfigModes{Enabled: []string{v1beta1.ModeInteractive}},
			}},
			in: cellBinding{EnvOverrides: []string{"PROMPT=hi"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inferMode(tc.bp, tc.cfg, tc.in)
			if got.Mode != tc.wantMode {
				t.Errorf("mode = %q, want %q", got.Mode, tc.wantMode)
			}
			if len(got.Params) != len(tc.wantParams) ||
				(len(tc.wantParams) > 0 && !reflect.DeepEqual(got.Params, tc.wantParams)) {
				t.Errorf("params = %v, want %v", got.Params, tc.wantParams)
			}
		})
	}
}
