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
)

type Runner interface {
	BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)

	GetRealm(realm intmodel.Realm) (intmodel.Realm, error)
	ListRealms() ([]intmodel.Realm, error)
	CreateRealm(realm intmodel.Realm) (intmodel.Realm, error)
	EnsureRealm(realm intmodel.Realm) (intmodel.Realm, error)
	UpdateRealm(realm intmodel.Realm) (intmodel.Realm, error)
	ExistsRealmContainerdNamespace(namespace string) (bool, error)
	DeleteRealm(realm intmodel.Realm) error

	GetSpace(space intmodel.Space) (intmodel.Space, error)
	ListSpaces(realmName string) ([]intmodel.Space, error)
	CreateSpace(space intmodel.Space) (intmodel.Space, error)
	EnsureSpace(space intmodel.Space) (intmodel.Space, error)
	UpdateSpace(space intmodel.Space) (intmodel.Space, error)
	ExistsSpaceCNIConfig(space intmodel.Space) (bool, error)
	DeleteSpace(space intmodel.Space) error

	GetCell(cell intmodel.Cell) (intmodel.Cell, error)
	ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error)
	ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error)
	CreateCell(cell intmodel.Cell) (intmodel.Cell, error)
	EnsureCell(cell intmodel.Cell) (intmodel.Cell, error)
	StartCell(cell intmodel.Cell) error
	StopCell(cell intmodel.Cell) error
	StartContainer(cell intmodel.Cell, containerID string) error
	StopContainer(cell intmodel.Cell, containerID string) error
	KillCell(cell intmodel.Cell) error
	KillContainer(cell intmodel.Cell, containerID string) error
	DeleteContainer(cell intmodel.Cell, containerID string) error
	CreateContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error)
	EnsureContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error)
	UpdateCell(cell intmodel.Cell) (intmodel.Cell, error)
	RecreateCell(cell intmodel.Cell) (intmodel.Cell, error)
	UpdateContainer(cell intmodel.Cell, container intmodel.ContainerSpec) (intmodel.Cell, error)
	UpdateCellMetadata(cell intmodel.Cell) error
	ExistsCellRootContainer(cell intmodel.Cell) (bool, error)
	DeleteCell(cell intmodel.Cell) error

	GetStack(stack intmodel.Stack) (intmodel.Stack, error)
	ListStacks(realmName, spaceName string) ([]intmodel.Stack, error)
	CreateStack(stack intmodel.Stack) (intmodel.Stack, error)
	EnsureStack(stack intmodel.Stack) (intmodel.Stack, error)
	UpdateStack(stack intmodel.Stack) (intmodel.Stack, error)
	DeleteStack(stack intmodel.Stack) error

	ExistsCgroup(doc any) (bool, error)

	PurgeRealm(realm intmodel.Realm) error
	PurgeSpace(space intmodel.Space) error
	PurgeStack(stack intmodel.Stack) error
	PurgeCell(cell intmodel.Cell) error
	PurgeContainer(realm intmodel.Realm, containerID string) error

	Close() error
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

func (r *Exec) Close() error {
	if r.ctrClient == nil {
		return nil
	}
	return r.ctrClient.Close()
}

// ensureClientConnected ensures the containerd client is initialized and connected.
// It creates a new client if needed, and reconnects if the connection was closed.
func (r *Exec) ensureClientConnected() error {
	if r.ctrClient == nil {
		r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
	}
	return r.ctrClient.Connect()
}
