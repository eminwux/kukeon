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
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
)

// ReconcileOutcome describes the per-cell effect of a single reconcile pass.
// At most one of Updated / Deleted is true: an AutoDelete cell whose root
// task has exited skips the persisted state-update path and runs the
// kill+delete sequence instead, so callers see Deleted, not Updated.
type ReconcileOutcome struct {
	// Updated is true if the cell's status (state or container statuses)
	// was written back to disk during the pass.
	Updated bool
	// Deleted is true if the reconciler killed and removed the cell as
	// part of honoring Spec.AutoDelete. When true the input cell no
	// longer exists in metadata.
	Deleted bool
}

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

	// WriteSecret persists a `kind: Secret`'s bytes to the daemon-managed,
	// root-owned, 0o600 file under the scope's metadata tree (issue #619).
	// It returns whether the file was newly created (vs. overwritten). The
	// caller (ReconcileSecret) is responsible for having verified the scope
	// exists; WriteSecret only creates the secrets/ subdir and the file, not
	// the scope itself.
	WriteSecret(secret intmodel.Secret) (created bool, err error)

	// GetSecret reports the metadata-only view of a single named, scoped
	// `kind: Secret` and whether it exists on disk (issue #622). The bytes
	// are never read — a stat confirms existence. Returns
	// errdefs.ErrSecretNotFound when absent.
	GetSecret(secret intmodel.Secret) (intmodel.Secret, error)
	// ListSecrets enumerates the metadata of every Secret bound to the
	// filter scope or any scope nested within it (issue #622). An empty
	// realmName lists across all realms; the filter is a subtree prefix.
	ListSecrets(realmName, spaceName, stackName, cellName string) ([]intmodel.Secret, error)
	// DeleteSecret removes the daemon-stored bytes file for a single named,
	// scoped Secret (issue #622). Returns errdefs.ErrSecretNotFound when
	// the file is absent.
	DeleteSecret(secret intmodel.Secret) error

	// WriteBlueprint persists a `kind: CellBlueprint`'s serialized document to
	// the daemon-managed, root-owned, world-readable file under the scope's
	// metadata tree (issue #620). Returns whether the file was newly created
	// (vs. overwritten). The caller (ReconcileBlueprint) is responsible for
	// having verified the scope exists.
	WriteBlueprint(blueprint intmodel.CellBlueprint) (created bool, err error)

	// GetBlueprint reads a single named, scoped CellBlueprint's document off
	// disk (issue #620). Unlike GetSecret the full document is returned — a
	// blueprint carries only template references, no credential bytes — so
	// `kuke run -b` can materialize it. Returns errdefs.ErrBlueprintNotFound
	// when the file is absent.
	GetBlueprint(blueprint intmodel.CellBlueprint) (intmodel.CellBlueprint, error)
	// ListBlueprints enumerates the metadata of every CellBlueprint bound to
	// the filter scope or any scope nested within it (issue #643). An empty
	// realmName lists across all realms; the filter is a subtree prefix
	// bounded at stack depth (a Blueprint is never cell-scoped). The returned
	// carriers are metadata-only — the document is not read.
	ListBlueprints(realmName, spaceName, stackName string) ([]intmodel.CellBlueprint, error)
	// DeleteBlueprint removes the daemon-stored document file for a single
	// named, scoped CellBlueprint (issue #643). Returns
	// errdefs.ErrBlueprintNotFound when the file is absent.
	DeleteBlueprint(blueprint intmodel.CellBlueprint) error

	// WriteConfig persists a `kind: CellConfig`'s serialized document to the
	// daemon-managed, root-owned, world-readable file under the scope's metadata
	// tree (issue #624). Returns whether the file was newly created (vs.
	// overwritten). The caller (ReconcileConfig) is responsible for having
	// verified the config's scope exists and the referenced blueprint resolves.
	WriteConfig(config intmodel.CellConfig) (created bool, err error)

	// WriteConfigIfAbsent atomically persists a CellConfig document only when
	// no file at the target path exists yet (issue #839). It is the
	// create-only sibling of WriteConfig used by `kuke run <src> --clone`:
	// the gap-fill counter allocator retries on errdefs.ErrConfigExists to
	// race-safely claim the next-free N. The implementation writes to a
	// same-directory temp file, then uses `os.Link` to atomically claim the
	// destination — link returns EEXIST when the path is taken, so two
	// concurrent invocations never silently overwrite each other.
	WriteConfigIfAbsent(config intmodel.CellConfig) error

	// GetConfig reads a single named, scoped CellConfig's document off disk
	// (issue #644). Like GetBlueprint the full document is returned — a Config
	// carries only a blueprint ref, scalar values, and slot fills, no credential
	// bytes. Returns errdefs.ErrConfigNotFound when the file is absent.
	GetConfig(config intmodel.CellConfig) (intmodel.CellConfig, error)
	// ListConfigs enumerates the metadata of every CellConfig bound to the
	// filter scope or any scope nested within it (issue #644). An empty
	// realmName lists across all realms; the filter is a subtree prefix bounded
	// at stack depth (a Config is never cell-scoped). The returned carriers are
	// metadata-only — the document is not read.
	ListConfigs(realmName, spaceName, stackName string) ([]intmodel.CellConfig, error)
	// DeleteConfig removes the daemon-stored document file for a single named,
	// scoped CellConfig (issue #644). Returns errdefs.ErrConfigNotFound when the
	// file is absent. Deleting a Config never touches the cell it materialized.
	DeleteConfig(config intmodel.CellConfig) error

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
	// ReconcileCell is the daemon-side counterpart to RefreshCell: it
	// re-derives the cell's status from cgroup + root-container task state
	// (so a Ready cell whose root task exits externally flips to Stopped),
	// and — when Spec.AutoDelete is set on a cell that has reached
	// Stopped/Failed — best-effort kills and deletes the cell instead of
	// persisting the new status. Subsumes the per-cell `kuke run --rm`
	// watcher: the trigger (cell stopped) is exactly what the loop already
	// computes, and a single-instance, restart-resilient ticker means a
	// cell whose Spec.AutoDelete=true survives a daemon restart without
	// needing the daemon to re-install per-cell goroutines on startup.
	ReconcileCell(cell intmodel.Cell) (intmodel.Cell, ReconcileOutcome, error)

	GetContainerState(cell intmodel.Cell, containerID string) (intmodel.ContainerState, error)

	// LoadImage imports an OCI/docker image tarball into the given
	// containerd namespace and returns the names of the imported images.
	LoadImage(namespace string, reader io.Reader) ([]string, error)

	// ListImages enumerates images in the given containerd namespace.
	ListImages(namespace string) ([]ctr.ImageInfo, error)

	// GetImage returns metadata for the named image ref in the given
	// containerd namespace. Returns errdefs.ErrImageNotFound when the
	// ref is absent.
	GetImage(namespace, ref string) (ctr.ImageInfo, error)

	// ImageChainID returns the chainID the image at ref would unpack to
	// today in the given containerd namespace. bootstrapCell pairs it
	// with ContainerRootChainID to catch the case where the
	// persisted ContainerSpec.Image ref matches the desired image but
	// the underlying content has been re-pointed (issue #915 defect 2).
	ImageChainID(namespace, ref string) (string, error)

	// ContainerRootChainID returns the chainID the container's root
	// snapshot is anchored on, regardless of what the image ref by the
	// same name resolves to today (issue #915 defect 2).
	ContainerRootChainID(namespace, containerID string) (string, error)

	// DeleteImage removes the named image ref from the given containerd
	// namespace. Returns errdefs.ErrImageNotFound when the ref is absent.
	DeleteImage(namespace, ref string) error

	Close() error
}

type Exec struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options

	ctrClient ctr.Client
	// ctrClientOnce serializes the lazy construction of ctrClient so two
	// concurrent first-use goroutines (e.g. the first RPC handler and the
	// first reconcile tick) cannot each build a client and overwrite the
	// other's assignment — a data race on the pointer plus a duplicate
	// containerd connection (issue #684). Connect() itself stays outside the
	// Once so the reconnect-on-broken-connection path still runs every call.
	ctrClientOnce sync.Once

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

	// nowFn returns the wall clock used for status timestamps (issue
	// #166). nil falls through to time.Now in nowUTC; tests that need
	// to freeze the clock override the function via the runner_test
	// hook (see metadata_test.go).
	nowFn func() time.Time

	// cellLocks serializes the containerd+CNI side-effect span of the cell
	// lifecycle ops against each other on the same cell (issue #714). Keyed
	// per realm/space/stack/cell so unrelated cells never serialize. Lazily
	// built via cellLocksOnce so *Exec values constructed directly in tests
	// (not via NewRunner) share the same lock semantics.
	cellLocks     *cellLockManager
	cellLocksOnce sync.Once
}

type Options struct {
	ContainerdSocket string
	RunPath          string
	CniConf          cni.Conf
	// ForceRegenerateCNI forces ensureSpaceCNIConfig to rewrite an existing conflist
	// even when one is present and its bridge name matches SafeBridgeName. Set by
	// `kuke init --force-regenerate-cni` as an operator escape hatch.
	ForceRegenerateCNI bool
	// KukeonGroupGID, when non-zero, is the numeric GID of the kukeon system
	// group. The runner uses it to chown the host-side per-container tty
	// directory created for Attachable containers, so that members of the
	// kukeon group can reach the per-container sbsh socket via the same
	// group-traversal path that `kuke init` sets up on /opt/kukeon.
	KukeonGroupGID int
	// DefaultMemoryLimitBytes, when > 0, is forwarded to ctr.BuildContainerSpec
	// / ctr.BuildRootContainerSpec via ctr.WithDefaultMemoryLimit so the
	// kernel-level memory.max is written for any admitted container whose
	// ContainerSpec does not already declare a positive
	// Resources.MemoryLimitBytes. Plumbed from controller.Options of the
	// same name. Zero preserves the prior behavior. Issue #531.
	DefaultMemoryLimitBytes int64
	// KukettyLogLevel is the daemon-wide default verbosity of the kuketty
	// wrapper's slog output, threaded down from controller.Options into the
	// renderer's resolveTtyLogLevel helper. Empty falls through to the
	// hardcoded "info" inside that helper (preserves the behavior of test
	// fixtures that build the runner directly with zero-value Options).
	// Issue #599.
	KukettyLogLevel string
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

// daemonDefaultBuildOpts returns ctr.BuildOption entries that carry daemon-
// wide knobs into ctr.BuildContainerSpec / ctr.BuildRootContainerSpec: the
// daemon-default memory cap (issue #531) and the RunPath used to resolve a
// ContainerSecret.secretRef from its scope's secrets tree (issue #623).
// Returns nil when no daemon-wide options are configured so existing call
// sites pay no cost.
func (r *Exec) daemonDefaultBuildOpts() []ctr.BuildOption {
	var opts []ctr.BuildOption
	if r.opts.DefaultMemoryLimitBytes > 0 {
		opts = append(opts, ctr.WithDefaultMemoryLimit(r.opts.DefaultMemoryLimitBytes))
	}
	if r.opts.RunPath != "" {
		opts = append(opts, ctr.WithSecretRunPath(r.opts.RunPath))
	}
	if r.opts.KukeonGroupGID > 0 {
		//nolint:gosec // KukeonGroupGID is a real GID (uint16 territory); the
		// runtime-spec field is uint32 so the conversion is widening.
		opts = append(opts, ctr.WithKukeonGroupGID(uint32(r.opts.KukeonGroupGID)))
	}
	return opts
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
	r.ctrClientOnce.Do(func() {
		// Guard the nil check so a test-injected fake (constructed via
		// &Exec{ctrClient: fake}) survives — only a runner that started with
		// no client builds the real one here.
		if r.ctrClient == nil {
			r.ctrClient = ctr.NewClient(r.ctx, r.logger, r.opts.ContainerdSocket)
		}
	})
	return r.ctrClient.Connect()
}
