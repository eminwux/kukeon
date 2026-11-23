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

package initialize

import (
	"fmt"
	"io/fs"
	"strings"

	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/internal/util/buildinfo"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// sourceSkewError is a preflight composition failure over fetched sources. Its
// message names them; withRerun does not blame a local prerequisite for it.
type sourceSkewError struct{ err error }

func (e *sourceSkewError) Error() string { return e.err.Error() }
func (e *sourceSkewError) Unwrap() error { return e.err }

// compositionError wraps a failed composition with the sources it read.
func compositionError(err error, declared []config.Source, trees map[string]fs.FS) error {
	hint := sourceSkewHint(declared, trees)
	wrapped := fmt.Errorf("preflight composition: %w%s", err, hint)
	if hint == "" {
		return wrapped
	}
	return &sourceSkewError{err: wrapped}
}

// sourceSkewHint names the fetched sources a failed composition read, so a
// document the binary rejects points at the revision it came from rather than
// at a local prerequisite (#1631). kuke reads exactly one schema version, so a
// rejected apiVersion means that revision and this binary disagree on it.
func sourceSkewHint(declared []config.Source, trees map[string]fs.FS) string {
	var names []string
	for _, source := range declared {
		if _, ok := trees[source.Name]; !ok {
			continue
		}
		location := "URL withheld"
		if _, err := gitremote.URL(source.URL); err == nil {
			location = source.URL
		}
		names = append(names, fmt.Sprintf("%s (%s, %s)", source.Name, location, source.Revision()))
	}
	if len(names) == 0 {
		return ""
	}
	version := buildinfo.Version()
	if version == "" {
		version = "version unknown"
	}
	return fmt.Sprintf(
		"; composed from fetched sources %s, and this kuke (%s) reads only %s documents: "+
			"a rejected apiVersion means a source revision and this binary disagree on schema version, "+
			"so pin that source to a revision that matches this binary",
		strings.Join(names, ", "), version, config.APIVersion,
	)
}
