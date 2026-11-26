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

// setupUpgrade stores the state --upgrade starts from, in the shape kukeond
// stores since #1643: the kuke team (when withTeam) refers to the kukeon
// Source, stored at sourcePin (nil: not stored), and the TeamDefaults lists
// no sources. It returns --upgrade options pinned to commit B.
func setupUpgrade(t *testing.T, withTeam bool, sourcePin *config.Source) (InitOptions, *fakeClient) {
	t.Helper()
	opts, f := setupInit(t)
	if sourcePin != nil {
		f.storeSource(config.KukeonSourceName, specOf(*sourcePin))
	}
	storeLegacyDefaults(t, f, InitialDefaults(config.DefaultRealm))
	if withTeam {
		storeLegacyTeam(t, f, config.Majordomo())
		// An install of this release stores the default team beside it
		// (issue #1692).
		storeTeam(t, f, config.DefaultTeam())
	}
	return upgradeTo(opts), f
}

// setupLegacyUpgrade stores the state a kukeon release before #1643 left:
// the kuke team's kukeon entry at teamPin and the TeamDefaults' kukeon
// source at defaultsPin (nil: not stored), and no Source.
func setupLegacyUpgrade(t *testing.T, teamPin, defaultsPin *config.Source) (InitOptions, *fakeClient) {
	t.Helper()
	opts, f := setupInit(t)
	if teamPin != nil {
		team := config.Majordomo()
		clonePath := team.Spec.Sources[0].ClonePath
		team.Spec.Sources[0] = *teamPin
		team.Spec.Sources[0].ClonePath = clonePath
		storeLegacyTeam(t, f, team)
	}
	if defaultsPin != nil {
		defaults := InitialDefaults(config.DefaultRealm)
		defaults.Spec.Defaults.Sources = []config.Source{*defaultsPin}
		storeLegacyDefaults(t, f, defaults)
	}
	return upgradeTo(opts), f
}

func upgradeTo(opts InitOptions) InitOptions {
	pin := kukeonAt(commitB, "")
	opts.Upgrade = true
	opts.Pin = &pin
	return opts
}

func hasNote(notes []string, sub string) bool {
	return slices.ContainsFunc(notes, func(n string) bool { return strings.Contains(n, sub) })
}

// TestUpgradeRepinsTheKukeonSource: --upgrade moves the stored kukeon Source
// to the build's revision and recomposes the kuke team in the same apply;
// the kuke team reads the Source, so the Team document keeps its shape.
func TestUpgradeRepinsTheKukeonSource(t *testing.T) {
	a := kukeonAt(commitA, "")
	opts, f := setupUpgrade(t, true, &a)
	// The opencode harness consumes the binding, so composition keeps it.
	editTeam(t, f, "kuke", func(team *config.Team) {
		team.Spec.Harnesses = append(team.Spec.Harnesses,
			config.Harness{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"})
		team.Spec.Secrets = []config.SecretBinding{binding(nil)}
	})
	before := storedTeam(t, f, "kuke")
	defaults := slices.Clone(f.defaults)

	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) {
		t.Fatalf("writes = %v, want the kuke team, then the default team", f.writes)
	}
	kinds, _ := appliedDocs(t, f.byTeam["kuke"])
	if kinds[0] != "Source" || kinds[len(kinds)-1] != "Team" || slices.Contains(kinds, "TeamDefaults") {
		t.Fatalf("apply stream kinds = %v", kinds)
	}
	if got := storedSource(t, f, config.KukeonSourceName); got.Commit != commitB || got.Tag != "" || got.Branch != "" {
		t.Fatalf("kukeon Source = %+v", got)
	}
	after := storedTeam(t, f, "kuke")
	if after.Spec.Sources[0] != before.Spec.Sources[0] {
		t.Fatalf("team kukeon entry = %+v, want it unchanged", after.Spec.Sources[0])
	}
	if !slices.Equal(after.Spec.Secrets, before.Spec.Secrets) || len(after.Spec.Secrets) != 1 {
		t.Fatalf("spec.secrets = %+v, want %+v", after.Spec.Secrets, before.Spec.Secrets)
	}
	if !slices.EqualFunc(after.Spec.Roles, before.Spec.Roles, func(x, y config.Role) bool {
		return x.Name == y.Name && x.Path == y.Path && x.InitialPath == y.InitialPath
	}) || !slices.Equal(after.Spec.Harnesses, before.Spec.Harnesses) {
		t.Fatalf("roles or harnesses changed: %+v", after.Spec)
	}
	if !bytes.Equal(defaults, f.defaults) {
		t.Fatal("the TeamDefaults changed")
	}
	if !hasNote(out.Result.Notes, "Source kukeon: commit "+commitA+" -> commit "+commitB) {
		t.Fatalf("notes = %v", out.Result.Notes)
	}
	if out.Plan.Command != "kuke team init --upgrade" || out.Plan.Origin.Kind != OriginStored {
		t.Fatalf("plan = %+v", out.Plan)
	}
}

// TestUpgradeConvertsLegacyState is AC13 for --upgrade: the TeamDefaults'
// sources become Sources, the kuke team's kukeon entry becomes a reference,
// and the kukeon Source is stored at the build's revision, in one apply.
func TestUpgradeConvertsLegacyState(t *testing.T) {
	a := kukeonAt(commitA, "")
	opts, f := setupLegacyUpgrade(t, &a, &a)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) {
		t.Fatalf("writes = %v, want the kuke team, then the default team", f.writes)
	}
	kinds, _ := appliedDocs(t, f.byTeam["kuke"])
	if kinds[0] != "TeamDefaults" || !slices.Contains(kinds, "Source") || kinds[len(kinds)-1] != "Team" {
		t.Fatalf("apply stream kinds = %v", kinds)
	}
	if got := storedDefaults(t, f).Spec.Defaults; got.Sources != nil ||
		!slices.Equal(got.Harnesses, config.MajordomoHarnesses()) {
		t.Fatalf("converted defaults = %+v", got)
	}
	if got := storedSource(t, f, config.KukeonSourceName); got.URL != config.KukeonSourceURL || got.Commit != commitB {
		t.Fatalf("kukeon Source = %+v", got)
	}
	want := config.Source{Name: config.KukeonSourceName, ClonePath: "/home/user/kukeon-src"}
	if got := storedTeam(t, f, "kuke").Spec.Sources[0]; got != want {
		t.Fatalf("team kukeon entry = %+v, want %+v", got, want)
	}
	for _, note := range []string{
		"converted 1 registered sources to Source documents",
		"kuke team: its kukeon entry now reads the stored kukeon Source",
		"Source kukeon: commit " + commitA + " -> commit " + commitB,
	} {
		if !hasNote(out.Result.Notes, note) {
			t.Fatalf("notes = %v, want %q", out.Result.Notes, note)
		}
	}
}

func TestUpgradeTagPinBecomesCommit(t *testing.T) {
	tag := kukeonAt("", "v0.10.0")
	opts, f := setupUpgrade(t, true, &tag)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := storedSource(t, f, config.KukeonSourceName); got.Tag != "" || got.Commit != commitB {
		t.Fatalf("kukeon Source = %+v", got)
	}
	if !hasNote(out.Result.Notes, "Source kukeon: tag v0.10.0 -> commit "+commitB) {
		t.Fatalf("notes = %v", out.Result.Notes)
	}
}

func TestUpgradeAlreadyAtPinChangesNothing(t *testing.T) {
	b := kukeonAt(commitB, "")
	opts, f := setupUpgrade(t, true, &b)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != 0 || out.Plan != nil || out.Result != nil {
		t.Fatalf("writes=%v plan=%v result=%v", f.writes, out.Plan, out.Result)
	}
	if want := []string{"Source kukeon already at commit " + commitB}; !slices.Equal(out.Notes, want) {
		t.Fatalf("notes = %v, want %v", out.Notes, want)
	}
}

func TestUpgradeNeverCreatesTheKukeTeam(t *testing.T) {
	t.Run("source behind", func(t *testing.T) {
		a := kukeonAt(commitA, "")
		opts, f := setupUpgrade(t, false, &a)
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, stored := f.teams["kuke"]; stored || f.applies != 0 {
			t.Fatalf("a Team was applied: writes=%v", f.writes)
		}
		if !slices.Equal(f.writes, []string{"apply"}) {
			t.Fatalf("writes = %v, want the Source write", f.writes)
		}
		if got := storedSource(t, f, config.KukeonSourceName); got.Commit != commitB {
			t.Fatalf("kukeon Source = %+v", got)
		}
		if !hasNote(out.Notes, "team kuke is not stored; run kuke team init to create it") {
			t.Fatalf("notes = %v", out.Notes)
		}
	})
	t.Run("legacy defaults behind", func(t *testing.T) {
		a := kukeonAt(commitA, "")
		opts, f := setupLegacyUpgrade(t, nil, &a)
		if _, err := Init(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		if _, stored := f.teams["kuke"]; stored || !slices.Equal(f.writes, []string{"apply"}) {
			t.Fatalf("writes = %v, want one plain apply", f.writes)
		}
		if got := storedSource(t, f, config.KukeonSourceName); got.Commit != commitB {
			t.Fatalf("kukeon Source = %+v", got)
		}
		if storedDefaults(t, f).Spec.Defaults.Sources != nil {
			t.Fatal("the legacy defaults were not converted")
		}
	})
	t.Run("nothing stored", func(t *testing.T) {
		opts, f := setupLegacyUpgrade(t, nil, nil)
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.writes) != 0 || f.defaults != nil || f.sources != nil {
			t.Fatalf("writes = %v", f.writes)
		}
		if want := []string{
			"Source kukeon already at commit " + commitB, "team kuke is not stored; run kuke team init to create it",
		}; !slices.Equal(out.Notes, want) {
			t.Fatalf("notes = %v", out.Notes)
		}
	})
}

func TestUpgradeLeavesAForeignKukeonSource(t *testing.T) {
	foreign := config.Source{Name: config.KukeonSourceName, URL: "github.com/someone/kukeon-fork", Commit: commitA}
	opts, f := setupUpgrade(t, true, &foreign)
	original := slices.Clone(f.sources[config.KukeonSourceName])
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, f.sources[config.KukeonSourceName]) {
		t.Fatal("the foreign kukeon Source was changed")
	}
	if len(f.writes) != 0 || !hasNote(out.Notes, "github.com/someone/kukeon-fork") {
		t.Fatalf("writes = %v notes = %v", f.writes, out.Notes)
	}
}

func TestUpgradeNamesInheritingTeamsWithoutRecomposingThem(t *testing.T) {
	a := kukeonAt(commitA, "")
	opts, f := setupUpgrade(t, true, &a)
	project := inheritingTeam("project")
	storeTeam(t, f, project)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) {
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
		opts, f := setupUpgrade(t, true, &a)
		opts.Force, opts.Reapply = mode == "force", mode == "reapply"
		if _, err := Init(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "--upgrade") {
			t.Fatalf("%s: err = %v", mode, err)
		}
		if len(f.writes) != 0 {
			t.Fatalf("%s: writes = %v", mode, f.writes)
		}
	}
}
