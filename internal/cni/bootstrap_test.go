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
	"os"
	"path/filepath"
	"testing"

	cni "github.com/eminwux/kukeon/internal/cni"
)

func TestBootstrapCNI(t *testing.T) {
	tests := []struct {
		name       string
		cfgDir     string
		cacheDir   string
		binDir     string
		setup      func(t *testing.T, baseDir string) (string, string, string)
		wantReport func(t *testing.T, report cni.BootstrapReport)
		wantErr    bool
	}{
		{
			name: "success with all directories provided",
			setup: func(_ *testing.T, baseDir string) (string, string, string) {
				cfgDir := filepath.Join(baseDir, "config")
				cacheDir := filepath.Join(baseDir, "cache")
				binDir := filepath.Join(baseDir, "bin")
				return cfgDir, cacheDir, binDir
			},
			wantReport: func(t *testing.T, report cni.BootstrapReport) {
				// Verify directories were created
				if !report.ConfigDirExistsPost {
					t.Error("ConfigDirExistsPost = false, want true")
				}
				if !report.CacheDirExistsPost {
					t.Error("CacheDirExistsPost = false, want true")
				}
				if !report.BinDirExistsPost {
					t.Error("BinDirExistsPost = false, want true")
				}

				// Verify report fields
				if report.CniConfigDir == "" {
					t.Error("CniConfigDir is empty")
				}
				if report.CniCacheDir == "" {
					t.Error("CniCacheDir is empty")
				}
				if report.CniBinDir == "" {
					t.Error("CniBinDir is empty")
				}
			},
			wantErr: false,
		},
		{
			name: "success with empty directories (uses defaults)",
			setup: func(_ *testing.T, _ string) (string, string, string) {
				return "", "", ""
			},
			wantReport: func(t *testing.T, report cni.BootstrapReport) {
				// Verify defaults are used
				if report.CniConfigDir != "/opt/cni/net.d" {
					t.Errorf("CniConfigDir = %q, want %q", report.CniConfigDir, "/opt/cni/net.d")
				}
				if report.CniCacheDir != "/opt/cni/cache" {
					t.Errorf("CniCacheDir = %q, want %q", report.CniCacheDir, "/opt/cni/cache")
				}
				if report.CniBinDir != "/opt/cni/bin" {
					t.Errorf("CniBinDir = %q, want %q", report.CniBinDir, "/opt/cni/bin")
				}
			},
			wantErr: false,
		},
		{
			name: "creates missing directories",
			setup: func(_ *testing.T, baseDir string) (string, string, string) {
				cfgDir := filepath.Join(baseDir, "config")
				cacheDir := filepath.Join(baseDir, "cache")
				binDir := filepath.Join(baseDir, "bin")
				return cfgDir, cacheDir, binDir
			},
			wantReport: func(t *testing.T, report cni.BootstrapReport) {
				// Verify directories were created
				if !report.ConfigDirCreated {
					t.Error("ConfigDirCreated = false, want true")
				}
				if !report.CacheDirCreated {
					t.Error("CacheDirCreated = false, want true")
				}
				if !report.BinDirCreated {
					t.Error("BinDirCreated = false, want true")
				}

				// Verify pre-state was false
				if report.ConfigDirExistsPre {
					t.Error("ConfigDirExistsPre = true, want false")
				}
				if report.CacheDirExistsPre {
					t.Error("CacheDirExistsPre = true, want false")
				}
				if report.BinDirExistsPre {
					t.Error("BinDirExistsPre = true, want false")
				}

				// Verify post-state is true
				if !report.ConfigDirExistsPost {
					t.Error("ConfigDirExistsPost = false, want true")
				}
				if !report.CacheDirExistsPost {
					t.Error("CacheDirExistsPost = false, want true")
				}
				if !report.BinDirExistsPost {
					t.Error("BinDirExistsPost = false, want true")
				}

				// Verify directories actually exist
				if _, err := os.Stat(report.CniConfigDir); err != nil {
					t.Errorf("config directory does not exist: %v", err)
				}
				if _, err := os.Stat(report.CniCacheDir); err != nil {
					t.Errorf("cache directory does not exist: %v", err)
				}
				if _, err := os.Stat(report.CniBinDir); err != nil {
					t.Errorf("bin directory does not exist: %v", err)
				}
			},
			wantErr: false,
		},
		{
			name: "reports correct pre/post states when directories exist",
			setup: func(t *testing.T, baseDir string) (string, string, string) {
				cfgDir := filepath.Join(baseDir, "config")
				cacheDir := filepath.Join(baseDir, "cache")
				binDir := filepath.Join(baseDir, "bin")

				// Create directories before bootstrap
				if err := os.MkdirAll(cfgDir, 0o750); err != nil {
					t.Fatalf("failed to create config dir: %v", err)
				}
				if err := os.MkdirAll(cacheDir, 0o750); err != nil {
					t.Fatalf("failed to create cache dir: %v", err)
				}
				if err := os.MkdirAll(binDir, 0o750); err != nil {
					t.Fatalf("failed to create bin dir: %v", err)
				}

				return cfgDir, cacheDir, binDir
			},
			wantReport: func(t *testing.T, report cni.BootstrapReport) {
				// Verify pre-state was true
				if !report.ConfigDirExistsPre {
					t.Error("ConfigDirExistsPre = false, want true")
				}
				if !report.CacheDirExistsPre {
					t.Error("CacheDirExistsPre = false, want true")
				}
				if !report.BinDirExistsPre {
					t.Error("BinDirExistsPre = false, want true")
				}

				// Verify directories were not created
				if report.ConfigDirCreated {
					t.Error("ConfigDirCreated = true, want false")
				}
				if report.CacheDirCreated {
					t.Error("CacheDirCreated = true, want false")
				}
				if report.BinDirCreated {
					t.Error("BinDirCreated = true, want false")
				}

				// Verify post-state is true
				if !report.ConfigDirExistsPost {
					t.Error("ConfigDirExistsPost = false, want true")
				}
				if !report.CacheDirExistsPost {
					t.Error("CacheDirExistsPost = false, want true")
				}
				if !report.BinDirExistsPost {
					t.Error("BinDirExistsPost = false, want true")
				}
			},
			wantErr: false,
		},
		{
			name: "idempotent (can be called multiple times)",
			setup: func(_ *testing.T, baseDir string) (string, string, string) {
				cfgDir := filepath.Join(baseDir, "config")
				cacheDir := filepath.Join(baseDir, "cache")
				binDir := filepath.Join(baseDir, "bin")
				return cfgDir, cacheDir, binDir
			},
			wantReport: func(t *testing.T, report cni.BootstrapReport) {
				// First call should create directories
				if !report.ConfigDirCreated {
					t.Error("first call: ConfigDirCreated = false, want true")
				}
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseDir := t.TempDir()
			cfgDir, cacheDir, binDir := tt.setup(t, baseDir)

			report, err := cni.BootstrapCNI(cfgDir, cacheDir, binDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("BootstrapCNI() error = nil, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("BootstrapCNI() error = %v, want nil", err)
			}

			if tt.wantReport != nil {
				tt.wantReport(t, report)
			}

			// Test idempotency for the "idempotent" test case
			if tt.name == "idempotent (can be called multiple times)" {
				report2, err2 := cni.BootstrapCNI(cfgDir, cacheDir, binDir)
				if err2 != nil {
					t.Fatalf("second BootstrapCNI() call error = %v, want nil", err2)
				}

				// Second call should not create directories
				if report2.ConfigDirCreated {
					t.Error("second call: ConfigDirCreated = true, want false")
				}
				if report2.CacheDirCreated {
					t.Error("second call: CacheDirCreated = true, want false")
				}
				if report2.BinDirCreated {
					t.Error("second call: BinDirCreated = true, want false")
				}

				// Pre-state should be true on second call
				if !report2.ConfigDirExistsPre {
					t.Error("second call: ConfigDirExistsPre = false, want true")
				}
				if !report2.CacheDirExistsPre {
					t.Error("second call: CacheDirExistsPre = false, want true")
				}
				if !report2.BinDirExistsPre {
					t.Error("second call: BinDirExistsPre = false, want true")
				}
			}
		})
	}
}
