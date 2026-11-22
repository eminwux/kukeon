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

// Package teamtest holds what the team packages' tests share.
package teamtest

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
)

// MajordomoRole is the shipped role document, relative to the repository root.
const MajordomoRole = "team/roles/majordomo/role.yaml"

// shippedRoleExtras is what the shipped majordomo role declares beyond a plain
// role: its host mounts and its scheduled task. Composer and init tests use
// the role as a neutral fixture and add what each case needs.
const shippedRoleExtras = `    mounts:
      - kukeond-socket
      - kuke-binary
  tasks:
    - name: health-sweep
      skill: health-sweep
      schedule: "@hourly"
      suspend: true
`

// PlainRole returns the majordomo role document without its host mounts and
// task. It fails when the document no longer contains them verbatim, so a
// change to role.yaml cannot silently turn the fixture back into the shipped
// role.
func PlainRole(raw []byte) ([]byte, error) {
	if !bytes.Contains(raw, []byte(shippedRoleExtras)) {
		return nil, errors.New("teamtest: majordomo role.yaml no longer matches shippedRoleExtras")
	}
	return bytes.Replace(raw, []byte(shippedRoleExtras), nil, 1), nil
}

// PlainSource copies team/roles and team/harnesses of the repository at root
// into memory, with the majordomo role made plain. The team/roles/operator
// compatibility symlink is skipped.
func PlainSource(root string) (fstest.MapFS, error) {
	out := fstest.MapFS{}
	for _, dir := range []string{"team/roles", "team/harnesses"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil // a symlink to a directory
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
			if name == MajordomoRole {
				if raw, err = PlainRole(raw); err != nil {
					return err
				}
			}
			out[name] = &fstest.MapFile{Data: raw}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
