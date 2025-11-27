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

package controller

import (
	"context"
	"log/slog"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type Controller interface {
	Bootstrap() (BootstrapReport, error)
	CreateRealm(realm intmodel.Realm) (CreateRealmResult, error)
	CreateSpace(space intmodel.Space) (CreateSpaceResult, error)
	CreateStack(stack intmodel.Stack) (CreateStackResult, error)
	CreateCell(cell intmodel.Cell) (CreateCellResult, error)
	CreateContainer(container intmodel.Container) (CreateContainerResult, error)
	DeleteRealm(realm intmodel.Realm, force, cascade bool) (DeleteRealmResult, error)
	DeleteSpace(space intmodel.Space, force, cascade bool) (DeleteSpaceResult, error)
	DeleteStack(stack intmodel.Stack, force, cascade bool) (DeleteStackResult, error)
	DeleteCell(cell intmodel.Cell) (DeleteCellResult, error)
	DeleteContainer(container intmodel.Container) (DeleteContainerResult, error)
	GetRealm(doc *v1beta1.RealmDoc) (GetRealmResult, error)
	ListRealms() ([]*v1beta1.RealmDoc, error)
	GetSpace(doc *v1beta1.SpaceDoc) (GetSpaceResult, error)
	ListSpaces(realmName string) ([]*v1beta1.SpaceDoc, error)
	GetStack(doc *v1beta1.StackDoc) (GetStackResult, error)
	ListStacks(realmName, spaceName string) ([]*v1beta1.StackDoc, error)
	GetCell(doc *v1beta1.CellDoc) (GetCellResult, error)
	ListCells(realmName, spaceName, stackName string) ([]*v1beta1.CellDoc, error)
	GetContainer(doc *v1beta1.ContainerDoc) (GetContainerResult, error)
	ListContainers(realmName, spaceName, stackName, cellName string) ([]*v1beta1.ContainerSpec, error)
	StartCell(doc *v1beta1.CellDoc) (StartCellResult, error)
	StartContainer(doc *v1beta1.ContainerDoc) (StartContainerResult, error)
	StopCell(doc *v1beta1.CellDoc) (StopCellResult, error)
	StopContainer(doc *v1beta1.ContainerDoc) (StopContainerResult, error)
	KillCell(doc *v1beta1.CellDoc) (KillCellResult, error)
	KillContainer(doc *v1beta1.ContainerDoc) (KillContainerResult, error)
	PurgeRealm(doc *v1beta1.RealmDoc, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(doc *v1beta1.SpaceDoc, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(doc *v1beta1.StackDoc, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(doc *v1beta1.CellDoc, force, cascade bool) (PurgeCellResult, error)
	PurgeContainer(doc *v1beta1.ContainerDoc) (PurgeContainerResult, error)
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options
	runner runner.Runner
}

// BootstrapReport moved to bootstrap.go

type Options struct {
	RunPath          string
	ContainerdSocket string
}

func NewControllerExec(ctx context.Context, logger *slog.Logger, opts Options) *Exec {
	return &Exec{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		runner: runner.NewRunner(ctx, logger, runner.Options{
			ContainerdSocket: opts.ContainerdSocket,
			RunPath:          opts.RunPath,
		}),
	}
}

func (b *Exec) Bootstrap() (BootstrapReport, error) {
	b.logger.DebugContext(b.ctx, "bootstrapping kukeon", "options", b.opts)

	var err error
	report := BootstrapReport{
		RealmName:                consts.KukeonRealmName,
		RealmContainerdNamespace: consts.KukeonRealmNamespace,
		RunPath:                  b.opts.RunPath,
	}

	// Bootstrap CNI environment (use defaults by passing empty values)
	report, err = b.bootstrapCNI(report)
	if err != nil {
		return report, err
	}

	// Bootstrap realm
	report, err = b.bootstrapRealm(report)
	if err != nil {
		return report, err
	}

	report.RealmCreated = !report.RealmMetadataExistsPre && report.RealmMetadataExistsPost
	report.RealmContainerdNamespaceCreated = !report.RealmContainerdNamespaceExistsPre &&
		report.RealmContainerdNamespaceExistsPost

	// Bootstrap default space and its CNI network
	report, err = b.bootstrapSpace(report)
	if err != nil {
		return report, err
	}

	// Space outcomes
	report.SpaceCreated = !report.SpaceMetadataExistsPre && report.SpaceMetadataExistsPost
	report.SpaceCNINetworkCreated = !report.SpaceCNINetworkExistsPre && report.SpaceCNINetworkExistsPost

	// Bootstrap default stack
	report, err = b.bootstrapStack(report)
	if err != nil {
		return report, err
	}

	// Bootstrap default cell
	report, err = b.bootstrapCell(report)
	if err != nil {
		return report, err
	}

	return report, nil
}

// moved to bootstrap.go
