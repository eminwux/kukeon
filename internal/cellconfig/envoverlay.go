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

package cellconfig

import (
	"strings"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ApplyEnvOverrides bakes the validated `--env KEY=VALUE` per-cell overrides
// into the materialised CellDoc (issue #1023). Two effects, mirroring the
// `kuke run --env` provenance contract (#1021) but persisting rather than
// riding the transport-only Spec.RuntimeEnv:
//
//   - the overrides are merged into the attachable container's persisted Env
//     (ResolveAttachableContainerIndex picks the target the same way
//     `kuke run` would attach), winning over any value the Config's
//     spec.values resolved for the same key — so the override survives the
//     stopped-cell persist and takes effect on the later `kuke start`;
//   - the same entries are recorded verbatim in Spec.Provenance.EnvOverrides,
//     the P1 materialization-input record P5 re-resolves against.
//
// A nil/empty envArgs is a no-op. When no container is attachable the
// overrides are still recorded in provenance (the operator's intent is
// preserved) but have nowhere to bake, mirroring the runtime path's
// silent no-op on a non-attachable cell.
//
// Shared by `kuke create cell --env`, the clone path, and the reconcile
// re-resolve path (epic:cell-identity P5, #1024) so all three re-apply the
// overlay through one precedence-identical helper.
func ApplyEnvOverrides(doc *v1beta1.CellDoc, envArgs []string) {
	if len(envArgs) == 0 {
		return
	}
	if idx := ResolveAttachableContainerIndex(doc.Spec); idx >= 0 {
		doc.Spec.Containers[idx].Env = MergeEnv(doc.Spec.Containers[idx].Env, envArgs)
	}
	if doc.Spec.Provenance != nil {
		doc.Spec.Provenance.EnvOverrides = append([]string(nil), envArgs...)
	}
}

// ResolveAttachableContainerIndex returns the index of the container `kuke run`
// would attach to (issue #834's runtime-env target), or -1 when none qualifies.
// Precedence mirrors the daemon-side resolveAttachableContainerID and the
// CLI-side pickAttachTarget:
//
//  1. Spec.Tty.Default, when it names an existing non-root container;
//  2. the first non-root container with Attachable=true, in declaration order;
//  3. -1 when no container qualifies.
func ResolveAttachableContainerIndex(spec v1beta1.CellSpec) int {
	if spec.Tty != nil {
		if pref := strings.TrimSpace(spec.Tty.Default); pref != "" {
			for i, c := range spec.Containers {
				if !c.Root && c.ID == pref {
					return i
				}
			}
		}
	}
	for i, c := range spec.Containers {
		if !c.Root && c.Attachable {
			return i
		}
	}
	return -1
}

// MergeEnv layers the validated env overrides on top of a container's existing
// Env. For each KEY in envArgs, any specEnv entry with the same KEY is dropped
// (the override wins); surviving spec entries keep their order, then the
// override entries follow in their input order. Mirrors the runner-side
// mergeRuntimeEnv merge semantics so a `create cell --env` override and a
// `run --env` override resolve identically. Never mutates the input slices.
func MergeEnv(specEnv, envArgs []string) []string {
	if len(envArgs) == 0 {
		return specEnv
	}
	overrideKeys := make(map[string]struct{}, len(envArgs))
	for _, entry := range envArgs {
		key, _, _ := strings.Cut(entry, "=")
		overrideKeys[key] = struct{}{}
	}
	// Cap on len(specEnv) alone — not len(specEnv)+len(envArgs) — to
	// keep CodeQL's go/allocation-size-overflow analysis silent on
	// operator-tainted inputs. The envArgs tail is appended below and
	// Go's append handles the tiny extra growth fine for typical kuke
	// cells (<50 entries on either side). Mirrors the runner-side
	// mergeRuntimeEnv cap in internal/controller/runner/cell_runtime_env.go.
	merged := make([]string, 0, len(specEnv))
	for _, entry := range specEnv {
		key, _, _ := strings.Cut(entry, "=")
		if _, override := overrideKeys[key]; override {
			continue
		}
		merged = append(merged, entry)
	}
	return append(merged, envArgs...)
}
