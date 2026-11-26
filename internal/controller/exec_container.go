// Copyright 2026 Emiliano Spinella (eminwux)
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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// ExecContainer starts spec as a new process in the running task of
// container (issue #1432). It only locates the cell and forwards to the
// runner: the gates (the container exists, its task is running) and the
// session around the returned process belong to the caller.
func (b *Exec) ExecContainer(
	container intmodel.Container,
	spec ctr.ExecSpec,
	stdio ctr.ExecStdio,
) (ctr.ExecProcess, error) {
	name := strings.TrimSpace(container.Metadata.Name)
	if name == "" {
		return nil, errdefs.ErrContainerNameRequired
	}
	cellName := strings.TrimSpace(container.Spec.CellName)
	if cellName == "" {
		return nil, errdefs.ErrCellNameRequired
	}
	cell, err := b.runner.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: cellName},
		Spec: intmodel.CellSpec{
			RealmName: strings.TrimSpace(container.Spec.RealmName),
			SpaceName: strings.TrimSpace(container.Spec.SpaceName),
			StackName: strings.TrimSpace(container.Spec.StackName),
		},
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return nil, fmt.Errorf("failed to get cell %q: %w", cellName, errdefs.ErrCellNotFound)
		}
		return nil, fmt.Errorf("failed to get cell %q: %w", cellName, err)
	}
	return b.runner.ExecContainer(cell, name, spec, stdio)
}
