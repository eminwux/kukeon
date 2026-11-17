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

package controller

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/cronspec"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// scheduleSuspended is the fingerprint recorded for a suspended schedule. It
// never equals a real one, so a resumed schedule is always re-anchored instead
// of catching up on the fires it sat out.
const scheduleSuspended = "suspended"

// scheduleState is what the scheduler remembers between passes, in memory
// only: for each scheduled Config, the cron + time zone its stored
// nextScheduleTime was computed for. A pass that finds a different fingerprint
// knows the schedule was changed, resumed or first applied since the last
// pass, and re-anchors it rather than trusting the stored time. After a daemon
// restart the map is empty, which is how a pass tells "missed while the daemon
// was down" from "changed while it was up".
type scheduleState struct {
	mu   sync.Mutex
	seen map[string]string
	// fire creates and starts a fire's cell. nil falls through to
	// CreateCellFromConfig; tests override it to exercise the scheduling
	// decisions without a full cell create.
	fire func(CellFromConfigRequest) error
}

// ScheduleResult summarizes one scheduler pass.
type ScheduleResult struct {
	// Scheduled counts the Configs with a spec.schedule the pass looked at.
	Scheduled int
	// Fired counts the fires that created a cell (or found the cell an earlier
	// attempt at the same fire had already created).
	Fired int
	// Skipped counts the fires that did not run: held back by the concurrency
	// policy, or missed and past their starting deadline.
	Skipped int64
	// Failed counts the fires whose cell could not be created.
	Failed int
	// Errors records what went wrong, one entry per Config.
	Errors []string
}

// RunSchedules is one pass of the CellConfig scheduler (issue #1503): for
// every Config with a spec.schedule it fires the schedule if a fire time has
// come since the previous pass, and brings the Config's stored schedule state
// up to date. kukeond calls it on its own one-minute timer, independent of the
// reconcile loop.
//
// prev is the time of the previous pass, zero on the first pass after the
// daemon started; now is the time of this one. They decide what a fire time
// that has already passed means:
//
//   - A fire time after prev came due while the scheduler was running. It is
//     fired now, however late the pass is.
//   - A fire time at or before prev — every due fire time, on the first pass —
//     was missed while the daemon was down. It is fired only when the schedule
//     sets startingDeadlineSeconds and the fire time is no older than that.
//
// Either way at most one fire runs per Config per pass: the latest due one.
// The rest are counted in skippedFires.
//
// The returned error is always nil. Failures land in Errors (and, per Config,
// in its status.schedule.lastError) so one bad Config does not stop the rest.
func (b *Exec) RunSchedules(prev, now time.Time) (ScheduleResult, error) {
	result := ScheduleResult{Errors: []string{}}

	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("list configs: %v", err))
		return result, nil
	}

	b.schedules.mu.Lock()
	defer b.schedules.mu.Unlock()
	if b.schedules.seen == nil {
		b.schedules.seen = map[string]string{}
	}

	scheduled := map[string]struct{}{}
	for _, cfg := range configs {
		key := scheduleKey(cfg.Metadata)
		if cfg.Schedule == nil {
			// Not scheduled (any more): future fires stop, running cells are
			// left alone, and any leftover state goes.
			if pruneErr := b.pruneScheduleStatus(cfg); pruneErr != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("config %s: %v", key, pruneErr))
			}
			continue
		}
		scheduled[key] = struct{}{}
		result.Scheduled++
		if runErr := b.runSchedule(&result, cfg.Metadata, key, prev, now); runErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("config %s: %v", key, runErr))
		}
	}
	// Forget Configs that were deleted or lost their schedule, so one
	// re-created under the same name is treated as new.
	for key := range b.schedules.seen {
		if _, ok := scheduled[key]; !ok {
			delete(b.schedules.seen, key)
		}
	}
	return result, nil
}

// pruneScheduleStatus removes the schedule state left behind by a Config that
// no longer has a spec.schedule. The listing that called it unscheduled reads
// each document best-effort, so before deleting state that holds a schedule's
// history and next fire time, the document is read again in full: the state
// goes only when the Config really has no schedule.
func (b *Exec) pruneScheduleStatus(listed intmodel.CellConfig) error {
	if _, found, err := b.runner.GetConfigScheduleStatus(listed); err == nil && !found {
		return nil // nothing stored: the common case
	}
	cfg, err := b.runner.GetConfig(intmodel.CellConfig{Metadata: listed.Metadata})
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return b.runner.DeleteConfigScheduleStatus(listed)
		}
		return err
	}
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	if cfgDoc.Spec.Schedule != nil {
		return nil // the listing misread it; the next pass schedules it
	}
	return b.runner.DeleteConfigScheduleStatus(listed)
}

// scheduleKey identifies a Config across passes by its scope and name.
func scheduleKey(md intmodel.CellConfigMetadata) string {
	return strings.Join([]string{md.Realm, md.Space, md.Stack, md.Name}, "/")
}

// runSchedule handles one scheduled Config in a pass. The caller holds
// b.schedules.mu.
func (b *Exec) runSchedule(
	result *ScheduleResult, md intmodel.CellConfigMetadata, key string, prev, now time.Time,
) error {
	cfg, err := b.runner.GetConfig(intmodel.CellConfig{Metadata: md})
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return nil // deleted since the listing
		}
		return err
	}
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	schedule := cfgDoc.Spec.Schedule
	if schedule == nil {
		return nil // schedule removed since the listing; the next pass prunes
	}

	stored, found, statusErr := b.runner.GetConfigScheduleStatus(cfg)
	if statusErr != nil {
		// Unreadable state must not stop the schedule: start over from an
		// empty one, which re-anchors and overwrites it below.
		b.logger.WarnContext(b.ctx, "schedule: stored status unreadable; starting over",
			"config", key, "error", statusErr)
		stored, found = intmodel.CellConfigScheduleStatus{}, false
	}
	status := stored

	cells, err := b.scheduledCells(cfgDoc)
	if err != nil {
		return fmt.Errorf("list scheduled cells: %w", err)
	}
	status.ActiveCells = cells.active

	if fireTime, due := b.advanceSchedule(&status, schedule, key, prev, now); due {
		b.fireSchedule(result, &status, cfgDoc, cells, fireTime)
	}
	result.Skipped += status.SkippedFires - stored.SkippedFires

	if found && scheduleStatusEqual(stored, status) {
		return nil
	}
	return b.runner.WriteConfigScheduleStatus(cfg, status)
}

// advanceSchedule moves a schedule's nextScheduleTime past now and reports the
// fire that should run in this pass, if any. Everything it decides not to run
// — a suspended or unparseable schedule, missed fires past their deadline, all
// but the latest of several due fires — is recorded on status instead.
func (b *Exec) advanceSchedule(
	status *intmodel.CellConfigScheduleStatus,
	schedule *v1beta1.CellConfigSchedule,
	key string,
	prev, now time.Time,
) (time.Time, bool) {
	spec, loc, parseErr := cellconfig.ParseSchedule(*schedule)
	if parseErr != nil {
		// Only a document stored without validation gets here. Report it and
		// keep it from firing.
		delete(b.schedules.seen, key)
		status.NextScheduleTime = nil
		status.LastError = parseErr.Error()
		return time.Time{}, false
	}
	if schedule.Suspend {
		b.schedules.seen[key] = scheduleSuspended
		status.NextScheduleTime = nil
		return time.Time{}, false
	}

	fingerprint := strings.TrimSpace(schedule.Cron) + "\x00" + strings.TrimSpace(schedule.Timezone)
	seen, wasSeen := b.schedules.seen[key]
	b.schedules.seen[key] = fingerprint
	if !nextIsTrusted(status.NextScheduleTime, spec, loc, wasSeen, seen == fingerprint) {
		// New, changed or resumed: anchor at the previous pass, so a fire time
		// since then still runs but nothing older is caught up on.
		status.NextScheduleTime = timeOrNil(spec.Next(scheduleAnchor(prev, now), loc))
	}
	if status.NextScheduleTime == nil || status.NextScheduleTime.After(now) {
		return time.Time{}, false
	}

	// One or more fire times are due. Walk them to find the latest and the
	// first one still ahead.
	var latest time.Time
	var due int64
	next := *status.NextScheduleTime
	for !next.IsZero() && !next.After(now) {
		latest = next
		due++
		next = spec.Next(next, loc)
	}
	status.NextScheduleTime = timeOrNil(next)

	if !fireIsEligible(schedule, latest, prev, now) {
		status.SkippedFires += due
		return time.Time{}, false
	}
	status.SkippedFires += due - 1
	return latest, true
}

// scheduleResolution is the precision of a schedule, and the period kukeond
// runs passes at.
const scheduleResolution = time.Minute

// scheduleAnchor is the time a schedule that is new to the scheduler — just
// applied, edited or resumed — starts counting fire times from: the previous
// pass, so a fire time that fell between two passes is not lost. It is never
// more than one scheduleResolution back (a pass held up behind a slow cell
// create must not turn into a catch-up), and it is `now` on the first pass
// after a daemon start, when there is no previous pass.
func scheduleAnchor(prev, now time.Time) time.Time {
	if prev.IsZero() || prev.After(now) {
		return now
	}
	if floor := now.Add(-scheduleResolution); prev.Before(floor) {
		return floor
	}
	return prev
}

// nextIsTrusted reports whether a stored nextScheduleTime still stands for the
// schedule as it is now. While the scheduler has been watching the Config it
// does only if the cron and time zone are unchanged. On the first pass after a
// daemon start there is nothing to compare against, so it stands if it is a
// fire time of the current expression — which is what makes a fire missed
// during the downtime visible.
func nextIsTrusted(
	next *time.Time, spec cronspec.Schedule, loc *time.Location, wasSeen, unchanged bool,
) bool {
	if next == nil {
		return false
	}
	if wasSeen {
		return unchanged
	}
	return spec.Matches(*next, loc)
}

// fireIsEligible decides whether a due fire runs. A fire time that came due
// while the scheduler was running (after the previous pass) always does. One
// missed while the daemon was down does only inside the schedule's
// startingDeadlineSeconds; without a deadline it is dropped.
func fireIsEligible(schedule *v1beta1.CellConfigSchedule, fireTime, prev, now time.Time) bool {
	if !prev.IsZero() && fireTime.After(prev) {
		return true
	}
	deadline := schedule.StartingDeadlineSeconds
	if deadline == nil {
		return false
	}
	return now.Sub(fireTime) <= time.Duration(*deadline)*time.Second
}

// fireSchedule runs one fire of a schedule: it creates and starts the cell
// `<config>-<unix-fire-time>` exactly as `kuke run -d --from-config` would,
// labelled kukeon.io/schedule=<config> and self-deleting, and records the
// outcome on status.
//
// The fire-time name makes a fire idempotent. A fire whose cell already exists
// — the daemon restarted between creating it and recording it — counts as
// fired, but only when that cell is this Config's: a same-named Config in
// another scope that fires into the same stack derives the same name, and its
// cell is a collision, recorded as a failed fire. Under the Forbid policy (the only one) a fire is skipped while a cell
// an earlier fire created is still running. A fire that fails records
// lastError and is not retried: the next attempt is the next fire time.
func (b *Exec) fireSchedule(
	result *ScheduleResult,
	status *intmodel.CellConfigScheduleStatus,
	cfgDoc v1beta1.CellConfigDoc,
	cells scheduledCellSet,
	fireTime time.Time,
) {
	md := intmodel.CellConfigMetadata{
		Name:  cfgDoc.Metadata.Name,
		Realm: cfgDoc.Metadata.Realm,
		Space: cfgDoc.Metadata.Space,
		Stack: cfgDoc.Metadata.Stack,
	}
	name := cellconfig.ScheduledCellName(md.Name, fireTime)
	fired := func() {
		status.LastScheduleTime = timeOrNil(fireTime)
		status.LastCell = name
		status.LastError = ""
		result.Fired++
	}

	failed := func(err error) {
		status.LastError = fmt.Sprintf("fire %s: %v", fireTime.UTC().Format(time.RFC3339), err)
		result.Failed++
		result.Errors = append(result.Errors,
			fmt.Sprintf("config %s: %s", scheduleKey(md), status.LastError))
	}

	if _, exists := cells.owned[name]; exists {
		fired()
		return
	}
	if _, taken := cells.others[name]; taken {
		failed(fmt.Errorf("%w: cell %q was not created by this Config's schedule",
			errdefs.ErrCellAlreadyExists, name))
		return
	}
	if len(status.ActiveCells) > 0 {
		status.SkippedFires++
		b.logger.InfoContext(b.ctx, "schedule: fire skipped, an earlier cell is still running",
			"config", scheduleKey(md), "fireTime", fireTime.UTC().Format(time.RFC3339),
			"activeCells", status.ActiveCells)
		return
	}

	create := b.schedules.fire
	if create == nil {
		create = func(req CellFromConfigRequest) error {
			_, createErr := b.CreateCellFromConfig(req)
			return createErr
		}
	}
	err := create(CellFromConfigRequest{
		Config:     md,
		Name:       name,
		AutoDelete: true,
		Start:      true,
		Schedule:   md.Name,
	})
	switch {
	case err == nil:
		fired()
		status.ActiveCells = append(status.ActiveCells, name)
		slices.Sort(status.ActiveCells)
		b.logger.InfoContext(b.ctx, "schedule: fired",
			"config", scheduleKey(md), "cell", name, "fireTime", fireTime.UTC().Format(time.RFC3339))
	case errors.Is(err, errdefs.ErrCellAlreadyExists):
		// Created between the listing and the create. Count it as this fire
		// only when the cell really is this Config's.
		owned, ownErr := b.scheduledCellIsOwned(cfgDoc, name)
		switch {
		case ownErr != nil:
			failed(fmt.Errorf("%w; checking its owner: %w", err, ownErr))
		case owned:
			fired()
		default:
			failed(fmt.Errorf("%w: cell %q was not created by this Config's schedule", err, name))
		}
	default:
		failed(err)
	}
}

// scheduledCellSet is what a pass knows about the cells in a scheduled
// Config's cell scope.
type scheduledCellSet struct {
	// owned holds the cells this Config's schedule fired that still exist.
	owned map[string]struct{}
	// active holds, sorted, the owned cells still running.
	active []string
	// others holds every other cell in the scope, so a fire whose name is
	// already taken by a cell that is not this Config's is told apart from
	// one an earlier attempt created.
	others map[string]struct{}
}

// cellIsScheduledBy reports whether cell was fired by the schedule of the
// Config cfgDoc. The kukeon.io/schedule label carries only the Config's name,
// and Configs of the same name can live at realm, space and stack scope and
// fire into the same stack, so the cell's provenance must also point back at
// this Config's own scope.
func cellIsScheduledBy(cell intmodel.Cell, cfgDoc v1beta1.CellConfigDoc) bool {
	if cell.Metadata.Labels[v1beta1.LabelSchedule] != cfgDoc.Metadata.Name {
		return false
	}
	prov := cell.Spec.Provenance
	if prov == nil || prov.BindingKind != v1beta1.BindingKindConfig {
		return false
	}
	ref := prov.BindingRef
	md := cfgDoc.Metadata
	return strings.TrimSpace(ref.Name) == strings.TrimSpace(md.Name) &&
		strings.TrimSpace(ref.Realm) == strings.TrimSpace(md.Realm) &&
		strings.TrimSpace(ref.Space) == strings.TrimSpace(md.Space) &&
		strings.TrimSpace(ref.Stack) == strings.TrimSpace(md.Stack)
}

// scheduledCellIsOwned reads the named cell in the Config's cell scope and
// reports whether this Config's schedule fired it.
func (b *Exec) scheduledCellIsOwned(cfgDoc v1beta1.CellConfigDoc, name string) (bool, error) {
	realm, space, stack := cellconfig.CellScope(cfgDoc)
	cell, err := b.runner.GetCell(intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: name},
		Spec:     intmodel.CellSpec{RealmName: realm, SpaceName: space, StackName: stack},
	})
	if err != nil {
		return false, err
	}
	return cellIsScheduledBy(cell, cfgDoc), nil
}

// scheduledCells returns the cells the Config's schedule has fired that still
// exist — every cell in the Config's cell scope labelled
// kukeon.io/schedule=<config> whose provenance points at this Config's own
// scope — and, sorted, the names of those still running. A cell started by
// hand from the same Config carries no such label, and a cell fired by a
// same-named Config in another scope points elsewhere, so neither is owned;
// both are listed in others.
//
// Each cell is reconciled first, so the verdict reflects the cell's live state
// (and a finished self-deleting cell is reaped) even when the operator has
// turned the reconcile loop off.
func (b *Exec) scheduledCells(cfgDoc v1beta1.CellConfigDoc) (scheduledCellSet, error) {
	set := scheduledCellSet{owned: map[string]struct{}{}, others: map[string]struct{}{}}
	realm, space, stack := cellconfig.CellScope(cfgDoc)
	listed, err := b.runner.ListCells(realm, space, stack)
	if err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) || errors.Is(err, errdefs.ErrSpaceNotFound) ||
			errors.Is(err, errdefs.ErrStackNotFound) {
			return set, nil
		}
		return scheduledCellSet{}, err
	}

	for _, cell := range listed {
		if !cellIsScheduledBy(cell, cfgDoc) {
			set.others[cell.Metadata.Name] = struct{}{}
			continue
		}
		reconciled, outcome, reconcileErr := b.runner.ReconcileCell(cell)
		switch {
		case reconcileErr != nil:
			// Judge by the listed state rather than drop the cell: wrongly
			// forgetting a running cell would let a Forbid schedule overlap.
			b.logger.DebugContext(b.ctx, "schedule: reconcile scheduled cell failed",
				"cell", cell.Metadata.Name, "error", reconcileErr)
		case outcome.Deleted || outcome.Vanished:
			continue
		default:
			cell = reconciled
		}
		set.owned[cell.Metadata.Name] = struct{}{}
		if scheduleCellIsActive(cell.Status.State) {
			set.active = append(set.active, cell.Metadata.Name)
		}
	}
	slices.Sort(set.active)
	return set, nil
}

// scheduleCellIsActive reports whether a cell in this state holds back the
// next fire of a Forbid schedule: it is running, starting, or of unknown
// health. Stopped, Exited, Failed and Error cells are finished and do not.
func scheduleCellIsActive(state intmodel.CellState) bool {
	switch state {
	case intmodel.CellStatePending, intmodel.CellStateReady,
		intmodel.CellStateDegraded, intmodel.CellStateUnknown:
		return true
	case intmodel.CellStateStopped, intmodel.CellStateExited,
		intmodel.CellStateFailed, intmodel.CellStateError:
		return false
	default:
		return true
	}
}

// timeOrNil returns t in UTC — the form schedule times are stored and reported
// in, whatever time zone the schedule is read in — or nil for the zero time.
func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	utc := t.UTC()
	return &utc
}

func scheduleStatusEqual(a, b intmodel.CellConfigScheduleStatus) bool {
	return timesEqual(a.LastScheduleTime, b.LastScheduleTime) &&
		timesEqual(a.NextScheduleTime, b.NextScheduleTime) &&
		a.LastCell == b.LastCell &&
		slices.Equal(a.ActiveCells, b.ActiveCells) &&
		a.SkippedFires == b.SkippedFires &&
		a.LastError == b.LastError
}

func timesEqual(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
