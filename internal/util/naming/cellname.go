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

package naming

import (
	"fmt"
	"strings"
)

// MaxCellNameAllocAttempts bounds the suffix-collision retry loop in
// AllocCellName. A DefaultCellNameSuffixBytes-wide (24-bit) suffix makes a
// single collision astronomically unlikely, so the bound is a safety net
// against a pathological run rather than an expected hot path. It is sized to
// match the retry budget of the clone-era `<src>-<N>` gap-fill allocator this
// generator supersedes (epic:cell-identity #1022).
const MaxCellNameAllocAttempts = 64

// GenerateCellName returns "<prefix>-<6hex>" with a fresh
// DefaultCellNameSuffixBytes-wide random suffix — the single generated
// cell-name shape shared by every materialization source kind
// (epic:cell-identity #1022 retires the per-kind generators: blueprint's
// inline `<prefix>-<6hex>`, the config StableName pin, and the clone
// `<src>-<N>` counter). Callers resolve prefix from Spec.Prefix with a
// metadata.name fallback (see cellconfig.Prefix / cellblueprint.Prefix).
func GenerateCellName(prefix string) (string, error) {
	suffix, err := RandomHexSuffix(DefaultCellNameSuffixBytes)
	if err != nil {
		return "", fmt.Errorf("generate cell-name suffix for prefix %q: %w", strings.TrimSpace(prefix), err)
	}
	return strings.TrimSpace(prefix) + "-" + suffix, nil
}

// AllocCellName resolves the final name for a cell about to be materialized
// (epic:cell-identity #1022):
//
//   - A non-empty explicit name is used verbatim. The caller's persist layer
//     rejects an in-scope collision — an explicitly named create is not an
//     idempotent attach.
//   - An empty explicit name yields a generated "<prefix>-<6hex>" that does not
//     collide per exists, regenerating the suffix up to MaxCellNameAllocAttempts
//     times before giving up.
//
// exists reports whether a cell of the candidate name already lives in the
// target scope; callers build it over the daemon's GetCell view. A nil exists
// disables the collision probe (single-shot generation) for pure callers with
// no client.
func AllocCellName(explicit, prefix string, exists func(string) (bool, error)) (string, error) {
	if e := strings.TrimSpace(explicit); e != "" {
		return e, nil
	}
	var last string
	for i := 0; i < MaxCellNameAllocAttempts; i++ {
		candidate, err := GenerateCellName(prefix)
		if err != nil {
			return "", err
		}
		if exists == nil {
			return candidate, nil
		}
		taken, err := exists(candidate)
		if err != nil {
			return "", err
		}
		if !taken {
			return candidate, nil
		}
		last = candidate
	}
	return "", fmt.Errorf(
		"could not allocate a free cell name for prefix %q after %d attempts (last tried %q): persistent suffix collision",
		strings.TrimSpace(prefix), MaxCellNameAllocAttempts, last,
	)
}
