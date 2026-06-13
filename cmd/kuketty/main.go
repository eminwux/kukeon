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

// kuketty is the kukeon-owned terminal wrapper that runs inside an attachable
// container in place of sbsh. It reads its runtime configuration from a
// bind-mounted kukeon ContainerDoc (no CLI flags beyond an optional --config
// override), builds the sbsh TerminalSpec from kukeon's own ContainerSpec
// (issue #641), and serves the JSON-RPC + SCM_RIGHTS attach protocol via
// sbsh's public pkg/terminal/server facade, so `kuke attach` consumes the same
// wire protocol it does on the host.
//
// kuketty is a standalone binary — not argv[0]-dispatched from the kuke
// multi-call binary — so its import set stays small (the sbsh facade closure
// is well clear of the kuke + kukeond containerd / gRPC / protobuf closure).
// See issue #165 for the per-process RSS + startup-time rationale at
// attachable-container scale.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshlogging "github.com/eminwux/sbsh/pkg/logging"
	sbshserver "github.com/eminwux/sbsh/pkg/terminal/server"
	"golang.org/x/sys/unix"
)

// defaultConfigPath is the fixed in-container path of the kukeond-rendered
// ContainerDoc. The daemon bind-mounts the per-container metadata file over
// this path at OCI spec build time (see internal/ctr/attachable.go). Kept in
// sync with ctr.AttachableMetadataPath.
const defaultConfigPath = "/.kukeon/kuketty/metadata.json"

// exitCodeUsage is returned when invocation is malformed (e.g. unknown flag,
// missing config file argument). 64 is the BSD EX_USAGE convention; matches
// sbsh's convention so an operator who replaced sbsh with kuketty in a
// minimal smoke test does not get a confusingly different exit code.
const exitCodeUsage = 64

// exitCodeInternal is returned when kuketty itself fails (config parse,
// socket listen, server bring-up). 70 is BSD EX_SOFTWARE. It is reserved for
// genuine wrapper failures: the *workload's* own exit is surfaced separately
// through kuketty's exit code (see workloadExitCode / workloadExitError and
// issue #1273), so the daemon's task-exit-code → container/cell-state
// derivation (#1267/#1269) reflects the workload's fate rather than mapping
// every clean workload exit to this internal-error code.
const exitCodeInternal = 70

// codeRe extracts the workload child's numeric exit code from sbsh's PID-1
// (init-mode) EvCmdExited cause, which embeds it as "code=N"
// (sbsh internal/terminal/terminalrunner/lifecycle.go).
var codeRe = regexp.MustCompile(`code=(-?\d+)`)

// workloadExitError carries the workload child's non-zero exit code up to
// main() so it can propagate it as kuketty's own exit code. It is distinct
// from a kuketty-internal failure (exitCodeInternal): a non-zero workload
// exit is the workload's fate, not a wrapper bug, and must reach the daemon
// verbatim so the container/cell lands in Error with the workload's status.
type workloadExitError struct{ code int }

func (e *workloadExitError) Error() string {
	return fmt.Sprintf("workload exited with status %d", e.code)
}

// workloadExitCode reports whether err is sbsh's "workload child exited"
// terminating cause and, if so, the child's exit code. sbsh v0.13.1 (the
// pinned release) has no machine-readable exit-code field on EvCmdExited — it
// embeds the code in the terminating error: an *exec.ExitError on the non-init
// cmd.Wait path, a "(code 0)" literal on a non-init clean exit, or a "code=N"
// string on the PID-1 reaper path (internal/terminal/terminalrunner/
// lifecycle.go). This recognizes all three carriers. ok=false means err is a
// genuine kuketty-internal failure that should map to exitCodeInternal.
//
// Before sbsh v0.13.1 a clean PID-1 exit could surface here as the benign
// PTY-master read EIO ("read /dev/ptmx: input/output error") instead of the
// "code=N" carrier: the EIO is the immediate kernel side-effect of the child
// closing its tty and it reliably preempted the reaper's EvCmdExited inside
// sbsh's runLoop, so Serve returned the EIO as its cause and kuketty flipped a
// clean exit to exitCodeInternal (issue #1282). sbsh v0.13.1's server.Serve now
// waits briefly for the authoritative EvCmdExited before returning a PTY-read
// EvError (eminwux/sbsh#439), so the benign race no longer reaches here — a
// raw PTY-read EIO that *does* surface now means a genuine error with the child
// still alive, and ok=false / exitCodeInternal is the correct mapping for it.
func workloadExitCode(err error) (code int, ok bool) {
	if err == nil {
		return 0, true
	}
	// Non-init cmd.Wait path: the *exec.ExitError carries the real code (and
	// signal info on signaled deaths), wrapped by sbsh with %w.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), true
	}
	msg := err.Error()
	if !strings.Contains(msg, "shell process exited") {
		return 0, false
	}
	// Non-init clean exit: errors.New("shell process exited (code 0)").
	if strings.Contains(msg, "(code 0)") {
		return 0, true
	}
	// PID-1 init mode: fmt.Errorf("shell process exited: code=%d", code).
	if m := codeRe.FindStringSubmatch(msg); m != nil {
		if c, perr := strconv.Atoi(m[1]); perr == nil {
			return c, true
		}
	}
	// "shell process exited" with no recoverable code — an abnormal reaper or
	// shutdown-race path (sbsh #398: reaper channel closed, or the tracked-exit
	// wait was cancelled by Close). These arise during teardown, not a workload
	// crash, so treat them as a clean termination rather than flipping a
	// stopped cell to Error.
	return 0, true
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		var usageErr *usageError
		var wlErr *workloadExitError
		switch {
		case errors.As(err, &usageErr):
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeUsage)
		case errors.As(err, &wlErr):
			// The workload itself exited non-zero. Propagate its code so the
			// daemon records the container/cell as Error with the workload's
			// real status (#1273) — this is the workload's fate, not a kuketty
			// failure, so it gets no "kuketty:" diagnostic.
			os.Exit(wlErr.code)
		default:
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeInternal)
		}
	}
}

// usageError is the typed wrapper for malformed invocations so main() can
// map it to exitCodeUsage without string-matching the error message.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// run parses the (single optional) flag, loads the ContainerDoc from disk,
// builds the sbsh TerminalSpec from kukeon's own ContainerSpec, binds the
// control socket, and hands the listener + spec to sbsh's server facade.
// Returns the terminating cause; main() maps the error class to an exit code.
func run(args []string) error {
	configPath, err := parseArgs(args)
	if err != nil {
		return err
	}

	doc, err := loadContainerDoc(configPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()

	// Build the sbsh TerminalSpec from kukeon's ContainerSpec (issue #641).
	// A bootstrap stderr logger covers the build step; the file-backed logger
	// kuketty serves under is opened from the resulting spec below.
	buildLogger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	spec, err := buildTerminalSpec(ctx, buildLogger, doc.Spec)
	if err != nil {
		return err
	}

	listener, err := claimSocketListener(ctx, spec.SocketFile, spec.SocketMode, spec.SocketGID)
	if err != nil {
		return err
	}

	logger, closeLogger, err := openTerminalLogger(spec.LogFile, spec.LogLevel)
	if err != nil {
		return err
	}
	defer closeLogger()

	// Pre-Serve step (issue #617): clone/fetch the container's declared repos
	// before the workload starts. A required-repo failure returns here, so
	// kuketty exits non-zero before sbshserver.Serve and the daemon observes
	// the task as Failed (the RPC below is never reached on that path — AC #5).
	// An empty repos[] is a no-op. The per-repo outcomes feed the GetSetupStatus
	// verb registered on the control server below (issue #642).
	repoStatuses, err := processRepos(ctx, doc.Spec.Repos, logger)
	if err != nil {
		return err
	}

	// Pre-Serve step (issue #635): run the container's runOn: create TtyStages
	// to completion before the workload starts. Like a required-repo failure, a
	// failed create stage returns here so kuketty exits non-zero before
	// sbshserver.Serve and the daemon observes the task as Failed. runOn: start
	// (and absent) stages are not run here — they were forwarded to sbsh's
	// Stages.OnInit at buildTerminalSpec and run in-shell every boot. The
	// per-stage outcomes feed the GetSetupStatus verb registered below (issue
	// #689); on the failure path stageStatuses is discarded with the error and
	// the verb is never reached.
	stageStatuses, err := processStages(ctx, createStages(doc.Spec.Tty), logger)
	if err != nil {
		return err
	}

	// Register the GetSetupStatus verb on the same control socket the daemon
	// dials for `kuke attach`, so kukeond can pull the repo + create-stage
	// outcomes post-Serve and write ContainerStatus.Repos / .Stages (issues
	// #642, #689). ContainerStatus is the single source of truth — there is no
	// status file in the container.
	srv, err := sbshserver.New(spec, logger, setupStatusOption(repoStatuses, stageStatuses)...)
	if err != nil {
		return fmt.Errorf("server.New: %w", err)
	}
	// The workload's exit is surfaced through kuketty's own exit code so the
	// daemon's task-exit-code → container/cell-state derivation (#1267/#1269)
	// reflects the workload's fate, not the wrapper's (issue #1273). sbsh
	// reports terminal exit as the Serve terminating cause:
	//   - a clean operator-initiated shutdown (ctx cancel / Stop) → exit 0;
	//   - a workload that exited carries the child's code: 0 → Exited (return
	//     nil), non-zero → Error (return *workloadExitError so main() exits
	//     with that code);
	//   - anything else is a genuine kuketty-internal failure → exitCodeInternal.
	serveErr := srv.Serve(ctx, listener)
	if isCleanShutdown(serveErr) {
		return nil
	}
	if code, ok := workloadExitCode(serveErr); ok {
		if code == 0 {
			return nil
		}
		return &workloadExitError{code: code}
	}
	return fmt.Errorf("server.Serve: %w", serveErr)
}

// parseArgs accepts a single optional `--config <path>` override. Any other
// argument is a usage error — kuketty has no other runtime configuration
// flags (issue #410, extending issue #165's no-flags rule). The OCI
// injection path never sets the override; it is provided for test / debug
// ergonomics only.
func parseArgs(args []string) (string, error) {
	fs := flag.NewFlagSet("kuketty", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath,
		"path to the kuketty config (a kukeon ContainerDoc JSON document); "+
			"normally bind-mounted by kukeond at "+defaultConfigPath)
	if err := fs.Parse(args); err != nil {
		return "", &usageError{msg: err.Error()}
	}
	if fs.NArg() > 0 {
		return "", &usageError{
			msg: fmt.Sprintf("unexpected positional argument(s): %v", fs.Args()),
		}
	}
	return *configPath, nil
}

// loadContainerDoc reads the bind-mounted config file, decodes it as a kukeon
// ContainerDoc, and validates the APIVersion + Kind discriminator so a kuketty
// binary that loaded a malformed (or wrong-schema) file refuses cleanly rather
// than silently misinterpreting fields. The socket path is no longer validated
// here — kuketty derives it from the kukeon contract constant when it builds
// the TerminalSpec (issue #641), so it is never read off the doc.
func loadContainerDoc(path string) (*v1beta1.ContainerDoc, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var doc v1beta1.ContainerDoc
	if err = json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if doc.APIVersion != v1beta1.APIVersionV1Beta1 {
		return nil, fmt.Errorf("config %s: apiVersion %q, want %q",
			path, doc.APIVersion, v1beta1.APIVersionV1Beta1)
	}
	if doc.Kind != v1beta1.KindContainer {
		return nil, fmt.Errorf("config %s: kind %q, want %q",
			path, doc.Kind, v1beta1.KindContainer)
	}
	return &doc, nil
}

// openTerminalLogger pre-creates the per-terminal log file via sbsh's
// public pkg/logging.NewFileLogger helper and returns the file-backed
// slog.Logger plus a close func the caller must defer. sbsh's terminal
// runner chmods the LogFile path during StartTerminal without O_CREATE,
// so out-of-tree callers of pkg/terminal/server must pre-create the file
// at the matching mode (per v0.11.1's pkg/logging package contract). An
// empty path falls through to a discard logger for test fixtures that
// bypass pkg/builder.BuildTerminalSpec; in the OCI-injection path the
// daemon always stamps Spec.LogFile = ctr.AttachableKukettyLogPath
// (issue #599).
//
// loglevel is the operator-supplied Tty.LogLevel from the cell schema,
// already normalized to "info" by the renderer when the cell left it
// empty (sbsh's NewFileLogger rejects an empty level).
func openTerminalLogger(logfile, loglevel string) (*slog.Logger, func(), error) {
	if logfile == "" {
		return slog.New(slog.NewTextHandler(io.Discard, nil)), func() {}, nil
	}
	if loglevel == "" {
		loglevel = "info"
	}
	fl, err := sbshlogging.NewFileLogger(logfile, loglevel)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file %s: %w", logfile, err)
	}
	return fl.Logger, func() { _ = fl.File.Close() }, nil
}

// defaultSocketMode is the legacy owner-only mode applied when the spec
// leaves SocketMode at its zero value. Mirrors sbsh's terminalrunner
// defaultSocketMode so the two paths land at the same on-disk perms.
const defaultSocketMode os.FileMode = 0o600

// listenerUmaskMu serializes the umask save/set/Listen/restore sequence
// in claimSocketListener against in-process concurrent callers. umask(2)
// is process-wide (it lives in the kernel's shared fs_struct on Linux),
// so two concurrent claims without this mutex could observe each other's
// temporary umask. The mutex does not isolate the brief Listen window
// from file creations in *other* packages that share this process —
// that's an inherent cost of using umask to plug the bind-then-chmod
// EACCES window. Mirrors sbsh's listenerUmaskMu rationale.
//
//nolint:gochecknoglobals // process-wide invariant guard, like the umask it serializes
var listenerUmaskMu sync.Mutex

// claimSocketListener removes any stale inode at the spec'd socket path
// (a previous crash on the same in-container path would otherwise hit
// EADDRINUSE on the first Listen) and binds a fresh listener at the
// configured mode + group.
//
// The bind runs under a temporary umask of ^mode & 0o777 (with
// runtime.LockOSThread) so the inode is born at mode and there is no
// window during which a group-member client dialing the socket hits
// EACCES because the daemon's umask masked off group access. A belt-
// and-braces os.Chmod + os.Chown follow immediately so the on-disk
// perms are correct even on the unlikely path where Listen returned
// before the umask took effect. Mirrors sbsh's listenUnixWithMode +
// applySocketPerms recipe (sbsh@v0.12.1/internal/terminal/terminalrunner/
// sockets.go), which the sbsh-side facade only applies later inside
// UseListener → bringUp — i.e. after kuketty's pre-Serve work
// (processRepos, processStages) has already run, reopening the same
// EACCES window for the duration of that work and indefinitely if
// either pre-Serve step fails. Issue #916.
//
// mode == 0 falls back to defaultSocketMode so callers can pass the
// raw spec field through without a guard. gid == nil leaves the
// group unchanged (typical when no kukeon group is configured;
// buildTerminalSpec already gates WithSocketGID on a non-zero GID).
// The chown form is Chown(path, -1, gid) so the listener's uid stays
// the owner; only the group is rewritten.
//
// The returned listener is owned by the caller — sbsh's server facade
// closes it during shutdown via its underlying runner.
func claimSocketListener(ctx context.Context, socketPath string, mode os.FileMode, gid *int) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}
	if mode == 0 {
		mode = defaultSocketMode
	}
	l, err := listenUnixWithMode(ctx, socketPath, mode)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if chmodErr := os.Chmod(socketPath, mode); chmodErr != nil {
		_ = l.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", socketPath, chmodErr)
	}
	if gid != nil {
		if chownErr := os.Chown(socketPath, -1, *gid); chownErr != nil {
			_ = l.Close()
			return nil, fmt.Errorf("chown socket %s: %w", socketPath, chownErr)
		}
	}
	return l, nil
}

// listenUnixWithMode binds an AF_UNIX listener with the process umask
// temporarily set so the socket inode is born at the configured mode,
// not at (0o666 & ~processUmask). Mirrors sbsh's listenUnixWithMode
// (sbsh@v0.12.1/internal/terminal/terminalrunner/sockets.go).
//
// runtime.LockOSThread pins the goroutine so the save/restore sequence
// runs on a single OS thread; listenerUmaskMu serializes the temporary
// umask against in-process concurrent callers.
func listenUnixWithMode(ctx context.Context, socketPath string, mode os.FileMode) (net.Listener, error) {
	listenerUmaskMu.Lock()
	defer listenerUmaskMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	//nolint:mnd // 0o777 is the standard permission-bit mask for umask(2)
	prev := unix.Umask(int(^mode & 0o777))
	defer unix.Umask(prev)
	var lc net.ListenConfig
	return lc.Listen(ctx, "unix", socketPath)
}

// isCleanShutdown reports whether the server.Serve terminating cause
// represents an operator-initiated end of session rather than an internal
// failure. Context cancellation (SIGINT/SIGTERM forwarded by the signal
// handler) and a Stop call are both expected outcomes — they should map
// to exit 0 from the workload-supervisor's perspective.
func isCleanShutdown(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return false
}
