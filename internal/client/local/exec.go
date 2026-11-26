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
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"golang.org/x/sys/unix"
)

const (
	execIDPrefix      = "kuke-exec-"
	execIDRandomBytes = 8

	// execControlTimeout bounds a resize or stdin close against containerd.
	execControlTimeout = 5 * time.Second
	// execTeardownTimeout bounds the kill, the wait for the killed process
	// and the delete when a session ends.
	execTeardownTimeout = 10 * time.Second

	execIdentityContainer = "container"
	execIdentityRoot      = "root"
	// execExitUnknown is the audit exit code of a process whose exit status
	// could not be read.
	execExitUnknown = -1
)

// execStarter starts the process of a session against the given streams.
type execStarter func(spec ctr.ExecSpec, stdio ctr.ExecStdio) (ctr.ExecProcess, error)

// ExecContainer runs a new process in the target container (issue #1432).
// It validates the request, requires the container to exist and its task to
// be running, then returns one end of a socket pair; the session on the
// other end outlives this call and ends when the process exits, the caller
// closes its end, or ctx (the daemon's lifetime) is cancelled. In kukeond
// the RPC service passes the returned end back over the kukeond socket.
func (c *Client) ExecContainer(
	ctx context.Context,
	doc v1beta1.ContainerDoc,
	req kukeonv1.ExecRequest,
) (*net.UnixConn, error) {
	if err := validateExecRequest(req); err != nil {
		return nil, err
	}
	container, err := c.resolveContainer(doc)
	if err != nil {
		return nil, err
	}
	if err = guardExecTaskRunning(container); err != nil {
		return nil, err
	}
	execID, err := newExecID()
	if err != nil {
		return nil, err
	}
	callerEnd, daemonEnd, err := execSocketPair()
	if err != nil {
		return nil, err
	}

	session := &execSession{
		logger: c.ctrl.Logger(),
		conn:   daemonEnd,
		req:    req,
		spec: ctr.ExecSpec{
			ID:      execID,
			Command: req.Command,
			Root:    req.Root,
			User:    req.User,
			Env:     req.Env,
			Workdir: req.Workdir,
			TTY:     req.TTY,
		},
		start: func(spec ctr.ExecSpec, stdio ctr.ExecStdio) (ctr.ExecProcess, error) {
			return c.ctrl.ExecContainer(container, spec, stdio)
		},
		audit: execAuditAttrs(ctx, container.Spec, execID, req),
	}
	go session.run(ctx)
	return callerEnd, nil
}

// validateExecRequest refuses a request before anything is looked up.
func validateExecRequest(req kukeonv1.ExecRequest) error {
	switch {
	case len(req.Command) == 0 || req.Command[0] == "":
		return fmt.Errorf("%w: a command is required", errdefs.ErrExecInvalidRequest)
	case req.Root && req.User != "":
		return fmt.Errorf("%w: root and user are mutually exclusive", errdefs.ErrExecInvalidRequest)
	case !req.TTY && (req.Cols != 0 || req.Rows != 0):
		return fmt.Errorf("%w: a terminal size needs a TTY", errdefs.ErrExecInvalidRequest)
	}
	for i, entry := range req.Env {
		if key, _, ok := strings.Cut(entry, "="); !ok || key == "" {
			// The entry may be a mistyped secret: name its position, not it.
			return fmt.Errorf("%w: env entry %d is not KEY=VAL", errdefs.ErrExecInvalidRequest, i+1)
		}
	}
	return nil
}

// guardExecTaskRunning refuses a container whose task is not running: there
// is no task to add a process to.
func guardExecTaskRunning(container intmodel.Container) error {
	if container.Status.State == intmodel.ContainerStateReady {
		return nil
	}
	// ContainerState ordinals are kept in lockstep with the v1beta1 enum.
	state := v1beta1.ContainerState(container.Status.State)
	return fmt.Errorf(
		"%w: container %q in cell %q has state %s",
		errdefs.ErrExecTaskNotRunning,
		container.Spec.ID,
		container.Spec.CellName,
		state.String(),
	)
}

func newExecID() (string, error) {
	raw := make([]byte, execIDRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate exec id: %w", err)
	}
	return execIDPrefix + hex.EncodeToString(raw), nil
}

// execSocketPair returns both ends of a connected unix stream socket pair.
func execSocketPair() (*net.UnixConn, *net.UnixConn, error) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("exec socket pair: %w", err)
	}
	a, err := unixConnFromFD(fds[0])
	if err != nil {
		_ = unix.Close(fds[1])
		return nil, nil, err
	}
	b, err := unixConnFromFD(fds[1])
	if err != nil {
		_ = a.Close()
		return nil, nil, err
	}
	return a, b, nil
}

// unixConnFromFD wraps fd as a *net.UnixConn, taking ownership of fd.
func unixConnFromFD(fd int) (*net.UnixConn, error) {
	f := os.NewFile(uintptr(fd), "kuke-exec")
	defer func() { _ = f.Close() }() // FileConn dups; drop the original
	c, err := net.FileConn(f)
	if err != nil {
		return nil, fmt.Errorf("exec socket pair: %w", err)
	}
	uc, ok := c.(*net.UnixConn)
	if !ok {
		_ = c.Close()
		return nil, fmt.Errorf("exec socket pair: got %T, want a unix socket", c)
	}
	return uc, nil
}

// execAuditAttrs builds the fields both audit entries of an exec carry.
// Env values are never logged, only the keys; the caller uid is omitted when
// the call did not arrive over the kukeond socket.
func execAuditAttrs(ctx context.Context, spec intmodel.ContainerSpec, execID string, req kukeonv1.ExecRequest) []any {
	envKeys := make([]string, 0, len(req.Env))
	for _, entry := range req.Env {
		key, _, _ := strings.Cut(entry, "=")
		envKeys = append(envKeys, key)
	}
	identity := execIdentityContainer
	switch {
	case req.Root:
		identity = execIdentityRoot
	case req.User != "":
		identity = req.User
	}
	attrs := []any{
		"realm", spec.RealmName,
		"space", spec.SpaceName,
		"stack", spec.StackName,
		"cell", spec.CellName,
		"container", spec.ID,
		"execID", execID,
		"identity", identity,
		"root", req.Root || execUserIsRoot(req.User),
		"argv", req.Command,
		"envKeys", envKeys,
		"tty", req.TTY,
	}
	if uid, ok := kukeonv1.PeerUIDFrom(ctx); ok {
		attrs = append(attrs, "callerUID", uid)
	}
	return attrs
}

// execUserIsRoot reports whether an explicit user names uid 0: "0",
// "0:<gid>", or the name root.
func execUserIsRoot(user string) bool {
	uidPart, _, _ := strings.Cut(user, ":")
	if uidPart == execIdentityRoot {
		return true
	}
	uid, err := strconv.ParseUint(uidPart, 10, 32)
	return err == nil && uid == 0
}

// execSession bridges one exec process and the daemon end of its socket
// pair. Frames from the caller become stdin, stdin EOF and resizes; the
// process's output becomes stdout and stderr frames, followed by one exit
// frame.
type execSession struct {
	logger *slog.Logger
	conn   *net.UnixConn
	req    kukeonv1.ExecRequest
	spec   ctr.ExecSpec
	start  execStarter
	audit  []any
}

// execOutcome is what the finish audit entry reports.
type execOutcome struct {
	code         int
	disconnected bool
	err          error
}

type execWaitResult struct {
	code uint32
	err  error
}

// run serves the session and writes its two audit entries.
func (s *execSession) run(ctx context.Context) {
	started := time.Now()
	s.logger.InfoContext(ctx, "exec started", s.audit...)
	outcome := s.serve(ctx)
	attrs := append(slices.Clone(s.audit),
		"exitCode", outcome.code,
		"duration", time.Since(started).String(),
		"clientDisconnected", outcome.disconnected,
	)
	if outcome.err != nil {
		attrs = append(attrs, "error", outcome.err.Error())
	}
	s.logger.InfoContext(ctx, "exec finished", attrs...)
}

func (s *execSession) serve(ctx context.Context) execOutcome {
	// Teardown must run even when ctx (the daemon's lifetime) is done.
	detached := context.WithoutCancel(ctx)
	watcher := newHangupWatcher(s.conn)
	disconnected := make(chan struct{})
	var once sync.Once
	// disconnect closes the daemon end: it stops the read loop, fails any
	// output write still in flight and lets the caller see the end.
	disconnect := func() {
		once.Do(func() {
			close(disconnected)
			watcher.stop()
			_ = s.conn.Close()
		})
	}
	defer disconnect()

	out := &execOutput{w: execstream.NewWriter(s.conn)}
	stdio := ctr.ExecStdio{Stdout: out.stream(execstream.TypeStdout)}
	if !s.req.TTY {
		// A PTY carries both streams on stdout.
		stdio.Stderr = out.stream(execstream.TypeStderr)
	}
	var stdinR *io.PipeReader
	var stdinW *io.PipeWriter
	if s.req.Stdin {
		stdinR, stdinW = io.Pipe()
		stdio.Stdin = stdinR
	}

	process, err := s.start(s.spec, stdio)
	if err != nil {
		_ = out.exit(execstream.Exit{Code: 1, Error: err.Error()})
		return execOutcome{code: 1, err: err}
	}
	if s.req.TTY && s.req.Cols > 0 && s.req.Rows > 0 {
		s.control(detached, "resize", func(c context.Context) error {
			return process.Resize(c, uint32(s.req.Cols), uint32(s.req.Rows))
		})
	}

	waitCtx, cancelWait := context.WithCancel(detached)
	defer cancelWait()
	waited := make(chan execWaitResult, 1)
	go func() {
		code, waitErr := process.Wait(waitCtx)
		waited <- execWaitResult{code: code, err: waitErr}
	}()
	watcher.start(disconnect)
	go s.readLoop(detached, process, stdinW, disconnect)

	var outcome execOutcome
	select {
	case result := <-waited:
		outcome = s.finish(out, result)
	case <-disconnected:
		outcome = s.kill(detached, process, waited)
		outcome.disconnected = true
	case <-ctx.Done():
		disconnect()
		outcome = s.kill(detached, process, waited)
		if outcome.err == nil {
			outcome.err = fmt.Errorf("session stopped with kukeond: %w", ctx.Err())
		}
	}

	// Close the connection before the delete: a copier still blocked on a
	// caller that stopped reading must fail, not hold the delete.
	disconnect()
	if stdinR != nil {
		_ = stdinR.CloseWithError(io.ErrClosedPipe)
	}
	deleteCtx, cancelDelete := context.WithTimeout(detached, execTeardownTimeout)
	defer cancelDelete()
	if err = process.Delete(deleteCtx); err != nil {
		s.logger.WarnContext(ctx, "exec process could not be deleted", append(slices.Clone(s.audit), "error", err)...)
		if outcome.err == nil {
			outcome.err = err
		}
	}
	return outcome
}

// finish reports a process that exited on its own. The exit frame is the
// last one: output that arrives after it is dropped.
func (s *execSession) finish(out *execOutput, result execWaitResult) execOutcome {
	if result.err != nil {
		_ = out.exit(execstream.Exit{Code: 1, Error: result.err.Error()})
		return execOutcome{code: 1, err: result.err}
	}
	code := int(result.code)
	if err := out.exit(execstream.Exit{Code: code}); err != nil {
		// The caller went away between the exit and its report.
		return execOutcome{code: code, disconnected: true}
	}
	return execOutcome{code: code}
}

// kill ends a process whose caller is gone and waits for it to exit so it
// can be deleted. The connection is already closed.
func (s *execSession) kill(
	ctx context.Context,
	process ctr.ExecProcess,
	waited <-chan execWaitResult,
) execOutcome {
	killCtx, cancel := context.WithTimeout(ctx, execTeardownTimeout)
	defer cancel()
	outcome := execOutcome{code: execExitUnknown}
	if err := process.Kill(killCtx); err != nil {
		outcome.err = err
	}
	select {
	case result := <-waited:
		if result.err == nil {
			outcome.code = int(result.code)
		} else if outcome.err == nil {
			outcome.err = result.err
		}
	case <-killCtx.Done():
		if outcome.err == nil {
			outcome.err = errors.New("exec process did not exit after SIGKILL")
		}
	}
	return outcome
}

// readLoop applies the caller's frames until the connection ends, which it
// reports through disconnect. A stdin write blocks while the process is not
// reading; the hangup watcher covers a caller that leaves meanwhile.
func (s *execSession) readLoop(
	ctx context.Context,
	process ctr.ExecProcess,
	stdin *io.PipeWriter,
	disconnect func(),
) {
	defer disconnect()
	stdinOpen := stdin != nil
	for {
		typ, payload, err := execstream.ReadFrame(s.conn)
		if err != nil {
			return
		}
		switch typ {
		case execstream.TypeStdin:
			if stdinOpen {
				if _, writeErr := stdin.Write(payload); writeErr != nil {
					stdinOpen = false
				}
			}
		case execstream.TypeStdinEOF:
			if stdinOpen {
				stdinOpen = false
				_ = stdin.Close()
				s.control(ctx, "close stdin", process.CloseStdin)
			}
		case execstream.TypeResize:
			cols, rows, decodeErr := execstream.DecodeResize(payload)
			if decodeErr != nil || !s.req.TTY || cols == 0 || rows == 0 {
				continue
			}
			s.control(ctx, "resize", func(c context.Context) error {
				return process.Resize(c, uint32(cols), uint32(rows))
			})
		}
	}
}

// control runs one bounded containerd call for the session. A failure is
// logged and otherwise ignored: the process itself is unaffected.
func (s *execSession) control(ctx context.Context, what string, fn func(context.Context) error) {
	callCtx, cancel := context.WithTimeout(ctx, execControlTimeout)
	defer cancel()
	if err := fn(callCtx); err != nil {
		s.logger.DebugContext(ctx, "exec control call failed", "execID", s.spec.ID, "call", what, "error", err)
	}
}

// execOutput frames the process's output and guarantees that nothing
// follows the exit frame.
type execOutput struct {
	mu       sync.Mutex
	w        *execstream.Writer
	finished bool
}

func (o *execOutput) stream(typ byte) io.Writer {
	return execOutputStream{out: o, typ: typ}
}

func (o *execOutput) write(typ byte, p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.finished {
		return 0, io.ErrClosedPipe
	}
	if err := o.w.WriteData(typ, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o *execOutput) exit(exit execstream.Exit) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.finished = true
	return o.w.Exit(exit)
}

type execOutputStream struct {
	out *execOutput
	typ byte
}

func (s execOutputStream) Write(p []byte) (int, error) {
	return s.out.write(s.typ, p)
}

// hangupWatcher notices that the caller closed its end even while the read
// loop is not reading (blocked handing stdin to a process that does not
// consume it). It polls a duplicate of the descriptor, registered with the
// runtime poller on its own, so it never competes with the read loop.
type hangupWatcher struct {
	file *os.File
}

// newHangupWatcher duplicates conn's descriptor. A failure leaves a watcher
// that does nothing: the read loop still notices a hangup whenever it reads.
func newHangupWatcher(conn *net.UnixConn) *hangupWatcher {
	file, err := conn.File()
	if err != nil {
		return &hangupWatcher{}
	}
	return &hangupWatcher{file: file}
}

// start calls onHangup once the peer is gone. It returns without calling it
// when stop runs first.
func (w *hangupWatcher) start(onHangup func()) {
	if w.file == nil {
		return
	}
	raw, err := w.file.SyscallConn()
	if err != nil {
		return
	}
	go func() {
		hung := false
		_ = raw.Read(func(fd uintptr) bool {
			hung = peerHungUp(fd)
			return hung
		})
		if hung {
			onHangup()
		}
	}()
}

// stop ends the watch and releases the duplicate descriptor, without which
// the peer would never see the connection close.
func (w *hangupWatcher) stop() {
	if w.file != nil {
		_ = w.file.Close()
	}
}

// peerHungUp reports whether the peer of the socket fd closed its end.
func peerHungUp(fd uintptr) bool {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLRDHUP}}
	for {
		_, err := unix.Poll(fds, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return true
		}
		return fds[0].Revents&(unix.POLLRDHUP|unix.POLLHUP|unix.POLLERR|unix.POLLNVAL) != 0
	}
}
