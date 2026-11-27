// Copyright 2026 Emiliano Spinella (eminwux)
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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/util/naming"
)

// validateScopeSegments rejects, with sentinel, a resource name or scope
// coordinate that fails the shared name rule (naming.NamePattern, issue #1719)
// before a lookup joins it into a path under <RunPath>/data. Values are
// trimmed, matching the lookup validators' required-field checks; empty values
// are unset and pass, since whether a value is required is the caller's
// concern. coords are the scope coordinates outermost first (realm, space, ...).
func validateScopeSegments(sentinel error, name string, coords ...string) error {
	for i, value := range append([]string{name}, coords...) {
		value = strings.TrimSpace(value)
		if naming.IsValidSegment(value) {
			continue
		}
		fields := [...]string{"name", "realm", "space", "stack", "cell"}
		field := fields[min(i, len(fields)-1)]
		return fmt.Errorf("%w (%s %q)", sentinel, field, value)
	}
	return nil
}
