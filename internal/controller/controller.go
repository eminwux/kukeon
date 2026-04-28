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
	GetRealm(realm intmodel.Realm) (GetRealmResult, error)
	ListRealms() ([]intmodel.Realm, error)
	GetSpace(space intmodel.Space) (GetSpaceResult, error)
	ListSpaces(realmName string) ([]intmodel.Space, error)
	GetStack(stack intmodel.Stack) (GetStackResult, error)
	ListStacks(realmName, spaceName string) ([]intmodel.Stack, error)
	GetCell(cell intmodel.Cell) (GetCellResult, error)
	ListCells(realmName, spaceName, stackName string) ([]intmodel.Cell, error)
	GetContainer(container intmodel.Container) (GetContainerResult, error)
	ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error)
	StartCell(cell intmodel.Cell) (StartCellResult, error)
	StartContainer(container intmodel.Container) (StartContainerResult, error)
	StopCell(cell intmodel.Cell) (StopCellResult, error)
	StopContainer(container intmodel.Container) (StopContainerResult, error)
	KillCell(cell intmodel.Cell) (KillCellResult, error)
	KillContainer(container intmodel.Container) (KillContainerResult, error)
	PurgeRealm(realm intmodel.Realm, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(space intmodel.Space, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(stack intmodel.Stack, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(cell intmodel.Cell, force, cascade bool) (PurgeCellResult, error)
	PurgeContainer(container intmodel.Container) (PurgeContainerResult, error)
	RefreshAll() (RefreshResult, error)
	Uninstall(opts UninstallOptions) (UninstallReport, error)
	Close() error
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options
	runner runner.Runner
}

type Options struct {
	RunPath          string
	ContainerdSocket string
	// KukeondImage is the container image for the kukeond system cell. If empty,
	// bootstrap will skip provisioning the system cell (but still provision the
	// system realm/space/stack).
	KukeondImage string
	// KukeondSocket is the unix socket path kukeond serves on. Used by bootstrap
	// to build the bind-mount for the system cell.
	KukeondSocket string
	// ForceRegenerateCNI forces bootstrap to rewrite space conflists even when
	// they already exist with the expected bridge name. Surfaces via
	// `kuke init --force-regenerate-cni`.
	ForceRegenerateCNI bool
}

func NewControllerExec(ctx context.Context, logger *slog.Logger, opts Options) *Exec {
	return &Exec{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		runner: runner.NewRunner(ctx, logger, runner.Options{
			ContainerdSocket:   opts.ContainerdSocket,
			RunPath:            opts.RunPath,
			ForceRegenerateCNI: opts.ForceRegenerateCNI,
		}),
	}
}

// NewControllerExecForTesting creates a controller with a custom runner for testing.
// This function is exported for testing purposes only.
func NewControllerExecForTesting(ctx context.Context, logger *slog.Logger, opts Options, r runner.Runner) *Exec {
	return &Exec{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		runner: r,
	}
}

func (b *Exec) Bootstrap() (BootstrapReport, error) {
	b.logger.DebugContext(b.ctx, "bootstrapping kukeon", "options", b.opts)

	report := BootstrapReport{
		RunPath:      b.opts.RunPath,
		KukeondImage: b.opts.KukeondImage,
	}

	// CNI.
	report, err := b.bootstrapCNI(report)
	if err != nil {
		return report, err
	}

	// Kukeon root cgroup.
	report, err = b.bootstrapKukeonCgroup(report)
	if err != nil {
		return report, err
	}

	// Default user hierarchy: default realm / default space / default stack (no cell).
	if err = b.bootstrapRealm(
		&report.DefaultRealm,
		consts.KukeonDefaultRealmName,
		consts.RealmNamespace(consts.KukeonDefaultRealmName),
	); err != nil {
		return report, err
	}
	if err = b.bootstrapSpace(
		&report.DefaultSpace,
		consts.KukeonDefaultRealmName,
		consts.KukeonDefaultSpaceName,
	); err != nil {
		return report, err
	}
	if err = b.bootstrapStack(
		&report.DefaultStack,
		consts.KukeonDefaultRealmName,
		consts.KukeonDefaultSpaceName,
		consts.KukeonDefaultStackName,
	); err != nil {
		return report, err
	}

	// System hierarchy: kuke-system realm / kukeon space / kukeon stack / kukeond cell.
	if err = b.bootstrapRealm(
		&report.SystemRealm,
		consts.KukeSystemRealmName,
		consts.RealmNamespace(consts.KukeSystemRealmName),
	); err != nil {
		return report, err
	}
	if err = b.bootstrapSpace(
		&report.SystemSpace,
		consts.KukeSystemRealmName,
		consts.KukeSystemSpaceName,
	); err != nil {
		return report, err
	}
	if err = b.bootstrapStack(
		&report.SystemStack,
		consts.KukeSystemRealmName,
		consts.KukeSystemSpaceName,
		consts.KukeSystemStackName,
	); err != nil {
		return report, err
	}

	// System cell: kukeond. Provisioned only when an image is configured (lets
	// callers opt out in tests / non-daemon setups).
	if b.opts.KukeondImage != "" {
		if err = ensureSocketDir(b.opts.KukeondSocket); err != nil {
			return report, err
		}
		if err = ensureCNIStateDir(); err != nil {
			return report, err
		}
		cellDoc := kukeondCellDoc(
			b.opts.KukeondImage,
			b.opts.KukeondSocket,
			b.opts.RunPath,
			b.opts.ContainerdSocket,
		)
		if err = b.bootstrapCell(&report.SystemCell, cellDoc); err != nil {
			return report, err
		}
	}

	return report, nil
}

// Close closes the controller and releases all resources, including the containerd connection.
func (b *Exec) Close() error {
	return b.runner.Close()
}

// RunPath returns the configured kukeon run path. Surfaced for callers that
// need to derive host paths from the same root the controller writes to —
// notably the in-process AttachContainer endpoint, which resolves the
// per-container sbsh socket via fs.ContainerSocketPath.
func (b *Exec) RunPath() string {
	return b.opts.RunPath
}
