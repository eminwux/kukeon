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
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TestLoadServerConfigurationFromFlag_PrecedenceChain pins the issue #284
// precedence rule for the shared admin loader: flag > KUKEOND_CONFIGURATION
// env > default file. Each case exercises one branch of the chain and
// asserts the resolved spec round-trips into viper for the admin-common
// fields (runPath here as a representative).
func TestLoadServerConfigurationFromFlag_PrecedenceChain(t *testing.T) {
	yamlBody := []byte("apiVersion: v1beta1\nkind: ServerConfiguration\nspec:\n  runPath: /opt/from-yaml\n")

	t.Run("flag wins over env and default", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		tmp := t.TempDir()
		flagYAML := filepath.Join(tmp, "flag.yaml")
		envYAML := filepath.Join(tmp, "env.yaml")
		if err := os.WriteFile(flagYAML, []byte(
			"apiVersion: v1beta1\nkind: ServerConfiguration\nspec:\n  runPath: /opt/from-flag\n",
		), 0o600); err != nil {
			t.Fatalf("write flag yaml: %v", err)
		}
		if err := os.WriteFile(envYAML, yamlBody, 0o600); err != nil {
			t.Fatalf("write env yaml: %v", err)
		}
		t.Setenv(config.KUKEOND_CONFIGURATION.EnvVar(), envYAML)

		cmd := &cobra.Command{}
		RegisterServerConfigurationFlag(cmd)
		if err := cmd.Flags().Set(ServerConfigurationFlag, flagYAML); err != nil {
			t.Fatalf("set --server-configuration: %v", err)
		}

		spec, path, err := LoadServerConfigurationFromFlag(cmd)
		if err != nil {
			t.Fatalf("LoadServerConfigurationFromFlag: %v", err)
		}
		if path != flagYAML {
			t.Errorf("resolved path = %q, want %q (flag wins)", path, flagYAML)
		}
		if spec.RunPath != "/opt/from-flag" {
			t.Errorf("spec.RunPath = %q, want /opt/from-flag", spec.RunPath)
		}
		if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/opt/from-flag" {
			t.Errorf("viper RunPath = %q, want /opt/from-flag", got)
		}
	})

	t.Run("env wins over default when no flag", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		tmp := t.TempDir()
		envYAML := filepath.Join(tmp, "env.yaml")
		if err := os.WriteFile(envYAML, yamlBody, 0o600); err != nil {
			t.Fatalf("write env yaml: %v", err)
		}
		t.Setenv(config.KUKEOND_CONFIGURATION.EnvVar(), envYAML)

		cmd := &cobra.Command{}
		RegisterServerConfigurationFlag(cmd)

		_, path, err := LoadServerConfigurationFromFlag(cmd)
		if err != nil {
			t.Fatalf("LoadServerConfigurationFromFlag: %v", err)
		}
		if path != envYAML {
			t.Errorf("resolved path = %q, want %q (env wins over default)", path, envYAML)
		}
		if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/opt/from-yaml" {
			t.Errorf("viper RunPath = %q, want /opt/from-yaml", got)
		}
	})

	t.Run("absent file falls through to defaults", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(viper.Reset)
		tmp := t.TempDir()
		missing := filepath.Join(tmp, "missing.yaml")
		t.Setenv(config.KUKEOND_CONFIGURATION.EnvVar(), missing)

		// Re-seed package-init defaults that viper.Reset wiped — without
		// these consts.ConfigureRuntime rejects empty values.
		viper.Set(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey, config.KUKEON_ROOT_NAMESPACE_SUFFIX.Default)
		viper.Set(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey, config.KUKEON_ROOT_CGROUP_ROOT.Default)

		cmd := &cobra.Command{}
		RegisterServerConfigurationFlag(cmd)

		spec, path, err := LoadServerConfigurationFromFlag(cmd)
		if err != nil {
			t.Fatalf("LoadServerConfigurationFromFlag: %v", err)
		}
		if path != missing {
			t.Errorf("resolved path = %q, want %q (env value preserved)", path, missing)
		}
		// Zero-value spec means no fields applied → viper keys stay at the
		// values seeded above (the hardcoded defaults).
		if spec.RunPath != "" {
			t.Errorf("spec.RunPath = %q, want empty (absent file)", spec.RunPath)
		}
	})
}

// TestLoadServerConfigurationFromFlag_NonDefaultSuffixIsAppliedRuntime
// pins the issue #284 cross-instance isolation rule at the admin-CLI
// layer: loading a ServerConfiguration with a non-default suffix mutates
// consts.RealmNamespaceSuffix so downstream IsKukeonNamespace observes
// the configured value. This is what stops a `kuke uninstall
// --server-configuration ./dev.yaml` from enumerating prod namespaces.
func TestLoadServerConfigurationFromFlag_NonDefaultSuffixIsAppliedRuntime(t *testing.T) {
	// Save/restore the runtime-override globals. Cannot run in parallel.
	prevSuffix := consts.RealmNamespaceSuffix
	prevRoot := consts.KukeonCgroupRoot
	t.Cleanup(func() {
		consts.RealmNamespaceSuffix = prevSuffix //nolint:reassign // restore runtime-overridable global
		consts.KukeonCgroupRoot = prevRoot       //nolint:reassign // restore runtime-overridable global
		viper.Reset()
	})
	viper.Reset()

	tmp := t.TempDir()
	yamlPath := filepath.Join(tmp, "kukeond-dev.yaml")
	yaml := []byte(
		"apiVersion: v1beta1\n" +
			"kind: ServerConfiguration\n" +
			"spec:\n" +
			"  containerdNamespaceSuffix: dev.kukeon.io\n" +
			"  cgroupRoot: /kukeon-dev\n",
	)
	if err := os.WriteFile(yamlPath, yaml, 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cmd := &cobra.Command{}
	RegisterServerConfigurationFlag(cmd)
	if err := cmd.Flags().Set(ServerConfigurationFlag, yamlPath); err != nil {
		t.Fatalf("set --server-configuration: %v", err)
	}

	if _, _, err := LoadServerConfigurationFromFlag(cmd); err != nil {
		t.Fatalf("LoadServerConfigurationFromFlag: %v", err)
	}

	if got, want := consts.RealmNamespaceSuffix, ".dev.kukeon.io"; got != want {
		t.Errorf("RealmNamespaceSuffix = %q, want %q (loader must call ConfigureRuntime)", got, want)
	}
	if got, want := consts.KukeonCgroupRoot, "/kukeon-dev"; got != want {
		t.Errorf("KukeonCgroupRoot = %q, want %q (loader must call ConfigureRuntime)", got, want)
	}

	// IsKukeonNamespace now refuses default-suffix namespaces; this is the
	// cross-instance isolation barrier the uninstall path depends on.
	if consts.IsKukeonNamespace("default.kukeon.io") {
		t.Errorf("IsKukeonNamespace(default.kukeon.io) = true under dev suffix; want false")
	}
	if !consts.IsKukeonNamespace("default.dev.kukeon.io") {
		t.Errorf("IsKukeonNamespace(default.dev.kukeon.io) = false under dev suffix; want true")
	}
}

// TestApplyServerConfigurationCommonFields_FlagWinsOverYAML locks the
// `--flag > YAML` precedence for the admin-common fields the shared helper
// owns. An explicit `--run-path` on the command line must keep overriding
// a YAML-supplied spec.RunPath; without the flag-changed check, viper.Set
// would silently invert this.
func TestApplyServerConfigurationCommonFields_FlagWinsOverYAML(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	cmd := &cobra.Command{}
	cmd.Flags().String("run-path", "/opt/kukeon-default", "")
	if err := cmd.Flags().Set("run-path", "/opt/kukeon-from-flag"); err != nil {
		t.Fatalf("set --run-path: %v", err)
	}
	if err := viper.BindPFlag(config.KUKEON_ROOT_RUN_PATH.ViperKey, cmd.Flags().Lookup("run-path")); err != nil {
		t.Fatalf("BindPFlag run-path: %v", err)
	}

	spec := v1beta1.ServerConfigurationSpec{RunPath: "/opt/kukeon-from-yaml"}
	ApplyServerConfigurationCommonFields(cmd, spec)

	if got := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey); got != "/opt/kukeon-from-flag" {
		t.Errorf("RunPath = %q, want /opt/kukeon-from-flag (flag must win over YAML)", got)
	}
}
