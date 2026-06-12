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

package controller

import (
	"sort"

	"github.com/eminwux/kukeon/internal/apply/parser"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// SortDocumentsByKind sorts documents by kind order.
// If reverse is true, sorts in reverse dependency order (Container → Cell → Stack → Space → Realm).
// If reverse is false, sorts in dependency order (Realm → Space → Stack → Cell → Container).
// Within the same kind, documents are sorted by their original index.
func SortDocumentsByKind(docs []parser.Document, reverse bool) []parser.Document {
	// Create a copy to avoid modifying the original
	sorted := make([]parser.Document, len(docs))
	copy(sorted, docs)

	// Define kind order
	kindOrder := map[v1beta1.Kind]int{
		v1beta1.KindRealm:     1,
		v1beta1.KindSpace:     2,
		v1beta1.KindStack:     3,
		v1beta1.KindCell:      4,
		v1beta1.KindContainer: 5,
	}
	// Secrets sort one past the deepest hierarchy kind so any scope
	// (realm/space/stack/cell) bundled in the same apply file is materialized
	// before a secret that targets it — apply rejects a secret whose scope
	// does not yet exist. Derived from the map length rather than a literal so
	// it stays correct if a kind is inserted above.
	kindOrder[v1beta1.KindSecret] = len(kindOrder) + 1
	// CellBlueprints, like Secrets, target an existing scope (realm/space/stack)
	// rather than creating one, so they sort after the hierarchy kinds: any
	// scope bundled in the same apply file is materialized before a blueprint
	// that targets it (reconcile rejects a blueprint whose scope does not yet
	// exist). Evaluated after the Secret line so it lands one slot further out.
	kindOrder[v1beta1.KindCellBlueprint] = len(kindOrder) + 1
	// CellConfigs reference a CellBlueprint and validate their slot fills
	// against it at reconcile time, so they sort after CellBlueprints: a
	// blueprint bundled in the same apply file is written before a config that
	// references it (reconcile rejects a config whose blueprint does not yet
	// exist). Evaluated after the CellBlueprint line so it lands one slot
	// further out.
	kindOrder[v1beta1.KindCellConfig] = len(kindOrder) + 1
	// Volumes, like Secrets and CellBlueprints, target an existing scope
	// (realm/space/stack) rather than creating one, so they sort after the
	// hierarchy kinds: any scope bundled in the same apply file is materialized
	// before a volume that targets it (reconcile rejects a volume whose scope
	// does not yet exist). Evaluated after the CellConfig line so it lands one
	// slot further out; the relative order among the scope-targeting kinds is
	// immaterial since none reference another.
	kindOrder[v1beta1.KindVolume] = len(kindOrder) + 1

	// Sort by kind order, then by original index
	sort.Slice(sorted, func(i, j int) bool {
		orderI := kindOrder[sorted[i].Kind]
		orderJ := kindOrder[sorted[j].Kind]
		if orderI != orderJ {
			if reverse {
				return orderI > orderJ // Descending order for reverse
			}
			return orderI < orderJ // Ascending order for forward
		}
		return sorted[i].Index < sorted[j].Index
	})

	return sorted
}
