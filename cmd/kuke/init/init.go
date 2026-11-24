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

package init

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/firewall"
	"github.com/eminwux/kukeon/internal/instance"
	"github.com/eminwux/kukeon/internal/sysuser"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// defaultContainerdSocket is the conventional path of a standalone
	// containerd's gRPC socket. Used as the fallback when no socket is
	// configured (matches the containerd library's own default and the
	// pre-flight in scripts/dev-init.sh).
	defaultContainerdSocket = "/run/containerd/containerd.sock"

	// File modes applied by `kuke init` so the kukeon group can reach the
	// runtime/socket without world access. Writes under /opt/kukeon still
	// require root and go through the daemon.
	//
	// Files get 0o640 instead of 0o750: blanket-chmoding the tree to 0o750
	// would mark JSON metadata files as executable, which has no purpose.
	// Dirs need execute (traverse) for the kukeon group, so they stay
	// 0o750. The SGID bit is set on directories (mode 2750) so that files
	// the daemon writes later (metadata.json, CNI state, etc.) inherit
	// the kukeon group instead of landing as root:root and breaking
	// `--no-daemon` reads for non-root operators.
	kukeonRunPathDirMode  os.FileMode = os.ModeSetgid | 0o750
	kukeonRunPathFileMode os.FileMode = 0o640
	// kukeonSocketMode mirrors consts.KukeonSocketMode — the single source of
	// truth shared with `kuke daemon recreate` so the init and recreate socket
	// permissions cannot drift.
	kukeonSocketMode os.FileMode = consts.KukeonSocketMode
)

func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize a new Kukeon project",
		RunE:         runInit,
		SilenceUsage: true,
	}

	if err := setupInitCmd(cmd); err != nil {
		return nil
	}

	return cmd
}

func setupInitCmd(cmd *cobra.Command) error {
	if err := setFlags(cmd); err != nil {
		return fmt.Errorf("failed to set flags: %w", err)
	}

	if err := setPersistentFlags(cmd); err != nil {
		return fmt.Errorf("failed to set persistent flags: %w", err)
	}

	// `--no-daemon` is retained on init per #222 ACs even though init does
	// not consult the viper key directly — it bootstraps the daemon, it does
	// not talk to one. The flag stays accepted so existing operator muscle
	// memory does not break; behavior is unchanged.
	kukshared.RegisterNoDaemonFlag(cmd)

	bindEnvVars()
	return nil
}

// bindEnvVars wires each kuke-init viper key to its `KUKE_INIT_*`
// environment variable. Without these calls applyServerConfiguration's
// envSet check skips the YAML write yet viper has no env binding to read
// the value back from — so the operator-exported value is recognised as
// a gate but its content is silently dropped, leaving the flag default
// to win. Mirrors the BindEnv block in cmd/kukeond/kukeond.go's
// bindEnvVars and the KUKEON_* binds in cmd/kuke/kuke.go's loadConfig.
//
// KUKEOND_CONFIGURATION (the new single source of truth for the
// --server-configuration path, issue #284) is bound by
// kukshared.LoadServerConfigurationFromFlag at runtime, not here, so the
// binding follows the leaf command being executed.
func bindEnvVars() {
	for _, v := range []config.Var{
		config.KUKE_INIT_REALM,
		config.KUKE_INIT_SPACE,
		config.KUKE_INIT_KUKEOND_IMAGE,
		config.KUKE_INIT_NO_WAIT,
		config.KUKE_INIT_FORCE_REGENERATE_CNI,
	} {
		_ = v.BindEnv()
	}
}

func setFlags(cmd *cobra.Command) error {
	cmd.Flags().String("realm", "default", "Name of default realm")
	err := viper.BindPFlag(config.KUKE_INIT_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().String("space", "default", "Name of default space")
	err = viper.BindPFlag(config.KUKE_INIT_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().String(
		"kukeond-image", "",
		"Container image for kukeond (default: ghcr.io/eminwux/kukeon:<kuke version>)",
	)
	err = viper.BindPFlag(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey, cmd.Flags().Lookup("kukeond-image"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().Bool(
		"no-wait", false,
		"Do not wait for kukeond to become ready after bootstrap",
	)
	err = viper.BindPFlag(config.KUKE_INIT_NO_WAIT.ViperKey, cmd.Flags().Lookup("no-wait"))
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().Bool(
		"force-regenerate-cni", false,
		"Rewrite space CNI conflists even when they already exist; use to "+
			"recover from stale on-disk state after a generator fix",
	)
	err = viper.BindPFlag(
		config.KUKE_INIT_FORCE_REGENERATE_CNI.ViperKey,
		cmd.Flags().Lookup("force-regenerate-cni"),
	)
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	// --server-configuration is the shared admin-instance selector across
	// kuke init, kuke daemon {start,stop,kill,reset,restart}, and kuke
	// uninstall (issue #284). Registration declares the flag without binding
	// to viper; LoadServerConfigurationFromFlag rebinds at RunE time so
	// viper.BindPFlag's last-bind-wins semantics cannot have one verb's flag
	// silently drive another's.
	kukshared.RegisterServerConfigurationFlag(cmd)

	cmd.Flags().String(
		"containerd-namespace-suffix", config.KUKEON_ROOT_NAMESPACE_SUFFIX.Default,
		"Suffix appended to every realm name to form its containerd namespace "+
			"(e.g. \"kukeon.io\" -> \"default.kukeon.io\")",
	)
	err = viper.BindPFlag(
		config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey,
		cmd.Flags().Lookup("containerd-namespace-suffix"),
	)
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

	cmd.Flags().String(
		"cgroup-root", config.KUKEON_ROOT_CGROUP_ROOT.Default,
		"Cgroup root under which all realms / spaces / stacks / cells live",
	)
	err = viper.BindPFlag(
		config.KUKEON_ROOT_CGROUP_ROOT.ViperKey,
		cmd.Flags().Lookup("cgroup-root"),
	)
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}
	return nil
}

func setPersistentFlags(_ *cobra.Command) error {
	return nil
}

// flagChanged checks both the local and persistent flag sets so the helper
// is correct in both unit tests (where cmd is built bare and persistent
// flags are not yet merged into cmd.Flags()) and in production (where
// cmd is the leaf subcommand and the merged set already contains the
// parent's persistent flags). Mirrors the helper of the same name in
// cmd/kuke/kuke.go.
func flagChanged(cmd *cobra.Command, name string) bool {
	if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
		return true
	}
	if f := cmd.PersistentFlags().Lookup(name); f != nil && f.Changed {
		return true
	}
	return false
}

// applyServerConfiguration layers the init-specific ServerConfiguration
// fields onto viper. The common admin fields (socket, runPath,
// containerdSocket, logLevel, namespaceSuffix, cgroupRoot) are owned by the
// shared helper kukshared.ApplyServerConfigurationCommonFields, which
// runInit calls via kukshared.LoadServerConfigurationFromFlag before this
// function. That split keeps one source of truth for the admin-common
// precedence chain across kuke init, kuke daemon {start,stop,kill,reset,
// restart}, and kuke uninstall (issue #284) while leaving init's own
// kukeondImage knob — which the daemon ignores and no other admin verb
// consumes — here.
//
// Order of precedence is unchanged: explicit `--flag` > env >
// ServerConfiguration > flag default. Flag-changed values win so a one-off
// `--kukeond-image` keeps overriding the on-disk document; env-set values
// win because `viper.Set` would otherwise override viper's env binding.
func applyServerConfiguration(cmd *cobra.Command, spec v1beta1.ServerConfigurationSpec) {
	if spec.KukeondImage != "" && !flagChanged(cmd, "kukeond-image") &&
		!envSet(config.KUKE_INIT_KUKEOND_IMAGE) {
		viper.Set(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey, spec.KukeondImage)
	}
}

// envSet reports whether the OS env var backing v is present (any value,
// including empty string, counts as set — same semantics as viper's BindEnv).
func envSet(v config.Var) bool {
	_, ok := os.LookupEnv(v.EnvVar())
	return ok
}

// resolveKukeondImage returns the kukeond container image to provision.
// If the user passed --kukeond-image, that wins. Otherwise compose
// config.KukeondImageRepo (e.g. ghcr.io/eminwux/kukeon, injected via ldflags
// by the release pipeline) with the immutable tag of config.Version's channel:
// the release tag on stable, sha-<short> on edge (issue #1638). Versions with
// no channel keep the historical fallbacks: a v-prefixed version is used as
// the tag as-is, anything else falls back to :latest.
func resolveKukeondImage() string {
	if override := viper.GetString(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey); override != "" {
		return override
	}

	repo := strings.TrimSpace(config.KukeondImageRepo)
	if repo == "" {
		repo = "ghcr.io/eminwux/kukeon"
	}

	version := strings.TrimSpace(config.Version)
	if tag := config.ParseVersion(version).ImageTag(version); tag != "" {
		return fmt.Sprintf("%s:%s", repo, tag)
	}

	tag := version
	if tag == "" || !strings.HasPrefix(tag, "v") {
		tag = "latest"
	}
	return fmt.Sprintf("%s:%s", repo, tag)
}

// preflightContainerdSocket fails fast when the configured containerd socket
// does not exist or is not a socket file. Without this check, RunInit can
// reach the controller bootstrap and silently succeed against an unreachable
// socket — runner.ExistsRealmContainerdNamespace deliberately returns
// (false, nil) on a missing socket for test ergonomics, which lets the
// "namespace missing, create it" path proceed through downstream no-ops.
func preflightContainerdSocket(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf(
			"%w: %s — start containerd before re-running `kuke init`: %w",
			errdefs.ErrConnectContainerd, path, err,
		)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf(
			"%w: %s is not a socket file — verify containerd is running and the path is correct",
			errdefs.ErrConnectContainerd, path,
		)
	}
	return nil
}

// installForwardAdmission installs the kukeon-owned FORWARD admission chain
// so cells reach the network on hosts where `iptables -P FORWARD DROP` is
// the default (Docker, firewalld, ufw, hardened distros). Idempotent —
// re-running `kuke init` on a healthy host produces no rule churn.
//
// On hosts without an iptables binary on PATH (minimal containers,
// nftables-only distros that lack the iptables-nft compat shim), this
// logs a WARN and continues: every runner call would otherwise fail and
// abort bring-up. The host owner has implicitly opted out of
// kukeon-managed FORWARD admission and is expected to provide whatever
// equivalent the host needs (or run with FORWARD ACCEPT).
func installForwardAdmission(ctx context.Context, logger *slog.Logger) error {
	if !firewall.IsIptablesAvailable() {
		logger.WarnContext(
			ctx,
			"iptables not found on PATH; skipping forward admission chain — kukeon-bridge traffic may be blocked if FORWARD default policy is DROP",
			"chain",
			firewall.ForwardChainName,
		)
		return nil
	}
	if err := firewall.NewInstaller(logger).Install(ctx); err != nil {
		return fmt.Errorf("install forward admission chain: %w", err)
	}
	return nil
}

// applyKukeonOwnership chown+chmods /run/kukeon (socket's parent dir,
// already created by ensureSocketDir during bootstrap) and /opt/kukeon
// (RunPath). Both end up root:kukeon mode 0o750 so the kukeon group can
// traverse without world access.
//
// The RunPath sweep is a recursive denylist ("chown everything, prune what
// the runner owns"), not an allowlist of init-created paths. The allowlist
// redesign proposed in issue #1416 was considered and declined: the scope
// tree (realm/space/stack/cell dirs and their metadata.json files) is created
// by the runner on every `kuke create`/`kuke apply`, not by init, so an
// allowlist of "paths init itself creates" would stop healing exactly the
// tree the sweep exists to keep traversable for the kukeon group (non-root
// `--no-daemon` reads of scope metadata), and would require init to
// re-derive every scope layout the runner can produce. Instead every runner
// subtree whose ownership or mode differs from the sweep's root:kukeon
// 2750/0640 is pruned structurally by initSweepSkip, and the root-only
// subtrees (secrets/, volume-meta/) are then restored to their runner
// contract by healRootOnlyScopeSubdirs so hosts a pre-#1416 sweep already
// widened stop leaking; blueprints/ and configs/
// are deliberately still swept (their 0o755/0o644 runner modes are wider than
// the sweep's, so the sweep only narrows world access). A new runner-owned
// subdir with a tighter or differently-owned contract must be added to
// runnerOwnedScopeSubdirMaxDepth.
func applyKukeonOwnership(
	socketPath, runPath string,
	ensure sysuser.EnsureResult,
) (permissionsReport, error) {
	runDir := filepath.Dir(socketPath)
	r := permissionsReport{
		User:         consts.KukeonSystemUser,
		Group:        consts.KukeonSystemGroup,
		UID:          ensure.UID,
		GID:          ensure.GID,
		UserCreated:  ensure.UserCreated,
		GroupCreated: ensure.GroupCreated,
		RunDirPath:   runDir,
		RunPath:      runPath,
		SocketPath:   socketPath,
	}
	if err := sysuser.ChownAndChmod(runDir, 0, ensure.GID, consts.KukeonRunDirMode); err != nil {
		return r, fmt.Errorf("apply kukeon ownership to %q: %w", runDir, err)
	}
	r.RunDirApplied = true
	if err := sysuser.ChownTreeAndChmodSkip(
		runPath, 0, ensure.GID, kukeonRunPathDirMode, kukeonRunPathFileMode,
		initSweepSkip(runPath),
	); err != nil {
		return r, fmt.Errorf("apply kukeon ownership to %q: %w", runPath, err)
	}
	if err := healRootOnlyScopeSubdirs(runPath, 0, ensure.GID); err != nil {
		return r, fmt.Errorf("apply kukeon ownership to %q: %w", runPath, err)
	}
	r.RunPathApplied = true
	return r, nil
}

// Runner contract for the root-only per-scope resource subdirs (secrets/ and
// volume-meta/): mirrors secretsDirMode/secretFileMode in
// internal/controller/runner/secret.go and volumeMetaDirMode/volumeMetaFileMode
// in internal/controller/runner/volume_meta.go.
const (
	rootOnlyScopeSubdirDirMode  os.FileMode = 0o700
	rootOnlyScopeSubdirFileMode os.FileMode = 0o600
)

// initSweepSkip returns the skip predicate applyKukeonOwnership hands to
// sysuser.ChownTreeAndChmodSkip: it prunes every path whose ownership and
// mode belong to the runner rather than to init — live per-container kuketty
// state (skipLiveCellState) and the per-scope runner-owned resource subdirs
// (isRunnerOwnedScopeSubdir: volumes/, volume-meta/, secrets/).
func initSweepSkip(runPath string) func(path string, info os.FileInfo) bool {
	dataRoot := fs.MetadataRoot(runPath)
	return func(path string, info os.FileInfo) bool {
		return skipLiveCellState(path, info) || isRunnerOwnedScopeSubdir(dataRoot, path, info)
	}
}

// healRootOnlyScopeSubdirs re-asserts the runner's root-only contract (dir
// 0o700, files 0o600, root-owned) on every per-scope secrets/ and volume-meta/
// dir. The main sweep prunes them so it never widens them again, but every
// host that ran a pre-#1416 `kuke init` already has them at root:kukeon
// 2750/0640 — and the runner re-chmods only on write (WriteSecret,
// WriteVolumeMeta), so without this pass the kukeon group would keep reading
// those secrets until each one is re-applied. The group is set to the kukeon
// GID, matching what the runner itself produces under the setgid scope dirs
// (MkdirAll inherits the parent's group); the 0o700/0o600 modes make it
// irrelevant to access. Idempotent: a host already on the contract sees the
// same owner and modes re-applied.
func healRootOnlyScopeSubdirs(runPath string, uid, gid int) error {
	dataRoot := fs.MetadataRoot(runPath)
	skip := initSweepSkip(runPath)
	err := filepath.Walk(dataRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if path == dataRoot && os.IsNotExist(walkErr) {
				return filepath.SkipAll
			}
			return walkErr
		}
		if !info.IsDir() {
			return nil
		}
		if isRootOnlyScopeSubdir(dataRoot, path, info) {
			if err := sysuser.ChownTreeAndChmod(
				path, uid, gid, rootOnlyScopeSubdirDirMode, rootOnlyScopeSubdirFileMode,
			); err != nil {
				return err
			}
			return filepath.SkipDir
		}
		if path != dataRoot && skip(path, info) {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("restore root-only resource dirs: %w", err)
	}
	return nil
}

// Scope depths of a per-scope resource subdir, counted in path components
// under the metadata root including the subdir itself: <realm>/X = 2,
// <realm>/<space>/X = 3, <realm>/<space>/<stack>/X = 4,
// <realm>/<space>/<stack>/<cell>/X = 5.
const (
	realmScopeSubdirDepth = 2
	stackScopeSubdirDepth = 4
	cellScopeSubdirDepth  = 5
)

// runnerOwnedScopeSubdirMaxDepth reports whether name is a per-scope resource
// subdir the init sweep must leave to the runner and, if so, the deepest
// scope depth it can sit at.
//
//   - volumes/ (realm/space/stack): WriteVolume sets volumes/ to 0o755 and
//     each volume dir to root:kukeon setgid 0o770, volumePostCreateChown hands
//     the volume dir to the mounting container's uid, and the workload creates
//     files inside under its own uid. The sweep reset all of it to
//     root:kukeon 2750/0640, stripping group-write and the container-uid owner
//     so every running non-root cell lost write access to its volumes on each
//     `make dev-init` (issue #1415). Pruned and left untouched.
//   - volume-meta/ (realm/space/stack): root-only reclaim manifests, dir
//     0o700 / files 0o600 (runner volume_meta.go). The sweep widened them to
//     kukeon-group-readable 2750/0640 (issue #1416). Pruned from the sweep,
//     then restored by healRootOnlyScopeSubdirs.
//   - secrets/ (realm/space/stack/cell): root-only secret bytes, dir 0o700 /
//     files 0o600 (runner secret.go, issue #619). The sweep widened them to
//     2750/0640, letting every kukeon-group member read every secret
//     (issue #1416). Secrets are consumed only by root (the daemon, or
//     `sudo kuke --no-daemon`), so the runner's root-only contract stands.
//     Pruned from the sweep, then restored by healRootOnlyScopeSubdirs.
func runnerOwnedScopeSubdirMaxDepth(name string) (int, bool) {
	switch name {
	case consts.KukeonVolumesSubdir, consts.KukeonVolumeMetaSubdir:
		return stackScopeSubdirDepth, true
	case consts.KukeonSecretsSubdir:
		return cellScopeSubdirDepth, true
	default:
		return 0, false
	}
}

// isRunnerOwnedScopeSubdir reports whether path is one of the per-scope
// runner-owned resource subdirs named by runnerOwnedScopeSubdirMaxDepth,
// sitting at a scope position under the metadata root. Returning true prunes
// the whole subtree from the init sweep — workload data and secret bytes
// included.
//
// The match is structural (scope depth under the metadata root) rather than
// by basename alone, so an unrelated directory that happens to share one of
// these names elsewhere under RunPath is still swept. At cell depth the
// per-container dirs are siblings of the cell-scope secrets/ dir, so a
// secrets/ there is only matched when the cell does not declare a container
// of that name (cellScopeSecretsDir).
func isRunnerOwnedScopeSubdir(dataRoot, path string, info os.FileInfo) bool {
	if !info.IsDir() {
		return false
	}
	maxDepth, ok := runnerOwnedScopeSubdirMaxDepth(info.Name())
	if !ok {
		return false
	}
	rel, err := filepath.Rel(dataRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	depth := len(strings.Split(rel, string(filepath.Separator)))
	if depth < realmScopeSubdirDepth || depth > maxDepth {
		return false
	}
	if depth == cellScopeSubdirDepth {
		return cellScopeSecretsDir(path)
	}
	return true
}

// isRootOnlyScopeSubdir reports whether path is a runner-owned scope subdir
// (isRunnerOwnedScopeSubdir) whose contract is root-only — secrets/ or
// volume-meta/ — and so is restored by healRootOnlyScopeSubdirs.
func isRootOnlyScopeSubdir(dataRoot, path string, info os.FileInfo) bool {
	switch info.Name() {
	case consts.KukeonSecretsSubdir, consts.KukeonVolumeMetaSubdir:
		return isRunnerOwnedScopeSubdir(dataRoot, path, info)
	default:
		return false
	}
}

// cellScopeSecretsDir reports whether a cell-depth secrets/ dir is the
// cell-scope secrets dir rather than the metadata dir of a container named
// "secrets" (fs.ContainerMetadataDir puts container dirs alongside it, and
// ValidateHierarchyName does not reserve the name). It reads the cell's
// metadata.json and returns false when the cell declares a container with
// that ID, or when the document cannot be read or parsed — an unknown cell
// leaves the dir to the main sweep, the pre-#1416 behavior, rather than
// locking a possible container dir to root-only.
func cellScopeSecretsDir(path string) bool {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(path), consts.KukeonMetadataFile))
	if err != nil {
		return false
	}
	var cell v1beta1.CellDoc
	if err = json.Unmarshal(data, &cell); err != nil {
		return false
	}
	name := filepath.Base(path)
	for _, c := range cell.Spec.Containers {
		if c.ID == name {
			return false
		}
	}
	return true
}

// skipLiveCellState reports whether the recursive `kuke init` ownership sweep
// must leave a path untouched. Per-container kuketty state is provisioned and
// owned by the runner at the container's resolved process uid
// (attachableBuildOpts + attachablePostCreateChown + kuketty), not by init:
//
//   - the tty directory (consts.KukeonContainerTTYDir) and everything inside
//     it — the live attach socket bound 0o660, the capture transcript, the
//     kuketty log, and kuketty's own runtime metadata it rewrites in place;
//   - the rendered per-container kuketty metadata file
//     (ctr.AttachableMetadataFile), chown'd to the container uid so the
//     in-container wrapper can read it through the bind mount.
//
// The blanket sweep treated a live socket as "just a file": it chmodded it
// 0o660 → 0o640 (stripping group-write, so every kukeon-group operator got
// EACCES on `kuke attach`) and chown'd it back to root, undoing
// attachablePostCreateChown. Because `make dev-init` runs `kuke init` on every
// iteration, the sweep re-broke attach for every pre-existing cell each time
// (issue #1207). Returning true for the tty directory prunes the whole subtree.
func skipLiveCellState(_ string, info os.FileInfo) bool {
	if info.IsDir() {
		return info.Name() == consts.KukeonContainerTTYDir
	}
	return info.Name() == ctr.AttachableMetadataFile
}

func runInit(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke init"); err != nil {
		return err
	}

	// Resolve --server-configuration via the shared precedence chain
	// (flag > KUKEOND_CONFIGURATION env > /etc/kukeon/kukeond.yaml > zero
	// document), layer the common admin fields onto viper, and call
	// consts.ConfigureRuntime. The init-specific kukeondImage field is not
	// part of the shared overlay — it is applied below alongside the
	// returned spec so the resolveKukeondImage call observes it.
	serverSpec, serverConfigPath, err := kukshared.LoadServerConfigurationFromFlag(cmd)
	if err != nil {
		return err
	}
	applyServerConfiguration(cmd, serverSpec)

	image := resolveKukeondImage()

	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}

	socketPath := viper.GetString(config.KUKEOND_SOCKET.ViperKey)
	if socketPath == "" {
		socketPath = config.KUKEOND_SOCKET.Default
	}

	containerdSocket := viper.GetString(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey)
	if containerdSocket == "" {
		containerdSocket = defaultContainerdSocket
	}
	if err = preflightContainerdSocket(containerdSocket); err != nil {
		return err
	}

	if mismatchErr := instance.VerifyOrWrite(
		runPath,
		viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey),
		viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey),
	); mismatchErr != nil {
		return mismatchErr
	}

	// Ensure the kukeon system user/group exist before bootstrap so the
	// post-bootstrap chown step has a GID to apply, and so the kukeond cell
	// is provisioned with --socket-gid pointing at it.
	ensure, ensureErr := sysuser.EnsureUserGroup(
		cmd.Context(),
		consts.KukeonSystemUser,
		consts.KukeonSystemGroup,
		sysuser.EnsureOptions{},
	)
	if ensureErr != nil {
		return fmt.Errorf("ensure kukeon user/group: %w", ensureErr)
	}

	if fwErr := installForwardAdmission(cmd.Context(), logger); fwErr != nil {
		return fwErr
	}

	// Pre-stage kukepause under <RunPath>/bin before Bootstrap creates the
	// kukeond cell: that cell's root container bind-mounts the staged binary at
	// /.kukeon/bin/kukepause and execs it as PID 1, so it must exist on the host first (issue
	// #931). All later user cells reuse the same staged copy.
	kukepausePath, stageErr := lifecycle.StageKukepause(runPath)
	if stageErr != nil {
		return fmt.Errorf("stage kukepause: %w", stageErr)
	}
	// Stage this kuke for cells' `kuke-binary` directory mount (issue #1483),
	// so re-running init after a rebuild reaches running cells.
	clientPaths, stageErr := lifecycle.StageKukeClient(runPath)
	if stageErr != nil {
		return fmt.Errorf("stage kuke client: %w", stageErr)
	}

	opts := controller.Options{
		RunPath:              runPath,
		ContainerdSocket:     containerdSocket,
		KukeondImage:         image,
		KukeondSocket:        socketPath,
		KukeondSocketGID:     ensure.GID,
		KukeondConfiguration: serverConfigPath,
		ForceRegenerateCNI:   viper.GetBool(config.KUKE_INIT_FORCE_REGENERATE_CNI.ViperKey),
	}

	logger.DebugContext(cmd.Context(), "running init", "opts", opts)

	ctrl := controller.NewControllerExec(cmd.Context(), logger, opts)
	report, bootstrapErr := ctrl.Bootstrap()
	if bootstrapErr != nil {
		return bootstrapErr
	}

	// The web UI login secret (issue #1652) is written before the ownership
	// pass so the heal step re-asserts its root-only modes like any secret's.
	webLogin, loginErr := ensureWebUILogin(ctrl)
	if loginErr != nil {
		return loginErr
	}

	permsReport, ownErr := applyKukeonOwnership(socketPath, runPath, ensure)
	if ownErr != nil {
		return ownErr
	}

	// applyKukeonOwnership's recursive RunPath chown drops every file to 0o640,
	// stripping kukepause's execute bit. Re-assert it root:kukeon 0o750 so root
	// containers created after init can exec the bind-mounted kukepause (issue
	// #931). The kukeond cell created during Bootstrap already exec'd the
	// pre-chown copy, so this only matters for subsequent cells.
	if chmodErr := sysuser.ChownAndChmod(kukepausePath, 0, ensure.GID, lifecycle.KukepauseStagedMode); chmodErr != nil {
		return fmt.Errorf("restore kukepause exec bit %q: %w", kukepausePath, chmodErr)
	}
	for _, path := range []string{clientPaths.Dir, clientPaths.Binary, clientPaths.Shim} {
		if chmodErr := sysuser.ChownAndChmod(path, 0, ensure.GID, lifecycle.KukeClientStagedMode); chmodErr != nil {
			return fmt.Errorf("restore kuke client mode %q: %w", path, chmodErr)
		}
	}

	printBootstrapReport(cmd, report, permsReport)
	cmd.Printf("  - kukepause staged: %s\n", kukepausePath)
	cmd.Printf("  - kuke client staged: %s\n", clientPaths.Binary)
	printWebUILogin(cmd, webLogin)

	if viper.GetBool(config.KUKE_INIT_NO_WAIT.ViperKey) {
		return nil
	}

	if waitErr := lifecycle.WaitForKukeondReady(cmd.Context(), socketPath, lifecycle.KukeondReadyTimeout); waitErr != nil {
		return fmt.Errorf("kukeond did not become ready: %w", waitErr)
	}

	// The daemon (running inside the kukeond container) created the socket
	// once it bound the listener. Apply kukeon ownership on it now so a
	// non-root group member can dial it. Daemon restart resets the
	// ownership; re-running `sudo kuke init` (idempotent) restores it.
	if chownErr := sysuser.ChownAndChmod(socketPath, 0, ensure.GID, kukeonSocketMode); chownErr != nil {
		return fmt.Errorf("apply kukeon ownership to %q: %w", socketPath, chownErr)
	}
	cmd.Println(fmt.Sprintf(
		"  - socket %q: chown root:%s mode %s",
		socketPath, consts.KukeonSystemGroup, formatPosixMode(kukeonSocketMode),
	))

	cmd.Println(fmt.Sprintf("kukeond is ready (unix://%s)", socketPath))
	return nil
}

// permissionsReport holds the chown/chmod outcome captured by runInit so the
// printer can report it alongside the controller's BootstrapReport.
type permissionsReport struct {
	User           string
	Group          string
	UID            int
	GID            int
	UserCreated    bool
	GroupCreated   bool
	RunDirPath     string
	RunPath        string
	SocketPath     string
	RunDirApplied  bool
	RunPathApplied bool
}

func printBootstrapReport(cmd *cobra.Command, report controller.BootstrapReport, perms permissionsReport) {
	printHeader(cmd, report)
	printOverview(cmd, report)
	cmd.Println("Actions:")
	printKukeonCgroupAction(cmd, report)
	printCNIActions(cmd, report)

	cmd.Println("  Default hierarchy:")
	printRealmActions(cmd, report.DefaultRealm)
	printSpaceActions(cmd, report.DefaultSpace)
	printStackActions(cmd, report.DefaultStack)

	cmd.Println("  System hierarchy:")
	printRealmActions(cmd, report.SystemRealm)
	printSpaceActions(cmd, report.SystemSpace)
	printStackActions(cmd, report.SystemStack)
	printCellActions(cmd, report.SystemCell, report.KukeondImage)

	printPermissionsActions(cmd, perms)
}

func printPermissionsActions(cmd *cobra.Command, perms permissionsReport) {
	if perms.User == "" {
		return
	}
	cmd.Println("  Permissions:")
	if perms.GroupCreated {
		cmd.Println(fmt.Sprintf("    - group %q: created (gid %d)", perms.Group, perms.GID))
	} else {
		cmd.Println(fmt.Sprintf("    - group %q: already existed (gid %d)", perms.Group, perms.GID))
	}
	if perms.UserCreated {
		cmd.Println(fmt.Sprintf("    - user %q: created (uid %d)", perms.User, perms.UID))
	} else {
		cmd.Println(fmt.Sprintf("    - user %q: already existed (uid %d)", perms.User, perms.UID))
	}
	if perms.RunDirApplied {
		cmd.Println(fmt.Sprintf(
			"    - %q: chown root:%s mode %s",
			perms.RunDirPath, perms.Group, formatPosixMode(consts.KukeonRunDirMode),
		))
	}
	if perms.RunPathApplied {
		cmd.Println(fmt.Sprintf(
			"    - %q (recursive): chown root:%s mode %s (dirs) / %s (files)",
			perms.RunPath, perms.Group,
			formatPosixMode(kukeonRunPathDirMode),
			formatPosixMode(kukeonRunPathFileMode),
		))
	}
}

// formatPosixMode renders an os.FileMode as the on-disk POSIX octal mode
// (including SUID/SGID/sticky bits), matching what `stat`/`ls -l` show.
// Go's %#o on a FileMode prints internal flag bits like ModeSetgid (0x400000)
// instead of the syscall-level 02750, which is unreadable in init output.
func formatPosixMode(m os.FileMode) string {
	val := uint32(m.Perm())
	if m&os.ModeSetuid != 0 {
		val |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		val |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		val |= 0o1000
	}
	return fmt.Sprintf("0o%04o", val)
}

func printKukeonCgroupAction(cmd *cobra.Command, report controller.BootstrapReport) {
	printCgroupAction(
		cmd,
		"kukeon root",
		report.KukeonCgroupExistsPre,
		report.KukeonCgroupExistsPost,
		report.KukeonCgroupCreated,
	)
}

func printHeader(cmd *cobra.Command, report controller.BootstrapReport) {
	anyCreated := report.KukeonCgroupCreated ||
		sectionRealmChanged(report.DefaultRealm) ||
		sectionSpaceChanged(report.DefaultSpace) ||
		sectionStackChanged(report.DefaultStack) ||
		sectionRealmChanged(report.SystemRealm) ||
		sectionSpaceChanged(report.SystemSpace) ||
		sectionStackChanged(report.SystemStack) ||
		sectionCellChanged(report.SystemCell) ||
		report.CniConfigDirCreated ||
		report.CniCacheDirCreated ||
		report.CniBinDirCreated
	if anyCreated {
		cmd.Println("Initialized Kukeon runtime")
		return
	}
	cmd.Println("Kukeon runtime already initialized")
}

func sectionRealmChanged(s controller.RealmSection) bool {
	return s.RealmCreated || s.RealmContainerdNamespaceCreated || s.RealmCgroupCreated
}

func sectionSpaceChanged(s controller.SpaceSection) bool {
	return s.SpaceCreated || s.SpaceCNINetworkCreated || s.SpaceCgroupCreated
}

func sectionStackChanged(s controller.StackSection) bool {
	return s.StackCreated || s.StackCgroupCreated
}

func sectionCellChanged(s controller.CellSection) bool {
	return s.CellCreated || s.CellCgroupCreated || s.CellRootContainerCreated || s.CellStarted
}

func printOverview(cmd *cobra.Command, report controller.BootstrapReport) {
	cmd.Println(fmt.Sprintf(
		"Realm: %s (namespace: %s)",
		report.DefaultRealm.RealmName,
		report.DefaultRealm.RealmContainerdNamespace,
	))
	cmd.Println(fmt.Sprintf(
		"System realm: %s (namespace: %s)",
		report.SystemRealm.RealmName,
		report.SystemRealm.RealmContainerdNamespace,
	))
	cmd.Println(fmt.Sprintf("Run path: %s", report.RunPath))
	if report.KukeondImage != "" {
		cmd.Println(fmt.Sprintf("Kukeond image: %s", report.KukeondImage))
	}
}

func printRealmActions(cmd *cobra.Command, section controller.RealmSection) {
	if section.RealmCreated {
		cmd.Println(fmt.Sprintf("    - realm %q: created", section.RealmName))
	} else {
		cmd.Println(fmt.Sprintf("    - realm %q: already existed", section.RealmName))
	}
	if section.RealmContainerdNamespaceCreated {
		cmd.Println(fmt.Sprintf("    - containerd namespace %q: created", section.RealmContainerdNamespace))
	} else {
		cmd.Println(fmt.Sprintf("    - containerd namespace %q: already existed", section.RealmContainerdNamespace))
	}
	printCgroupAction(
		cmd,
		"realm",
		section.RealmCgroupExistsPre,
		section.RealmCgroupExistsPost,
		section.RealmCgroupCreated,
	)
}

func printSpaceActions(cmd *cobra.Command, section controller.SpaceSection) {
	if section.SpaceCreated {
		cmd.Println(fmt.Sprintf("    - space %q: created", section.SpaceName))
	} else {
		cmd.Println(fmt.Sprintf("    - space %q: already existed", section.SpaceName))
	}
	if section.SpaceCNINetworkCreated {
		cmd.Println(fmt.Sprintf(
			"    - network %q: created",
			section.SpaceCNINetworkName,
		))
	} else {
		cmd.Println(fmt.Sprintf(
			"    - network %q: already existed",
			section.SpaceCNINetworkName,
		))
	}
	printCgroupAction(
		cmd,
		"space",
		section.SpaceCgroupExistsPre,
		section.SpaceCgroupExistsPost,
		section.SpaceCgroupCreated,
	)
}

func printStackActions(cmd *cobra.Command, section controller.StackSection) {
	if section.StackCreated {
		cmd.Println(fmt.Sprintf("    - stack %q: created", section.StackName))
	} else {
		cmd.Println(fmt.Sprintf("    - stack %q: already existed", section.StackName))
	}
	printCgroupAction(
		cmd,
		"stack",
		section.StackCgroupExistsPre,
		section.StackCgroupExistsPost,
		section.StackCgroupCreated,
	)
}

func printCellActions(cmd *cobra.Command, section controller.CellSection, image string) {
	if section.CellName == "" {
		cmd.Println("    - cell: not provisioned")
		return
	}
	switch {
	case section.CellRecreatedDueToImageDrift:
		// Image drift on a surviving cell record (issue #868). Surfacing the
		// prior → desired transition is what tells the operator the rebuild
		// actually picked up their `kuke build` output instead of silently
		// restarting the stale container. When the prior and desired refs
		// match byte-for-byte but bootstrapCell still routed through
		// RecreateCell, the trigger is a content digest swap on the same
		// ref (issue #915 defect 2: `kuke build` re-pointed the same tag
		// at fresh layers). The ref-string transition would render as
		// "kukeon-local:dev → kukeon-local:dev" which says nothing; the
		// digest-drift label is the readable form.
		if section.CellPriorImage == image {
			cmd.Println(fmt.Sprintf(
				"    - cell %q: recreated (image digest drift on %s)",
				section.CellName, image,
			))
		} else {
			cmd.Println(fmt.Sprintf(
				"    - cell %q: recreated (image drift: %s → %s)",
				section.CellName, section.CellPriorImage, image,
			))
		}
	case section.CellCreated:
		cmd.Println(fmt.Sprintf("    - cell %q: created (image %s)", section.CellName, image))
	default:
		cmd.Println(fmt.Sprintf("    - cell %q: already existed", section.CellName))
	}
	printCgroupAction(
		cmd,
		"cell",
		section.CellCgroupExistsPre,
		section.CellCgroupExistsPost,
		section.CellCgroupCreated,
	)
	printContainerAction(
		cmd,
		"cell root container",
		section.CellRootContainerExistsPre,
		section.CellRootContainerExistsPost,
		section.CellRootContainerCreated,
	)
	printStartAction(
		cmd,
		"cell containers",
		section.CellStartedPre,
		section.CellStartedPost,
		section.CellStarted,
	)
}

func printCNIActions(cmd *cobra.Command, report controller.BootstrapReport) {
	printDirAction(
		cmd,
		"CNI config dir",
		report.CniConfigDir,
		report.CniConfigDirCreated,
		report.CniConfigDirExistsPost,
	)
	printDirAction(cmd, "CNI cache dir", report.CniCacheDir, report.CniCacheDirCreated, report.CniCacheDirExistsPost)
	printDirAction(cmd, "CNI bin dir", report.CniBinDir, report.CniBinDirCreated, report.CniBinDirExistsPost)
}

func printDirAction(cmd *cobra.Command, label string, path string, created bool, existsPost bool) {
	if created {
		cmd.Println(fmt.Sprintf("  - %s %q: created", label, path))
		return
	}
	if existsPost {
		cmd.Println(fmt.Sprintf("  - %s %q: already existed", label, path))
		return
	}
	cmd.Println(fmt.Sprintf("  - %s %q: not created", label, path))
}

func printCgroupAction(cmd *cobra.Command, label string, existedPre bool, existsPost bool, created bool) {
	switch {
	case created:
		cmd.Println(fmt.Sprintf("    - %s cgroup: created", label))
	case existsPost:
		cmd.Println(fmt.Sprintf("    - %s cgroup: already existed", label))
	default:
		if existedPre {
			cmd.Println(fmt.Sprintf("    - %s cgroup: missing (was previously present)", label))
		} else {
			cmd.Println(fmt.Sprintf("    - %s cgroup: missing", label))
		}
	}
}

func printContainerAction(cmd *cobra.Command, label string, existedPre bool, existsPost bool, created bool) {
	switch {
	case created:
		cmd.Println(fmt.Sprintf("    - %s: created", label))
	case existsPost:
		cmd.Println(fmt.Sprintf("    - %s: already existed", label))
	default:
		if existedPre {
			cmd.Println(fmt.Sprintf("    - %s: missing (was previously present)", label))
		} else {
			cmd.Println(fmt.Sprintf("    - %s: missing", label))
		}
	}
}

func printStartAction(cmd *cobra.Command, label string, startedPre bool, startedPost bool, started bool) {
	switch {
	case started:
		cmd.Println(fmt.Sprintf("    - %s: started", label))
	case startedPost:
		cmd.Println(fmt.Sprintf("    - %s: already running", label))
	default:
		if startedPre {
			cmd.Println(fmt.Sprintf("    - %s: stopped (was previously running)", label))
		} else {
			cmd.Println(fmt.Sprintf("    - %s: not running", label))
		}
	}
}
