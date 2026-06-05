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
	"context"
	"errors"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ResolveCellName returns the final name for a cell about to be materialized
// into the given scope (epic:cell-identity #1022). It is the CLI-side wrapper
// around naming.AllocCellName that builds the in-scope collision probe over the
// daemon's GetCell view:
//
//   - explicit non-empty → used verbatim (the persist layer rejects an in-scope
//     collision; an explicitly named create is not an idempotent attach);
//   - explicit empty → a generated `<prefix>-<6hex>` probed free against the
//     daemon at realm/space/stack, regenerating the suffix on collision.
//
// The probe treats ErrCellNotFound as "free"; any other GetCell error aborts
// allocation.
func ResolveCellName(
	ctx context.Context,
	client kukeonv1.Client,
	explicit, prefix, realm, space, stack string,
) (string, error) {
	exists := func(name string) (bool, error) {
		pre, err := client.GetCell(ctx, v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: name},
			Spec: v1beta1.CellSpec{
				RealmID: realm,
				SpaceID: space,
				StackID: stack,
			},
		})
		if err != nil {
			if errors.Is(err, errdefs.ErrCellNotFound) {
				return false, nil
			}
			return false, err
		}
		return pre.MetadataExists, nil
	}
	return naming.AllocCellName(explicit, prefix, exists)
}
