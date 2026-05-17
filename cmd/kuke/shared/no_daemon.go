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

import "github.com/spf13/cobra"

// noDaemonFlagUsage is the usage string for the `--no-daemon` flag, kept in
// one place so every retained registration stays byte-identical.
const noDaemonFlagUsage = "bypass kukeond and run operations in-process (requires privileges)"

// RegisterNoDaemonFlag registers `--no-daemon` as a local flag on cmd.
//
// `--no-daemon` used to be a root-persistent flag inherited by every
// subcommand. #222 demoted it to a per-command opt-in: only the commands that
// still accept it user-facing (kuke init, kuke uninstall, and — via the
// persistent variant below — kuke purge and every kuke get <kind>) call this
// helper or its persistent sibling. Daemon-routed workload commands (apply,
// create, run, attach, delete, kill, start, stop, log, refresh) no longer
// accept the flag at the CLI surface; they still reach the in-process branch
// via the `--run-path` promotion in applyRunPathImpliesNoDaemon and via
// `KUKEON_NO_DAEMON=true`.
//
// Bind to viper at PreRun time, not here — see kuke.go's
// rebindNoDaemonViperToLeaf for the reason (viper.BindPFlag is
// last-bind-wins, so binding during setup would have one command's flag
// silently drive viper for every other command).
func RegisterNoDaemonFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("no-daemon", false, noDaemonFlagUsage)
}

// RegisterNoDaemonPersistentFlag registers `--no-daemon` as a persistent
// flag on cmd. Used by `kuke purge` and `kuke get`, where the flag must
// propagate to every resource subcommand (realm/space/stack/cell/container)
// without each having to call RegisterNoDaemonFlag individually.
func RegisterNoDaemonPersistentFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("no-daemon", false, noDaemonFlagUsage)
}
