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
	"fmt"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// PickContainer enumerates the cell's containers via client.ListContainers
// and returns the single container ID for which include returns true.
//
// `kuke attach` and `kuke log` both need to resolve the implicit container
// when the operator omits --container; the two disagree on which specs to
// keep — `kuke attach` requires Attachable=true, `kuke log` accepts
// non-Attachable too — but the enumeration / sort / error semantics are
// identical and live here so a future change to one subcommand cannot
// drift the other.
//
// Callers pass include and decide for themselves whether to exclude
// Spec.Root (both current callers do).
//
// Errors:
//   - errdefs.ErrAttachNoCandidate (wrapped with cell) when no spec
//     passes include.
//   - errdefs.ErrAttachAmbiguous (wrapped with cell + sorted candidate
//     list) when more than one passes.
func PickContainer(
	ctx context.Context,
	client kukeonv1.Client,
	realm, space, stack, cell string,
	include func(v1beta1.ContainerSpec) bool,
) (string, error) {
	specs, err := client.ListContainers(ctx, realm, space, stack, cell)
	if err != nil {
		return "", err
	}

	candidates := make([]string, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		if !include(spec) {
			continue
		}
		candidates = append(candidates, spec.ID)
	}
	sort.Strings(candidates)

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("%w (cell %q)", errdefs.ErrAttachNoCandidate, cell)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("%w (cell %q): candidates: %s",
			errdefs.ErrAttachAmbiguous, cell, strings.Join(candidates, ", "))
	}
}
