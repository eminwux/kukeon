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

// Tests for the CellConfig scheduler (issue #1503): what a pass fires, what it
// skips, and what it records in status.schedule.

package controller_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const scheduleConfigName = "disk-watch"

// scheduleHarness is a host with CellConfigs, their stored schedule state and
// the cells their fires created, served through the fake runner. The
// scheduler's cell create is replaced by one that records the request and
// adds a Ready cell, so tests drive RunSchedules pass by pass.
type scheduleHarness struct {
	t    *testing.T
	ctrl *controller.Exec
	mock *fakeRunner

	configs  map[string]ext.CellConfigDoc
	statuses map[string]intmodel.CellConfigScheduleStatus
	cells    []intmodel.Cell
	fires    []controller.CellFromConfigRequest
	// fireErr, when set, is what the next fires return instead of creating.
	fireErr error
	// statusWrites counts WriteConfigScheduleStatus calls.
	statusWrites int
	prev         time.Time
}

func newScheduleHarness(t *testing.T) *scheduleHarness {
	t.Helper()
	h := &scheduleHarness{
		t:        t,
		configs:  map[string]ext.CellConfigDoc{},
		statuses: map[string]intmodel.CellConfigScheduleStatus{},
	}
	h.restartDaemon()
	return h
}

// restartDaemon replaces the controller, dropping the scheduler's in-memory
// state the way a daemon restart does. Stored configs, status and cells stay.
func (h *scheduleHarness) restartDaemon() {
	h.t.Helper()
	mock := &fakeRunner{
		ListConfigsFn: func(string, string, string) ([]intmodel.CellConfig, error) {
			names := make([]string, 0, len(h.configs))
			for name := range h.configs {
				names = append(names, name)
			}
			slices.Sort(names)
			out := make([]intmodel.CellConfig, 0, len(names))
			for _, name := range names {
				carrier := configCarrier(h.t, h.configs[name])
				carrier.Document = nil // a list is metadata-only
				out = append(out, carrier)
			}
			return out, nil
		},
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			doc, ok := h.configs[c.Metadata.Name]
			if !ok {
				return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
			}
			return configCarrier(h.t, doc), nil
		},
		GetConfigScheduleStatusFn: func(c intmodel.CellConfig) (intmodel.CellConfigScheduleStatus, bool, error) {
			status, ok := h.statuses[c.Metadata.Name]
			return status, ok, nil
		},
		WriteConfigScheduleStatusFn: func(c intmodel.CellConfig, status intmodel.CellConfigScheduleStatus) error {
			h.statusWrites++
			status.ActiveCells = slices.Clone(status.ActiveCells)
			h.statuses[c.Metadata.Name] = status
			return nil
		},
		DeleteConfigScheduleStatusFn: func(c intmodel.CellConfig) error {
			delete(h.statuses, c.Metadata.Name)
			return nil
		},
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) {
			return slices.Clone(h.cells), nil
		},
		ReconcileCellFn: func(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
			return c, runner.ReconcileOutcome{}, nil
		},
	}
	h.mock = mock
	h.ctrl = setupTestController(h.t, mock)
	h.ctrl.SetScheduleFireForTest(func(req controller.CellFromConfigRequest) error {
		h.fires = append(h.fires, req)
		if h.fireErr != nil {
			return h.fireErr
		}
		h.cells = append(h.cells, scheduleCellFor(req.Name, req.Schedule, req.Config, intmodel.CellStateReady))
		return nil
	})
	h.prev = time.Time{}
}

// runner returns the fake runner behind the current controller, for a test
// that needs to change one of its answers.
func (h *scheduleHarness) runner() *fakeRunner { return h.mock }

// scheduleCell builds a cell in the default scope; schedule, when set, is the
// name of the stack-scoped default/default/default Config whose schedule fired
// it (the scope scheduledConfig uses).
func scheduleCell(name, schedule string, state intmodel.CellState) intmodel.Cell {
	return scheduleCellFor(name, schedule, intmodel.CellConfigMetadata{
		Name: schedule, Realm: "default", Space: "default", Stack: "default",
	}, state)
}

// scheduleCellFor builds a cell in the default scope; schedule, when set, is
// the name of the Config at scope config whose schedule fired it, recorded the
// way CreateCellFromConfig records it: the kukeon.io/schedule label plus a
// config provenance pointing at the Config's own scope.
func scheduleCellFor(
	name, schedule string, config intmodel.CellConfigMetadata, state intmodel.CellState,
) intmodel.Cell {
	cell := intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name, Labels: map[string]string{}},
		Spec:     intmodel.CellSpec{RealmName: "default", SpaceName: "default", StackName: "default"},
		Status:   intmodel.CellStatus{State: state},
	}
	if schedule != "" {
		cell.Metadata.Labels[ext.LabelSchedule] = schedule
		cell.Spec.Provenance = &intmodel.CellProvenance{
			BindingKind: ext.BindingKindConfig,
			BindingRef: intmodel.CellBindingRef{
				Name: config.Name, Realm: config.Realm, Space: config.Space, Stack: config.Stack,
			},
		}
	}
	return cell
}

func scheduledConfig(cron string) ext.CellConfigDoc {
	return ext.CellConfigDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellConfig,
		Metadata: ext.CellConfigMetadata{
			Name: scheduleConfigName, Realm: "default", Space: "default", Stack: "default",
		},
		Spec: ext.CellConfigSpec{
			Blueprint: ext.CellConfigBlueprintRef{Name: "disk-watch", Realm: "default"},
			Schedule:  &ext.CellConfigSchedule{Cron: cron},
		},
	}
}

func at(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

// pass runs one scheduler pass at the given time, threading the previous
// pass's time through the way kukeond's schedule loop does.
func (h *scheduleHarness) pass(now string) controller.ScheduleResult {
	h.t.Helper()
	when := at(h.t, now)
	res, err := h.ctrl.RunSchedules(h.prev, when)
	if err != nil {
		h.t.Fatalf("RunSchedules(%s) error = %v", now, err)
	}
	h.prev = when
	return res
}

func (h *scheduleHarness) status() intmodel.CellConfigScheduleStatus {
	h.t.Helper()
	status, ok := h.statuses[scheduleConfigName]
	if !ok {
		h.t.Fatalf("no schedule status stored for %q", scheduleConfigName)
	}
	return status
}

func (h *scheduleHarness) wantFires(want int) {
	h.t.Helper()
	if len(h.fires) != want {
		h.t.Fatalf("fires = %d, want %d (%+v)", len(h.fires), want, h.fires)
	}
}

func (h *scheduleHarness) wantNext(want string) {
	h.t.Helper()
	next := h.status().NextScheduleTime
	if want == "" {
		if next != nil {
			h.t.Fatalf("nextScheduleTime = %s, want unset", next.Format(time.RFC3339))
		}
		return
	}
	if next == nil || !next.Equal(at(h.t, want)) {
		h.t.Fatalf("nextScheduleTime = %v, want %s", next, want)
	}
}

func (h *scheduleHarness) wantSkipped(want int64) {
	h.t.Helper()
	if got := h.status().SkippedFires; got != want {
		h.t.Fatalf("skippedFires = %d, want %d", got, want)
	}
}

// finishCells moves every cell to Exited, as a run that ended would.
func (h *scheduleHarness) finishCells() {
	for i := range h.cells {
		h.cells[i].Status.State = intmodel.CellStateExited
	}
}

// TestRunSchedules_FiresOnSchedule: a fire creates and starts
// <config>-<unix-fire-time> from the Config, labelled kukeon.io/schedule and
// self-deleting, and status.schedule records it.
func TestRunSchedules_FiresOnSchedule(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")

	// The first pass after a start only anchors the schedule.
	res := h.pass("2026-11-17T08:22:19Z")
	h.wantFires(0)
	h.wantNext("2026-11-17T08:30:00Z")
	if res.Scheduled != 1 {
		t.Errorf("Scheduled = %d, want 1", res.Scheduled)
	}

	// Passes before the fire time do nothing and write nothing.
	writes := h.statusWrites
	h.pass("2026-11-17T08:29:00Z")
	h.wantFires(0)
	if h.statusWrites != writes {
		t.Errorf("an idle pass wrote status %d times", h.statusWrites-writes)
	}

	res = h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	fire := h.fires[0]
	fireTime := at(t, "2026-11-17T08:30:00Z")
	wantName := "disk-watch-1794904200"
	if fireTime.Unix() != 1794904200 {
		t.Fatalf("fixture drift: fire time unix = %d", fireTime.Unix())
	}
	if fire.Name != wantName {
		t.Errorf("cell name = %q, want %q", fire.Name, wantName)
	}
	if !fire.Start || !fire.AutoDelete {
		t.Errorf("fire Start=%v AutoDelete=%v, want both true", fire.Start, fire.AutoDelete)
	}
	if fire.Schedule != scheduleConfigName {
		t.Errorf("fire Schedule = %q, want %q", fire.Schedule, scheduleConfigName)
	}
	if fire.Config.Name != scheduleConfigName || fire.Config.Realm != "default" ||
		fire.Config.Space != "default" || fire.Config.Stack != "default" {
		t.Errorf("fire Config = %+v, want the Config's own scope", fire.Config)
	}
	if fire.Mode != "" || len(fire.Params) != 0 || len(fire.EnvOverrides) != 0 {
		t.Errorf("a fire must carry no per-fire overrides, got %+v", fire)
	}
	if res.Fired != 1 {
		t.Errorf("Fired = %d, want 1", res.Fired)
	}

	status := h.status()
	if status.LastScheduleTime == nil || !status.LastScheduleTime.Equal(fireTime) {
		t.Errorf("lastScheduleTime = %v, want %s", status.LastScheduleTime, fireTime)
	}
	if status.LastCell != wantName {
		t.Errorf("lastCell = %q, want %q", status.LastCell, wantName)
	}
	if !slices.Equal(status.ActiveCells, []string{wantName}) {
		t.Errorf("activeCells = %v, want [%s]", status.ActiveCells, wantName)
	}
	if status.SkippedFires != 0 || status.LastError != "" {
		t.Errorf("skippedFires=%d lastError=%q, want 0 and empty", status.SkippedFires, status.LastError)
	}
	h.wantNext("2026-11-17T08:40:00Z")
}

// TestRunSchedules_ConfigAppliedBetweenPasses: a Config applied since the
// previous pass still gets a fire time that falls before the next one.
func TestRunSchedules_ConfigAppliedBetweenPasses(t *testing.T) {
	h := newScheduleHarness(t)
	h.pass("2026-11-17T08:30:00Z")

	h.configs[scheduleConfigName] = scheduledConfig("31 8 * * *") // applied at 08:30:20
	h.pass("2026-11-17T08:31:00Z")
	h.wantFires(1)
	h.wantNext("2026-11-18T08:31:00Z")
}

// TestRunSchedules_ForbidSkipsWhileACellRuns: a fire is skipped and counted
// while a cell an earlier fire created is Pending, Ready, Degraded or Unknown;
// Stopped, Exited, Failed and Error cells do not hold it back.
func TestRunSchedules_ForbidSkipsWhileACellRuns(t *testing.T) {
	for _, tc := range []struct {
		name   string
		state  intmodel.CellState
		blocks bool
	}{
		{"Pending", intmodel.CellStatePending, true},
		{"Ready", intmodel.CellStateReady, true},
		{"Degraded", intmodel.CellStateDegraded, true},
		{"Unknown", intmodel.CellStateUnknown, true},
		{"Stopped", intmodel.CellStateStopped, false},
		{"Exited", intmodel.CellStateExited, false},
		{"Failed", intmodel.CellStateFailed, false},
		{"Error", intmodel.CellStateError, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newScheduleHarness(t)
			h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
			h.cells = []intmodel.Cell{scheduleCell("disk-watch-1794903600", scheduleConfigName, tc.state)}
			h.pass("2026-11-17T08:22:19Z")
			res := h.pass("2026-11-17T08:30:00Z")

			if tc.blocks {
				h.wantFires(0)
				h.wantSkipped(1)
				if res.Skipped != 1 {
					t.Errorf("Skipped = %d, want 1", res.Skipped)
				}
				if got := h.status().ActiveCells; !slices.Equal(got, []string{"disk-watch-1794903600"}) {
					t.Errorf("activeCells = %v, want the running cell", got)
				}
				if h.status().LastScheduleTime != nil {
					t.Errorf("a skipped fire set lastScheduleTime = %v", h.status().LastScheduleTime)
				}
			} else {
				h.wantFires(1)
				h.wantSkipped(0)
			}
			// Skipped or not, the schedule moves on to the next fire time.
			h.wantNext("2026-11-17T08:40:00Z")
		})
	}
}

// TestRunSchedules_ForbidResumesOnceTheCellEnds: skipped fires add up while a
// cell runs, and the first fire after it ends goes through.
func TestRunSchedules_ForbidResumesOnceTheCellEnds(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)

	h.pass("2026-11-17T08:40:00Z")
	h.pass("2026-11-17T08:50:00Z")
	h.wantFires(1)
	h.wantSkipped(2)

	h.finishCells()
	h.pass("2026-11-17T09:00:00Z")
	h.wantFires(2)
	h.wantSkipped(2)
	if got := h.status().ActiveCells; !slices.Equal(got, []string{"disk-watch-1794906000"}) {
		t.Errorf("activeCells = %v, want only the new cell", got)
	}
}

// TestRunSchedules_HandStartedCellsNeitherBlockNorAreBlocked: a cell started
// by hand from the same Config carries no schedule label, so it does not hold
// a fire back.
func TestRunSchedules_HandStartedCellsNeitherBlockNorAreBlocked(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.cells = []intmodel.Cell{
		scheduleCell("disk-watch-a1b2c3", "", intmodel.CellStateReady),
		// Another Config's scheduled cell is not this schedule's either.
		scheduleCell("other-1794903600", "other", intmodel.CellStateReady),
	}
	h.pass("2026-11-17T08:22:19Z")
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	h.wantSkipped(0)
	if got := h.status().ActiveCells; !slices.Equal(got, []string{"disk-watch-1794904200"}) {
		t.Errorf("activeCells = %v, want only the scheduled cell", got)
	}
}

// TestRunSchedules_ReapedCellsAreNotActive: a scheduled cell the reconcile
// reaps (self-deleting, run finished) or finds gone no longer holds a fire
// back, even when its listed state still reads Ready.
func TestRunSchedules_ReapedCellsAreNotActive(t *testing.T) {
	for name, outcome := range map[string]runner.ReconcileOutcome{
		"deleted":  {Deleted: true},
		"vanished": {Vanished: true},
	} {
		t.Run(name, func(t *testing.T) {
			h := newScheduleHarness(t)
			h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
			h.cells = []intmodel.Cell{
				scheduleCell("disk-watch-1794903600", scheduleConfigName, intmodel.CellStateReady),
			}
			mock := h.runner()
			mock.ReconcileCellFn = func(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
				if c.Metadata.Name == "disk-watch-1794903600" {
					return c, outcome, nil
				}
				return c, runner.ReconcileOutcome{}, nil
			}
			h.pass("2026-11-17T08:22:19Z")
			h.pass("2026-11-17T08:30:00Z")
			h.wantFires(1)
			h.wantSkipped(0)
		})
	}
}

// TestRunSchedules_ReconciledStateDecidesForbid: the state a scheduled cell is
// reconciled to — not the possibly stale listed one — decides whether it is
// still running, so schedules keep working with the reconcile loop off.
func TestRunSchedules_ReconciledStateDecidesForbid(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.cells = []intmodel.Cell{
		scheduleCell("disk-watch-1794903600", scheduleConfigName, intmodel.CellStateReady),
	}
	h.runner().ReconcileCellFn = func(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
		c.Status.State = intmodel.CellStateExited
		return c, runner.ReconcileOutcome{Updated: true}, nil
	}
	h.pass("2026-11-17T08:22:19Z")
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
}

// TestRunSchedules_Suspend: suspend stops new fires and clears the next fire
// time without touching running cells; clearing it resumes from the next
// scheduled time with no catch-up.
func TestRunSchedules_Suspend(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	cellsBefore := len(h.cells)

	cfg := scheduledConfig("*/10 * * * *")
	cfg.Spec.Schedule.Suspend = true
	h.configs[scheduleConfigName] = cfg
	h.finishCells()
	for _, now := range []string{"2026-11-17T08:40:00Z", "2026-11-17T08:50:00Z", "2026-11-17T09:00:00Z"} {
		h.pass(now)
	}
	h.wantFires(1)
	h.wantNext("")
	h.wantSkipped(0)
	if len(h.cells) != cellsBefore {
		t.Errorf("suspend changed the cell count: %d -> %d", cellsBefore, len(h.cells))
	}
	if h.status().LastCell != "disk-watch-1794904200" {
		t.Errorf("suspend dropped the fire history: %+v", h.status())
	}

	// Resumed at 09:03: the 08:40-09:00 fires are not caught up on.
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T09:04:00Z")
	h.wantFires(1)
	h.wantNext("2026-11-17T09:10:00Z")
	h.wantSkipped(0)

	h.pass("2026-11-17T09:10:00Z")
	h.wantFires(2)
}

// TestRunSchedules_MissedFiresAfterDowntime: of the fires missed while the
// daemon was down at most the latest runs, and only inside
// startingDeadlineSeconds; the rest — all of them without a deadline — are
// dropped and counted.
func TestRunSchedules_MissedFiresAfterDowntime(t *testing.T) {
	deadline := func(seconds int64) *int64 { return &seconds }
	for _, tc := range []struct {
		name        string
		deadline    *int64
		wantFire    string
		wantSkipped int64
	}{
		{name: "no deadline drops every missed fire", wantSkipped: 4},
		{name: "deadline too short drops every missed fire", deadline: deadline(60), wantSkipped: 4},
		{
			name: "deadline covers the latest missed fire", deadline: deadline(600),
			wantFire: "disk-watch-1794906000", wantSkipped: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newScheduleHarness(t)
			cfg := scheduledConfig("*/10 * * * *")
			cfg.Spec.Schedule.StartingDeadlineSeconds = tc.deadline
			h.configs[scheduleConfigName] = cfg
			h.pass("2026-11-17T08:22:19Z")
			h.wantNext("2026-11-17T08:30:00Z")

			// Down from 08:25 to 09:05: 08:30, 08:40, 08:50 and 09:00 are missed.
			h.restartDaemon()
			res := h.pass("2026-11-17T09:05:00Z")

			if tc.wantFire == "" {
				h.wantFires(0)
			} else {
				h.wantFires(1)
				if h.fires[0].Name != tc.wantFire {
					t.Errorf("fired %q, want %q", h.fires[0].Name, tc.wantFire)
				}
			}
			h.wantSkipped(tc.wantSkipped)
			if res.Skipped != tc.wantSkipped {
				t.Errorf("Skipped = %d, want %d", res.Skipped, tc.wantSkipped)
			}
			h.wantNext("2026-11-17T09:10:00Z")

			// The daemon is back: the next fire runs on time.
			h.finishCells()
			h.pass("2026-11-17T09:10:00Z")
			h.wantSkipped(tc.wantSkipped)
			if got := h.fires[len(h.fires)-1].Name; got != "disk-watch-1794906600" {
				t.Errorf("fire after recovery = %q, want disk-watch-1794906600", got)
			}
		})
	}
}

// TestRunSchedules_LatePassFiresOnce: fire times that came due while a pass
// was running are not "missed" — the daemon was up — but still only the
// latest runs; the others are counted.
func TestRunSchedules_LatePassFiresOnce(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("* * * * *")
	h.pass("2026-11-17T08:22:19Z")

	// The next pass is five minutes late: 08:23 through 08:27 are due.
	h.pass("2026-11-17T08:27:30Z")
	h.wantFires(1)
	if got, want := h.fires[0].Name, "disk-watch-1794904020"; got != want { // 08:27:00
		t.Errorf("fired %q, want %q", got, want)
	}
	h.wantSkipped(4)
	h.wantNext("2026-11-17T08:28:00Z")
}

// TestRunSchedules_FailedFire: a fire that cannot create its cell records
// lastError, is not retried before the next scheduled time, and does not stop
// later fires.
func TestRunSchedules_FailedFire(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")

	h.fireErr = errdefs.ErrConfigBlueprintNotFound
	res := h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	status := h.status()
	if !strings.Contains(status.LastError, errdefs.ErrConfigBlueprintNotFound.Error()) ||
		!strings.Contains(status.LastError, "2026-11-17T08:30:00Z") {
		t.Errorf("lastError = %q, want the fire time and the cause", status.LastError)
	}
	if status.LastScheduleTime != nil || status.LastCell != "" {
		t.Errorf("a failed fire recorded a cell: %+v", status)
	}
	if res.Failed != 1 || len(res.Errors) != 1 {
		t.Errorf("Failed=%d Errors=%v, want one of each", res.Failed, res.Errors)
	}
	h.wantNext("2026-11-17T08:40:00Z")

	// Not retried before the next fire time.
	for _, now := range []string{"2026-11-17T08:31:00Z", "2026-11-17T08:39:00Z"} {
		h.pass(now)
	}
	h.wantFires(1)

	// The cause is fixed: the next fire succeeds and clears the error.
	h.fireErr = nil
	h.pass("2026-11-17T08:40:00Z")
	h.wantFires(2)
	if got := h.status(); got.LastError != "" || got.LastCell != "disk-watch-1794904800" {
		t.Errorf("after recovery lastError=%q lastCell=%q", got.LastError, got.LastCell)
	}
}

// TestRunSchedules_FireIsIdempotent: a fire whose cell already exists — the
// daemon restarted between creating it and recording it — counts as fired
// rather than failing or being skipped as a concurrent run.
func TestRunSchedules_FireIsIdempotent(t *testing.T) {
	t.Run("the cell is already listed", func(t *testing.T) {
		h := newScheduleHarness(t)
		cfg := scheduledConfig("*/10 * * * *")
		deadline := int64(600)
		cfg.Spec.Schedule.StartingDeadlineSeconds = &deadline
		h.configs[scheduleConfigName] = cfg
		h.pass("2026-11-17T08:22:19Z")

		// The 08:30 fire created its cell, then the daemon died before
		// recording it: status still says next is 08:30.
		h.cells = []intmodel.Cell{
			scheduleCell("disk-watch-1794904200", scheduleConfigName, intmodel.CellStateReady),
		}
		h.restartDaemon()
		res := h.pass("2026-11-17T08:31:00Z")

		h.wantFires(0) // no second create
		h.wantSkipped(0)
		status := h.status()
		if status.LastCell != "disk-watch-1794904200" || status.LastScheduleTime == nil {
			t.Errorf("the existing cell was not recorded as the fire: %+v", status)
		}
		if res.Fired != 1 {
			t.Errorf("Fired = %d, want 1", res.Fired)
		}
		h.wantNext("2026-11-17T08:40:00Z")
	})

	t.Run("the create reports the cell exists", func(t *testing.T) {
		h := newScheduleHarness(t)
		h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
		h.pass("2026-11-17T08:22:19Z")

		// The cell appeared between the listing and the create.
		h.fireErr = errdefs.ErrCellAlreadyExists
		h.runner().GetCellFn = func(c intmodel.Cell) (intmodel.Cell, error) {
			return scheduleCell(c.Metadata.Name, scheduleConfigName, intmodel.CellStateReady), nil
		}
		res := h.pass("2026-11-17T08:30:00Z")
		status := h.status()
		if status.LastError != "" || status.LastCell != "disk-watch-1794904200" {
			t.Errorf("lastError=%q lastCell=%q, want the fire counted", status.LastError, status.LastCell)
		}
		if res.Fired != 1 || res.Failed != 0 {
			t.Errorf("Fired=%d Failed=%d, want 1 and 0", res.Fired, res.Failed)
		}
	})
}

// TestRunSchedules_RestartRecoveredFireWithoutDeadline: the daemon restarted
// between creating a fire's cell and recording it, and the schedule sets no
// startingDeadlineSeconds. The first pass after the restart sees the fire as
// missed during the downtime, which alone would drop it as skipped; its cell
// exists, so it is recorded as fired instead (issue #1553, was #1521).
func TestRunSchedules_RestartRecoveredFireWithoutDeadline(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")
	h.wantNext("2026-11-17T08:30:00Z")

	// The 08:30 fire created its cell, then the daemon died before recording
	// it: status still says next is 08:30.
	h.cells = []intmodel.Cell{
		scheduleCell("disk-watch-1794904200", scheduleConfigName, intmodel.CellStateReady),
	}
	h.restartDaemon()
	res := h.pass("2026-11-17T08:31:00Z")

	h.wantFires(0) // no second create
	h.wantSkipped(0)
	if res.Skipped != 0 || res.Fired != 1 {
		t.Errorf("Skipped=%d Fired=%d, want 0 and 1", res.Skipped, res.Fired)
	}
	status := h.status()
	if status.LastCell != "disk-watch-1794904200" {
		t.Errorf("lastCell = %q, want the existing cell", status.LastCell)
	}
	if status.LastScheduleTime == nil || !status.LastScheduleTime.Equal(at(t, "2026-11-17T08:30:00Z")) {
		t.Errorf("lastScheduleTime = %v, want 2026-11-17T08:30:00Z", status.LastScheduleTime)
	}
	h.wantNext("2026-11-17T08:40:00Z")
}

// TestRunSchedules_RestartRecoveredEarlierFire: the daemon died after creating
// one fire's cell, and the downtime ran past the next fire time too. The
// latest due fire is missed with no deadline and does not run, but the
// earlier fire's existing cell is recorded as the fire rather than skipped.
func TestRunSchedules_RestartRecoveredEarlierFire(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")

	// The 08:30 fire created its cell, then the daemon was down until after
	// 08:40.
	h.cells = []intmodel.Cell{
		scheduleCell("disk-watch-1794904200", scheduleConfigName, intmodel.CellStateExited),
	}
	h.restartDaemon()
	res := h.pass("2026-11-17T08:45:00Z")

	h.wantFires(0)
	h.wantSkipped(1) // 08:40 only
	if res.Skipped != 1 || res.Fired != 1 {
		t.Errorf("Skipped=%d Fired=%d, want 1 and 1", res.Skipped, res.Fired)
	}
	status := h.status()
	if status.LastCell != "disk-watch-1794904200" {
		t.Errorf("lastCell = %q, want the 08:30 fire's existing cell", status.LastCell)
	}
	if status.LastScheduleTime == nil || !status.LastScheduleTime.Equal(at(t, "2026-11-17T08:30:00Z")) {
		t.Errorf("lastScheduleTime = %v, want 2026-11-17T08:30:00Z", status.LastScheduleTime)
	}
	h.wantNext("2026-11-17T08:50:00Z")
}

// TestRunSchedules_ConfigDeletedDuringThePass: a Config deleted while a pass
// fires it — after the pass read it, before the pass wrote its schedule
// state — leaves no config-status/<name> behind, and one re-created under the
// same name starts with no schedule history (issue #1553, was #1522).
func TestRunSchedules_ConfigDeletedDuringThePass(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")

	h.ctrl.SetScheduleFireForTest(func(req controller.CellFromConfigRequest) error {
		h.fires = append(h.fires, req)
		h.cells = append(h.cells, scheduleCellFor(req.Name, req.Schedule, req.Config, intmodel.CellStateReady))
		// `kuke delete config` lands mid-pass: DeleteConfig removes the
		// document, then its schedule state.
		delete(h.configs, scheduleConfigName)
		delete(h.statuses, scheduleConfigName)
		return nil
	})
	res := h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	if len(res.Errors) != 0 {
		t.Errorf("Errors = %v, want none", res.Errors)
	}
	if status, ok := h.statuses[scheduleConfigName]; ok {
		t.Fatalf("schedule status re-created for a deleted Config: %+v", status)
	}

	// Re-created under the same name: no lastCell, lastScheduleTime or
	// skippedFires carried over from the deleted Config.
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:35:00Z")
	status := h.status()
	if status.LastCell != "" || status.LastScheduleTime != nil || status.SkippedFires != 0 {
		t.Errorf("re-created Config inherited schedule history: %+v", status)
	}
	h.wantNext("2026-11-17T08:40:00Z")
}

// TestRunSchedules_ConfigReappliedDuringThePass pins the residual
// dropStatusOfDeletedConfig documents: a Config deleted and re-applied under
// the same name while a pass is still firing it keeps the deleted Config's
// schedule state, because the re-read after the write finds a document and
// nothing tells the new one from the old.
func TestRunSchedules_ConfigReappliedDuringThePass(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")

	h.ctrl.SetScheduleFireForTest(func(req controller.CellFromConfigRequest) error {
		h.fires = append(h.fires, req)
		h.cells = append(h.cells, scheduleCellFor(req.Name, req.Schedule, req.Config, intmodel.CellStateReady))
		// `kuke delete config` then `kuke apply` of the same document, both
		// landing mid-pass.
		delete(h.configs, scheduleConfigName)
		delete(h.statuses, scheduleConfigName)
		h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
		return nil
	})
	h.pass("2026-11-17T08:30:00Z")

	status, ok := h.statuses[scheduleConfigName]
	if !ok {
		t.Fatal("schedule status removed for a Config that exists again; the residual changed — update the docstring")
	}
	if status.LastCell != "disk-watch-1794904200" {
		t.Errorf("lastCell = %q, want the deleted Config's fire kept (documented residual)", status.LastCell)
	}
}

// TestRunSchedules_RemovingTheScheduleStopsFires: a Config that loses its
// spec.schedule, or is deleted, fires no more; its running cells stay and its
// stored schedule state goes.
func TestRunSchedules_RemovingTheScheduleStopsFires(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(*scheduleHarness)
	}{
		{"schedule removed", func(h *scheduleHarness) {
			cfg := scheduledConfig("*/10 * * * *")
			cfg.Spec.Schedule = nil
			h.configs[scheduleConfigName] = cfg
		}},
		{"config deleted", func(h *scheduleHarness) {
			delete(h.configs, scheduleConfigName)
			delete(h.statuses, scheduleConfigName) // DeleteConfig removes it
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newScheduleHarness(t)
			h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
			h.pass("2026-11-17T08:22:19Z")
			h.pass("2026-11-17T08:30:00Z")
			h.wantFires(1)
			cellsBefore := len(h.cells)

			tc.remove(h)
			h.finishCells()
			for _, now := range []string{"2026-11-17T08:40:00Z", "2026-11-17T08:50:00Z"} {
				res := h.pass(now)
				if res.Scheduled != 0 {
					t.Errorf("Scheduled = %d, want 0", res.Scheduled)
				}
			}
			h.wantFires(1)
			if len(h.cells) != cellsBefore {
				t.Errorf("cells changed: %d -> %d", cellsBefore, len(h.cells))
			}
			if _, ok := h.statuses[scheduleConfigName]; ok {
				t.Errorf("schedule status survived: %+v", h.statuses[scheduleConfigName])
			}

			// Scheduling it again starts from a clean slate, with no catch-up.
			h.pass("2026-11-17T09:03:00Z")
			h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
			h.pass("2026-11-17T09:04:00Z")
			h.wantFires(1)
			h.wantNext("2026-11-17T09:10:00Z")
		})
	}
}

// TestRunSchedules_ChangedCronAppliesFromItsNextFireTime: after the cron is
// edited the stored next fire time, computed for the old expression, is
// dropped and the new expression's next fire time is used.
func TestRunSchedules_ChangedCronAppliesFromItsNextFireTime(t *testing.T) {
	t.Run("to a more frequent expression", func(t *testing.T) {
		h := newScheduleHarness(t)
		h.configs[scheduleConfigName] = scheduledConfig("0 * * * *")
		h.pass("2026-11-17T08:22:19Z")
		h.wantNext("2026-11-17T09:00:00Z")

		h.pass("2026-11-17T08:40:00Z")
		h.configs[scheduleConfigName] = scheduledConfig("*/15 * * * *") // edited at 08:40:30
		h.pass("2026-11-17T08:41:00Z")
		h.wantFires(0)
		h.wantNext("2026-11-17T08:45:00Z")
		h.pass("2026-11-17T08:45:00Z")
		h.wantFires(1)
	})

	t.Run("to a less frequent expression", func(t *testing.T) {
		h := newScheduleHarness(t)
		h.configs[scheduleConfigName] = scheduledConfig("*/15 * * * *")
		h.pass("2026-11-17T08:22:19Z")
		h.wantNext("2026-11-17T08:30:00Z")

		h.pass("2026-11-17T08:29:00Z")
		h.configs[scheduleConfigName] = scheduledConfig("0 * * * *") // edited at 08:29:30
		h.pass("2026-11-17T08:30:00Z")
		h.wantFires(0) // 08:30 belonged to the old expression
		h.wantNext("2026-11-17T09:00:00Z")
		h.wantSkipped(0)
	})

	t.Run("a changed time zone re-anchors too", func(t *testing.T) {
		h := newScheduleHarness(t)
		h.configs[scheduleConfigName] = scheduledConfig("0 9 * * *")
		h.pass("2026-11-17T08:22:19Z")
		h.wantNext("2026-11-17T09:00:00Z")

		cfg := scheduledConfig("0 9 * * *")
		cfg.Spec.Schedule.Timezone = "America/New_York" // 09:00 EST is 14:00 UTC
		h.configs[scheduleConfigName] = cfg
		h.pass("2026-11-17T08:23:00Z")
		h.wantNext("2026-11-17T14:00:00Z")
	})
}

// TestRunSchedules_HonorsTimezone: the cron expression is read in the
// schedule's time zone, and the times in status are reported in UTC.
func TestRunSchedules_HonorsTimezone(t *testing.T) {
	h := newScheduleHarness(t)
	cfg := scheduledConfig("0 9 * * *")
	cfg.Spec.Schedule.Timezone = "Europe/Madrid"
	h.configs[scheduleConfigName] = cfg

	h.pass("2026-11-17T07:30:00Z")
	h.wantNext("2026-11-17T08:00:00Z") // 09:00 CET
	if loc := h.status().NextScheduleTime.Location(); loc != time.UTC {
		t.Errorf("nextScheduleTime is stored in %s, want UTC", loc)
	}
	h.pass("2026-11-17T08:00:00Z")
	h.wantFires(1)
	if got, want := h.fires[0].Name, "disk-watch-1794902400"; got != want {
		t.Errorf("fired %q, want %q", got, want)
	}
}

// TestRunSchedules_SteadyStateSurvivesRestart: a restart with nothing missed
// keeps the stored next fire time and fires it on time.
func TestRunSchedules_SteadyStateSurvivesRestart(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")

	h.restartDaemon()
	h.pass("2026-11-17T08:25:00Z")
	h.wantFires(0)
	h.wantNext("2026-11-17T08:30:00Z")
	h.wantSkipped(0)
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
}

// TestRunSchedules_OneBadConfigDoesNotStopTheRest: a Config whose stored
// schedule cannot be parsed reports it in lastError and never fires, while
// the other Configs keep their schedule.
func TestRunSchedules_OneBadConfigDoesNotStopTheRest(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	bad := scheduledConfig("not a cron")
	bad.Metadata.Name = "broken"
	h.configs["broken"] = bad

	h.pass("2026-11-17T08:22:19Z")
	h.pass("2026-11-17T08:30:00Z")
	h.wantFires(1)
	if h.fires[0].Config.Name != scheduleConfigName {
		t.Errorf("fired %q, want only the valid Config", h.fires[0].Config.Name)
	}
	broken := h.statuses["broken"]
	if broken.LastError == "" || broken.NextScheduleTime != nil {
		t.Errorf("broken config status = %+v, want lastError and no next fire", broken)
	}
}

// TestRunSchedules_ListErrorIsReported: a pass that cannot list Configs
// reports it without failing, so the loop keeps ticking.
func TestRunSchedules_ListErrorIsReported(t *testing.T) {
	mock := &fakeRunner{
		ListConfigsFn: func(string, string, string) ([]intmodel.CellConfig, error) {
			return nil, errors.New("disk on fire")
		},
	}
	res, err := setupTestController(t, mock).RunSchedules(time.Time{}, time.Now())
	if err != nil {
		t.Fatalf("RunSchedules() error = %v, want nil", err)
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "disk on fire") {
		t.Errorf("Errors = %v, want the list failure", res.Errors)
	}
}

// TestPreviewCellFromConfig_ScheduledCell: a cell rendered for a schedule
// carries the kukeon.io/schedule label and is self-deleting; one rendered by
// hand from the same Config is neither.
func TestPreviewCellFromConfig_ScheduledCell(t *testing.T) {
	ctrl := setupTestController(t, newRenderRunner(t, &renderScope{}, renderConfig()))

	req := previewRequest()
	byHand, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig() error = %v", err)
	}
	if _, ok := byHand.Cell.Metadata.Labels[ext.LabelSchedule]; ok || byHand.Cell.Spec.AutoDelete {
		t.Errorf("a hand-made cell got the schedule label or autoDelete: labels=%v autoDelete=%v",
			byHand.Cell.Metadata.Labels, byHand.Cell.Spec.AutoDelete)
	}

	req.Schedule = "kukeon-dev"
	scheduled, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig() error = %v", err)
	}
	if got := scheduled.Cell.Metadata.Labels[ext.LabelSchedule]; got != "kukeon-dev" {
		t.Errorf("schedule label = %q, want kukeon-dev", got)
	}
	if !scheduled.Cell.Spec.AutoDelete {
		t.Error("a scheduled cell must be self-deleting")
	}
}

// TestReconcileCells_OutOfSync_ScheduleIsNotPartOfTheCell: adding, changing or
// removing a Config's spec.schedule leaves the cells made from it Synced —
// both the ones a schedule fired and the ones started by hand.
func TestReconcileCells_OutOfSync_ScheduleIsNotPartOfTheCell(t *testing.T) {
	withSchedule := func(schedule *ext.CellConfigSchedule) ext.CellConfigDoc {
		cfg := renderConfig()
		cfg.Spec.Schedule = schedule
		return cfg
	}
	for _, tc := range []struct {
		name          string
		before, after *ext.CellConfigSchedule
		scheduledCell bool
	}{
		{
			name:   "schedule changed, scheduled cell",
			before: &ext.CellConfigSchedule{Cron: "*/10 * * * *"},
			after:  &ext.CellConfigSchedule{Cron: "@daily", Timezone: "Europe/Madrid", Suspend: true},

			scheduledCell: true,
		},
		{
			name:          "schedule removed, scheduled cell",
			before:        &ext.CellConfigSchedule{Cron: "*/10 * * * *"},
			scheduledCell: true,
		},
		{
			name:  "schedule added, hand-started cell",
			after: &ext.CellConfigSchedule{Cron: "*/10 * * * *"},
		},
		{
			name:   "schedule removed, hand-started cell",
			before: &ext.CellConfigSchedule{Cron: "*/10 * * * *"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := withSchedule(tc.before)
			mock := newRenderRunner(t, &renderScope{}, renderConfig())
			mock.GetConfigFn = func(intmodel.CellConfig) (intmodel.CellConfig, error) {
				return configCarrier(t, current), nil
			}
			ctrl := setupTestController(t, mock)

			req := previewRequest()
			if tc.scheduledCell {
				req.Schedule = "kukeon-dev"
			}
			preview, err := ctrl.PreviewCellFromConfig(req)
			if err != nil {
				t.Fatalf("render live cell: %v", err)
			}
			live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
			if err != nil {
				t.Fatalf("convert live cell: %v", err)
			}

			current = withSchedule(tc.after)
			mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
				return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
			}
			mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
				return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
			}
			mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
				return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
			}
			mock.ReconcileCellFn = passthroughReconcile
			res, captured := runOutOfSyncHarness(t, live, mock)
			if captured != nil {
				t.Errorf("a schedule edit wrote OutOfSync state: %+v", captured.Status)
			}
			if res.CellsErrored != 0 {
				t.Errorf("CellsErrored = %d (%v)", res.CellsErrored, res.Errors)
			}
		})
	}
}

// TestRunSchedules_SlowPassIsNotACatchUp: a schedule applied while a pass was
// held up is anchored at most a minute back, so the fire times of the whole
// hold-up are not caught up on.
func TestRunSchedules_SlowPassIsNotACatchUp(t *testing.T) {
	h := newScheduleHarness(t)
	h.pass("2026-11-17T08:30:00Z")

	// The next pass comes 14 minutes later; the Config was applied meanwhile.
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:44:00Z")
	h.wantFires(0)
	h.wantNext("2026-11-17T08:50:00Z")
	h.wantSkipped(0)
}

// TestRunSchedules_MisreadListingDoesNotDropState: the listing reads each
// document best-effort, so a scheduled Config it reports as unscheduled must
// not lose its stored schedule state — the document is re-read in full before
// anything is deleted.
func TestRunSchedules_MisreadListingDoesNotDropState(t *testing.T) {
	h := newScheduleHarness(t)
	h.configs[scheduleConfigName] = scheduledConfig("*/10 * * * *")
	h.pass("2026-11-17T08:22:19Z")
	h.wantNext("2026-11-17T08:30:00Z")

	list := h.runner().ListConfigsFn
	h.runner().ListConfigsFn = func(realm, space, stack string) ([]intmodel.CellConfig, error) {
		configs, err := list(realm, space, stack)
		for i := range configs {
			configs[i].Schedule = nil // a document the list could not read
		}
		return configs, err
	}
	h.pass("2026-11-17T08:23:00Z")
	h.wantNext("2026-11-17T08:30:00Z")
}

// crossScopeHarness serves two Configs named nightly, one realm-scoped and one
// stack-scoped at default/default/default. Both fire into the same stack, so
// their cells sit side by side, labelled with the same Config name.
type crossScopeHarness struct {
	t        *testing.T
	ctrl     *controller.Exec
	mock     *fakeRunner
	configs  map[string]ext.CellConfigDoc
	statuses map[string]intmodel.CellConfigScheduleStatus
	cells    []intmodel.Cell
	fires    []controller.CellFromConfigRequest
	prev     time.Time
}

const (
	crossScopeRealm = "default"
	crossScopeStack = "default/default/default"
)

func crossScopeConfig(scope, cron string) ext.CellConfigDoc {
	doc := scheduledConfig(cron)
	doc.Metadata.Name = "nightly"
	if scope == crossScopeRealm {
		doc.Metadata.Space, doc.Metadata.Stack = "", ""
	}
	return doc
}

func crossScopeKey(md intmodel.CellConfigMetadata) string {
	if md.Stack != "" {
		return crossScopeStack
	}
	return crossScopeRealm
}

func newCrossScopeHarness(t *testing.T, realmCron, stackCron string) *crossScopeHarness {
	t.Helper()
	h := &crossScopeHarness{
		t: t,
		configs: map[string]ext.CellConfigDoc{
			crossScopeRealm: crossScopeConfig(crossScopeRealm, realmCron),
			crossScopeStack: crossScopeConfig(crossScopeStack, stackCron),
		},
		statuses: map[string]intmodel.CellConfigScheduleStatus{},
	}
	cellNamed := func(name string) (intmodel.Cell, bool) {
		for _, cell := range h.cells {
			if cell.Metadata.Name == name {
				return cell, true
			}
		}
		return intmodel.Cell{}, false
	}
	h.mock = &fakeRunner{
		ListConfigsFn: func(string, string, string) ([]intmodel.CellConfig, error) {
			var out []intmodel.CellConfig
			for _, key := range []string{crossScopeRealm, crossScopeStack} {
				carrier := configCarrier(t, h.configs[key])
				carrier.Document = nil
				out = append(out, carrier)
			}
			return out, nil
		},
		GetConfigFn: func(c intmodel.CellConfig) (intmodel.CellConfig, error) {
			return configCarrier(t, h.configs[crossScopeKey(c.Metadata)]), nil
		},
		GetConfigScheduleStatusFn: func(c intmodel.CellConfig) (intmodel.CellConfigScheduleStatus, bool, error) {
			status, ok := h.statuses[crossScopeKey(c.Metadata)]
			return status, ok, nil
		},
		WriteConfigScheduleStatusFn: func(c intmodel.CellConfig, status intmodel.CellConfigScheduleStatus) error {
			status.ActiveCells = slices.Clone(status.ActiveCells)
			h.statuses[crossScopeKey(c.Metadata)] = status
			return nil
		},
		DeleteConfigScheduleStatusFn: func(intmodel.CellConfig) error { return nil },
		ListCellsFn: func(string, string, string) ([]intmodel.Cell, error) {
			return slices.Clone(h.cells), nil
		},
		GetCellFn: func(c intmodel.Cell) (intmodel.Cell, error) {
			if cell, ok := cellNamed(c.Metadata.Name); ok {
				return cell, nil
			}
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		},
		ReconcileCellFn: func(c intmodel.Cell) (intmodel.Cell, runner.ReconcileOutcome, error) {
			return c, runner.ReconcileOutcome{}, nil
		},
	}
	h.ctrl = setupTestController(t, h.mock)
	h.ctrl.SetScheduleFireForTest(func(req controller.CellFromConfigRequest) error {
		h.fires = append(h.fires, req)
		if _, exists := cellNamed(req.Name); exists {
			return errdefs.ErrCellAlreadyExists
		}
		h.cells = append(h.cells, scheduleCellFor(req.Name, req.Schedule, req.Config, intmodel.CellStateReady))
		return nil
	})
	return h
}

func (h *crossScopeHarness) pass(now string) controller.ScheduleResult {
	h.t.Helper()
	when := at(h.t, now)
	res, err := h.ctrl.RunSchedules(h.prev, when)
	if err != nil {
		h.t.Fatalf("RunSchedules(%s) error = %v", now, err)
	}
	h.prev = when
	return res
}

// TestRunSchedules_SameNamedConfigsInOtherScopes: a realm-scoped and a
// stack-scoped Config of the same name fire into the same stack and label
// their cells with the same name. Each one owns only the cells its own
// schedule fired: the other's running cells neither block it under Forbid nor
// show in its activeCells, and a fire whose name the other Config already took
// is a failed fire, not a fire of a cell it never created.
func TestRunSchedules_SameNamedConfigsInOtherScopes(t *testing.T) {
	t.Run("Forbid and activeCells are per Config", func(t *testing.T) {
		h := newCrossScopeHarness(t, "0 * * * *", "30 * * * *")
		h.pass("2026-11-17T08:55:00Z")

		h.pass("2026-11-17T09:00:00Z") // the realm Config fires; its cell keeps running
		if len(h.fires) != 1 || h.fires[0].Config.Stack != "" {
			t.Fatalf("fires = %+v, want one from the realm Config", h.fires)
		}

		res := h.pass("2026-11-17T09:30:00Z") // the stack Config is not held back by it
		if len(h.fires) != 2 || h.fires[1].Config.Stack != "default" {
			t.Fatalf("fires = %+v, want a second one from the stack Config", h.fires)
		}
		if res.Skipped != 0 {
			t.Errorf("Skipped = %d, want 0", res.Skipped)
		}

		realm, stack := h.statuses[crossScopeRealm], h.statuses[crossScopeStack]
		if want := []string{"nightly-1794906000"}; !slices.Equal(realm.ActiveCells, want) {
			t.Errorf("realm activeCells = %v, want %v", realm.ActiveCells, want)
		}
		if want := []string{"nightly-1794907800"}; !slices.Equal(stack.ActiveCells, want) {
			t.Errorf("stack activeCells = %v, want %v", stack.ActiveCells, want)
		}
		if realm.SkippedFires != 0 || stack.SkippedFires != 0 {
			t.Errorf("skippedFires realm=%d stack=%d, want 0 and 0", realm.SkippedFires, stack.SkippedFires)
		}
	})

	t.Run("a name the other Config took is not this Config's fire", func(t *testing.T) {
		h := newCrossScopeHarness(t, "0 * * * *", "0 * * * *")
		h.pass("2026-11-17T08:55:00Z")

		// The realm Config is listed first and takes nightly-<09:00>.
		res := h.pass("2026-11-17T09:00:00Z")
		if res.Fired != 1 || res.Failed != 1 {
			t.Fatalf("Fired=%d Failed=%d, want 1 and 1 (%v)", res.Fired, res.Failed, res.Errors)
		}
		realm, stack := h.statuses[crossScopeRealm], h.statuses[crossScopeStack]
		if realm.LastCell != "nightly-1794906000" || realm.LastError != "" {
			t.Errorf("realm lastCell=%q lastError=%q, want the fire recorded", realm.LastCell, realm.LastError)
		}
		if stack.LastCell != "" || stack.LastScheduleTime != nil {
			t.Errorf("stack lastCell=%q lastScheduleTime=%v, want no fire recorded",
				stack.LastCell, stack.LastScheduleTime)
		}
		if !strings.Contains(stack.LastError, "not created by this Config's schedule") {
			t.Errorf("stack lastError = %q, want the name collision", stack.LastError)
		}
		if len(stack.ActiveCells) != 0 {
			t.Errorf("stack activeCells = %v, want none", stack.ActiveCells)
		}
	})

	t.Run("a create that finds the other Config's cell is not this Config's fire", func(t *testing.T) {
		h := newCrossScopeHarness(t, "0 * * * *", "0 * * * *")
		h.pass("2026-11-17T08:55:00Z")

		// The realm Config's cell appears between the stack Config's listing
		// and its create: hide it from the listing, so only the create and the
		// follow-up read see it.
		realmCell := scheduleCellFor("nightly-1794906000", "nightly",
			intmodel.CellConfigMetadata{Name: "nightly", Realm: "default"}, intmodel.CellStateReady)
		h.configs[crossScopeRealm] = crossScopeConfig(crossScopeRealm, "0 0 1 1 *")
		h.cells = []intmodel.Cell{realmCell}
		h.mock.ListCellsFn = func(string, string, string) ([]intmodel.Cell, error) { return nil, nil }

		res := h.pass("2026-11-17T09:00:00Z")
		if res.Fired != 0 || res.Failed != 1 {
			t.Fatalf("Fired=%d Failed=%d, want 0 and 1 (%v)", res.Fired, res.Failed, res.Errors)
		}
		stack := h.statuses[crossScopeStack]
		if stack.LastCell != "" || !strings.Contains(stack.LastError, "not created by this Config's schedule") {
			t.Errorf("stack lastCell=%q lastError=%q, want the collision reported", stack.LastCell, stack.LastError)
		}
	})
}
