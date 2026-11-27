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

package configrename_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/configrename"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestCheckSpec(t *testing.T) {
	const path = "/etc/kukeon/kukeond.yaml"
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name:    "retired key under spec",
			raw:     "kind: ServerConfiguration\nspec:\n  " + configrename.RetiredSpaceSubnetKey + ": 10.89.0.0/16\n",
			wantErr: true,
		},
		{
			name: "retired key next to its replacement",
			raw: "spec:\n  " + configrename.SpaceSubnetKey + ": 10.89.0.0/16\n  " +
				configrename.RetiredSpaceSubnetKey + ": 10.89.0.0/16\n",
			wantErr: true,
		},
		{
			name: "replacement key",
			raw:  "spec:\n  " + configrename.SpaceSubnetKey + ": 10.89.0.0/16\n",
		},
		{
			name: "retired spelling outside spec",
			raw:  configrename.RetiredSpaceSubnetKey + ": 10.89.0.0/16\nspec: {}\n",
		},
		{
			name: "no spec",
			raw:  "kind: ServerConfiguration\n",
		},
		{
			name: "unparsable document is left to the caller",
			raw:  "spec: [\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := configrename.CheckSpec([]byte(tt.raw), path)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("CheckSpec() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, errdefs.ErrSettingRenamed) {
				t.Fatalf("CheckSpec() error = %v, want wrapping ErrSettingRenamed", err)
			}
			want := configrename.RetiredSpaceSubnetKey + " was renamed to " +
				configrename.SpaceSubnetKey + "; update " + path
			if !strings.Contains(err.Error(), want) {
				t.Errorf("CheckSpec() error = %q, want it to contain %q", err, want)
			}
		})
	}
}

func TestCheckEnv(t *testing.T) {
	env := func(vars map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			v, ok := vars[key]
			return v, ok
		}
	}

	if err := configrename.CheckEnv(env(nil)); err != nil {
		t.Errorf("CheckEnv() with nothing set: %v", err)
	}
	if err := configrename.CheckEnv(env(map[string]string{configrename.SpaceSubnetEnv: "10.89.0.0/16"})); err != nil {
		t.Errorf("CheckEnv() with only the replacement set: %v", err)
	}
	// Any value counts as set, the empty string included.
	for _, value := range []string{"10.89.0.0/16", ""} {
		err := configrename.CheckEnv(env(map[string]string{configrename.RetiredSpaceSubnetEnv: value}))
		if !errors.Is(err, errdefs.ErrSettingRenamed) {
			t.Fatalf("CheckEnv(%q) error = %v, want wrapping ErrSettingRenamed", value, err)
		}
		want := configrename.RetiredSpaceSubnetEnv + " was renamed to " + configrename.SpaceSubnetEnv
		if !strings.Contains(err.Error(), want) {
			t.Errorf("CheckEnv(%q) error = %q, want it to contain %q", value, err, want)
		}
	}
}
