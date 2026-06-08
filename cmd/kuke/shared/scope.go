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
// / `kuke apply` / `kuke create cell` blueprint or config lookup.
// Precedence: an explicit value wins — the named cobra flag (--space /
// --stack), else the kv.Key env var — and an explicitly-empty value is
// honored as empty. When neither flag nor env is set at all, the lookup
// falls back to kv.Default (operator-supplied default, "default" for the
// KUKE_{RUN,CREATE_CELL}_{SPACE,STACK} vars) so a no-flag lookup resolves
// to the full default scope (realm/space/stack = "default") and finds
// resources stored at that coordinate — how `kuke create config/blueprint`
// and the team renderer actually store them (issue #1156). A realm-scoped
// Blueprint/Config (no space/stack coordinate) stays findable by passing
// an explicit empty `--space "" --stack ""`, which the explicit branch
// honors.
//
// Bypasses viper for the same reason as PickLookupRealm: DefineKV's
// SetDefault on KUKE_RUN_SPACE / KUKE_RUN_STACK trips viper.IsSet, so a
// viper-aware fallback could not distinguish an operator-pinned coordinate
// from the registered default. Reading kv.Default directly keeps the
// explicit-empty escape hatch intact.
func ExplicitScope(cmd *cobra.Command, flagName string, kv *config.Var) string {
	if cmd.Flags().Changed(flagName) {
		v, _ := cmd.Flags().GetString(flagName)
		return strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(kv.Key); ok {
		return strings.TrimSpace(v)
	}
	return kv.Default
}
