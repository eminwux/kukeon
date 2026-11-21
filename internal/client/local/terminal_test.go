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

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	kfs "github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// gateRunner stubs the three runner calls the attach gates make. Every other
// runner method is left to the nil embedded interface, so a stray call
// panics instead of passing silently.
type gateRunner struct {
	runner.Runner

	attachable bool
	state      intmodel.ContainerState
	reapplied  int
}

func (g *gateRunner) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	cell.Spec.Containers = []intmodel.ContainerSpec{{
		ID:         "work",
		RealmName:  cell.Spec.RealmName,
		SpaceName:  cell.Spec.SpaceName,
		StackName:  cell.Spec.StackName,
		CellName:   cell.Metadata.Name,
		Attachable: g.attachable,
	}}
	return cell, nil
}

func (g *gateRunner) GetContainerState(intmodel.Cell, string) (intmodel.ContainerState, error) {
	return g.state, nil
}

func (g *gateRunner) ReapplyAttachableSocketPerms(intmodel.ContainerSpec) { g.reapplied++ }

func newGateClient(t *testing.T, r *gateRunner) (*Client, string) {
	t.Helper()
	// A short run path keeps the socket under sun_path's 108-byte limit,
	// which t.TempDir()'s per-test path can overflow.
	runPath, err := os.MkdirTemp("", "kt") //nolint:usetesting // sun_path limit
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runPath) })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	exec := controller.NewControllerExecForTesting(context.Background(), logger,
		controller.Options{RunPath: runPath}, r)
	return NewFromExec(exec), kfs.ContainerSocketSymlinkPath(runPath, "r", "s", "st", "c", "work")
}

func workDoc() v1beta1.ContainerDoc {
	return v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata:   v1beta1.ContainerMetadata{Name: "work"},
		Spec: v1beta1.ContainerSpec{
			ID: "work", RealmID: "r", SpaceID: "s", StackID: "st", CellID: "c",
		},
	}
}

// TestConnectContainerTerminal_RefusesLikeAttachContainer pins that the new
// RPC applies AttachContainer's gates with the same errors.
func TestConnectContainerTerminal_RefusesLikeAttachContainer(t *testing.T) {
	cases := []struct {
		name   string
		runner *gateRunner
		want   error
	}{
		{
			name:   "not attachable",
			runner: &gateRunner{attachable: false, state: intmodel.ContainerStateReady},
			want:   errdefs.ErrAttachNotSupported,
		},
		{
			name:   "not running",
			runner: &gateRunner{attachable: true, state: intmodel.ContainerStateStopped},
			want:   errdefs.ErrAttachTaskNotRunning,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newGateClient(t, tc.runner)

			_, attachErr := c.AttachContainer(context.Background(), workDoc())
			conn, connectErr := c.ConnectContainerTerminal(context.Background(), workDoc())
			if conn != nil {
				_ = conn.Close()
				t.Fatalf("ConnectContainerTerminal returned a connection, want refusal")
			}
			if !errors.Is(attachErr, tc.want) || !errors.Is(connectErr, tc.want) {
				t.Fatalf("AttachContainer err=%v, ConnectContainerTerminal err=%v; want both %v",
					attachErr, connectErr, tc.want)
			}
			if attachErr.Error() != connectErr.Error() {
				t.Errorf("messages differ: AttachContainer %q vs ConnectContainerTerminal %q",
					attachErr, connectErr)
			}
		})
	}
}

// TestConnectContainerTerminal_DialsSocket pins the happy path: gates pass,
// the wrong-mode heal runs, and the connection reaches the listener.
func TestConnectContainerTerminal_DialsSocket(t *testing.T) {
	r := &gateRunner{attachable: true, state: intmodel.ContainerStateReady}
	c, socketPath := newGateClient(t, r)
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr == nil {
			accepted <- conn
		}
	}()

	conn, err := c.ConnectContainerTerminal(context.Background(), workDoc())
	if err != nil {
		t.Fatalf("ConnectContainerTerminal: %v", err)
	}
	defer func() { _ = conn.Close() }()
	peer := <-accepted
	defer func() { _ = peer.Close() }()

	if _, err = conn.Write([]byte("x")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 1)
	if _, err = io.ReadFull(peer, buf); err != nil || buf[0] != 'x' {
		t.Fatalf("peer read %q, %v; want x", buf, err)
	}
	if r.reapplied != 1 {
		t.Errorf("ReapplyAttachableSocketPerms calls = %d, want 1", r.reapplied)
	}
}

// TestConnectContainerTerminal_AbsentSocket_StaleClass pins that a dial to a
// socket kuketty has not bound yet comes back as ErrAttachStaleSocket.
func TestConnectContainerTerminal_AbsentSocket_StaleClass(t *testing.T) {
	c, _ := newGateClient(t, &gateRunner{attachable: true, state: intmodel.ContainerStateReady})
	_, err := c.ConnectContainerTerminal(context.Background(), workDoc())
	if !errors.Is(err, errdefs.ErrAttachStaleSocket) {
		t.Fatalf("err = %v, want ErrAttachStaleSocket", err)
	}
}

// TestClassifyTerminalDialError covers each dial class and pins that the
// errno stays out of the chain, so the wire Kind is the sentinel's.
func TestClassifyTerminalDialError(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present.sock")
	if err := os.WriteFile(present, nil, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	absent := filepath.Join(dir, "absent.sock")
	dialErr := func(path string, errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "unix", Err: fmt.Errorf("connect %s: %w", path, errno)}
	}

	cases := []struct {
		name     string
		path     string
		errno    syscall.Errno
		want     error
		wantKind string
	}{
		{"ENOENT", absent, syscall.ENOENT, errdefs.ErrAttachStaleSocket, "AttachStaleSocket"},
		{"EACCES absent", absent, syscall.EACCES, errdefs.ErrAttachStaleSocket, "AttachStaleSocket"},
		{"EACCES present", present, syscall.EACCES, errdefs.ErrAttachPermissionDenied, "AttachPermissionDenied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := classifyTerminalDialError(tc.path, dialErr(tc.path, tc.errno))
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if errors.Is(err, tc.errno) || errors.Is(err, fs.ErrPermission) {
				t.Errorf("err = %v keeps the errno in its chain; the wire Kind would be ambiguous", err)
			}
			if kind := kukeonv1.KindFromError(err); kind != tc.wantKind {
				t.Errorf("KindFromError = %q, want %q", kind, tc.wantKind)
			}
		})
	}

	refused := classifyTerminalDialError(present, dialErr(present, syscall.ECONNREFUSED))
	if errors.Is(refused, errdefs.ErrAttachStaleSocket) || errors.Is(refused, errdefs.ErrAttachPermissionDenied) {
		t.Errorf("ECONNREFUSED classified as a boot-race class: %v", refused)
	}
	if !errors.Is(refused, syscall.ECONNREFUSED) {
		t.Errorf("ECONNREFUSED err = %v, want the errno kept", refused)
	}
}
