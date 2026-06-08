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

package shared

import (
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/spf13/cobra"
)

// newScopeCmd builds a bare cobra command with --realm/--space/--stack
// pflags so the helpers can read flag state. Mirrors the surface
// NewRunCmd / NewApplyCmd register on their commands without dragging in
// the rest of those packages' wiring.
func newScopeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().String("realm", "", "")
	cmd.Flags().String("space", "", "")
	cmd.Flags().String("stack", "", "")
	return cmd
}

func TestPickLookupRealm_Precedence(t *testing.T) {
	tests := []struct {
		name      string
		flagValue string
		flagSet   bool
		env       string
		envSet    bool
		want      string
	}{
		{name: "no flag, no env → default", want: "default"},
		{name: "env only → env", env: "from-env", envSet: true, want: "from-env"},
		{name: "flag only → flag", flagValue: "from-flag", flagSet: true, want: "from-flag"},
		{
			name:      "flag wins over env",
			flagValue: "from-flag",
			flagSet:   true,
			env:       "from-env",
			envSet:    true,
			want:      "from-flag",
		},
		{name: "whitespace env ignored", env: "   ", envSet: true, want: "default"},
		{
			name:      "whitespace flag ignored, env wins",
			flagValue: "   ",
			flagSet:   true,
			env:       "from-env",
			envSet:    true,
			want:      "from-env",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv(config.KUKE_RUN_REALM.Key, tc.env)
			}
			cmd := newScopeCmd()
			if tc.flagSet {
				if err := cmd.Flags().Set("realm", tc.flagValue); err != nil {
					t.Fatalf("flag set: %v", err)
				}
			}
			if got := PickLookupRealm(cmd, &config.KUKE_RUN_REALM); got != tc.want {
				t.Errorf("PickLookupRealm=%q want %q", got, tc.want)
			}
		})
	}
}

func TestExplicitScope_Precedence(t *testing.T) {
	tests := []struct {
		name      string
		flagValue string
		flagSet   bool
		env       string
		envSet    bool
		kv        *config.Var
		flag      string
		want      string
	}{
		{
			// No flag, no env → falls back to kv.Default ("default" for
			// KUKE_RUN_SPACE) so a no-flag lookup resolves to the full
			// default scope (issue #1156).
			name: "space: no flag, no env → kv.Default",
			flag: "space",
			kv:   &config.KUKE_RUN_SPACE,
			want: "default",
		},
		{
			// Explicit empty flag is honored as empty — the escape hatch that
			// keeps realm-scoped Blueprints/Configs (no space/stack) findable.
			name:      "space: empty flag set explicitly → empty (escape hatch)",
			flag:      "space",
			kv:        &config.KUKE_RUN_SPACE,
			flagValue: "",
			flagSet:   true,
			want:      "",
		},
		{
			// Explicit empty env is likewise honored as empty.
			name:   "space: empty env set explicitly → empty (escape hatch)",
			flag:   "space",
			kv:     &config.KUKE_RUN_SPACE,
			env:    "",
			envSet: true,
			want:   "",
		},
		{
			name:   "space: env only → env",
			flag:   "space",
			kv:     &config.KUKE_RUN_SPACE,
			env:    "eng",
			envSet: true,
			want:   "eng",
		},
		{
			name:      "space: flag only → flag",
			flag:      "space",
			kv:        &config.KUKE_RUN_SPACE,
			flagValue: "platform",
			flagSet:   true,
			want:      "platform",
		},
		{
			name:      "space: flag wins over env",
			flag:      "space",
			kv:        &config.KUKE_RUN_SPACE,
			flagValue: "platform",
			flagSet:   true,
			env:       "eng",
			envSet:    true,
			want:      "platform",
		},
		{
			name:   "stack: env only → env",
			flag:   "stack",
			kv:     &config.KUKE_RUN_STACK,
			env:    "core",
			envSet: true,
			want:   "core",
		},
		{
			name:      "stack: empty flag set explicitly → empty (flag wins)",
			flag:      "stack",
			kv:        &config.KUKE_RUN_STACK,
			flagValue: "",
			flagSet:   true,
			env:       "core",
			envSet:    true,
			want:      "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envSet {
				t.Setenv(tc.kv.Key, tc.env)
			}
			cmd := newScopeCmd()
			if tc.flagSet {
				if err := cmd.Flags().Set(tc.flag, tc.flagValue); err != nil {
					t.Fatalf("flag set: %v", err)
				}
			}
			if got := ExplicitScope(cmd, tc.flag, tc.kv); got != tc.want {
				t.Errorf("ExplicitScope(%s)=%q want %q", tc.flag, got, tc.want)
			}
		})
	}
}
