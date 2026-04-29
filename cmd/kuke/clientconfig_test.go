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

package kuke

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func bindKukeEnv(t *testing.T) {
	t.Helper()
	for _, v := range []config.Var{
		config.KUKEON_ROOT_HOST,
		config.KUKEON_ROOT_RUN_PATH,
		config.KUKEON_ROOT_CONTAINERD_SOCKET,
		config.KUKEON_ROOT_LOG_LEVEL,
	} {
		if err := v.BindEnv(); err != nil {
			t.Fatalf("BindEnv %s: %v", v.EnvVar(), err)
		}
	}
}

func TestNewKukeCmdHasConfigurationFlag(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	if flag := cmd.PersistentFlags().Lookup("configuration"); flag == nil {
		t.Fatal("--configuration persistent flag not found")
	}
}

func TestApplyClientConfigurationDefaultsLayered(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	spec := v1beta1.ClientConfigurationSpec{
		Host:             "unix:///tmp/from-config.sock",
		RunPath:          "/opt/kukeon-from-config",
		ContainerdSocket: "/run/containerd/from-config.sock",
		LogLevel:         "warn",
	}
	applyClientConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != spec.Host {
		t.Errorf("Host: got %q, want %q", got, spec.Host)
	}
	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != spec.RunPath {
		t.Errorf("RunPath: got %q, want %q", got, spec.RunPath)
	}
	if got := viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey); got != spec.ContainerdSocket {
		t.Errorf("ContainerdSocket: got %q, want %q", got, spec.ContainerdSocket)
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got != spec.LogLevel {
		t.Errorf("LogLevel: got %q, want %q", got, spec.LogLevel)
	}
}

func TestApplyClientConfigurationFlagOverridesConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	// Operator passed --host on the command line; the config file's value
	// must not clobber it.
	if setErr := cmd.PersistentFlags().Set("host", "unix:///tmp/from-flag.sock"); setErr != nil {
		t.Fatalf("set --host: %v", setErr)
	}

	spec := v1beta1.ClientConfigurationSpec{Host: "unix:///tmp/from-config.sock"}
	applyClientConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != "unix:///tmp/from-flag.sock" {
		t.Errorf("Host flag override lost: got %q, want %q", got, "unix:///tmp/from-flag.sock")
	}
}

func TestApplyClientConfigurationEnvOverridesConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	bindKukeEnv(t)

	// Operator exported KUKEON_HOST and KUKEON_LOG_LEVEL; the on-disk
	// ClientConfiguration must not clobber them. Locks the precedence
	// documented on applyClientConfiguration: --flag > env >
	// ClientConfiguration > default. The mirror of this guard exists for
	// applyServerConfiguration in cmd/kukeond/kukeond_test.go; keep both
	// in lock-step.
	t.Setenv(config.KUKEON_ROOT_HOST.EnvVar(), "unix:///tmp/from-env.sock")
	t.Setenv(config.KUKEON_ROOT_LOG_LEVEL.EnvVar(), "debug")

	spec := v1beta1.ClientConfigurationSpec{
		Host:     "unix:///tmp/from-config.sock",
		LogLevel: "warn",
		RunPath:  "/opt/kukeon-from-config",
	}
	applyClientConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != "unix:///tmp/from-env.sock" {
		t.Errorf("Host env override lost: got %q, want %q", got, "unix:///tmp/from-env.sock")
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got != "debug" {
		t.Errorf("LogLevel env override lost: got %q, want %q", got, "debug")
	}
	// Field with no env var set still picks up the ClientConfiguration value —
	// the env check is per-field, not all-or-nothing.
	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/opt/kukeon-from-config" {
		t.Errorf("RunPath: got %q, want %q", got, "/opt/kukeon-from-config")
	}
}

func TestApplyClientConfigurationEmptyFieldsLeaveViperUntouched(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	viper.Set(config.KUKEON_ROOT_HOST.ViperKey, "unix:///tmp/preexisting.sock")

	applyClientConfiguration(cmd, v1beta1.ClientConfigurationSpec{})

	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != "unix:///tmp/preexisting.sock" {
		t.Errorf("empty spec must not overwrite existing viper value: got %q", got)
	}
}

func TestLoadClientConfigurationAbsentFileNoError(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	// Point --configuration at a path that does not exist; loader returns
	// a zero-value doc and applyClientConfiguration leaves viper untouched.
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	viper.Set(config.KUKE_CONFIGURATION.ViperKey, missing)

	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() unexpected error for absent file: %v", loadErr)
	}
}

func TestLoadClientConfigurationSeedsViper(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "kuke.yaml")
	content := `apiVersion: v1beta1
kind: ClientConfiguration
metadata:
  name: default
spec:
  host: unix:///tmp/from-config.sock
  logLevel: warn
`
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}
	viper.Set(config.KUKE_CONFIGURATION.ViperKey, path)

	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() error = %v", loadErr)
	}
	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != "unix:///tmp/from-config.sock" {
		t.Errorf("Host: got %q, want %q", got, "unix:///tmp/from-config.sock")
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got != "warn" {
		t.Errorf("LogLevel: got %q, want %q", got, "warn")
	}
}
