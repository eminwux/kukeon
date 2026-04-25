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
	"os"
	"path/filepath"
	"strings"
	"testing"

	attachcmd "github.com/eminwux/kukeon/cmd/kuke/attach"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	testHostSocket = "/opt/kukeon/r1/s1/st1/c1/work/sbsh.io"
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

// execCapture records the argv passed to syscall.Exec and returns nil so the
// test treats the call as success (the real syscall.Exec never returns on
// success, so nil is the in-test analogue).
type execCapture struct {
	calls int
	argv0 string
	argv  []string
}

func (e *execCapture) fn(argv0 string, argv []string, _ []string) error {
	e.calls++
	e.argv0 = argv0
	e.argv = argv
	return nil
}

func newCmdWithCtx(t *testing.T, fc *fakeClient, exec *execCapture) *cobra.Command {
	t.Helper()

	cmd := attachcmd.NewAttachCmd()
	ctx := context.Background()
	if fc != nil {
		ctx = context.WithValue(ctx, attachcmd.MockControllerKey{}, kukeonv1.Client(fc))
	}
	if exec != nil {
		ctx = context.WithValue(ctx, attachcmd.MockExecKey{}, attachcmd.ExecFn(exec.fn))
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
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--sb-binary", "/usr/local/bin/sb",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if exec.calls != 1 {
		t.Fatalf("exec called %d times, want 1", exec.calls)
	}
	wantArgv := []string{"/usr/local/bin/sb", "attach", "--socket", testHostSocket}
	if !strSliceEq(exec.argv, wantArgv) {
		t.Errorf("argv = %v, want %v", exec.argv, wantArgv)
	}
	if exec.argv0 != "/usr/local/bin/sb" {
		t.Errorf("argv0 = %q, want %q", exec.argv0, "/usr/local/bin/sb")
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
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--sb-binary", "/usr/local/bin/sb",
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
	if exec.calls != 0 {
		t.Errorf("exec called %d times on ambiguous picker, want 0", exec.calls)
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
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--sb-binary", "/usr/local/bin/sb",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNoCandidate) {
		t.Fatalf("error %v does not unwrap to ErrAttachNoCandidate", err)
	}
	if exec.calls != 0 {
		t.Errorf("exec called %d times on empty picker, want 0", exec.calls)
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
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--sb-binary", "/usr/local/bin/sb",
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
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "side",
		"--sb-binary", "/usr/local/bin/sb",
	})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrAttachNotSupported) {
		t.Fatalf("error %v does not unwrap to ErrAttachNotSupported", err)
	}
	if exec.calls != 0 {
		t.Errorf("exec called %d times on non-attachable target, want 0", exec.calls)
	}
}

func TestAttach_ExplicitContainer_Attachable_Succeeds(t *testing.T) {
	t.Cleanup(viper.Reset)

	fc := &fakeClient{
		attachContainerFn: func(_ v1beta1.ContainerDoc) (kukeonv1.AttachContainerResult, error) {
			return kukeonv1.AttachContainerResult{HostSocketPath: testHostSocket}, nil
		},
	}
	exec := &execCapture{}
	cmd := newCmdWithCtx(t, fc, exec)
	cmd.SetArgs([]string{
		"--realm", "r1", "--space", "s1", "--stack", "st1", "--cell", "c1",
		"--container", "shell",
		"--sb-binary", "/usr/local/bin/sb",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if exec.calls != 1 {
		t.Fatalf("exec called %d times, want 1", exec.calls)
	}
	wantArgv := []string{"/usr/local/bin/sb", "attach", "--socket", testHostSocket}
	if !strSliceEq(exec.argv, wantArgv) {
		t.Errorf("argv = %v, want %v", exec.argv, wantArgv)
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

			cmd := newCmdWithCtx(t, &fakeClient{}, &execCapture{})
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error %v does not unwrap to %v", err, tc.wantErr)
			}
		})
	}
}

func TestResolveSbBinary_AbsolutePath_PassesThrough(t *testing.T) {
	got, err := attachcmd.ResolveSbBinaryForTest("/opt/sb/bin/sb")
	if err != nil {
		t.Fatalf("ResolveSbBinary returned error: %v", err)
	}
	if got != "/opt/sb/bin/sb" {
		t.Errorf("ResolveSbBinary = %q, want %q", got, "/opt/sb/bin/sb")
	}
}

func TestResolveSbBinary_PathLookup_FindsBinaryOnPATH(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "sb-test")
	// Create an empty executable file. exec.LookPath only checks for the
	// executable bit, so we don't need real content.
	if err := writeExecutable(binPath); err != nil {
		t.Fatalf("create test binary: %v", err)
	}
	t.Setenv("PATH", dir)

	got, err := attachcmd.ResolveSbBinaryForTest("sb-test")
	if err != nil {
		t.Fatalf("ResolveSbBinary returned error: %v", err)
	}
	if got != binPath {
		t.Errorf("ResolveSbBinary = %q, want %q", got, binPath)
	}
}

func TestResolveSbBinary_PathLookup_MissingBinary_Errors(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := attachcmd.ResolveSbBinaryForTest("definitely-not-on-path"); err == nil {
		t.Fatal("ResolveSbBinary returned nil, want error for missing binary")
	}
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeExecutable(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	return f.Close()
}
