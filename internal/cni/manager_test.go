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

package cni_test

import (
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
)

func TestNewManager(t *testing.T) {
	tests := []struct {
		name      string
		binDir    string
		configDir string
		cacheDir  string
		wantErr   bool
	}{
		{
			name:      "success with all directories provided",
			binDir:    "/test/bin",
			configDir: "/test/config",
			cacheDir:  "/test/cache",
			wantErr:   false,
		},
		{
			name:      "success with empty directories (defaults applied)",
			binDir:    "",
			configDir: "",
			cacheDir:  "",
			wantErr:   false,
		},
		{
			name:      "success with partial directories (some defaults)",
			binDir:    "/test/bin",
			configDir: "",
			cacheDir:  "/test/cache",
			wantErr:   false,
		},
		{
			name:      "success with only config dir provided",
			binDir:    "",
			configDir: "/test/config",
			cacheDir:  "",
			wantErr:   false,
		},
		{
			name:      "success with only bin dir provided",
			binDir:    "/test/bin",
			configDir: "",
			cacheDir:  "",
			wantErr:   false,
		},
		{
			name:      "success with only cache dir provided",
			binDir:    "",
			configDir: "",
			cacheDir:  "/test/cache",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, err := cni.NewManager(tt.binDir, tt.configDir, tt.cacheDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("NewManager() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("NewManager() error = %v, want nil", err)
			}

			if mgr == nil {
				t.Fatal("NewManager() returned nil manager")
			}

			// Note: We cannot directly access the conf field in external tests.
			// Directory configuration is tested indirectly through methods that use it
			// (e.g., CreateNetworkWithConfig uses the config directory).
		})
	}
}
