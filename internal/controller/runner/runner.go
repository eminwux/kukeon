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
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Runner interface {
	CreateRealm(spec *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	NamespaceExists(namespace string) (bool, error)
	GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
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

func (r *Exec) CreateRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	r.logger.Debug("run-path", "run-path", r.opts.RunPath)

	rDoc, err := r.GetRealm(doc)
	if err != nil && !errors.Is(err, errdefs.ErrRealmNotFound) {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}

	// Realm found, check if namespace exists
	if rDoc != nil {
		return r.ensureRealmNamespace(rDoc)
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

func (r *Exec) UpdateRealmMetadata(doc *v1beta1.RealmDoc) error {
	metadataRunPath := filepath.Join(r.opts.RunPath, "realms", doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, "metadata.json")
	err := metadata.WriteMetadata(r.ctx, r.logger, doc, metadataFilePath)
	if err != nil {
		r.logger.Error("failed to write metadata", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrWriteMetadata, err)
	}
	return nil
}

func (r *Exec) ensureRealmNamespace(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
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

func (r *Exec) provisionNewRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Update realm metadata
	if err := r.UpdateRealmMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}

	// Create realm namespace
	if err := r.CreateRealmNamespace(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrCreateRealmNamespace, err)
	}

	// Update realm state
	doc.Status.State = v1beta1.RealmStateReady
	if err := r.UpdateRealmMetadata(doc); err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrUpdateRealmMetadata, err)
	}
	return doc, nil
}

func (r *Exec) CreateRealmNamespace(doc *v1beta1.RealmDoc) error {
	// Create realm namespace
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

func (r *Exec) GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error) {
	// Get realm metadata
	metadataRunPath := filepath.Join(r.opts.RunPath, "realms", doc.Metadata.Name)
	metadataFilePath := filepath.Join(metadataRunPath, "metadata.json")
	realmDoc, err := metadata.ReadMetadata[v1beta1.RealmDoc](r.ctx, r.logger, metadataFilePath)
	if err != nil {
		if errors.Is(err, errdefs.ErrMissingMetadataFile) {
			return nil, errdefs.ErrRealmNotFound
		}
		return nil, fmt.Errorf("%w: %w", errdefs.ErrGetRealm, err)
	}
	return &realmDoc, nil
}

func (r *Exec) NamespaceExists(namespace string) (bool, error) {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	if err := r.ctrClient.Connect(); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrConnectContainerd, err)
	}
	defer r.ctrClient.Close()
	return r.ctrClient.ExistsNamespace(namespace)
}

func (r *Exec) CreateSpace(_ string, _ string) error {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}

	var err error
	err = r.ctrClient.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to containerd: %w", err)
	}
	defer r.ctrClient.Close()

	var mgr *cni.Manager
	mgr, err = cni.NewManager(
		r.cniConf.CniBinDir,
		r.cniConf.CniConfigFile,
		r.cniConf.CniConfigDir,
		r.cniConf.CniCacheDir,
	)
	if err != nil {
		r.logger.Error("could not initializa cni manager")
		return fmt.Errorf("%w: %w", errdefs.ErrInitCniManager, err)
	}

	_, _, err = mgr.NetworkExists("hola")
	if err != nil {
		r.logger.Error("could not check if network exists", "err", fmt.Sprintf("%v", err))
		return fmt.Errorf("%w: %w", errdefs.ErrCheckNetworkExists, err)
	}

	return nil
}
