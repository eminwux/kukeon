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

// Package connectortest serves a connector catalog with one connector kuke
// does not ship: `example-token`, defined by a single connector.yaml under
// testdata. Tests across kuke — the CLI, team composition, the daemon render
// and the web server — load it to prove that a connector is data: adding that
// one file makes the connector appear everywhere, with no code naming it
// (issue #1548).
package connectortest

import (
	"embed"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/team/connectors"
)

// Name is the test-only connector's name. Tests refer to it through this
// constant so the name appears in exactly one Go file.
const Name = "example-token"

// Slot, EnvName and Field are what the fixture declares: the secret slot its
// one method fills, the environment variable it delivers and its one readable
// identity field.
const (
	Slot    = "example-token"
	EnvName = "EXAMPLE_TOKEN"
	Field   = "account"
)

//go:embed testdata/*/connector.yaml
var fixture embed.FS

// FS is the embedded catalog plus the test-only connector, laid out the way
// connector.Load reads it.
func FS(t testing.TB) fs.FS {
	t.Helper()
	out := fstest.MapFS{}
	add := func(source fs.FS, pattern string, strip string) {
		files, err := fs.Glob(source, pattern)
		if err != nil {
			t.Fatalf("list connectors: %v", err)
		}
		for _, name := range files {
			raw, readErr := fs.ReadFile(source, name)
			if readErr != nil {
				t.Fatalf("read %s: %v", name, readErr)
			}
			out[name[len(strip):]] = &fstest.MapFile{Data: raw}
		}
	}
	add(connectors.FS, "*/connector.yaml", "")
	add(fixture, "testdata/*/connector.yaml", "testdata/")
	return out
}

// Catalog loads FS.
func Catalog(t testing.TB) *connector.Catalog {
	t.Helper()
	catalog, err := connector.Load(FS(t))
	if err != nil {
		t.Fatalf("load connector catalog: %v", err)
	}
	if _, ok := catalog.Find(Name); !ok {
		t.Fatalf("the catalog does not carry the test connector %q", Name)
	}
	return catalog
}
