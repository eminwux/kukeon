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
	"strings"
	"testing"

	attachcmd "github.com/eminwux/kukeon/cmd/kuke/attach"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshattach "github.com/eminwux/sbsh/pkg/attach"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	testHostSocket = "/opt/kukeon/r1/s1/st1/c1/work/tty/socket"
)

type fakeClient struct {
	kukeonv1.FakeClient

	listContainersFn  func(realm, space, stack, cell string) ([]v1beta1.ContainerSpec, error)
	attachContainerFn func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error)
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

func (f *fakeClient) AttachContainer(
	_ context.Context,
	doc v1beta1.ContainerDoc,
) (kukeonv1.AttachContainerResult, error) {
	if f.attachContainerFn == nil {
		return kukeonv1.AttachContainerResult{}, errors.New("unexpected AttachContainer call")
	}
	return f.attachContainerFn(doc)
}

// runCapture records the Options passed to pkg/attach.Run and returns
// nil so the test treats the call as a clean detach.
type runCapture struct {
	calls int
	opts  sbshattach.Options
}

func (r *runCapture) fn(_ context.Context, opts sbshattach.Options) error {
	r.calls++
	r.opts = opts
	return nil
}

// runReturning is the mirror of runCapture for tests that want pkg/attach.Run
// to return a specific error (rather than the clean-detach default).
type runReturning struct {
	calls int
	err   error
}

func (r *runReturning) fn(_ context.Context, _ sbshattach.Options) error {
	r.calls++
	return r.err
}

func newCmdWithCtx(t *testing.T, fc *fakeClient, run *runCapture) *cobra.Command {
	t.Helper()

	cmd := attachcmd.NewAttachCmd()
	ctx := context.Background()
	if fc != nil {
		ctx = context.WithValue(ctx, attachcmd.MockControllerKey{}, kukeonv1.Client(fc))
	}
	if run != nil {
		ctx = context.WithValue(ctx, attachcmd.MockRunKey{}, attachcmd.RunFn(run.fn))
	}
	cmd.SetContext(ctx)
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	return cmd
}

func TestAttach_SingleNonRootAttachable_Succeeds(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Attachable: false},
				{ID: "work", Attachable: true},
			}, nil
		},
		attachContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			if got := doc.Metadata.Name; got != "work" {
				t.Errorf("AttachContainer called with name %q, want %q", got, "work")
			}
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if run.calls != 1 {
		t.Fatalf("attach.Run called %d times, want 1", run.calls)
	}
	if run.opts.SocketPath != testHostSocket {
		t.Errorf("Options.SocketPath = %q, want %q", run.opts.SocketPath, testHostSocket)
	}
}

func TestAttach_AmbiguousCandidates_ErrorsWithList(t *testing.T) {
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
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute returned nil, want ErrAttachAmbiguous")
	}
	if !errors.Is(err, errdefs.ErrAttachAmbiguous) {
		t.Fatalf("error %v does not unwrap to ErrAttachAmbiguous", err)
	}
	// The candidate list must be sorted and present in the error message so
	// the operator can re-run with --container.
	if got := err.Error(); !strings.Contains(got, "claude, shell") {
		t.Errorf("error message %q missing sorted candidate list", got)
	}
	if run.calls != 0 {
		t.Errorf("attach.Run called %d times on ambiguous picker, want 0", run.calls)
	}
}

func TestAttach_NoCandidate_Errors(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true},
				// non-attachable workload doesn't count
				{ID: "side", Attachable: false},
			}, nil
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("error %v does not unwrap to ErrAttachNoCandidate", err)
	}
	if run.calls != 0 {
		t.Errorf("attach.Run called %d times on empty picker, want 0", run.calls)
	}
}

func TestAttach_RootContainerExcludedFromAutoPick(t *testing.T) {
	t.Cleanup(viper.Reset)

	// A root container with Attachable=true must NOT be picked: the issue
	// excludes the root container from the auto-pick set explicitly.
	fc := &fakeClient{
		listContainersFn: func(_, _, _, _ string) ([]v1beta1.ContainerSpec, error) {
			return []v1beta1.ContainerSpec{
				{ID: "root", Root: true, Attachable: true},
			}, nil
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("error %v does not unwrap to ErrAttachNoCandidate (root must be excluded from auto-pick)", err)
	}
}

func TestAttach_ExplicitContainer_NotAttachable_SurfacesSentinel(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		// ListContainers must not be called when --container is explicit.
		attachContainerFn: func(doc v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			if got := doc.Metadata.Name; got != "side" {
				t.Errorf("AttachContainer called with name %q, want %q", got, "side")
			}
			return kukeonv1.AttachContainerResult{}, errdefs.ErrAttachNotSupported
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "side",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNotSupported) {
		t.Fatalf("error %v does not unwrap to ErrAttachNotSupported", err)
	}
	if run.calls != 0 {
		t.Errorf("attach.Run called %d times on non-attachable target, want 0", run.calls)
	}
}

func TestAttach_ExplicitContainer_Attachable_Succeeds(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	run := &runCapture{}
	cmd := newCmdWithCtx(t, fc, run)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "shell",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if run.calls != 1 {
		t.Fatalf("attach.Run called %d times, want 1", run.calls)
	}
	if run.opts.SocketPath != testHostSocket {
		t.Errorf("Options.SocketPath = %q, want %q", run.opts.SocketPath, testHostSocket)
	}
}

// TestAttach_RunReturnsCleanDetachError covers the regression from
// #147: when the user presses Ctrl+] Ctrl+], sbsh's controller fires
// the Detach RPC and pkg/attach.Run returns attach.ErrDetached
// (sbsh#192, available since sbsh v0.10.1). The wrapper must recognise
// the public sentinel as a benign session end and return nil so `kuke
// attach` exits 0.
func TestAttach_RunReturnsCleanDetachError(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	cleanDetachErr := fmt.Errorf("wrapped by harness: %w", sbshattach.ErrDetached)
	run := &runReturning{err: cleanDetachErr}
	cmd := newCmdWithCtx(t, fc, nil)
	cmd.SetContext(context.WithValue(cmd.Context(), attachcmd.MockRunKey{}, attachcmd.RunFn(run.fn)))
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v on clean-detach error, want nil", err)
	}
	if run.calls != 1 {
		t.Fatalf("attach.Run called %d times, want 1", run.calls)
	}
}

// TestAttach_RunReturnsPeerClosedError mirrors the clean-detach case for
// the other benign session-end signal: pkg/attach.Run returns
// attach.ErrPeerClosed when the remote terminal drops the IO connection
// (workload exited, peer crashed). For `kuke attach` (a peer attach,
// not the cell owner), this is also exit 0 — same semantics as detach.
func TestAttach_RunReturnsPeerClosedError(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	peerClosedErr := fmt.Errorf("wrapped by harness: %w", sbshattach.ErrPeerClosed)
	run := &runReturning{err: peerClosedErr}
	cmd := newCmdWithCtx(t, fc, nil)
	cmd.SetContext(context.WithValue(cmd.Context(), attachcmd.MockRunKey{}, attachcmd.RunFn(run.fn)))
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned %v on peer-closed error, want nil", err)
	}
	if run.calls != 1 {
		t.Fatalf("attach.Run called %d times, want 1", run.calls)
	}
}

// TestAttach_RunReturnsRealError ensures that any pkg/attach.Run error
// not matching the clean-detach signature still bubbles up — the wrapper
// must not swallow real failures like socket-not-found or RPC errors.
func TestAttach_RunReturnsRealError(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	bootErr := errors.New("wait on ready: dial unix: connection refused")
	run := &runReturning{err: bootErr}
	cmd := newCmdWithCtx(t, fc, nil)
	cmd.SetContext(context.WithValue(cmd.Context(), attachcmd.MockRunKey{}, attachcmd.RunFn(run.fn)))
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "work",
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatalf("Execute returned nil on real error, want %v", bootErr)
	}
	if !errors.Is(err, bootErr) {
		t.Fatalf("error %v does not unwrap to bootErr", err)
	}
}

func TestAttach_MissingFlags(t *testing.T) {
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
			// Reset clears viper's defaults set by DefineKV at package
			// init ("default" for realm/space/stack), so any flag the
			// case omits resolves to the empty string and trips the
			// missing-flag guard. t.Setenv keeps the env-var path
			// quiet too, in case the dev box exports a KUKE_ATTACH_*.
			viper.Reset()
			t.Setenv("KUKE_ATTACH_REALM", "")
			t.Setenv("KUKE_ATTACH_SPACE", "")
			t.Setenv("KUKE_ATTACH_STACK", "")
			t.Setenv("KUKE_ATTACH_CELL", "")

			cmd := newCmdWithCtx(t, &fakeClient{}, &runCapture{})
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v does not unwrap to %v", err, tc.wantErr)
			}
		})
	}
}
