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
	"fmt"
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
	CreateSecret(secret intmodel.Secret) (CreateSecretResult, error)
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
	StopCell(cell intmodel.Cell) (StopCellResult, error)
	KillCell(cell intmodel.Cell) (KillCellResult, error)
	PurgeRealm(realm intmodel.Realm, force, cascade bool) (PurgeRealmResult, error)
	PurgeSpace(space intmodel.Space, force, cascade bool) (PurgeSpaceResult, error)
	PurgeStack(stack intmodel.Stack, force, cascade bool) (PurgeStackResult, error)
	PurgeCell(cell intmodel.Cell, force, cascade bool) (PurgeCellResult, error)
	PurgeContainer(container intmodel.Container) (PurgeContainerResult, error)
	RefreshAll() (RefreshResult, error)
	ReconcileCells() (ReconcileResult, error)
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
	// KukeondSocketGID, when non-zero, is passed to the kukeond cell as
	// --socket-gid <gid> so the daemon can chown its listener socket on every
	// restart. Set by `kuke init` to the kukeon group's numeric GID.
	KukeondSocketGID int
	// KukeondConfiguration, when set, is passed to the kukeond cell as
	// --configuration <path> and bind-mounted into the cell so the daemon
	// re-reads the same ServerConfiguration on every restart.
	KukeondConfiguration string
	// ForceRegenerateCNI forces bootstrap to rewrite space conflists even when
	// they already exist with the expected bridge name. Surfaces via
	// `kuke init --force-regenerate-cni`.
	ForceRegenerateCNI bool
	// DefaultMemoryLimitBytes, when > 0, is the daemon-wide fallback memory
	// limit (in bytes) applied to every admitted container whose
	// ContainerSpec.Resources.MemoryLimitBytes is unset or zero. Surfaces via
	// `kukeond serve --default-memory-limit-bytes`, KUKEOND_DEFAULT_MEMORY_LIMIT_BYTES,
	// or ServerConfiguration.Spec.DefaultMemoryLimitBytes. Closes the host-
	// wedge gap on no-swap + no-userspace-OOM hosts (issue #531). Zero (the
	// default) preserves the prior behavior — no fallback, the kernel sees
	// memory.max=max for any spec that did not declare a limit.
	DefaultMemoryLimitBytes int64
	// KukettyLogLevel is the daemon-wide default verbosity of the kuketty
	// wrapper's slog output, applied when a cell omits per-container
	// ContainerTty.LogLevel. Surfaces via `kukeond serve --kuketty-log-level`,
	// KUKEOND_KUKETTY_LOG_LEVEL, or ServerConfiguration.Spec.KukettyLogLevel.
	// Empty falls through to the hardcoded "info" default inside the renderer.
	// Issue #599.
	KukettyLogLevel string
}

func NewControllerExec(ctx context.Context, logger *slog.Logger, opts Options) *Exec {
	return &Exec{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		runner: runner.NewRunner(ctx, logger, runner.Options{
			ContainerdSocket:        opts.ContainerdSocket,
			RunPath:                 opts.RunPath,
			ForceRegenerateCNI:      opts.ForceRegenerateCNI,
			KukeonGroupGID:          opts.KukeondSocketGID,
			DefaultMemoryLimitBytes: opts.DefaultMemoryLimitBytes,
			KukettyLogLevel:         opts.KukettyLogLevel,
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
		cellSection, err := b.ProvisionKukeondCell()
		if err != nil {
			return report, err
		}
		report.SystemCell = cellSection
	}

	return report, nil
}

// ProvisionKukeondCell creates the kukeond daemon cell using the same
// cell-creation path that `kuke init`'s Bootstrap exercises. Factored out so
// `kuke daemon recreate` cannot drift from `kuke init` on the kukeond
// provisioning logic — both verbs call this single helper.
func (b *Exec) ProvisionKukeondCell() (CellSection, error) {
	var section CellSection

	if b.opts.KukeondImage == "" {
		return section, fmt.Errorf("kukeond image is required for cell provisioning")
	}
	if err := ensureSocketDir(b.opts.KukeondSocket); err != nil {
		return section, err
	}
	if err := ensureCNIStateDir(); err != nil {
		return section, err
	}
	if err := ensureServerConfigurationDir(b.opts.KukeondConfiguration); err != nil {
		return section, err
	}
	cellDoc := kukeondCellDoc(
		b.opts.KukeondImage,
		b.opts.KukeondSocket,
		b.opts.RunPath,
		b.opts.ContainerdSocket,
		b.opts.KukeondSocketGID,
		b.opts.KukeondConfiguration,
	)
	if err := b.bootstrapCell(&section, cellDoc); err != nil {
		return section, err
	}
	return section, nil
}

// Close closes the controller and releases all resources, including the containerd connection.
func (b *Exec) Close() error {
	return b.runner.Close()
}

// RunPath returns the configured kukeon run path. Surfaced for callers that
// need to derive host paths from the same root the controller writes to —
// notably the in-process AttachContainer endpoint, which resolves the
// SUN_PATH-safe per-container kuketty socket via
// fs.ContainerSocketSymlinkPath.
func (b *Exec) RunPath() string {
	return b.opts.RunPath
}
