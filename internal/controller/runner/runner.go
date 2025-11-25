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
	"log/slog"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Runner interface {
	BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)

	GetRealm(doc *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	CreateRealm(spec *v1beta1.RealmDoc) (*v1beta1.RealmDoc, error)
	ExistsRealmContainerdNamespace(namespace string) (bool, error)
	DeleteRealm(doc *v1beta1.RealmDoc) (DeleteRealmOutcome, error)

	GetSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	CreateSpace(doc *v1beta1.SpaceDoc) (*v1beta1.SpaceDoc, error)
	ExistsSpaceCNIConfig(doc *v1beta1.SpaceDoc) (bool, error)
	DeleteSpace(doc *v1beta1.SpaceDoc) error

	GetCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
	CreateCell(doc *v1beta1.CellDoc) (*v1beta1.CellDoc, error)
	StartCell(doc *v1beta1.CellDoc) error
	StopCell(doc *v1beta1.CellDoc) error
	StopContainer(doc *v1beta1.CellDoc, containerID string) error
	KillCell(doc *v1beta1.CellDoc) error
	KillContainer(doc *v1beta1.CellDoc, containerID string) error
	DeleteContainer(doc *v1beta1.CellDoc, containerID string) error
	UpdateCellMetadata(cell intmodel.Cell) error
	ExistsCellRootContainer(doc *v1beta1.CellDoc) (bool, error)
	DeleteCell(doc *v1beta1.CellDoc) error

	GetStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)
	CreateStack(doc *v1beta1.StackDoc) (*v1beta1.StackDoc, error)
	DeleteStack(doc *v1beta1.StackDoc) error

	ExistsCgroup(doc any) (bool, error)

	PurgeRealm(doc *v1beta1.RealmDoc) error
	PurgeSpace(doc *v1beta1.SpaceDoc) error
	PurgeStack(doc *v1beta1.StackDoc) error
	PurgeCell(doc *v1beta1.CellDoc) error
	PurgeContainer(containerID, namespace string) error
}

// DeleteRealmOutcome reports which realm resources were deleted successfully.
type DeleteRealmOutcome struct {
	MetadataDeleted            bool
	CgroupDeleted              bool
	ContainerdNamespaceDeleted bool
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

func (r *Exec) BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error) {
	// Delegate to cni package bootstrap; empty params will default.
	return cni.BootstrapCNI(cfgDir, cacheDir, binDir)
}
