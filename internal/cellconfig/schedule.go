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

package cellconfig

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/eminwux/kukeon/internal/cronspec"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// ValidateSchedule checks the parts of a CellConfig's spec.schedule that need
// no blueprint to be judged (issue #1503): the cron expression parses, the
// time zone is known, the concurrency policy is the one supported, and the
// starting deadline is not negative. A nil schedule is valid — the Config is
// simply not scheduled. Whether the Config's default mode can run unattended
// is checked against the referenced blueprint at reconcile time.
func ValidateSchedule(schedule *v1beta1.CellConfigSchedule) error {
	if schedule == nil {
		return nil
	}
	if _, _, err := ParseSchedule(*schedule); err != nil {
		return err
	}
	if policy := strings.TrimSpace(schedule.ConcurrencyPolicy); policy != "" &&
		policy != v1beta1.ConcurrencyPolicyForbid {
		return fmt.Errorf("%w: spec.schedule.concurrencyPolicy %q is not supported (supported: %s)",
			errdefs.ErrConfigSchedule, schedule.ConcurrencyPolicy, v1beta1.ConcurrencyPolicyForbid)
	}
	if deadline := schedule.StartingDeadlineSeconds; deadline != nil && *deadline < 0 {
		return fmt.Errorf("%w: spec.schedule.startingDeadlineSeconds must not be negative (got %d)",
			errdefs.ErrConfigSchedule, *deadline)
	}
	return nil
}

// ParseSchedule parses a schedule's cron expression and resolves its time
// zone (UTC when unset).
func ParseSchedule(schedule v1beta1.CellConfigSchedule) (cronspec.Schedule, *time.Location, error) {
	spec, err := cronspec.Parse(schedule.Cron)
	if err != nil {
		return cronspec.Schedule{}, nil, fmt.Errorf("%w: spec.schedule.cron: %w", errdefs.ErrConfigSchedule, err)
	}
	loc, err := cronspec.LoadLocation(schedule.Timezone)
	if err != nil {
		return cronspec.Schedule{}, nil, fmt.Errorf(
			"%w: spec.schedule.timezone: %w", errdefs.ErrConfigSchedule, err)
	}
	return spec, loc, nil
}

// ScheduledCellName is the name of the cell a Config's fire at fireTime
// creates: `<config>-<unix-fire-time>`. Deriving it from the fire time makes a
// fire idempotent — retrying one finds the cell it already made.
func ScheduledCellName(configName string, fireTime time.Time) string {
	return strings.TrimSpace(configName) + "-" + strconv.FormatInt(fireTime.Unix(), 10)
}
