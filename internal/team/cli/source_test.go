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
	"slices"
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

// TestSourceAddListShowRemove is AC13.
func TestSourceAddListShowRemove(t *testing.T) {
	h := sourceHarness(t)
	out, err := h.run(t, "source", "add", "extra", "--url", extraURL, "--branch", "main")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Source extra created: " + extraURL + ", branch main, cloned at /home/user/extra; offers",
		"No stored team inherits the defaults.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("add output lacks %q:\n%s", want, out)
		}
	}
	defaults := h.store.storedDefaults(t)
	i := slices.IndexFunc(defaults.Spec.Defaults.Sources, func(s config.Source) bool { return s.Name == "extra" })
	if i < 0 || defaults.Spec.Defaults.Sources[i].ClonePath != "/home/user/extra" {
		t.Fatalf("sources = %+v", defaults.Spec.Defaults.Sources)
	}
	if out, err = h.run(t, "source", "add", "extra", "--url", extraURL, "--tag", "v1"); err != nil ||
		!strings.Contains(out, "Source extra updated") {
		t.Fatalf("update: %v\n%s", err, out)
	}
	out, err = h.run(t, "source", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAME", "CLONE PATH", "kukeon", "branch main", "extra", extraURL, "tag v1", "/home/user/extra"} {
		if !strings.Contains(out, want) {
			t.Errorf("list lacks %q:\n%s", want, out)
		}
	}
	out, err = h.run(t, "source", "show", "kukeon")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Source kukeon:", "ROLE", "majordomo", "team/roles/majordomo", "HARNESS", "codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "testdata") {
		t.Errorf("show lists a testdata role (G3):\n%s", out)
	}
	if out, err = h.run(t, "source", "remove", "extra"); err != nil || !strings.Contains(out, "Source extra removed") {
		t.Fatalf("remove: %v\n%s", err, out)
	}
	if _, err = h.run(t, "source", "show", "extra"); err == nil || !strings.Contains(err.Error(), "not registered") {
		t.Fatalf("show removed: %v", err)
	}
}

// TestSourceRemoveIsRefusedWhileReferenced is AC13: a default or a stored
// Team that inherits the sources names the source.
func TestSourceRemoveIsRefusedWhileReferenced(t *testing.T) {
	h := sourceHarness(t)
	if _, err := h.run(t, "source", "remove", "kukeon"); err == nil ||
		!strings.Contains(err.Error(), "default harness codex") {
		t.Fatalf("error = %v", err)
	}
	if _, err := h.run(t, "source", "add", "extra", "--url", extraURL, "--branch", "main"); err != nil {
		t.Fatal(err)
	}
	h.store.putTeam(t, &config.Team{
		APIVersion: config.APIVersion, Kind: config.KindTeam,
		Metadata: config.Metadata{Name: "proj", Realm: "default"},
		Spec: config.TeamSpec{Defaults: config.Defaults{
			Roles: []config.Role{{Name: "lead", Source: "extra", Path: "team/roles/majordomo"}},
		}},
	})
	writes := h.store.writes
	if _, err := h.run(t, "source", "remove", "extra"); err == nil || !strings.Contains(err.Error(), "team proj") {
		t.Fatalf("error = %v", err)
	}
	if h.store.writes != writes {
		t.Fatal("a refused remove wrote")
	}
	if _, err := h.run(t, "source", "remove", "nothing"); err == nil ||
		!strings.Contains(err.Error(), "not registered") {
		t.Fatalf("error = %v", err)
	}
}

func TestSourceAddValidates(t *testing.T) {
	for _, args := range [][]string{
		{"source", "add", "project", "--url", extraURL, "--branch", "main"},
		{"source", "add", "Bad_Name", "--url", extraURL, "--branch", "main"},
		{"source", "add", "extra", "--url", "not a url", "--branch", "main"},
		{"source", "add", "extra", "--url", extraURL, "--branch", "main", "--clone-path", "relative"},
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
	h.store.putTeam(t, storedProject())
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
	if len(d.Roles) != 1 || d.Roles[0].Name != "lead" || len(d.Harnesses) != 2 {
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
