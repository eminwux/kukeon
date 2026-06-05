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
	"fmt"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/serverconfig"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// ServerConfigurationFlag is the canonical flag name every admin verb that
// targets a specific kukeond instance accepts (kuke init,
// kuke daemon {start,stop,kill,reset,restart}, kuke uninstall). Centralised
// so the helpers in this file and the verbs that use them cannot drift on
// the literal flag name.
const ServerConfigurationFlag = "server-configuration"

// RegisterServerConfigurationFlag declares --server-configuration on cmd
// without binding it to viper. Binding is intentionally deferred to
// LoadServerConfigurationFromFlag (called from each verb's RunE) because
// viper.BindPFlag is last-bind-wins (same reason RegisterNoDaemonFlag
// defers binding to PreRun, see cmd/kuke/kuke.go's rebindNoDaemonViperToLeaf):
// the command tree registers every admin verb's copy of the flag at setup
// time, so binding eagerly would leave viper pointing at whichever
// subcommand happened to register last.
//
// Issue #284 collapsed KUKE_INIT_SERVER_CONFIGURATION into
// KUKEOND_CONFIGURATION — one env var, one source of truth — so the flag
// reads its default from KUKEOND_CONFIGURATION.Default.
func RegisterServerConfigurationFlag(cmd *cobra.Command) {
	cmd.Flags().String(
		ServerConfigurationFlag, config.KUKEOND_CONFIGURATION.Default,
		"Path to a ServerConfiguration YAML targeting a specific kukeond instance; "+
			"absent file uses hardcoded defaults",
	)
}

// LoadServerConfigurationFromFlag resolves the ServerConfiguration document
// the admin verb should target, following the precedence chain documented
// on issue #284:
//
//  1. --server-configuration <path> (explicit override)
//  2. KUKEOND_CONFIGURATION env var
//  3. /etc/kukeon/kukeond.yaml (default file)
//  4. Hardcoded defaults (file absent → zero-value document, callers fall
//     through to their existing default-handling).
//
// The same chain kukeond itself uses (cmd/kukeond/kukeond.go) so a
// `--server-configuration <path>` on any admin command points at the same
// document the daemon honors.
//
// The loaded spec is layered onto viper for the common admin keys
// (ApplyServerConfigurationCommonFields) and consts.ConfigureRuntime is
// invoked so downstream RealmNamespace/IsKukeonNamespace observe the
// configured suffix. Returning the resolved path lets callers pass it down
// (e.g. kuke init's controller.Options.KukeondConfiguration) without
// re-resolving.
func LoadServerConfigurationFromFlag(cmd *cobra.Command) (v1beta1.ServerConfigurationSpec, string, error) {
	// Re-bind to the leaf cmd's flag so viper reads from this command's
	// instance rather than a peer admin verb's. viper.BindPFlag is
	// last-bind-wins; same shape as rebindNoDaemonViperToLeaf in kuke.go.
	if f := cmd.Flags().Lookup(ServerConfigurationFlag); f != nil {
		_ = viper.BindPFlag(config.KUKEOND_CONFIGURATION.ViperKey, f)
	}
	_ = config.KUKEOND_CONFIGURATION.BindEnv()

	path := viper.GetString(config.KUKEOND_CONFIGURATION.ViperKey)
	if path == "" {
		path = config.DefaultServerConfigurationFile()
	}
	doc, err := serverconfig.Load(path)
	if err != nil {
		return v1beta1.ServerConfigurationSpec{}, path, fmt.Errorf("load server configuration: %w", err)
	}
	ApplyServerConfigurationCommonFields(cmd, doc.Spec)
	// Fall back to the in-binary defaults when viper has neither a
	// SetDefault (e.g. unit tests that call viper.Reset before invoking the
	// loader) nor an env / flag / YAML override — consts.ConfigureRuntime
	// rejects empty values, and a stricter contract here would force every
	// caller to know which package-init defaults to re-seed.
	suffix := viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey)
	if suffix == "" {
		suffix = config.KUKEON_ROOT_NAMESPACE_SUFFIX.Default
	}
	cgroupRoot := viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey)
	if cgroupRoot == "" {
		cgroupRoot = config.KUKEON_ROOT_CGROUP_ROOT.Default
	}
	if cfgErr := consts.ConfigureRuntime(suffix, cgroupRoot); cfgErr != nil {
		return doc.Spec, path, fmt.Errorf("configure runtime: %w", cfgErr)
	}
	podSubnetCIDR := viper.GetString(config.KUKEON_ROOT_POD_SUBNET_CIDR.ViperKey)
	if podSubnetCIDR == "" {
		podSubnetCIDR = config.KUKEON_ROOT_POD_SUBNET_CIDR.Default
	}
	if cfgErr := cni.ConfigureSubnetParentCIDR(podSubnetCIDR); cfgErr != nil {
		return doc.Spec, path, fmt.Errorf("configure pod subnet CIDR: %w", cfgErr)
	}
	return doc.Spec, path, nil
}

// ApplyServerConfigurationCommonFields layers ServerConfiguration fields
// onto viper for the keys every admin verb that targets a specific kukeond
// instance reads:
//
//   - socket (KUKEOND_SOCKET)
//   - runPath (KUKEON_RUN_PATH)
//   - containerdSocket (KUKEON_CONTAINERD_SOCKET)
//   - logLevel (KUKEON_LOG_LEVEL)
//   - containerdNamespaceSuffix (KUKEON_NAMESPACE_SUFFIX)
//   - cgroupRoot (KUKEON_CGROUP_ROOT)
//   - podSubnetCIDR (KUKEON_POD_SUBNET_CIDR)
//
// Precedence order: explicit `--flag` > env > ServerConfiguration > flag
// default. The flag check skips fields whose `--flag` was changed; the env
// check skips fields whose env var is set — without it, viper.Set would
// override viper's env binding and silently invert env > YAML. Mirrors the
// daemon-side helper in cmd/kukeond/kukeond.go.
//
// Daemon-only fields (socketGID, reconcileInterval, defaultMemoryLimitBytes,
// kukettyLogLevel) and init-only fields (kukeondImage) are left to the
// caller — only admin-client-relevant fields land here.
func ApplyServerConfigurationCommonFields(cmd *cobra.Command, spec v1beta1.ServerConfigurationSpec) {
	if spec.Socket != "" && !flagChangedAny(cmd, "socket") && !envIsSet(config.KUKEOND_SOCKET) {
		viper.Set(config.KUKEOND_SOCKET.ViperKey, spec.Socket)
	}
	if spec.RunPath != "" && !flagChangedAny(cmd, "run-path") && !envIsSet(config.KUKEON_ROOT_RUN_PATH) {
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, spec.RunPath)
	}
	if spec.ContainerdSocket != "" && !flagChangedAny(cmd, "containerd-socket") &&
		!envIsSet(config.KUKEON_ROOT_CONTAINERD_SOCKET) {
		viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, spec.ContainerdSocket)
	}
	if spec.LogLevel != "" && !flagChangedAny(cmd, "log-level") && !envIsSet(config.KUKEON_ROOT_LOG_LEVEL) {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, spec.LogLevel)
	}
	if spec.ContainerdNamespaceSuffix != "" &&
		!flagChangedAny(cmd, "containerd-namespace-suffix") &&
		!envIsSet(config.KUKEON_ROOT_NAMESPACE_SUFFIX) {
		viper.Set(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey, spec.ContainerdNamespaceSuffix)
	}
	if spec.CgroupRoot != "" &&
		!flagChangedAny(cmd, "cgroup-root") &&
		!envIsSet(config.KUKEON_ROOT_CGROUP_ROOT) {
		viper.Set(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey, spec.CgroupRoot)
	}
	if spec.PodSubnetCIDR != "" &&
		!flagChangedAny(cmd, "pod-subnet-cidr") &&
		!envIsSet(config.KUKEON_ROOT_POD_SUBNET_CIDR) {
		viper.Set(config.KUKEON_ROOT_POD_SUBNET_CIDR.ViperKey, spec.PodSubnetCIDR)
	}
}

// flagChangedAny mirrors the same-named helpers in cmd/kuke/kuke.go and
// cmd/kukeond/kukeond.go: it checks both the local and persistent flag sets
// so the helper is correct in unit tests (where cmd is built bare and
// persistent flags are not yet merged into cmd.Flags()) and in production
// (where cmd is the leaf subcommand and the merged set already contains
// the parent's persistent flags).
func flagChangedAny(cmd *cobra.Command, name string) bool {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return true
	}
	if f := cmd.PersistentFlags().Lookup(name); f != nil && f.Changed {
		return true
	}
	return false
}

// envIsSet reports whether the OS env var backing v is present (any value,
// including empty string, counts as set — same semantics as viper's BindEnv).
// Local copy of the same predicate used in cmd/kuke/kuke.go and
// cmd/kukeond/kukeond.go so this package does not have to take a dependency
// on either to read it.
func envIsSet(v config.Var) bool {
	_, ok := os.LookupEnv(v.EnvVar())
	return ok
}
