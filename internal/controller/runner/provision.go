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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/cgroups"
	ctrutil "github.com/eminwux/kukeon/internal/util/ctr"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// ensureCgroupParams holds parameters for ensureCgroupInternal.
type ensureCgroupParams struct {
	spec           ctr.CgroupSpec
	docName        string
	cgroupPath     *string
	createErr      error
	updateErr      error
	logLabel       string
	updateMetadata func() error
}

func (r *Exec) provisionNewRealm(realm intmodel.Realm) (intmodel.Realm, error) {
	// Update realm metadata with Creating state
	if err := r.UpdateRealmMetadata(realm); err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}

	// Create realm namespace
	if err := r.createRealmContainerdNamespace(realm); err != nil {
		// Set state to Failed and update metadata before returning error
		realm.Status.State = intmodel.RealmStateFailed
		_ = r.UpdateRealmMetadata(realm) // Best effort to save failed state
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrCreateRealmNamespace, err)
	}

	// Create realm cgroup
	cgroupPath, err := r.createRealmCgroup(realm)
	if err != nil {
		// Set state to Failed and update metadata before returning error
		realm.Status.State = intmodel.RealmStateFailed
		_ = r.UpdateRealmMetadata(realm) // Best effort to save failed state
		return intmodel.Realm{}, err
	}

	// Update CgroupPath and state in internal model
	realm.Status.CgroupPath = cgroupPath
	realm.Status.State = intmodel.RealmStateReady

	// Always update metadata after cgroup creation to ensure CgroupPath is saved
	// (similar to ensureRealmCgroup pattern for consistency)
	if err = r.UpdateRealmMetadata(realm); err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}

	return realm, nil
}

func (r *Exec) createRealmContainerdNamespace(realm intmodel.Realm) error {
	// Create realm containerd namespace
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	exists, err := r.ctrClient.ExistsNamespace(realm.Spec.Namespace)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to check if kukeon namespace exists", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}

	if exists {
		r.logger.InfoContext(r.ctx, "kukeon namespace already exists", "namespace", realm.Spec.Namespace)
		return errdefs.ErrNamespaceAlreadyExists
	}

	fmt.Fprintf(os.Stdout, "Creating containerd namespace '%s'\n", realm.Spec.Namespace)
	err = r.ctrClient.CreateNamespace(realm.Spec.Namespace)
	if err != nil {
		r.logger.InfoContext(r.ctx, "failed to create kukeon namespace", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrCreateNamespace, err)
	}
	r.logger.InfoContext(r.ctx, "created kukeon namespace", "namespace", realm.Spec.Namespace)

	return nil
}

func (r *Exec) ensureRealmContainerdNamespace(realm intmodel.Realm) (intmodel.Realm, error) {
	// Ensure containerd namespace exists for the realm
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	exists, err := r.ctrClient.ExistsNamespace(realm.Spec.Namespace)
	if err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNamespaceExists, err)
	}
	if !exists {
		if err = r.ctrClient.CreateNamespace(realm.Spec.Namespace); err != nil {
			return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrCreateNamespace, err)
		}
		r.logger.InfoContext(
			r.ctx,
			"recreated missing containerd namespace for realm",
			"namespace",
			realm.Spec.Namespace,
		)
	}
	return realm, nil
}

func (r *Exec) provisionNewSpace(space intmodel.Space) (intmodel.Space, error) {
	// Update space metadata
	if err := r.UpdateSpaceMetadata(space); err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, err)
	}

	// Create space network (strict create)
	cniConfigPath, err := r.createSpaceCNIConfig(space)
	if err != nil {
		return intmodel.Space{}, err
	}
	// Update internal model with CNIConfigPath
	space.Spec.CNIConfigPath = cniConfigPath

	// Create space cgroup
	cgroupPath, createErr := r.createSpaceCgroup(space)
	if createErr != nil {
		return intmodel.Space{}, createErr
	}

	// Update CgroupPath and state in internal model
	space.Status.CgroupPath = cgroupPath
	space.Status.State = intmodel.SpaceStateReady

	// Update space metadata
	if updateErr := r.UpdateSpaceMetadata(space); updateErr != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
	}

	return space, nil
}

func (r *Exec) ensureSpaceCNIConfig(space intmodel.Space) (intmodel.Space, error) {
	// Initialize CNI manager
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	// Convert to external model for other operations (CNIConfigPath handling)
	spaceDoc, err := apischeme.BuildSpaceExternalFromInternal(space, apischeme.VersionV1Beta1)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	networkName, err := naming.BuildSpaceNetworkName(space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	// Ensure a network exists for this space
	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	if !exists {
		if writeErr := fs.WriteSpaceNetworkConfig(confPath, networkName); writeErr != nil {
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrCreateNetwork, writeErr)
		}
	}
	specUpdated := spaceDoc.Spec.CNIConfigPath != confPath
	if specUpdated {
		spaceDoc.Spec.CNIConfigPath = confPath
		// Convert back to internal model for UpdateSpaceMetadata
		// Note: CNIConfigPath will be lost in internal model, but UpdateSpaceMetadata
		// converts to external which will have CNIConfigPath from spaceDoc
		// Actually, we need to preserve CNIConfigPath, so we'll convert spaceDoc which has it
		updatedSpace, err := apischeme.ConvertSpaceDocToInternal(spaceDoc)
		if err != nil {
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
		}
		// Preserve any internal-only fields that might have been set
		updatedSpace.Status.CgroupPath = space.Status.CgroupPath
		// UpdateSpaceMetadata will convert to external, but CNIConfigPath is not in internal model
		// So we need to ensure it's preserved. Since UpdateSpaceMetadata converts internal->external,
		// and CNIConfigPath is not in internal, we need to handle this differently.
		// Solution: Call UpdateSpaceMetadata with spaceDoc directly converted, but that requires
		// changing UpdateSpaceMetadata signature... Actually, we can build external from the
		// updatedSpace and then manually set CNIConfigPath before saving.
		spaceDocForSave, err := apischeme.BuildSpaceExternalFromInternal(updatedSpace, apischeme.VersionV1Beta1)
		if err != nil {
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
		}
		// Preserve CNIConfigPath from the original spaceDoc
		spaceDocForSave.Spec.CNIConfigPath = spaceDoc.Spec.CNIConfigPath
		// Now save using the external model directly
		metadataFilePath := fs.SpaceMetadataPath(
			r.opts.RunPath,
			spaceDocForSave.Spec.RealmID,
			spaceDocForSave.Metadata.Name,
		)
		if updateErr := metadata.WriteMetadata(r.ctx, r.logger, spaceDocForSave, metadataFilePath); updateErr != nil {
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
		}
	}
	r.logger.InfoContext(
		r.ctx,
		"space network exists",
		"space",
		space.Metadata.Name,
		"network",
		networkName,
		"conf",
		confPath,
	)
	return space, nil
}

func (r *Exec) createSpaceCNIConfig(space intmodel.Space) (string, error) {
	// Initialize CNI manager
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	networkName, err := naming.BuildSpaceNetworkName(space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	// Check if network already exists
	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	if exists {
		r.logger.InfoContext(r.ctx, "space network already exists", "network", networkName)
		return "", errdefs.ErrNetworkAlreadyExists
	}

	fmt.Fprintf(os.Stdout, "Creating space network '%s'\n", networkName)
	if writeErr := fs.WriteSpaceNetworkConfig(confPath, networkName); writeErr != nil {
		r.logger.InfoContext(r.ctx, "failed to create space network", "err", fmt.Sprintf("%v", writeErr))
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateNetwork, writeErr)
	}
	r.logger.InfoContext(
		r.ctx,
		"created space network",
		"space",
		space.Metadata.Name,
		"network",
		networkName,
		"conf",
		confPath,
	)
	return confPath, nil
}

// buildCgroupPath discovers the cgroup mountpoint and current process cgroup path,
// then combines the spec's Group path relative to the current process's cgroup.
// Returns the updated spec with Group and Mountpoint set, and the full filesystem path.
func (r *Exec) buildCgroupPath(spec ctr.CgroupSpec) (ctr.CgroupSpec, string, error) {
	mountpoint := r.ctrClient.GetCgroupMountpoint()
	currentCgroupPath, err := r.ctrClient.GetCurrentCgroupPath()
	if err != nil {
		return spec, "", fmt.Errorf("failed to get current cgroup path: %w", err)
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

	return spec, fullCgroupPath, nil
}

// createCgroupInternal handles client initialization, connection, and cgroup creation.
// It assumes the spec already has Group and Mountpoint set correctly.
func (r *Exec) createCgroupInternal(spec ctr.CgroupSpec) (string, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	if _, err := r.ctrClient.NewCgroup(spec); err != nil {
		return "", err
	}

	return spec.Group, nil
}

// ensureCgroupInternal handles the common ensure cgroup logic: checking existence,
// creating if missing, verifying creation, and backfilling metadata if needed.
func (r *Exec) ensureCgroupInternal(params ensureCgroupParams) error {
	// Build the cgroup path
	spec, fullCgroupPath, err := r.buildCgroupPath(params.spec)
	if err != nil {
		return err
	}

	r.logger.DebugContext(
		r.ctx,
		fmt.Sprintf("checking if %s cgroup exists", params.logLabel),
		params.logLabel,
		params.docName,
		"cgroup_path",
		spec.Group,
		"mountpoint",
		spec.Mountpoint,
		"full_path",
		fullCgroupPath,
		"metadata_cgroup_path",
		*params.cgroupPath,
	)

	// Check if cgroup exists
	_, err = r.ctrClient.LoadCgroup(spec.Group, spec.Mountpoint)
	if err != nil {
		r.logger.InfoContext(
			r.ctx,
			fmt.Sprintf("%s cgroup does not exist, attempting to create", params.logLabel),
			params.logLabel,
			params.docName,
			"cgroup_path",
			spec.Group,
			"mountpoint",
			spec.Mountpoint,
			"full_path",
			fullCgroupPath,
			"load_error",
			fmt.Sprintf("%v", err),
		)

		// Attempt to create if missing
		manager, createErr := r.ctrClient.NewCgroup(spec)
		if createErr != nil {
			r.logger.ErrorContext(
				r.ctx,
				fmt.Sprintf("failed to create %s cgroup", params.logLabel),
				params.logLabel,
				params.docName,
				"cgroup_path",
				spec.Group,
				"mountpoint",
				spec.Mountpoint,
				"full_path",
				fullCgroupPath,
				"error",
				fmt.Sprintf("%v", createErr),
			)
			return fmt.Errorf("%w: %w", params.createErr, createErr)
		}
		if manager == nil {
			r.logger.ErrorContext(
				r.ctx,
				"NewCgroup returned nil manager without error",
				params.logLabel,
				params.docName,
				"cgroup_path",
				spec.Group,
			)
			return fmt.Errorf("%w: NewCgroup returned nil manager", params.createErr)
		}

		// Verify the cgroup was actually created
		actualPath := fullCgroupPath
		path, pathErr := r.ctrClient.CgroupPath(spec.Group, spec.Mountpoint)
		if pathErr != nil {
			r.logger.WarnContext(
				r.ctx,
				"failed to get actual cgroup path after creation",
				params.logLabel,
				params.docName,
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
					params.logLabel,
					params.docName,
					"expected_path",
					actualPath,
					"error",
					fmt.Sprintf("%v", statErr),
				)
			}
		}

		r.logger.InfoContext(
			r.ctx,
			fmt.Sprintf("successfully created %s cgroup", params.logLabel),
			params.logLabel,
			params.docName,
			"cgroup_path",
			spec.Group,
			"actual_path",
			actualPath,
			"full_path",
			fullCgroupPath,
		)

		*params.cgroupPath = spec.Group
		if err = params.updateMetadata(); err != nil {
			r.logger.ErrorContext(
				r.ctx,
				fmt.Sprintf("failed to update %s metadata after cgroup creation", params.logLabel),
				params.logLabel,
				params.docName,
				"error",
				fmt.Sprintf("%v", err),
			)
			return fmt.Errorf("%w: %w", params.updateErr, err)
		}
		r.logger.InfoContext(
			r.ctx,
			fmt.Sprintf("recreated missing %s cgroup", params.logLabel),
			params.logLabel,
			params.docName,
			"path",
			spec.Group,
		)
		return nil
	}

	// Cgroup exists
	r.logger.DebugContext(
		r.ctx,
		fmt.Sprintf("%s cgroup exists", params.logLabel),
		params.logLabel,
		params.docName,
		"cgroup_path",
		spec.Group,
	)

	// Backfill metadata if path is empty
	if *params.cgroupPath == "" {
		r.logger.InfoContext(
			r.ctx,
			"cgroup exists but metadata path is empty, backfilling",
			params.logLabel,
			params.docName,
			"cgroup_path",
			spec.Group,
		)
		*params.cgroupPath = spec.Group
		if err = params.updateMetadata(); err != nil {
			return fmt.Errorf("%w: %w", params.updateErr, err)
		}
		r.logger.InfoContext(
			r.ctx,
			fmt.Sprintf("backfilled %s cgroup path", params.logLabel),
			params.logLabel,
			params.docName,
			"path",
			*params.cgroupPath,
		)
	}

	return nil
}

func (r *Exec) ensureSpaceCgroup(space intmodel.Space) (intmodel.Space, error) {
	// Extract realm name from space and validate
	if space.Spec.RealmName == "" {
		return intmodel.Space{}, errdefs.ErrRealmNameRequired
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultSpaceSpec(space)

	// Convert to external model for ensureCgroupInternal (requires external model for CgroupPath updates)
	spaceDoc, err := apischeme.BuildSpaceExternalFromInternal(space, apischeme.VersionV1Beta1)
	if err != nil {
		return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Capture space for closure
	spaceForUpdate := space
	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    spaceDoc.Metadata.Name,
		cgroupPath: &spaceDoc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateSpaceCgroup,
		updateErr:  errdefs.ErrUpdateSpaceMetadata,
		logLabel:   "space",
		updateMetadata: func() error {
			// Update internal model's CgroupPath from external model
			spaceForUpdate.Status.CgroupPath = spaceDoc.Status.CgroupPath
			return r.UpdateSpaceMetadata(spaceForUpdate)
		},
	})
	if ensureErr != nil {
		return intmodel.Space{}, ensureErr
	}

	// Update internal model's CgroupPath from external model after ensureCgroupInternal
	space.Status.CgroupPath = spaceDoc.Status.CgroupPath

	// Always update metadata after ensureCgroupInternal to ensure CgroupPath is saved
	// (closure may not have been called if cgroup already existed with path)
	if spaceDoc.Status.CgroupPath != "" {
		if err := r.UpdateSpaceMetadata(space); err != nil {
			return intmodel.Space{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, err)
		}
	}

	return space, nil
}

func (r *Exec) createSpaceCgroup(space intmodel.Space) (string, error) {
	// Extract realm name from space and validate
	if space.Spec.RealmName == "" {
		return "", errdefs.ErrRealmNameRequired
	}

	// Fetch the realm internally for DefaultSpaceSpec
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: space.Spec.RealmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return "", err
	}

	spec := cgroups.DefaultSpaceSpec(space)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return "", err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateSpaceCgroup, createErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"created space cgroup",
		"space",
		space.Metadata.Name,
		"realm",
		internalRealm.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) createRealmCgroup(realm intmodel.Realm) (string, error) {
	spec := cgroups.DefaultRealmSpec(realm)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	var err error
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return "", err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateRealmCgroup, createErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"created realm cgroup",
		"realm",
		realm.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) ensureRealmCgroup(realm intmodel.Realm) (intmodel.Realm, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultRealmSpec(realm)

	// Convert to external model for ensureCgroupInternal (requires external model for CgroupPath updates)
	realmDoc, err := apischeme.BuildRealmExternalFromInternal(realm, apischeme.VersionV1Beta1)
	if err != nil {
		return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	// Capture realm for closure
	realmForUpdate := realm
	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    realmDoc.Metadata.Name,
		cgroupPath: &realmDoc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateRealmCgroup,
		updateErr:  errdefs.ErrUpdateRealmMetadata,
		logLabel:   "realm",
		updateMetadata: func() error {
			// Update internal model's CgroupPath from external model
			realmForUpdate.Status.CgroupPath = realmDoc.Status.CgroupPath
			return r.UpdateRealmMetadata(realmForUpdate)
		},
	})
	if ensureErr != nil {
		return intmodel.Realm{}, ensureErr
	}

	// Update internal model's CgroupPath from external model after ensureCgroupInternal
	realm.Status.CgroupPath = realmDoc.Status.CgroupPath

	// Always update metadata after ensureCgroupInternal to ensure CgroupPath is saved
	// (closure may not have been called if cgroup already existed with path)
	if realmDoc.Status.CgroupPath != "" {
		if err := r.UpdateRealmMetadata(realm); err != nil {
			return intmodel.Realm{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
		}
	}

	return realm, nil
}

func (r *Exec) provisionNewStack(stack intmodel.Stack) (intmodel.Stack, error) {
	// Update stack metadata
	if err := r.UpdateStackMetadata(stack); err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, err)
	}

	// Create stack cgroup
	cgroupPath, err := r.createStackCgroup(stack)
	if err != nil {
		return intmodel.Stack{}, err
	}

	// Update CgroupPath and state in internal model
	stack.Status.CgroupPath = cgroupPath
	stack.Status.State = intmodel.StackStateReady

	// Update stack metadata
	if err = r.UpdateStackMetadata(stack); err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, err)
	}

	return stack, nil
}

func (r *Exec) createStackCgroup(stack intmodel.Stack) (string, error) {
	// Extract realm and space names from stack and validate
	if stack.Spec.RealmName == "" {
		return "", errdefs.ErrRealmNameRequired
	}
	if stack.Spec.SpaceName == "" {
		return "", errdefs.ErrSpaceNameRequired
	}

	spec := cgroups.DefaultStackSpec(stack)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	var err error
	if err = r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return "", err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateStackCgroup, createErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"created stack cgroup",
		"stack",
		stack.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) ensureStackCgroup(stack intmodel.Stack) (intmodel.Stack, error) {
	realmName := strings.TrimSpace(stack.Spec.RealmName)
	if realmName == "" {
		return intmodel.Stack{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(stack.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Stack{}, errdefs.ErrSpaceNameRequired
	}

	stackDoc, err := apischeme.BuildStackExternalFromInternal(stack, apischeme.VersionV1Beta1)
	if err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultStackSpec(stack)

	// Capture stack for closure
	stackForUpdate := stack
	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    stackDoc.Metadata.Name,
		cgroupPath: &stackDoc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateStackCgroup,
		updateErr:  errdefs.ErrUpdateStackMetadata,
		logLabel:   "stack",
		updateMetadata: func() error {
			// Update internal model's CgroupPath from external model
			stackForUpdate.Status.CgroupPath = stackDoc.Status.CgroupPath
			return r.UpdateStackMetadata(stackForUpdate)
		},
	})
	if ensureErr != nil {
		return intmodel.Stack{}, ensureErr
	}

	// Update internal model's CgroupPath from external model after ensureCgroupInternal
	stack.Status.CgroupPath = stackDoc.Status.CgroupPath

	// Always update metadata after ensureCgroupInternal to ensure CgroupPath is saved
	// (closure may not have been called if cgroup already existed with path)
	if stackDoc.Status.CgroupPath != "" {
		if err := r.UpdateStackMetadata(stack); err != nil {
			return intmodel.Stack{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, err)
		}
	}

	return stack, nil
}

func (r *Exec) provisionNewCell(cell intmodel.Cell) (intmodel.Cell, error) {
	// Update cell metadata
	if err := r.UpdateCellMetadata(cell); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}

	// Create cell cgroup
	cgroupPath, err := r.createCellCgroup(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Update internal model with cgroup path
	cell.Status.CgroupPath = cgroupPath

	// Create pause container and all containers for the cell
	_, err = r.createCellContainers(cell)
	if err != nil {
		return intmodel.Cell{}, err
	}

	// Update cell state in internal model
	cell.Status.State = intmodel.CellStateReady

	// Update cell metadata
	if err = r.UpdateCellMetadata(cell); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}

	return cell, nil
}

func (r *Exec) createCellCgroup(cell intmodel.Cell) (string, error) {
	// Extract realm, space, and stack names from cell and validate
	if cell.Spec.RealmName == "" {
		return "", errdefs.ErrRealmNameRequired
	}
	if cell.Spec.SpaceName == "" {
		return "", errdefs.ErrSpaceNameRequired
	}
	if cell.Spec.StackName == "" {
		return "", errdefs.ErrStackNameRequired
	}

	spec := cgroups.DefaultCellSpec(cell)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	var err error
	if err = r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	spec, _, err = r.buildCgroupPath(spec)
	if err != nil {
		return "", err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateCellCgroup, createErr)
	}

	r.logger.InfoContext(
		r.ctx,
		"created cell cgroup",
		"cell",
		cell.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) ensureCellCgroup(cell intmodel.Cell) (intmodel.Cell, error) {
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.Cell{}, errdefs.ErrRealmNameRequired
	}
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.Cell{}, errdefs.ErrSpaceNameRequired
	}
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.Cell{}, errdefs.ErrStackNameRequired
	}

	cellDoc, err := apischeme.BuildCellExternalFromInternal(cell, apischeme.VersionV1Beta1)
	if err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultCellSpec(cell)

	// Capture cell for closure
	cellForUpdate := cell
	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    cellDoc.Metadata.Name,
		cgroupPath: &cellDoc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateStackCgroup,
		updateErr:  errdefs.ErrUpdateStackMetadata,
		logLabel:   "stack",
		updateMetadata: func() error {
			// Update internal model's CgroupPath from external model
			cellForUpdate.Status.CgroupPath = cellDoc.Status.CgroupPath
			return r.UpdateCellMetadata(cellForUpdate)
		},
	})
	if ensureErr != nil {
		return intmodel.Cell{}, ensureErr
	}

	// Update internal model's CgroupPath from external model after ensureCgroupInternal
	cell.Status.CgroupPath = cellDoc.Status.CgroupPath

	// Always update metadata after ensureCgroupInternal to ensure CgroupPath is saved
	// (closure may not have been called if cgroup already existed with path)
	if cellDoc.Status.CgroupPath != "" {
		if err := r.UpdateCellMetadata(cell); err != nil {
			return intmodel.Cell{}, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
		}
	}

	return cell, nil
}

// createCellContainers creates the root container and all containers defined in the Cell.
// The root container is created first, then all containers in cell.Spec.Containers are created.
func (r *Exec) createCellContainers(cell intmodel.Cell) (containerd.Container, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return nil, errdefs.ErrCellNameRequired
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return nil, errdefs.ErrCellIDRequired
	}

	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmName, spaceName)
	if cniErr != nil {
		return nil, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values
	ctrCtx := context.Background()

	// Initialize ctr client if needed
	// Use background context for client creation to avoid cancellation issues
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return nil, fmt.Errorf("failed to get realm: %w", err)
	}

	namespace := internalRealm.Spec.Namespace
	if namespace == "" {
		return nil, fmt.Errorf("realm %q has no namespace", realmName)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(namespace)

	// Generate container ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerName(spaceName, stackName, cellID)
	if err != nil {
		return nil, fmt.Errorf("failed to build root container name: %w", err)
	}

	rootContainerSpec, err := r.ensureCellRootContainerSpec(cell)
	if err != nil {
		return nil, err
	}

	rootLabels := buildRootContainerLabels(cell)
	containerSpec := ctrutil.BuildRootContainerSpec(rootContainerSpec, rootLabels)

	container, err := r.ctrClient.CreateContainer(ctrCtx, containerSpec)
	if err != nil {
		logFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		logFields = append(
			logFields,
			"space",
			spaceName,
			"realm",
			realmName,
			"cniConfig",
			cniConfigPath,
			"err",
			fmt.Sprintf("%v", err),
		)
		r.logger.ErrorContext(
			r.ctx,
			"failed to create root container",
			logFields...,
		)
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateRootContainer, err)
	}

	infoFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
	infoFields = append(infoFields, "space", spaceName, "realm", realmName, "cniConfig", cniConfigPath)
	r.logger.InfoContext(
		r.ctx,
		"created root container",
		infoFields...,
	)

	// Create all containers defined in the cell
	for i := range cell.Spec.Containers {
		containerSpec := cell.Spec.Containers[i]

		// Ensure container spec has required names
		if containerSpec.CellName == "" {
			containerSpec.CellName = cellID
		}
		if containerSpec.SpaceName == "" {
			containerSpec.SpaceName = spaceName
		}
		if containerSpec.RealmName == "" {
			containerSpec.RealmName = realmName
		}
		if containerSpec.StackName == "" {
			containerSpec.StackName = stackName
		}
		if containerSpec.CNIConfigPath == "" {
			containerSpec.CNIConfigPath = cniConfigPath
		}
		cell.Spec.Containers[i] = containerSpec

		// Build container ID using hierarchical format for containerd operations
		// Don't modify containerSpec.ID in the document - keep it as the base name
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceName,
			containerSpec.StackName,
			containerSpec.CellName,
			containerSpec.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build container name: %w", err)
		}

		// Update container spec with hierarchical ID
		containerSpec.ID = containerID

		// Use container name with hierarchical format for containerd operations
		_, err = r.ctrClient.CreateContainerFromSpec(
			ctrCtx,
			containerSpec,
		)
		if err != nil {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceName,
				"realm",
				realmName,
				"cniConfig",
				containerSpec.CNIConfigPath,
				"err",
				fmt.Sprintf("%v", err),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to create container from cell",
				fields...,
			)
			return nil, fmt.Errorf("failed to create container %s: %w", containerID, err)
		}
	}

	return container, nil
}

// ensureCellContainers ensures the root container and all containers defined in the CellDoc exist.
// The root container is ensured first, then all containers in doc.Spec.Containers are ensured.
// If any container doesn't exist, it is created. Returns the root container or an error.
func (r *Exec) ensureCellContainers(cell intmodel.Cell) (containerd.Container, error) {
	cellName := strings.TrimSpace(cell.Metadata.Name)
	if cellName == "" {
		return nil, errdefs.ErrCellNotFound
	}

	cellID := cell.Spec.ID
	if cellID == "" {
		return nil, errdefs.ErrCellIDRequired
	}

	realmName := cell.Spec.RealmName
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}

	spaceName := cell.Spec.SpaceName
	if spaceName == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	stackName := cell.Spec.StackName
	if stackName == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	cniConfigPath, cniErr := r.resolveSpaceCNIConfigPath(realmName, spaceName)
	if cniErr != nil {
		return nil, fmt.Errorf("failed to resolve space CNI config: %w", cniErr)
	}

	// Create a background context for containerd operations
	// This ensures operations complete even if the parent context is canceled
	// The logger is passed separately, so we don't need to preserve context values
	ctrCtx := context.Background()

	// Initialize ctr client if needed
	// Use background context for client creation to avoid cancellation issues
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(context.Background(), r.logger, r.opts.ContainerdSocket)
	}

	err := r.ctrClient.Connect()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Get realm to access namespace
	lookupRealm := intmodel.Realm{
		Metadata: intmodel.RealmMetadata{
			Name: realmName,
		},
	}
	internalRealm, err := r.GetRealm(lookupRealm)
	if err != nil {
		return nil, fmt.Errorf("failed to get realm: %w", err)
	}

	// Set namespace to realm namespace
	r.ctrClient.SetNamespace(internalRealm.Spec.Namespace)

	// Generate container ID with cell identifier for uniqueness
	containerID, err := naming.BuildRootContainerName(spaceName, stackName, cellID)
	if err != nil {
		return nil, fmt.Errorf("failed to build root container name: %w", err)
	}

	// Declare container variable to be used in both branches
	var container containerd.Container

	// Check if container exists
	exists, err := r.ctrClient.ExistsContainer(ctrCtx, containerID)
	if err != nil {
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName, "err", fmt.Sprintf("%v", err))
		r.logger.ErrorContext(
			r.ctx,
			"failed to check if root container exists",
			fields...,
		)
		return nil, fmt.Errorf("failed to check if root container exists: %w", err)
	}

	if exists {
		// Container exists, load it but continue to process other containers
		var loadErr error
		container, loadErr = r.ctrClient.GetContainer(ctrCtx, containerID)
		if loadErr != nil {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceName, "realm", realmName, "err", fmt.Sprintf("%v", loadErr))
			r.logger.WarnContext(
				r.ctx,
				"root container exists but failed to load",
				fields...,
			)
			return nil, fmt.Errorf("failed to load existing root container: %w", loadErr)
		}
		fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		fields = append(fields, "space", spaceName, "realm", realmName)
		r.logger.DebugContext(
			r.ctx,
			"root container exists",
			fields...,
		)
		// Don't return early - continue to process other containers in the cell
	} else {
		// Container doesn't exist, create it
		createFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		createFields = append(createFields, "space", spaceName, "realm", realmName, "cniConfig", cniConfigPath)
		r.logger.InfoContext(
			r.ctx,
			"root container does not exist, creating",
			createFields...,
		)

		rootContainerSpec, err := r.ensureCellRootContainerSpec(cell)
		if err != nil {
			return nil, err
		}

		rootLabels := buildRootContainerLabels(cell)
		containerSpec := ctrutil.BuildRootContainerSpec(rootContainerSpec, rootLabels)

		var createErr error
		container, createErr = r.ctrClient.CreateContainer(ctrCtx, containerSpec)
		if createErr != nil {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(
				fields,
				"space",
				spaceName,
				"realm",
				realmName,
				"cniConfig",
				cniConfigPath,
				"err",
				fmt.Sprintf("%v", createErr),
			)
			r.logger.ErrorContext(
				r.ctx,
				"failed to create root container",
				fields...,
			)
			return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateRootContainer, createErr)
		}

		ensuredFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		ensuredFields = append(ensuredFields, "space", spaceName, "realm", realmName, "cniConfig", cniConfigPath)
		r.logger.InfoContext(
			r.ctx,
			"ensured root container exists",
			ensuredFields...,
		)
	}

	// Log how many containers we're about to process
	containerCountFields := appendCellLogFields([]any{"cell", cellID}, cellID, cellName)
	containerCountFields = append(
		containerCountFields,
		"space",
		spaceName,
		"realm",
		realmName,
		"stack",
		stackName,
		"containerCount",
		len(cell.Spec.Containers),
	)
	r.logger.DebugContext(
		r.ctx,
		"processing containers from cell",
		containerCountFields...,
	)

	// Ensure all containers defined in the cell exist
	for i := range cell.Spec.Containers {
		containerSpec := cell.Spec.Containers[i]

		// Log which container we're processing
		processFields := appendCellLogFields([]any{"cell", cellID}, cellID, cellName)
		processFields = append(
			processFields,
			"space",
			spaceName,
			"realm",
			realmName,
			"stack",
			stackName,
			"containerName",
			containerSpec.ID,
			"containerIndex",
			i,
		)
		r.logger.DebugContext(
			r.ctx,
			"processing container from cell",
			processFields...,
		)

		if containerSpec.CNIConfigPath == "" {
			containerSpec.CNIConfigPath = cniConfigPath
			cell.Spec.Containers[i] = containerSpec
		}

		// Ensure container spec has required names
		if containerSpec.CellName == "" {
			containerSpec.CellName = cellID
		}
		if containerSpec.SpaceName == "" {
			containerSpec.SpaceName = spaceName
		}
		if containerSpec.RealmName == "" {
			containerSpec.RealmName = realmName
		}
		if containerSpec.StackName == "" {
			containerSpec.StackName = stackName
		}

		// Build container ID using hierarchical format for containerd operations
		// Don't modify containerSpec.ID in the document - keep it as the base name
		containerID, err = naming.BuildContainerName(
			containerSpec.SpaceName,
			containerSpec.StackName,
			containerSpec.CellName,
			containerSpec.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build container name: %w", err)
		}

		// Log the hierarchical container ID we built
		idFields := appendCellLogFields([]any{"cell", cellID}, cellID, cellName)
		idFields = append(
			idFields,
			"space",
			spaceName,
			"realm",
			realmName,
			"stack",
			stackName,
			"containerName",
			containerSpec.ID,
			"hierarchicalID",
			containerID,
		)
		r.logger.DebugContext(
			r.ctx,
			"built hierarchical container ID",
			idFields...,
		)

		// Use container name with hierarchical format for containerd operations
		exists, err = r.ctrClient.ExistsContainer(ctrCtx, containerID)
		if err != nil {
			// Check if the error indicates the container doesn't exist
			// In that case, treat it as "doesn't exist" (false) rather than a fatal error
			if errors.Is(err, ctr.ErrContainerNotFound) {
				// Container doesn't exist, which is fine - we'll create it
				exists = false
			} else {
				// Some other error occurred
				fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				fields = append(
					fields,
					"space",
					spaceName,
					"realm",
					realmName,
					"containerName",
					containerSpec.ID,
					"err",
					fmt.Sprintf("%v", err),
				)
				r.logger.ErrorContext(
					r.ctx,
					"failed to check if container exists",
					fields...,
				)
				return nil, fmt.Errorf("failed to check if container %s exists: %w", containerID, err)
			}
		}

		// Log the existence check result
		existsFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
		existsFields = append(
			existsFields,
			"space",
			spaceName,
			"realm",
			realmName,
			"containerName",
			containerSpec.ID,
			"exists",
			exists,
		)
		r.logger.DebugContext(
			r.ctx,
			"checked container existence",
			existsFields...,
		)

		if !exists {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceName, "realm", realmName, "cniConfig", containerSpec.CNIConfigPath)
			r.logger.InfoContext(
				r.ctx,
				"container does not exist, creating",
				fields...,
			)
			if containerSpec.CNIConfigPath == "" {
				containerSpec.CNIConfigPath = cniConfigPath
				cell.Spec.Containers[i] = containerSpec
			}

			// Container doesn't exist, create it
			// Update container spec with hierarchical ID
			containerSpec.ID = containerID

			// Log container spec details before creation
			createSpecFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			createSpecFields = append(
				createSpecFields,
				"space",
				spaceName,
				"realm",
				realmName,
				"stack",
				stackName,
				"containerName",
				containerSpec.ID,
				"image",
				containerSpec.Image,
				"command",
				containerSpec.Command,
			)
			r.logger.DebugContext(
				r.ctx,
				"creating container with spec",
				createSpecFields...,
			)

			createdContainer, containerCreateErr := r.ctrClient.CreateContainerFromSpec(
				ctrCtx,
				containerSpec,
			)
			if containerCreateErr != nil {
				// Check if the error indicates the container already exists
				// This can happen due to race conditions where the container
				// was created between the existence check and creation attempt
				errMsg := containerCreateErr.Error()
				if strings.Contains(errMsg, "container already exists") {
					debugFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
					debugFields = append(debugFields, "space", spaceName, "realm", realmName)
					r.logger.DebugContext(
						r.ctx,
						"container already exists (race condition), skipping",
						debugFields...,
					)
					continue
				}
				errorFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				errorFields = append(
					errorFields,
					"space",
					spaceName,
					"realm",
					realmName,
					"cniConfig",
					containerSpec.CNIConfigPath,
					"err",
					fmt.Sprintf("%v", containerCreateErr),
				)
				r.logger.ErrorContext(
					r.ctx,
					"failed to create container from cell",
					errorFields...,
				)
				return nil, fmt.Errorf("failed to create container %s: %w", containerID, containerCreateErr)
			}
			if createdContainer != nil {
				successFields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
				successFields = append(
					successFields,
					"space",
					spaceName,
					"realm",
					realmName,
					"containerName",
					containerSpec.ID,
				)
				r.logger.InfoContext(
					r.ctx,
					"created container from cell",
					successFields...,
				)
			}
		} else {
			fields := appendCellLogFields([]any{"id", containerID}, cellID, cellName)
			fields = append(fields, "space", spaceName, "realm", realmName)
			r.logger.DebugContext(
				r.ctx,
				"container exists",
				fields...,
			)
		}
	}

	return container, nil
}

func (r *Exec) ensureCellRootContainerSpec(cell intmodel.Cell) (intmodel.ContainerSpec, error) {
	// Extract cell ID and validate
	cellID := strings.TrimSpace(cell.Spec.ID)
	if cellID == "" {
		return intmodel.ContainerSpec{}, errdefs.ErrCellIDRequired
	}

	// Extract and validate realm name
	realmName := strings.TrimSpace(cell.Spec.RealmName)
	if realmName == "" {
		return intmodel.ContainerSpec{}, errdefs.ErrRealmNameRequired
	}

	// Extract and validate space name
	spaceName := strings.TrimSpace(cell.Spec.SpaceName)
	if spaceName == "" {
		return intmodel.ContainerSpec{}, errdefs.ErrSpaceNameRequired
	}

	// Extract and validate stack name
	stackName := strings.TrimSpace(cell.Spec.StackName)
	if stackName == "" {
		return intmodel.ContainerSpec{}, errdefs.ErrStackNameRequired
	}

	// Resolve CNI config path
	cniConfigPath, err := r.resolveSpaceCNIConfigPath(realmName, spaceName)
	if err != nil {
		return intmodel.ContainerSpec{}, fmt.Errorf("failed to resolve space CNI config: %w", err)
	}

	// Generate container ID
	containerID, err := naming.BuildRootContainerName(spaceName, stackName, cellID)
	if err != nil {
		return intmodel.ContainerSpec{}, fmt.Errorf("failed to build root container name: %w", err)
	}

	// Work with internal model's RootContainer if it exists
	var rootSpec intmodel.ContainerSpec
	if cell.Spec.RootContainer != nil {
		rootSpec = *cell.Spec.RootContainer
	} else {
		// Create default root container spec
		rootSpec = ctrutil.DefaultRootContainerSpec(
			containerID,
			cellID,
			realmName,
			spaceName,
			stackName,
			cniConfigPath,
		)
	}

	// Ensure required fields are set
	rootSpec.Root = true
	if rootSpec.ID == "" {
		rootSpec.ID = containerID
	}
	if rootSpec.CellName == "" {
		rootSpec.CellName = cellID
	}
	if rootSpec.RealmName == "" {
		rootSpec.RealmName = realmName
	}
	if rootSpec.SpaceName == "" {
		rootSpec.SpaceName = spaceName
	}
	if rootSpec.StackName == "" {
		rootSpec.StackName = stackName
	}
	if rootSpec.CNIConfigPath == "" {
		rootSpec.CNIConfigPath = cniConfigPath
	}

	return rootSpec, nil
}
