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

package runner

import (
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// resolveAttachableContainerID returns the ID of the container that `kuke run`
// would attach to (issue #834's runtime-env target). Precedence mirrors the
// CLI-side pickAttachTarget contract in cmd/kuke/run/attach.go:
//
//  1. cell.Spec.Tty.Default, if set and naming an existing non-root
//     container.
//  2. The first non-root container with Attachable=true, in declaration
//     order.
//  3. "" when no container in the cell qualifies — the caller treats this
//     as a no-op (runtime env entries are dropped rather than misdirected
//     into the root or some unrelated container).
//
// Server-side rather than CLI-passed because the CLI may invoke `kuke run`
// with -d/--detach where pickAttachTarget never runs, and because the
// `kuke run <cfg> --name X --env` idempotent-attach path threads a cell-
// lookup doc with no containers populated — the daemon must rediscover
// the attachable container against the persisted spec it reads from disk.
func resolveAttachableContainerID(cell intmodel.Cell) string {
	if cell.Spec.Tty != nil {
		if pref := strings.TrimSpace(cell.Spec.Tty.Default); pref != "" {
			for _, c := range cell.Spec.Containers {
				if c.Root {
					continue
				}
				if c.ID == pref {
					return c.ID
				}
			}
		}
	}
	for _, c := range cell.Spec.Containers {
		if c.Root {
			continue
		}
		if c.Attachable {
			return c.ID
		}
	}
	return ""
}

// mergeRuntimeEnvForContainer returns a ContainerSpec whose Env is the
// merge of spec.Env and runtimeEnv (issue #834). If containerID does not
// match attachableID or runtimeEnv is empty, the original spec is returned
// unchanged so the caller can pass it straight through to BuildContainerSpec
// without copying.
//
// Merge rules:
//
//   - runtimeEnv entries are KEY=VALUE strings produced by parseEnvArgs
//     in cmd/kuke/run (deduplicated, validated format).
//   - For each KEY in runtimeEnv, any entry in spec.Env with the same KEY
//     is dropped from the result. The runtimeEnv value wins
//     (--env LABEL=bug overrides spec env LABEL=…).
//   - Order: surviving spec entries first (in their original order),
//     then runtimeEnv entries (in their original order). Stable output
//     keeps OCI process env reproducible across runs with the same flag
//     set and lets ctr.kukeonContainerEnv apply its existing KUKEON_*
//     defaults-on-collision rule against a deterministic list.
//
// Mutation contract: never mutates the input slices or struct. The caller
// holds a containerSpec value; if the merge fires, it receives a copy with
// a fresh Env slice (so a later mutation of the caller's slice cannot
// reach through to the OCI build). When the merge does not fire (empty
// runtimeEnv or non-attachable container), the caller's value is returned
// as-is — no allocation, no risk of accidental sharing through Env.
func mergeRuntimeEnvForContainer(
	spec intmodel.ContainerSpec,
	attachableID string,
	runtimeEnv []string,
) intmodel.ContainerSpec {
	if len(runtimeEnv) == 0 {
		return spec
	}
	if attachableID == "" || spec.ID != attachableID {
		return spec
	}
	out := spec
	out.Env = mergeRuntimeEnv(spec.Env, runtimeEnv)
	return out
}

// mergeRuntimeEnv implements the env-list merge documented on
// mergeRuntimeEnvForContainer. Exposed at package scope (lowercase) so the
// tests in cell_runtime_env_test.go can pin the merge rules independently of
// the attachable-container resolution.
func mergeRuntimeEnv(specEnv, runtimeEnv []string) []string {
	if len(runtimeEnv) == 0 {
		return specEnv
	}
	runtimeKeys := make(map[string]struct{}, len(runtimeEnv))
	for _, entry := range runtimeEnv {
		key, _, _ := strings.Cut(entry, "=")
		runtimeKeys[key] = struct{}{}
	}
	// Cap on len(specEnv) alone — not len(specEnv)+len(runtimeEnv) — to
	// keep CodeQL's go/allocation-size-overflow analysis silent on
	// operator-tainted inputs. The runtimeEnv tail is appended below and
	// Go's append handles the tiny extra growth fine for typical kuke
	// cells (<50 entries on either side).
	merged := make([]string, 0, len(specEnv))
	for _, entry := range specEnv {
		key, _, _ := strings.Cut(entry, "=")
		if _, override := runtimeKeys[key]; override {
			continue
		}
		merged = append(merged, entry)
	}
	merged = append(merged, runtimeEnv...)
	return merged
}
