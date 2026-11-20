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

package cronspec_test

import (
	"errors"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/cronspec"
)

func madridLocation(t *testing.T) *time.Location {
	t.Helper()
	loc, err := cronspec.LoadLocation("Europe/Madrid")
	if err != nil {
		t.Fatalf("LoadLocation(Europe/Madrid): %v", err)
	}
	return loc
}

func mustParse(t *testing.T, expr string) cronspec.Schedule {
	t.Helper()
	s, err := cronspec.Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return s
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func TestParseRejects(t *testing.T) {
	for name, expr := range map[string]string{
		"empty":               "",
		"blank":               "   ",
		"four fields":         "* * * *",
		"seconds field":       "0 */10 * * * *",
		"every macro":         "@every 5m",
		"yearly macro":        "@yearly",
		"reboot macro":        "@reboot",
		"minute out of range": "60 * * * *",
		"hour out of range":   "0 24 * * *",
		"day zero":            "0 0 0 * *",
		"month out of range":  "0 0 1 13 *",
		"weekday too large":   "0 0 * * 8",
		"backwards range":     "30-10 * * * *",
		"zero step":           "*/0 * * * *",
		"negative step":       "*/-5 * * * *",
		"empty list entry":    "1,,2 * * * *",
		"not a value":         "abc * * * *",
		"question mark":       "0 0 ? * *",
		"timezone prefix":     "CRON_TZ=UTC 0 0 * * *",
		"never fires":         "0 0 31 2 *",
		"month name as day":   "0 0 jan * *",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := cronspec.Parse(expr); !errors.Is(err, cronspec.ErrInvalid) {
				t.Fatalf("Parse(%q) error = %v, want ErrInvalid", expr, err)
			}
		})
	}
}

func TestNextUTC(t *testing.T) {
	for _, tc := range []struct {
		name, expr, after, want string
	}{
		{"every ten minutes", "*/10 * * * *", "2026-11-17T08:22:19Z", "2026-11-17T08:30:00Z"},
		{"strictly after a fire time", "*/10 * * * *", "2026-11-17T08:30:00Z", "2026-11-17T08:40:00Z"},
		{"hour rollover", "*/10 * * * *", "2026-11-17T08:55:00Z", "2026-11-17T09:00:00Z"},
		{"hourly macro", "@hourly", "2026-11-17T08:22:19Z", "2026-11-17T09:00:00Z"},
		{"daily macro", "@daily", "2026-11-17T08:22:19Z", "2026-11-18T00:00:00Z"},
		{"weekly macro lands on sunday", "@weekly", "2026-11-17T08:22:19Z", "2026-11-22T00:00:00Z"},
		{"monthly macro", "@monthly", "2026-11-17T08:22:19Z", "2026-12-01T00:00:00Z"},
		{"macro is case-insensitive", "@Daily", "2026-11-17T08:22:19Z", "2026-11-18T00:00:00Z"},
		{"year rollover", "0 0 1 1 *", "2026-11-17T08:22:19Z", "2027-01-01T00:00:00Z"},
		{"list and range", "15,45 9-10 * * *", "2026-11-17T09:20:00Z", "2026-11-17T09:45:00Z"},
		{"list and range past the hour", "15,45 9-10 * * *", "2026-11-17T10:50:00Z", "2026-11-18T09:15:00Z"},
		{"stepped range", "0 8-18/5 * * *", "2026-11-17T08:30:00Z", "2026-11-17T13:00:00Z"},
		{"value with step runs to the max", "50/5 * * * *", "2026-11-17T08:51:00Z", "2026-11-17T08:55:00Z"},
		{"weekday names", "30 6 * * mon-fri", "2026-11-20T07:00:00Z", "2026-11-23T06:30:00Z"},
		{"month names", "0 12 1 MAR,sep *", "2026-11-17T00:00:00Z", "2027-03-01T12:00:00Z"},
		{"seven is sunday", "0 0 * * 7", "2026-11-17T08:22:19Z", "2026-11-22T00:00:00Z"},
		// Both day fields restricted: either one matches (the 20th, a Friday,
		// comes before Monday the 23rd).
		{"day fields combine with or", "0 0 20 * mon", "2026-11-17T08:22:19Z", "2026-11-20T00:00:00Z"},
		{"day fields combine with or (weekday)", "0 0 28 * mon", "2026-11-17T08:22:19Z", "2026-11-23T00:00:00Z"},
		// A day field that starts with `*` is unrestricted even when stepped, so
		// the day fields combine with and: the first Monday on an odd day is
		// the 23rd, not Thursday the 19th.
		{"stepped star day of month combines with and", "0 0 */2 * mon", "2026-11-17T08:22:19Z", "2026-11-23T00:00:00Z"},
		// Day of week */2 is Sun, Tue, Thu, Sat; the first 13th on one of them is
		// Sunday 13 December, not Thursday the 19th.
		{"stepped star day of week combines with and", "0 0 13 * */2", "2026-11-17T08:22:19Z", "2026-12-13T00:00:00Z"},
		// Only the field's first character counts, as in Vixie cron.
		{"list starting with star combines with and", "0 0 *,20 * mon", "2026-11-17T08:22:19Z", "2026-11-23T00:00:00Z"},
		{"list not starting with star combines with or", "0 0 20,* * mon", "2026-11-17T08:22:19Z", "2026-11-18T00:00:00Z"},
		{"leap day", "0 0 29 2 *", "2026-11-17T08:22:19Z", "2028-02-29T00:00:00Z"},
		{"short months are skipped", "0 0 31 * *", "2026-11-17T08:22:19Z", "2026-12-31T00:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mustParse(t, tc.expr).Next(mustTime(t, tc.after), time.UTC)
			if want := mustTime(t, tc.want); !got.Equal(want) {
				t.Fatalf("Next(%s) = %s, want %s", tc.after, got.Format(time.RFC3339), tc.want)
			}
		})
	}
}

func TestNextHonorsTimezone(t *testing.T) {
	madrid := madridLocation(t)
	// 09:00 in Madrid is 08:00 UTC in winter (CET) and 07:00 UTC in summer.
	daily := mustParse(t, "0 9 * * *")
	if got, want := daily.Next(mustTime(t, "2026-01-15T00:00:00Z"), madrid),
		mustTime(t, "2026-01-15T08:00:00Z"); !got.Equal(want) {
		t.Fatalf("winter Next = %s, want %s", got.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
	}
	if got, want := daily.Next(mustTime(t, "2026-07-15T00:00:00Z"), madrid),
		mustTime(t, "2026-07-15T07:00:00Z"); !got.Equal(want) {
		t.Fatalf("summer Next = %s, want %s", got.UTC().Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

// fireTimes returns the n fire times after `after`, in UTC RFC 3339.
func fireTimes(s cronspec.Schedule, after time.Time, loc *time.Location, n int) []string {
	out := make([]string, 0, n)
	for range n {
		after = s.Next(after, loc)
		out = append(out, after.UTC().Format(time.RFC3339))
	}
	return out
}

func assertFireTimes(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("fire times = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("fire time %d = %s, want %s (all: %v)", i, got[i], want[i], got)
		}
	}
}

// Europe/Madrid springs forward on 2026-03-29: 02:00 CET becomes 03:00 CEST,
// so wall-clock 02:00-02:59 does not exist that day.
func TestNextSpringForward(t *testing.T) {
	madrid := madridLocation(t)
	before := mustTime(t, "2026-03-28T12:00:00Z")

	t.Run("a time inside the gap does not fire that day", func(t *testing.T) {
		assertFireTimes(t, fireTimes(mustParse(t, "30 2 * * *"), before, madrid, 2), []string{
			"2026-03-30T00:30:00Z", // 02:30 CEST on the 30th; the 29th is skipped
			"2026-03-31T00:30:00Z",
		})
	})

	t.Run("a time outside the gap keeps its wall-clock hour", func(t *testing.T) {
		assertFireTimes(t, fireTimes(mustParse(t, "30 3 * * *"), before, madrid, 2), []string{
			"2026-03-29T01:30:00Z", // 03:30 CEST
			"2026-03-30T01:30:00Z",
		})
	})

	t.Run("an interval keeps its real-time spacing", func(t *testing.T) {
		assertFireTimes(t,
			fireTimes(mustParse(t, "*/30 * * * *"), mustTime(t, "2026-03-29T00:10:00Z"), madrid, 4),
			[]string{
				"2026-03-29T00:30:00Z", // 01:30 CET
				"2026-03-29T01:00:00Z", // 03:00 CEST
				"2026-03-29T01:30:00Z",
				"2026-03-29T02:00:00Z",
			})
	})

	t.Run("a daily midnight schedule is unaffected", func(t *testing.T) {
		assertFireTimes(t, fireTimes(mustParse(t, "@daily"), before, madrid, 2), []string{
			"2026-03-28T23:00:00Z", // 00:00 CET on the 29th
			"2026-03-29T22:00:00Z", // 00:00 CEST on the 30th
		})
	})
}

// Europe/Madrid falls back on 2026-10-25: 03:00 CEST becomes 02:00 CET, so
// wall-clock 02:00-02:59 happens twice that day.
func TestNextFallBack(t *testing.T) {
	madrid := madridLocation(t)
	before := mustTime(t, "2026-10-24T12:00:00Z")

	t.Run("a repeated time fires on both occurrences", func(t *testing.T) {
		assertFireTimes(t, fireTimes(mustParse(t, "30 2 * * *"), before, madrid, 3), []string{
			"2026-10-25T00:30:00Z", // 02:30 CEST
			"2026-10-25T01:30:00Z", // 02:30 CET
			"2026-10-26T01:30:00Z",
		})
	})

	t.Run("a time outside the repeated hour fires once", func(t *testing.T) {
		assertFireTimes(t, fireTimes(mustParse(t, "30 3 * * *"), before, madrid, 2), []string{
			"2026-10-25T02:30:00Z", // 03:30 CET
			"2026-10-26T02:30:00Z",
		})
	})

	t.Run("an interval keeps its real-time spacing", func(t *testing.T) {
		assertFireTimes(t,
			fireTimes(mustParse(t, "0 * * * *"), mustTime(t, "2026-10-24T23:30:00Z"), madrid, 4),
			[]string{
				"2026-10-25T00:00:00Z", // 02:00 CEST
				"2026-10-25T01:00:00Z", // 02:00 CET
				"2026-10-25T02:00:00Z", // 03:00 CET
				"2026-10-25T03:00:00Z",
			})
	})
}

func TestMatches(t *testing.T) {
	madrid := madridLocation(t)
	s := mustParse(t, "30 9 * * *")
	if !s.Matches(mustTime(t, "2026-07-15T07:30:00Z"), madrid) {
		t.Fatal("09:30 CEST should match")
	}
	if s.Matches(mustTime(t, "2026-07-15T09:30:00Z"), madrid) {
		t.Fatal("11:30 CEST should not match")
	}
	if !s.Matches(mustTime(t, "2026-07-15T09:30:00Z"), time.UTC) {
		t.Fatal("09:30 UTC should match")
	}
}

func TestZeroScheduleNeverFires(t *testing.T) {
	var s cronspec.Schedule
	if got := s.Next(time.Now(), time.UTC); !got.IsZero() {
		t.Fatalf("zero Schedule Next = %s, want zero", got)
	}
}

func TestLoadLocation(t *testing.T) {
	if loc, err := cronspec.LoadLocation(""); err != nil || loc != time.UTC {
		t.Fatalf("LoadLocation(\"\") = %v, %v; want UTC", loc, err)
	}
	if loc, err := cronspec.LoadLocation("Europe/Madrid"); err != nil || loc.String() != "Europe/Madrid" {
		t.Fatalf("LoadLocation(Europe/Madrid) = %v, %v", loc, err)
	}
	for _, name := range []string{"Mars/Olympus", "Local", "Europe/"} {
		if _, err := cronspec.LoadLocation(name); !errors.Is(err, cronspec.ErrInvalid) {
			t.Fatalf("LoadLocation(%q) error = %v, want ErrInvalid", name, err)
		}
	}
}
