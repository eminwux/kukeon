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

package exec_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	execcmd "github.com/eminwux/kukeon/cmd/kuke/exec"
	"github.com/eminwux/kukeon/cmd/kuke/internal/terminaltest"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/execstream"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
)

const testTimeout = 5 * time.Second

// daemon is the kukeond side of the command under test: it records what
// ExecContainer was asked and runs script on the daemon end of the session.
type daemon struct {
	*terminaltest.Client

	// script plays the daemon's end; nil reports exit 0 at once.
	script func(peer *net.UnixConn, req kukeonv1.ExecRequest)

	mu    sync.Mutex
	docs  []v1beta1.ContainerDoc
	reqs  []kukeonv1.ExecRequest
	lists int
	done  sync.WaitGroup
}

func newDaemon(t *testing.T, containers ...v1beta1.ContainerSpec) *daemon {
	t.Helper()
	if containers == nil {
		containers = []v1beta1.ContainerSpec{{ID: "root", Root: true}, {ID: "work"}}
	}
	d := &daemon{Client: terminaltest.NewClient()}
	d.ListContainersFn = func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
		d.mu.Lock()
		d.lists++
		d.mu.Unlock()
		return containers, nil
	}
	d.ExecContainerFn = func(doc v1beta1.ContainerDoc, req kukeonv1.ExecRequest) (*net.UnixConn, error) {
		d.mu.Lock()
		d.docs = append(d.docs, doc)
		d.reqs = append(d.reqs, req)
		d.mu.Unlock()
		conn, peer, err := terminaltest.SocketPair()
		if err != nil {
			return nil, err
		}
		d.done.Go(func() {
			defer func() { _ = peer.Close() }()
			_ = peer.SetDeadline(time.Now().Add(testTimeout))
			if d.script == nil {
				_ = execstream.NewWriter(peer).Exit(execstream.Exit{})
				return
			}
			d.script(peer, req)
		})
		return conn, nil
	}
	t.Cleanup(d.done.Wait)
	return d
}

func (d *daemon) calls() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.reqs)
}

func (d *daemon) only(t *testing.T) (v1beta1.ContainerDoc, kukeonv1.ExecRequest) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.reqs) != 1 {
		t.Fatalf("ExecContainer called %d times, want 1", len(d.reqs))
	}
	return d.docs[0], d.reqs[0]
}

type result struct {
	cmd    *cobra.Command
	stdout bytes.Buffer
	stderr bytes.Buffer
	err    error
}

// run executes `kuke exec` with args against d. stdin nil leaves the
// command's default stdin, which no test without -i or -t reads.
func run(t *testing.T, d *daemon, stdin io.Reader, args ...string) *result {
	t.Helper()
	t.Cleanup(viper.Reset)
	res := &result{cmd: execcmd.NewExecCmd()}
	res.cmd.SetContext(context.WithValue(context.Background(), execcmd.MockControllerKey{}, kukeonv1.Client(d)))
	res.cmd.SetOut(&res.stdout)
	res.cmd.SetErr(&res.stderr)
	if stdin != nil {
		res.cmd.SetIn(stdin)
	}
	res.cmd.SetArgs(args)
	res.err = res.cmd.Execute()
	return res
}

func TestExec_UsageErrors(t *testing.T) {
	cases := map[string][]string{
		"no dash":               {"c1", "id"},
		"no dash, command flag": {"c1", "id", "-u"},
		"no dash, unknown flag": {"c1", "ls", "--all"},
		"dash first":            {"--", "id"},
		"nothing after dash":    {"c1", "--"},
		"two cells before dash": {"c1", "c2", "--", "id"},
		"no arguments":          {},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			d := newDaemon(t)
			res := run(t, d, nil, args...)
			if res.err == nil || !strings.Contains(res.err.Error(), "kuke exec <cell> [flags] -- <command>") {
				t.Fatalf("err = %v, want a usage error showing the -- form", res.err)
			}
			if d.calls() != 0 {
				t.Errorf("ExecContainer called %d times for a usage error", d.calls())
			}
			if !strings.Contains(res.stderr.String(), "Error:") {
				t.Errorf("usage error was not printed: %q", res.stderr.String())
			}
		})
	}
}

func TestExec_RefusedBeforeAnyRPC(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"root with user":     {[]string{"c1", "--root", "-u", "1000", "--", "id"}, "root"},
		"env without equals": {[]string{"c1", "-e", "OK=1", "-e", "BROKEN", "--", "id"}, "--env value 2"},
		"env without a key":  {[]string{"c1", "-e", "=v", "--", "id"}, "--env value 1"},
		"tty without a terminal": {
			[]string{"c1", "-t", "--", "sh"}, "needs a terminal on stdin",
		},
		"empty realm": {[]string{"c1", "--realm", " ", "--", "id"}, "realm"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := newDaemon(t)
			d.GetCellFn = func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
				t.Error("GetCell called for a request that must be refused locally")
				return kukeonv1.GetCellResult{}, errors.New("unreachable")
			}
			res := run(t, d, strings.NewReader("not a terminal"), tc.args...)
			if res.err == nil || !strings.Contains(res.err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", res.err, tc.want)
			}
			if d.calls() != 0 {
				t.Errorf("ExecContainer called %d times", d.calls())
			}
		})
	}
}

func TestExec_FlagsMapToRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want kukeonv1.ExecRequest
	}{
		{
			"plain",
			[]string{"c1", "--", "id", "-u"},
			kukeonv1.ExecRequest{Command: []string{"id", "-u"}},
		},
		{
			"root",
			[]string{"c1", "--root", "--", "apt-get", "install", "-y", "curl"},
			kukeonv1.ExecRequest{Command: []string{"apt-get", "install", "-y", "curl"}, Root: true},
		},
		{
			"user, env twice, workdir, stdin",
			[]string{"c1", "-u", "1234:5678", "-e", "A=1", "-e", "B=x=y,z", "-w", "/tmp", "-i", "--", "cat"},
			kukeonv1.ExecRequest{
				Command: []string{"cat"}, User: "1234:5678", Env: []string{"A=1", "B=x=y,z"}, Workdir: "/tmp",
				Stdin: true,
			},
		},
		{
			"long flags",
			[]string{"c1", "--user", "agent", "--env", "A=", "--workdir", "/w", "--stdin", "--", "sh", "-c", "true"},
			kukeonv1.ExecRequest{
				Command: []string{"sh", "-c", "true"}, User: "agent", Env: []string{"A="}, Workdir: "/w", Stdin: true,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newDaemon(t)
			res := run(t, d, strings.NewReader(""), tc.args...)
			if res.err != nil {
				t.Fatalf("Execute: %v", res.err)
			}
			if _, req := d.only(t); !reflect.DeepEqual(req, tc.want) {
				t.Fatalf("request = %+v\nwant      %+v", req, tc.want)
			}
		})
	}
}

func TestExec_TargetAndContainerPicker(t *testing.T) {
	t.Run("the only non-root container, attachable or not", func(t *testing.T) {
		d := newDaemon(t)
		res := run(t, d, nil, "c1", "--realm", "r1", "--space", "s1", "--stack", "st1", "--", "id")
		if res.err != nil {
			t.Fatalf("Execute: %v", res.err)
		}
		doc, _ := d.only(t)
		want := v1beta1.ContainerSpec{ID: "work", RealmID: "r1", SpaceID: "s1", StackID: "st1", CellID: "c1"}
		if doc.Metadata.Name != "work" || !reflect.DeepEqual(doc.Spec, want) {
			t.Fatalf("target = %q %+v, want work in r1/s1/st1/c1", doc.Metadata.Name, doc.Spec)
		}
	})
	t.Run("default realm, space and stack", func(t *testing.T) {
		d := newDaemon(t)
		if res := run(t, d, nil, "c1", "--", "id"); res.err != nil {
			t.Fatalf("Execute: %v", res.err)
		}
		doc, _ := d.only(t)
		if doc.Spec.RealmID != "default" || doc.Spec.SpaceID != "default" || doc.Spec.StackID != "default" {
			t.Fatalf("target = %+v, want the default coordinates", doc.Spec)
		}
	})
	t.Run("two non-root containers", func(t *testing.T) {
		d := newDaemon(t, v1beta1.ContainerSpec{ID: "root", Root: true},
			v1beta1.ContainerSpec{ID: "web"}, v1beta1.ContainerSpec{ID: "db", Attachable: true})
		res := run(t, d, nil, "c1", "--", "id")
		if !errors.Is(res.err, errdefs.ErrExecAmbiguous) {
			t.Fatalf("err = %v, want ErrExecAmbiguous", res.err)
		}
		if msg := res.err.Error(); !strings.Contains(msg, "db, web") || strings.Contains(msg, "attachable") {
			t.Errorf("error %q must list db and web and not say attachable", msg)
		}
		if d.calls() != 0 {
			t.Error("ExecContainer called without a chosen container")
		}
	})
	t.Run("only the root container", func(t *testing.T) {
		d := newDaemon(t, v1beta1.ContainerSpec{ID: "root", Root: true})
		res := run(t, d, nil, "c1", "--", "id")
		if !errors.Is(res.err, errdefs.ErrExecNoCandidate) {
			t.Fatalf("err = %v, want ErrExecNoCandidate", res.err)
		}
		if strings.Contains(res.err.Error(), "attachable") {
			t.Errorf("error %q says attachable", res.err)
		}
	})
	t.Run("a named container skips the picker, the root one included", func(t *testing.T) {
		d := newDaemon(t, v1beta1.ContainerSpec{ID: "root", Root: true})
		if res := run(t, d, nil, "c1", "-c", "root", "--", "id"); res.err != nil {
			t.Fatalf("Execute: %v", res.err)
		}
		doc, _ := d.only(t)
		if doc.Metadata.Name != "root" || d.lists != 0 {
			t.Fatalf("target = %q after %d ListContainers calls, want root and no listing", doc.Metadata.Name, d.lists)
		}
	})
}

func TestExec_CellGuardMatchesAttach(t *testing.T) {
	cases := map[string]struct {
		cell kukeonv1.GetCellResult
		want string
	}{
		"missing cell": {kukeonv1.GetCellResult{}, "does not exist"},
		"stopped cell": {
			kukeonv1.GetCellResult{
				MetadataExists: true,
				Cell:           v1beta1.CellDoc{Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped}},
			},
			"start it first with `kuke start c1`",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := newDaemon(t)
			d.GetCellFn = func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) { return tc.cell, nil }
			res := run(t, d, nil, "c1", "--", "id")
			if res.err == nil || !strings.Contains(res.err.Error(), tc.want) {
				t.Fatalf("err = %v, want the attach guard's %q", res.err, tc.want)
			}
			if d.calls() != 0 || d.lists != 0 {
				t.Error("the picker or ExecContainer ran for a cell that is not live")
			}
		})
	}
}

func TestExec_DaemonRefusals(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"unknown container": {errdefs.ErrContainerNotFound, `container "nope" not found in cell "c1"`},
		"task not running":  {errdefs.ErrExecTaskNotRunning, "not running"},
		"outdated daemon":   {errdefs.ErrDaemonMethodUnsupported, "upgrade kukeond"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := newDaemon(t)
			d.ExecContainerFn = func(v1beta1.ContainerDoc, kukeonv1.ExecRequest) (*net.UnixConn, error) {
				return nil, tc.err
			}
			res := run(t, d, nil, "c1", "-c", "nope", "--", "id")
			if !errors.Is(res.err, tc.err) || !strings.Contains(res.err.Error(), tc.want) {
				t.Fatalf("err = %v, want %v mentioning %q", res.err, tc.err, tc.want)
			}
			var exit execcmd.ExitError
			if errors.As(res.err, &exit) {
				t.Errorf("a kuke failure came back as the process's exit code %d", exit.Code)
			}
		})
	}
}

// TestExec_StreamsAndExitCode pins the non-interactive contract: stdout and
// stderr stay apart, and a non-zero exit is ExitError with nothing printed.
// TestExec_UnknownContainerIsNamedOnce pins that the daemon's own wording,
// which already names the container and the cell, is not wrapped again.
func TestExec_UnknownContainerIsNamedOnce(t *testing.T) {
	daemonErr := fmt.Errorf("container %q not found in cell %q at run-path %q: %w",
		"nope", "c1", "/opt/kukeon", errdefs.ErrContainerNotFound)
	d := newDaemon(t)
	d.ExecContainerFn = func(v1beta1.ContainerDoc, kukeonv1.ExecRequest) (*net.UnixConn, error) {
		return nil, daemonErr
	}
	res := run(t, d, nil, "c1", "-c", "nope", "--", "id")
	if !errors.Is(res.err, errdefs.ErrContainerNotFound) || res.err.Error() != daemonErr.Error() {
		t.Fatalf("err = %q, want the daemon's message unchanged: %q", res.err, daemonErr)
	}
}

func TestExec_StreamsAndExitCode(t *testing.T) {
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		_ = w.WriteData(execstream.TypeStdout, []byte("to stdout\n"))
		_ = w.WriteData(execstream.TypeStderr, []byte("to stderr\n"))
		_ = w.WriteData(execstream.TypeStdout, []byte("more stdout\n"))
		_ = w.Exit(execstream.Exit{Code: 7})
	}
	res := run(t, d, nil, "c1", "--", "sh", "-c", "exit 7")

	var exit execcmd.ExitError
	if !errors.As(res.err, &exit) || exit.Code != 7 || exit.ExitCode() != 7 {
		t.Fatalf("err = %v, want ExitError{7}", res.err)
	}
	if got := res.stdout.String(); got != "to stdout\nmore stdout\n" {
		t.Errorf("stdout = %q", got)
	}
	if got := res.stderr.String(); got != "to stderr\n" {
		t.Errorf("stderr = %q, want only the process's stderr (nothing printed for the exit code)", got)
	}
}

func TestExec_ExitZero(t *testing.T) {
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		_ = w.WriteData(execstream.TypeStdout, []byte("1000\n"))
		_ = w.Exit(execstream.Exit{})
	}
	res := run(t, d, nil, "c1", "--", "id", "-u")
	if res.err != nil || res.stdout.String() != "1000\n" || res.stderr.Len() != 0 {
		t.Fatalf("err %v, stdout %q, stderr %q; want a clean exit printing 1000", res.err, &res.stdout, &res.stderr)
	}
}

func TestExec_ConnectionClosesWithoutExit(t *testing.T) {
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = execstream.NewWriter(peer).WriteData(execstream.TypeStdout, []byte("partial"))
	}
	res := run(t, d, nil, "c1", "--", "sleep", "600")
	if !errors.Is(res.err, errdefs.ErrExecSessionLost) {
		t.Fatalf("err = %v, want ErrExecSessionLost", res.err)
	}
	var exit execcmd.ExitError
	if errors.As(res.err, &exit) {
		t.Errorf("a lost session came back as exit code %d, want a plain failure (exit 1)", exit.Code)
	}
	if !strings.Contains(res.stderr.String(), "Error:") {
		t.Errorf("the failure was not printed: %q", res.stderr.String())
	}
}

func TestExec_StartFailureIsAKukeError(t *testing.T) {
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = execstream.NewWriter(peer).Exit(execstream.Exit{Code: 1, Error: `exec: "nope": not found`})
	}
	res := run(t, d, nil, "c1", "--", "nope")
	if !errors.Is(res.err, errdefs.ErrExecFailed) || !strings.Contains(res.err.Error(), `"nope": not found`) {
		t.Fatalf("err = %v, want ErrExecFailed carrying the daemon's reason", res.err)
	}
	var exit execcmd.ExitError
	if errors.As(res.err, &exit) {
		t.Error("a start failure must not be reported as the process's exit code")
	}
}

// TestExec_StdinIsFramedThenEnded pins -i: every byte of a stdin larger than
// one frame reaches the daemon, followed by the stdin EOF frame.
func TestExec_StdinIsFramedThenEnded(t *testing.T) {
	input := bytes.Repeat([]byte("0123456789abcdef"), 100*1024/16)
	var got []byte
	sawEOF := false
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		for {
			typ, payload, err := execstream.ReadFrame(peer)
			if err != nil {
				return
			}
			switch typ {
			case execstream.TypeStdin:
				if sawEOF {
					t.Error("stdin frame after the stdin EOF frame")
				}
				got = append(got, payload...)
			case execstream.TypeStdinEOF:
				sawEOF = true
				_ = w.WriteData(execstream.TypeStdout, []byte("hi\n"))
				_ = w.Exit(execstream.Exit{})
				return
			}
		}
	}
	res := run(t, d, bytes.NewReader(input), "c1", "-i", "--", "cat")
	if res.err != nil {
		t.Fatalf("Execute: %v", res.err)
	}
	d.done.Wait()
	if !sawEOF || !bytes.Equal(got, input) {
		t.Fatalf("daemon got %d stdin bytes (EOF frame: %t), want all %d and the EOF", len(got), sawEOF, len(input))
	}
	if res.stdout.String() != "hi\n" {
		t.Errorf("stdout = %q", res.stdout.String())
	}
}

// TestExec_WithoutStdinFlagSendsNoStdin pins that stdin is left alone
// without -i.
func TestExec_WithoutStdinFlagSendsNoStdin(t *testing.T) {
	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, _ kukeonv1.ExecRequest) {
		_ = peer.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		if typ, _, err := execstream.ReadFrame(peer); err == nil {
			t.Errorf("got a frame of type %d without -i", typ)
		}
		_ = execstream.NewWriter(peer).Exit(execstream.Exit{})
	}
	if res := run(t, d, strings.NewReader("must not be read"), "c1", "--", "true"); res.err != nil {
		t.Fatalf("Execute: %v", res.err)
	}
}

func echoIsOn(t *testing.T, tty *os.File) bool {
	t.Helper()
	termios, err := unix.IoctlGetTermios(int(tty.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatalf("read terminal state: %v", err)
	}
	return termios.Lflag&unix.ECHO != 0
}

// waitRaw waits for the command to put the terminal in raw mode. The fake
// daemon's script starts as soon as ExecContainer hands back the connection,
// which is just before the command switches the terminal.
func waitRaw(t *testing.T, tty *os.File) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for echoIsOn(t, tty) {
		if time.Now().After(deadline) {
			t.Error("the terminal is not raw while the session runs")
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// TestExec_InteractiveTerminal drives -it over a real pty: the initial size
// travels in the request, the terminal is raw while the session runs, a
// SIGWINCH becomes a resize frame, typed bytes become stdin frames without
// waiting for a newline, and the terminal is restored afterwards.
func TestExec_InteractiveTerminal(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() {
		_ = tty.Close()
		_ = ptmx.Close()
	})
	if err = pty.Setsize(ptmx, &pty.Winsize{Cols: 132, Rows: 43}); err != nil {
		t.Fatal(err)
	}
	if !echoIsOn(t, tty) {
		t.Fatal("a fresh pty should echo")
	}

	d := newDaemon(t)
	d.script = func(peer *net.UnixConn, req kukeonv1.ExecRequest) {
		w := execstream.NewWriter(peer)
		if !req.TTY || !req.Stdin || req.Cols != 132 || req.Rows != 43 {
			t.Errorf("request = %+v, want a 132x43 terminal with stdin", req)
		}
		waitRaw(t, tty)
		if sizeErr := pty.Setsize(ptmx, &pty.Winsize{Cols: 100, Rows: 50}); sizeErr != nil {
			t.Error(sizeErr)
		}
		// The test process does not own the pty, so the kernel sends it no
		// SIGWINCH; deliver one until the command's handler is listening.
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			for {
				_ = unix.Kill(os.Getpid(), unix.SIGWINCH)
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
		typ, payload, readErr := execstream.ReadFrame(peer)
		cols, rows, _ := execstream.DecodeResize(payload)
		if readErr != nil || typ != execstream.TypeResize || cols != 100 || rows != 50 {
			t.Errorf("frame = type %d %dx%d (%v), want a 100x50 resize", typ, cols, rows, readErr)
		}
		if _, readErr = ptmx.WriteString("x"); readErr != nil {
			t.Error(readErr)
		}
		for {
			typ, payload, readErr = execstream.ReadFrame(peer)
			if readErr != nil {
				t.Errorf("waiting for the typed byte: %v", readErr)
				break
			}
			if typ == execstream.TypeStdin {
				if string(payload) != "x" {
					t.Errorf("stdin frame = %q, want the single typed byte", payload)
				}
				break
			}
		}
		_ = w.WriteData(execstream.TypeStdout, []byte("$ "))
		_ = w.Exit(execstream.Exit{Code: 3})
	}

	res := run(t, d, tty, "c1", "-it", "--", "bash")
	var exit execcmd.ExitError
	if !errors.As(res.err, &exit) || exit.Code != 3 {
		t.Fatalf("err = %v, want the shell's exit code 3", res.err)
	}
	if res.stdout.String() != "$ " {
		t.Errorf("stdout = %q", res.stdout.String())
	}
	if !echoIsOn(t, tty) {
		t.Error("the terminal was not restored after the session")
	}
}

// TestExec_InteractiveTerminalRestoredOnDrop pins that a connection that
// drops mid-session still restores the terminal.
func TestExec_InteractiveTerminalRestoredOnDrop(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	t.Cleanup(func() {
		_ = tty.Close()
		_ = ptmx.Close()
	})
	d := newDaemon(t)
	d.script = func(*net.UnixConn, kukeonv1.ExecRequest) {
		waitRaw(t, tty)
	}
	res := run(t, d, tty, "c1", "-it", "--", "bash")
	if !errors.Is(res.err, errdefs.ErrExecSessionLost) {
		t.Fatalf("err = %v, want ErrExecSessionLost", res.err)
	}
	if !echoIsOn(t, tty) {
		t.Error("the terminal was not restored after the connection dropped")
	}
}
