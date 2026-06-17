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

package recreate_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	recreate "github.com/eminwux/kukeon/cmd/kuke/daemon/recreate"
	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// TestMain mocks the shared euid lookup to euid=0 for every test in this
// package so the fail-fast root gate in runRecreate does not short-circuit the
// existing fakes-driven coverage when CI runs as a non-root user.
func TestMain(m *testing.M) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 0 })
	code := m.Run()
	restore()
	os.Exit(code)
}

func TestDaemonRecreate(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		fake           *fakeClient
		wantErr        string
		wantOutputs    []string
		wantNotOutputs []string
	}{
		{
			name: "running cell is torn down, re-provisioned, and daemon becomes ready",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: func() *fakeClient {
				var getCalls int
				return &fakeClient{
					getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
						assertKukeondTarget(t, doc)
						getCalls++
						if getCalls == 1 {
							return kukeonv1.GetCellResult{
								Cell: v1beta1.CellDoc{
									Status: v1beta1.CellStatus{
										State: v1beta1.CellStateReady,
										Containers: []v1beta1.ContainerStatus{
											{State: v1beta1.ContainerStateReady},
										},
									},
								},
								MetadataExists: true,
							}, nil
						}
						return kukeonv1.GetCellResult{
							Cell: v1beta1.CellDoc{
								Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
							},
							MetadataExists: true,
						}, nil
					},
					stopCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
						assertKukeondTarget(t, doc)
						return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
					},
					deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
						assertKukeondTarget(t, doc)
						return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
					},
				}
			}(),
			wantOutputs: []string{
				`kukeond stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond cell deleted (cell "kukeond" in realm "kuke-system")`,
			},
		},
		{
			name: "already-stopped cell skips stop phase and still deletes",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					t.Fatalf("StopCell must not be called when daemon is already stopped")
					return kukeonv1.StopCellResult{}, nil
				},
				deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
				},
			},
			wantOutputs: []string{
				`kukeond was already stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond cell deleted (cell "kukeond" in realm "kuke-system")`,
			},
		},
		{
			name: "host not initialized",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{MetadataExists: false}, nil
				},
			},
			wantErr: "kukeon host is not initialized",
		},
		{
			name:    "--kukeond-image flag is required",
			args:    []string{},
			fake:    &fakeClient{},
			wantErr: "--kukeond-image is required",
		},
		{
			name: "GetCell error is wrapped",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{}, errors.New("io: read failed")
				},
			},
			wantErr: "inspect kukeond cell:",
		},
		{
			name: "StopCell error is wrapped and delete phase is not reached",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{
								State: v1beta1.CellStateReady,
								Containers: []v1beta1.ContainerStatus{
									{State: v1beta1.ContainerStateReady},
								},
							},
						},
						MetadataExists: true,
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, errors.New("runner blew up")
				},
				deleteCellFn: func(_ v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					t.Fatalf("DeleteCell must not be called when stop phase fails")
					return kukeonv1.DeleteCellResult{}, nil
				},
			},
			wantErr: "stop kukeond cell:",
		},
		{
			name: "DeleteCell error is wrapped",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				deleteCellFn: func(_ v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					return kukeonv1.DeleteCellResult{}, errors.New("runner blew up")
				},
			},
			wantErr: "delete kukeond cell:",
		},
		{
			name: "DeleteCell reports no change is an error",
			args: []string{"--kukeond-image", "test-img:dev"},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: false}, nil
				},
			},
			wantErr: "controller reported no change",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshViper(t)
			cmd := recreate.NewRecreateCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(tt.fake))
			ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
			ctx = context.WithValue(ctx, recreate.MockProvisionKukeondCellKey{}, func() error { return nil })
			ctx = context.WithValue(ctx, recreate.MockWaitForReadyKey{}, func() error { return nil })
			ctx = context.WithValue(ctx, recreate.MockApplySocketOwnershipKey{}, func() error { return nil })
			ctx = context.WithValue(ctx, recreate.MockSocketDirKey{}, t.TempDir())
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			out := buf.String()
			for _, want := range tt.wantOutputs {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q\nGot:\n%s", want, out)
				}
			}
			for _, notWant := range tt.wantNotOutputs {
				if strings.Contains(out, notWant) {
					t.Errorf("output unexpectedly contained %q\nGot:\n%s", notWant, out)
				}
			}
		})
	}
}

func TestDaemonRecreate_RemovesSocketAndPidFiles(t *testing.T) {
	withFreshViper(t)

	socketDir := t.TempDir()
	sockPath := filepath.Join(socketDir, "kukeond.sock")
	pidPath := filepath.Join(socketDir, "kukeond.pid")
	if err := os.WriteFile(sockPath, []byte{}, 0o600); err != nil {
		t.Fatalf("seed sock file: %v", err)
	}
	if err := os.WriteFile(pidPath, []byte("12345\n"), 0o600); err != nil {
		t.Fatalf("seed pid file: %v", err)
	}

	fake := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
				},
				MetadataExists: true,
			}, nil
		},
		deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
			return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
		},
	}

	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockProvisionKukeondCellKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockWaitForReadyKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockApplySocketOwnershipKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockSocketDirKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--kukeond-image", "test-img:dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error on missing sock/pid: %v", err)
	}
	if strings.Contains(buf.String(), "removed") {
		t.Errorf("did not expect any 'removed' line when files are already gone; got:\n%s", buf.String())
	}
}

// TestDaemonRecreate_AppliesSocketOwnership confirms the post-ready step that
// re-asserts root:kukeon ownership on the socket runs after the daemon becomes
// reachable and that the chown notice is printed — the regression guard for the
// bug where `kuke daemon recreate` left the socket 0o600 root-only, forcing
// non-root kukeon-group members to sudo.
func TestDaemonRecreate_AppliesSocketOwnership(t *testing.T) {
	withFreshViper(t)

	var ownershipCalled bool
	fake := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
				},
				MetadataExists: true,
			}, nil
		},
		deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
			return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
		},
	}

	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockProvisionKukeondCellKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockWaitForReadyKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockApplySocketOwnershipKey{}, func() error {
		ownershipCalled = true
		return nil
	})
	ctx = context.WithValue(ctx, recreate.MockSocketDirKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--kukeond-image", "test-img:dev"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ownershipCalled {
		t.Fatal("expected the post-ready socket-ownership step to be invoked")
	}
	if out := buf.String(); !strings.Contains(out, "chown root:kukeon mode 0660") {
		t.Errorf("output missing socket chown notice; got:\n%s", out)
	}
}

// TestDaemonRecreate_SocketOwnershipFailureIsWrapped confirms a failure in the
// post-ready socket-ownership step surfaces as a wrapped error rather than a
// silent success that leaves the socket inaccessible.
func TestDaemonRecreate_SocketOwnershipFailureIsWrapped(t *testing.T) {
	withFreshViper(t)

	fake := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
				},
				MetadataExists: true,
			}, nil
		},
		deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
			return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
		},
	}

	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockProvisionKukeondCellKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockWaitForReadyKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockApplySocketOwnershipKey{}, func() error {
		return errors.New("chown blew up")
	})
	ctx = context.WithValue(ctx, recreate.MockSocketDirKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--kukeond-image", "test-img:dev"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "apply kukeon ownership") {
		t.Fatalf("want wrapped ownership error, got %v", err)
	}
}

// TestDaemonRecreate_GracefulTimeoutEscalatesToKill exercises the SIGTERM ->
// SIGKILL escalation path: StopCell blocks past --timeout, KillCell must be
// invoked, the escalation notice is printed, and DeleteCell still runs.
func TestDaemonRecreate_GracefulTimeoutEscalatesToKill(t *testing.T) {
	withFreshViper(t)

	stopBlocked := make(chan struct{})
	releaseStop := make(chan struct{})
	defer close(releaseStop)

	var killCalled, deleteCalled atomicBool

	fake := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Status: v1beta1.CellStatus{
						State: v1beta1.CellStateReady,
						Containers: []v1beta1.ContainerStatus{
							{State: v1beta1.ContainerStateReady},
						},
					},
				},
				MetadataExists: true,
			}, nil
		},
		stopCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
			close(stopBlocked)
			<-releaseStop
			return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
		},
		killCellFn: func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			killCalled.Store(true)
			return kukeonv1.KillCellResult{Cell: doc, Killed: true}, nil
		},
		deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
			deleteCalled.Store(true)
			return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
		},
	}

	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockProvisionKukeondCellKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockWaitForReadyKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockApplySocketOwnershipKey{}, func() error { return nil })
	ctx = context.WithValue(ctx, recreate.MockSocketDirKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--kukeond-image", "test-img:dev", "--timeout", "50ms"})

	errCh := make(chan error, 1)
	go func() { errCh <- cmd.Execute() }()

	select {
	case <-stopBlocked:
	case <-time.After(2 * time.Second):
		t.Fatal("StopCell was not invoked within 2s")
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("runRecreate did not return after timeout fired")
	}

	if !killCalled.Load() {
		t.Fatal("expected KillCell to be invoked when --timeout fires before StopCell returns")
	}
	if !deleteCalled.Load() {
		t.Fatal("expected DeleteCell to be invoked after escalation completes")
	}
	out := buf.String()
	if !strings.Contains(out, "force-killed after 50ms grace period expired") {
		t.Errorf("output missing escalation notice; got:\n%s", out)
	}
	if !strings.Contains(out, "kukeond cell deleted") {
		t.Errorf("output missing delete-phase notice; got:\n%s", out)
	}
}

// TestDaemonRecreate_NonRootIsRejected confirms the fail-fast UID gate rejects
// non-root invocations before any side effect.
func TestDaemonRecreate_NonRootIsRejected(t *testing.T) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)
	withFreshViper(t)

	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd.SetContext(context.WithValue(context.Background(), types.CtxLogger, logger))
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("kuke daemon recreate returned nil under euid=1000, want ErrMustRunAsRoot")
	}
	if !errors.Is(err, errdefs.ErrMustRunAsRoot) {
		t.Fatalf("kuke daemon recreate error does not wrap ErrMustRunAsRoot: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke daemon recreate") {
		t.Errorf("error does not name the subcommand: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not suggest sudo: %v", err)
	}
}

func TestDaemonRecreate_LoggerMissingFromContext(t *testing.T) {
	withFreshViper(t)
	cmd := recreate.NewRecreateCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.Background())
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "logger not found") {
		t.Fatalf("expected logger-missing error, got %v", err)
	}
}

func assertKukeondTarget(t *testing.T, doc v1beta1.CellDoc) {
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

func withFreshViper(t *testing.T) {
	t.Helper()
	viper.Reset()
}

type atomicBool struct {
	mu sync.Mutex
	v  bool
}

func (a *atomicBool) Store(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.v = v
}

func (a *atomicBool) Load() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn    func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	stopCellFn   func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	killCellFn   func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error)
	deleteCellFn func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) StopCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	if f.stopCellFn == nil {
		return kukeonv1.StopCellResult{}, errors.New("unexpected StopCell call")
	}
	return f.stopCellFn(doc)
}

func (f *fakeClient) KillCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
	if f.killCellFn == nil {
		return kukeonv1.KillCellResult{}, errors.New("unexpected KillCell call")
	}
	return f.killCellFn(doc)
}

func (f *fakeClient) DeleteCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
	if f.deleteCellFn == nil {
		return kukeonv1.DeleteCellResult{}, errors.New("unexpected DeleteCell call")
	}
	return f.deleteCellFn(doc)
}
