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
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/pkg/team/config"
)

// overlayFS serves the overlay's files ahead of the base filesystem.
type overlayFS struct {
	base    fs.FS
	overlay fstest.MapFS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	if _, ok := o.overlay[name]; ok {
		return o.overlay.Open(name)
	}
	return o.base.Open(name)
}

// A role.yaml at another schema version fails init naming the fetched source
// and revision, not a local prerequisite (#1631).
func TestPreflightCompositionNamesTheSkewedSource(t *testing.T) {
	opts, fake := setupInit(t)
	const rolePath = "team/roles/majordomo/role.yaml"
	raw, err := os.ReadFile("../../../" + rolePath)
	if err != nil {
		t.Fatal(err)
	}
	skewed := strings.Replace(string(raw), "apiVersion: "+config.APIVersion, "apiVersion: kuketeams.io/v1", 1)
	if skewed == string(raw) {
		t.Fatalf("%s does not declare apiVersion %s", rolePath, config.APIVersion)
	}
	opts.Sources = map[string]fs.FS{"kukeon": overlayFS{
		base:    os.DirFS("../../.."),
		overlay: fstest.MapFS{rolePath: {Data: []byte(skewed)}},
	}}
	_, err = Init(t.Context(), opts)
	if err == nil {
		t.Fatal("init must reject a role at another schema version")
	}
	source := config.KukeonSource()
	for _, want := range []string{
		"preflight composition: ",
		"role.yaml: require " + config.APIVersion + " Role",
		"fetched sources kukeon (" + config.KukeonSourceURL + ", " + source.Revision() + ")",
		"reads only " + config.APIVersion + " documents",
		"disagree on schema version",
		"then rerun kuke team init",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "after fixing the prerequisite") {
		t.Errorf("error %q blames a local prerequisite", err)
	}
	if len(fake.raw) != 0 {
		t.Errorf("a failed preflight must apply nothing:\n%s", fake.raw)
	}
}

func TestSourceSkewHintWithholdsUnsafeURLsAndSkipsUnfetchedSources(t *testing.T) {
	declared := []config.Source{
		{Name: "roles", URL: "https://user:token@example.test/roles", Tag: "v1.0.0"},
		{Name: "unused", URL: "https://example.test/unused", Branch: "main"},
	}
	hint := sourceSkewHint(declared, map[string]fs.FS{"roles": fstest.MapFS{}})
	if !strings.Contains(hint, "roles (URL withheld, tag v1.0.0)") {
		t.Errorf("hint %q must name the fetched source with its URL withheld", hint)
	}
	if strings.Contains(hint, "token") || strings.Contains(hint, "unused") {
		t.Errorf("hint %q leaks a credential or names an unfetched source", hint)
	}
	if got := sourceSkewHint(declared, nil); got != "" {
		t.Errorf("no fetched source must add no hint, got %q", got)
	}
}
