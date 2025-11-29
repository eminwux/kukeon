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
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func (r *Exec) DeleteRealm(realm intmodel.Realm) error {
	realmName := strings.TrimSpace(realm.Metadata.Name)
	if realmName == "" {
		return errdefs.ErrRealmNameRequired
	}

	internalRealm, err := r.GetRealm(realm)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			// Idempotent: realm doesn't exist, consider it deleted
			return nil
		}
		return fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Delete realm cgroup
	spec := cgroups.DefaultRealmSpec(internalRealm)
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	if err = r.ctrClient.DeleteCgroup(spec.Group, mountpoint); err != nil {
		return fmt.Errorf("%w: failed to delete realm cgroup: %w", errdefs.ErrDeleteRealm, err)
	}

	// Delete containerd namespace
	if err = r.ctrClient.DeleteNamespace(internalRealm.Spec.Namespace); err != nil {
		return fmt.Errorf("%w: failed to delete containerd namespace: %w", errdefs.ErrDeleteRealm, err)
	}

	// Delete realm metadata
	metadataFilePath := fs.RealmMetadataPath(r.opts.RunPath, internalRealm.Metadata.Name)
	if err = metadata.DeleteMetadata(r.ctx, r.logger, metadataFilePath); err != nil {
		return fmt.Errorf("%w: failed to delete realm metadata: %w", errdefs.ErrDeleteRealm, err)
	}

	return nil
}
