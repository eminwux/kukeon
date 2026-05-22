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

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	ctd "github.com/containerd/containerd/v2/client"
	ctddefaults "github.com/containerd/containerd/v2/defaults"
	"github.com/distribution/reference"
	"github.com/moby/buildkit/cache/remotecache"
	localremotecache "github.com/moby/buildkit/cache/remotecache/local"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/db/boltutil"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/util/progress/progressui"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/containerd"
	"github.com/tonistiigi/fsutil"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// grpcMaxMsgSize caps individual gRPC frames on the in-process control pipe.
// BuildKit transfers the build context and solve results over this connection;
// the default 4 MiB grpc cap is too small for real build payloads, so widen it
// on both the server and the client (client.New sets its own call-option
// defaults; this matches them on the server side).
const grpcMaxMsgSize = 1 << 30

// dockerfileFrontend is BuildKit's built-in Dockerfile frontend key. The
// frontend handles multi-stage, COPY --from=, --build-arg and
// --platform=$BUILDPLATFORM at the library level — phase 1b (#616) validated
// all four against kukeon's own multi-stage Dockerfile end-to-end, with no
// frontend-side wiring beyond the attrs newSolveOpt already sets.
const dockerfileFrontend = "dockerfile.v0"

// localNameContext / localNameDockerfile are the LocalMounts keys BuildKit's
// Dockerfile frontend reads the build context and the Dockerfile from,
// respectively (see buildkit frontend/dockerui defaults). Kept as named
// constants so the wire contract with the frontend is explicit.
const (
	localNameContext    = "context"
	localNameDockerfile = "dockerfile"
)

// bufconnSize is the in-memory listener buffer for the in-process control
// gRPC pipe. BuildKit streams solve status and transfers the build context
// over this connection; size it generously so a large context does not stall
// the pipe. The grpc max-message overrides below cap individual frames.
const bufconnSize = 1 << 20

// buildStateDirMode / buildDBMode are the permissions for BuildKit's state
// directory and its bbolt databases (cache.db, history.db). Root-owned and
// private, matching kukebuild's root-on-host posture and buildkitd's own
// 0600 on the history DB.
const (
	buildStateDirMode os.FileMode = 0o700
	buildDBMode       os.FileMode = 0o600
)

// containerdDialTimeout bounds the initial connection to the host containerd
// socket so a missing/unresponsive daemon fails fast with a clear error rather
// than hanging. Matches buildkitd's containerd worker default.
const containerdDialTimeout = 60 * time.Second

// runBuild wires BuildKit's in-process Controller (containerd worker backend +
// Dockerfile frontend), connects a BuildKit client to it over an in-memory
// gRPC pipe, and solves the Dockerfile, exporting the result as an OCI image
// into the target realm's containerd namespace (<realm>.<suffix>, where the
// suffix is resolved from the operator's kukeond.yaml — see
// resolveNamespaceSuffix). progressW receives the human-readable build
// progress.
func runBuild(ctx context.Context, cfg *buildConfig, progressW io.Writer) error {
	// kukebuild writes directly into containerd's content store; the
	// standalone containerd socket is root-only on a stock host. Fail fast
	// under non-root rather than letting containerd surface an opaque EACCES
	// several phases in. Same posture as `kuke image load --no-daemon`.
	if os.Geteuid() != 0 {
		return errors.New(
			"kukebuild writes to root-owned containerd state — re-run as root (e.g. via `sudo kuke build`)",
		)
	}

	if fi, err := os.Stat(cfg.contextDir); err != nil {
		return fmt.Errorf("build context %q: %w", cfg.contextDir, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("build context %q is not a directory", cfg.contextDir)
	}
	if _, err := os.Stat(cfg.dockerfile); err != nil {
		return fmt.Errorf("dockerfile %q: %w", cfg.dockerfile, err)
	}

	suffix, err := resolveNamespaceSuffix(cfg.kukeondConfig)
	if err != nil {
		return err
	}
	namespace := realmNamespace(cfg.realm, suffix)

	// Scope the default build state root per target namespace before creating
	// it: BuildKit's cache under --root mirrors a single containerd content
	// store, but that store is per-namespace, so a default root shared across
	// namespaces makes build 2 reuse build 1's cache entry and skip
	// re-materializing the layer into namespace 2's store — the unpack then
	// can't find the blob (issue #663).
	cfg.root = resolveBuildRoot(cfg.root, cfg.rootExplicit, namespace)
	if err := os.MkdirAll(cfg.root, buildStateDirMode); err != nil {
		return fmt.Errorf("create build state dir %q: %w", cfg.root, err)
	}

	ctrl, cleanup, err := newController(ctx, cfg, namespace)
	if err != nil {
		return err
	}
	defer cleanup()

	// Serve the controller over an in-memory pipe and connect a BuildKit
	// client to it. This is the "embed BuildKit, no long-running buildkitd"
	// contract from #522: the controller lives only for this process.
	listener := bufconn.Listen(bufconnSize)
	server := grpc.NewServer(
		grpc.MaxRecvMsgSize(grpcMaxMsgSize),
		grpc.MaxSendMsgSize(grpcMaxMsgSize),
	)
	ctrl.Register(server)
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	c, err := client.New(ctx, "",
		client.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
		client.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		return fmt.Errorf("connect to in-process buildkit controller: %w", err)
	}
	defer func() { _ = c.Close() }()

	// Resolve registry credentials before the solve when pushing, so a
	// misconfigured docker config fails fast and credsFound can tailor an
	// anonymous-rejection error after the push attempt. registryHost is safe to
	// recompute — parseArgs already validated the reference is fully qualified.
	var (
		authProvider session.Attachable
		pushHost     string
		credsFound   bool
	)
	if cfg.push {
		pushHost, err = registryHost(cfg.tag)
		if err != nil {
			return err
		}
		authProvider, credsFound, err = newAuthProvider(os.Getenv, pushHost)
		if err != nil {
			return err
		}
	}

	solveOpt, err := newSolveOpt(cfg, authProvider)
	if err != nil {
		return err
	}

	// Solve and render progress concurrently, mirroring buildkit's own
	// build-using-dockerfile example. UpdateFrom uses a fresh context so the
	// display finishes reporting a solve error even after ctx is cancelled.
	ch := make(chan *client.SolveStatus)
	eg, egctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		_, solveErr := c.Solve(egctx, nil, *solveOpt, ch)
		return solveErr
	})
	eg.Go(func() error {
		d, derr := progressui.NewDisplay(progressW, progressui.AutoMode)
		if derr != nil {
			return derr
		}
		_, derr = d.UpdateFrom(context.TODO(), ch)
		return derr
	})
	if waitErr := eg.Wait(); waitErr != nil {
		if cfg.push && isRegistryAuthError(waitErr) {
			return pushAuthError(cfg.tag, pushHost, credsFound, waitErr)
		}
		return fmt.Errorf("build %q: %w", cfg.tag, waitErr)
	}
	return nil
}

// resolveBuildRoot returns the directory BuildKit uses for its own state for a
// build into namespace. The default root is scoped per namespace
// (<root>/<namespace>) so consecutive builds targeting different containerd
// namespaces never share a single BuildKit cache: a shared cache records a
// layer as already materialized in the first namespace's content store, and
// the `unpack: "true"` export into the second namespace then fails to find the
// blob in that namespace's (separate) store — issue #663. An operator-supplied
// --root (explicit) is honored verbatim; keeping it unique per namespace is
// then the operator's responsibility.
func resolveBuildRoot(root string, explicit bool, namespace string) string {
	if explicit {
		return root
	}
	return filepath.Join(root, namespace)
}

// newSolveOpt builds the BuildKit SolveOpt for a Dockerfile build: the
// Dockerfile frontend, the context + dockerfile local mounts, the
// forwarded --build-arg values, and the containerd image exporter that names
// the result cfg.tag. The "image" exporter writes into the worker's
// containerd namespace, which newController points at <realm>.kukeon.io. When
// cfg.push is set the same exporter also pushes to the tag's registry (push is
// additive — the image still lands in the namespace) and authProvider, a
// BuildKit session attachable carrying the resolved registry credentials, is
// attached to the session; authProvider is nil when cfg.push is false.
func newSolveOpt(cfg *buildConfig, authProvider session.Attachable) (*client.SolveOpt, error) {
	contextFS, err := fsutil.NewFS(cfg.contextDir)
	if err != nil {
		return nil, fmt.Errorf("open build context %q: %w", cfg.contextDir, err)
	}
	dockerfileFS, err := fsutil.NewFS(filepath.Dir(cfg.dockerfile))
	if err != nil {
		return nil, fmt.Errorf("open dockerfile dir %q: %w", filepath.Dir(cfg.dockerfile), err)
	}

	frontendAttrs := map[string]string{
		"filename": filepath.Base(cfg.dockerfile),
	}
	for k, v := range cfg.buildArgs {
		frontendAttrs["build-arg:"+k] = v
	}

	name, err := normalizeImageName(cfg.tag)
	if err != nil {
		return nil, err
	}

	solveOpt := &client.SolveOpt{
		Frontend:      dockerfileFrontend,
		FrontendAttrs: frontendAttrs,
		LocalMounts: map[string]fsutil.FS{
			localNameContext:    contextFS,
			localNameDockerfile: dockerfileFS,
		},
		Exports: []client.ExportEntry{
			{
				Type: client.ExporterImage,
				Attrs: map[string]string{
					"name": name,
					// Unpack the built image's snapshot so `kuke create` can
					// reference the tag without a separate pull/unpack step —
					// the image lands ready-to-run in the realm namespace.
					"unpack": "true",
				},
			},
		},
	}

	// --push: the image exporter both writes the image into the containerd
	// namespace (the name/unpack attrs above) and pushes it to the tag's
	// registry. push is therefore additive, not substitutive — the local image
	// remains inspectable via `kuke image get`. The auth provider rides in on
	// the session so BuildKit resolves registry credentials during the push.
	if cfg.push {
		solveOpt.Exports[0].Attrs["push"] = "true"
		if authProvider != nil {
			solveOpt.Session = append(solveOpt.Session, authProvider)
		}
	}

	// Build secrets ride in on a session attachable: the Dockerfile frontend
	// requests a secret by id over the session, and the secretsprovider serves
	// it from the host file. A RUN step opts in with
	// --mount=type=secret,id=<id>, which BuildKit mounts at /run/secrets/<id>.
	// NewStore stats each src up front, so a missing/oversized secret file
	// fails fast here rather than mid-solve.
	if len(cfg.secrets) > 0 {
		sources := make([]secretsprovider.Source, 0, len(cfg.secrets))
		for _, s := range cfg.secrets {
			sources = append(sources, secretsprovider.Source{ID: s.id, FilePath: s.src})
		}
		store, err := secretsprovider.NewStore(sources)
		if err != nil {
			return nil, fmt.Errorf("prepare build secrets: %w", err)
		}
		solveOpt.Session = append(solveOpt.Session, secretsprovider.NewSecretProvider(store))
	}

	// Local cache export/import: the BuildKit client wires the dest/src dirs
	// into a content store and the session itself (see client.parseCacheOptions
	// for type=local), so kukebuild only has to forward the entries.
	for _, ce := range cfg.cacheExports {
		solveOpt.CacheExports = append(solveOpt.CacheExports, client.CacheOptionsEntry{Type: ce.typ, Attrs: ce.attrs})
	}
	for _, ci := range cfg.cacheImports {
		solveOpt.CacheImports = append(solveOpt.CacheImports, client.CacheOptionsEntry{Type: ci.typ, Attrs: ci.attrs})
	}

	return solveOpt, nil
}

// newController assembles BuildKit's in-process Controller backed by a
// containerd worker pointed at cfg.containerdSocket and scoped to the realm's
// containerd namespace. It mirrors buildkitd's own newController
// (cmd/buildkitd/main.go) trimmed to the phase-1/2a surface: the Dockerfile
// frontend only, no registry-backed cache resolvers (registry/s3/gha cache is
// deferred — phase 2a only wires local-disk cache, which the BuildKit client
// handles via content stores, no controller-side resolver), no tracing, no
// gateway frontend. The returned cleanup closes the history DB and the worker
// controller; callers must defer it.
func newController(ctx context.Context, cfg *buildConfig, namespace string) (*control.Controller, func(), error) {
	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, nil, fmt.Errorf("create session manager: %w", err)
	}

	wc, w, err := newWorkerController(ctx, cfg, namespace)
	if err != nil {
		return nil, nil, err
	}

	frontends := map[string]frontend.Frontend{
		dockerfileFrontend: forwarder.NewGatewayForwarder(wc.Infos(), dockerfile.Build),
	}

	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(cfg.root, "cache.db"))
	if err != nil {
		_ = wc.Close()
		return nil, nil, fmt.Errorf("open build cache store: %w", err)
	}
	historyDB, err := boltutil.Open(filepath.Join(cfg.root, "history.db"), buildDBMode, nil)
	if err != nil {
		_ = wc.Close()
		return nil, nil, fmt.Errorf("open build history db: %w", err)
	}

	ctrl, err := control.NewController(control.Opt{
		SessionManager:   sessionManager,
		WorkerController: wc,
		Frontends:        frontends,
		CacheManager:     solver.NewCacheManager(ctx, "local", cacheStorage, worker.NewCacheResultStorage(wc)),
		// Register the local-disk cache exporter/importer so --cache-to /
		// --cache-from type=local resolve (issue #523). Registry/s3/gha backends
		// are deferred — only "local" is wired, matching buildkitd's
		// remoteCacheExporterFuncs map trimmed to the phase-2a surface.
		ResolveCacheExporterFuncs: map[string]remotecache.ResolveCacheExporterFunc{
			"local": localremotecache.ResolveCacheExporterFunc(sessionManager),
		},
		ResolveCacheImporterFuncs: map[string]remotecache.ResolveCacheImporterFunc{
			"local": localremotecache.ResolveCacheImporterFunc(sessionManager),
		},
		HistoryDB:      historyDB,
		CacheStore:     cacheStorage,
		LeaseManager:   w.LeaseManager(),
		ContentStore:   w.ContentStore(),
		GarbageCollect: w.GarbageCollect,
		GracefulStop:   ctx.Done(),
	})
	if err != nil {
		_ = historyDB.Close()
		_ = wc.Close()
		return nil, nil, fmt.Errorf("create buildkit controller: %w", err)
	}

	cleanup := func() {
		_ = historyDB.Close()
		_ = wc.Close()
	}
	return ctrl, cleanup, nil
}

// newWorkerController builds a single-worker controller using BuildKit's
// containerd worker backend. The worker connects to cfg.containerdSocket and
// is scoped to the realm's containerd namespace, so the image exporter writes
// layers + manifest into <realm>.kukeon.io. Host network mode (the build
// containers share the host's network namespace) was confirmed sufficient in
// phase 1b (#616): kukeon's own Dockerfile reaches the network from a RUN step
// for the CNI-plugins curl-fetch + sha256-verify, and that succeeds over host
// mode with no CNI / build-network wiring — no bridge provider is needed.
// SnapshotterName is the containerd default (overlayfs on Linux); phase 1b
// confirmed it handles multi-stage cache reuse and COPY --from= against the
// real recipe without further plumbing.
func newWorkerController(
	ctx context.Context,
	cfg *buildConfig,
	namespace string,
) (*worker.Controller, *base.Worker, error) {
	workerOpts := containerd.WorkerOptions{
		Root:            cfg.root,
		Address:         cfg.containerdSocket,
		SnapshotterName: ctddefaults.DefaultSnapshotter,
		Namespace:       namespace,
		NetworkOpt:      netproviders.Opt{Mode: "host"},
	}
	opt, err := containerd.NewWorkerOpt(workerOpts, ctd.WithTimeout(containerdDialTimeout))
	if err != nil {
		return nil, nil, fmt.Errorf("init containerd worker (socket %q): %w", cfg.containerdSocket, err)
	}

	w, err := base.NewWorker(ctx, opt)
	if err != nil {
		return nil, nil, fmt.Errorf("create buildkit worker: %w", err)
	}

	wc := &worker.Controller{}
	if addErr := wc.Add(w); addErr != nil {
		_ = w.Close()
		return nil, nil, fmt.Errorf("register buildkit worker: %w", addErr)
	}
	return wc, w, nil
}

// normalizeImageName canonicalizes the user-supplied -t tag the same way
// `docker build` and `kuke create`/`kuke image *` do, so a short tag round-
// trips: `kukebuild -t app:dev` stores the image under
// `docker.io/library/app:dev` — the exact name `kuke create --image app:dev`
// resolves to. Without this, the image lands under the verbatim short name
// while kuke's reference normalization looks for the docker.io/library form,
// and `kuke create` falls through to a (failing) registry pull. An untagged
// reference gets `:latest`, matching docker's default.
func normalizeImageName(tag string) (string, error) {
	named, err := reference.ParseNormalizedNamed(tag)
	if err != nil {
		return "", fmt.Errorf("invalid image tag %q: %w", tag, err)
	}
	return reference.TagNameOnly(named).String(), nil
}
