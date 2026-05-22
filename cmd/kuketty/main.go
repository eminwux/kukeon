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
	"os/signal"
	"syscall"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshlogging "github.com/eminwux/sbsh/pkg/logging"
	sbshserver "github.com/eminwux/sbsh/pkg/terminal/server"
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
// socket listen, server bring-up). 70 is BSD EX_SOFTWARE. The workload's
// own exit code is not surfaced through kuketty — the sbsh server reports
// terminal-exit via its event loop and the attached client sees the
// workload's status through the RPC, not the wrapper's exit code.
const exitCodeInternal = 70

func main() {
	if err := run(os.Args[1:]); err != nil {
		var usageErr *usageError
		switch {
		case errors.As(err, &usageErr):
			fmt.Fprintf(os.Stderr, "kuketty: %v\n", err)
			os.Exit(exitCodeUsage)
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

	listener, err := claimSocketListener(spec.SocketFile)
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
	if serveErr := srv.Serve(ctx, listener); serveErr != nil && !isCleanShutdown(serveErr) {
		return fmt.Errorf("server.Serve: %w", serveErr)
	}
	return nil
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

// claimSocketListener removes any stale inode at the spec'd socket path
// (a previous crash on the same in-container path would otherwise hit
// EADDRINUSE on the first Listen) and binds a fresh listener. The
// returned listener is owned by the caller — sbsh's server facade closes
// it during shutdown via its underlying runner.
func claimSocketListener(socketPath string) (net.Listener, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", socketPath, err)
	}
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	return l, nil
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
