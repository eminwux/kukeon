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

package restart_test

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

	"github.com/eminwux/kukeon/cmd/kuke/daemon/internal/lifecycle"
	restart "github.com/eminwux/kukeon/cmd/kuke/daemon/restart"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// TestMain mocks the shared euid lookup to euid=0 for every test in this
// package so the fail-fast root gate in runRestart does not short-circuit the
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

func TestDaemonRestart(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		fake           *fakeClient
		probe          lifecycle.ReachableProbe
		wantErr        string
		wantOutputs    []string
		wantNotOutputs []string
	}{
		{
			name: "running cell is gracefully stopped then started",
			fake: func() *fakeClient {
				// GetCell #1: runRestart sees Ready and picks StopPhase.
				// GetCell #2: StopPhase's post-stop verification (issue
				// #868) returns Stopped so the escalation path is not taken.
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
					startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
						assertKukeondTarget(t, doc)
						return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
					},
					killCellFn: func(_ v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
						t.Fatalf("KillCell must not be called when graceful stop succeeds")
						return kukeonv1.KillCellResult{}, nil
					},
				}
			}(),
			wantOutputs: []string{
				`kukeond stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond started (cell "kukeond" in realm "kuke-system")`,
			},
		},
		{
			name: "already-stopped cell with unreachable socket skips stop phase and starts",
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
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			probe: fixedProbe(false),
			wantOutputs: []string{
				`kukeond was already stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond started (cell "kukeond" in realm "kuke-system")`,
			},
		},
		{
			// Other-direction staleness fix (#611): metadata reads
			// not-Ready but the socket still answers, so the daemon is
			// live and the controller's status lags. Old behaviour
			// would skip the stop phase and call StartCell on top of a
			// running daemon; new behaviour falls through to StopPhase
			// first so restart actually restarts.
			name: "metadata says not-Ready but socket is reachable runs stop phase then starts",
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
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			probe: fixedProbe(true),
			wantOutputs: []string{
				"metadata reports not-Ready but socket",
				`kukeond stopped (cell "kukeond" in realm "kuke-system")`,
				`kukeond started (cell "kukeond" in realm "kuke-system")`,
			},
			wantNotOutputs: []string{
				`kukeond was already stopped`,
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
			name: "StopCell error is wrapped and start phase is not reached",
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
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					t.Fatalf("StartCell must not be called when stop phase fails")
					return kukeonv1.StartCellResult{}, nil
				},
			},
			wantErr: "stop kukeond cell:",
		},
		{
			name: "StopCell reports no change is an error and start is not reached",
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
				startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					t.Fatalf("StartCell must not be called when stop phase reports no change")
					return kukeonv1.StartCellResult{}, nil
				},
			},
			wantErr: "controller reported no change",
		},
		{
			name: "StartCell error after successful stop is wrapped",
			fake: func() *fakeClient {
				// GetCell #1: runRestart sees Ready → StopPhase.
				// GetCell #2: StopPhase post-stop verification sees Stopped
				// so StartCell is reached and its error is what surfaces.
				var getCalls int
				return &fakeClient{
					getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
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
						return kukeonv1.StopCellResult{Cell: doc, Stopped: true}, nil
					},
					startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
						return kukeonv1.StartCellResult{}, errors.New("runner blew up")
					},
				}
			}(),
			wantErr: "start kukeond cell:",
		},
		{
			name: "StartCell reports no change is an error",
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						Cell: v1beta1.CellDoc{
							Status: v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
						MetadataExists: true,
					}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: false}, nil
				},
			},
			probe:   fixedProbe(false),
			wantErr: "start kukeond cell: controller reported no change",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshViper(t)
			cmd := restart.NewRestartCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(tt.fake))
			ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
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

// TestDaemonRestart_GracefulTimeoutEscalatesToKillThenStarts exercises the
// SIGTERM → SIGKILL escalation path inside the stop phase: StopCell blocks
// past --timeout, KillCell must be invoked, the escalation notice is printed,
// and the start phase still runs to completion.
func TestDaemonRestart_GracefulTimeoutEscalatesToKillThenStarts(t *testing.T) {
	withFreshViper(t)

	stopBlocked := make(chan struct{})
	releaseStop := make(chan struct{})
	defer close(releaseStop)

	var killCalled, startCalled atomicBool

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
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			startCalled.Store(true)
			return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
		},
	}

	cmd := restart.NewRestartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
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
		t.Fatal("runRestart did not return after timeout fired")
	}

	if !killCalled.Load() {
		t.Fatal("expected KillCell to be invoked when --timeout fires before StopCell returns")
	}
	if !startCalled.Load() {
		t.Fatal("expected StartCell to be invoked after escalation completes")
	}
	out := buf.String()
	if !strings.Contains(out, "force-killed after 50ms grace period expired") {
		t.Errorf("output missing escalation notice; got:\n%s", out)
	}
	if !strings.Contains(out, "kukeond started") {
		t.Errorf("output missing start phase notice; got:\n%s", out)
	}
}

// TestDaemonRestart_TimeoutOverridesDefault confirms the --timeout flag
// reaches the stop phase rather than the 10s default. Setting --timeout=20ms
// against a blocking StopCell must escalate within milliseconds, not seconds —
// this is the regression guard for "AC: --timeout overrides stop's grace
// period".
func TestDaemonRestart_TimeoutOverridesDefault(t *testing.T) {
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
		killCellFn: func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error) {
			return kukeonv1.KillCellResult{Cell: doc, Killed: true}, nil
		},
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
		},
	}

	cmd := restart.NewRestartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--timeout", "20ms"})

	start := time.Now()
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	elapsed := time.Since(start)

	// Generous upper bound: the override must escalate well below the 10s default.
	if elapsed > 2*time.Second {
		t.Fatalf("restart took %s — --timeout=20ms did not override the default", elapsed)
	}
	if !strings.Contains(buf.String(), "20ms grace period expired") {
		t.Errorf("output should mention the 20ms grace period; got:\n%s", buf.String())
	}
}

// TestDaemonRestart_KillCellErrorIsWrapped covers the failure mode where the
// graceful timeout fires, escalation runs, and KillCell itself errors — the
// start phase must not run, and the error must mention the expired grace
// period.
func TestDaemonRestart_KillCellErrorIsWrapped(t *testing.T) {
	withFreshViper(t)

	releaseStop := make(chan struct{})
	defer close(releaseStop)

	var startCalled atomicBool

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
		startCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			startCalled.Store(true)
			return kukeonv1.StartCellResult{}, nil
		},
	}

	cmd := restart.NewRestartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, lifecycle.MockClientKey{}, kukeonv1.Client(fake))
	ctx = context.WithValue(ctx, lifecycle.EnsureSocketDirKey{}, func() error { return nil })
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"--timeout", "20ms"})

	err := cmd.Execute()
	if err == nil ||
		!strings.Contains(err.Error(), "kill kukeond cell after") ||
		!strings.Contains(err.Error(), "20ms grace period expired") {
		t.Fatalf("expected wrapped kill error mentioning grace period, got %v", err)
	}
	if startCalled.Load() {
		t.Fatal("StartCell must not run when the stop-phase escalation fails")
	}
}

// TestDaemonRestart_NonRootIsRejected confirms the fail-fast UID gate rejects
// non-root invocations before any side effect (cell lookup, in-process
// controller construction). Symmetric with the same guard on `kuke daemon
// reset` and the rest of the daemon-lifecycle verbs (#463).
func TestDaemonRestart_NonRootIsRejected(t *testing.T) {
	restore := kukshared.SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)
	viper.Reset()

	cmd := restart.NewRestartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cmd.SetContext(context.WithValue(context.Background(), types.CtxLogger, logger))
	cmd.SetArgs(nil)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("kuke daemon restart returned nil under euid=1000, want ErrMustRunAsRoot")
	}
	if !errors.Is(err, errdefs.ErrMustRunAsRoot) {
		t.Fatalf("kuke daemon restart error does not wrap ErrMustRunAsRoot: %v", err)
	}
	if !strings.Contains(err.Error(), "kuke daemon restart") {
		t.Errorf("error does not name the subcommand: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo") {
		t.Errorf("error does not suggest sudo: %v", err)
	}
}

func TestDaemonRestart_LoggerMissingFromContext(t *testing.T) {
	withFreshViper(t)
	cmd := restart.NewRestartCmd()
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

// withFreshViper resets the viper key the restart command binds to, so prior
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

	getCellFn   func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	stopCellFn  func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	killCellFn  func(doc v1beta1.CellDoc) (kukeonv1.KillCellResult, error)
	startCellFn func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
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

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
}
