// Copyright 2026 Emiliano Spinella (eminwux)
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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

const execTestTimeout = 5 * time.Second

// fakeExecProcess is the process behind a session under test. Its script
// runs once the session started it and has the session's streams.
type fakeExecProcess struct {
	stdio ctr.ExecStdio
	exit  chan uint32

	mu          sync.Mutex
	calls       []string
	resizes     [][2]uint32
	stdinClosed chan struct{}
	deleted     chan struct{}
}

func newFakeExecProcess() *fakeExecProcess {
	return &fakeExecProcess{
		exit:        make(chan uint32, 1),
		stdinClosed: make(chan struct{}),
		deleted:     make(chan struct{}),
	}
}

func (p *fakeExecProcess) record(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
}

func (p *fakeExecProcess) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.calls)
}

func (p *fakeExecProcess) Wait(ctx context.Context) (uint32, error) {
	select {
	case code := <-p.exit:
		return code, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (p *fakeExecProcess) Resize(_ context.Context, cols, rows uint32) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]uint32{cols, rows})
	return nil
}

func (p *fakeExecProcess) CloseStdin(context.Context) error {
	p.record("CloseStdin")
	close(p.stdinClosed)
	return nil
}

func (p *fakeExecProcess) Kill(context.Context) error {
	p.record("Kill")
	select {
	case p.exit <- 137:
	default:
	}
	return nil
}

func (p *fakeExecProcess) Delete(context.Context) error {
	p.record("Delete")
	close(p.deleted)
	return nil
}

func (p *fakeExecProcess) waitDeleted(t *testing.T) {
	t.Helper()
	select {
	case <-p.deleted:
	case <-time.After(execTestTimeout):
		t.Fatalf("session did not delete the process; calls so far: %v", p.recorded())
	}
}

// execRunner stubs the runner calls ExecContainer makes. A stray call to any
// other method panics on the nil embedded interface.
type execRunner struct {
	runner.Runner

	state   intmodel.ContainerState
	process *fakeExecProcess
	// script runs in its own goroutine once the process "started".
	script   func(p *fakeExecProcess)
	startErr error

	mu      sync.Mutex
	lookups int
	execs   []ctr.ExecSpec
	names   []string
}

func (r *execRunner) GetCell(cell intmodel.Cell) (intmodel.Cell, error) {
	r.mu.Lock()
	r.lookups++
	r.mu.Unlock()
	cell.Spec.Containers = []intmodel.ContainerSpec{
		{
			ID: "root", Root: true, RealmName: cell.Spec.RealmName, SpaceName: cell.Spec.SpaceName,
			StackName: cell.Spec.StackName, CellName: cell.Metadata.Name,
		},
		{
			ID: "work", RealmName: cell.Spec.RealmName, SpaceName: cell.Spec.SpaceName,
			StackName: cell.Spec.StackName, CellName: cell.Metadata.Name,
		},
	}
	return cell, nil
}

func (r *execRunner) GetContainerState(intmodel.Cell, string) (intmodel.ContainerState, error) {
	return r.state, nil
}

func (r *execRunner) ExecContainer(
	_ intmodel.Cell, name string, spec ctr.ExecSpec, stdio ctr.ExecStdio,
) (ctr.ExecProcess, error) {
	r.mu.Lock()
	r.execs = append(r.execs, spec)
	r.names = append(r.names, name)
	r.mu.Unlock()
	if r.startErr != nil {
		return nil, r.startErr
	}
	r.process.stdio = stdio
	if r.script != nil {
		go r.script(r.process)
	}
	return r.process, nil
}

func (r *execRunner) calls() (int, []ctr.ExecSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lookups, slices.Clone(r.execs)
}

// syncBuffer is a log sink safe to read while the session still writes.
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

func newExecClient(t *testing.T, r *execRunner) (*Client, *syncBuffer) {
	t.Helper()
	if r.process == nil {
		r.process = newFakeExecProcess()
	}
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	exec := controller.NewControllerExecForTesting(context.Background(), logger,
		controller.Options{RunPath: t.TempDir()}, r)
	return NewFromExec(exec), logs
}

// frame is one frame read from the caller's end of a session.
type frame struct {
	typ     byte
	payload string
}

// readSession reads the caller's end until the daemon closes it.
func readSession(t *testing.T, conn *net.UnixConn) []frame {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(execTestTimeout))
	var frames []frame
	for {
		typ, payload, err := execstream.ReadFrame(conn)
		if errors.Is(err, io.EOF) {
			return frames
		}
		if err != nil {
			t.Fatalf("read session: %v (frames so far: %v)", err, frames)
		}
		frames = append(frames, frame{typ, string(payload)})
	}
}

func exitOf(t *testing.T, frames []frame) execstream.Exit {
	t.Helper()
	if len(frames) == 0 || frames[len(frames)-1].typ != execstream.TypeExit {
		t.Fatalf("session did not end with an exit frame: %v", frames)
	}
	exit, err := execstream.DecodeExit([]byte(frames[len(frames)-1].payload))
	if err != nil {
		t.Fatal(err)
	}
	return exit
}

// auditEntries returns the "exec started" and "exec finished" log records.
func auditEntries(t *testing.T, logs *syncBuffer) (map[string]any, map[string]any) {
	t.Helper()
	var started, finished map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		switch entry["msg"] {
		case "exec started":
			started = entry
		case "exec finished":
			finished = entry
		}
	}
	if started == nil || finished == nil {
		t.Fatalf("want an exec started and an exec finished entry, got:\n%s", logs.String())
	}
	return started, finished
}

// waitFinished blocks until the session wrote its finish audit entry.
func waitFinished(t *testing.T, logs *syncBuffer) {
	t.Helper()
	deadline := time.Now().Add(execTestTimeout)
	for !strings.Contains(logs.String(), `"msg":"exec finished"`) {
		if time.Now().After(deadline) {
			t.Fatalf("no exec finished entry:\n%s", logs.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestExecContainer_InvalidRequests(t *testing.T) {
	cases := map[string]kukeonv1.ExecRequest{
		"empty command":       {},
		"empty argv[0]":       {Command: []string{""}},
		"root with user":      {Command: []string{"id"}, Root: true, User: "1000"},
		"env without equals":  {Command: []string{"id"}, Env: []string{"OK=1", "BROKEN"}},
		"env without a key":   {Command: []string{"id"}, Env: []string{"=value"}},
		"size without a tty":  {Command: []string{"id"}, Cols: 80, Rows: 24},
		"width without a tty": {Command: []string{"id"}, Cols: 80},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			r := &execRunner{state: intmodel.ContainerStateReady}
			c, _ := newExecClient(t, r)
			conn, err := c.ExecContainer(context.Background(), workDoc(), req)
			if conn != nil {
				_ = conn.Close()
				t.Fatal("got a connection for an invalid request")
			}
			if !errors.Is(err, errdefs.ErrExecInvalidRequest) {
				t.Fatalf("err = %v, want ErrExecInvalidRequest", err)
			}
			if strings.Contains(err.Error(), "BROKEN") || strings.Contains(err.Error(), "value") {
				t.Errorf("error %q repeats the env entry", err)
			}
			if lookups, execs := r.calls(); lookups != 0 || len(execs) != 0 {
				t.Errorf("runner was called (%d lookups, %d execs) for an invalid request", lookups, len(execs))
			}
		})
	}
}

func TestExecContainer_Gates(t *testing.T) {
	t.Run("unknown container", func(t *testing.T) {
		r := &execRunner{state: intmodel.ContainerStateReady}
		c, _ := newExecClient(t, r)
		doc := workDoc()
		doc.Metadata.Name, doc.Spec.ID = "nope", "nope"
		_, err := c.ExecContainer(context.Background(), doc, kukeonv1.ExecRequest{Command: []string{"id"}})
		if !errors.Is(err, errdefs.ErrContainerNotFound) {
			t.Fatalf("err = %v, want ErrContainerNotFound", err)
		}
	})
	t.Run("task not running", func(t *testing.T) {
		r := &execRunner{state: intmodel.ContainerStateStopped}
		c, _ := newExecClient(t, r)
		_, err := c.ExecContainer(context.Background(), workDoc(), kukeonv1.ExecRequest{Command: []string{"id"}})
		if !errors.Is(err, errdefs.ErrExecTaskNotRunning) {
			t.Fatalf("err = %v, want ErrExecTaskNotRunning", err)
		}
		for _, want := range []string{`"work"`, `"c"`, "Stopped"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name %s", err, want)
			}
		}
		if _, execs := r.calls(); len(execs) != 0 {
			t.Error("exec reached the runner for a container that is not running")
		}
	})
	t.Run("root container, not attachable", func(t *testing.T) {
		r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) { p.exit <- 0 }}
		c, _ := newExecClient(t, r)
		doc := workDoc()
		doc.Metadata.Name, doc.Spec.ID = "root", "root"
		conn, err := c.ExecContainer(context.Background(), doc, kukeonv1.ExecRequest{Command: []string{"id"}})
		if err != nil {
			t.Fatalf("exec into the root container: %v", err)
		}
		defer conn.Close()
		if exit := exitOf(t, readSession(t, conn)); exit.Code != 0 {
			t.Fatalf("exit = %+v, want 0", exit)
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if !slices.Equal(r.names, []string{"root"}) {
			t.Errorf("runner exec'd %v, want root", r.names)
		}
	})
}

// TestExecSession_OutputThenExit pins the normal path: stdout and stderr
// arrive as their own frames, the exit frame carries the code and comes
// last, and the process is deleted.
func TestExecSession_OutputThenExit(t *testing.T) {
	r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) {
		_, _ = p.stdio.Stdout.Write([]byte("out"))
		_, _ = p.stdio.Stderr.Write([]byte("err"))
		p.exit <- 7
	}}
	c, logs := newExecClient(t, r)
	req := kukeonv1.ExecRequest{Command: []string{"sh", "-c", "echo out; echo err >&2; exit 7"}, Workdir: "/tmp"}
	conn, err := c.ExecContainer(context.Background(), workDoc(), req)
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()

	frames := readSession(t, conn)
	want := []frame{{execstream.TypeStdout, "out"}, {execstream.TypeStderr, "err"}}
	if len(frames) != 3 || !slices.Equal(frames[:2], want) {
		t.Fatalf("frames = %v, want stdout, stderr, exit", frames)
	}
	if exit := exitOf(t, frames); exit.Code != 7 || exit.Error != "" {
		t.Fatalf("exit = %+v, want code 7", exit)
	}
	r.process.waitDeleted(t)
	if calls := r.process.recorded(); !slices.Equal(calls, []string{"Delete"}) {
		t.Errorf("process calls = %v, want only Delete (no kill on a normal exit)", calls)
	}
	if r.process.stdio.Stdin != nil {
		t.Error("process got a stdin stream without Stdin in the request")
	}

	_, execs := r.calls()
	if len(execs) != 1 {
		t.Fatalf("runner exec'd %d times, want 1", len(execs))
	}
	spec := execs[0]
	if !strings.HasPrefix(spec.ID, "kuke-exec-") || len(spec.ID) != len("kuke-exec-")+16 {
		t.Errorf("exec id = %q, want kuke-exec- plus 16 hex characters", spec.ID)
	}
	if !slices.Equal(spec.Command, req.Command) || spec.Workdir != "/tmp" || spec.Root || spec.TTY {
		t.Errorf("exec spec = %+v, want the request's command and workdir", spec)
	}
	waitFinished(t, logs)
}

func TestExecSession_StartFailure(t *testing.T) {
	r := &execRunner{state: intmodel.ContainerStateReady, startErr: errors.New("exec: \"nope\": not found")}
	c, logs := newExecClient(t, r)
	conn, err := c.ExecContainer(context.Background(), workDoc(), kukeonv1.ExecRequest{Command: []string{"nope"}})
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()

	frames := readSession(t, conn)
	exit := exitOf(t, frames)
	if len(frames) != 1 || exit.Code != 1 || !strings.Contains(exit.Error, "not found") {
		t.Fatalf("frames = %v, want one exit frame with code 1 and the start error", frames)
	}
	waitFinished(t, logs)
	_, finished := auditEntries(t, logs)
	if finished["exitCode"] != float64(1) || !strings.Contains(finished["error"].(string), "not found") {
		t.Errorf("finish entry = %v, want exitCode 1 and the start error", finished)
	}
}

// TestExecSession_ClientGoneKillsProcess pins the disconnect path: the
// process is killed, waited for and deleted, and the session ends.
func TestExecSession_ClientGoneKillsProcess(t *testing.T) {
	started := make(chan struct{})
	r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) {
		_, _ = p.stdio.Stdout.Write([]byte("running"))
		close(started)
	}}
	c, logs := newExecClient(t, r)
	conn, err := c.ExecContainer(
		context.Background(),
		workDoc(),
		kukeonv1.ExecRequest{Command: []string{"sleep", "600"}},
	)
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	<-started
	_ = conn.Close()

	r.process.waitDeleted(t)
	if calls := r.process.recorded(); !slices.Equal(calls, []string{"Kill", "Delete"}) {
		t.Fatalf("process calls = %v, want Kill then Delete", calls)
	}
	waitFinished(t, logs)
	_, finished := auditEntries(t, logs)
	if finished["clientDisconnected"] != true || finished["exitCode"] != float64(137) {
		t.Errorf("finish entry = %v, want clientDisconnected and exit code 137", finished)
	}
}

// TestExecSession_ClientGoneWhileStdinBlocked pins the case the read loop
// cannot see: it is blocked handing stdin to a process that never reads,
// and the caller disappears.
func TestExecSession_ClientGoneWhileStdinBlocked(t *testing.T) {
	r := &execRunner{state: intmodel.ContainerStateReady}
	c, logs := newExecClient(t, r)
	conn, err := c.ExecContainer(context.Background(), workDoc(),
		kukeonv1.ExecRequest{Command: []string{"sleep", "600"}, Stdin: true})
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	// Nothing reads the process's stdin, so the session's read loop blocks on
	// the first frame and the rest stays in the socket buffer.
	w := execstream.NewWriter(conn)
	for range 3 {
		if err = w.WriteData(execstream.TypeStdin, []byte("unread")); err != nil {
			t.Fatalf("write stdin: %v", err)
		}
	}
	time.Sleep(50 * time.Millisecond) // let the read loop reach the blocked write
	_ = conn.Close()

	r.process.waitDeleted(t)
	if calls := r.process.recorded(); !slices.Equal(calls, []string{"Kill", "Delete"}) {
		t.Fatalf("process calls = %v, want Kill then Delete", calls)
	}
	waitFinished(t, logs)
}

// TestExecSession_StdinEOFAndResize pins the caller-to-daemon frames.
func TestExecSession_StdinEOFAndResize(t *testing.T) {
	got := make(chan string, 1)
	r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) {
		data, _ := io.ReadAll(p.stdio.Stdin)
		<-p.stdinClosed
		got <- string(data)
		p.exit <- 0
	}}
	c, logs := newExecClient(t, r)
	req := kukeonv1.ExecRequest{Command: []string{"cat"}, Stdin: true, TTY: true, Cols: 80, Rows: 24}
	conn, err := c.ExecContainer(context.Background(), workDoc(), req)
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()

	w := execstream.NewWriter(conn)
	payload := strings.Repeat("0123456789", 10_000) // several frames
	if err = w.Resize(132, 43); err != nil {
		t.Fatal(err)
	}
	if err = w.WriteData(execstream.TypeStdin, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err = w.StdinEOF(); err != nil {
		t.Fatal(err)
	}

	if exit := exitOf(t, readSession(t, conn)); exit.Code != 0 {
		t.Fatalf("exit = %+v, want 0", exit)
	}
	if data := <-got; data != payload {
		t.Fatalf("process read %d stdin bytes, want the %d written", len(data), len(payload))
	}
	r.process.waitDeleted(t)
	if calls := r.process.recorded(); !slices.Equal(calls, []string{"CloseStdin", "Delete"}) {
		t.Errorf("process calls = %v, want CloseStdin then Delete", calls)
	}
	r.process.mu.Lock()
	resizes := slices.Clone(r.process.resizes)
	r.process.mu.Unlock()
	if !slices.Equal(resizes, [][2]uint32{{80, 24}, {132, 43}}) {
		t.Errorf("resizes = %v, want the initial 80x24 then 132x43", resizes)
	}
	if r.process.stdio.Stderr != nil {
		t.Error("a TTY session wired a stderr stream; the PTY carries both on stdout")
	}
	_, execs := r.calls()
	if len(execs) != 1 || !execs[0].TTY {
		t.Errorf("exec spec = %+v, want TTY", execs)
	}
	waitFinished(t, logs)
}

// TestExecSession_NoOutputAfterExit pins that the exit frame is the last:
// a process whose child keeps writing after it exited cannot append to it.
func TestExecSession_NoOutputAfterExit(t *testing.T) {
	late := make(chan error, 1)
	r := &execRunner{state: intmodel.ContainerStateReady}
	c, logs := newExecClient(t, r)
	r.script = func(p *fakeExecProcess) {
		p.exit <- 0
		waitFinished(t, logs)
		_, err := p.stdio.Stdout.Write([]byte("late"))
		late <- err
	}
	conn, err := c.ExecContainer(context.Background(), workDoc(), kukeonv1.ExecRequest{Command: []string{"true"}})
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()

	if frames := readSession(t, conn); len(frames) != 1 || frames[0].typ != execstream.TypeExit {
		t.Fatalf("frames = %v, want only the exit frame", frames)
	}
	if err = <-late; err == nil {
		t.Error("a write after the exit frame was accepted")
	}
}

func TestExecSession_Audit(t *testing.T) {
	cases := []struct {
		name         string
		req          kukeonv1.ExecRequest
		wantIdentity string
		wantRoot     bool
	}{
		{"container user", kukeonv1.ExecRequest{}, "container", false},
		{"root", kukeonv1.ExecRequest{Root: true}, "root", true},
		{"uid 0", kukeonv1.ExecRequest{User: "0"}, "0", true},
		{"uid 0 with gid", kukeonv1.ExecRequest{User: "0:1000"}, "0:1000", true},
		{"root by name", kukeonv1.ExecRequest{User: "root"}, "root", true},
		{"other uid", kukeonv1.ExecRequest{User: "1000:0"}, "1000:0", false},
		{"other name", kukeonv1.ExecRequest{User: "agent"}, "agent", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) { p.exit <- 3 }}
			c, logs := newExecClient(t, r)
			req := tc.req
			req.Command = []string{"env"}
			req.Env = []string{"TOKEN=s3cr3t-value", "MODE=debug"}
			ctx := kukeonv1.WithPeerUID(context.Background(), 4242)
			conn, err := c.ExecContainer(ctx, workDoc(), req)
			if err != nil {
				t.Fatalf("ExecContainer: %v", err)
			}
			defer conn.Close()
			readSession(t, conn)
			waitFinished(t, logs)

			if strings.Contains(logs.String(), "s3cr3t-value") || strings.Contains(logs.String(), "debug") {
				t.Fatalf("the log carries an env value:\n%s", logs.String())
			}
			started, finished := auditEntries(t, logs)
			for _, entry := range []map[string]any{started, finished} {
				want := map[string]any{
					"level": "INFO", "realm": "r", "space": "s", "stack": "st", "cell": "c", "container": "work",
					"identity": tc.wantIdentity, "root": tc.wantRoot, "tty": false, "callerUID": float64(4242),
				}
				for key, value := range want {
					if entry[key] != value {
						t.Errorf("%s: %s = %v, want %v", entry["msg"], key, entry[key], value)
					}
				}
				if id, _ := entry["execID"].(string); !strings.HasPrefix(id, "kuke-exec-") {
					t.Errorf("%s: execID = %v", entry["msg"], entry["execID"])
				}
				if argv, _ := json.Marshal(entry["argv"]); string(argv) != `["env"]` {
					t.Errorf("%s: argv = %s", entry["msg"], argv)
				}
				if keys, _ := json.Marshal(entry["envKeys"]); string(keys) != `["TOKEN","MODE"]` {
					t.Errorf("%s: envKeys = %s", entry["msg"], keys)
				}
			}
			if finished["exitCode"] != float64(3) || finished["clientDisconnected"] != false {
				t.Errorf("finish entry = %v, want exitCode 3 and no disconnect", finished)
			}
			if _, ok := finished["duration"].(string); !ok {
				t.Errorf("finish entry has no duration: %v", finished)
			}
			if _, ok := started["exitCode"]; ok {
				t.Errorf("start entry carries an exit code: %v", started)
			}
		})
	}
}

// TestExecSession_AuditOmitsUnknownCaller pins that an in-process call, with
// no socket peer, logs no caller uid rather than a made-up one.
func TestExecSession_AuditOmitsUnknownCaller(t *testing.T) {
	r := &execRunner{state: intmodel.ContainerStateReady, script: func(p *fakeExecProcess) { p.exit <- 0 }}
	c, logs := newExecClient(t, r)
	conn, err := c.ExecContainer(context.Background(), workDoc(), kukeonv1.ExecRequest{Command: []string{"id"}})
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()
	readSession(t, conn)
	waitFinished(t, logs)
	started, finished := auditEntries(t, logs)
	if _, ok := started["callerUID"]; ok {
		t.Errorf("start entry names a caller uid: %v", started)
	}
	if _, ok := finished["callerUID"]; ok {
		t.Errorf("finish entry names a caller uid: %v", finished)
	}
}

// TestExecSession_EndsWithDaemonContext pins that a session does not outlive
// kukeond: cancelling the context the call ran on kills the process.
func TestExecSession_EndsWithDaemonContext(t *testing.T) {
	r := &execRunner{state: intmodel.ContainerStateReady}
	c, logs := newExecClient(t, r)
	ctx, cancel := context.WithCancel(context.Background())
	conn, err := c.ExecContainer(ctx, workDoc(), kukeonv1.ExecRequest{Command: []string{"sleep", "600"}})
	if err != nil {
		t.Fatalf("ExecContainer: %v", err)
	}
	defer conn.Close()
	cancel()

	r.process.waitDeleted(t)
	if calls := r.process.recorded(); !slices.Equal(calls, []string{"Kill", "Delete"}) {
		t.Fatalf("process calls = %v, want Kill then Delete", calls)
	}
	// The caller sees the connection end without an exit frame.
	if frames := readSession(t, conn); len(frames) != 0 {
		t.Errorf("frames = %v, want none", frames)
	}
	waitFinished(t, logs)
}
