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

package runner

import (
	"fmt"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// ExecContainer starts spec as a new process in the running task of the
// named container of cell (issue #1432). The containerd namespace and ID
// come from the same resolution GetContainerObservation uses. The stdio
// FIFOs go under the run path, which the kukeond cell and the host see at
// the same path.
func (r *Exec) ExecContainer(
	cell intmodel.Cell,
	containerName string,
	spec ctr.ExecSpec,
	stdio ctr.ExecStdio,
) (ctr.ExecProcess, error) {
	target, found, err := r.resolveContainerdTarget(cell, containerName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("container %q in cell %q: %w",
			containerName, cell.Metadata.Name, errdefs.ErrContainerNotFound)
	}
	if spec.FIFODir == "" && r.opts.RunPath != "" {
		spec.FIFODir = fs.ExecFIFODir(r.opts.RunPath)
	}
	return r.ctrClient.ExecTask(
		target.namespace, target.containerdID, spec, stdio.Stdin, stdio.Stdout, stdio.Stderr,
	)
}
