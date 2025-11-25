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
	"os"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) ExistsRealmContainerdNamespace(namespace string) (bool, error) {
	// Check if containerd socket exists before trying to connect
	// This avoids connection timeouts in test scenarios where containerd is not available
	if _, err := os.Stat(r.opts.ContainerdSocket); os.IsNotExist(err) {
		// Socket doesn't exist, return false (namespace doesn't exist) without error
		// This is appropriate for test scenarios and when containerd is not running
		return false, nil
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()
	return r.ctrClient.ExistsNamespace(namespace)
}

func (r *Exec) ExistsCellRootContainer(doc *v1beta1.CellDoc) (bool, error) {
	if doc == nil {
		return false, errdefs.ErrCellNotFound
	}

	cellName := doc.Metadata.Name
	if cellName == "" {
		return false, errdefs.ErrCellNotFound
	}

	cellID := doc.Spec.ID
	if cellID == "" {
		return false, errdefs.ErrCellIDRequired
	}

	realmID := doc.Spec.RealmID
	if realmID == "" {
		return false, errdefs.ErrRealmNameRequired
	}

	spaceID := doc.Spec.SpaceID
	if spaceID == "" {
		return false, errdefs.ErrSpaceNameRequired
	}

	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmID,
		},
	})
	if err != nil {
		return false, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(realmDoc.Spec.Namespace)

	// Generate container ID with cell identifier for uniqueness
	// Need to get spaceID and stackID from doc
	stackID := doc.Spec.StackID
	if stackID == "" {
		return false, errdefs.ErrStackNameRequired
	}
	containerID, err := naming.BuildRootContainerName(spaceID, stackID, cellID)
	if err != nil {
		return false, fmt.Errorf("failed to build root container name: %w", err)
	}

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(r.ctx, containerID)
	if err != nil {
		return false, fmt.Errorf("failed to check if root container exists: %w", err)
	}

	return exists, nil
}

func (r *Exec) ExistsCgroup(doc any) (bool, error) {
	// Initialize ctr client if needed
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	var spec ctr.CgroupSpec
	var err error

	// Build cgroup spec based on doc type
	switch d := doc.(type) {
	case *v1beta1.RealmDoc:
		if d == nil {
			return false, errdefs.ErrRealmNotFound
		}
		spec = cgroups.DefaultRealmSpec(d)

	case *v1beta1.SpaceDoc:
		if d == nil {
			return false, errdefs.ErrSpaceNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spec = cgroups.DefaultSpaceSpec(realmDoc, d)

	case *v1beta1.StackDoc:
		if d == nil {
			return false, errdefs.ErrStackNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		if d.Spec.SpaceID == "" {
			return false, errdefs.ErrSpaceNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spaceDoc, spaceErr := r.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: d.Spec.SpaceID,
			},
			Spec: v1beta1.SpaceSpec{
				RealmID: d.Spec.RealmID,
			},
		})
		if spaceErr != nil {
			return false, fmt.Errorf("failed to get space: %w", spaceErr)
		}
		spec = cgroups.DefaultStackSpec(realmDoc, spaceDoc, d)

	case *v1beta1.CellDoc:
		if d == nil {
			return false, errdefs.ErrCellNotFound
		}
		if d.Spec.RealmID == "" {
			return false, errdefs.ErrRealmNameRequired
		}
		if d.Spec.SpaceID == "" {
			return false, errdefs.ErrSpaceNameRequired
		}
		if d.Spec.StackID == "" {
			return false, errdefs.ErrStackNameRequired
		}
		realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{
				Name: d.Spec.RealmID,
			},
		})
		if realmErr != nil {
			return false, fmt.Errorf("failed to get realm: %w", realmErr)
		}
		spaceDoc, spaceErr := r.GetSpace(&v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{
				Name: d.Spec.SpaceID,
			},
			Spec: v1beta1.SpaceSpec{
				RealmID: d.Spec.RealmID,
			},
		})
		if spaceErr != nil {
			return false, fmt.Errorf("failed to get space: %w", spaceErr)
		}
		stackDoc, stackErr := r.GetStack(&v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{
				Name: d.Spec.StackID,
			},
			Spec: v1beta1.StackSpec{
				RealmID: d.Spec.RealmID,
				SpaceID: d.Spec.SpaceID,
			},
		})
		if stackErr != nil {
			return false, fmt.Errorf("failed to get stack: %w", stackErr)
		}
		spec = cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, d)

	default:
		return false, fmt.Errorf("unsupported doc type: %T", doc)
	}

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return false, fmt.Errorf("failed to build cgroup path: %w", err)
	}

	// Check if cgroup exists
	_, err = r.ctrClient.LoadCgroup(spec.Group, spec.Mountpoint)
	if err != nil {
		// Check if error is "cgroup path does not exist"
		if err.Error() == "cgroup path does not exist" {
			return false, nil
		}
		return false, fmt.Errorf("failed to check if cgroup exists: %w", err)
	}

	return true, nil
}

func (r *Exec) ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error) {
	if doc == nil {
		return false, errdefs.ErrSpaceDocRequired
	}
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" {
		return false, errdefs.ErrRealmNameRequired
	}
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, realmName, doc.Metadata.Name)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	networkName, err := naming.BuildSpaceNetworkName(realmName, doc.Metadata.Name)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	return exists, nil
}
