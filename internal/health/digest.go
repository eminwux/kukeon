// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package health

import (
	"sort"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// FailedCells picks the cells in a terminal failure state and tags each with
// who it belongs to: Failed is kukeon's own bring-up failure, Error is a
// workload that exited non-zero (issue #1267).
func FailedCells(cells []v1beta1.CellDoc) []kukeonv1.HealthCell {
	out := []kukeonv1.HealthCell{}
	for i := range cells {
		cell := &cells[i]
		var class string
		switch cell.Status.State {
		case v1beta1.CellStateFailed:
			class = kukeonv1.HealthClassKukeon
		case v1beta1.CellStateError:
			class = kukeonv1.HealthClassWorkload
		default:
			continue
		}
		out = append(out, kukeonv1.HealthCell{
			Realm: cell.Spec.RealmID, Space: cell.Spec.SpaceID, Stack: cell.Spec.StackID,
			Name: cell.Metadata.Name, State: cell.Status.State.String(), Classification: class,
			Since: cellSince(cell), Reason: cell.Status.Reason, Message: cell.Status.Message,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Realm+"/"+a.Space+"/"+a.Stack+"/"+a.Name < b.Realm+"/"+b.Space+"/"+b.Stack+"/"+b.Name
	})
	return out
}

// cellSince is the closest record kukeond keeps of when a cell entered its
// state. No transition time is stored, so an Error cell uses its newest
// container finish time and anything else its last status write.
func cellSince(cell *v1beta1.CellDoc) time.Time {
	if cell.Status.State == v1beta1.CellStateError {
		var newest time.Time
		for _, container := range cell.Status.Containers {
			if container.FinishTime.After(newest) {
				newest = container.FinishTime
			}
		}
		if !newest.IsZero() {
			return newest
		}
	}
	if !cell.Status.UpdatedAt.IsZero() {
		return cell.Status.UpdatedAt
	}
	return cell.Status.CreatedAt
}

// Clean reports whether a digest holds nothing to triage.
func Clean(digest *kukeonv1.HealthDigest) bool {
	for _, check := range digest.Checks {
		if check.Status != kukeonv1.HealthStatusOK {
			return false
		}
	}
	return len(digest.LogErrors) == 0 && len(digest.Cells) == 0
}
