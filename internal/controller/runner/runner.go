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

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

type Runner interface {
	BootstrapCNI(cfgDir, cacheDir, binDir string) (cni.BootstrapReport, error)
	EnsureKukeonRootCgroup() (existsPre bool, created bool, err error)

	GetRealm(realm intmodel.Realm) (intmodel.Realm, error)
	ListRealms() ([]intmodel.Realm, error)
	CreateRealm(realm intmodel.Realm) (intmodel.Realm, error)
	EnsureRealm(realm intmodel.Realm) (intmodel.Realm, error)
	UpdateRealm(realm intmodel.Realm) (intmodel.Realm, error)
	ExistsRealmContainerdNamespace(namespace string) (bool, error)
	// ListContainerdNamespaces returns every containerd namespace the
	// runner's client can see. Surfaced for the uninstall path so it can
	// enumerate kukeon namespaces by `.kukeon.io` suffix and clean up
	// user-created realms whose on-disk metadata was already wiped.
	ListContainerdNamespaces() ([]string, error)
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
	StartCell(cell intmodel.Cell) (intmodel.Cell, error)
	StopCell(cell intmodel.Cell) (intmodel.Cell, error)
	StartContainer(cell intmodel.Cell, containerID string) (intmodel.Cell, error)
	StopContainer(cell intmodel.Cell, containerID string) error
	KillCell(cell intmodel.Cell) (intmodel.Cell, error)
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

	PurgeRealm(realm intmodel.Realm) (namespaceRemoved bool, err error)
	PurgeSpace(space intmodel.Space) error
	PurgeStack(stack intmodel.Stack) error
	PurgeCell(cell intmodel.Cell) error
	PurgeContainer(realm intmodel.Realm, containerID string) error

	RefreshRealm(realm intmodel.Realm) (intmodel.Realm, bool, error)
	RefreshSpace(space intmodel.Space) (intmodel.Space, bool, error)
	RefreshStack(stack intmodel.Stack) (intmodel.Stack, bool, error)
	RefreshCell(cell intmodel.Cell) (intmodel.Cell, int, error)

	GetContainerState(cell intmodel.Cell, containerID string) (intmodel.ContainerState, error)

	WaitCellRootTaskExit(ctx context.Context, cell intmodel.Cell) (<-chan containerd.ExitStatus, error)

	Close() error
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options

	ctrClient ctr.Client

	cniConf *cni.Conf

	// subnetAllocator hands out per-space /24 chunks of 10.88.0.0/16 and
	// persists each assignment under <RunPath>/<realm>/<space>/network.json.
	// Built eagerly in NewRunner (and in test fixtures) so the per-instance
	// mutex inside *cni.SubnetAllocator is the single arbiter across all
	// concurrent gRPC requests — a lazy init under no lock would let two
	// parallel CreateSpace calls each construct their own allocator and
	// regress #131's collision-on-10.88.0.1 bug.
	subnetAllocator *cni.SubnetAllocator

	// netPolicy applies and removes space egress policies on the host
	// firewall. nil behaves as a NoopEnforcer so unit tests and read-only
	// clients never touch iptables.
	netPolicy netpolicy.Enforcer
}

type Options struct {
	ContainerdSocket string
	RunPath          string
	CniConf          cni.Conf
	// ForceRegenerateCNI forces ensureSpaceCNIConfig to rewrite an existing conflist
	// even when one is present and its bridge name matches SafeBridgeName. Set by
	// `kuke init --force-regenerate-cni` as an operator escape hatch.
	ForceRegenerateCNI bool
}

func NewRunner(ctx context.Context, logger *slog.Logger, opts Options) Runner {
	return &Exec{
		ctx:             ctx,
		logger:          logger,
		opts:            opts,
		cniConf:         &opts.CniConf,
		netPolicy:       netpolicy.NewIptablesEnforcer(logger),
		subnetAllocator: cni.NewDefaultSubnetAllocator(opts.RunPath),
	}
}

// netPolicyEnforcer returns the configured enforcer or a no-op when the
// runner was built without one (e.g., minimal test fixtures).
func (r *Exec) netPolicyEnforcer() netpolicy.Enforcer {
	if r.netPolicy == nil {
		return netpolicy.NoopEnforcer{}
	}
	return r.netPolicy
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
