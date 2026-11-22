// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package log_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	"github.com/eminwux/kukeon/internal/containerlog"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func logArgs() []string {
	return []string{"--realm", "r1", "--space", "s1", "--stack", "st1", "--container", "work", "c1"}
}

// fileBackedClient serves ReadContainerLog from a real file through the
// production chunk reader, and fails any LogContainer call: the RPC path
// must not resolve or open host paths itself.
func fileBackedClient(t *testing.T, path string, reqs *[]kukeonv1.ContainerLogRequest, mu *sync.Mutex) *fakeClient {
	t.Helper()
	return &fakeClient{
		logContainerFn: func(v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			t.Errorf("LogContainer called on the ReadContainerLog path")
			return kukeonv1.LogContainerResult{}, errors.New("unexpected")
		},
		readContainerLogFn: func(
			doc v1beta1.ContainerDoc, req kukeonv1.ContainerLogRequest,
		) (kukeonv1.ContainerLogResult, error) {
			if doc.Spec.CellID != "c1" || doc.Metadata.Name != "work" || doc.Spec.RealmID != "r1" {
				t.Errorf("doc = %+v", doc)
			}
			if mu != nil {
				mu.Lock()
				defer mu.Unlock()
			}
			if reqs != nil {
				*reqs = append(*reqs, req)
			}
			res, err := containerlog.Read(path, req)
			if errors.Is(err, os.ErrNotExist) {
				return kukeonv1.ContainerLogResult{Source: path}, errdefs.ErrContainerLogUnavailable
			}
			return res, err
		},
	}
}

func TestLog_RPC_DumpPagesWholeStreamVerbatim(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Larger than ContainerLogMaxBytes so the dump has to follow More; ends
	// in a prompt with no newline and carries non-UTF-8 bytes.
	var want bytes.Buffer
	for want.Len() < 2*kukeonv1.ContainerLogMaxBytes+17 {
		want.WriteString("\x1b[32mok\x1b[0m \xff\xfe line\r\n")
	}
	want.WriteString("user@cell:~$ ")
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, want.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var reqs []kukeonv1.ContainerLogRequest
	cmd, out := newCmdWithCtx(t, fileBackedClient(t, path, &reqs, nil), nil)
	cmd.SetArgs(logArgs())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want.Bytes()) {
		t.Fatalf("stdout differs from the stream (%d vs %d bytes)", out.Len(), want.Len())
	}
	if len(reqs) != 3 || reqs[0].Offset != 0 || reqs[1].Offset != kukeonv1.ContainerLogMaxBytes {
		t.Fatalf("requests = %+v, want three forward reads from 0", reqs)
	}
	for _, r := range reqs {
		if r.MaxBytes != kukeonv1.ContainerLogMaxBytes {
			t.Fatalf("MaxBytes = %d", r.MaxBytes)
		}
	}
}

// syncBuffer is an io.Writer safe to read while the follow loop writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
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

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// runFollow starts `kuke log -f` with client and returns its output, a
// cancel (SIGINT stand-in) and a wait for the command's result.
func runFollow(t *testing.T, client kukeonv1.Client) (*syncBuffer, context.CancelFunc, func() error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	t.Cleanup(logcmd.SetPollIntervalForTesting(5 * time.Millisecond))

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), logcmd.MockControllerKey{}, client))
	cmd := logcmd.NewLogCmd()
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := &syncBuffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(append(logArgs(), "-f"))

	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	return out, cancel, func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("kuke log -f did not stop after cancel")
			return nil
		}
	}
}

func TestLog_RPC_FollowContinuesFromCursorAndResetsOnShrink(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, []byte("first$ "), 0o600); err != nil {
		t.Fatal(err)
	}
	var (
		mu   sync.Mutex
		reqs []kukeonv1.ContainerLogRequest
	)
	out, cancel, wait := runFollow(t, fileBackedClient(t, path, &reqs, &mu))

	waitFor(t, "the initial dump", func() bool { return out.String() == "first$ " })

	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("ls\r\n")
	_ = f.Close()
	waitFor(t, "appended bytes", func() bool { return out.String() == "first$ ls\r\n" })

	mu.Lock()
	var sawCursor bool
	for _, r := range reqs {
		if r.Offset == int64(len("first$ ")) {
			sawCursor = true
		}
	}
	mu.Unlock()
	if !sawCursor {
		t.Fatalf("follow never polled from the cursor; requests %+v", reqs)
	}

	// Replace the stream with a shorter one: the cursor (11) is now past
	// the end (4), so the reader must start over from 0.
	if err = os.WriteFile(path, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the replaced stream", func() bool { return out.String() == "first$ ls\r\nnew\n" })

	cancel()
	if err = wait(); err != nil {
		t.Fatalf("follow returned %v on cancel, want nil", err)
	}
}

func TestLog_RPC_FollowSurvivesStreamFileRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, cancel, wait := runFollow(t, fileBackedClient(t, path, nil, &sync.Mutex{}))
	waitFor(t, "the initial dump", func() bool { return out.String() == "one\n" })

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // a few polls against the missing file
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the recreated stream", func() bool { return out.String() == "one\ntwo\n" })

	cancel()
	if err := wait(); err != nil {
		t.Fatalf("follow returned %v on cancel, want nil", err)
	}
}

func TestLog_RPC_ContainerNotFound_MatchesHostPathError(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			return kukeonv1.ContainerLogResult{}, errdefs.ErrContainerNotFound
		},
	}
	cmd, _ := newCmdWithCtx(t, fc, nil)
	cmd.SetArgs([]string{"--realm", "r1", "--space", "s1", "--stack", "st1", "--container", "ghost", "c1"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("error %v does not unwrap to ErrContainerNotFound", err)
	}
	if want := `container "ghost" not found in cell "c1": container not found`; err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestLog_RPC_UnknownCellPassesThrough(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			return kukeonv1.ContainerLogResult{}, errdefs.ErrCellNotFound
		},
	}
	cmd, _ := newCmdWithCtx(t, fc, nil)
	cmd.SetArgs(logArgs())
	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrCellNotFound) || err.Error() != errdefs.ErrCellNotFound.Error() {
		t.Fatalf("error = %v, want ErrCellNotFound unchanged (as LogContainer reported it)", err)
	}
}

func TestLog_RPC_MissingStreamFile_NamesDaemonPath(t *testing.T) {
	t.Cleanup(viper.Reset)
	path := filepath.Join(t.TempDir(), "never-written")
	cmd, _ := newCmdWithCtx(t, fileBackedClient(t, path, nil, nil), nil)
	cmd.SetArgs(logArgs())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want missing-file error")
	}
	want := `cell "c1" container "work" has no log file at ` + path +
		` (the runtime shim has not opened it yet — try again after the container produces output)`
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

// TestLog_RPC_OldDaemonFallsBackToHostPath covers a kukeond that predates
// ReadContainerLog: `kuke log` resolves the host path and reads it itself.
func TestLog_RPC_OldDaemonFallsBackToHostPath(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		logContainerFn: func(v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostCapturePath: testHostCapture}, nil
		},
	}
	tail := &tailCapture{payload: []byte("legacy")}
	cmd, out := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs(logArgs())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if tail.calls != 1 || tail.path != testHostCapture || out.String() != "legacy" {
		t.Fatalf("tail calls=%d path=%q out=%q", tail.calls, tail.path, out.String())
	}
}

// TestLog_RPC_UnsupportedAfterFirstChunkIsNotRetriedOnHostPath pins that the
// fallback only applies to the first request: a daemon swapped mid-read is
// reported, not silently re-read from the start via the host path.
func TestLog_RPC_UnsupportedAfterFirstChunkIsNotRetriedOnHostPath(t *testing.T) {
	t.Cleanup(viper.Reset)
	calls := 0
	fc := &fakeClient{
		readContainerLogFn: func(v1beta1.ContainerDoc, kukeonv1.ContainerLogRequest) (kukeonv1.ContainerLogResult, error) {
			calls++
			if calls == 1 {
				return kukeonv1.ContainerLogResult{Data: []byte("abc"), Next: 3, Size: 6, More: true}, nil
			}
			return kukeonv1.ContainerLogResult{}, errdefs.ErrDaemonMethodUnsupported
		},
	}
	tail := &tailCapture{}
	cmd, out := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs(logArgs())
	if err := cmd.Execute(); !errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		t.Fatalf("error = %v, want ErrDaemonMethodUnsupported", err)
	}
	if tail.calls != 0 || out.String() != "abc" {
		t.Fatalf("tail calls=%d out=%q", tail.calls, out.String())
	}
}

// wireDaemon serves ReadContainerLog over a real JSON-RPC unix socket,
// backed by the production chunk reader, so the test exercises the wire
// encoding of raw bytes end to end.
type wireDaemon struct{ path string }

func (w *wireDaemon) ReadContainerLog(
	args *kukeonv1.ReadContainerLogArgs, reply *kukeonv1.ReadContainerLogReply,
) error {
	res, err := containerlog.Read(w.path, args.Request)
	reply.Result = res
	reply.Err = kukeonv1.ToAPIError(err)
	return nil
}

func serveRPC(t *testing.T, svc any) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "kukeond.sock")
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

func TestLog_RPC_RealWireKeepsBytesExact(t *testing.T) {
	t.Cleanup(viper.Reset)

	var want bytes.Buffer
	for i := 0; want.Len() < kukeonv1.ContainerLogMaxBytes+100; i++ {
		want.Write([]byte{0x1b, '[', 'K', 0xff, 0x00, 0xc3, '\r', '\n'})
		want.WriteString(strings.Repeat("é", i%7))
	}
	want.WriteString("$ ")
	path := filepath.Join(t.TempDir(), "capture")
	if err := os.WriteFile(path, want.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	client := kukeonv1.NewUnixClient(serveRPC(t, &wireDaemon{path: path}))
	t.Cleanup(func() { _ = client.Close() })

	cmd := logcmd.NewLogCmd()
	cmd.SetContext(context.WithValue(context.Background(), logcmd.MockControllerKey{}, kukeonv1.Client(client)))
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(logArgs())
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !bytes.Equal(out.Bytes(), want.Bytes()) {
		t.Fatalf("bytes changed on the wire (%d vs %d bytes)", out.Len(), want.Len())
	}
}
