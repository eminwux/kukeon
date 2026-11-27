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

package fs_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/fs"
)

func TestEnsureContained(t *testing.T) {
	run := "/opt/kukeon"
	root := fs.MetadataRoot(run)
	tests := []struct {
		name   string
		parent string
		target string
		depth  int
		ok     bool
	}{
		{"realm dir", root, fs.RealmMetadataDir(run, "alpha"), fs.RealmDepth, true},
		{"space dir", root, fs.SpaceMetadataDir(run, "alpha", "s"), fs.SpaceDepth, true},
		{"stack dir", root, fs.StackMetadataDir(run, "alpha", "s", "st"), fs.StackDepth, true},
		{"cell dir", root, fs.CellMetadataDir(run, "alpha", "s", "st", "c"), fs.CellDepth, true},
		{"direct child", "/x/secrets", "/x/secrets/token", 1, true},
		{"unclean target", root, root + "/alpha/./s", fs.SpaceDepth, true},

		{"root itself", root, root, fs.RealmDepth, false},
		{"realm ..", root, fs.RealmMetadataDir(run, ".."), fs.RealmDepth, false},
		{"realm .", root, fs.RealmMetadataDir(run, "."), fs.RealmDepth, false},
		{"space ..", root, fs.SpaceMetadataDir(run, "alpha", ".."), fs.SpaceDepth, false},
		{"space .", root, fs.SpaceMetadataDir(run, "alpha", "."), fs.SpaceDepth, false},
		{"stack .. keeps cell depth short", root, fs.CellMetadataDir(run, "a", "s", "..", "c"), fs.CellDepth, false},
		{"space .. with deeper names", root, fs.StackMetadataDir(run, "a", "..", "x"), fs.StackDepth, false},
		{"too deep", root, fs.CellMetadataDir(run, "a", "s", "st", "c"), fs.StackDepth, false},
		{"sibling of parent", "/x/secrets", "/x/secretsx/token", 1, false},
		{"child ..", "/x/configs", "/x/configs/../metadata.json", 1, false},
		{"escapes tree", "/x/blueprints", "/x/blueprints/../../../y", 1, false},
		{"zero depth", root, root, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fs.EnsureContained(tt.parent, tt.target, tt.depth)
			if tt.ok && err != nil {
				t.Fatalf("EnsureContained(%q, %q, %d) = %v, want nil", tt.parent, tt.target, tt.depth, err)
			}
			if !tt.ok && !errors.Is(err, errdefs.ErrPathEscapesScope) {
				t.Fatalf("EnsureContained(%q, %q, %d) = %v, want ErrPathEscapesScope",
					tt.parent, tt.target, tt.depth, err)
			}
		})
	}
}
