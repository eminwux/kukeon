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
// resources or escape the metadata tree once joined into a path. The name must
// satisfy the shared NamePattern (issue #1719): besides path traversal ("." and
// "..") it excludes "_", which collides with the containerd container-ID
// separator ({space}_{stack}_{cell}_{container}), and "/", which would inject
// extra path components into the cgroup path (/kukeon/{realm}/{space}/...).
//
// Empty names are also rejected for callers that want a single entrypoint;
// callers that need a separate "required" error should keep using
// errdefs.ErrRealmNameRequired before invoking this.
func ValidateRealmName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return errdefs.ErrRealmNameRequired
	}
	if !IsValidName(trimmed) {
		return fmt.Errorf("%w: %q %s", errdefs.ErrInvalidRealmName, trimmed, nameRuleHint)
	}
	return nil
}
