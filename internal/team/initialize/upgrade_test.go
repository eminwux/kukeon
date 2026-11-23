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

package initialize

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/config"
)

var (
	commitA = strings.Repeat("a", 40)
	commitB = strings.Repeat("b", 40)
)

func kukeonAt(commit, tag string) config.Source {
	return config.Source{Name: config.KukeonSourceName, URL: config.KukeonSourceURL, Commit: commit, Tag: tag}
}

// setupUpgrade stores a kuke team and a TeamDefaults whose kukeon sources are
// at teamPin and defaultsPin (nil: not stored) and returns --upgrade options
// pinned to commit B.
func setupUpgrade(t *testing.T, teamPin, defaultsPin *config.Source) (InitOptions, *fakeClient) {
	t.Helper()
	opts, f := setupInit(t)
	if teamPin != nil {
		team := config.Majordomo()
		clonePath := team.Spec.Sources[0].ClonePath
		team.Spec.Sources[0] = *teamPin
		team.Spec.Sources[0].ClonePath = clonePath
		storeTeam(t, f, team)
	}
	if defaultsPin != nil {
		defaults := InitialDefaults(config.DefaultRealm)
		defaults.Spec.Defaults.Sources[0] = *defaultsPin
		storeDefaults(t, f, defaults)
	}
	pin := kukeonAt(commitB, "")
	opts.Upgrade = true
	opts.Pin = &pin
	return opts, f
}

func hasNote(notes []string, sub string) bool {
	return slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, sub) })
}

func TestUpgradeRepinsTeamAndDefaultsInOneApply(t *testing.T) {
	a := kukeonAt(commitA, "")
	opts, f := setupUpgrade(t, &a, &a)
	// The opencode harness consumes the binding, so composition keeps it.
	editTeam(t, f, "kuke", func(team *config.Team) {
		team.Spec.Harnesses = append(team.Spec.Harnesses,
			config.Harness{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"})
		team.Spec.Secrets = []config.SecretBinding{binding(nil)}
	})
	before := storedTeam(t, f, "kuke")

	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke"}) {
		t.Fatalf("writes = %v, want one team apply", f.writes)
	}
	kinds, _ := appliedDocs(t, f.raw)
	if kinds[0] != "TeamDefaults" || kinds[len(kinds)-1] != "Team" {
		t.Fatalf("apply stream kinds = %v", kinds)
	}
	after := storedTeam(t, f, "kuke")
	if got := after.Spec.Sources[0]; got.Commit != commitB || got.Tag != "" || got.Branch != "" ||
		got.ClonePath != before.Spec.Sources[0].ClonePath {
		t.Fatalf("team kukeon source = %+v", got)
	}
	if !slices.Equal(after.Spec.Secrets, before.Spec.Secrets) || len(after.Spec.Secrets) != 1 {
		t.Fatalf("spec.secrets = %+v, want %+v", after.Spec.Secrets, before.Spec.Secrets)
	}
	if !slices.EqualFunc(after.Spec.Roles, before.Spec.Roles, func(x, y config.Role) bool {
		return x.Name == y.Name && x.Path == y.Path && x.InitialPath == y.InitialPath
	}) || !slices.Equal(after.Spec.Harnesses, before.Spec.Harnesses) {
		t.Fatalf("roles or harnesses changed: %+v", after.Spec)
	}
	if got := storedDefaults(t, f).Spec.Defaults.Sources[0]; got.Commit != commitB {
		t.Fatalf("defaults kukeon source = %+v", got)
	}
	notes := out.Result.Notes
	if !hasNote(notes, "kuke team: kukeon source commit "+commitA+" -> commit "+commitB) ||
		!hasNote(notes, "team defaults: kukeon source commit "+commitA+" -> commit "+commitB) {
		t.Fatalf("notes = %v", notes)
	}
	if out.Plan.Command != "kuke team init --upgrade" || out.Plan.Origin.Kind != OriginStored {
		t.Fatalf("plan = %+v", out.Plan)
	}
}

func TestUpgradeTagPinBecomesCommit(t *testing.T) {
	tag := kukeonAt("", "v0.10.0")
	b := kukeonAt(commitB, "")
	opts, f := setupUpgrade(t, &tag, &b)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := storedTeam(t, f, "kuke").Spec.Sources[0]; got.Tag != "" || got.Commit != commitB {
		t.Fatalf("team kukeon source = %+v", got)
	}
	if kinds, _ := appliedDocs(t, f.raw); slices.Contains(kinds, "TeamDefaults") {
		t.Fatal("unchanged TeamDefaults re-sent")
	}
	if !hasNote(out.Result.Notes, "kuke team: kukeon source tag v0.10.0 -> commit "+commitB) {
		t.Fatalf("notes = %v", out.Result.Notes)
	}
}

func TestUpgradeAlreadyAtPinChangesNothing(t *testing.T) {
	b := kukeonAt(commitB, "")
	opts, f := setupUpgrade(t, &b, &b)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 || out.Plan != nil || out.Result != nil {
		t.Fatalf("writes=%v plan=%v result=%v", f.writes, out.Plan, out.Result)
	}
	if want := []string{"kuke team and defaults already at commit " + commitB}; !slices.Equal(out.Notes, want) {
		t.Fatalf("notes = %v, want %v", out.Notes, want)
	}
}

func TestUpgradeNeverCreatesTheKukeTeam(t *testing.T) {
	t.Run("defaults behind", func(t *testing.T) {
		a := kukeonAt(commitA, "")
		opts, f := setupUpgrade(t, nil, &a)
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, stored := f.teams["kuke"]; stored || f.applies != 0 {
			t.Fatalf("a Team was applied: writes=%v", f.writes)
		}
		if !slices.Equal(f.writes, []string{"apply"}) {
			t.Fatalf("writes = %v, want the TeamDefaults write", f.writes)
		}
		if got := storedDefaults(t, f).Spec.Defaults.Sources[0]; got.Commit != commitB {
			t.Fatalf("defaults kukeon source = %+v", got)
		}
		if !hasNote(out.Notes, "team kuke is not stored; run kuke team init to create it") {
			t.Fatalf("notes = %v", out.Notes)
		}
	})
	t.Run("no defaults", func(t *testing.T) {
		opts, f := setupUpgrade(t, nil, nil)
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.writes) != 0 || f.defaults != nil {
			t.Fatalf("writes = %v", f.writes)
		}
		if want := []string{"team kuke is not stored; run kuke team init to create it"}; !slices.Equal(out.Notes, want) {
			t.Fatalf("notes = %v", out.Notes)
		}
	})
}

func TestUpgradeLeavesAForeignKukeonSource(t *testing.T) {
	a := kukeonAt(commitA, "")
	foreign := config.Source{Name: config.KukeonSourceName, URL: "github.com/someone/kukeon-fork", Commit: commitA}
	opts, f := setupUpgrade(t, &a, &foreign)
	original := slices.Clone(f.defaults)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, f.defaults) {
		t.Fatal("foreign defaults source was changed")
	}
	if kinds, _ := appliedDocs(t, f.raw); slices.Contains(kinds, "TeamDefaults") {
		t.Fatal("unchanged TeamDefaults re-sent")
	}
	if got := storedTeam(t, f, "kuke").Spec.Sources[0]; got.Commit != commitB {
		t.Fatalf("team kukeon source = %+v", got)
	}
	if !hasNote(out.Result.Notes, "github.com/someone/kukeon-fork") {
		t.Fatalf("notes = %v", out.Result.Notes)
	}
}

func TestUpgradeNamesInheritingTeamsWithoutRecomposingThem(t *testing.T) {
	a := kukeonAt(commitA, "")
	opts, f := setupUpgrade(t, &a, &a)
	project := inheritingTeam("project")
	storeTeam(t, f, project)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke"}) {
		t.Fatalf("writes = %v, the inheriting team must not be applied", f.writes)
	}
	if bytes.Contains(f.raw, []byte("project-")) {
		t.Fatal("the inheriting team's configs were rendered")
	}
	if !hasNote(out.Result.Notes, "run kuke team apply project") {
		t.Fatalf("notes = %v", out.Result.Notes)
	}
	if hasNote(out.Result.Notes, "kuke team apply kuke") {
		t.Fatalf("kuke is recomposed by --upgrade itself: %v", out.Result.Notes)
	}
}

func TestUpgradeExcludesForceAndReapply(t *testing.T) {
	for _, mode := range []string{"force", "reapply"} {
		a := kukeonAt(commitA, "")
		opts, f := setupUpgrade(t, &a, &a)
		opts.Force, opts.Reapply = mode == "force", mode == "reapply"
		if _, err := Init(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "--upgrade") {
			t.Fatalf("%s: err = %v", mode, err)
		}
		if len(f.writes) != 0 {
			t.Fatalf("%s: writes = %v", mode, f.writes)
		}
	}
}
