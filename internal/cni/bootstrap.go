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

package cni

import "os"

// BootstrapReport captures CNI environment checks and actions.
type BootstrapReport struct {
	CniConfigDir        string
	CniCacheDir         string
	CniBinDir           string
	ConfigDirExistsPre  bool
	CacheDirExistsPre   bool
	BinDirExistsPre     bool
	ConfigDirCreated    bool
	CacheDirCreated     bool
	BinDirCreated       bool
	ConfigDirExistsPost bool
	CacheDirExistsPost  bool
	BinDirExistsPost    bool
}

// BootstrapCNI ensures required directories exist and the bin directory is present.
// If any directory is empty, the defaults from config.go are used.
func BootstrapCNI(cfgDir, cacheDir, binDir string) (BootstrapReport, error) {
	if cfgDir == "" {
		cfgDir = defaultCniConfDir
	}
	if cacheDir == "" {
		cacheDir = defaultCniCacheDir
	}
	if binDir == "" {
		binDir = defaultCniBinDir
	}

	report := BootstrapReport{
		CniConfigDir: cfgDir,
		CniCacheDir:  cacheDir,
		CniBinDir:    binDir,
	}

	// Pre-state
	if _, err := os.Stat(cfgDir); err == nil {
		report.ConfigDirExistsPre = true
	}
	if _, err := os.Stat(cacheDir); err == nil {
		report.CacheDirExistsPre = true
	}
	if _, err := os.Stat(binDir); err == nil {
		report.BinDirExistsPre = true
	}

	// Create missing dirs
	if !report.ConfigDirExistsPre {
		if err := os.MkdirAll(cfgDir, 0o750); err != nil {
			return report, err
		}
		report.ConfigDirCreated = true
	}
	if !report.CacheDirExistsPre {
		if err := os.MkdirAll(cacheDir, 0o750); err != nil {
			return report, err
		}
		report.CacheDirCreated = true
	}
	if !report.BinDirExistsPre {
		if err := os.MkdirAll(binDir, 0o750); err != nil {
			return report, err
		}
		report.BinDirCreated = true
	}

	// Post-state
	if _, err := os.Stat(cfgDir); err == nil {
		report.ConfigDirExistsPost = true
	}
	if _, err := os.Stat(cacheDir); err == nil {
		report.CacheDirExistsPost = true
	}
	if _, err := os.Stat(binDir); err == nil {
		report.BinDirExistsPost = true
	}

	return report, nil
}
