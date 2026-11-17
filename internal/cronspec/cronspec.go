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

// Package cronspec parses the cron expressions a CellConfig's spec.schedule
// accepts and computes their fire times (issue #1503).
//
// An expression is either five whitespace-separated fields — minute, hour,
// day of month, month, day of week — or one of the macros @hourly, @daily,
// @weekly and @monthly. There is no seconds field and no @every: a schedule
// has one-minute precision.
//
// A field is a comma-separated list of `*`, a value, a range `a-b`, and any of
// those followed by a step `/n` (`a/n` means `a-<max>/n`). Months and days of
// week also accept three-letter English names; day of week runs 0-6 from
// Sunday, with 7 accepted as Sunday. When both day fields are restricted a
// time matches if either one does, as in Vixie cron.
//
// Fire times are wall-clock times in the schedule's time zone. Across a
// daylight-saving transition that means a wall-clock time the clocks skip does
// not fire that day, and one the clocks repeat fires on both occurrences.
package cronspec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	// Embed the IANA database so a time zone resolves the same on a host (or
	// in a daemon image) that ships no /usr/share/zoneinfo.
	_ "time/tzdata"
)

// ErrInvalid is returned for an expression or time zone that cannot be used.
var ErrInvalid = errors.New("invalid schedule")

// searchYears bounds how far Next looks for a fire time: long enough to reach
// the next 29 February across a skipped leap year. An expression whose day and
// month can never coincide (`0 0 31 2 *`) finds none inside it.
const searchYears = 8

// Schedule is a parsed cron expression. The zero value never fires.
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domStar and dowStar record that the day field was unrestricted, which
	// decides whether the two day fields combine with AND or OR.
	domStar, dowStar bool
}

// The bounds of each field, and the layout of an expression.
const (
	fieldCount  = 5
	maxMinute   = 59
	maxHour     = 23
	maxDay      = 31
	maxMonth    = 12
	hoursPerDay = 24
	// sundayAlias is the second spelling of Sunday in the day-of-week field;
	// parse folds it onto 0.
	sundayAlias = 7
)

// field describes one of an expression's five fields: its name for error
// messages, the values it accepts, and the names that stand for values.
type field struct {
	name     string
	min, max int
	names    func(string) (int, bool)
}

func minuteField() field { return field{name: "minute", min: 0, max: maxMinute} }
func hourField() field   { return field{name: "hour", min: 0, max: maxHour} }
func domField() field    { return field{name: "day of month", min: 1, max: maxDay} }
func monthField() field  { return field{name: "month", min: 1, max: maxMonth, names: monthByName} }
func dowField() field {
	return field{name: "day of week", min: 0, max: sundayAlias, names: weekdayByName}
}

// monthByName resolves a three-letter English month name.
func monthByName(name string) (int, bool) {
	for month := time.January; month <= time.December; month++ {
		if strings.EqualFold(name, month.String()[:3]) {
			return int(month), true
		}
	}
	return 0, false
}

// weekdayByName resolves a three-letter English weekday name; Sunday is 0.
func weekdayByName(name string) (int, bool) {
	for day := time.Sunday; day <= time.Saturday; day++ {
		if strings.EqualFold(name, day.String()[:3]) {
			return int(day), true
		}
	}
	return 0, false
}

// expandMacro returns the five-field expression a macro stands for.
func expandMacro(macro string) (string, bool) {
	switch strings.ToLower(macro) {
	case "@hourly":
		return "0 * * * *", true
	case "@daily":
		return "0 0 * * *", true
	case "@weekly":
		return "0 0 * * 0", true
	case "@monthly":
		return "0 0 1 * *", true
	default:
		return "", false
	}
}

// Parse parses a five-field cron expression or one of the four macros.
func Parse(expr string) (Schedule, error) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return Schedule{}, fmt.Errorf("%w: cron expression is empty", ErrInvalid)
	}
	if strings.HasPrefix(trimmed, "@") {
		expanded, ok := expandMacro(trimmed)
		if !ok {
			return Schedule{}, fmt.Errorf(
				"%w: unsupported macro %q (supported: @hourly, @daily, @weekly, @monthly)", ErrInvalid, trimmed)
		}
		trimmed = expanded
	}
	parts := strings.Fields(trimmed)
	if len(parts) != fieldCount {
		return Schedule{}, fmt.Errorf(
			"%w: cron %q has %d fields, want %d (minute hour day-of-month month day-of-week)",
			ErrInvalid, expr, len(parts), fieldCount)
	}

	var s Schedule
	var err error
	if s.minute, _, err = minuteField().parse(parts[0]); err != nil {
		return Schedule{}, err
	}
	if s.hour, _, err = hourField().parse(parts[1]); err != nil {
		return Schedule{}, err
	}
	if s.dom, s.domStar, err = domField().parse(parts[2]); err != nil {
		return Schedule{}, err
	}
	if s.month, _, err = monthField().parse(parts[3]); err != nil {
		return Schedule{}, err
	}
	if s.dow, s.dowStar, err = dowField().parse(parts[4]); err != nil {
		return Schedule{}, err
	}
	// Fold the second spelling of Sunday onto 0.
	if s.dow&(1<<sundayAlias) != 0 {
		s.dow = s.dow&^(1<<sundayAlias) | 1
	}
	if s.Next(time.Unix(0, 0), time.UTC).IsZero() {
		return Schedule{}, fmt.Errorf("%w: cron %q never fires", ErrInvalid, expr)
	}
	return s, nil
}

// parse returns the bit set of the values a field expression selects, and
// whether the field is an unrestricted `*`.
func (f field) parse(expr string) (uint64, bool, error) {
	var bits uint64
	star := false
	for _, item := range strings.Split(expr, ",") {
		itemBits, itemStar, err := f.parseItem(item)
		if err != nil {
			return 0, false, fmt.Errorf("%w: %s field %q: %w", ErrInvalid, f.name, expr, err)
		}
		bits |= itemBits
		star = star || itemStar
	}
	return bits, star, nil
}

func (f field) parseItem(item string) (uint64, bool, error) {
	if item == "" {
		return 0, false, errors.New("empty list entry")
	}
	rangePart, stepPart, hasStep := strings.Cut(item, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepPart)
		if err != nil || n <= 0 {
			return 0, false, fmt.Errorf("step %q must be a positive integer", stepPart)
		}
		step = n
	}

	lo, hi := f.min, f.max
	star := false
	switch {
	case rangePart == "*":
		// A stepped star (`*/2`) restricts the field; a bare one does not.
		star = !hasStep
	case strings.Contains(rangePart, "-"):
		from, to, _ := strings.Cut(rangePart, "-")
		var err error
		if lo, err = f.value(from); err != nil {
			return 0, false, err
		}
		if hi, err = f.value(to); err != nil {
			return 0, false, err
		}
		if lo > hi {
			return 0, false, fmt.Errorf("range %q runs backwards", rangePart)
		}
	default:
		v, err := f.value(rangePart)
		if err != nil {
			return 0, false, err
		}
		lo = v
		if !hasStep {
			hi = v
		}
	}

	var bits uint64
	for v := lo; v <= hi; v += step {
		bits |= 1 << v
	}
	return bits, star, nil
}

func (f field) value(text string) (int, error) {
	if f.names != nil {
		if v, ok := f.names(text); ok {
			return v, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("%q is not a value", text)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%d is outside %d-%d", v, f.min, f.max)
	}
	return v, nil
}

// LoadLocation resolves an IANA time zone name. The empty name is UTC.
// "Local" is refused: it would make a schedule depend on the daemon's host.
func LoadLocation(name string) (*time.Location, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return time.UTC, nil
	}
	if trimmed == "Local" {
		return nil, fmt.Errorf("%w: timezone %q is not an IANA time zone name", ErrInvalid, name)
	}
	loc, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown timezone %q", ErrInvalid, name)
	}
	return loc, nil
}

// Matches reports whether t, truncated to the minute and read in loc, is a
// fire time of the schedule.
func (s Schedule) Matches(t time.Time, loc *time.Location) bool {
	t = t.In(loc)
	return s.minute&(1<<t.Minute()) != 0 &&
		s.hour&(1<<t.Hour()) != 0 &&
		s.month&(1<<t.Month()) != 0 &&
		s.dayMatches(t)
}

// Next returns the first fire time strictly after `after`, read in loc. It
// returns the zero time when the schedule has no fire time within the next
// eight years.
func (s Schedule) Next(after time.Time, loc *time.Location) time.Time {
	if s.minute == 0 || s.hour == 0 || s.dom == 0 || s.month == 0 || s.dow == 0 {
		return time.Time{}
	}
	// Start at the minute after `after`, then walk each field up to its next
	// match, coarsest first. Advancing a field resets the finer ones; `reset`
	// records that this has happened so it is done only once.
	t := after.In(loc)
	t = t.Add(time.Minute - time.Duration(t.Second())*time.Second - time.Duration(t.Nanosecond()))
	reset := false
	yearLimit := t.Year() + searchYears

wrap:
	if t.Year() > yearLimit {
		return time.Time{}
	}

	for s.month&(1<<t.Month()) == 0 {
		if !reset {
			reset = true
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 1, 0)
		if t.Month() == time.January {
			goto wrap
		}
	}

	for !s.dayMatches(t) {
		if !reset {
			reset = true
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
		}
		t = t.AddDate(0, 0, 1)
		// A day that starts inside a daylight-saving transition does not
		// begin at midnight; step onto its first real hour.
		if t.Hour() != 0 {
			if t.Hour() > hoursPerDay/2 {
				t = t.Add(time.Duration(hoursPerDay-t.Hour()) * time.Hour)
			} else {
				t = t.Add(time.Duration(-t.Hour()) * time.Hour)
			}
		}
		if t.Day() == 1 {
			goto wrap
		}
	}

	for s.hour&(1<<t.Hour()) == 0 {
		if !reset {
			reset = true
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
		}
		t = t.Add(time.Hour)
		if t.Hour() == 0 {
			goto wrap
		}
	}

	for s.minute&(1<<t.Minute()) == 0 {
		t = t.Add(time.Minute)
		if t.Minute() == 0 {
			goto wrap
		}
	}

	return t
}

// dayMatches applies the cron day rule: with both day fields restricted a day
// matches if either does; otherwise both must (an unrestricted one always
// does).
func (s Schedule) dayMatches(t time.Time) bool {
	domMatch := s.dom&(1<<t.Day()) != 0
	dowMatch := s.dow&(1<<t.Weekday()) != 0
	if s.domStar || s.dowStar {
		return domMatch && dowMatch
	}
	return domMatch || dowMatch
}
