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

import "context"

// RemoveSystemdUnits exposes the unit teardown so tests can drive it with a
// fake systemctl and unit files under a temp dir instead of the host's.
func RemoveSystemdUnits(
	ctx context.Context,
	lookPath func(file string) (string, error),
	systemctl func(ctx context.Context, args ...string) error,
	units []SystemdUnit,
) []SystemdRemovalReport {
	return systemdHost{lookPath: lookPath, systemctl: systemctl}.removeUnits(ctx, units)
}

// DefaultSystemdUnits exposes the production unit list and its order.
func DefaultSystemdUnits() []SystemdUnit {
	return systemdUnits()
}
