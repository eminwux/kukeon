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

package run

import (
	"fmt"
	"strings"
)

// parseEnvArgs validates the repeatable `--env KEY=VALUE` flag (issue #834)
// and returns the normalized entries that runRun threads onto
// cellDoc.Spec.RuntimeEnv. The rules:
//
//   - Each entry must contain `=`. Missing `=` is rejected with a
//     "got: <input>" pointer so the operator can see exactly what was
//     supplied.
//   - The KEY (substring before the first `=`) must be non-empty after
//     trimming; an empty KEY (`=VALUE` or `=`) is rejected.
//   - The VALUE (substring after the first `=`) is preserved verbatim,
//     including empty (`KEY=`) — empty values are explicitly allowed per
//     the AC.
//   - If the same KEY appears twice with the same VALUE the duplicate is
//     a no-op (deduplicated to a single entry). If the same KEY appears
//     twice with DIFFERENT values the input is rejected — the operator
//     must pick one. "Last wins" silently is rejected explicitly per the
//     AC's "don't silently take last wins; explicit is better".
//
// The order of the returned slice mirrors the first occurrence of each
// KEY in the input. Stability matters because the runner's merge
// preserves the runtime-env entries verbatim (append after the spec
// env's surviving entries), so a deterministic order keeps the OCI env
// reproducible for test assertions.
func parseEnvArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	seen := make(map[string]string, len(args))
	out := make([]string, 0, len(args))
	for _, raw := range args {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("--env requires KEY=VALUE (got: %q)", raw)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("--env KEY must be non-empty (got: %q)", raw)
		}
		entry := key + "=" + value
		if prior, dup := seen[key]; dup {
			if prior != value {
				return nil, fmt.Errorf(
					"--env %s supplied twice with different values (%q vs %q); pick one",
					key, prior, value,
				)
			}
			// Same KEY, same VALUE: harmless duplicate, skip.
			continue
		}
		seen[key] = value
		out = append(out, entry)
	}
	return out, nil
}
