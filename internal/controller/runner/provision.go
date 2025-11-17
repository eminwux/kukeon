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
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func (r *Exec) provisionNewRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Update realm metadata
	if err := r.UpdateRealmMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}

	// Create realm namespace
	if err := r.createRealmContainerdNamespace(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateRealmNamespace, err)
	}

	// Create realm cgroup
	cgroupPath, err := r.createRealmCgroup(doc)
	if err != nil {
		return nil, err
	}

	// Persist cgroup path in metadata
	doc.Status.CgroupPath = cgroupPath

	// Update realm state
	doc.Status.State = v1beta1.RealmStateReady
	if err = r.UpdateRealmMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}
	return doc, nil
}

func (r *Exec) createRealmContainerdNamespace(doc *v1beta1.RealmDoc) error {
	// Create realm containerd namespace
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	exists, err := r.ctrClient.ExistsNamespace(doc.Spec.Namespace)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to check if kukeon namespace exists", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	if exists {
		r.logger.InfoContext(r.ctx, "kukeon namespace already exists", "namespace", doc.Spec.Namespace)
		return errdefs.ErrNamespaceAlreadyExists
	}

	fmt.Fprintf(os.Stdout, "Creating containerd namespace '%s'\n", doc.Spec.Namespace)
	err = r.ctrClient.CreateNamespace(doc.Spec.Namespace)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to create kukeon namespace", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrCreateNamespace, err)
	}
	r.logger.InfoContext(r.ctx, "created kukeon namespace", "namespace", doc.Spec.Namespace)

	return nil
}

func (r *Exec) ensureRealmContainerdNamespace(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Ensure containerd namespace exists for the realm described by doc
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	exists, err := r.ctrClient.ExistsNamespace(doc.Spec.Namespace)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	if !exists {
		if err = r.ctrClient.CreateNamespace(doc.Spec.Namespace); err != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateNamespace, err)
		}
		r.logger.InfoContext(r.ctx, "recreated missing containerd namespace for realm", "namespace", doc.Spec.Namespace)
	}
	return doc, nil
}

func (r *Exec) provisionNewSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	// Update space metadata
	if err := r.UpdateSpaceMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, err)
	}

	// Create space network (strict create)
	if networkErr := r.createSpaceCNIConfig(doc); networkErr != nil {
		return nil, networkErr
	}

	if doc.Spec.RealmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: doc.Spec.RealmID,
		},
	})
	if err != nil {
		return nil, err
	}

	// Create space cgroup
	if createErr := r.createSpaceCgroup(doc, realmDoc); createErr != nil {
		return nil, createErr
	}

	// Persist cgroup path in space metadata
	if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}

	// Update space state
	doc.Status.State = v1beta1.SpaceStateReady
	if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}
	return doc, nil
}

func (r *Exec) ensureSpaceCNIConfig(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	// Initialize CNI manager
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	networkName, err := naming.BuildSpaceNetworkName(doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, doc.Metadata.Name)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	// Ensure a network exists for this space
	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	if !exists {
		if writeErr := fs.WriteSpaceNetworkConfig(confPath, networkName); writeErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateNetwork, writeErr)
		}
	}
	r.logger.InfoContext(
		r.ctx,
		"space network exists",
		"space",
		doc.Metadata.Name,
		"network",
		networkName,
		"conf",
		confPath,
	)
	return doc, nil
}

func (r *Exec) createSpaceCNIConfig(doc *v1beta1.SpaceDoc) error {
	// Initialize CNI manager
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	networkName, err := naming.BuildSpaceNetworkName(doc)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, doc.Metadata.Name)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	// Check if network already exists
	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	if exists {
		r.logger.InfoContext(r.ctx, "space network already exists", "network", networkName)
		return errdefs.ErrNetworkAlreadyExists
	}

	fmt.Fprintf(os.Stdout, "Creating space network '%s'\n", networkName)
	if writeErr := fs.WriteSpaceNetworkConfig(confPath, networkName); writeErr != nil {
		r.logger.InfoContext(r.ctx, "failed to create space network", "err", fmt.Sprintf("%v", writeErr))
		return fmt.Errorf("%w: %w", errdefs.ErrCreateNetwork, writeErr)
	}
	r.logger.InfoContext(
		r.ctx,
		"created space network",
		"space",
		doc.Metadata.Name,
		"network",
		networkName,
		"conf",
		confPath,
	)
	return nil
}

func (r *Exec) ensureSpaceCgroup(doc *v1beta1.SpaceDoc, realm *v1beta1.RealmDoc) (*v1beta1.SpaceDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	connectErr := r.ctrClient.Connect()
	if connectErr != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, connectErr)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultSpaceSpec(realm, doc)

	// Discover the cgroup mountpoint and current process cgroup path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	currentCgroupPath, err := r.ctrClient.GetCurrentCgroupPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get current cgroup path: %w", err)
	}

	// Create the cgroup path relative to the current process's cgroup
	// e.g., if current is /user.slice/... and spec.Group is /kukeon/kuke-system/kuke-space,
	// the full path becomes /user.slice/.../kukeon/kuke-system/kuke-space
	relativeGroup := strings.TrimPrefix(spec.Group, "/")
	// Ensure the cgroup path starts with / for cgroup2
	combinedPath := filepath.Join(currentCgroupPath, relativeGroup)
	if !strings.HasPrefix(combinedPath, "/") {
		combinedPath = "/" + combinedPath
	}
	fullCgroupPath := filepath.Join(mountpoint, strings.TrimPrefix(combinedPath, "/"))
	spec.Group = combinedPath
	spec.Mountpoint = mountpoint

	r.logger.DebugContext(
		r.ctx,
		"checking if space cgroup exists",
		"space",
		doc.Metadata.Name,
		"realm",
		realm.Metadata.Name,
		"cgroup_path",
		spec.Group,
		"current_cgroup_path",
		currentCgroupPath,
		"mountpoint",
		mountpoint,
		"full_path",
		fullCgroupPath,
		"metadata_cgroup_path",
		doc.Status.CgroupPath,
	)

	_, loadErr := r.ctrClient.LoadCgroup(spec.Group, mountpoint)
	if loadErr != nil {
		r.logger.InfoContext(
			r.ctx,
			"space cgroup does not exist, attempting to create",
			"space",
			doc.Metadata.Name,
			"realm",
			realm.Metadata.Name,
			"cgroup_path",
			spec.Group,
			"current_cgroup_path",
			currentCgroupPath,
			"mountpoint",
			mountpoint,
			"full_path",
			fullCgroupPath,
			"load_error",
			fmt.Sprintf("%v", loadErr),
		)
		// Attempt to create if missing - spec now has the discovered mountpoint set
		manager, createErr := r.ctrClient.NewCgroup(spec)
		if createErr != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to create space cgroup",
				"space",
				doc.Metadata.Name,
				"realm",
				realm.Metadata.Name,
				"cgroup_path",
				spec.Group,
				"current_cgroup_path",
				currentCgroupPath,
				"mountpoint",
				mountpoint,
				"full_path",
				fullCgroupPath,
				"error",
				fmt.Sprintf("%v", createErr),
			)
			return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateSpaceCgroup, createErr)
		}
		if manager == nil {
			r.logger.ErrorContext(
				r.ctx,
				"NewCgroup returned nil manager without error",
				"space",
				doc.Metadata.Name,
				"realm",
				realm.Metadata.Name,
				"cgroup_path",
				spec.Group,
			)
			return nil, fmt.Errorf("%w: NewCgroup returned nil manager", errdefs.ErrCreateSpaceCgroup)
		}
		// Verify the cgroup was actually created
		actualPath := fullCgroupPath
		path, pathErr := r.ctrClient.CgroupPath(spec.Group, mountpoint)
		if pathErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to get actual cgroup path after creation",
				"space",
				doc.Metadata.Name,
				"realm",
				realm.Metadata.Name,
				"error",
				fmt.Sprintf("%v", pathErr),
			)
		} else {
			actualPath = path
			// Check if the path actually exists
			if _, statErr := os.Stat(actualPath); statErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"cgroup path does not exist on filesystem after creation",
					"space",
					doc.Metadata.Name,
					"realm",
					realm.Metadata.Name,
					"expected_path",
					actualPath,
					"error",
					fmt.Sprintf("%v", statErr),
				)
			}
		}

		r.logger.InfoContext(
			r.ctx,
			"successfully created space cgroup",
			"space",
			doc.Metadata.Name,
			"realm",
			realm.Metadata.Name,
			"cgroup_path",
			spec.Group,
			"actual_path",
			actualPath,
			"full_path",
			fullCgroupPath,
		)
		doc.Status.CgroupPath = spec.Group
		if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to update space metadata after cgroup creation",
				"space",
				doc.Metadata.Name,
				"realm",
				realm.Metadata.Name,
				"error",
				fmt.Sprintf("%v", updateErr),
			)
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
		}
		r.logger.InfoContext(
			r.ctx,
			"recreated missing space cgroup",
			"space",
			doc.Metadata.Name,
			"realm",
			realm.Metadata.Name,
			"path",
			spec.Group,
		)
		return doc, nil
	}

	r.logger.DebugContext(
		r.ctx,
		"space cgroup exists",
		"space",
		doc.Metadata.Name,
		"realm",
		realm.Metadata.Name,
		"cgroup_path",
		spec.Group,
	)

	if doc.Status.CgroupPath == "" {
		r.logger.InfoContext(
			r.ctx,
			"cgroup exists but metadata path is empty, backfilling",
			"space",
			doc.Metadata.Name,
			"realm",
			realm.Metadata.Name,
			"cgroup_path",
			spec.Group,
		)
		doc.Status.CgroupPath = spec.Group
		if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
		}
		r.logger.InfoContext(
			r.ctx,
			"backfilled space cgroup path",
			"space",
			doc.Metadata.Name,
			"realm",
			realm.Metadata.Name,
			"path",
			spec.Group,
		)
	}
	return doc, nil
}

func (r *Exec) createSpaceCgroup(doc *v1beta1.SpaceDoc, realm *v1beta1.RealmDoc) error {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	connectErr := r.ctrClient.Connect()
	if connectErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, connectErr)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultSpaceSpec(realm, doc)

	// Discover the cgroup mountpoint and current process cgroup path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	currentCgroupPath, err := r.ctrClient.GetCurrentCgroupPath()
	if err != nil {
		return fmt.Errorf("failed to get current cgroup path: %w", err)
	}

	// Create the cgroup path relative to the current process's cgroup
	// e.g., if current is /user.slice/... and spec.Group is /kukeon/kuke-system/kuke-space,
	// the full path becomes /user.slice/.../kukeon/kuke-system/kuke-space
	relativeGroup := strings.TrimPrefix(spec.Group, "/")
	// Ensure the cgroup path starts with / for cgroup2
	combinedPath := filepath.Join(currentCgroupPath, relativeGroup)
	if !strings.HasPrefix(combinedPath, "/") {
		combinedPath = "/" + combinedPath
	}
	spec.Group = combinedPath
	spec.Mountpoint = mountpoint

	_, createErr := r.ctrClient.NewCgroup(spec)
	if createErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCreateSpaceCgroup, createErr)
	}

	doc.Status.CgroupPath = spec.Group
	updateErr := r.UpdateSpaceMetadata(doc)
	if updateErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"created space cgroup",
		"space",
		doc.Metadata.Name,
		"realm",
		realm.Metadata.Name,
		"path",
		spec.Group,
	)
	return nil
}

func (r *Exec) createRealmCgroup(doc *v1beta1.RealmDoc) (string, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultRealmSpec(doc)

	// Discover the cgroup mountpoint and current process cgroup path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	currentCgroupPath, err := r.ctrClient.GetCurrentCgroupPath()
	if err != nil {
		return "", fmt.Errorf("failed to get current cgroup path: %w", err)
	}

	// Create the cgroup path relative to the current process's cgroup
	// e.g., if current is /user.slice/... and spec.Group is /kukeon/kuke-system,
	// the full path becomes /user.slice/.../kukeon/kuke-system
	relativeGroup := strings.TrimPrefix(spec.Group, "/")
	// Ensure the cgroup path starts with / for cgroup2
	combinedPath := filepath.Join(currentCgroupPath, relativeGroup)
	if !strings.HasPrefix(combinedPath, "/") {
		combinedPath = "/" + combinedPath
	}
	spec.Group = combinedPath
	spec.Mountpoint = mountpoint

	if _, err := r.ctrClient.NewCgroup(spec); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateRealmCgroup, err)
	}

	doc.Status.CgroupPath = spec.Group
	r.logger.InfoContext(
		r.ctx,
		"created realm cgroup",
		"realm",
		doc.Metadata.Name,
		"path",
		spec.Group,
	)
	return spec.Group, nil
}

func (r *Exec) ensureRealmCgroup(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultRealmSpec(doc)

	// Discover the cgroup mountpoint and current process cgroup path
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	currentCgroupPath, err := r.ctrClient.GetCurrentCgroupPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get current cgroup path: %w", err)
	}

	// Create the cgroup path relative to the current process's cgroup
	// e.g., if current is /user.slice/... and spec.Group is /kukeon/kuke-system,
	// the full path becomes /user.slice/.../kukeon/kuke-system
	relativeGroup := strings.TrimPrefix(spec.Group, "/")
	// Ensure the cgroup path starts with / for cgroup2
	combinedPath := filepath.Join(currentCgroupPath, relativeGroup)
	if !strings.HasPrefix(combinedPath, "/") {
		combinedPath = "/" + combinedPath
	}
	fullCgroupPath := filepath.Join(mountpoint, strings.TrimPrefix(combinedPath, "/"))
	spec.Group = combinedPath
	spec.Mountpoint = mountpoint

	r.logger.DebugContext(
		r.ctx,
		"checking if realm cgroup exists",
		"realm",
		doc.Metadata.Name,
		"cgroup_path",
		spec.Group,
		"current_cgroup_path",
		currentCgroupPath,
		"mountpoint",
		mountpoint,
		"full_path",
		fullCgroupPath,
		"metadata_cgroup_path",
		doc.Status.CgroupPath,
	)

	_, err = r.ctrClient.LoadCgroup(spec.Group, mountpoint)
	if err != nil {
		r.logger.InfoContext(
			r.ctx,
			"realm cgroup does not exist, attempting to create",
			"realm",
			doc.Metadata.Name,
			"cgroup_path",
			spec.Group,
			"current_cgroup_path",
			currentCgroupPath,
			"mountpoint",
			mountpoint,
			"full_path",
			fullCgroupPath,
			"load_error",
			fmt.Sprintf("%v", err),
		)
		// Attempt to create if missing - spec now has the discovered mountpoint set
		manager, createErr := r.ctrClient.NewCgroup(spec)
		if createErr != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to create realm cgroup",
				"realm",
				doc.Metadata.Name,
				"cgroup_path",
				spec.Group,
				"current_cgroup_path",
				currentCgroupPath,
				"mountpoint",
				mountpoint,
				"full_path",
				fullCgroupPath,
				"error",
				fmt.Sprintf("%v", createErr),
			)
			return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateRealmCgroup, createErr)
		}
		if manager == nil {
			r.logger.ErrorContext(
				r.ctx,
				"NewCgroup returned nil manager without error",
				"realm",
				doc.Metadata.Name,
				"cgroup_path",
				spec.Group,
			)
			return nil, fmt.Errorf("%w: NewCgroup returned nil manager", errdefs.ErrCreateRealmCgroup)
		}
		// Verify the cgroup was actually created
		actualPath := fullCgroupPath
		path, pathErr := r.ctrClient.CgroupPath(spec.Group, mountpoint)
		if pathErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to get actual cgroup path after creation",
				"realm",
				doc.Metadata.Name,
				"error",
				fmt.Sprintf("%v", pathErr),
			)
		} else {
			actualPath = path
			// Check if the path actually exists
			if _, statErr := os.Stat(actualPath); statErr != nil {
				r.logger.WarnContext(
					r.ctx,
					"cgroup path does not exist on filesystem after creation",
					"realm",
					doc.Metadata.Name,
					"expected_path",
					actualPath,
					"error",
					fmt.Sprintf("%v", statErr),
				)
			}
		}

		r.logger.InfoContext(
			r.ctx,
			"successfully created realm cgroup",
			"realm",
			doc.Metadata.Name,
			"cgroup_path",
			spec.Group,
			"actual_path",
			actualPath,
			"full_path",
			fullCgroupPath,
		)
		doc.Status.CgroupPath = spec.Group
		if updateErr := r.UpdateRealmMetadata(doc); updateErr != nil {
			r.logger.ErrorContext(
				r.ctx,
				"failed to update realm metadata after cgroup creation",
				"realm",
				doc.Metadata.Name,
				"error",
				fmt.Sprintf("%v", updateErr),
			)
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, updateErr)
		}
		r.logger.InfoContext(
			r.ctx,
			"recreated missing realm cgroup",
			"realm",
			doc.Metadata.Name,
			"path",
			spec.Group,
		)
		return doc, nil
	}

	r.logger.DebugContext(
		r.ctx,
		"realm cgroup exists",
		"realm",
		doc.Metadata.Name,
		"cgroup_path",
		spec.Group,
	)

	if doc.Status.CgroupPath == "" {
		r.logger.InfoContext(
			r.ctx,
			"cgroup exists but metadata path is empty, backfilling",
			"realm",
			doc.Metadata.Name,
			"cgroup_path",
			spec.Group,
		)
		doc.Status.CgroupPath = spec.Group
		if updateErr := r.UpdateRealmMetadata(doc); updateErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, updateErr)
		}
		r.logger.InfoContext(
			r.ctx,
			"backfilled realm cgroup path",
			"realm",
			doc.Metadata.Name,
			"path",
			doc.Status.CgroupPath,
		)
	}
	return doc, nil
}
