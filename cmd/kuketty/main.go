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
// bind-mounted metadata file (no CLI flags), runs the wrapped command under
// a PTY, and creates the attach-socket inode at the metadata-declared path.
//
// Phase 1 (issue #165) scope: PTY exec + socket-inode creation. The
// attach-socket RPC protocol — the JSON-RPC + SCM_RIGHTS surface that
// `kuke attach` consumes via github.com/eminwux/sbsh/pkg/attach — lands in
// phase 1b (#410). Capture-file (phase 2 / #288), log-file (phase 3 / #289),
// and prompt/onInit rendering (phase 4 / #290) follow.
//
// kuketty is a standalone binary — not argv[0]-dispatched from the kuke
// multi-call binary — so its import set stays stdlib + a small pty helper.
// See the issue body's "Why a separate binary" note for the per-process RSS
// + startup-time rationale at attachable-container scale.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/creack/pty"
	"github.com/eminwux/kukeon/pkg/kuketty"
)

// metadataPath is the fixed in-container path of the kukeond-rendered terminal
// metadata file. The daemon bind-mounts the per-container metadata over this
// path at OCI spec build time (see internal/ctr/attachable.go). Fixed (not a
// flag) per the issue's "no CLI flags for runtime configuration" redirect.
const metadataPath = "/.kukeon/kuketty/metadata.json"

// exitCodeUsage is returned when the wrapper invocation is malformed (no `--`
// separator or empty workload). 64 is the BSD EX_USAGE convention; matches
// sbsh's convention so an operator who replaced sbsh with kuketty in a
// minimal smoke test does not get a confusingly different exit code.
const exitCodeUsage = 64

// exitCodeInternal is returned when kuketty itself fails before the workload
// runs (metadata parse, PTY setup, socket listen). 70 is BSD EX_SOFTWARE.
const exitCodeInternal = 70

func main() {
	if err := run(os.Args[1:]); err != nil {
		var (
			usageErr   *usageError
			internalEr *internalError
			exitErr    *exec.ExitError
		)
		switch {
		case errors.As(err, &usageErr):
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeUsage)
		case errors.As(err, &exitErr):
			os.Exit(exitErr.ExitCode())
		case errors.As(err, &internalEr):
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeInternal)
		default:
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeInternal)
		}
	}
}

// usageError is the typed wrapper for malformed invocations so main() can map
// it to exitCodeUsage without string-matching the error message.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// internalError wraps pre-workload failures (metadata, PTY, socket setup) so
// they exit with EX_SOFTWARE rather than the workload's exit code, which is
// reserved for the wrapped command itself.
type internalError struct{ err error }

func (e *internalError) Error() string { return e.err.Error() }
func (e *internalError) Unwrap() error { return e.err }

// run parses the wrapper invocation, loads the metadata file, claims the
// attach-socket inode, and execs the workload under a PTY. Returns a typed
// error so main() can map to the right exit code. The returned error is the
// child's *exec.ExitError on a non-zero workload exit, so the wrapper
// transparently propagates the workload's exit code.
func run(args []string) error {
	workload, err := parseArgs(args)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return &internalError{err: fmt.Errorf("read metadata %s: %w", metadataPath, err)}
	}
	md, err := kuketty.Unmarshal(data)
	if err != nil {
		return &internalError{err: err}
	}

	if listenErr := claimSocketInode(md.Spec.Socket); listenErr != nil {
		return &internalError{err: listenErr}
	}

	if err = execUnderPTY(workload); err != nil {
		return err
	}
	return nil
}

// parseArgs splits the wrapper argv at the `--` separator and returns the
// workload command. The `--` is the only positional contract kuketty
// exposes — without it, the wrapper cannot tell its own residual args from
// the workload's, and silently guessing would mask kukeond regressions.
func parseArgs(args []string) ([]string, error) {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return nil, &usageError{msg: "missing '--' separator between kuketty and the wrapped workload"}
	}
	workload := args[sep+1:]
	if len(workload) == 0 {
		return nil, &usageError{msg: "no workload command after '--'"}
	}
	return workload, nil
}

// claimSocketInode creates the per-container attach-socket inode at
// md.Path so it is host-visible through the per-container tty bind mount.
// Phase 1 listens and goes silent: any client that connects (notably
// `kuke attach` via pkg/attach) is closed immediately. Phase 1b (#410)
// replaces this with the JSON-RPC + SCM_RIGHTS server `kuke attach`
// actually consumes.
//
// Listening (not just touch+close) is what gives the socket the "socket"
// file-type so the host-side waitForSocket helper (e2e) and any
// administrative tooling that stats it can recognise it before the RPC
// server is ready. A bare regular file would lie about the inode shape.
//
// Mode and GID are applied after listen() so the kukeon-group operator on
// the host can dial the socket once phase 1b lands — preserving the
// host-side group-traversal contract the sbsh wrapper already honored.
// Empty Mode / zero GID is the legacy fallback that mirrors sbsh's 0600
// owner-only default.
func claimSocketInode(s kuketty.SocketSpec) error {
	// Remove any stale inode at the path. A restart that lands on top of
	// a previous run's leftover would otherwise hit EADDRINUSE before the
	// first Listen.
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket %s: %w", s.Path, err)
	}
	l, err := net.Listen("unix", s.Path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Path, err)
	}
	if s.Mode != "" {
		mode, parseErr := strconv.ParseUint(s.Mode, 8, 32)
		if parseErr != nil {
			return fmt.Errorf("parse socket mode %q: %w", s.Mode, parseErr)
		}
		if chmodErr := os.Chmod(s.Path, os.FileMode(mode)); chmodErr != nil {
			return fmt.Errorf("chmod socket %s to %s: %w", s.Path, s.Mode, chmodErr)
		}
	}
	if s.GID > 0 {
		if chownErr := os.Chown(s.Path, -1, s.GID); chownErr != nil {
			return fmt.Errorf("chown socket %s to gid %d: %w", s.Path, s.GID, chownErr)
		}
	}
	// Drain Accept until the socket is closed. Phase 1 has no RPC, so a
	// client that connects is immediately disconnected; phase 1b swaps
	// this drainer for the real server.
	go func() {
		for {
			c, acceptErr := l.Accept()
			if acceptErr != nil {
				return
			}
			_ = c.Close()
		}
	}()
	return nil
}

// execUnderPTY spawns the workload under a fresh pseudo-terminal, wires the
// container task's stdio onto the master end, forwards SIGWINCH-equivalent
// resize requests (none yet — phase 1b adds them), and propagates SIGINT/
// SIGTERM to the child. Returns the child's *exec.ExitError on non-zero
// exit so main() can transparently propagate the workload's exit code.
func execUnderPTY(workload []string) error {
	cmd := exec.Command(workload[0], workload[1:]...)
	cmd.Env = os.Environ()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		return &internalError{err: fmt.Errorf("pty.Start %v: %w", workload, err)}
	}
	defer func() { _ = ptmx.Close() }()

	// containerd's task IO forwards the container's stdio to the runtime
	// shim's pipes; tying the PTY master to those streams is what makes
	// `ctr task attach` / `kuke log` see the workload's output. Phase 1b
	// overrides this on attached clients via the RPC server's Subscribe
	// stream; until then, the container's foreground output stays the
	// shim's stdout/stderr.
	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()
	go func() { _, _ = io.Copy(os.Stdout, ptmx) }()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	// Wait returns *exec.ExitError on non-zero exit — propagate verbatim
	// so main() reflects the workload's exit code.
	return cmd.Wait()
}
