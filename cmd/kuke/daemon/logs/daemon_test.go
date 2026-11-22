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

package logs_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/cmd/config"
	logscmd "github.com/eminwux/kukeon/cmd/kuke/daemon/logs"
	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/daemonlog"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// fakeDaemon serves ReadDaemonLogs from a scripted list of replies. Once the
// script is exhausted it answers with an empty caught-up chunk at the last
// cursor, the way an idle kukeond does.
type fakeDaemon struct {
	kukeonv1.FakeClient

	mu       sync.Mutex
	replies  []daemonReply
	requests []kukeonv1.DaemonLogRequest
	closed   bool
}

type daemonReply struct {
	res kukeonv1.DaemonLogResult
	err error
}

func (f *fakeDaemon) ReadDaemonLogs(
	ctx context.Context, req kukeonv1.DaemonLogRequest,
) (kukeonv1.DaemonLogResult, error) {
	if err := ctx.Err(); err != nil {
		return kukeonv1.DaemonLogResult{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)
	if len(f.replies) == 0 {
		return kukeonv1.DaemonLogResult{Start: req.Offset, Next: req.Offset, Size: req.Offset}, nil
	}
	r := f.replies[0]
	f.replies = f.replies[1:]
	return r.res, r.err
}

func (f *fakeDaemon) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeDaemon) snapshot() []kukeonv1.DaemonLogRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]kukeonv1.DaemonLogRequest(nil), f.requests...)
}

// dialRefused is the error shape UnixClient returns when nothing listens on
// the socket path.
func dialRefused() error {
	return fmt.Errorf("dial kukeond at /run/kukeon/kukeond/kukeond.sock: %w",
		&net.OpError{Op: "dial", Net: "unix", Err: errors.New("connect: no such file or directory")})
}

func unreachableDaemon() *fakeDaemon {
	return &fakeDaemon{replies: []daemonReply{{err: dialRefused()}}}
}

// inProcessForbidden is an in-process client whose every call errors: the
// socket path must not fall through to it.
func inProcessForbidden() *fakeClient {
	return &fakeClient{}
}

// readyInProcess is an in-process client for a running kukeond cell.
func readyInProcess(t *testing.T) *fakeClient {
	t.Helper()
	return &fakeClient{
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			assertKukeondCell(t, doc)
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostLogPath: kukeondHostLogPath}, nil
		},
	}
}

func newDaemonCmd(
	t *testing.T, fd *fakeDaemon, fc *fakeClient, tail *tailCapture,
) (*cobra.Command, *strings.Builder) {
	t.Helper()
	cmd, _ := newCmd(t, fc, tail)
	ctx := context.WithValue(cmd.Context(), logscmd.MockDaemonClientKey{}, kukeonv1.Client(fd))
	cmd.SetContext(ctx)
	out := &strings.Builder{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	return cmd, out
}

func TestDaemonLogs_Socket_PrintsWholeLogAcrossChunks(t *testing.T) {
	t.Cleanup(viper.Reset)
	// The socket path needs no root: only the in-process fallback does.
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)

	fd := &fakeDaemon{replies: []daemonReply{
		{res: kukeonv1.DaemonLogResult{Data: "one\ntwo\n", Start: 0, Next: 8, Size: 12, More: true}},
		{res: kukeonv1.DaemonLogResult{Data: "three\n", Start: 8, Next: 14, Size: 14}},
	}}
	tail := &tailCapture{}
	cmd, out := newDaemonCmd(t, fd, inProcessForbidden(), tail)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want nil", err)
	}
	if got, want := out.String(), "one\ntwo\nthree\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	reqs := fd.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("ReadDaemonLogs calls = %d, want 2 (stop once caught up)", len(reqs))
	}
	if reqs[0].Offset != 0 || reqs[1].Offset != 8 {
		t.Errorf("offsets = %d,%d, want 0,8 (forward from the start, then the cursor)",
			reqs[0].Offset, reqs[1].Offset)
	}
	for i, r := range reqs {
		if r.MaxBytes != kukeonv1.DaemonLogMaxBytes {
			t.Errorf("request %d MaxBytes = %d, want %d", i, r.MaxBytes, kukeonv1.DaemonLogMaxBytes)
		}
	}
	if tail.calls != 0 {
		t.Errorf("in-process tail ran %d times, want 0", tail.calls)
	}
	if !fd.closed {
		t.Error("daemon client was not closed")
	}
}

func TestDaemonLogs_Socket_FollowPollsFromCursorUntilCancel(t *testing.T) {
	t.Cleanup(viper.Reset)
	t.Cleanup(logscmd.SetDaemonPollIntervalForTesting(time.Millisecond))

	fd := &fakeDaemon{replies: []daemonReply{
		{res: kukeonv1.DaemonLogResult{Data: "seed\n", Start: 0, Next: 5, Size: 5}},
		{res: kukeonv1.DaemonLogResult{Start: 5, Next: 5, Size: 5}},
		{res: kukeonv1.DaemonLogResult{Data: "later\n", Start: 5, Next: 11, Size: 11}},
		// The source was replaced: the daemon answers with a tail of it.
		{res: kukeonv1.DaemonLogResult{Data: "fresh\n", Start: 0, Next: 6, Size: 6, Reset: true}},
	}}
	cmd, out := newDaemonCmd(t, fd, inProcessForbidden(), nil)
	cmd.SetArgs([]string{"--follow"})
	ctx, cancel := context.WithCancel(cmd.Context())
	cmd.SetContext(ctx)

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()

	deadline := time.Now().Add(5 * time.Second)
	for len(fd.snapshot()) < 6 {
		if time.Now().After(deadline) {
			t.Fatalf("follow issued %d requests, want it to keep polling", len(fd.snapshot()))
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Execute returned %v on cancel, want nil", err)
	}

	if got, want := out.String(), "seed\nlater\nfresh\n"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	reqs := fd.snapshot()
	wantOffsets := []int64{0, 5, 5, 11, 6}
	for i, want := range wantOffsets {
		if reqs[i].Offset != want {
			t.Errorf("request %d offset = %d, want %d", i, reqs[i].Offset, want)
		}
	}
}

func TestDaemonLogs_Socket_FallbackOnUnreachable(t *testing.T) {
	cases := map[string]error{
		"dial refused":            dialRefused(),
		"connection dropped":      io.ErrUnexpectedEOF,
		"daemon predates the rpc": fmt.Errorf("%w: KukeonV1.ReadDaemonLogs", errdefs.ErrDaemonMethodUnsupported),
		"unsupported scheme":      kukeonv1.ErrUnsupportedScheme,
	}
	for name, sockErr := range cases {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			fd := &fakeDaemon{replies: []daemonReply{{err: sockErr}}}
			fc := readyInProcess(t)
			tail := &tailCapture{payload: []byte("in-process\n")}
			cmd, out := newDaemonCmd(t, fd, fc, tail)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute returned %v, want the in-process fallback to succeed", err)
			}
			if tail.calls != 1 || tail.path != kukeondHostLogPath {
				t.Errorf("tail calls=%d path=%q, want 1 call on %q", tail.calls, tail.path, kukeondHostLogPath)
			}
			if got := out.String(); got != "in-process\n" {
				t.Errorf("stdout = %q, want %q", got, "in-process\n")
			}
		})
	}
}

func TestDaemonLogs_Socket_DaemonAnswerIsFinal(t *testing.T) {
	t.Cleanup(viper.Reset)
	answer := fmt.Errorf("%w: no log file at /x", errdefs.ErrDaemonLogUnavailable)
	fd := &fakeDaemon{replies: []daemonReply{{err: answer}}}
	tail := &tailCapture{}
	cmd, _ := newDaemonCmd(t, fd, inProcessForbidden(), tail)

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrDaemonLogUnavailable) {
		t.Fatalf("Execute error = %v, want ErrDaemonLogUnavailable from the daemon", err)
	}
	if tail.calls != 0 {
		t.Errorf("in-process tail ran %d times, want 0: a daemon answer must not fall back", tail.calls)
	}
}

func TestDaemonLogs_Socket_DropAfterFirstChunkDoesNotFallBack(t *testing.T) {
	t.Cleanup(viper.Reset)
	fd := &fakeDaemon{replies: []daemonReply{
		{res: kukeonv1.DaemonLogResult{Data: "partial\n", Start: 0, Next: 8, Size: 20, More: true}},
		{err: io.ErrUnexpectedEOF},
	}}
	tail := &tailCapture{}
	cmd, out := newDaemonCmd(t, fd, inProcessForbidden(), tail)

	err := cmd.Execute()
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("Execute error = %v, want the dropped connection surfaced", err)
	}
	if tail.calls != 0 {
		t.Errorf("in-process tail ran %d times, want 0 (output would repeat)", tail.calls)
	}
	if got := out.String(); got != "partial\n" {
		t.Errorf("stdout = %q, want %q", got, "partial\n")
	}
}

func TestDaemonLogs_SocketSkipped(t *testing.T) {
	absent := t.TempDir() + "/absent.yaml"
	cases := map[string]struct {
		setup func(t *testing.T)
		args  []string
	}{
		"no-daemon": {setup: func(_ *testing.T) {
			viper.Set(config.KUKEON_ROOT_NO_DAEMON.ViperKey, true)
		}},
		"server-configuration flag": {
			setup: func(_ *testing.T) {},
			args:  []string{"--server-configuration", absent},
		},
		"server-configuration env": {setup: func(t *testing.T) {
			t.Setenv(config.KUKEOND_CONFIGURATION.EnvVar(), absent)
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			tc.setup(t)
			args := tc.args
			fd := &fakeDaemon{}
			tail := &tailCapture{payload: []byte("in-process\n")}
			cmd, _ := newDaemonCmd(t, fd, readyInProcess(t), tail)
			cmd.SetArgs(args)

			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute returned %v, want nil", err)
			}
			if n := len(fd.snapshot()); n != 0 {
				t.Errorf("ReadDaemonLogs calls = %d, want 0", n)
			}
			if tail.calls != 1 {
				t.Errorf("in-process tail calls = %d, want 1", tail.calls)
			}
		})
	}
}

// TestDaemonLogs_Socket_RealDialErrorsFallBack drives the real UnixClient so
// the fallback classification is checked against the errors a dial actually
// returns: a missing socket path (a cell without the mount, or a stopped
// daemon that removed it) and a stale socket file nobody listens on.
func TestDaemonLogs_Socket_RealDialErrorsFallBack(t *testing.T) {
	dir := t.TempDir()
	stale := dir + "/stale.sock"
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Keep the socket file but stop listening: dials now get ECONNREFUSED.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()

	for name, path := range map[string]string{"missing": dir + "/absent.sock", "stale": stale} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			tail := &tailCapture{payload: []byte("in-process\n")}
			cmd, out := newCmd(t, readyInProcess(t), tail)
			ctx := context.WithValue(cmd.Context(), logscmd.MockDaemonClientKey{},
				kukeonv1.Client(kukeonv1.NewUnixClient(path)))
			cmd.SetContext(ctx)

			if execErr := cmd.Execute(); execErr != nil {
				t.Fatalf("Execute returned %v, want the in-process fallback", execErr)
			}
			if got := out.String(); got != "in-process\n" {
				t.Errorf("stdout = %q, want %q", got, "in-process\n")
			}
		})
	}
}

// wireDaemon serves ReadDaemonLogs over a real JSON-RPC unix socket, backed
// by the production chunk reader, so the test exercises the wire end to end.
type wireDaemon struct{ path string }

func (w *wireDaemon) ReadDaemonLogs(args *kukeonv1.ReadDaemonLogsArgs, reply *kukeonv1.ReadDaemonLogsReply) error {
	res, err := daemonlog.Read(w.path, args.Request)
	if err != nil {
		return err
	}
	reply.Result = res
	return nil
}

// pingOnlyDaemon is a kukeond that predates ReadDaemonLogs.
type pingOnlyDaemon struct{}

func (*pingOnlyDaemon) Ping(_ *kukeonv1.PingArgs, reply *kukeonv1.PingReply) error {
	reply.Version = "old"
	return nil
}

func serveRPC(t *testing.T, svc any) string {
	t.Helper()
	sock := t.TempDir() + "/kukeond.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := rpc.NewServer()
	if regErr := srv.RegisterName(kukeonv1.ServiceName, svc); regErr != nil {
		t.Fatalf("register: %v", regErr)
	}
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go srv.ServeCodec(jsonrpc.NewServerCodec(conn))
		}
	}()
	return sock
}

// newHostCmd builds the command with no daemon mock: the socket path dials
// KUKEON_HOST for real.
func newHostCmd(t *testing.T, sock string, fc *fakeClient, tail *tailCapture) (*cobra.Command, *syncBuffer) {
	t.Helper()
	viper.Set(config.KUKEON_ROOT_HOST.ViperKey, "unix://"+sock)
	cmd := logscmd.NewLogsCmd()
	ctx := context.WithValue(context.Background(), types.CtxLogger, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fc))
	if tail != nil {
		ctx = context.WithValue(ctx, logscmd.MockTailKey{}, logcmd.TailFn(tail.fn))
	}
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := &syncBuffer{}
	cmd.SetOut(out)
	cmd.SetErr(io.Discard)
	return cmd, out
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestDaemonLogs_Socket_RealWire(t *testing.T) {
	t.Cleanup(viper.Reset)
	t.Cleanup(logscmd.SetDaemonPollIntervalForTesting(5 * time.Millisecond))
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)

	// Larger than DaemonLogMaxBytes so the dump has to follow More.
	logPath := t.TempDir() + "/kukeond.log"
	var want strings.Builder
	for i := 0; want.Len() < 3*kukeonv1.DaemonLogMaxBytes; i++ {
		fmt.Fprintf(&want, "time=2026-11-22 level=INFO msg=line-%06d\n", i)
	}
	if err := os.WriteFile(logPath, []byte(want.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := serveRPC(t, &wireDaemon{path: logPath})

	t.Run("dump", func(t *testing.T) {
		cmd, out := newHostCmd(t, sock, inProcessForbidden(), nil)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("Execute returned %v, want nil", err)
		}
		if out.String() != want.String() {
			t.Fatalf("stdout is %d bytes, want the whole %d-byte log", len(out.String()), want.Len())
		}
	})

	t.Run("follow", func(t *testing.T) {
		cmd, out := newHostCmd(t, sock, inProcessForbidden(), nil)
		cmd.SetArgs([]string{"-f"})
		ctx, cancel := context.WithCancel(cmd.Context())
		cmd.SetContext(ctx)
		done := make(chan error, 1)
		go func() { done <- cmd.Execute() }()

		waitFor(t, func() bool { return len(out.String()) == want.Len() })
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.WriteString("appended-")
		waitFor(t, func() bool { return true })
		_, _ = f.WriteString("after-follow\n")
		_ = f.Close()
		waitFor(t, func() bool { return strings.HasSuffix(out.String(), "appended-after-follow\n") })

		cancel()
		if execErr := <-done; execErr != nil {
			t.Fatalf("Execute returned %v on cancel, want nil", execErr)
		}
	})
}

func TestDaemonLogs_Socket_RealWireOldDaemonFallsBack(t *testing.T) {
	t.Cleanup(viper.Reset)
	sock := serveRPC(t, &pingOnlyDaemon{})
	tail := &tailCapture{payload: []byte("in-process\n")}
	cmd, out := newHostCmd(t, sock, readyInProcess(t), tail)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want the in-process fallback", err)
	}
	if got := out.String(); got != "in-process\n" {
		t.Errorf("stdout = %q, want %q", got, "in-process\n")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
