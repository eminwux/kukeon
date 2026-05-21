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
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/frontend"
	dockerfile "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/gateway/forwarder"
	"github.com/moby/buildkit/session"
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
// --platform=$BUILDPLATFORM at the library level (phase 1b, #616, validates
// those against kukeon's own Dockerfile).
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
// into the target realm's containerd namespace (<realm>.kukeon.io). progressW
// receives the human-readable build progress.
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
	if err := os.MkdirAll(cfg.root, buildStateDirMode); err != nil {
		return fmt.Errorf("create build state dir %q: %w", cfg.root, err)
	}

	namespace := consts.RealmNamespace(cfg.realm)

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

	solveOpt, err := newSolveOpt(cfg)
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
		return fmt.Errorf("build %q: %w", cfg.tag, waitErr)
	}
	return nil
}

// newSolveOpt builds the BuildKit SolveOpt for a Dockerfile build: the
// Dockerfile frontend, the context + dockerfile local mounts, the
// forwarded --build-arg values, and the containerd image exporter that names
// the result cfg.tag. The "image" exporter writes into the worker's
// containerd namespace, which newController points at <realm>.kukeon.io.
func newSolveOpt(cfg *buildConfig) (*client.SolveOpt, error) {
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

	return &client.SolveOpt{
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
	}, nil
}

// newController assembles BuildKit's in-process Controller backed by a
// containerd worker pointed at cfg.containerdSocket and scoped to the realm's
// containerd namespace. It mirrors buildkitd's own newController
// (cmd/buildkitd/main.go) trimmed to the phase-1 surface: the Dockerfile
// frontend only, no remote-cache import/export resolvers (those are phase 2a,
// #523), no tracing, no gateway frontend. The returned cleanup closes the
// history DB and the worker controller; callers must defer it.
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
		HistoryDB:        historyDB,
		CacheStore:       cacheStorage,
		LeaseManager:     w.LeaseManager(),
		ContentStore:     w.ContentStore(),
		GarbageCollect:   w.GarbageCollect,
		GracefulStop:     ctx.Done(),
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
// layers + manifest into <realm>.kukeon.io. Host network mode keeps phase 1
// simple — a trivial single-stage Dockerfile needs no build network, and the
// CNI / build-network quirks that surface on a real multi-stage build are
// deferred to phase 1b (#616).
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
