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

// SetScheduleFireForTest replaces the scheduler's cell create so external
// tests can drive RunSchedules' decisions — what fires, what is skipped, what
// is recorded — without standing up a full cell create. Issue #1503.
func (b *Exec) SetScheduleFireForTest(fire func(CellFromConfigRequest) error) {
	b.schedules.fire = fire
}
