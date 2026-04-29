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

package naming

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// ValidateHierarchyName rejects space, stack, cell, and container names that
// would corrupt downstream resources, the same way ValidateRealmName guards
// realm names. Two characters are blocked:
//
//   - "_" collides with the containerd container-ID separator
//     ({space}_{stack}_{cell}_{container}) built by BuildContainerdID. A name
//     containing "_" produces an ambiguous ID that the inverse parser cannot
//     round-trip back to its hierarchy components.
//   - "/" injects extra path components into the cgroup path
//     (/kukeon/{realm}/{space}/{stack}/{cell}/...).
//
// Empty / whitespace-only names are also rejected so callers have a single
// entrypoint. Callers that need to surface a "required" error per kind
// should check emptiness and return their own sentinel before invoking this.
//
// The kind argument ("space", "stack", "cell", "container") is included in
// the error message so the operator knows which input was rejected.
func ValidateHierarchyName(kind, name string) error {
	if strings.TrimSpace(kind) == "" {
		return errors.New("hierarchy kind is required")
	}
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%w: %s name is required", errdefs.ErrInvalidName, kind)
	}
	if strings.ContainsAny(trimmed, "_/") {
		return fmt.Errorf("%w: %s name %q contains disallowed character (must not contain '_' or '/')",
			errdefs.ErrInvalidName, kind, trimmed)
	}
	return nil
}
