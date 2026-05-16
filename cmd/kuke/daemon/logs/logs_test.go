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
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	logscmd "github.com/eminwux/kukeon/cmd/kuke/daemon/logs"
	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TestMain mocks the shared euid lookup to euid=0 for every test in this
// package so the fail-fast root gate in runLogs does not short-circuit the
// existing fakes-driven coverage when CI runs as a non-root user
// (ubuntu-latest defaults to UID 1001). The dedicated non-root case overrides
// this with its own SetGeteuidForTesting call.
func TestMain(m *testing.M) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 0 })
	code := m.Run()
	restore()
	os.Exit(code)
}

const kukeondHostLogPath = "/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/log"

type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn      func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	logContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) LogContainer(
	_ context.Context, doc v1beta1.ContainerDoc,
) (kukeonv1.LogContainerResult, error) {
	if f.logContainerFn == nil {
		return kukeonv1.LogContainerResult{}, errors.New("unexpected LogContainer call")
	}
	return f.logContainerFn(doc)
}

type tailCapture struct {
	calls            int
	path             string
	follow           bool
	payload          []byte
	err              error
	blockUntilCancel bool
}

func (t *tailCapture) fn(ctx context.Context, path string, out io.Writer, follow bool) error {
	t.calls++
	t.path = path
	t.follow = follow
	if t.payload != nil {
		if _, err := out.Write(t.payload); err != nil {
			return err
		}
	}
	if t.err != nil {
		return t.err
	}
	if t.blockUntilCancel {
		<-ctx.Done()
	}
	return nil
}

func newCmd(t *testing.T, fc *fakeClient, tail *tailCapture) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	cmd := logscmd.NewLogsCmd()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	if fc != nil {
		ctx = context.WithValue(ctx, logscmd.MockClientKey{}, kukeonv1.Client(fc))
	}
	if tail != nil {
		ctx = context.WithValue(ctx, logscmd.MockTailKey{}, logcmd.TailFn(tail.fn))
	}
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}

func readyCell() v1beta1.CellDoc {
	return v1beta1.CellDoc{
		Status: v1beta1.CellStatus{
			State: v1beta1.CellStateReady,
			Containers: []v1beta1.ContainerStatus{
				{State: v1beta1.ContainerStateReady},
			},
		},
	}
}

func TestDaemonLogs_DefaultDumpsAndExits(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			assertKukeondCell(t, doc)
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			assertKukeondContainer(t, doc)
			return kukeonv1.LogContainerResult{HostLogPath: kukeondHostLogPath}, nil
		},
	}
	tail := &tailCapture{payload: []byte("daemon-line\n")}
	cmd, out := newCmd(t, fc, tail)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want nil", err)
	}
	if tail.calls != 1 {
		t.Fatalf("tail calls = %d, want 1", tail.calls)
	}
	if tail.follow {
		t.Errorf("follow = true, want false (default dump-and-exit)")
	}
	if tail.path != kukeondHostLogPath {
		t.Errorf("tail path = %q, want %q", tail.path, kukeondHostLogPath)
	}
	if got := out.String(); got != "daemon-line\n" {
		t.Errorf("stdout = %q, want %q", got, "daemon-line\n")
	}
}

func TestDaemonLogs_FollowCancelsCleanlyOnCtxDone(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostLogPath: kukeondHostLogPath}, nil
		},
	}
	tail := &tailCapture{blockUntilCancel: true, payload: []byte("seed")}
	cmd, out := newCmd(t, fc, tail)
	cmd.SetArgs([]string{"--follow"})
	ctx, cancel := context.WithCancel(cmd.Context())
	cmd.SetContext(ctx)

	var wg sync.WaitGroup
	wg.Add(1)
	var execErr error
	go func() {
		defer wg.Done()
		execErr = cmd.Execute()
	}()

	// Give the goroutine a beat to enter tail.fn before cancelling so we
	// exercise the ctx.Done() path rather than the pre-cancelled fast exit.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	if execErr != nil {
		t.Fatalf("Execute returned %v on ctx cancel, want nil", execErr)
	}
	if !tail.follow {
		t.Errorf("follow = false, want true")
	}
	if got := out.String(); got != "seed" {
		t.Errorf("stdout = %q, want %q", got, "seed")
	}
}

func TestDaemonLogs_HostNotInitialized(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{MetadataExists: false}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			t.Fatalf("LogContainer must not be called when host is uninitialized")
			return kukeonv1.LogContainerResult{}, nil
		},
	}
	cmd, _ := newCmd(t, fc, &tailCapture{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want host-not-initialized error")
	}
	for _, want := range []string{"not initialized", "kuke init"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDaemonLogs_CellNotRunning_PointsAtDaemonStart(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
				},
				MetadataExists: true,
			}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			t.Fatalf("LogContainer must not be called when cell is not running")
			return kukeonv1.LogContainerResult{}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmd(t, fc, tail)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want cell-not-running error")
	}
	for _, want := range []string{"kukeond is not running", "kuke daemon start", "kuke status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
	if tail.calls != 0 {
		t.Errorf("tail called %d times when cell is not running, want 0", tail.calls)
	}
}

func TestDaemonLogs_GetCellError_IsWrapped(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{}, errors.New("io: read failed")
		},
	}
	cmd, _ := newCmd(t, fc, &tailCapture{})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "inspect kukeond cell:") {
		t.Fatalf("want wrapped inspect error, got %v", err)
	}
}

func TestDaemonLogs_ContainerNotFound_NamedTarget(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{}, errdefs.ErrContainerNotFound
		},
	}
	cmd, _ := newCmd(t, fc, &tailCapture{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want container-not-found error")
	}
	for _, want := range []string{consts.KukeSystemContainerName, consts.KukeSystemCellName, "not found"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

// TestDaemonLogs_ContainerNotFound_SurfacesSentinel locks in the
// ErrContainerNotFound branch's %w wrap: when LogContainer's RPC reports
// the kukeond container doesn't exist, `kuke daemon logs` must propagate
// the sentinel so upstream callers can still errors.Is it. Distinct from
// TestDaemonLogs_ContainerNotFound_NamedTarget above, which only asserts
// the human-readable message — they share the same fake but exercise the
// two contracts (message + sentinel) independently.
func TestDaemonLogs_ContainerNotFound_SurfacesSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{}, errdefs.ErrContainerNotFound
		},
	}
	cmd, _ := newCmd(t, fc, &tailCapture{})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrContainerNotFound) {
		t.Fatalf("error %v does not unwrap to ErrContainerNotFound", err)
	}
}

func TestDaemonLogs_MissingLogFile_NamesTheCause(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostLogPath: kukeondHostLogPath}, nil
		},
	}
	tail := &tailCapture{err: os.ErrNotExist}
	cmd, _ := newCmd(t, fc, tail)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute returned nil, want missing-log-file error")
	}
	for _, want := range []string{kukeondHostLogPath, "runtime shim"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDaemonLogs_EmptyLogPath_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmd(t, fc, tail)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no log path") {
		t.Fatalf("want no-log-path error, got %v", err)
	}
	if tail.calls != 0 {
		t.Errorf("tail called %d times with empty path, want 0", tail.calls)
	}
}

func TestDaemonLogs_CapturePathFallback(t *testing.T) {
	t.Cleanup(viper.Reset)

	const capturePath = "/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/work/tty/capture"
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{Cell: readyCell(), MetadataExists: true}, nil
		},
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostCapturePath: capturePath}, nil
		},
	}
	tail := &tailCapture{payload: []byte("from-capture")}
	cmd, out := newCmd(t, fc, tail)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v, want nil", err)
	}
	if tail.path != capturePath {
		t.Errorf("tail path = %q, want %q", tail.path, capturePath)
	}
	if got := out.String(); got != "from-capture" {
		t.Errorf("stdout = %q, want %q", got, "from-capture")
	}
}

// TestDaemonLogs_NonRootIsRejected confirms the fail-fast UID gate rejects
// non-root invocations before any side effect (cell lookup, in-process
// controller construction). Symmetric with the same guard on `kuke daemon
// reset` and the rest of the daemon-lifecycle verbs (#463).
func TestDaemonLogs_NonRootIsRejected(t *testing.T) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)
	viper.Reset()

	cmd := logscmd.NewLogsCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd.SetContext(context.WithValue(context.Background(), types.CtxLogger, logger))
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("kuke daemon logs returned nil under euid=1000, want ErrMustRunAsRoot")
	}
	if !errors.Is(err, errdefs.ErrMustRunAsRoot) {
		t.Fatalf("kuke daemon logs error does not wrap ErrMustRunAsRoot: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke daemon logs") {
		t.Errorf("error does not name the subcommand: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not suggest sudo: %v", err)
	}
}

func TestDaemonLogs_LoggerMissingFromContext(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := logscmd.NewLogsCmd()
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "logger") {
		t.Fatalf("expected logger-missing error, got %v", err)
	}
}

func assertKukeondCell(t *testing.T, doc v1beta1.CellDoc) {
	t.Helper()
	if doc.Metadata.Name != consts.KukeSystemCellName {
		t.Errorf("cell name: want %q, got %q", consts.KukeSystemCellName, doc.Metadata.Name)
	}
	if doc.Spec.RealmID != consts.KukeSystemRealmName {
		t.Errorf("realm: want %q, got %q", consts.KukeSystemRealmName, doc.Spec.RealmID)
	}
	if doc.Spec.SpaceID != consts.KukeSystemSpaceName {
		t.Errorf("space: want %q, got %q", consts.KukeSystemSpaceName, doc.Spec.SpaceID)
	}
	if doc.Spec.StackID != consts.KukeSystemStackName {
		t.Errorf("stack: want %q, got %q", consts.KukeSystemStackName, doc.Spec.StackID)
	}
}

func assertKukeondContainer(t *testing.T, doc v1beta1.ContainerDoc) {
	t.Helper()
	if doc.Metadata.Name != consts.KukeSystemContainerName {
		t.Errorf("container name: want %q, got %q", consts.KukeSystemContainerName, doc.Metadata.Name)
	}
	if doc.Spec.CellID != consts.KukeSystemCellName {
		t.Errorf("cell: want %q, got %q", consts.KukeSystemCellName, doc.Spec.CellID)
	}
	if doc.Spec.RealmID != consts.KukeSystemRealmName {
		t.Errorf("realm: want %q, got %q", consts.KukeSystemRealmName, doc.Spec.RealmID)
	}
}
