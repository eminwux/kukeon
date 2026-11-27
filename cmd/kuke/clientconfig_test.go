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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/configrename"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// restoreRuntimeGlobals snapshots the consts package runtime globals and the
// cni subnet allocator's process-global parent CIDR, and returns a cleanup
// that restores them. loadClientConfiguration calls consts.ConfigureRuntime
// and cni.ConfigureSubnetParentCIDR, which mutate these — tests that exercise
// it must roll back so the next test starts on the in-binary defaults.
func restoreRuntimeGlobals(t *testing.T) {
	t.Helper()
	prevSuffix := consts.RealmNamespaceSuffix
	prevRoot := consts.KukeonCgroupRoot
	t.Cleanup(func() {
		consts.RealmNamespaceSuffix = prevSuffix //nolint:reassign // restore runtime-overridable global
		consts.KukeonCgroupRoot = prevRoot       //nolint:reassign // restore runtime-overridable global
		_ = cni.ConfigureSubnetParentCIDR(cni.DefaultSubnetParentCIDR)
	})
}

// clientConfigEnvVars lists the config.Var descriptors whose env var
// applyClientConfiguration consults (via envSet) before layering a
// ClientConfiguration field into viper.
func clientConfigEnvVars() []config.Var {
	return []config.Var{
		config.KUKEON_ROOT_HOST,
		config.KUKEON_ROOT_RUN_PATH,
		config.KUKEON_ROOT_CONTAINERD_SOCKET,
		config.KUKEON_ROOT_LOG_LEVEL,
		config.KUKEON_ROOT_NAMESPACE_SUFFIX,
		config.KUKEON_ROOT_CGROUP_ROOT,
		config.KUKEON_ROOT_SPACE_SUBNET_CIDR,
	}
}

// clearKukeEnv unsets every env var in clientConfigEnvVars for the duration
// of the test. envSet treats an empty value as set, so t.Setenv(..., "")
// alone is not enough: it registers the restore-on-cleanup, and the
// os.Unsetenv that follows makes the var absent. Without this, an inherited
// KUKEON_HOST (kukeon cells export it) makes applyClientConfiguration skip
// the config-file layer and the layering assertions fail (#1498).
func clearKukeEnv(t *testing.T) {
	t.Helper()
	for _, v := range clientConfigEnvVars() {
		t.Setenv(v.EnvVar(), "")
		if err := os.Unsetenv(v.EnvVar()); err != nil {
			t.Fatalf("Unsetenv %s: %v", v.EnvVar(), err)
		}
	}
}

func bindKukeEnv(t *testing.T) {
	t.Helper()
	for _, v := range clientConfigEnvVars() {
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
	clearKukeEnv(t)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	spec := v1beta1.ClientConfigurationSpec{
		Host:                      "unix:///tmp/from-config.sock",
		RunPath:                   "/opt/kukeon-from-config",
		ContainerdSocket:          "/run/containerd/from-config.sock",
		LogLevel:                  "warn",
		ContainerdNamespaceSuffix: "dev.kukeon.io",
		CgroupRoot:                "/kukeon-dev",
		SpaceSubnetCIDR:           "10.89.0.0/16",
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
	if got := viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey); got != spec.ContainerdNamespaceSuffix {
		t.Errorf("ContainerdNamespaceSuffix: got %q, want %q",
			got, spec.ContainerdNamespaceSuffix)
	}
	if got := viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey); got != spec.CgroupRoot {
		t.Errorf("CgroupRoot: got %q, want %q", got, spec.CgroupRoot)
	}
	if got := viper.GetString(config.KUKEON_ROOT_SPACE_SUBNET_CIDR.ViperKey); got != spec.SpaceSubnetCIDR {
		t.Errorf("SpaceSubnetCIDR: got %q, want %q", got, spec.SpaceSubnetCIDR)
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
	clearKukeEnv(t)

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
	restoreRuntimeGlobals(t)

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
	clearKukeEnv(t)
	restoreRuntimeGlobals(t)

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
  containerdNamespaceSuffix: dev.kukeon.io
  cgroupRoot: /kukeon-dev
  spaceSubnetCIDR: 10.89.0.0/16
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
	if got := viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey); got != "dev.kukeon.io" {
		t.Errorf("ContainerdNamespaceSuffix: got %q, want %q", got, "dev.kukeon.io")
	}
	if got := viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey); got != "/kukeon-dev" {
		t.Errorf("CgroupRoot: got %q, want %q", got, "/kukeon-dev")
	}
	// consts.ConfigureRuntime must observe the configured values so
	// downstream RealmNamespace / KukeonCgroupRoot reads (in --no-daemon
	// workload paths) follow the loaded client configuration.
	if got, want := consts.RealmNamespaceSuffix, ".dev.kukeon.io"; got != want {
		t.Errorf("consts.RealmNamespaceSuffix: got %q, want %q "+
			"(loadClientConfiguration must call ConfigureRuntime)", got, want)
	}
	if got, want := consts.KukeonCgroupRoot, "/kukeon-dev"; got != want {
		t.Errorf("consts.KukeonCgroupRoot: got %q, want %q "+
			"(loadClientConfiguration must call ConfigureRuntime)", got, want)
	}
	// loadClientConfiguration must also call cni.ConfigureSubnetParentCIDR so
	// --no-daemon in-process space creation carves the configured /16 (#1079).
	if got, want := cni.NewDefaultSubnetAllocator("").ParentCIDR(), "10.89.0.0/16"; got != want {
		t.Errorf("subnet allocator ParentCIDR: got %q, want %q "+
			"(loadClientConfiguration must call ConfigureSubnetParentCIDR)", got, want)
	}
}

// TestLoadClientConfigurationHonorsKukeConfigurationEnv pins the #1325 fix:
// the dev-init mechanism points in-process client commands at the dev-profile
// config via the KUKE_CONFIGURATION *env var* (not the --configuration flag),
// so loadClientConfiguration must bind that env before reading the path. The
// sibling TestLoadClientConfigurationSeedsViper seeds the viper key directly,
// which masked the missing BindEnv: with the env var alone, the daemon-
// independent `kuke image *` / `kuke get image*` paths resolved the default
// `.kukeon.io` suffix instead of the configured `dev.kukeon.io`.
func TestLoadClientConfigurationHonorsKukeConfigurationEnv(t *testing.T) {
	t.Cleanup(viper.Reset)
	clearKukeEnv(t)
	restoreRuntimeGlobals(t)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "kuke-dev.yaml")
	content := `apiVersion: v1beta1
kind: ClientConfiguration
metadata:
  name: default
spec:
  containerdNamespaceSuffix: dev.kukeon.io
  cgroupRoot: /kukeon-dev
`
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}
	// Drive the path through the env var only — no viper.Set, no --configuration
	// flag — exactly how scripts/dev-init.sh invokes every `kuke` client command.
	t.Setenv(config.KUKE_CONFIGURATION.EnvVar(), path)

	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() error = %v", loadErr)
	}
	if got := viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey); got != "dev.kukeon.io" {
		t.Errorf("ContainerdNamespaceSuffix from KUKE_CONFIGURATION env: got %q, want %q "+
			"(loadClientConfiguration must BindEnv before reading the path)", got, "dev.kukeon.io")
	}
	// The package-level suffix is what consts.RealmNamespace appends, so the
	// in-process image controller resolves <realm>.dev.kukeon.io only when
	// ConfigureRuntime observed the env-supplied config.
	if got, want := consts.RealmNamespace("kuke-system"), "kuke-system.dev.kukeon.io"; got != want {
		t.Errorf("consts.RealmNamespace: got %q, want %q "+
			"(image verbs must resolve the env-configured suffix)", got, want)
	}
}

// TestMaybeWriteClientConfigurationDefaultSkipsCustomPath locks the guard
// that prevents tests (and operators pointing --configuration at a custom
// location) from writing to ~/.kuke/kuke.yaml on the host. The path-equality
// guard is what makes operator-supplied custom paths "opt out" of the
// default-location dump (per the issue's "Both honor --configuration to opt
// out of the default location" note).
func TestMaybeWriteClientConfigurationDefaultSkipsCustomPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	dir := t.TempDir()
	custom := filepath.Join(dir, "custom-kuke.yaml")
	maybeWriteClientConfigurationDefault(cmd, custom)

	if _, statErr := os.Stat(custom); !os.IsNotExist(statErr) {
		t.Fatalf("custom path was written; got Stat err=%v, want IsNotExist", statErr)
	}
}

// TestMaybeWriteClientConfigurationDefaultSkipsUnknownHome proves that with
// $HOME unset the first-run dump does not fire: the default path then falls
// back to a relative "tmp/.kuke/kuke.yaml", and writing it would drop a file
// into the working directory (#1550). The test chdirs into a TempDir so a
// regression writes there, not into the package source tree.
func TestMaybeWriteClientConfigurationDefaultSkipsUnknownHome(t *testing.T) {
	t.Cleanup(viper.Reset)

	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HOME", "")

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	maybeWriteClientConfigurationDefault(cmd, config.DefaultClientConfigurationFile())

	entries, err := os.ReadDir(cwd)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", cwd, err)
	}
	if len(entries) != 0 {
		t.Fatalf("working directory not empty after dump with HOME unset: %v", entries)
	}
}

// TestMaybeWriteClientConfigurationDefaultWritesAtDefaultPath proves the
// happy path: when the resolved path equals the project default, the dump
// fires and produces a parseable document. We swap $HOME to a TempDir so
// DefaultClientConfigurationFile() resolves to a sandbox path and the
// host's real ~/.kuke is untouched.
func TestMaybeWriteClientConfigurationDefaultWritesAtDefaultPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	t.Setenv("HOME", t.TempDir())
	defaultPath := config.DefaultClientConfigurationFile()
	if _, statErr := os.Stat(defaultPath); !os.IsNotExist(statErr) {
		t.Fatalf("HOME shim is leaky; default path already exists: %v", statErr)
	}

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}

	maybeWriteClientConfigurationDefault(cmd, defaultPath)

	if _, statErr := os.Stat(defaultPath); statErr != nil {
		t.Fatalf("default-path write did not fire: %v", statErr)
	}
}

// unknownHomeConfig chdirs into a TempDir, unsets HOME and writes the
// relative default client configuration (tmp/.kuke/kuke.yaml) there with a
// distinctive host, returning that relative path and the host (#1577).
func unknownHomeConfig(t *testing.T) (string, string) {
	t.Helper()
	t.Cleanup(viper.Reset)
	clearKukeEnv(t)
	restoreRuntimeGlobals(t)
	t.Setenv(config.KUKE_CONFIGURATION.EnvVar(), "")
	if err := os.Unsetenv(config.KUKE_CONFIGURATION.EnvVar()); err != nil {
		t.Fatalf("Unsetenv: %v", err)
	}
	t.Chdir(t.TempDir())
	t.Setenv("HOME", "")

	const host = "unix:///tmp/from-working-directory.sock"
	path := config.DefaultClientConfigurationFile()
	if filepath.IsAbs(path) {
		t.Fatalf("default path with HOME unset is %q, want a relative path", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	content := "apiVersion: v1beta1\nkind: ClientConfiguration\nmetadata:\n  name: default\nspec:\n  host: " + host + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	viper.Reset()
	return path, host
}

// TestLoadClientConfigurationIgnoresRelativeDefaultWithUnknownHome pins that
// with HOME unset and no operator-chosen path, a tmp/.kuke/kuke.yaml under the
// working directory is not applied as the client configuration.
func TestLoadClientConfigurationIgnoresRelativeDefaultWithUnknownHome(t *testing.T) {
	_, host := unknownHomeConfig(t)
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() error = %v", loadErr)
	}
	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got == host {
		t.Fatalf("Host = %q: the working directory's default-path file was applied", got)
	}
}

func TestLoadClientConfigurationHonorsFlagWithUnknownHome(t *testing.T) {
	path, host := unknownHomeConfig(t)
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	if setErr := cmd.PersistentFlags().Set("configuration", path); setErr != nil {
		t.Fatalf("set --configuration: %v", setErr)
	}
	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() error = %v", loadErr)
	}
	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != host {
		t.Fatalf("Host = %q, want %q from the --configuration file", got, host)
	}
}

func TestLoadClientConfigurationHonorsEnvWithUnknownHome(t *testing.T) {
	path, host := unknownHomeConfig(t)
	t.Setenv(config.KUKE_CONFIGURATION.EnvVar(), path)
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	if loadErr := loadClientConfiguration(cmd); loadErr != nil {
		t.Fatalf("loadClientConfiguration() error = %v", loadErr)
	}
	if got := viper.GetString(config.KUKEON_ROOT_HOST.ViperKey); got != host {
		t.Fatalf("Host = %q, want %q from the KUKE_CONFIGURATION file", got, host)
	}
}

// kuke refuses to start while the retired subnet env var is set, rather than
// ignoring it and carving spaces from the default subnet (#1760).
func TestLoadClientConfigurationRejectsRetiredSpaceSubnetEnv(t *testing.T) {
	t.Cleanup(viper.Reset)
	clearKukeEnv(t)
	restoreRuntimeGlobals(t)
	t.Setenv(configrename.RetiredSpaceSubnetEnv, "10.89.0.0/16")

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	viper.Set(config.KUKE_CONFIGURATION.ViperKey, filepath.Join(t.TempDir(), "absent.yaml"))

	err = loadClientConfiguration(cmd)
	if !errors.Is(err, errdefs.ErrSettingRenamed) {
		t.Fatalf("loadClientConfiguration() error = %v, want wrapping ErrSettingRenamed", err)
	}
	if !strings.Contains(err.Error(), configrename.SpaceSubnetEnv) {
		t.Errorf("loadClientConfiguration() error = %q, want the replacement %s named",
			err, configrename.SpaceSubnetEnv)
	}
}

// kuke refuses to start on a client configuration that still sets the
// retired subnet key (#1760).
func TestLoadClientConfigurationRejectsRetiredSpaceSubnetKey(t *testing.T) {
	t.Cleanup(viper.Reset)
	clearKukeEnv(t)
	restoreRuntimeGlobals(t)

	viper.Reset()
	cmd, err := NewKukeCmd()
	if err != nil {
		t.Fatalf("NewKukeCmd() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "kuke.yaml")
	content := "kind: ClientConfiguration\nspec:\n  " + configrename.RetiredSpaceSubnetKey + ": 10.89.0.0/16\n"
	if writeErr := os.WriteFile(path, []byte(content), 0o644); writeErr != nil {
		t.Fatalf("WriteFile: %v", writeErr)
	}
	viper.Set(config.KUKE_CONFIGURATION.ViperKey, path)

	err = loadClientConfiguration(cmd)
	if !errors.Is(err, errdefs.ErrSettingRenamed) {
		t.Fatalf("loadClientConfiguration() error = %v, want wrapping ErrSettingRenamed", err)
	}
	if want := configrename.SpaceSubnetKey + "; update " + path; !strings.Contains(err.Error(), want) {
		t.Errorf("loadClientConfiguration() error = %q, want it to contain %q", err, want)
	}
	if got := cni.NewDefaultSubnetAllocator("").ParentCIDR(); got != cni.DefaultSubnetParentCIDR {
		t.Errorf("subnet parent CIDR = %q after a rejected configuration, want the untouched default %q",
			got, cni.DefaultSubnetParentCIDR)
	}
}
