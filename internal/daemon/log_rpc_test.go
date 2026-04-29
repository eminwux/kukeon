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
	"testing"

	"github.com/eminwux/kukeon/internal/daemon"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// logClientFake stubs only the kukeonv1.Client.LogContainer method so the
// service test exercises the RPC layer without a real controller. All other
// methods stay at the FakeClient-default ErrUnexpectedCall behavior so a
// stray call would fail loudly.
type logClientFake struct {
	kukeonv1.FakeClient

	result kukeonv1.LogContainerResult
	err    error
}

func (l *logClientFake) LogContainer(
	context.Context,
	v1beta1.ContainerDoc,
) (kukeonv1.LogContainerResult, error) {
	return l.result, l.err
}

func TestLogContainer_NotAttachable_RPCSurfacesSentinel(t *testing.T) {
	core := &logClientFake{err: errdefs.ErrAttachNotSupported}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, nil)

	args := &kukeonv1.LogContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.LogContainerReply{}
	if err := svc.LogContainer(args, reply); err != nil {
		t.Fatalf("LogContainer returned transport error: %v", err)
	}
	if reply.Err == nil {
		t.Fatalf("expected wire-level Err to be populated, got nil")
	}
	wireErr := kukeonv1.FromAPIError(reply.Err)
	if !errors.Is(wireErr, errdefs.ErrAttachNotSupported) {
		t.Errorf("wire error %q does not unwrap to ErrAttachNotSupported", wireErr)
	}
}

func TestLogContainer_Attachable_RPCReturnsCapturePath(t *testing.T) {
	const wantPath = "/opt/kukeon/default/default/default/cellA/work/tty/capture"
	core := &logClientFake{result: kukeonv1.LogContainerResult{HostCapturePath: wantPath}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core, nil)

	args := &kukeonv1.LogContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.LogContainerReply{}
	if err := svc.LogContainer(args, reply); err != nil {
		t.Fatalf("LogContainer returned transport error: %v", err)
	}
	if reply.Err != nil {
		t.Fatalf("expected wire-level Err to be nil, got %v", kukeonv1.FromAPIError(reply.Err))
	}
	if reply.Result.HostCapturePath != wantPath {
		t.Errorf("HostCapturePath = %q, want %q", reply.Result.HostCapturePath, wantPath)
	}
}
