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
	specUpdated := doc.Spec.CNIConfigPath != confPath
	doc.Spec.CNIConfigPath = confPath
	if specUpdated {
		if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
			return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateSpaceMetadata, updateErr)
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
	doc.Spec.CNIConfigPath = confPath
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

func (r *Exec) ensureSpaceCgroup(doc *v1beta1.SpaceDoc, realm *v1beta1.RealmDoc) (*v1beta1.SpaceDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultSpaceSpec(realm, doc)

	err := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    doc.Metadata.Name,
		cgroupPath: &doc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateSpaceCgroup,
		updateErr:  errdefs.ErrUpdateSpaceMetadata,
		logLabel:   "space",
		updateMetadata: func() error {
			return r.UpdateSpaceMetadata(doc)
		},
	})
	if err != nil {
		return nil, err
	}

	return doc, nil
}

func (r *Exec) createSpaceCgroup(doc *v1beta1.SpaceDoc, realm *v1beta1.RealmDoc) error {
	spec := cgroups.DefaultSpaceSpec(realm, doc)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	spec, _, err := r.buildCgroupPath(spec)
	if err != nil {
		return err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrCreateSpaceCgroup, createErr)
	}

	doc.Status.CgroupPath = cgroupPath
	if updateErr := r.UpdateSpaceMetadata(doc); updateErr != nil {
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
		cgroupPath,
	)
	return nil
}

func (r *Exec) createRealmCgroup(doc *v1beta1.RealmDoc) (string, error) {
	spec := cgroups.DefaultRealmSpec(doc)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	// Build the cgroup path
	spec, _, err := r.buildCgroupPath(spec)
	if err != nil {
		return "", err
	}

	// Create the cgroup
	cgroupPath, createErr := r.createCgroupInternal(spec)
	if createErr != nil {
		return "", fmt.Errorf("%w: %w", errdefs.ErrCreateRealmCgroup, createErr)
	}

	doc.Status.CgroupPath = cgroupPath
	r.logger.InfoContext(
		r.ctx,
		"created realm cgroup",
		"realm",
		doc.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
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

	err := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    doc.Metadata.Name,
		cgroupPath: &doc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateRealmCgroup,
		updateErr:  errdefs.ErrUpdateRealmMetadata,
		logLabel:   "realm",
		updateMetadata: func() error {
			return r.UpdateRealmMetadata(doc)
		},
	})
	if err != nil {
		return nil, err
	}

	return doc, nil
}

func (r *Exec) provisionNewStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	// Update realm metadata
	if err := r.UpdateStackMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, err)
	}

	// Create realm cgroup
	cgroupPath, err := r.createStackCgroup(doc)
	if err != nil {
		return nil, err
	}

	// Persist cgroup path in metadata
	doc.Status.CgroupPath = cgroupPath

	// Update realm state
	doc.Status.State = v1beta1.StackStateReady
	if err = r.UpdateStackMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateStackMetadata, err)
	}
	return doc, nil
}

func (r *Exec) createStackCgroup(doc *v1beta1.StackDoc) (string, error) {
	if doc.Spec.RealmID == "" {
		return "", errdefs.ErrRealmNameRequired
	}
	if doc.Spec.SpaceID == "" {
		return "", errdefs.ErrSpaceNameRequired
	}

	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: doc.Spec.RealmID,
		},
	})
	if err != nil {
		return "", err
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: doc.Spec.SpaceID,
		},
	})
	if err != nil {
		return "", err
	}

	spec := cgroups.DefaultStackSpec(realmDoc, spaceDoc, doc)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
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

	doc.Status.CgroupPath = cgroupPath
	r.logger.InfoContext(
		r.ctx,
		"created stack cgroup",
		"stack",
		doc.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) ensureStackCgroup(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	if doc.Spec.RealmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	if doc.Spec.SpaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}

	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: doc.Spec.RealmID,
		},
	})
	if err != nil {
		return nil, err
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: doc.Spec.SpaceID,
		},
	})
	if err != nil {
		return nil, err
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultStackSpec(realmDoc, spaceDoc, doc)

	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    doc.Metadata.Name,
		cgroupPath: &doc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateStackCgroup,
		updateErr:  errdefs.ErrUpdateStackMetadata,
		logLabel:   "stack",
		updateMetadata: func() error {
			return r.UpdateStackMetadata(doc)
		},
	})
	if ensureErr != nil {
		return nil, ensureErr
	}

	return doc, nil
}

func (r *Exec) provisionNewCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	// Update realm metadata
	if err := r.UpdateCellMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}

	// Create realm cgroup
	cgroupPath, err := r.createCellCgroup(doc)
	if err != nil {
		return nil, err
	}

	// Persist cgroup path in metadata
	doc.Status.CgroupPath = cgroupPath

	// Create pause container and all containers for the cell
	_, err = r.createCellContainers(doc)
	if err != nil {
		return nil, err
	}

	// Update realm state
	doc.Status.State = v1beta1.CellStateReady
	if err = r.UpdateCellMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateCellMetadata, err)
	}
	return doc, nil
}

func (r *Exec) createCellCgroup(doc *v1beta1.CellDoc) (string, error) {
	if doc.Spec.RealmID == "" {
		return "", errdefs.ErrRealmNameRequired
	}
	if doc.Spec.SpaceID == "" {
		return "", errdefs.ErrSpaceNameRequired
	}
	if doc.Spec.StackID == "" {
		return "", errdefs.ErrStackNameRequired
	}

	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: doc.Spec.RealmID,
		},
	})
	if err != nil {
		return "", err
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: doc.Spec.SpaceID,
		},
	})
	if err != nil {
		return "", err
	}
	stackDoc, err := r.GetStack(&v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: doc.Spec.StackID,
		},
	})
	if err != nil {
		return "", err
	}

	spec := cgroups.DefaultCellSpec(
		realmDoc,
		spaceDoc,
		stackDoc,
		doc,
	)

	// Ensure client is initialized and connected
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
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

	doc.Status.CgroupPath = cgroupPath
	r.logger.InfoContext(
		r.ctx,
		"created stack cgroup",
		"stack",
		doc.Metadata.Name,
		"path",
		cgroupPath,
	)
	return cgroupPath, nil
}

func (r *Exec) ensureCellCgroup(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	if doc.Spec.RealmID == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	if doc.Spec.SpaceID == "" {
		return nil, errdefs.ErrSpaceNameRequired
	}
	if doc.Spec.StackID == "" {
		return nil, errdefs.ErrStackNameRequired
	}

	realmDoc, err := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: doc.Spec.RealmID,
		},
	})
	if err != nil {
		return nil, err
	}

	spaceDoc, err := r.GetSpace(&v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{
			Name: doc.Spec.SpaceID,
		},
	})
	if err != nil {
		return nil, err
	}

	stackDoc, err := r.GetStack(&v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{
			Name: doc.Spec.StackID,
		},
	})
	if err != nil {
		return nil, err
	}

	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err = r.ctrClient.Connect(); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()

	spec := cgroups.DefaultCellSpec(realmDoc, spaceDoc, stackDoc, doc)

	ensureErr := r.ensureCgroupInternal(ensureCgroupParams{
		spec:       spec,
		docName:    doc.Metadata.Name,
		cgroupPath: &doc.Status.CgroupPath,
		createErr:  errdefs.ErrCreateStackCgroup,
		updateErr:  errdefs.ErrUpdateStackMetadata,
		logLabel:   "stack",
		updateMetadata: func() error {
			return r.UpdateCellMetadata(doc)
		},
	})
	if ensureErr != nil {
		return nil, ensureErr
	}

	return doc, nil
}
