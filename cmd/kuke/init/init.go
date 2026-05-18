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
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/firewall"
	"github.com/eminwux/kukeon/internal/instance"
	"github.com/eminwux/kukeon/internal/serverconfig"
	"github.com/eminwux/kukeon/internal/sysuser"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	kukeondReadyTimeout = 30 * time.Second
	kukeondReadyTick    = 200 * time.Millisecond

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
	kukeonRunDirMode      os.FileMode = os.ModeSetgid | 0o750
	kukeonRunPathDirMode  os.FileMode = os.ModeSetgid | 0o750
	kukeonRunPathFileMode os.FileMode = 0o640
	kukeonSocketMode      os.FileMode = 0o660
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
func bindEnvVars() {
	for _, v := range []config.Var{
		config.KUKE_INIT_REALM,
		config.KUKE_INIT_SPACE,
		config.KUKE_INIT_KUKEOND_IMAGE,
		config.KUKE_INIT_NO_WAIT,
		config.KUKE_INIT_FORCE_REGENERATE_CNI,
		config.KUKE_INIT_SERVER_CONFIGURATION,
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

	cmd.Flags().String(
		"server-configuration", config.KUKE_INIT_SERVER_CONFIGURATION.Default,
		"Path to a ServerConfiguration YAML to seed the daemon with; "+
			"absent file uses hardcoded defaults",
	)
	err = viper.BindPFlag(
		config.KUKE_INIT_SERVER_CONFIGURATION.ViperKey,
		cmd.Flags().Lookup("server-configuration"),
	)
	if err != nil {
		return fmt.Errorf("failed to bind flag: %w", err)
	}

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

// applyRunPathImpliesKukeondSocket derives the daemon's listen socket as
// `<runPath>/kukeond.sock` when the operator passed `--run-path` explicitly
// (flag or KUKEON_RUN_PATH env) but did not pin KUKEOND_SOCKET themselves.
// Without this, `kuke init --run-path X` always bootstraps the kukeond
// system cell bind-bound to the default `/run/kukeon/kukeond.sock` — which
// collides with the parent host's daemon when running inside a nested
// `kukeon-dev-root` cell (PR #548 bind-mounts the parent's `/run/kukeon/`
// into the nest at the same path), and blocks per-test isolation in the
// e2e suite's TestKuke_Init_VerifyState.
//
// Operator intent: `--run-path` is per-invocation, explicit "this run
// lives under X" signal. The daemon socket living under X follows; the
// operator who genuinely wants the in-tree convention restores it with
// `KUKEOND_SOCKET=/run/kukeon/kukeond.sock kuke init --run-path X`.
//
// ServerConfiguration's spec.Socket trips !viper.IsSet through
// applyServerConfiguration's viper.Set, so YAML-configured socket paths
// are respected. KUKEOND_SOCKET is registered via DefineKVNoViperDefault
// (cmd/config/env.go) precisely so this IsSet check stays meaningful —
// viper.SetDefault would trip IsSet even with no env/flag/YAML and
// silently disable derivation.
func applyRunPathImpliesKukeondSocket(cmd *cobra.Command, runPath string) {
	if viper.IsSet(config.KUKEOND_SOCKET.ViperKey) {
		return
	}
	if !flagChanged(cmd, "run-path") && !envSet(config.KUKEON_ROOT_RUN_PATH) {
		return
	}
	viper.Set(config.KUKEOND_SOCKET.ViperKey, filepath.Join(runPath, "kukeond.sock"))
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

// applyServerConfiguration layers the loaded ServerConfiguration on top of
// viper for fields the operator did not explicitly set on the command line
// or via environment. Order of precedence: explicit `--flag` > env >
// ServerConfiguration > flag default. Flag-changed values win so a one-off
// `--kukeond-image` keeps overriding the on-disk document; env-set values
// win because `viper.Set` would otherwise override viper's env binding.
func applyServerConfiguration(cmd *cobra.Command, spec v1beta1.ServerConfigurationSpec) {
	flags := cmd.Flags()
	if spec.Socket != "" && !envSet(config.KUKEOND_SOCKET) {
		viper.Set(config.KUKEOND_SOCKET.ViperKey, spec.Socket)
	}
	if spec.RunPath != "" && !flags.Changed("run-path") && !envSet(config.KUKEON_ROOT_RUN_PATH) {
		viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, spec.RunPath)
	}
	if spec.ContainerdSocket != "" && !flags.Changed("containerd-socket") &&
		!envSet(config.KUKEON_ROOT_CONTAINERD_SOCKET) {
		viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, spec.ContainerdSocket)
	}
	if spec.LogLevel != "" && !flags.Changed("log-level") && !envSet(config.KUKEON_ROOT_LOG_LEVEL) {
		viper.Set(config.KUKEON_ROOT_LOG_LEVEL.ViperKey, spec.LogLevel)
	}
	if spec.KukeondImage != "" && !flags.Changed("kukeond-image") &&
		!envSet(config.KUKE_INIT_KUKEOND_IMAGE) {
		viper.Set(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey, spec.KukeondImage)
	}
	if spec.ContainerdNamespaceSuffix != "" &&
		!flags.Changed("containerd-namespace-suffix") &&
		!envSet(config.KUKEON_ROOT_NAMESPACE_SUFFIX) {
		viper.Set(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey, spec.ContainerdNamespaceSuffix)
	}
	if spec.CgroupRoot != "" &&
		!flags.Changed("cgroup-root") &&
		!envSet(config.KUKEON_ROOT_CGROUP_ROOT) {
		viper.Set(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey, spec.CgroupRoot)
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
// by the release pipeline) with a tag matching config.Version. Dev builds
// whose version isn't a release tag fall back to :latest.
func resolveKukeondImage() string {
	if override := viper.GetString(config.KUKE_INIT_KUKEOND_IMAGE.ViperKey); override != "" {
		return override
	}

	repo := strings.TrimSpace(config.KukeondImageRepo)
	if repo == "" {
		repo = "ghcr.io/eminwux/kukeon"
	}

	tag := strings.TrimSpace(config.Version)
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
func installForwardAdmission(ctx context.Context, logger *slog.Logger) error {
	if err := firewall.NewInstaller(logger).Install(ctx); err != nil {
		return fmt.Errorf("install forward admission chain: %w", err)
	}
	return nil
}

// applyKukeonOwnership chown+chmods /run/kukeon (socket's parent dir,
// already created by ensureSocketDir during bootstrap) and /opt/kukeon
// (RunPath). Both end up root:kukeon mode 0o750 so the kukeon group can
// traverse without world access.
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
	if err := sysuser.ChownAndChmod(runDir, 0, ensure.GID, kukeonRunDirMode); err != nil {
		return r, fmt.Errorf("apply kukeon ownership to %q: %w", runDir, err)
	}
	r.RunDirApplied = true
	if err := sysuser.ChownTreeAndChmod(
		runPath, 0, ensure.GID, kukeonRunPathDirMode, kukeonRunPathFileMode,
	); err != nil {
		return r, fmt.Errorf("apply kukeon ownership to %q: %w", runPath, err)
	}
	r.RunPathApplied = true
	return r, nil
}

func runInit(cmd *cobra.Command, _ []string) error {
	logger, ok := cmd.Context().Value(types.CtxLogger).(*slog.Logger)
	if !ok || logger == nil {
		return errdefs.ErrLoggerNotFound
	}

	if err := kukshared.RequireRoot("kuke init"); err != nil {
		return err
	}

	serverConfigPath := viper.GetString(config.KUKE_INIT_SERVER_CONFIGURATION.ViperKey)
	if serverConfigPath == "" {
		serverConfigPath = config.DefaultServerConfigurationFile()
	}
	serverDoc, err := serverconfig.Load(serverConfigPath)
	if err != nil {
		return fmt.Errorf("load server configuration: %w", err)
	}
	applyServerConfiguration(cmd, serverDoc.Spec)

	if cfgErr := consts.ConfigureRuntime(
		viper.GetString(config.KUKEON_ROOT_NAMESPACE_SUFFIX.ViperKey),
		viper.GetString(config.KUKEON_ROOT_CGROUP_ROOT.ViperKey),
	); cfgErr != nil {
		return fmt.Errorf("configure runtime: %w", cfgErr)
	}

	image := resolveKukeondImage()

	runPath := viper.GetString(config.KUKEON_ROOT_RUN_PATH.ViperKey)
	if runPath == "" {
		runPath = config.DefaultRunPath()
	}

	// Must run after applyServerConfiguration (which may viper.Set the
	// socket from YAML) and after runPath is resolved (the derived value
	// is `<runPath>/kukeond.sock`).
	applyRunPathImpliesKukeondSocket(cmd, runPath)

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

	permsReport, ownErr := applyKukeonOwnership(socketPath, runPath, ensure)
	if ownErr != nil {
		return ownErr
	}

	printBootstrapReport(cmd, report, permsReport)

	if viper.GetBool(config.KUKE_INIT_NO_WAIT.ViperKey) {
		return nil
	}

	if waitErr := waitForKukeondReady(cmd.Context(), socketPath, kukeondReadyTimeout); waitErr != nil {
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

// waitForKukeondReady polls the kukeond socket with Ping until it responds or
// the timeout expires. The socket file may appear before the RPC handler is
// actually serving, so we dial AND ping.
func waitForKukeondReady(ctx context.Context, socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if time.Now().After(deadline) {
			if lastErr != nil {
				return fmt.Errorf("timed out after %s: %w", timeout, lastErr)
			}
			return fmt.Errorf("timed out after %s", timeout)
		}

		attemptCtx, cancel := context.WithTimeout(ctx, kukeondReadyTick)
		err := pingKukeond(attemptCtx, socketPath)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(kukeondReadyTick):
		}
	}
}

func pingKukeond(ctx context.Context, socketPath string) error {
	d := net.Dialer{Timeout: kukeondReadyTick}
	conn, err := d.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	_ = conn.Close()

	client := kukeonv1.NewUnixClient(socketPath, kukeonv1.WithDialTimeout(kukeondReadyTick))
	defer func() { _ = client.Close() }()

	if err = client.Ping(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}
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
			perms.RunDirPath, perms.Group, formatPosixMode(kukeonRunDirMode),
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
	if section.CellCreated {
		cmd.Println(fmt.Sprintf("    - cell %q: created (image %s)", section.CellName, image))
	} else {
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
