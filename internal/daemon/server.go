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

// Package daemon implements the kukeond process: it owns a controller.Exec,
// exposes the kukeonv1 API over a unix socket using net/rpc + jsonrpc, and
// manages its own lifecycle (listener, PID file, graceful shutdown).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/client/local"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Options configures a Server.
type Options struct {
	// SocketPath is the unix socket path the daemon listens on.
	SocketPath string
	// SocketMode is the file mode applied to the socket after creation.
	SocketMode os.FileMode
	// SocketGID, when non-zero, is the group the listener socket is chowned
	// to (uid stays root) so non-root members of that group can dial the
	// daemon. Set by `kuke init` to the kukeon GID. Each Serve re-applies
	// this so a daemon restart does not lose group access.
	SocketGID int
	// PIDFile, when non-empty, is written on Serve and removed on Stop.
	PIDFile string
	// ReconcileInterval is the period of the background cell-reconciliation
	// loop. Zero or negative disables the loop — useful for tests and for
	// operators who explicitly opt out via `--reconcile-interval 0`.
	ReconcileInterval time.Duration
	// Controller is forwarded to controller.NewControllerExec.
	Controller controller.Options
}

// Server hosts the kukeonv1 API over JSON-RPC on a unix socket.
type Server struct {
	ctx    context.Context
	logger *slog.Logger
	opts   Options

	core *local.Client
	rpc  *rpc.Server

	// reconcileFn is the per-tick callable for the cell-reconciliation loop.
	// Defaults to core.ReconcileCells; tests in the daemon package overwrite
	// it before Serve so they exercise the ticker without a real controller.
	reconcileFn func() (controller.ReconcileResult, error)

	mu       sync.Mutex
	listener net.Listener
	closed   bool
}

// NewServer constructs a Server. It does not open the listener; call Serve.
func NewServer(ctx context.Context, logger *slog.Logger, opts Options) *Server {
	if opts.SocketMode == 0 {
		opts.SocketMode = 0o600
	}
	core := local.New(ctx, logger, opts.Controller)
	srv := &Server{
		ctx:    ctx,
		logger: logger,
		opts:   opts,
		core:   core,
	}
	srv.reconcileFn = srv.core.ReconcileCells
	return srv
}

// Serve opens the listener and accepts connections until Stop is called or
// the context is cancelled. Returns nil on graceful shutdown.
func (s *Server) Serve() error {
	if err := s.prepareSocketDir(); err != nil {
		return err
	}
	if err := s.removeStaleSocket(); err != nil {
		return err
	}

	listener, err := net.Listen("unix", s.opts.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.opts.SocketPath, err)
	}
	if s.opts.SocketGID > 0 {
		if chownErr := os.Chown(s.opts.SocketPath, 0, s.opts.SocketGID); chownErr != nil {
			s.logger.WarnContext(s.ctx,
				"chown socket to kukeon group failed; non-root operators will need sudo",
				"socket", s.opts.SocketPath,
				"gid", s.opts.SocketGID,
				"error", chownErr,
			)
		}
	}
	if err := os.Chmod(s.opts.SocketPath, s.opts.SocketMode); err != nil {
		_ = listener.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	if err := s.writePIDFile(); err != nil {
		_ = listener.Close()
		return err
	}

	s.rpc = rpc.NewServer()
	svc := NewKukeonV1Service(s.ctx, s.logger, s.core, s.autoDeleteLauncher())
	if err := s.rpc.RegisterName(kukeonv1.ServiceName, svc); err != nil {
		_ = listener.Close()
		return fmt.Errorf("register rpc service: %w", err)
	}

	s.logger.InfoContext(s.ctx, "kukeond listening", "socket", s.opts.SocketPath)
	s.startReconcileLoop()
	s.acceptLoop(listener)
	return nil
}

// startReconcileLoop spawns the background cell-reconciliation ticker when
// ReconcileInterval > 0. Lifetime is bound to s.ctx — daemon shutdown stops
// the loop. Errors during a pass are logged and the loop continues. One log
// line per pass: brief on success, detailed on failure (per #161 AC).
func (s *Server) startReconcileLoop() {
	if s.opts.ReconcileInterval <= 0 {
		s.logger.InfoContext(s.ctx, "reconcile loop disabled",
			"interval", s.opts.ReconcileInterval)
		return
	}
	s.logger.InfoContext(s.ctx, "starting reconcile loop",
		"interval", s.opts.ReconcileInterval)
	go s.runReconcileLoop(s.opts.ReconcileInterval)
}

func (s *Server) runReconcileLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			s.logger.InfoContext(s.ctx, "reconcile loop stopped")
			return
		case <-ticker.C:
			s.runReconcileOnce()
		}
	}
}

func (s *Server) runReconcileOnce() {
	defer func() {
		if r := recover(); r != nil {
			s.logger.ErrorContext(s.ctx, "reconcile pass panicked; loop continues",
				"panic", r)
		}
	}()
	res, err := s.reconcileFn()
	switch {
	case err != nil:
		s.logger.ErrorContext(s.ctx, "reconcile pass failed",
			"error", err,
			"cells_scanned", res.CellsScanned,
			"cells_updated", res.CellsUpdated,
			"cells_errored", res.CellsErrored,
			"errors", res.Errors)
	case len(res.Errors) > 0:
		s.logger.WarnContext(s.ctx, "reconcile pass completed with errors",
			"cells_scanned", res.CellsScanned,
			"cells_updated", res.CellsUpdated,
			"cells_errored", res.CellsErrored,
			"errors", res.Errors)
	default:
		s.logger.InfoContext(s.ctx, "reconcile ok",
			"cells_scanned", res.CellsScanned,
			"cells_updated", res.CellsUpdated)
	}
}

func (s *Server) acceptLoop(listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return
			}
			if s.ctx.Err() != nil {
				return
			}
			s.logger.WarnContext(s.ctx, "accept error", "error", err)
			continue
		}
		go s.rpc.ServeCodec(jsonrpc.NewServerCodec(conn))
	}
}

// Stop closes the listener, releases the controller, and removes the socket
// and PID file. Safe to call multiple times.
func (s *Server) Stop() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	listener := s.listener
	s.listener = nil
	s.mu.Unlock()

	var firstErr error
	if listener != nil {
		if err := listener.Close(); err != nil {
			firstErr = err
		}
	}
	if s.core != nil {
		if err := s.core.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.opts.SocketPath != "" {
		_ = os.Remove(s.opts.SocketPath)
	}
	if s.opts.PIDFile != "" {
		_ = os.Remove(s.opts.PIDFile)
	}
	return firstErr
}

func (s *Server) prepareSocketDir() error {
	dir := filepath.Dir(s.opts.SocketPath)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

func (s *Server) removeStaleSocket() error {
	err := os.Remove(s.opts.SocketPath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove stale socket: %w", err)
}

// autoDeleteLauncher returns the AutoDeleteLauncher that the RPC service
// hands a CellDoc to when it sees Spec.AutoDelete=true on a successful
// CreateCell. The launcher delegates to *local.Client.WatchCellAutoDelete,
// which spawns the wait-and-cleanup goroutine bound to the daemon context.
func (s *Server) autoDeleteLauncher() AutoDeleteLauncher {
	return func(_ context.Context, doc v1beta1.CellDoc) error {
		// Always pass the daemon's context (s.ctx) — not the per-RPC ctx,
		// which is cancelled when the CreateCell call returns. The watcher
		// must outlive the RPC.
		return s.core.WatchCellAutoDelete(s.ctx, s.logger, doc)
	}
}

func (s *Server) writePIDFile() error {
	if s.opts.PIDFile == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.opts.PIDFile), 0o755); err != nil {
		return fmt.Errorf("pid file dir: %w", err)
	}
	content := fmt.Sprintf("%d\n", os.Getpid())
	if err := os.WriteFile(s.opts.PIDFile, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	return nil
}
