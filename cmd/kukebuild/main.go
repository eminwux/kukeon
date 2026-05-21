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

// kukebuild is the kukeon-owned native image builder. It embeds BuildKit as a
// library, drives BuildKit's Dockerfile frontend through an in-process
// Controller backed by a containerd worker, and writes the resulting OCI image
// straight into the target realm's containerd namespace (<realm>.<suffix>,
// where the suffix is resolved from the operator's kukeond.yaml — see
// config.go). This removes the docker / nerdctl + buildkitd build dependency
// from kukeon's "self-contained runtime" pitch — see issue #522.
//
// kukebuild lives in its own Go module (cmd/kukebuild/go.mod, issue #655) so
// BuildKit's `go 1.25` floor and its ~160-package moby / runc / grpc closure
// advance independently of the root module, which stays on go 1.24.5 and links
// zero BuildKit packages. The module is wired for local dev via the repo-root
// go.work. kukebuild imports no kukeon package: the only cross-module surface
// (the realm->namespace suffix) is resolved from the public kukeond.yaml
// config key, not a private internal/consts symbol.
//
// kukebuild is a standalone binary — not argv[0]-dispatched from the kuke
// multi-call binary — for the same reason kuketty is (see cmd/kuketty/main.go):
// keep BuildKit's transitive moby / runc / grpc closure entirely out of the
// kuke and kukeond import sets. `kuke build` is a thin CLI shim
// (cmd/kuke/build) that locates this binary on PATH and exec's it; kukebuild
// spawns on demand and exits when the build completes (no long-running
// embedded buildkitd).
//
// Phase 1 (#522) wires the engine and validates a trivial single-stage
// Dockerfile. Multi-stage validation against kukeon's own Dockerfile, plus
// --secret / --cache-* / --push / --platform flags, are deferred to the phase
// 1b / 2 follow-ups (#616, #523, #524, #646).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

// exitCodeUsage is returned when invocation is malformed (unknown flag,
// missing tag, missing context). 64 is the BSD EX_USAGE convention; matches
// the sibling kuketty binary so an operator sees a consistent exit code.
const exitCodeUsage = 64

// exitCodeInternal is returned when kukebuild itself fails (containerd
// unreachable, worker bring-up, solve error). 70 is BSD EX_SOFTWARE, again
// matching kuketty.
const exitCodeInternal = 70

func main() {
	if err := run(os.Args[1:]); err != nil {
		var usageErr *usageError
		if errors.As(err, &usageErr) {
			fmt.Fprintf(os.Stderr, "kukebuild: %v\n", err)
			os.Exit(exitCodeUsage)
		}
		fmt.Fprintf(os.Stderr, "kukebuild: %v\n", err)
		os.Exit(exitCodeInternal)
	}
}

// usageError is the typed wrapper for malformed invocations so main() can map
// it to exitCodeUsage without string-matching the error message.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// run parses the args, wires the BuildKit engine, and runs the solve. Returns
// the terminating cause; main() maps the error class to an exit code. A
// SIGINT/SIGTERM cancels the build context so an interrupted build tears the
// in-process controller down cleanly rather than leaking a containerd lease.
func run(args []string) error {
	cfg, err := parseArgs(args)
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

	return runBuild(ctx, cfg, os.Stderr)
}

// buildConfig is the resolved invocation: what to build, where the context
// lives, and which realm namespace + containerd socket the image lands in.
type buildConfig struct {
	// contextDir is the build context root (the positional argument).
	contextDir string
	// dockerfile is the resolved path to the Dockerfile; defaults to
	// <contextDir>/Dockerfile when -f is not supplied.
	dockerfile string
	// tag is the image reference the built image is named with (-t). Required.
	tag string
	// realm names the kukeon realm whose containerd namespace
	// (<realm>.kukeon.io) the image is written into.
	realm string
	// buildArgs are the --build-arg KEY=VALUE pairs forwarded to the
	// Dockerfile frontend.
	buildArgs map[string]string
	// containerdSocket is the host containerd socket the containerd worker
	// backend connects to.
	containerdSocket string
	// root is the directory BuildKit uses for its own state (cache.db,
	// history.db, snapshot metadata). Distinct from containerd's content
	// store.
	root string
	// kukeondConfig is the path to the kukeond.yaml from which kukebuild
	// resolves the realm->namespace suffix (--kukeond-config). Empty falls
	// back to /etc/kukeon/kukeond.yaml and then the hardcoded default suffix;
	// see resolveNamespaceSuffix.
	kukeondConfig string
}

// defaultContainerdSocket is the standalone host containerd socket the
// containerd worker connects to. Matches the socket the project smoke test
// (CLAUDE.md "Local smoke test") and `kuke image load` operate against — not
// the docker-private containerd.
const defaultContainerdSocket = "/run/containerd/containerd.sock"

// defaultBuildRoot is where BuildKit keeps its own state (build cache,
// history, snapshot metadata) — separate from containerd's content store, per
// the issue notes. Under /var/lib so it survives across builds for cache
// reuse; root-owned, matching kukebuild's root-on-host posture.
const defaultBuildRoot = "/var/lib/kukebuild"

// defaultRealm matches the convention in cmd/kuke/image: the user-owned
// `default` realm when --realm is not supplied.
const defaultRealm = "default"

// repeatableFlag accumulates a flag that may appear multiple times (e.g.
// --build-arg K=V --build-arg J=W) into an ordered slice.
type repeatableFlag []string

func (r *repeatableFlag) String() string { return strings.Join(*r, ",") }

func (r *repeatableFlag) Set(v string) error {
	*r = append(*r, v)
	return nil
}

// parseArgs resolves the kukebuild invocation. It mirrors the `kuke build`
// CLI shim's surface (cmd/kuke/build) since the shim forwards these flags
// verbatim. A missing -t or context directory is a usage error; an
// unparseable --build-arg likewise. Output is discarded so a parse error
// surfaces as a typed usageError through main() rather than the flag
// package's default stderr dump.
func parseArgs(args []string) (*buildConfig, error) {
	fs := flag.NewFlagSet("kukebuild", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	file := fs.String("file", "", "path to the Dockerfile (default <context>/Dockerfile)")
	fs.StringVar(file, "f", "", "alias for --file")
	tag := fs.String("tag", "", "image reference for the built image, name:tag")
	fs.StringVar(tag, "t", "", "alias for --tag")
	realm := fs.String("realm", defaultRealm, "target realm; the image lands in <realm>.kukeon.io")
	containerdSocket := fs.String(
		"containerd-socket",
		defaultContainerdSocket,
		"host containerd socket the worker connects to",
	)
	root := fs.String("root", defaultBuildRoot, "directory for BuildKit's own state (cache, history, snapshots)")
	kukeondConfig := fs.String(
		"kukeond-config",
		"",
		"path to kukeond.yaml; resolves the realm->namespace suffix (default /etc/kukeon/kukeond.yaml)",
	)

	var buildArgs repeatableFlag
	fs.Var(&buildArgs, "build-arg", "set a build-time variable KEY=VALUE (repeatable)")

	if err := fs.Parse(args); err != nil {
		return nil, &usageError{msg: err.Error()}
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return nil, &usageError{msg: "a build context directory is required"}
	}
	if len(rest) > 1 {
		return nil, &usageError{msg: fmt.Sprintf("unexpected positional argument(s) after context: %v", rest[1:])}
	}
	contextDir := rest[0]

	if strings.TrimSpace(*tag) == "" {
		return nil, &usageError{msg: "a tag is required (-t name:tag)"}
	}
	if strings.TrimSpace(*realm) == "" {
		return nil, &usageError{msg: "--realm must not be empty"}
	}

	parsedArgs, err := parseBuildArgs(buildArgs)
	if err != nil {
		return nil, err
	}

	dockerfile := *file
	if strings.TrimSpace(dockerfile) == "" {
		dockerfile = filepath.Join(contextDir, "Dockerfile")
	}

	return &buildConfig{
		contextDir:       contextDir,
		dockerfile:       dockerfile,
		tag:              strings.TrimSpace(*tag),
		realm:            strings.TrimSpace(*realm),
		buildArgs:        parsedArgs,
		containerdSocket: strings.TrimSpace(*containerdSocket),
		root:             strings.TrimSpace(*root),
		kukeondConfig:    strings.TrimSpace(*kukeondConfig),
	}, nil
}

// parseBuildArgs turns the raw --build-arg values into a KEY=VALUE map. Each
// entry must contain an `=`; a bare key is a usage error so a typo surfaces at
// parse time rather than as a silently-empty build arg.
func parseBuildArgs(raw []string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, &usageError{msg: fmt.Sprintf("invalid --build-arg %q: expected KEY=VALUE", kv)}
		}
		out[k] = v
	}
	return out, nil
}
