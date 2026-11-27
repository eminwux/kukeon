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

package cli_test

import (
	"os"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const extraURL = "https://example.test/extra.git"

// sourceHarness has the initial TeamDefaults stored and a source "extra"
// whose tree is this repository.
func sourceHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness(t)
	h.sources["extra"] = os.DirFS("../../..")
	h.store.putDefaults(t, initialize.InitialDefaults("default"))
	return h
}

// TestCreateSourceAndShow is kuke create source and kuke source show (#1643
// Step 6): create stores a Source document, refuses a stored name without
// --force, and a replacement names the teams that use it.
func TestCreateSourceAndShow(t *testing.T) {
	h := sourceHarness(t)
	out, err := h.run(t, "create", "source", "extra", "--url", extraURL, "--branch", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Source extra created: example.test/extra.git, branch main; offers") {
		t.Errorf("create output:\n%s", out)
	}
	if raw := string(h.store.sources["extra"]); !strings.Contains(raw, "kind: Source") ||
		!strings.Contains(raw, "url: "+extraURL) || strings.Contains(raw, "clonePath") {
		t.Fatalf("stored Source:\n%s", raw)
	}
	if d := h.store.storedDefaults(t); d.Spec.Defaults.Sources != nil {
		t.Fatalf("create source wrote the defaults: %+v", d.Spec.Defaults.Sources)
	}
	writes := h.store.writes
	if _, err = h.run(t, "create", "source", "extra", "--url", extraURL, "--tag", "v1"); err == nil ||
		!strings.Contains(err.Error(), "--force") || h.store.writes != writes {
		t.Fatalf("create of a stored Source: %v", err)
	}
	h.store.putTeam(t, &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{Defaults: config.Defaults{
			Roles: []config.Role{{Name: "lead", Source: "extra", Path: "team/roles/majordomo"}},
		}},
	})
	out, err = h.run(t, "create", "source", "extra", "--url", extraURL, "--tag", "v1", "--force")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Source extra updated: example.test/extra.git, tag v1", "Used by: team proj (role lead)",
		"kuke team apply proj",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("replace output lacks %q:\n%s", want, out)
		}
	}
	out, err = h.run(t, "source", "show", "kukeon")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Source kukeon:", "ROLE", "majordomo", "team/roles/majordomo", "HARNESS", "codex",
		"health-sweep (@hourly, suspended)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "testdata") {
		t.Errorf("show lists a testdata role (G3):\n%s", out)
	}
	if _, err = h.run(t, "source", "show", "nothing"); err == nil ||
		!strings.Contains(err.Error(), "kuke create source nothing") {
		t.Fatalf("show of no Source: %v", err)
	}
}

// TestCreateSourceDryRunAndIdentity: --dry-run stores nothing; --identity is
// the Source's spec.git.identityRef.
func TestCreateSourceDryRunAndIdentity(t *testing.T) {
	h := sourceHarness(t)
	h.sources["other"] = h.sources["extra"]
	out, err := h.run(t, "create", "source", "other", "--url", extraURL, "--branch", "main", "--dry-run")
	if err != nil || !strings.Contains(out, "dry run, nothing stored") || h.store.writes != 0 {
		t.Fatalf("dry run: %v writes=%d\n%s", err, h.store.writes, out)
	}
	out, err = h.run(t, "create", "source", "other", "--url", extraURL, "--branch", "main", "--identity", "gh")
	if err != nil || !strings.Contains(out, "identity gh") {
		t.Fatalf("identity: %v\n%s", err, out)
	}
	if raw := string(h.store.sources["other"]); !strings.Contains(raw, "identityRef") ||
		!strings.Contains(raw, "name: gh") {
		t.Fatalf("stored Source:\n%s", raw)
	}
}

func TestCreateSourceValidates(t *testing.T) {
	for _, args := range [][]string{
		{"create", "source", "project", "--url", extraURL, "--branch", "main"},
		{"create", "source", "Bad_Name", "--url", extraURL, "--branch", "main"},
		{"create", "source", "extra", "--url", "not a url", "--branch", "main"},
		{"create", "source", "extra", "--url", extraURL, "--branch", "main", "--clone-path", "/opt/x"},
		{"create", "source", "extra", "--branch", "main"},
	} {
		h := sourceHarness(t)
		if _, err := h.run(t, args...); err == nil || h.store.writes != 0 {
			t.Fatalf("%v: err=%v writes=%d", args, err, h.store.writes)
		}
	}
}

// TestDefaultsSet is AC14: the flags given replace their list, and the
// output names the inheriting teams and how to recompose them.
func TestDefaultsSet(t *testing.T) {
	h := sourceHarness(t)
	h.store.putProject(t)
	out, err := h.run(t, "defaults", "set", "--role", "lead=kukeon:team/roles/majordomo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Defaults updated.", "Teams inheriting the defaults (not recomposed): proj", "kuke team apply proj"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	d := h.store.storedDefaults(t).Spec.Defaults
	if len(d.Roles) != 1 || d.Roles[0].Name != "lead" || len(d.Harnesses) != 3 {
		t.Fatalf("defaults = %+v", d)
	}
	if _, err = h.run(t, "defaults", "set", "--harness", "codex"); err != nil {
		t.Fatal(err)
	}
	d = h.store.storedDefaults(t).Spec.Defaults
	if len(d.Harnesses) != 1 || d.Harnesses[0].Name != "codex" || len(d.Roles) != 1 {
		t.Fatalf("defaults = %+v", d)
	}
	writes := h.store.writes
	if _, err = h.run(t, "defaults", "set"); err == nil || !strings.Contains(err.Error(), "--role or --harness") {
		t.Fatalf("error = %v", err)
	}
	if _, err = h.run(t, "defaults", "set", "--role", "nobody"); err == nil {
		t.Fatal("accepted an unknown default role")
	}
	if h.store.writes != writes {
		t.Fatal("a refused defaults set wrote")
	}
}
