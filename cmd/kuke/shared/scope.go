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
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/spf13/cobra"
)

// PickLookupRealm returns the realm coordinate for a `kuke run` /
// `kuke apply` blueprint or config lookup. Precedence: --realm flag → env
// var named by kv.Key → "default". The realm always resolves to a
// non-empty value because the daemon's blueprint/config namespace is
// realm-scoped — an empty realm name has no lookup meaning.
//
// Bypasses viper deliberately. DefineKV(.., "default") on KUKE_RUN_REALM
// registers a viper.SetDefault, which trips viper.IsSet on the bound key
// in viper v1.21 (see config/env.go's DefineKVNoViperDefault commentary).
// A viper-aware fallback would silently swallow the env-var step below
// and narrow every lookup to "default" even when the operator set
// KUKE_RUN_REALM in their shell.
func PickLookupRealm(cmd *cobra.Command, kv *config.Var) string {
	if v, _ := cmd.Flags().GetString("realm"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(kv.Key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return "default"
}

// ExplicitScope returns the named space/stack coordinate for a `kuke run`
// / `kuke apply` blueprint or config lookup, but only when the operator
// set it explicitly — via the named cobra flag or the kv.Key env var.
// Returns "" otherwise so realm-scoped Blueprints/Configs (no space/stack
// coordinate) stay findable; a missing flag must not narrow the lookup to
// space="default" / stack="default" or it would hide realm-scoped
// resources.
//
// Bypasses viper for the same reason as PickLookupRealm: DefineKV's
// SetDefault on KUKE_RUN_SPACE / KUKE_RUN_STACK trips viper.IsSet, so a
// viper-aware fallback would report "default" for an unset coordinate
// even when the operator left the flag and env var empty.
func ExplicitScope(cmd *cobra.Command, flagName string, kv *config.Var) string {
	if cmd.Flags().Changed(flagName) {
		v, _ := cmd.Flags().GetString(flagName)
		return strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(kv.Key); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
