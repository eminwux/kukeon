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

package daemon_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// attachClientFake stubs the kukeonv1.Client.AttachContainer method so the
// service test can drive the RPC layer without a real controller. All other
// methods remain at their FakeClient-default ErrUnexpectedCall behavior so a
// stray call would fail loudly.
type attachClientFake struct {
	kukeonv1.FakeClient
	result kukeonv1.AttachContainerResult
	err    error
}

func (a *attachClientFake) AttachContainer(
	context.Context,
	v1beta1.ContainerDoc,
) (kukeonv1.AttachContainerResult, error) {
	return a.result, a.err
}

func TestAttachContainer_NotAttachable_RPCSurfacesSentinel(t *testing.T) {
	core := &attachClientFake{err: errdefs.ErrAttachNotSupported}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.AttachContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.AttachContainerReply{}
	if err := svc.AttachContainer(args, reply); err != nil {
		t.Fatalf("AttachContainer returned transport error: %v", err)
	}
	if reply.Err == nil {
		t.Fatalf("expected wire-level Err to be populated, got nil")
	}
	wireErr := kukeonv1.FromAPIError(reply.Err)
	if !errors.Is(wireErr, errdefs.ErrAttachNotSupported) {
		t.Errorf("wire error %q does not unwrap to ErrAttachNotSupported", wireErr)
	}
}

func TestAttachContainer_Attachable_RPCReturnsSocketPath(t *testing.T) {
	// When the target is Attachable=true the daemon hands back the host
	// socket path so the client can open it directly. The RPC layer must
	// pass that path through verbatim and leave Err nil.
	const wantSocket = "/opt/kukeon/default/default/default/cellA/work/tty/socket"
	core := &attachClientFake{result: kukeonv1.AttachContainerResult{HostSocketPath: wantSocket}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.AttachContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.AttachContainerReply{}
	if err := svc.AttachContainer(args, reply); err != nil {
		t.Fatalf("AttachContainer returned transport error: %v", err)
	}
	if reply.Err != nil {
		t.Fatalf("expected wire-level Err to be nil, got %v", kukeonv1.FromAPIError(reply.Err))
	}
	if reply.Result.HostSocketPath != wantSocket {
		t.Errorf("HostSocketPath = %q, want %q", reply.Result.HostSocketPath, wantSocket)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
