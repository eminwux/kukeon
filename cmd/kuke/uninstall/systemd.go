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

package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// SystemdUnitPath is the absolute path of the kukeond systemd unit
// scripts/install.sh writes. Exported so the test package can match against
// it without re-spelling the literal.
const SystemdUnitPath = "/etc/systemd/system/kukeond.service"

// SystemdUnitName is the canonical short name the systemctl verbs accept.
const SystemdUnitName = "kukeond.service"

// WebUISystemdUnitPath is the absolute path of the web UI systemd unit
// scripts/install.sh writes (install_web_ui_unit). Absent on hosts installed
// with KUKE_SKIP_WEB_UI=1, on systemd-less hosts and on hand-built hosts.
const WebUISystemdUnitPath = "/etc/systemd/system/kuke-serve.service"

// WebUISystemdUnitName is the short name of the web UI unit.
const WebUISystemdUnitName = "kuke-serve.service"

// SystemdUnit names one systemd unit kuke uninstall tears down: the short
// name the systemctl verbs accept and the unit file the installer wrote.
type SystemdUnit struct {
	Name string
	Path string
}

// systemdUnits returns the units kuke uninstall removes, in teardown order.
// The web UI goes first: kuke serve is a client of kukeond, and stopping it
// before the daemon keeps a burst of failed requests out of its journal.
func systemdUnits() []SystemdUnit {
	return []SystemdUnit{
		{Name: WebUISystemdUnitName, Path: WebUISystemdUnitPath},
		{Name: SystemdUnitName, Path: SystemdUnitPath},
	}
}

// SystemdRemovalReport summarizes what the remover did to one unit. Every
// field is independently surfaced in the CLI report so the operator can tell
// which sub-step ran on a partially-supervised host (e.g., systemctl present
// but the unit was hand-deleted before re-running uninstall).
type SystemdRemovalReport struct {
	// Skipped is true when the helper short-circuited without touching the
	// unit (no systemctl in PATH, or no unit file). SkipReason names the
	// branch so the printed report is self-explanatory.
	Skipped    bool
	SkipReason string
	// UnitPath is the absolute unit file path the helper attempted to
	// disable; carried in the report so a future change to a unit path
	// does not require chasing the renderer to update its hard-coded text.
	UnitPath string
	// Stopped/Disabled/FileRemoved/DaemonReloaded each flip true on success
	// of the corresponding sub-step. Stop / disable / daemon-reload are
	// best-effort — the binding step is FileRemoved. The fields are
	// independent so the renderer can show (e.g.) "stop+disable failed
	// (no systemd PID 1) but file removed" on partial paths. One
	// daemon-reload runs after every unit is handled, so DaemonReloaded
	// carries that single reload's outcome on each removed unit.
	Stopped        bool
	Disabled       bool
	FileRemoved    bool
	DaemonReloaded bool
	// Err carries the binding-step error so runUninstall can propagate a
	// non-zero exit. Best-effort sub-steps (stop / disable / daemon-reload)
	// do not populate Err — their outcomes are visible via their bool
	// fields. The report is still returned in full on error so the operator
	// sees what did succeed before the failure.
	Err error
}

// SystemdRemover is the contract removeSystemdUnits and any test substitute
// satisfy: one report per unit, in teardown order. Injected via
// MockSystemdRemoverKey so tests need not touch the host's systemctl or
// /etc/systemd/system.
type SystemdRemover func(ctx context.Context) []SystemdRemovalReport

// MockSystemdRemoverKey injects a SystemdRemover via cmd.Context() for tests
// in the same package layout as MockControllerKey.
type MockSystemdRemoverKey struct{}

// systemdHost holds the host seams the unit teardown calls, so tests can
// drive it without a real systemctl. Unit file stat/remove go through the
// filesystem directly; tests point the unit paths at a temp dir.
type systemdHost struct {
	lookPath  func(file string) (string, error)
	systemctl func(ctx context.Context, args ...string) error
}

func execSystemctl(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, "systemctl", args...).Run()
}

// removeSystemdUnits stops, disables, and removes the systemd units installed
// by scripts/install.sh (kuke-serve.service, then kukeond.service), then runs
// one `systemctl daemon-reload` so subsequent systemctl calls see the absent
// units. Idempotent: a host without systemctl in PATH, or one where a unit
// file is already gone, reports that unit with Skipped=true, so `kuke
// uninstall` is safe to run on dev hosts that bootstrapped via `make
// dev-init` (issue #541 AC #4) and on hosts installed with
// KUKE_SKIP_WEB_UI=1.
//
// The stop+disable pair runs before realm purge / filesystem teardown
// because a still-enabled Restart=on-failure unit would race the
// controller's PID-file-based daemon stop and respawn kukeond mid-uninstall.
func removeSystemdUnits(ctx context.Context) []SystemdRemovalReport {
	host := systemdHost{lookPath: exec.LookPath, systemctl: execSystemctl}
	return host.removeUnits(ctx, systemdUnits())
}

// removeUnits tears down each unit in order and then reloads systemd once
// if any unit file was removed. A binding-step failure on one unit does not
// stop the others: each unit's outcome is reported, and runUninstall fails
// on any Err before the realm teardown.
func (h systemdHost) removeUnits(ctx context.Context, units []SystemdUnit) []SystemdRemovalReport {
	reports := make([]SystemdRemovalReport, 0, len(units))
	if _, err := h.lookPath("systemctl"); err != nil {
		for _, u := range units {
			reports = append(reports, SystemdRemovalReport{
				UnitPath:   u.Path,
				Skipped:    true,
				SkipReason: "systemctl not found in PATH",
			})
		}
		return reports
	}

	anyRemoved := false
	for _, u := range units {
		r := h.removeUnit(ctx, u)
		anyRemoved = anyRemoved || r.FileRemoved
		reports = append(reports, r)
	}
	if !anyRemoved {
		return reports
	}

	// daemon-reload is best-effort, like stop and disable: once the unit
	// files are gone, no future `systemctl` call will find the units after
	// the next reload, whoever triggers it.
	reloaded := h.systemctl(ctx, "daemon-reload") == nil
	for i := range reports {
		if reports[i].FileRemoved {
			reports[i].DaemonReloaded = reloaded
		}
	}
	return reports
}

// removeUnit stops, disables, and removes one unit file. It does not run
// daemon-reload; removeUnits runs it once for every unit.
func (h systemdHost) removeUnit(ctx context.Context, u SystemdUnit) SystemdRemovalReport {
	report := SystemdRemovalReport{UnitPath: u.Path}

	if _, statErr := os.Stat(u.Path); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			report.Skipped = true
			report.SkipReason = "unit file absent"
			return report
		}
		report.Err = fmt.Errorf("stat unit %q: %w", u.Path, statErr)
		return report
	}

	// stop and disable are best-effort: their non-zero exits do not block
	// the uninstall. The binding step is the file remove — once the unit
	// file is gone, no future `systemctl` call will find the unit
	// regardless of the prior stop / disable / reload outcomes. Non-zero
	// exits here are expected on three benign paths:
	//   1. The unit is already stopped / already disabled (idempotent re-run).
	//   2. systemctl is present but PID 1 is not systemd (some minimal
	//      container images ship systemctl as a no-op binary).
	//   3. The unit was hand-edited / corrupted and systemd refuses to act
	//      on it; the operator wants the file gone anyway.
	// Surfacing these as uninstall failures would break the
	// "uninstall still works on hosts where the unit was never installed"
	// AC for any of those partial-state paths.
	if err := h.systemctl(ctx, "stop", u.Name); err == nil {
		report.Stopped = true
	}
	if err := h.systemctl(ctx, "disable", u.Name); err == nil {
		report.Disabled = true
	}

	if rmErr := os.Remove(u.Path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		report.Err = fmt.Errorf("remove unit %q: %w", u.Path, rmErr)
		return report
	}
	report.FileRemoved = true
	return report
}
