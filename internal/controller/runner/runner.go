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
	"log/slog"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Runner interface {
	BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)

	GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	CreateRealm(spec *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	ExistsRealmContainerdNamespace(namespace string) (bool, error)

	GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error)

	GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)
	CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)

	GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
	CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options

	ctrClient ctr.Client

	cniConf *cni.Conf
}

type Options struct {
	ContainerdSocket string
	RunPath          string
	CniConf          cni.Conf
}

func NewRunner(ctx context.Context, logger *slog.Logger, opts Options) Runner {
	return &Exec{
		ctx:     ctx,
		logger:  logger,
		opts:    opts,
		cniConf: &opts.CniConf,
	}
}

func (r *Exec) GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Get realm metadata
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonRealmMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	realmDoc, err := metadata.ReadMetadata[v1beta1.RealmDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrRealmNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	return &realmDoc, nil
}

func (r *Exec) CreateRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	rDoc, err := r.GetRealm(doc)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Realm found, check if namespace exists
	if rDoc != nil {
		doc, err = r.ensureRealmContainerdNamespace(rDoc)
		if err != nil {
			return nil, err
		}
		return r.ensureRealmCgroup(doc)
	}

	// Realm not found, normalize request to internal then emit external doc for storage
	var internalRealm intmodel.Realm
	var version v1beta1.Version
	internalRealm, version, err = apischeme.NormalizeRealm(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	internalRealm.Status.State = intmodel.RealmStateCreating
	rDocNewExt, err := apischeme.BuildRealmExternalFromInternal(internalRealm, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	rDocNew := &rDocNewExt

	return r.provisionNewRealm(rDocNew)
}

func (r *Exec) ExistsRealmContainerdNamespace(namespace string) (bool, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()
	return r.ctrClient.ExistsNamespace(namespace)
}

func (r *Exec) GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	// Get space metadata
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonSpaceMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	spaceDoc, err := metadata.ReadMetadata[v1beta1.SpaceDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrSpaceNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}
	return &spaceDoc, nil
}

func (r *Exec) CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	sDoc, err := r.GetSpace(doc)
	if err != nil && !errors.Is(err, errdefs.ErrSpaceNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetSpace, err)
	}

	realmName := doc.Spec.RealmID
	if sDoc != nil && sDoc.Spec.RealmID != "" {
		realmName = sDoc.Spec.RealmID
	}
	if realmName == "" {
		return nil, errdefs.ErrRealmNameRequired
	}
	realmDoc, realmErr := r.GetRealm(&v1beta1.RealmDoc{
		Metadata: v1beta1.RealmMetadata{
			Name: realmName,
		},
	})
	if realmErr != nil {
		return nil, realmErr
	}

	// Space found, ensure CNI config exists
	if sDoc != nil {
		spaceDocEnsured, ensureErr := r.ensureSpaceCNIConfig(sDoc)
		if ensureErr != nil {
			return nil, ensureErr
		}
		return r.ensureSpaceCgroup(spaceDocEnsured, realmDoc)
	}

	// Space not found, normalize request to internal then emit external doc for storage
	var internalSpace intmodel.Space
	var version v1beta1.Version
	internalSpace, version, err = apischeme.NormalizeSpace(*doc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	internalSpace.Status.State = intmodel.SpaceStateCreating
	sDocNewExt, err := apischeme.BuildSpaceExternalFromInternal(internalSpace, version)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	sDocNew := &sDocNewExt

	return r.provisionNewSpace(sDocNew)
}

func (r *Exec) ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error) {
	if doc == nil {
		return false, errdefs.ErrSpaceDocRequired
	}
	mgr, err := cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	confPath, err := fs.SpaceNetworkConfigPath(r.opts.RunPath, doc.Metadata.Name)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	networkName, err := naming.BuildSpaceNetworkName(doc)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	exists, _, err := mgr.ExistsNetworkConfig(networkName, confPath)
	if err != nil && !errors.Is(err, errdefs.ErrNetworkNotFound) {
		return false, fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}
	return exists, nil
}

func (r *Exec) GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	// Get stack metadata
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonStackMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	stackDoc, err := metadata.ReadMetadata[v1beta1.StackDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrStackNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}
	return &stackDoc, nil
}

func (r *Exec) CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	sDoc, err := r.GetStack(doc)
	if err != nil && !errors.Is(err, errdefs.ErrStackNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetStack, err)
	}

	// Stack found, ensure cgroup exists
	if sDoc != nil {
		return r.ensureStackCgroup(sDoc)
	}

	// Stack not found, create new stack
	return r.provisionNewStack(doc)
}

func (r *Exec) GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	// Get cell metadata
	metadataRunPath := filepath.Join(r.opts.RunPath, consts.KukeonCellMetadataSubDir, doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, consts.KukeonMetadataFile)
	cellDoc, err := metadata.ReadMetadata[v1beta1.CellDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrCellNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}
	return &cellDoc, nil
}

func (r *Exec) CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	cDoc, err := r.GetCell(doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetCell, err)
	}

	// Cell found, ensure cgroup exists
	if cDoc != nil {
		return r.ensureCellCgroup(cDoc)
	}

	// Cell not found, create new cell
	return r.provisionNewCell(doc)
}

func (r *Exec) BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error) {
	// Delegate to cni package bootstrap; empty params will default.
	return cni.BootstrapCNI(cfgDir, cacheDir, binDir)
}
