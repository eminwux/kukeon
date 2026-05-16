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

// SystemdRemovalReport summarizes what removeKukeondSystemdUnit did. Every
// field is independently surfaced in the CLI report so the operator can tell
// which sub-step ran on a partially-supervised host (e.g., systemctl present
// but the unit was hand-deleted before re-running uninstall).
type SystemdRemovalReport struct {
	// Skipped is true when the helper short-circuited without touching the
	// host (no systemctl in PATH, or no unit file). SkipReason names the
	// branch so the printed report is self-explanatory.
	Skipped    bool
	SkipReason string
	// UnitPath is the absolute unit file path the helper attempted to
	// disable; carried in the report so a future change to SystemdUnitPath
	// does not require chasing the renderer to update its hard-coded text.
	UnitPath string
	// Stopped/Disabled/FileRemoved/DaemonReloaded each flip true on success
	// of the corresponding sub-step. Stop / disable / daemon-reload are
	// best-effort — the binding step is FileRemoved. The fields are
	// independent so the renderer can show (e.g.) "stop+disable failed
	// (no systemd PID 1) but file removed" on partial paths.
	Stopped        bool
	Disabled       bool
	FileRemoved    bool
	DaemonReloaded bool
	// Err carries the first non-nil binding-step error so runUninstall can
	// propagate a non-zero exit. Best-effort sub-steps (stop / disable /
	// daemon-reload) do not populate Err — their outcomes are visible via
	// their bool fields. The report is still returned in full on error so
	// the operator sees what did succeed before the failure.
	Err error
}

// SystemdRemover is the contract removeKukeondSystemdUnit and any test
// substitute satisfy. Injected via MockSystemdRemoverKey so tests need not
// touch the host's systemctl or /etc/systemd/system.
type SystemdRemover func(ctx context.Context) SystemdRemovalReport

// MockSystemdRemoverKey injects a SystemdRemover via cmd.Context() for tests
// in the same package layout as MockControllerKey.
type MockSystemdRemoverKey struct{}

// removeKukeondSystemdUnit stops, disables, and removes the kukeond systemd
// unit installed by scripts/install.sh, then runs `systemctl daemon-reload`
// so subsequent systemctl calls see the absent unit. Idempotent: a host
// without systemctl in PATH, or one where the unit file is already gone,
// short-circuits with Skipped=true so `kuke uninstall` is safe to run on
// dev hosts that bootstrapped via `make dev-init` (issue #541 AC #4).
//
// The stop+disable pair runs before realm purge / filesystem teardown
// because a still-enabled Restart=on-failure unit would race the
// controller's PID-file-based daemon stop and respawn kukeond mid-uninstall.
func removeKukeondSystemdUnit(ctx context.Context) SystemdRemovalReport {
	report := SystemdRemovalReport{UnitPath: SystemdUnitPath}

	if _, err := exec.LookPath("systemctl"); err != nil {
		report.Skipped = true
		report.SkipReason = "systemctl not found in PATH"
		return report
	}
	if _, statErr := os.Stat(SystemdUnitPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			report.Skipped = true
			report.SkipReason = "unit file absent"
			return report
		}
		report.Err = fmt.Errorf("stat unit %q: %w", SystemdUnitPath, statErr)
		return report
	}

	// stop, disable, and daemon-reload are best-effort: their non-zero
	// exits do not block the uninstall. The binding step is the file
	// remove — once the unit file is gone, no future `systemctl` call will
	// find kukeond.service regardless of the prior stop / disable / reload
	// outcomes. Non-zero exits here are expected on three benign paths:
	//   1. The unit is already stopped / already disabled (idempotent re-run).
	//   2. systemctl is present but PID 1 is not systemd (some minimal
	//      container images ship systemctl as a no-op binary).
	//   3. The unit was hand-edited / corrupted and systemd refuses to act
	//      on it; the operator wants the file gone anyway.
	// Surfacing these as uninstall failures would break the
	// "uninstall still works on hosts where the unit was never installed"
	// AC for any of those partial-state paths.
	if err := exec.CommandContext(ctx, "systemctl", "stop", SystemdUnitName).Run(); err == nil {
		report.Stopped = true
	}
	if err := exec.CommandContext(ctx, "systemctl", "disable", SystemdUnitName).Run(); err == nil {
		report.Disabled = true
	}

	if rmErr := os.Remove(SystemdUnitPath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		report.Err = fmt.Errorf("remove unit %q: %w", SystemdUnitPath, rmErr)
		return report
	}
	report.FileRemoved = true

	if err := exec.CommandContext(ctx, "systemctl", "daemon-reload").Run(); err == nil {
		report.DaemonReloaded = true
	}
	return report
}
