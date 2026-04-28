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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// ValidateRealmName rejects realm names that would corrupt downstream
// resources. Two characters are blocked today:
//
//   - "_" collides with the containerd container-ID separator
//     ({space}_{stack}_{cell}_{container}) and would break the parser
//     that splits IDs back into hierarchy components.
//   - "/" injects extra path components into the cgroup path
//     (/kukeon/{realm}/{space}/...).
//
// Empty names are also rejected for callers that want a single entrypoint;
// callers that need a separate "required" error should keep using
// errdefs.ErrRealmNameRequired before invoking this.
func ValidateRealmName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errdefs.ErrRealmNameRequired
	}
	if strings.ContainsAny(trimmed, "_/") {
		return fmt.Errorf("%w: %q contains disallowed character (must not contain '_' or '/')",
			errdefs.ErrInvalidRealmName, trimmed)
	}
	return nil
}
