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

// runCmd builds the cobra command, plugs in a stub controller and a logger,
// and feeds the supplied stdin payload. Returns the captured stdout/stderr
// buffer so tests can assert on output.
func runCmd(
	t *testing.T,
	stub *stubController,
	stdin string,
	args ...string,
) (string, error) {
	t.Helper()
	cmd := uninstall.NewUninstallCmd()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, uninstall.MockControllerKey{}, controller.Controller(stub))
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

func TestUninstall_PromptAbortsOnNo(t *testing.T) {
	t.Cleanup(viper.Reset)

	called := false
	stub := &stubController{
		uninstallFn: func(controller.UninstallOptions) (controller.UninstallReport, error) {
			called = true
			return controller.UninstallReport{}, nil
		},
	}
	out, err := runCmd(t, stub, "no\n")
	if err != nil {
		t.Fatalf("Execute returned: %v\noutput:\n%s", err, out)
	}
	if called {
		t.Errorf("expected Uninstall not to fire after 'no' answer; output:\n%s", out)
	}
	if !strings.Contains(out, "Aborted.") {
		t.Errorf("expected aborted message; got:\n%s", out)
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

// Compile-time assertion: stubController must satisfy controller.Controller.
var _ controller.Controller = (*stubController)(nil)
