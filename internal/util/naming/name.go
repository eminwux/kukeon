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

package naming

import "regexp"

// NamePattern is the one rule for every name kukeon joins into a path segment
// under <RunPath>/data: realm, space, stack, cell, container, Secret,
// CellBlueprint, CellConfig, Volume, Team and Source (issue #1719).
//
// Lower-case letters and digits, with '.' and '-' allowed only between them.
// That excludes ".", "..", a leading dot (so a name can never shadow the
// secrets sidecars .types/, .meta/ or .secret-*.tmp), '_' (the containerd ID
// separator, and what keeps kukeon's own scope entries apart from child scopes),
// '/', '\', whitespace, NUL and upper case.
const NamePattern = `^[a-z0-9]+([a-z0-9.-]*[a-z0-9])?$`

var nameRE = regexp.MustCompile(NamePattern)

// IsValidName reports whether value satisfies NamePattern. The value is
// checked as given: callers that trim input trim it first.
func IsValidName(value string) bool {
	return nameRE.MatchString(value)
}

// IsValidSegment is IsValidName for an optional scope coordinate: an empty
// value is legal (the coordinate is unset), anything else must be a valid name.
func IsValidSegment(value string) bool {
	return value == "" || IsValidName(value)
}

// nameRuleHint is appended to invalid-name errors so the operator sees the rule.
const nameRuleHint = "must be lower-case letters and digits, with '.' or '-' only between them"
