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

package attach_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"

	attachcmd "github.com/eminwux/kukeon/cmd/kuke/attach"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshattach "github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
)

// socketPair returns both ends of a connected unix stream socketpair, the
// shape of connection ConnectContainerTerminal hands back.
func socketPair() (*net.UnixConn, *net.UnixConn) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		panic(fmt.Sprintf("socketpair: %v", err))
	}
	wrap := func(fd int) *net.UnixConn {
		f := os.NewFile(uintptr(fd), "socketpair")
		defer func() { _ = f.Close() }()
		c, connErr := net.FileConn(f)
		if connErr != nil {
			panic(fmt.Sprintf("FileConn: %v", connErr))
		}
		return c.(*net.UnixConn)
	}
	return wrap(fds[0]), wrap(fds[1])
}

// Close releases the peer ends the fake handed out.
func (f *fakeClient) Close() error {
	for _, c := range f.conns {
		_ = c.Close()
	}
	return nil
}

// assertDaemonPassedRoute pins that the attach loop dials through kukeond
// (Options.Dial) and never by path.
func assertDaemonPassedRoute(t *testing.T, opts sbshattach.Options) {
	t.Helper()
	if opts.Dial == nil {
		t.Errorf("Options.Dial is nil, want the daemon-passed dialer")
	}
	if opts.SocketPath != "" {
		t.Errorf("Options.SocketPath = %q, want empty (Dial and SocketPath are exclusive)", opts.SocketPath)
	}
}

func singleWorkContainer(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
	return []v1beta1.ContainerSpec{
		{ID: "root", Root: true},
		{ID: "work", Attachable: true},
	}, nil
}

// TestAttach_DialReusesPreflightThenConnectsPerCall pins the dial contract:
// the preflight connection serves sbsh's first dial (no extra round trip),
// and every later dial asks the daemon for a fresh connection.
func TestAttach_DialReusesPreflightThenConnectsPerCall(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: singleWorkContainer,
		connectFn:        func(v1beta1.ContainerDoc) error { return nil },
	}
	var dialed []net.Conn
	runFn := func(ctx context.Context, opts sbshattach.Options) error {
		assertDaemonPassedRoute(t, opts)
		if fc.connectCalls != 1 {
			t.Errorf("ConnectContainerTerminal calls before the loop = %d, want 1 (preflight)", fc.connectCalls)
		}
		for range 3 {
			c, err := opts.Dial(ctx)
			if err != nil {
				return err
			}
			dialed = append(dialed, c)
		}
		return nil
	}
	cmd := newCmdWithRun(t, fc, runFn)
	cmd.SetArgs([]string{"c1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if fc.connectCalls != 3 {
		t.Errorf("ConnectContainerTerminal calls = %d, want 3 (preflight reused for the first dial)", fc.connectCalls)
	}
	for i, c := range dialed {
		if _, ok := c.(*net.UnixConn); !ok {
			t.Errorf("dial %d returned %T, want *net.UnixConn", i, c)
		}
		_ = c.Close()
	}
}

// TestAttach_DialError_ReturnsUntypedNil guards the typed-nil trap: a failed
// dial must hand sbsh a nil interface, not a nil *net.UnixConn.
func TestAttach_DialError_ReturnsUntypedNil(t *testing.T) {
	t.Cleanup(viper.Reset)

	refused := errors.New("refused")
	calls := 0
	fc := &fakeClient{
		listContainersFn: singleWorkContainer,
		connectFn: func(v1beta1.ContainerDoc) error {
			calls++
			if calls == 1 {
				return nil
			}
			return refused
		},
	}
	runFn := func(ctx context.Context, opts sbshattach.Options) error {
		first, err := opts.Dial(ctx)
		if err != nil {
			t.Fatalf("first dial: %v", err)
		}
		_ = first.Close()
		c, err := opts.Dial(ctx)
		if !errors.Is(err, refused) {
			t.Errorf("second dial err = %v, want %v", err, refused)
		}
		if c != nil {
			t.Errorf("second dial conn = %#v, want an untyped nil", c)
		}
		return nil
	}
	cmd := newCmdWithRun(t, fc, runFn)
	cmd.SetArgs([]string{"c1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
}

// TestAttach_TaskNotRunning_FailsBeforeLoop pins that the daemon's
// not-Running refusal comes back from the preflight, before the attach
// loop takes over the terminal.
func TestAttach_TaskNotRunning_FailsBeforeLoop(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: singleWorkContainer,
		connectFn: func(v1beta1.ContainerDoc) error {
			return fmt.Errorf("%w: container \"work\" has state Stopped", errdefs.ErrAttachTaskNotRunning)
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{"c1"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachTaskNotRunning) {
		t.Fatalf("err = %v, want ErrAttachTaskNotRunning", err)
	}
	if run.calls != 0 {
		t.Errorf("attach.Run called %d times, want 0", run.calls)
	}
}

// TestAttach_OlderDaemon_FallsBackToSocketPath pins the version-skew
// fallback: a kukeond without ConnectContainerTerminal still serves
// AttachContainer, so kuke on the host keeps attaching by path.
func TestAttach_OlderDaemon_FallsBackToSocketPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: singleWorkContainer,
		connectFn: func(v1beta1.ContainerDoc) error {
			return fmt.Errorf("%w: KukeonV1.ConnectContainerTerminal", errdefs.ErrDaemonMethodUnsupported)
		},
		attachContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			if doc.Metadata.Name != "work" {
				t.Errorf("AttachContainer name = %q, want work", doc.Metadata.Name)
			}
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{"c1"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if run.opts.SocketPath != testHostSocket {
		t.Errorf("Options.SocketPath = %q, want %q", run.opts.SocketPath, testHostSocket)
	}
	if run.opts.Dial != nil {
		t.Errorf("Options.Dial is set on the fallback route, want nil")
	}
}

// newCmdWithRun is newCmdWithCtx for tests that need a custom attach loop.
func newCmdWithRun(
	t *testing.T,
	fc *fakeClient,
	runFn func(ctx context.Context, opts sbshattach.Options) error,
) *cobra.Command {
	t.Helper()
	cmd := attachcmd.NewAttachCmd()
	ctx := context.WithValue(context.Background(), attachcmd.MockControllerKey{}, kukeonv1.Client(fc))
	ctx = context.WithValue(ctx, attachcmd.MockRunKey{}, attachcmd.RunFn(runFn))
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd
}
