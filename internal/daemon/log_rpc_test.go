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
	"fmt"
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

// TestLogContainer_ContainerNotFound_RPCSurfacesSentinel pins down the
// daemon RPC layer's pass-through of structured errors from the in-process
// client. ErrContainerNotFound is the realistic sentinel callers can hit
// since issue #203 dropped the Attachable gate inside LogContainer.
func TestLogContainer_ContainerNotFound_RPCSurfacesSentinel(t *testing.T) {
	core := &logClientFake{err: errdefs.ErrContainerNotFound}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.LogContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.LogContainerReply{}
	if err := svc.LogContainer(args, reply); err != nil {
		t.Fatalf("LogContainer returned transport error: %v", err)
	}
	if reply.Err == nil {
		t.Fatalf("expected wire-level Err to be populated, got nil")
	}
	wireErr := kukeonv1.FromAPIError(reply.Err)
	if !errors.Is(wireErr, errdefs.ErrContainerNotFound) {
		t.Errorf("wire error %q does not unwrap to ErrContainerNotFound", wireErr)
	}
}

func TestLogContainer_Attachable_RPCReturnsCapturePath(t *testing.T) {
	const wantPath = "/opt/kukeon/default/default/default/cellA/work/tty/terminals/main/capture.raw"
	core := &logClientFake{result: kukeonv1.LogContainerResult{HostCapturePath: wantPath}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

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

// TestLogContainer_NonAttachable_RPCReturnsLogPath pins the wire-level
// passthrough of HostLogPath added in issue #203 — the field for the
// containerd-shim cio.LogFile branch.
func TestLogContainer_NonAttachable_RPCReturnsLogPath(t *testing.T) {
	const wantPath = "/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/container.log"
	core := &logClientFake{result: kukeonv1.LogContainerResult{HostLogPath: wantPath}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.LogContainerArgs{Doc: v1beta1.ContainerDoc{}}
	reply := &kukeonv1.LogContainerReply{}
	if err := svc.LogContainer(args, reply); err != nil {
		t.Fatalf("LogContainer returned transport error: %v", err)
	}
	if reply.Err != nil {
		t.Fatalf("expected wire-level Err to be nil, got %v", kukeonv1.FromAPIError(reply.Err))
	}
	if reply.Result.HostLogPath != wantPath {
		t.Errorf("HostLogPath = %q, want %q", reply.Result.HostLogPath, wantPath)
	}
	if reply.Result.HostCapturePath != "" {
		t.Errorf(
			"HostCapturePath = %q, want empty (non-Attachable should not populate it)",
			reply.Result.HostCapturePath,
		)
	}
}

type daemonLogsClientFake struct {
	kukeonv1.FakeClient

	req    kukeonv1.DaemonLogRequest
	result kukeonv1.DaemonLogResult
	err    error
}

func (d *daemonLogsClientFake) ReadDaemonLogs(
	_ context.Context, req kukeonv1.DaemonLogRequest,
) (kukeonv1.DaemonLogResult, error) {
	d.req = req
	return d.result, d.err
}

func TestReadDaemonLogs_RPCForwardsRequestAndResult(t *testing.T) {
	core := &daemonLogsClientFake{result: kukeonv1.DaemonLogResult{Data: "ready\n", Start: 4, Next: 10, Size: 10}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	args := &kukeonv1.ReadDaemonLogsArgs{Request: kukeonv1.DaemonLogRequest{Offset: 4, MaxBytes: 128}}
	reply := &kukeonv1.ReadDaemonLogsReply{}
	if err := svc.ReadDaemonLogs(args, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if reply.Err != nil || reply.Result != core.result || core.req != args.Request {
		t.Fatalf("reply=%+v req=%+v", reply, core.req)
	}
}

func TestReadDaemonLogs_RPCSurfacesUnavailableSentinel(t *testing.T) {
	core := &daemonLogsClientFake{err: errdefs.ErrDaemonLogUnavailable}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	reply := &kukeonv1.ReadDaemonLogsReply{}
	if err := svc.ReadDaemonLogs(&kukeonv1.ReadDaemonLogsArgs{}, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !errors.Is(kukeonv1.FromAPIError(reply.Err), errdefs.ErrDaemonLogUnavailable) {
		t.Fatalf("wire error %+v does not unwrap to ErrDaemonLogUnavailable", reply.Err)
	}
}

type containerLogClientFake struct {
	kukeonv1.FakeClient

	doc    v1beta1.ContainerDoc
	req    kukeonv1.ContainerLogRequest
	result kukeonv1.ContainerLogResult
	err    error
}

func (c *containerLogClientFake) ReadContainerLog(
	_ context.Context, doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	c.doc = doc
	c.req = req
	return c.result, c.err
}

func TestReadContainerLog_RPCForwardsDocRequestAndResult(t *testing.T) {
	core := &containerLogClientFake{result: kukeonv1.ContainerLogResult{
		Source: "/opt/kukeon/r/s/st/c/work/tty/terminals/main/capture.raw",
		Data:   []byte("$ \xff"), Start: 4, Next: 7, Size: 7,
	}}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

	doc := v1beta1.ContainerDoc{Metadata: v1beta1.ContainerMetadata{Name: "work"}}
	args := &kukeonv1.ReadContainerLogArgs{Doc: doc, Request: kukeonv1.ContainerLogRequest{Offset: 4, MaxBytes: 128}}
	reply := &kukeonv1.ReadContainerLogReply{}
	if err := svc.ReadContainerLog(args, reply); err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if reply.Err != nil {
		t.Fatalf("wire error: %v", kukeonv1.FromAPIError(reply.Err))
	}
	if core.doc.Metadata.Name != "work" || core.req != args.Request {
		t.Fatalf("core got doc=%+v req=%+v", core.doc, core.req)
	}
	if string(reply.Result.Data) != "$ \xff" || reply.Result.Next != 7 || reply.Result.Source != core.result.Source {
		t.Fatalf("reply %+v", reply.Result)
	}
}

func TestReadContainerLog_RPCSurfacesSentinels(t *testing.T) {
	for _, sentinel := range []error{
		errdefs.ErrContainerNotFound, errdefs.ErrCellNotFound, errdefs.ErrContainerLogUnavailable,
	} {
		core := &containerLogClientFake{
			result: kukeonv1.ContainerLogResult{Source: "/missing/capture"},
			err:    sentinel,
		}
		svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)

		reply := &kukeonv1.ReadContainerLogReply{}
		if err := svc.ReadContainerLog(&kukeonv1.ReadContainerLogArgs{}, reply); err != nil {
			t.Fatalf("transport error: %v", err)
		}
		if !errors.Is(kukeonv1.FromAPIError(reply.Err), sentinel) {
			t.Fatalf("wire error %+v does not unwrap to %v", reply.Err, sentinel)
		}
		if reply.Result.Source != "/missing/capture" {
			t.Fatalf("Source %q not carried alongside the error", reply.Result.Source)
		}
	}
}

type containerLogStreamFake struct {
	kukeonv1.FakeClient

	req kukeonv1.ContainerLogRequest
}

func (c *containerLogStreamFake) ReadContainerLog(
	_ context.Context, _ v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
) (kukeonv1.ContainerLogResult, error) {
	c.req = req
	return kukeonv1.ContainerLogResult{Data: []byte("x"), Next: 1, Size: 1, Stream: req.Stream},
		fmt.Errorf("%w: outside /run/kukeon/tty", errdefs.ErrSupervisorLogUnreadable)
}

// TestReadContainerLog_RPCForwardsStream pins that the supervisor stream
// request reaches the core and that both its echo and the typed error come
// back through the service (issue #1721).
func TestReadContainerLog_RPCForwardsStream(t *testing.T) {
	core := &containerLogStreamFake{}
	svc := daemon.NewKukeonV1Service(context.Background(), discardLogger(), core)
	args := &kukeonv1.ReadContainerLogArgs{Request: kukeonv1.ContainerLogRequest{
		Stream: kukeonv1.ContainerLogStreamSupervisor,
	}}
	reply := &kukeonv1.ReadContainerLogReply{}
	if err := svc.ReadContainerLog(args, reply); err != nil {
		t.Fatalf("ReadContainerLog returned transport error: %v", err)
	}
	if core.req.Stream != kukeonv1.ContainerLogStreamSupervisor {
		t.Errorf("core got Stream %q", core.req.Stream)
	}
	if reply.Result.Stream != kukeonv1.ContainerLogStreamSupervisor {
		t.Errorf("reply Stream = %q", reply.Result.Stream)
	}
	if !errors.Is(kukeonv1.FromAPIError(reply.Err), errdefs.ErrSupervisorLogUnreadable) {
		t.Errorf("wire error %v does not unwrap to ErrSupervisorLogUnreadable", reply.Err)
	}
}
