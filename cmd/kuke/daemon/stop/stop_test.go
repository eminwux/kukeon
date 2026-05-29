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

package stop_test

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

	"github.com/eminwux/kukeon/cmd/kuke/internal/lifecycle"
	stop "github.com/eminwux/kukeon/cmd/kuke/daemon/stop"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// TestMain mocks the shared euid lookup to euid=0 for every test in this
// package so the fail-fast root gate in runStop does not short-circuit the
// existing fakes-driven coverage when CI runs as a non-root user
// (ubuntu-latest defaults to UID 1001). The dedicated non-root case overrides
// this with its own SetGeteuidForTesting call.
func TestMain(m *testing.M) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 0 })
	code := m.Run()
	restore()
	os.Exit(code)
}

// fixedProbe returns a ReachableProbe that always reports `reachable`. Used
// to drive the socket-staleness branches deterministically without needing a
// real listening socket — the production default would dial the configured
// KUKEOND_SOCKET path, which is host-state dependent.
func fixedProbe(reachable bool) lifecycle.ReachableProbe {
	return func(_ context.Context, _ string, _ time.Duration) bool { return reachable }
}

func TestDaemonStop(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		fake       *fakeClient
		probe      lifecycle.ReachableProbe
		wantErr    string
		wantOutput string
	}{
		{
			name: "running cell is gracefully stopped",
			fake: func() *fakeClient {
				// GetCell #1: runStop's branch picker sees Ready.
				// GetCell #2: StopPhase's post-stop verification (issue #868)
				// returns Stopped so the escalation path is not taken.
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
					killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
						t.Fatalf("KillCell must not be called when graceful stop succeeds")
						return kukeonv1.KillCellResult{}, nil
					},
				}
			}(),
			wantOutput: `kukeond stopped (cell "kukeond" in realm "kuke-system")`,
		},
		{
			name: "already-stopped cell with unreachable socket is a no-op",
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
			},
			probe:      fixedProbe(false),
			wantOutput: `kukeond is already stopped (cell "kukeond" in realm "kuke-system")`,
		},
		{
			// The other-direction staleness fix from #611: metadata reads
			// not-Ready but the socket still answers, so the daemon is up
			// and the controller's status simply lags. Old behaviour would
			// silently no-op and leave the live daemon untouched. New
			// behaviour: log the staleness and fall through to StopPhase.
			name: "metadata says not-Ready but socket is reachable falls through to StopPhase",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				stopCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
				},
			},
			probe:      fixedProbe(true),
			wantOutput: "metadata reports not-Ready but socket",
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
			name: "StopCell error is wrapped",
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
			},
			wantErr: "stop kukeond cell:",
		},
		{
			name: "StopCell reports no change is an error",
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
			},
			wantErr: "controller reported no change",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshViper(t)
			cmd := stop.NewStopCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(tt.fake))
			if tt.probe != nil {
				ctx = context.WithValue(ctx, lifecycle.ReachableProbeKey{}, tt.probe)
			}
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
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

// TestDaemonStop_GracefulTimeoutEscalatesToKill exercises the SIGTERM →
// SIGKILL escalation path by having the fake StopCell block past the user's
// --timeout. KillCell must be invoked, and the output must mention the
// expired grace period.
func TestDaemonStop_GracefulTimeoutEscalatesToKill(t *testing.T) {
	withFreshViper(t)

	stopBlocked := make(chan struct{})
	releaseStop := make(chan struct{})
	defer close(releaseStop)

	var killCalled atomicBool

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
	}

	cmd := stop.NewStopCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
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
		t.Fatal("runStop did not return after timeout fired")
	}

	if !killCalled.Load() {
		t.Fatal("expected KillCell to be invoked when --timeout fires before StopCell returns")
	}
	if !strings.Contains(buf.String(), "force-killed after 50ms grace period expired") {
		t.Errorf("output missing escalation notice; got:\n%s", buf.String())
	}
}

// TestDaemonStop_KillCellErrorIsWrapped covers the failure mode where the
// graceful timeout fires, escalation runs, and KillCell itself errors.
func TestDaemonStop_KillCellErrorIsWrapped(t *testing.T) {
	withFreshViper(t)

	releaseStop := make(chan struct{})
	defer close(releaseStop)

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
			<-releaseStop
			return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
		},
		killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{}, errors.New("ctrd unreachable")
		},
	}

	cmd := stop.NewStopCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--timeout", "20ms"})

	err := cmd.Execute()
	if err == nil ||
		!strings.Contains(err.Error(), "kill kukeond cell after") ||
		!strings.Contains(err.Error(), "20ms grace period expired") {
		t.Fatalf("expected wrapped kill error mentioning grace period, got %v", err)
	}
}

// TestDaemonStop_NonRootIsRejected confirms the fail-fast UID gate rejects
// non-root invocations before any side effect (cell lookup, in-process
// controller construction). Symmetric with the same guard on `kuke daemon
// reset` and the rest of the daemon-lifecycle verbs (#463).
func TestDaemonStop_NonRootIsRejected(t *testing.T) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)
	viper.Reset()

	cmd := stop.NewStopCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd.SetContext(context.WithValue(context.Background(), types.CtxLogger, logger))
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("kuke daemon stop returned nil under euid=1000, want ErrMustRunAsRoot")
	}
	if !errors.Is(err, errdefs.ErrMustRunAsRoot) {
		t.Fatalf("kuke daemon stop error does not wrap ErrMustRunAsRoot: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke daemon stop") {
		t.Errorf("error does not name the subcommand: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not suggest sudo: %v", err)
	}
}

func TestDaemonStop_LoggerMissingFromContext(t *testing.T) {
	withFreshViper(t)
	cmd := stop.NewStopCmd()
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

// withFreshViper resets the viper key the stop command binds to, so prior
// tests' --timeout values don't leak between table-driven runs.
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

	getCellFn  func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	stopCellFn func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	killCellFn func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error)
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
