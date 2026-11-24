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
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestAddProjectFromAStoredSource is #1643 Step 5: --source NAME names a
// stored Source, which the team refers to; nothing new is stored.
func TestAddProjectFromAStoredSource(t *testing.T) {
	opts, _ := setupAdd(t)
	f := opts.Client.(*fakeClient)
	f.storeSource("app-repo", config.SourceSpec{URL: opts.Source, Branch: "trunk"})
	opts.Source = "app-repo"
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	// The entry states no ref: it reads the Source's (D4).
	project := storedTeam(t, f, opts.Name).Spec.ProjectSource
	if project.Stored != "app-repo" || project.URL != "" || project.Branch != "" {
		t.Fatalf("project = %+v", project)
	}
	for _, planned := range out.Plan.Sources {
		if planned.Name == "project" && (planned.Ref != "branch trunk" || planned.URL != opts.Client.(*fakeClient).
			sourceURL(t, "app-repo")) {
			t.Fatalf("planned project = %+v", planned)
		}
	}
	if _, ok := f.sources[opts.Name]; ok || out.Result.Resources != 3 {
		t.Fatalf("resources = %d; the add stored a Source of its own", out.Result.Resources)
	}

	opts.Source, opts.Force = "absent", true
	if _, err = Add(t.Context(), opts); !errors.Is(err, errdefs.ErrSourceNotFound) ||
		!strings.Contains(err.Error(), "kuke create source absent") {
		t.Fatalf("err = %v", err)
	}
}

// TestAddProjectFromTheFile is AC10: -f FILE with spec.projectSource.url
// needs neither --source nor a checkout, and a --source naming another
// repository wins with a note naming both.
func TestAddProjectFromTheFile(t *testing.T) {
	opts, _ := setupAdd(t)
	f := opts.Client.(*fakeClient)
	address := opts.Source
	opts.Source = ""
	opts.File = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: acme}\nspec:\n" +
		"  projectSource: {url: '" + address + "', branch: trunk}\n")
	opts.FileName = "acme.yaml"
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if got := storedSource(t, f, "acme"); got.URL != address || got.Branch != "trunk" {
		t.Fatalf("project Source = %+v", got)
	}

	opts.File = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: acme}\nspec:\n" +
		"  projectSource: {url: 'github.com/acme/other', branch: trunk}\n")
	opts.Source, opts.Force, opts.DryRun = address, true, true
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !hasNote(out.Plan.Notes, "names github.com/acme/other") || !hasNote(out.Plan.Notes, "(--source)") {
		t.Fatalf("notes = %v", out.Plan.Notes)
	}

	opts.Source, opts.File = "", []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: acme}\n")
	if _, err = Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "spec.projectSource.url") {
		t.Fatalf("err = %v", err)
	}
}

// TestAddIdentity: --identity sets the git identity of the project's Source
// the add creates; a stored Source keeps its own.
func TestAddIdentity(t *testing.T) {
	opts, _ := setupAdd(t)
	f := opts.Client.(*fakeClient)
	opts.Identity = &config.SecretRef{Name: "git-ssh"}
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	got := storedSource(t, f, opts.Name)
	if got.Git == nil || *got.Git.IdentityRef != (config.SecretRef{Name: "git-ssh", Realm: "default"}) {
		t.Fatalf("project Source = %+v", got)
	}
	if staged := f.staged[0]; staged.IdentityRef == nil || staged.IdentityRef.Name != "git-ssh" {
		t.Fatalf("the project was not fetched with the identity: %+v", staged)
	}
	if project := storedTeam(t, f, opts.Name).Spec.ProjectSource; project.Git != nil {
		t.Fatalf("the Team carries the identity: %+v", project)
	}
	opts.Force = true
	if _, err := Add(t.Context(), opts); err == nil ||
		!strings.Contains(err.Error(), "kuke create source acme --url ADDRESS --identity ID --force") {
		t.Fatalf("err = %v", err)
	}
}
