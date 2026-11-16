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

	"github.com/eminwux/kukeon/internal/consts"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// managedLabelSuffix is the trailing key segment used by every
// controller-injected canonical label (`realm.kukeon.io`, `space.kukeon.io`,
// `stack.kukeon.io`, `cell.kukeon.io`). Kept in sync with the same suffix
// hardcoded in `internal/controller/apply/diff.go::filterManagedLabels`.
const managedLabelSuffix = ".kukeon.io"

// mergeManagedLabels returns the label map an Update* runner should write back
// onto the persisted resource: every key from `desired` wins, plus any
// controller-managed `*.kukeon.io` key already in `existing` that `desired`
// did not author.
//
// The create-time injection in `internal/controller/create_{realm,space,stack,cell}.go`
// adds the canonical labels with "if not exists" semantics, so an explicit
// user-authored `*.kukeon.io` value in `desired` still wins (AC #3 of issue
// #455). User-authored non-managed labels are unchanged: present in `desired`
// → kept; absent in `desired` → dropped.
func mergeManagedLabels(existing, desired map[string]string) map[string]string {
	if len(existing) == 0 && len(desired) == 0 {
		return desired
	}
	out := make(map[string]string, len(desired)+len(existing))
	for k, v := range desired {
		out[k] = v
	}
	for k, v := range existing {
		if !strings.HasSuffix(k, managedLabelSuffix) {
			continue
		}
		if _, authored := out[k]; authored {
			continue
		}
		out[k] = v
	}
	return out
}

// ApplyDefaultCellLabels stamps the four controller-managed scope labels
// (`realm.kukeon.io`, `space.kukeon.io`, `stack.kukeon.io`, `cell.kukeon.io`)
// onto cell with "if not exists" semantics, so an explicit user-authored
// value still wins (#455 AC #3). Every cell-create entry point calls it
// before runner.CreateCell: controller.CreateCell (`kuke create`/`kuke run`)
// via normalizeCellInputs, and the `kuke apply` create branch (#1461) —
// without it a cell born from apply carries no canonical selector labels.
func ApplyDefaultCellLabels(cell *intmodel.Cell, realm, space, stack, name string) {
	if cell.Metadata.Labels == nil {
		cell.Metadata.Labels = make(map[string]string)
	}
	for key, value := range map[string]string{
		consts.KukeonRealmLabelKey: realm,
		consts.KukeonSpaceLabelKey: space,
		consts.KukeonStackLabelKey: stack,
		consts.KukeonCellLabelKey:  name,
	} {
		if _, exists := cell.Metadata.Labels[key]; !exists {
			cell.Metadata.Labels[key] = value
		}
	}
}
