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

package fs

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// Scope depths below MetadataRoot, for EnsureContained on a scope directory.
const (
	RealmDepth = 1
	SpaceDepth = 2
	StackDepth = 3
	CellDepth  = 4
)

// EnsureContained is the containment guard the destructive helpers run before
// removing target (issue #1719). It returns errdefs.ErrPathEscapesScope unless
// target, after filepath.Clean, lies exactly depth path elements below parent
// (depth >= 1, so parent itself is never a valid target).
//
// The path builders filepath.Join their segments, which cleans them, so a "."
// or ".." in any segment shortens the result: a space named ".." resolves its
// directory to the realm's parent, MetadataRoot. Checking the exact depth
// rather than mere containment catches a traversal in any segment, not only
// the last, as long as parent is built from trusted input (MetadataRoot, or a
// kind's directory for a direct-child document).
func EnsureContained(parent, target string, depth int) error {
	cleanParent := filepath.Clean(parent)
	cleanTarget := filepath.Clean(target)
	rel, err := filepath.Rel(cleanParent, cleanTarget)
	if err != nil || depth < 1 || rel == "." || filepath.IsAbs(rel) ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		len(strings.Split(rel, string(filepath.Separator))) != depth {
		return fmt.Errorf("%w: %q is not %d level(s) inside %q",
			errdefs.ErrPathEscapesScope, target, depth, parent)
	}
	return nil
}
