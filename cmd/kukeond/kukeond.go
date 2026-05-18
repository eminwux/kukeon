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

// Package kukeond is the cobra entry point for the kukeond daemon binary.
// It is dispatched from cmd/main.go by argv[0] basename.
package kukeond

import (
	"fmt"
	"os"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/serverconfig"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// NewKukeondCmd returns the root cobra command for the kukeond daemon.
func NewKukeondCmd() (*cobra.Command, error) {
	cmd := &cobra.Command{
		Use:           "kukeond",
		Short:         "Kukeon daemon: hosts the kukeonv1 API over a unix socket",
		SilenceUsage:  true,
		SilenceErrors: false,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			path := viper.GetString(config.KUKEOND_CONFIGURATION.ViperKey)
			if path == "" {
				path = config.DefaultServerConfigurationFile()
			}
			// First-run dump: when the operator did not pick a custom
			// --configuration, write the commented defaults to the well-known
			// path so a fresh host has a template to edit. Skipped silently
			// on permission/IO failures — the daemon must still start with
			// hardcoded defaults.
			maybeWriteServerConfigurationDefault(cmd, path)
			doc, err := serverconfig.Load(path)
			if err != nil {
				return fmt.Errorf("load server configuration: %w", err)
			}
			applyServerConfiguration(cmd, doc.Spec)
			if cfgErr := consts.ConfigureRuntime(
				viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey),
				viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey),
			); cfgErr != nil {
				return fmt.Errorf("configure runtime: %w", cfgErr)
			}
			return nil
		},
	}

	cmd.PersistentFlags().String(
		"configuration", config.KUKEOND_CONFIGURATION.Default,
		"Path to a ServerConfiguration YAML; absent file uses hardcoded defaults",
	)
	if err := viper.BindPFlag(
		config.KUKEOND_CONFIGURATION.ViperKey,
		cmd.PersistentFlags().Lookup("configuration"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"run-path", config.DefaultRunPath(),
		"Run path for the kukeon runtime",
	)
	if err := viper.BindPFlag(
		config.KUKEON_ROOT_RUN_PATH.ViperKey,
		cmd.PersistentFlags().Lookup("run-path"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"containerd-socket", "/run/containerd/containerd.sock",
		"Path to the containerd socket",
	)
	if err := viper.BindPFlag(
		config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey,
		cmd.PersistentFlags().Lookup("containerd-socket"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"socket", config.KUKEOND_SOCKET.Default,
		"Unix socket path the daemon listens on",
	)
	if err := viper.BindPFlag(
		config.KUKEOND_SOCKET.ViperKey,
		cmd.PersistentFlags().Lookup("socket"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().Int(
		"socket-gid", 0,
		"Group ID to chown the listener socket to (mode 0o660 with group). "+
			"Set by `kuke init` to the kukeon GID so non-root group members "+
			"can dial the daemon after a kukeond restart.",
	)
	if err := viper.BindPFlag(
		config.KUKEOND_SOCKET_GID.ViperKey,
		cmd.PersistentFlags().Lookup("socket-gid"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"log-level", "info",
		"Log level (debug, info, warn, error)",
	)
	if err := viper.BindPFlag(
		config.KUKEON_ROOT_LOG_LEVEL.ViperKey,
		cmd.PersistentFlags().Lookup("log-level"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"reconcile-interval", config.KUKEOND_RECONCILE_INTERVAL.Default,
		"Period of the cell-reconciliation loop (Go duration; 0 disables)",
	)
	if err := viper.BindPFlag(
		config.KUKEOND_RECONCILE_INTERVAL.ViperKey,
		cmd.PersistentFlags().Lookup("reconcile-interval"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"containerd-namespace-suffix", config.KUKEON_ROOT_NAMESPACE_SUFFIX.Default,
		"Suffix appended to every realm name to form its containerd namespace "+
			"(e.g. \"kukeon.io\" -> \"default.kukeon.io\")",
	)
	if err := viper.BindPFlag(
		config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey,
		cmd.PersistentFlags().Lookup("containerd-namespace-suffix"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().String(
		"cgroup-root", config.KUKEON_ROOT_CGROUP_ROOT.Default,
		"Cgroup root under which all realms / spaces / stacks / cells live",
	)
	if err := viper.BindPFlag(
		config.KUKEON_ROOT_CGROUP_ROOT.ViperKey,
		cmd.PersistentFlags().Lookup("cgroup-root"),
	); err != nil {
		return nil, err
	}

	cmd.PersistentFlags().Int64(
		"default-memory-limit-bytes", 0,
		"Daemon-wide fallback memory cap (bytes) for any admitted container "+
			"whose Resources.MemoryLimitBytes is unset. 0 disables; an explicit "+
			"per-container limit always wins. Recommended on hosts without swap "+
			"and without a userspace OOM guard. (issue #531)",
	)
	if err := viper.BindPFlag(
		config.KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES.ViperKey,
		cmd.PersistentFlags().Lookup("default-memory-limit-bytes"),
	); err != nil {
		return nil, err
	}

	bindEnvVars()

	cmd.AddCommand(newServeCmd())
	return cmd, nil
}

// bindEnvVars wires each kukeond viper key to its `KUKEON_…` / `KUKEOND_…`
// environment variable. Without these calls, applyServerConfiguration's
// envSet check skips the YAML write but viper has no env binding to read
// from — so both env and YAML are silently ignored and the flag default
// wins. Mirrors the BindEnv block in cmd/kuke/kuke.go's loadConfig.
func bindEnvVars() {
	for _, v := range []config.Var{
		config.KUKEON_ROOT_CONTAINERD_SOCKET,
		config.KUKEON_ROOT_RUN_PATH,
		config.KUKEON_ROOT_LOG_LEVEL,
		config.KUKEON_ROOT_NAMESPACE_SUFFIX,
		config.KUKEON_ROOT_CGROUP_ROOT,
		config.KUKEOND_SOCKET,
		config.KUKEOND_SOCKET_GID,
		config.KUKEOND_RECONCILE_INTERVAL,
		config.KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES,
	} {
		_ = v.BindEnv()
	}
}

// applyServerConfiguration layers the loaded ServerConfiguration on top of
// viper for fields the operator did not explicitly set on the command line
// or via environment. Order of precedence: explicit `--flag` > env >
// ServerConfiguration > flag default. The flag check skips fields whose
// `--flag` was changed; the env check skips fields whose env var is set —
// without it, `viper.Set` would override viper's env binding and silently
// invert env > YAML.
func applyServerConfiguration(cmd *cobra.Command, spec v1beta1.ServerConfigurationSpec) {
	if spec.Socket != "" && !flagChanged(cmd, "socket") && !envSet(config.KUKEOND_SOCKET) {
		viper.Set(config.KUKEOND_SOCKET.ViperKey, spec.Socket)
	}
	if spec.SocketGID != 0 && !flagChanged(cmd, "socket-gid") && !envSet(config.KUKEOND_SOCKET_GID) {
		viper.Set(config.KUKEOND_SOCKET_GID.ViperKey, spec.SocketGID)
	}
	if spec.RunPath != "" && !flagChanged(cmd, "run-path") && !envSet(config.KUKEON_ROOT_RUN_PATH) {
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, spec.RunPath)
	}
	if spec.ContainerdSocket != "" && !flagChanged(cmd, "containerd-socket") &&
		!envSet(config.KUKEON_ROOT_CONTAINERD_SOCKET) {
		viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, spec.ContainerdSocket)
	}
	if spec.LogLevel != "" && !flagChanged(cmd, "log-level") && !envSet(config.KUKEON_ROOT_LOG_LEVEL) {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, spec.LogLevel)
	}
	if spec.ReconcileInterval != "" &&
		!flagChanged(cmd, "reconcile-interval") &&
		!envSet(config.KUKEOND_RECONCILE_INTERVAL) {
		viper.Set(config.KUKEOND_RECONCILE_INTERVAL.ViperKey, spec.ReconcileInterval)
	}
	if spec.ContainerdNamespaceSuffix != "" &&
		!flagChanged(cmd, "containerd-namespace-suffix") &&
		!envSet(config.KUKEON_ROOT_NAMESPACE_SUFFIX) {
		viper.Set(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey, spec.ContainerdNamespaceSuffix)
	}
	if spec.CgroupRoot != "" &&
		!flagChanged(cmd, "cgroup-root") &&
		!envSet(config.KUKEON_ROOT_CGROUP_ROOT) {
		viper.Set(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey, spec.CgroupRoot)
	}
	if spec.DefaultMemoryLimitBytes > 0 &&
		!flagChanged(cmd, "default-memory-limit-bytes") &&
		!envSet(config.KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES) {
		viper.Set(config.KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES.ViperKey, spec.DefaultMemoryLimitBytes)
	}
}

// flagChanged reports whether `--<name>` was explicitly set on the command
// line. cobra invokes PersistentPreRunE on the leaf subcommand, but kukeond's
// persistent flags (socket, run-path, etc.) are defined on the root. On the
// leaf, cmd.PersistentFlags() returns only the leaf's local persistent flag
// set — it does not include parents' persistent flags — so a direct
// `cmd.PersistentFlags().Changed("socket")` lookup misses and returns false
// even when the operator passed `--socket`. cobra merges parent persistent
// flags into the leaf's cmd.Flags() before PersistentPreRunE runs, so the
// merged set is the production-correct read; the local PersistentFlags
// fallback keeps the helper correct in unit tests that pass the root cmd
// directly without going through Execute. Mirrors the same-named helper in
// cmd/kuke/kuke.go.
func flagChanged(cmd *cobra.Command, name string) bool {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return true
	}
	if f := cmd.PersistentFlags().Lookup(name); f != nil && f.Changed {
		return true
	}
	return false
}

// envSet reports whether the OS env var backing v is present (any value,
// including empty string, counts as set — same semantics as viper's BindEnv).
func envSet(v config.Var) bool {
	_, ok := os.LookupEnv(v.EnvVar())
	return ok
}

// maybeWriteServerConfigurationDefault writes the commented default
// ServerConfiguration when the resolved --configuration path equals the
// project default. The path-equality guard is the only filter: pointing
// --configuration at a custom path opts out of the default-location dump
// (the path won't equal the project default), while pointing it at the
// default path is a no-op overwrite-guarded by O_EXCL inside WriteDefault.
// This shape lets `kuke init` — which always plumbs --configuration
// /etc/kukeon/kukeond.yaml into the kukeond cell — still produce a fresh
// host's template on first boot. Errors are intentionally swallowed: a
// read-only /etc or unwritable parent dir must not block the daemon from
// starting on the in-binary defaults.
//
// The spec we hand to WriteDefault is read back from viper at write time so
// the dumped document reflects the values the daemon actually bound to —
// flag / env overrides included — instead of a hardcoded snapshot of the
// compile-time defaults (issue #581). The YAML has not been loaded yet on
// the first-write path, so viper holds exactly the flag-and-env-resolved
// state we want to record.
func maybeWriteServerConfigurationDefault(_ *cobra.Command, path string) {
	if path != config.DefaultServerConfigurationFile() {
		return
	}
	_, _ = serverconfig.WriteDefault(path, currentResolvedSpec())
}

// currentResolvedSpec snapshots the post-flag, post-env viper state into a
// ServerConfigurationSpec for serverconfig.WriteDefault. The KukeondImage
// field is intentionally left empty — the daemon has no --kukeond-image
// flag (it's a `kuke init` concern) and the commented default explains
// that `kuke init` resolves the value at runtime.
func currentResolvedSpec() v1beta1.ServerConfigurationSpec {
	return v1beta1.ServerConfigurationSpec{
		Socket:                    viper.GetString(config.KUKEOND_SOCKET.ViperKey),
		SocketGID:                 viper.GetInt(config.KUKEOND_SOCKET_GID.ViperKey),
		RunPath:                   viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey),
		ContainerdSocket:          viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey),
		LogLevel:                  viper.GetString(config.KUKEON_ROOT_LOG_LEVEL.ViperKey),
		ReconcileInterval:         viper.GetString(config.KUKEOND_RECONCILE_INTERVAL.ViperKey),
		ContainerdNamespaceSuffix: viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey),
		CgroupRoot:                viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey),
		DefaultMemoryLimitBytes:   viper.GetInt64(config.KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES.ViperKey),
	}
}
