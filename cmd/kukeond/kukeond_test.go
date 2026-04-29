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

package kukeond

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func TestNewKukeondCmdHasConfigurationFlag(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}
	if flag := cmd.PersistentFlags().Lookup("configuration"); flag == nil {
		t.Fatal("--configuration persistent flag not found")
	}
}

func TestApplyServerConfigurationDefaultsLayered(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}

	spec := v1beta1.ServerConfigurationSpec{
		Socket:           "/run/kukeon/from-config.sock",
		SocketGID:        4242,
		RunPath:          "/opt/kukeon-from-config",
		ContainerdSocket: "/run/containerd/from-config.sock",
		LogLevel:         "warn",
	}
	applyServerConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEOND_SOCKET.ViperKey); got != spec.Socket {
		t.Errorf("Socket: got %q, want %q", got, spec.Socket)
	}
	if got := viper.GetInt(config.KUKEOND_SOCKET_GID.ViperKey); got != spec.SocketGID {
		t.Errorf("SocketGID: got %d, want %d", got, spec.SocketGID)
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

func TestApplyServerConfigurationFlagOverridesConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}
	// Operator passed --socket on the command line; the config file's value
	// must not clobber it.
	if setErr := cmd.PersistentFlags().Set("socket", "/run/kukeon/from-flag.sock"); setErr != nil {
		t.Fatalf("set --socket: %v", setErr)
	}

	spec := v1beta1.ServerConfigurationSpec{Socket: "/run/kukeon/from-config.sock"}
	applyServerConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEOND_SOCKET.ViperKey); got != "/run/kukeon/from-flag.sock" {
		t.Errorf("Socket flag override lost: got %q, want %q", got, "/run/kukeon/from-flag.sock")
	}
}

func TestApplyServerConfigurationEnvOverridesConfig(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}

	// Operator exported KUKEOND_SOCKET, KUKEON_CONTAINERD_SOCKET, and
	// KUKEON_LOG_LEVEL; the on-disk ServerConfiguration must not clobber
	// them. Documents the precedence order in the PR description:
	// --flag > env > ServerConfiguration > default. The KUKEON_CONTAINERD_SOCKET
	// assertion is the regression guard for issue #191 — without
	// bindEnvVars() in NewKukeondCmd the env binding does not exist and
	// viper falls back to the flag default, silently dropping both env
	// and YAML.
	t.Setenv(config.KUKEOND_SOCKET.EnvVar(), "/run/kukeon/from-env.sock")
	t.Setenv(config.KUKEON_ROOT_CONTAINERD_SOCKET.EnvVar(), "/run/containerd/from-env.sock")
	t.Setenv(config.KUKEON_ROOT_LOG_LEVEL.EnvVar(), "debug")

	spec := v1beta1.ServerConfigurationSpec{
		Socket:           "/run/kukeon/from-config.sock",
		ContainerdSocket: "/run/containerd/from-config.sock",
		LogLevel:         "warn",
		RunPath:          "/opt/kukeon-from-config",
	}
	applyServerConfiguration(cmd, spec)

	if got := viper.GetString(config.KUKEOND_SOCKET.ViperKey); got != "/run/kukeon/from-env.sock" {
		t.Errorf("Socket env override lost: got %q, want %q", got, "/run/kukeon/from-env.sock")
	}
	if got := viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey); got != "/run/containerd/from-env.sock" {
		t.Errorf("ContainerdSocket env override lost: got %q, want %q", got, "/run/containerd/from-env.sock")
	}
	if got := viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey); got != "debug" {
		t.Errorf("LogLevel env override lost: got %q, want %q", got, "debug")
	}
	// Field with no env var set still picks up the ServerConfiguration value —
	// the env check is per-field, not all-or-nothing.
	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/opt/kukeon-from-config" {
		t.Errorf("RunPath: got %q, want %q", got, "/opt/kukeon-from-config")
	}
}

func TestApplyServerConfigurationEmptyFieldsLeaveViperUntouched(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}
	viper.Set(config.KUKEOND_SOCKET.ViperKey, "/run/kukeon/preexisting.sock")

	applyServerConfiguration(cmd, v1beta1.ServerConfigurationSpec{})

	if got := viper.GetString(config.KUKEOND_SOCKET.ViperKey); got != "/run/kukeon/preexisting.sock" {
		t.Errorf("empty spec must not overwrite existing viper value: got %q", got)
	}
}

// TestMaybeWriteServerConfigurationDefaultSkipsCustomPath locks the guard
// that prevents tests (and operators pointing --configuration at a custom
// location) from writing to /etc/kukeon/kukeond.yaml on the host. The
// path-equality guard is what makes operator-supplied custom paths opt out
// of the default-location dump (per the issue's "Both honor --configuration
// to opt out of the default location" note).
func TestMaybeWriteServerConfigurationDefaultSkipsCustomPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeondCmd()
	if err != nil {
		t.Fatalf("NewKukeondCmd() error = %v", err)
	}

	dir := t.TempDir()
	custom := filepath.Join(dir, "custom-kukeond.yaml")
	maybeWriteServerConfigurationDefault(cmd, custom)
	if _, statErr := os.Stat(custom); !os.IsNotExist(statErr) {
		t.Fatalf("custom path was written; got Stat err=%v, want IsNotExist", statErr)
	}
}
