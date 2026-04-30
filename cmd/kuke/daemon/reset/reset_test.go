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

package reset_test

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

	reset "github.com/eminwux/kukeon/cmd/kuke/daemon/reset"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// TestDaemonReset covers the table of states the verb has to handle: a
// running daemon (graceful stop+delete), an already-stopped daemon (skip
// stop, still delete), missing-init guard, and the propagation of GetCell /
// StopCell / DeleteCell errors.
func TestDaemonReset(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		fake           *fakeClient
		wantErr        string
		wantOutputs    []string
		wantNotOutputs []string
	}{
		{
			name: "running cell is gracefully stopped, deleted, sock+pid cleared",
			fake: &fakeClient{
				getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					assertKukeondTarget(t, doc)
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
					assertKukeondTarget(t, doc)
					return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
				},
				deleteCellFn: func(doc v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					assertKukeondTarget(t, doc)
					return kukeonv1.DeleteCellResult{Cell: doc, MetadataDeleted: true}, nil
				},
			},
			wantOutputs: []string{
				`kukeond stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond cell deleted (cell "kukeond" in realm "kuke-system")`,
			},
		},
		{
			name: "already-stopped cell skips stop phase and still deletes",
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
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{MetadataExists: false}, nil
				},
			},
			wantErr: "kukeon host is not initialized",
		},
		{
			name: "GetCell error is wrapped",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{}, errors.New("io: read failed")
				},
			},
			wantErr: "inspect kukeond cell:",
		},
		{
			name: "StopCell error is wrapped and delete phase is not reached",
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
			name: "StopCell reports no change is an error and delete is not reached",
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
				stopCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{Cell: doc, Stopped: false}, nil
				},
				deleteCellFn: func(_ v1beta1.CellDoc) (kukeonv1.DeleteCellResult, error) {
					t.Fatalf("DeleteCell must not be called when stop reports no change")
					return kukeonv1.DeleteCellResult{}, nil
				},
			},
			wantErr: "controller reported no change",
		},
		{
			name: "DeleteCell error is wrapped",
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
			wantErr: "delete kukeond cell: controller reported no change",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshViper(t)
			cmd := reset.NewResetCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(tt.fake))
			ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, t.TempDir())
			ctx = context.WithValue(ctx, reset.MockRunPathKey{}, t.TempDir())
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

// TestDaemonReset_RemovesSocketAndPidFiles confirms the cleanup step actually
// removes /run/kukeon/kukeond.{sock,pid} (or the per-test override) when both
// files are present.
func TestDaemonReset_RemovesSocketAndPidFiles(t *testing.T) {
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

	cmd := reset.NewResetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, socketDir)
	ctx = context.WithValue(ctx, reset.MockRunPathKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(sockPath); !os.IsNotExist(err) {
		t.Errorf("kukeond.sock not removed: stat err=%v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Errorf("kukeond.pid not removed: stat err=%v", err)
	}
	out := buf.String()
	if !strings.Contains(out, sockPath) || !strings.Contains(out, pidPath) {
		t.Errorf("expected output to mention removed sock/pid paths; got:\n%s", out)
	}
}

// TestDaemonReset_MissingSockAndPidIsNoOp confirms idempotency: a second
// `kuke daemon reset` (or one against a partially-cleaned host) does not
// error when the sock/pid files are already gone.
func TestDaemonReset_MissingSockAndPidIsNoOp(t *testing.T) {
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

	cmd := reset.NewResetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, t.TempDir())
	ctx = context.WithValue(ctx, reset.MockRunPathKey{}, t.TempDir())
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error on missing sock/pid: %v", err)
	}
	if strings.Contains(buf.String(), "removed") {
		t.Errorf("did not expect any 'removed' line when files are already gone; got:\n%s", buf.String())
	}
}

// TestDaemonReset_PreservesDefaultRealm covers the AC: --purge-system removes
// /opt/kukeon/kuke-system but never touches /opt/kukeon/default. Both
// directories are seeded with sentinel files; only the kuke-system tree
// disappears.
func TestDaemonReset_PreservesDefaultRealm(t *testing.T) {
	withFreshViper(t)

	runPath := t.TempDir()
	defaultDir := filepath.Join(runPath, consts.KukeonDefaultRealmName)
	systemDir := filepath.Join(runPath, consts.KukeSystemRealmName)
	if err := os.MkdirAll(defaultDir, 0o750); err != nil {
		t.Fatalf("mkdir default: %v", err)
	}
	if err := os.MkdirAll(systemDir, 0o750); err != nil {
		t.Fatalf("mkdir kuke-system: %v", err)
	}
	defaultMarker := filepath.Join(defaultDir, "user-data.txt")
	systemMarker := filepath.Join(systemDir, "system-data.txt")
	if err := os.WriteFile(defaultMarker, []byte("user data"), 0o600); err != nil {
		t.Fatalf("seed default marker: %v", err)
	}
	if err := os.WriteFile(systemMarker, []byte("system data"), 0o600); err != nil {
		t.Fatalf("seed system marker: %v", err)
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

	cmd := reset.NewResetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, t.TempDir())
	ctx = context.WithValue(ctx, reset.MockRunPathKey{}, runPath)
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--purge-system"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(systemDir); !os.IsNotExist(err) {
		t.Errorf("kuke-system dir was not removed under --purge-system: stat err=%v", err)
	}
	if _, err := os.Stat(defaultMarker); err != nil {
		t.Errorf("default-realm marker must not be touched: stat err=%v", err)
	}
	if !strings.Contains(buf.String(), systemDir) {
		t.Errorf("expected output to mention removed kuke-system dir; got:\n%s", buf.String())
	}
}

// TestDaemonReset_NoPurgeSystemKeepsKukeSystem is the negative twin of the
// preceding test: without --purge-system, the kuke-system tree must be
// preserved alongside the default realm.
func TestDaemonReset_NoPurgeSystemKeepsKukeSystem(t *testing.T) {
	withFreshViper(t)

	runPath := t.TempDir()
	systemDir := filepath.Join(runPath, consts.KukeSystemRealmName)
	if err := os.MkdirAll(systemDir, 0o750); err != nil {
		t.Fatalf("mkdir kuke-system: %v", err)
	}
	systemMarker := filepath.Join(systemDir, "system-data.txt")
	if err := os.WriteFile(systemMarker, []byte("system data"), 0o600); err != nil {
		t.Fatalf("seed system marker: %v", err)
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

	cmd := reset.NewResetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, t.TempDir())
	ctx = context.WithValue(ctx, reset.MockRunPathKey{}, runPath)
	cmd.SetContext(ctx)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(systemMarker); err != nil {
		t.Errorf("kuke-system marker must be preserved without --purge-system: stat err=%v", err)
	}
}

// TestDaemonReset_GracefulTimeoutEscalatesToKill exercises the SIGTERM →
// SIGKILL escalation path: StopCell blocks past --timeout, KillCell must be
// invoked, the escalation notice is printed, and DeleteCell still runs.
func TestDaemonReset_GracefulTimeoutEscalatesToKill(t *testing.T) {
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

	cmd := reset.NewResetCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, reset.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, reset.MockSocketDirKey{}, t.TempDir())
	ctx = context.WithValue(ctx, reset.MockRunPathKey{}, t.TempDir())
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--timeout", "50ms"})

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
		t.Fatal("runReset did not return after timeout fired")
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

// TestDaemonReset_LoggerMissingFromContext confirms the verb refuses to run
// when the logger context value is absent — same guard the other daemon-
// lifecycle verbs apply.
func TestDaemonReset_LoggerMissingFromContext(t *testing.T) {
	withFreshViper(t)
	cmd := reset.NewResetCmd()
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

// withFreshViper resets the viper keys the reset command binds to so prior
// tests' --timeout / --purge-system values don't leak between runs.
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
