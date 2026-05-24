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

// Package cellconfig holds the identity primitives for a daemon-stored
// CellConfig (kind: CellConfig, issue #624, phase 4b-i of #423): the
// back-reference label a materialized cell carries, the deterministic
// stable-name derivation, and the slot-fill validation that checks a Config's
// repo/secret fills against the referenced blueprint's declared structural
// slots. It is the config analog of the cellblueprint package's materialization
// helpers; the runtime state machine that consumes these primitives (and the
// `kuke run -c` verb that drives it) lands in #625.
package cellconfig

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// LabelConfig is the cell label recording the CellConfig a cell was
// materialized from, the config analog of cellprofile.LabelProfile and
// cellblueprint.LabelBlueprint. Set on the cell that `kuke run -c` materializes
// (#625) so the state machine can find the at-most-one live cell a Config owns
// (`kuke get cells -l kukeon.io/config=<name>`).
const LabelConfig = "kukeon.io/config"

// AnnotationSourceConfig is the metadata.annotations key a `kuke run <src>
// --clone` (#839) sets on the clone Config it forks from <src>. It is the
// lineage marker for the counter allocator (which scans clones of a given
// source to pick the lowest-unused N) and the filter key for
// `kuke get cellconfigs --annotation kukeon.io/source-config=<src>` (a future
// list filter — the bare `kuke get cellconfigs` already surfaces the
// annotations on the wire).
const AnnotationSourceConfig = "kukeon.io/source-config"

// StableName derives the deterministic name of the cell a CellConfig
// materializes from the config's metadata.name.
//
// Decision (issue #624 AC): the derivation is the config name *verbatim*, not
// `<name>-<hash-of-values>`. The CellConfig contract is "one Config → at most
// one live cell within scope" with a stable identity that survives edits to the
// config's values; #753's refuse-on-divergence behavior relies on the stable
// name so `-c` finds the same live cell across invocations whether it attaches
// (clean spec match) or refuses with a `kuke apply -c <config>` pointer. A
// value-hashed suffix would change the derived name on every value edit,
// spawning a fresh cell and orphaning the old one — defeating the idempotent
// identity that distinguishes a Config (`run -c`) from a Blueprint's
// always-fresh `<prefix>-<6hex>` (`run -b`). So the name is value-independent.
func StableName(configName string) string {
	return strings.TrimSpace(configName)
}

// ValidateSlotFill checks a CellConfig's structural slot fills against the slots
// the referenced blueprint declares (issue #624). Matching is by slot name,
// across all of the blueprint's containers:
//
//   - a repo slot is a blueprint repo with no inline url (a url'd repo is a
//     scalar-mode value, not a fillable slot);
//   - every blueprint secret slot is structural (the blueprint never carries
//     the secret source).
//
// It enforces the AC's two gates: a Config that fills a slot the blueprint does
// not declare is an error (ErrConfigUnknown{Repo,Secret}Slot), and a *required*
// slot the blueprint declares that the Config leaves unfilled is an error
// (ErrConfigRequiredSlotUnfilled). A slot is treated as required if any
// declaration of that name across the blueprint's containers is required.
func ValidateSlotFill(cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc) error {
	repoRequired := map[string]bool{}
	secretRequired := map[string]bool{}
	for _, c := range bp.Spec.Cell.Containers {
		for _, r := range c.Repos {
			if strings.TrimSpace(r.URL) != "" {
				continue // scalar-mode repo, not a fillable slot
			}
			name := strings.TrimSpace(r.Name)
			repoRequired[name] = repoRequired[name] || r.Required
		}
		for _, s := range c.Secrets {
			name := strings.TrimSpace(s.Name)
			secretRequired[name] = secretRequired[name] || s.Required
		}
	}

	for name := range cfg.Spec.Repos {
		if _, ok := repoRequired[strings.TrimSpace(name)]; !ok {
			return fmt.Errorf("%w: repo slot %q", errdefs.ErrConfigUnknownRepoSlot, name)
		}
	}
	for name := range cfg.Spec.Secrets {
		if _, ok := secretRequired[strings.TrimSpace(name)]; !ok {
			return fmt.Errorf("%w: secret slot %q", errdefs.ErrConfigUnknownSecretSlot, name)
		}
	}

	for name, required := range repoRequired {
		if required {
			if _, ok := cfg.Spec.Repos[name]; !ok {
				return fmt.Errorf("%w: repo slot %q", errdefs.ErrConfigRequiredSlotUnfilled, name)
			}
		}
	}
	for name, required := range secretRequired {
		if required {
			if _, ok := cfg.Spec.Secrets[name]; !ok {
				return fmt.Errorf("%w: secret slot %q", errdefs.ErrConfigRequiredSlotUnfilled, name)
			}
		}
	}

	return nil
}
