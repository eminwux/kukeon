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

package modelhub

import (
	"time"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// CellConfig is the internal carrier for a daemon-stored cell identity (kind:
// CellConfig, issue #624). Like CellBlueprint the daemon never interprets its
// body for storage: the canonical CellConfigDoc travels as an opaque
// serialized Document — the daemon writes it under the scope and reads it
// back. Scope coordinates are lifted onto the metadata so the storage runner
// can resolve the on-disk path without parsing the body. Apply-time slot-fill
// validation (which does need the body and the referenced blueprint)
// re-derives the typed view from Document via apischeme.
//
// The schedule state of a Config with a spec.schedule (#1503) is the one piece
// of status a Config has. It is stored beside the document, never inside it,
// so the scheduler's writes cannot race an apply or change the document's
// revision hash.
type CellConfig struct {
	Metadata CellConfigMetadata
	Document []byte
	// Schedule lifts the document's spec.schedule for the list path, which
	// returns no Document. Nil for an unscheduled Config.
	Schedule *CellConfigSchedule
	// ScheduleStatus is the stored schedule state; nil when the scheduler has
	// recorded none. The controller's get and list fill it.
	ScheduleStatus *CellConfigScheduleStatus
	// Connectors is how each connector the Config requires resolves right
	// now; the controller's get and list fill it (issue #1548).
	Connectors []v1beta1.ConnectorStatus
}

// CellConfigSchedule is the summary of a Config's spec.schedule a list shows.
type CellConfigSchedule struct {
	Cron     string
	Timezone string
	Suspend  bool
}

// CellConfigScheduleStatus is what the scheduler last observed and did for a
// scheduled Config. See external v1beta1.CellConfigScheduleStatus.
type CellConfigScheduleStatus struct {
	LastScheduleTime *time.Time
	NextScheduleTime *time.Time
	LastCell         string
	ActiveCells      []string
	SkippedFires     int64
	LastError        string
}

// CellConfigMetadata identifies a Config by name and the scope it binds to. A
// Config is scopable at realm, space, or stack only — never cell. The scope is
// the deepest non-empty coordinate; a deeper coordinate requires every
// shallower one. See external v1beta1.CellConfigMetadata for the full contract.
type CellConfigMetadata struct {
	Name  string
	Realm string
	Space string
	Stack string
	// Labels lifts the config document's metadata.labels onto the carrier
	// so daemon-side code can filter (`kukeon.io/team=<team>` for per-team
	// prune apply, #1027) without re-parsing the Document on every access.
	// ListConfigs populates it; WriteConfig preserves it via the Document
	// round-trip — the labels travel inside the canonical serialized doc,
	// the carrier just lifts a typed view.
	Labels map[string]string
	// Annotations lifts the document's metadata.annotations the same way,
	// so a list shows a Config's display name (issue #1692).
	Annotations map[string]string
}
