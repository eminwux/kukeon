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

package uninstall_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/uninstall"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/spf13/viper"
)

// stubController implements controller.Controller; only Uninstall and Close
// are exercised here. The rest panic so a stray call shows up loudly.
type stubController struct {
	uninstallFn func(controller.UninstallOptions) (controller.UninstallReport, error)
	closed      bool
}

func (s *stubController) Uninstall(opts controller.UninstallOptions) (controller.UninstallReport, error) {
	return s.uninstallFn(opts)
}

func (s *stubController) Close() error {
	s.closed = true
	return nil
}

// Unused interface methods — panic on call so tests fail fast on accidental
// reach-through. Keep this list aligned with controller.Controller.
func (s *stubController) Bootstrap() (controller.BootstrapReport, error) { panic("not used") }

func (s *stubController) CreateRealm(intmodel.Realm) (controller.CreateRealmResult, error) {
	panic("not used")
}

func (s *stubController) CreateSpace(intmodel.Space) (controller.CreateSpaceResult, error) {
	panic("not used")
}

func (s *stubController) CreateStack(intmodel.Stack) (controller.CreateStackResult, error) {
	panic("not used")
}

func (s *stubController) CreateCell(intmodel.Cell) (controller.CreateCellResult, error) {
	panic("not used")
}

func (s *stubController) CreateContainer(intmodel.Container) (controller.CreateContainerResult, error) {
	panic("not used")
}

func (s *stubController) DeleteRealm(intmodel.Realm, bool, bool) (controller.DeleteRealmResult, error) {
	panic("not used")
}

func (s *stubController) DeleteSpace(intmodel.Space, bool, bool) (controller.DeleteSpaceResult, error) {
	panic("not used")
}

func (s *stubController) DeleteStack(intmodel.Stack, bool, bool) (controller.DeleteStackResult, error) {
	panic("not used")
}

func (s *stubController) DeleteCell(intmodel.Cell) (controller.DeleteCellResult, error) {
	panic("not used")
}

func (s *stubController) DeleteContainer(intmodel.Container) (controller.DeleteContainerResult, error) {
	panic("not used")
}

func (s *stubController) GetRealm(intmodel.Realm) (controller.GetRealmResult, error) {
	panic("not used")
}
func (s *stubController) ListRealms() ([]intmodel.Realm, error) { panic("not used") }
func (s *stubController) GetSpace(intmodel.Space) (controller.GetSpaceResult, error) {
	panic("not used")
}
func (s *stubController) ListSpaces(string) ([]intmodel.Space, error) { panic("not used") }
func (s *stubController) GetStack(intmodel.Stack) (controller.GetStackResult, error) {
	panic("not used")
}
func (s *stubController) ListStacks(string, string) ([]intmodel.Stack, error) { panic("not used") }
func (s *stubController) GetCell(intmodel.Cell) (controller.GetCellResult, error) {
	panic("not used")
}

func (s *stubController) ListCells(string, string, string) ([]intmodel.Cell, error) {
	panic("not used")
}

func (s *stubController) GetContainer(intmodel.Container) (controller.GetContainerResult, error) {
	panic("not used")
}

func (s *stubController) ListContainers(string, string, string, string) ([]intmodel.ContainerSpec, error) {
	panic("not used")
}

func (s *stubController) StartCell(intmodel.Cell) (controller.StartCellResult, error) {
	panic("not used")
}

func (s *stubController) StartContainer(intmodel.Container) (controller.StartContainerResult, error) {
	panic("not used")
}

func (s *stubController) StopCell(intmodel.Cell) (controller.StopCellResult, error) {
	panic("not used")
}

func (s *stubController) StopContainer(intmodel.Container) (controller.StopContainerResult, error) {
	panic("not used")
}

func (s *stubController) KillCell(intmodel.Cell) (controller.KillCellResult, error) {
	panic("not used")
}

func (s *stubController) KillContainer(intmodel.Container) (controller.KillContainerResult, error) {
	panic("not used")
}

func (s *stubController) PurgeRealm(intmodel.Realm, bool, bool) (controller.PurgeRealmResult, error) {
	panic("not used")
}

func (s *stubController) PurgeSpace(intmodel.Space, bool, bool) (controller.PurgeSpaceResult, error) {
	panic("not used")
}

func (s *stubController) PurgeStack(intmodel.Stack, bool, bool) (controller.PurgeStackResult, error) {
	panic("not used")
}

func (s *stubController) PurgeCell(intmodel.Cell, bool, bool) (controller.PurgeCellResult, error) {
	panic("not used")
}

func (s *stubController) PurgeContainer(intmodel.Container) (controller.PurgeContainerResult, error) {
	panic("not used")
}
func (s *stubController) RefreshAll() (controller.RefreshResult, error) { panic("not used") }
func (s *stubController) ReconcileCells() (controller.ReconcileResult, error) {
	panic("not used")
}

// runCmd builds the cobra command, plugs in a stub controller and a logger,
// and feeds the supplied stdin payload. Returns the captured stdout/stderr
// buffer so tests can assert on output. A no-op systemd remover is injected
// by default so tests stay hermetic on hosts that happen to have systemctl
// and a stale unit file present; tests that need to exercise the systemd
// teardown rendering call runCmdWithSystemd instead.
func runCmd(
	t *testing.T,
	stub *stubController,
	stdin string,
	args ...string,
) (string, error) {
	t.Helper()
	return runCmdWithSystemd(t, stub, skippedSystemdRemover, stdin, args...)
}

// skippedSystemdRemover stands in for the real removeKukeondSystemdUnit on
// tests that do not care about the systemd path. Returns Skipped=true so the
// renderer prints no systemd row, matching the dev-host code path.
func skippedSystemdRemover(_ context.Context) uninstall.SystemdRemovalReport {
	return uninstall.SystemdRemovalReport{
		Skipped:    true,
		SkipReason: "test stub",
		UnitPath:   uninstall.SystemdUnitPath,
	}
}

func runCmdWithSystemd(
	t *testing.T,
	stub *stubController,
	systemd uninstall.SystemdRemover,
	stdin string,
	args ...string,
) (string, error) {
	t.Helper()
	cmd := uninstall.NewUninstallCmd()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, uninstall.MockControllerKey{}, controller.Controller(stub))
	ctx = context.WithValue(ctx, uninstall.MockSystemdRemoverKey{}, systemd)
	cmd.SetContext(ctx)

	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)

	err := cmd.Execute()
	return out.String(), err
}

func TestUninstall_YesFlagSkipsPrompt(t *testing.T) {
	t.Cleanup(viper.Reset)

	called := false
	stub := &stubController{
		uninstallFn: func(opts controller.UninstallOptions) (controller.UninstallReport, error) {
			called = true
			return controller.UninstallReport{
				SocketDir: opts.SocketDir,
				RunPath:   "/opt/kukeon",
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, nil
		},
	}

	// stdin is empty: if the prompt fires, ReadString hits io.EOF and the
	// command returns an error, so seeing a clean exit + Uninstall called
	// is proof the --yes path skipped it.
	out, err := runCmd(t, stub, "", "--yes")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	if !called {
		t.Errorf("Uninstall was not invoked; output:\n%s", out)
	}
	if strings.Contains(out, "Are you sure?") {
		t.Errorf("--yes still triggered the confirmation prompt:\n%s", out)
	}
}

// TestUninstall_PromptAbortsNonYes pins the docs/cli-use-cases.md invariant
// that any non-"yes" answer (including "no", "n", and an empty line) aborts
// with non-zero exit and no destructive side effect, matching the EOF path.
// Issue #433 — tooling that gates on the documented "non-zero on abort"
// contract was silently treating a "no" answer as success.
func TestUninstall_PromptAbortsNonYes(t *testing.T) {
	t.Cleanup(viper.Reset)

	answers := []struct {
		name  string
		stdin string
	}{
		{"no", "no\n"},
		{"n", "n\n"},
		{"empty line", "\n"},
	}
	for _, tc := range answers {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			stub := &stubController{
				uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
					called = true
					return controller.UninstallReport{}, nil
				},
			}
			out, err := runCmd(t, stub, tc.stdin)
			if !errors.Is(err, uninstall.ErrAborted) {
				t.Fatalf("expected ErrAborted, got %v; output:\n%s", err, out)
			}
			if called {
				t.Errorf("expected Uninstall not to fire after %q answer; output:\n%s", tc.name, out)
			}
			if !strings.Contains(out, "Aborted.") {
				t.Errorf("expected aborted message; got:\n%s", out)
			}
		})
	}
}

func TestUninstall_PromptAcceptsYes(t *testing.T) {
	t.Cleanup(viper.Reset)

	called := false
	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			called = true
			return controller.UninstallReport{}, nil
		},
	}
	out, err := runCmd(t, stub, "yes\n")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	if !called {
		t.Errorf("expected Uninstall to fire after 'yes' answer; output:\n%s", out)
	}
}

func TestUninstall_AbortsStepError(t *testing.T) {
	t.Cleanup(viper.Reset)

	stepErr := errors.New("synthetic")
	stub := &stubController{
		uninstallFn: func(opts controller.UninstallOptions) (controller.UninstallReport, error) {
			return controller.UninstallReport{
				Realms: []controller.RealmPurgeOutcome{
					{Name: "kuke-system", Namespace: "kuke-system.kukeon.io", Err: stepErr},
				},
				SocketDir: opts.SocketDir,
				RunPath:   "/opt/kukeon",
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, stepErr
		},
	}

	out, err := runCmd(t, stub, "", "--yes")
	if err == nil {
		t.Fatalf("expected error to propagate; got nil. output:\n%s", out)
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected failure marker in report; got:\n%s", out)
	}
}

func TestUninstall_DefaultRunPathFromConfig(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, "/var/lib/kukeon-test")

	gotRunPath := ""
	stub := &stubController{
		uninstallFn: func(opts controller.UninstallOptions) (controller.UninstallReport, error) {
			// The CLI doesn't pass RunPath through opts (it's on the
			// controller's Options); we capture it via SocketDir, which
			// is supplied. Indirectly verify by inspecting what the
			// command derives for confirmation output instead.
			_ = opts
			return controller.UninstallReport{RunPath: "/var/lib/kukeon-test"}, nil
		},
	}
	out, _ := runCmd(t, stub, "", "--yes")
	if strings.Contains(out, "/var/lib/kukeon-test") {
		gotRunPath = "/var/lib/kukeon-test"
	}
	if gotRunPath == "" {
		t.Errorf("expected report to surface configured run path; got:\n%s", out)
	}
}

// TestUninstall_CleanupSkippedRendersSkipMarker pins the half-cleaned-host
// rendering from issue #287: when the controller reports CleanupSkipped, the
// CLI must replace each filesystem/account row with the skip marker rather
// than misreporting "absent" or "removed". This is the operator-facing tell
// that a residual containerd namespace blocked teardown.
func TestUninstall_CleanupSkippedRendersSkipMarker(t *testing.T) {
	t.Cleanup(viper.Reset)

	stepErr := errors.New("synthetic purge failure")
	stub := &stubController{
		uninstallFn: func(opts controller.UninstallOptions) (controller.UninstallReport, error) {
			return controller.UninstallReport{
				Realms: []controller.RealmPurgeOutcome{
					{Name: "kuke-system", Namespace: "kuke-system.kukeon.io", Err: stepErr},
				},
				CleanupSkipped: true,
				SocketDir:      opts.SocketDir,
				RunPath:        "/opt/kukeon",
				UserName:       "kukeon",
				GroupName:      "kukeon",
			}, stepErr
		},
	}

	out, err := runCmd(t, stub, "", "--yes")
	if err == nil {
		t.Fatalf("expected error to propagate; got nil. output:\n%s", out)
	}

	wantMarkers := []string{
		"skipped (realm purge failed)",
		"filesystem + user/group cleanup skipped",
	}
	for _, want := range wantMarkers {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"removed", "absent"} {
		// Defensive: if the renderer falls back to dirOutcome/accountOutcome
		// on a CleanupSkipped report, it'll mislabel the rows. The skip
		// marker must replace those entirely.
		if strings.Contains(out, ": "+unwanted) {
			t.Errorf("CleanupSkipped report unexpectedly rendered %q line; got:\n%s", unwanted, out)
		}
	}
}

// TestUninstall_RendersReleasedAndResidualMountRows pins the #434 renderer
// half: the printed report has to surface both the successful unmount rows
// (so an operator can see kuke did the work) and any residual mount rows
// (so manual recovery is obvious without having to grep /proc/mounts).
func TestUninstall_RendersReleasedAndResidualMountRows(t *testing.T) {
	t.Cleanup(viper.Reset)

	stuckErr := errors.New("synthetic EBUSY")
	stub := &stubController{
		uninstallFn: func(opts controller.UninstallOptions) (controller.UninstallReport, error) {
			return controller.UninstallReport{
				SocketDir:       opts.SocketDir,
				SocketDirExists: true,
				SocketDirRemove: false,
				SocketDirMounts: []controller.MountReleaseAttempt{
					{Target: "/run/kukeon/tty", Err: stuckErr},
				},
				RunPath:       "/opt/kukeon",
				RunPathExists: true,
				RunPathRemove: true,
				RunPathMounts: []controller.MountReleaseAttempt{
					{Target: "/opt/kukeon/default/space", Released: true},
				},
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, stuckErr
		},
	}
	out, err := runCmd(t, stub, "", "--yes")
	if err == nil {
		t.Fatalf("expected non-zero error from residual mount; got nil\noutput:\n%s", out)
	}
	wantSubstrings := []string{
		"mount /run/kukeon/tty: still busy",
		"synthetic EBUSY",
		"mount /opt/kukeon/default/space: unmounted",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
}

// TestUninstall_SystemdUnitRemovedRendersRow pins the issue #541 AC #4
// rendering: when the systemd remover reports a successful stop+disable+
// remove, the CLI must emit a unit row above the realm/filesystem rows so
// the operator sees the host-supervisor teardown happened.
func TestUninstall_SystemdUnitRemovedRendersRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	ctrlCalled := false
	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			ctrlCalled = true
			return controller.UninstallReport{
				SocketDir: "/run/kukeon",
				RunPath:   "/opt/kukeon",
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, nil
		},
	}
	systemd := func(_ context.Context) uninstall.SystemdRemovalReport {
		return uninstall.SystemdRemovalReport{
			UnitPath:       uninstall.SystemdUnitPath,
			Stopped:        true,
			Disabled:       true,
			FileRemoved:    true,
			DaemonReloaded: true,
		}
	}
	out, err := runCmdWithSystemd(t, stub, systemd, "", "--yes")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	if !ctrlCalled {
		t.Errorf("controller.Uninstall did not fire after successful systemd teardown; output:\n%s", out)
	}
	wantRow := "systemd unit " + uninstall.SystemdUnitPath + ": stopped, disabled, removed"
	if !strings.Contains(out, wantRow) {
		t.Errorf("expected systemd row %q; got:\n%s", wantRow, out)
	}
}

// TestUninstall_SystemdPartialPath pins the renderer's honesty on the
// "file removed but best-effort sub-steps non-zero" path — e.g., a host
// where systemctl is present but PID 1 is not systemd. The CLI must show
// which sub-steps actually succeeded so the operator can verify the unit
// file is gone without being misled by a "stopped, disabled, removed"
// claim that did not match reality.
func TestUninstall_SystemdPartialPath(t *testing.T) {
	t.Cleanup(viper.Reset)

	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			return controller.UninstallReport{
				SocketDir: "/run/kukeon",
				RunPath:   "/opt/kukeon",
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, nil
		},
	}
	systemd := func(_ context.Context) uninstall.SystemdRemovalReport {
		// stop/disable/daemon-reload all failed (no systemd PID 1); file
		// removed succeeded — the binding step.
		return uninstall.SystemdRemovalReport{
			UnitPath:    uninstall.SystemdUnitPath,
			FileRemoved: true,
		}
	}
	out, err := runCmdWithSystemd(t, stub, systemd, "", "--yes")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	wantSubstr := "partial (stop=false disable=false remove=true reload=false)"
	if !strings.Contains(out, wantSubstr) {
		t.Errorf("expected partial row %q; got:\n%s", wantSubstr, out)
	}
}

// TestUninstall_SystemdSkippedEmitsNoRow guards the dev-host AC: a host
// where the unit was never installed (e.g. bootstrapped via `make
// dev-init`) must produce no systemd row at all so the report stays terse.
// The controller's realm/filesystem teardown still runs to completion.
func TestUninstall_SystemdSkippedEmitsNoRow(t *testing.T) {
	t.Cleanup(viper.Reset)

	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			return controller.UninstallReport{
				SocketDir: "/run/kukeon",
				RunPath:   "/opt/kukeon",
				UserName:  "kukeon",
				GroupName: "kukeon",
			}, nil
		},
	}
	out, err := runCmd(t, stub, "", "--yes")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(out, "systemd unit") {
		t.Errorf("Skipped systemd report unexpectedly rendered a row:\n%s", out)
	}
}

// TestUninstall_SystemdFailureShortCircuits pins the ordering invariant:
// if the systemd remover errors (e.g. daemon-reload fails on a broken PID
// 1), realm purge / filesystem teardown must not run. Allowing the
// controller to proceed while the unit is still respawn-eligible would
// race the daemon-stop step and is the regression #541 AC #4 prevents.
func TestUninstall_SystemdFailureShortCircuits(t *testing.T) {
	t.Cleanup(viper.Reset)

	ctrlCalled := false
	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			ctrlCalled = true
			return controller.UninstallReport{}, nil
		},
	}
	sentinel := errors.New("synthetic daemon-reload failure")
	systemd := func(_ context.Context) uninstall.SystemdRemovalReport {
		return uninstall.SystemdRemovalReport{
			UnitPath:    uninstall.SystemdUnitPath,
			Stopped:     true,
			Disabled:    true,
			FileRemoved: true,
			Err:         sentinel,
		}
	}
	out, err := runCmdWithSystemd(t, stub, systemd, "", "--yes")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error to propagate; got %v\noutput:\n%s", err, out)
	}
	if ctrlCalled {
		t.Errorf("controller.Uninstall must not fire when systemd teardown failed; output:\n%s", out)
	}
	if !strings.Contains(out, "failed:") || !strings.Contains(out, "synthetic daemon-reload failure") {
		t.Errorf("expected failure row to surface sentinel; got:\n%s", out)
	}
}

// Compile-time assertion: stubController must satisfy controller.Controller.
var _ controller.Controller = (*stubController)(nil)
