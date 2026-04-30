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

package log_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	logcmd "github.com/eminwux/kukeon/cmd/kuke/log"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const testHostCapture = "/opt/kukeon/r1/s1/st1/c1/work/tty/capture"

type fakeClient struct {
	kukeonv1.FakeClient

	listContainersFn func(realm, space, stack, cell string) ([]v1beta1.ContainerSpec, error)
	logContainerFn   func(doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error)
}

func (f *fakeClient) ListContainers(
	_ context.Context,
	realm, space, stack, cell string,
) ([]v1beta1.ContainerSpec, error) {
	if f.listContainersFn == nil {
		return nil, errors.New("unexpected ListContainers call")
	}
	return f.listContainersFn(realm, space, stack, cell)
}

func (f *fakeClient) LogContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.LogContainerResult, error) {
	if f.logContainerFn == nil {
		return kukeonv1.LogContainerResult{}, errors.New("unexpected LogContainer call")
	}
	return f.logContainerFn(doc)
}

// tailCapture records the (path, noFollow) it was called with and copies
// configured bytes to the writer so callers can assert on stdout.
type tailCapture struct {
	calls    int
	path     string
	noFollow bool
	payload  []byte
	err      error
	// blockUntilCancel makes the tail block on ctx.Done() before
	// returning, modeling the real follow loop. When false the call
	// returns immediately after writing payload (modeling --no-follow).
	blockUntilCancel bool
}

func (t *tailCapture) fn(ctx context.Context, path string, out io.Writer, noFollow bool) error {
	t.calls++
	t.path = path
	t.noFollow = noFollow
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

func newCmdWithCtx(t *testing.T, fc *fakeClient, tail *tailCapture) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	cmd := logcmd.NewLogCmd()
	ctx := context.Background()
	if fc != nil {
		ctx = context.WithValue(ctx, logcmd.MockControllerKey{}, kukeonv1.Client(fc))
	}
	if tail != nil {
		ctx = context.WithValue(ctx, logcmd.MockTailKey{}, logcmd.TailFn(tail.fn))
	}
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd, out
}

func TestLog_NoFollow_DumpsCaptureAndExits(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		logContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			if got := doc.Metadata.Name; got != "work" {
				t.Errorf("LogContainer called with name %q, want %q", got, "work")
			}
			return kukeonv1.LogContainerResult{HostCapturePath: testHostCapture}, nil
		},
	}
	tail := &tailCapture{payload: []byte("captured-bytes")}
	cmd, out := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work", "--no-follow",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if tail.calls != 1 {
		t.Fatalf("tail called %d times, want 1", tail.calls)
	}
	if !tail.noFollow {
		t.Errorf("noFollow flag = false, want true")
	}
	if tail.path != testHostCapture {
		t.Errorf("tail path = %q, want %q", tail.path, testHostCapture)
	}
	if got := out.String(); got != "captured-bytes" {
		t.Errorf("stdout = %q, want %q", got, "captured-bytes")
	}
}

func TestLog_Follow_CancelsCleanlyOnCtxDone(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostCapturePath: testHostCapture}, nil
		},
	}
	tail := &tailCapture{blockUntilCancel: true, payload: []byte("seed")}

	cmd, out := newCmdWithCtx(t, fc, tail)
	// Override context with one we can cancel to model SIGINT delivery.
	ctx, cancel := context.WithCancel(cmd.Context())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work",
	})

	var wg sync.WaitGroup
	wg.Add(1)
	var execErr error
	go func() {
		defer wg.Done()
		execErr = cmd.Execute()
	}()

	// Give the goroutine a beat to enter tail.fn before cancelling, so
	// we exercise the ctx.Done() path in the follow loop rather than
	// the pre-cancelled fast exit.
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()

	if execErr != nil {
		t.Fatalf("Execute returned %v on context cancel, want nil", execErr)
	}
	if tail.calls != 1 {
		t.Fatalf("tail called %d times, want 1", tail.calls)
	}
	if tail.noFollow {
		t.Errorf("noFollow flag = true, want false (follow mode)")
	}
	if got := out.String(); got != "seed" {
		t.Errorf("stdout = %q, want %q", got, "seed")
	}
}

func TestLog_MissingCaptureFile_WrapsWithCellAndContainer(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostCapturePath: testHostCapture}, nil
		},
	}
	tail := &tailCapture{err: os.ErrNotExist}
	cmd, _ := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work", "--no-follow",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute returned nil, want missing-capture error")
	}
	msg := err.Error()
	for _, want := range []string{"c1", "work", testHostCapture} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not contain %q", msg, want)
		}
	}
}

func TestLog_AmbiguousCandidates_ErrorsWithList(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "shell", Attachable: true},
				{ID: "claude", Attachable: true},
			}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachAmbiguous) {
		t.Fatalf("error %v does not unwrap to ErrAttachAmbiguous", err)
	}
	if got := err.Error(); !strings.Contains(got, "claude, shell") {
		t.Errorf("error message %q missing sorted candidate list", got)
	}
	if tail.calls != 0 {
		t.Errorf("tail called %d times on ambiguous picker, want 0", tail.calls)
	}
}

func TestLog_NoCandidate_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
			}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("error %v does not unwrap to ErrAttachNoCandidate", err)
	}
	if tail.calls != 0 {
		t.Errorf("tail called %d times on empty picker, want 0", tail.calls)
	}
}

// TestLog_NonAttachable_TailsHostLogPath locks in the issue-#203 contract:
// a non-Attachable container is not gated out — the daemon returns
// HostLogPath (cio.LogFile) and `kuke log` tails it the same way it tails
// the sbsh capture file for Attachable containers.
func TestLog_NonAttachable_TailsHostLogPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	const wantPath = "/opt/kukeon/r1/s1/st1/c1/side/log"
	fc := &fakeClient{
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostLogPath: wantPath}, nil
		},
	}
	tail := &tailCapture{payload: []byte("daemon-stderr-line\n")}
	cmd, out := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "side", "--no-follow",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if tail.calls != 1 {
		t.Fatalf("tail called %d times, want 1", tail.calls)
	}
	if tail.path != wantPath {
		t.Errorf("tail path = %q, want %q", tail.path, wantPath)
	}
	if got := out.String(); got != "daemon-stderr-line\n" {
		t.Errorf("stdout = %q, want %q", got, "daemon-stderr-line\n")
	}
}

// TestLog_AmbiguousAcceptsNonAttachable checks the picker fallout from
// the Attachable filter being dropped (issue #203): a cell with two
// non-root containers — one Attachable, one not — must surface as
// ambiguous because both are now valid log targets.
func TestLog_AmbiguousAcceptsNonAttachable(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				{ID: "shell", Attachable: true},
				{ID: "daemon"},
			}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachAmbiguous) {
		t.Fatalf("error %v does not unwrap to ErrAttachAmbiguous", err)
	}
	if got := err.Error(); !strings.Contains(got, "daemon, shell") {
		t.Errorf("error message %q missing both candidates", got)
	}
}

// TestLog_AutoPicksLoneNonAttachable checks the kukeond-cell shape:
// a cell whose only non-root container is non-Attachable resolves to
// that container without requiring --container on the command line.
func TestLog_AutoPicksLoneNonAttachable(t *testing.T) {
	t.Cleanup(viper.Reset)

	const wantPath = "/opt/kukeon/kuke-system/kukeon/kukeon/kukeond/kukeond/log"
	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "kukeon-system-root", Root: true},
				{ID: "kukeond"},
			}, nil
		},
		logContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			if got := doc.Metadata.Name; got != "kukeond" {
				t.Errorf("LogContainer called with name %q, want %q", got, "kukeond")
			}
			return kukeonv1.LogContainerResult{HostLogPath: wantPath}, nil
		},
	}
	tail := &tailCapture{}
	cmd, _ := newCmdWithCtx(t, fc, tail)
	cmd.SetArgs([]string{
		"--realm", "kuke-system", "--space", "kukeon", "--stack", "kukeon", "--cell", "kukeond",
		"--no-follow",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if tail.path != wantPath {
		t.Errorf("tail path = %q, want %q", tail.path, wantPath)
	}
}

func TestLog_MissingFlags(t *testing.T) {
	t.Cleanup(viper.Reset)

	cases := []struct {
		name    string
		args    []string
		wantErr error
	}{
		{
			name:    "missing realm",
			args:    []string{"--space", "s1", "--stack", "st1", "--cell", "c1"},
			wantErr: errdefs.ErrRealmNameRequired,
		},
		{
			name:    "missing space",
			args:    []string{"--realm", "r1", "--stack", "st1", "--cell", "c1"},
			wantErr: errdefs.ErrSpaceNameRequired,
		},
		{
			name:    "missing stack",
			args:    []string{"--realm", "r1", "--space", "s1", "--cell", "c1"},
			wantErr: errdefs.ErrStackNameRequired,
		},
		{
			name:    "missing cell",
			args:    []string{"--realm", "r1", "--space", "s1", "--stack", "st1"},
			wantErr: errdefs.ErrCellNameRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			viper.Reset()
			t.Setenv("KUKE_LOG_REALM", "")
			t.Setenv("KUKE_LOG_SPACE", "")
			t.Setenv("KUKE_LOG_STACK", "")
			t.Setenv("KUKE_LOG_CELL", "")

			cmd, _ := newCmdWithCtx(t, &fakeClient{}, &tailCapture{})
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v does not unwrap to %v", err, tc.wantErr)
			}
		})
	}
}

// TestTailFile_NoFollow_DumpsAndReturns exercises the real tailFile
// (not the mock) to lock in the dump-and-exit semantics required by the
// --no-follow path.
func TestTailFile_NoFollow_DumpsAndReturns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "capture")
	want := "hello sbsh\n"
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("seed capture: %v", err)
	}

	out := &bytes.Buffer{}
	cmd := logcmd.NewLogCmd()
	fc := &fakeClient{
		logContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.LogContainerResult, error) {
			return kukeonv1.LogContainerResult{HostCapturePath: path}, nil
		},
	}
	ctx := context.WithValue(context.Background(), logcmd.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work", "--no-follow",
	})
	t.Cleanup(viper.Reset)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got := out.String(); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}
