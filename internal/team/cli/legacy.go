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

package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// legacyPaths are the per-user files teams lived in before kukeond stored
// them (issue #1618). Nothing reads, converts or imports them; their only
// trace is the note below.
func legacyPaths(home string) []string {
	return []string{
		filepath.Join(home, ".kuke", "kuketeams.yaml"),
		filepath.Join(home, ".kuke", "kuketeam.d"),
	}
}

// legacyNote is printed once by kuke team init, add and apply when either
// old path exists.
const legacyNote = "Note: ~/.kuke/kuketeams.yaml and ~/.kuke/kuketeam.d/ are no longer read: kukeond stores " +
	"teams now. Add each team again with kuke team add; nothing is converted or imported.\n"

func printLegacyNote(w io.Writer, home string) error {
	for _, path := range legacyPaths(home) {
		if _, err := os.Lstat(path); err == nil {
			_, err = fmt.Fprint(w, legacyNote)
			return err
		} else if !errors.Is(err, os.ErrNotExist) {
			continue
		}
	}
	return nil
}
