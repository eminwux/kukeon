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
	hint := sourceSkewHint(declared, map[string]fs.FS{"roles": fstest.MapFS{}}, config.KukeonSource())
	if !strings.Contains(hint, "roles (URL withheld, tag v1.0.0)") {
		t.Errorf("hint %q must name the fetched source with its URL withheld", hint)
	}
	if strings.Contains(hint, "token") || strings.Contains(hint, "unused") {
		t.Errorf("hint %q leaks a credential or names an unfetched source", hint)
	}
	if got := sourceSkewHint(declared, nil, config.KukeonSource()); got != "" {
		t.Errorf("no fetched source must add no hint, got %q", got)
	}
}

// A stored kukeon Source at a revision other than the binary's pin is what
// --force and --reapply keep composing from: the hint names
// kuke team init --upgrade, the command that re-pins it.
func TestSourceSkewHintNamesUpgradeForAStaleKukeonSource(t *testing.T) {
	const old = "122e0cfde8b89ecbd90b96eaad7bb8e103a937be"
	pin := config.Source{Name: config.KukeonSourceName, Commit: "02e0c77e0d0a1f1b2c3d4e5f60718293a4b5c6d7"}
	trees := map[string]fs.FS{config.KukeonSourceName: fstest.MapFS{}}
	for _, url := range []string{config.KukeonSourceURL, "https://github.com/eminwux/kukeon.git"} {
		declared := []config.Source{{Name: config.KukeonSourceName, URL: url, Commit: old}}
		hint := sourceSkewHint(declared, trees, pin)
		want := "run kuke team init --upgrade to re-pin Source kukeon to this binary's commit " + pin.Commit
		if !strings.Contains(hint, want) || strings.Contains(hint, "pin that source to a revision") {
			t.Errorf("url %s: hint %q lacks %q", url, hint, want)
		}
	}
	for name, declared := range map[string]config.Source{
		"at the pin":     {Name: config.KukeonSourceName, URL: config.KukeonSourceURL, Commit: pin.Commit},
		"another URL":    {Name: config.KukeonSourceName, URL: "github.com/example/kukeon-fork", Commit: old},
		"another source": {Name: "roles", URL: config.KukeonSourceURL, Commit: old},
	} {
		hint := sourceSkewHint([]config.Source{declared}, map[string]fs.FS{declared.Name: fstest.MapFS{}}, pin)
		if strings.Contains(hint, "--upgrade") || !strings.Contains(hint, "pin that source to a revision") {
			t.Errorf("%s: hint %q must not name --upgrade", name, hint)
		}
	}
}

// kuke team init --force composes from the stored kukeon Source at its
// revision; when that revision is not the binary's, the skew error names
// kuke team init --upgrade.
func TestForceInitOverAStaleKukeonSourceNamesUpgrade(t *testing.T) {
	opts, fake := setupInit(t)
	const old = "122e0cfde8b89ecbd90b96eaad7bb8e103a937be"
	fake.storeSource(config.KukeonSourceName, config.SourceSpec{URL: config.KukeonSourceURL, Commit: old})
	const harnessPath = "team/harnesses/codex/harness.yaml"
	raw, err := os.ReadFile("../../../" + harnessPath)
	if err != nil {
		t.Fatal(err)
	}
	skewed := strings.Replace(string(raw), "renderer: kukeon/bundled", "renderer: kukeon/v1beta1", 1)
	if skewed == string(raw) {
		t.Fatalf("%s does not select renderer kukeon/bundled", harnessPath)
	}
	opts.Sources = map[string]fs.FS{"kukeon": overlayFS{
		base:    os.DirFS("../../.."),
		overlay: fstest.MapFS{harnessPath: {Data: []byte(skewed)}},
	}}
	opts.Force = true
	_, err = Init(t.Context(), opts)
	if err == nil {
		t.Fatal("init must reject a harness with the renamed renderer")
	}
	for _, want := range []string{
		`spec.renderer: "kukeon/v1beta1" was renamed to kukeon/bundled`,
		"fetched sources kukeon (" + config.KukeonSourceURL + ", commit " + old + ")",
		"run kuke team init --upgrade to re-pin Source kukeon to this binary's " + config.KukeonSource().Revision(),
		"then rerun kuke team init",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err, want)
		}
	}
	if len(fake.raw) != 0 {
		t.Errorf("a failed preflight must apply nothing:\n%s", fake.raw)
	}
}
