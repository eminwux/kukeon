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
	"fmt"
	"os"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// attachableBuildOpts returns the ctr.BuildOption slice to pass to
// CreateContainerFromSpec for a given container spec. When Attachable=false
// the slice is empty and the call is a no-op. When Attachable=true the
// runner pre-creates the per-container tty/ directory (sbsh's bind-mount
// source — sbsh creates the socket and its capture/log siblings there) and
// resolves the sbsh binary path keyed off the *image* arch, not the host
// arch — a cross-arch image running under emulation would otherwise pick a
// binary the in-container ELF interpreter cannot run.
func (r *Exec) attachableBuildOpts(spec intmodel.ContainerSpec) ([]ctr.BuildOption, error) {
	if !spec.Attachable {
		return nil, nil
	}

	ttyDir := fs.ContainerTTYDir(
		r.opts.RunPath,
		spec.RealmName, spec.SpaceName, spec.StackName, spec.CellName,
		spec.ID,
	)
	// 0700 so only the daemon (root) can reach the socket from the host
	// side. Documented in the issue's "Socket security threat model".
	if err := os.MkdirAll(ttyDir, 0o700); err != nil {
		return nil, err
	}

	binaryPath, err := r.ctrClient.ResolveSbshCachePath(spec.Image, r.opts.RunPath)
	if err != nil {
		return nil, fmt.Errorf("resolve sbsh cache path for %q: %w", spec.Image, err)
	}

	useProfile := !spec.Tty.IsEmpty()
	if useProfile {
		if err = writeSbshProfile(ttyDir, spec); err != nil {
			return nil, err
		}
	}

	return []ctr.BuildOption{
		ctr.WithAttachableInjection(ctr.AttachableInjection{
			SbshBinaryPath: binaryPath,
			HostTTYDir:     ttyDir,
			UseProfile:     useProfile,
		}),
	}, nil
}
