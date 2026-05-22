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
// Dockerfile. Phase 1b (#616) validated multi-stage, COPY --from=, --build-arg
// and --platform=$BUILDPLATFORM against kukeon's own Dockerfile end-to-end
// (host network mode + the default overlayfs snapshotter sufficed). Phase 2a
// (#523) adds build-time file secrets (--secret) and local-disk cache
// export/import (--cache-to / --cache-from). The --push / --platform-flag
// surface remains deferred to the phase 2 follow-ups (#524, #646).
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
	// store. When rootExplicit is false this is the base default and gets
	// scoped per target namespace (see resolveBuildRoot); an operator-supplied
	// --root is honored verbatim.
	root string
	// rootExplicit records whether the operator passed --root. The default
	// build root is scoped per containerd namespace to keep each namespace's
	// BuildKit cache isolated (issue #663); an explicit --root opts out of that
	// scoping and is the operator's responsibility to keep unique per namespace.
	rootExplicit bool
	// kukeondConfig is the path to the kukeond.yaml from which kukebuild
	// resolves the realm->namespace suffix (--kukeond-config). Empty falls
	// back to /etc/kukeon/kukeond.yaml and then the hardcoded default suffix;
	// see resolveNamespaceSuffix.
	kukeondConfig string
	// secrets are the --secret entries: build-time file secrets exposed to the
	// Dockerfile at /run/secrets/<id> via RUN --mount=type=secret,id=<id>.
	// File-based only in phase 2a (#523); env-var-source secrets are deferred.
	secrets []secretSpec
	// cacheExports / cacheImports are the --cache-to / --cache-from entries,
	// forwarded to BuildKit's CacheExports / CacheImports. Scoped to
	// type=local (local-disk export/import) in phase 2a (#523); registry/s3/gha
	// backends are deferred.
	cacheExports []cacheSpec
	cacheImports []cacheSpec
}

// secretSpec is a resolved --secret entry: a build secret named id, sourced
// from the host file at src. BuildKit exposes it inside a RUN step that opts in
// with --mount=type=secret,id=<id> at the BuildKit-standard /run/secrets/<id>.
type secretSpec struct {
	id  string
	src string
}

// cacheSpec is a resolved --cache-to / --cache-from entry: a BuildKit cache
// backend type plus its attributes (e.g. type=local with dest/src). Forwarded
// verbatim to BuildKit's CacheOptionsEntry.
type cacheSpec struct {
	typ   string
	attrs map[string]string
}

// defaultContainerdSocket is the standalone host containerd socket the
// containerd worker connects to. Matches the socket the project smoke test
// (CLAUDE.md "Local smoke test") and `kuke image load` operate against — not
// the docker-private containerd.
const defaultContainerdSocket = "/run/containerd/containerd.sock"

// defaultBuildRoot is the base under which BuildKit keeps its own state (build
// cache, history, snapshot metadata) — separate from containerd's content
// store, per the issue notes. Under /var/lib so it survives across builds for
// cache reuse; root-owned, matching kukebuild's root-on-host posture. The
// effective default root is scoped per target namespace beneath this base
// (<defaultBuildRoot>/<namespace>, see resolveBuildRoot) so each namespace's
// cache stays isolated (issue #663).
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
	root := fs.String("root", defaultBuildRoot, "directory for BuildKit's own state (cache, history, snapshots); the default is scoped per target namespace, an explicit value is used verbatim")
	kukeondConfig := fs.String(
		"kukeond-config",
		"",
		"path to kukeond.yaml; resolves the realm->namespace suffix (default /etc/kukeon/kukeond.yaml)",
	)

	var buildArgs repeatableFlag
	fs.Var(&buildArgs, "build-arg", "set a build-time variable KEY=VALUE (repeatable)")

	var secrets repeatableFlag
	fs.Var(&secrets, "secret", "expose a file secret to the build at /run/secrets/NAME: id=NAME,src=PATH (repeatable)")
	var cacheTo repeatableFlag
	fs.Var(&cacheTo, "cache-to", "export build cache: type=local,dest=PATH (repeatable)")
	var cacheFrom repeatableFlag
	fs.Var(&cacheFrom, "cache-from", "import build cache: type=local,src=PATH (repeatable)")

	if err := fs.Parse(args); err != nil {
		return nil, &usageError{msg: err.Error()}
	}

	rootExplicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "root" {
			rootExplicit = true
		}
	})

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

	parsedSecrets, err := parseSecrets(secrets)
	if err != nil {
		return nil, err
	}
	parsedCacheExports, err := parseCacheOpts(cacheTo, "cache-to")
	if err != nil {
		return nil, err
	}
	parsedCacheImports, err := parseCacheOpts(cacheFrom, "cache-from")
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
		rootExplicit:     rootExplicit,
		kukeondConfig:    strings.TrimSpace(*kukeondConfig),
		secrets:          parsedSecrets,
		cacheExports:     parsedCacheExports,
		cacheImports:     parsedCacheImports,
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

// parseCSVPairs splits a comma-separated KEY=VALUE list (e.g.
// "id=NAME,src=PATH") into a map. Empty segments are skipped; a segment without
// an `=` or with an empty key is a usage error attributed to --<flag>. The
// caller validates the recognized keys.
func parseCSVPairs(flag, raw string) (map[string]string, error) {
	out := map[string]string{}
	for _, seg := range strings.Split(raw, ",") {
		if strings.TrimSpace(seg) == "" {
			continue
		}
		k, v, ok := strings.Cut(seg, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, &usageError{msg: fmt.Sprintf("invalid --%s %q: expected comma-separated KEY=VALUE pairs", flag, raw)}
		}
		out[k] = v
	}
	return out, nil
}

// parseSecrets turns the raw --secret values into resolved secretSpecs. The
// supported form is `id=NAME,src=PATH`: a host file exposed to the build at
// /run/secrets/NAME. Both keys are required, env-var-source secrets (`env=…`)
// are rejected as deferred (issue #523 out-of-scope), and an unknown key is a
// usage error so a typo surfaces at parse time.
func parseSecrets(raw []string) ([]secretSpec, error) {
	out := make([]secretSpec, 0, len(raw))
	for _, s := range raw {
		kv, err := parseCSVPairs("secret", s)
		if err != nil {
			return nil, err
		}
		if env, ok := kv["env"]; ok {
			return nil, &usageError{msg: fmt.Sprintf(
				"invalid --secret %q: env-var-source secrets (env=%s) are not supported yet; use src=PATH", s, env)}
		}
		for k := range kv {
			if k != "id" && k != "src" {
				return nil, &usageError{msg: fmt.Sprintf("invalid --secret %q: unknown key %q (expected id, src)", s, k)}
			}
		}
		id := strings.TrimSpace(kv["id"])
		src := strings.TrimSpace(kv["src"])
		if id == "" {
			return nil, &usageError{msg: fmt.Sprintf("invalid --secret %q: id is required (id=NAME,src=PATH)", s)}
		}
		if src == "" {
			return nil, &usageError{msg: fmt.Sprintf("invalid --secret %q: src is required — file-based secrets only (id=NAME,src=PATH)", s)}
		}
		out = append(out, secretSpec{id: id, src: src})
	}
	return out, nil
}

// parseCacheOpts turns the raw --cache-to / --cache-from values into resolved
// cacheSpecs. Only `type=local` is supported in phase 2a (#523): cache-to
// requires `dest=PATH`, cache-from requires `src=PATH`. A missing or non-local
// type, or a missing path, is a usage error; registry/s3/gha backends are
// rejected as deferred. All non-type keys are forwarded verbatim to BuildKit so
// optional local-cache attrs (e.g. mode, compression) pass through.
func parseCacheOpts(raw []string, flag string) ([]cacheSpec, error) {
	requiredAttr := "dest"
	if flag == "cache-from" {
		requiredAttr = "src"
	}
	out := make([]cacheSpec, 0, len(raw))
	for _, s := range raw {
		kv, err := parseCSVPairs(flag, s)
		if err != nil {
			return nil, err
		}
		typ := strings.TrimSpace(kv["type"])
		if typ == "" {
			return nil, &usageError{msg: fmt.Sprintf(
				"invalid --%s %q: type is required (only type=local is supported)", flag, s)}
		}
		if typ != "local" {
			return nil, &usageError{msg: fmt.Sprintf(
				"invalid --%s %q: type=%s is not supported yet; only type=local (local-disk cache) is available", flag, s, typ)}
		}
		if strings.TrimSpace(kv[requiredAttr]) == "" {
			return nil, &usageError{msg: fmt.Sprintf(
				"invalid --%s %q: type=local requires %s=PATH", flag, s, requiredAttr)}
		}
		attrs := make(map[string]string, len(kv))
		for k, v := range kv {
			if k == "type" {
				continue
			}
			attrs[k] = v
		}
		out = append(out, cacheSpec{typ: typ, attrs: attrs})
	}
	return out, nil
}
