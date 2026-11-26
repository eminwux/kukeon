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
	"bytes"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// setupReapply initializes the kuke team from the role as shipped (host
// mounts and the suspended health sweep included) and returns options for a
// following --reapply against the same fake daemon.
func setupReapply(t *testing.T) (InitOptions, *fakeClient) {
	t.Helper()
	opts, fake := setupInit(t)
	opts.Sources = map[string]fs.FS{"kukeon": os.DirFS("../../..")}
	if _, err := Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	kuke := fake.byTeam["kuke"]
	if !bytes.Contains(kuke, []byte("name: kuke-majordomo-codex-health-sweep")) ||
		!bytes.Contains(kuke, []byte("suspend: true")) {
		t.Fatalf("init must render the sweep suspended:\n%s", kuke)
	}
	opts.Reapply = true
	return opts, fake
}

func TestReapplyEnablesTheSweepFromTheStoredTeam(t *testing.T) {
	opts, fake := setupReapply(t)
	defaults := slices.Clone(fake.defaults)
	on := false
	editTeam(t, fake, "kuke", func(team *config.Team) {
		team.Spec.Roles[0].Tasks = &[]config.TaskSelection{{Name: "health-sweep", Suspend: &on}}
	})
	applies := fake.applies

	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	// --reapply recomposes the kuke team, then the default team.
	if fake.applies != applies+2 || fake.team != "default" || out.Plan.Origin.Kind != OriginStored ||
		out.Plan.Command != "kuke team init --reapply" || len(out.Also) != 1 || out.Also[0].Result == nil ||
		out.Also[0].Plan.Origin.Kind != OriginStored || out.Also[0].Result.Team != "default" {
		t.Fatalf("applies=%d team=%q plan=%+v also=%+v", fake.applies, fake.team, out.Plan, out.Also)
	}
	kuke := fake.byTeam["kuke"]
	if !bytes.Contains(kuke, []byte("cron: '@hourly'")) && !bytes.Contains(kuke, []byte("cron: \"@hourly\"")) {
		t.Fatalf("sweep schedule missing:\n%s", kuke)
	}
	if bytes.Contains(kuke, []byte("suspend: true")) {
		t.Fatal("sweep is still suspended after the stored team switched it on")
	}
	if !bytes.Equal(defaults, fake.defaults) {
		t.Fatal("the TeamDefaults must be untouched by --reapply")
	}
	if team := storedTeam(t, fake, "kuke"); team.Spec.Roles[0].Tasks == nil ||
		(*team.Spec.Roles[0].Tasks)[0].Suspend == nil || *(*team.Spec.Roles[0].Tasks)[0].Suspend {
		t.Fatal("the stored team lost the edit")
	}
}

// TestReapplyIsApplyStoredKuke is AC12: kuke team apply kuke equals kuke team
// init --reapply.
func TestReapplyIsApplyStoredKuke(t *testing.T) {
	opts, fake := setupReapply(t)
	if _, err := Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	reapplied := slices.Clone(fake.byTeam["kuke"])
	if _, err := ApplyStored(t.Context(), opts.Options, "kuke", ""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reapplied, fake.raw) || fake.team != "kuke" {
		t.Fatal("kuke team apply kuke and kuke team init --reapply differ")
	}
}

// A config the new composition no longer renders is absent from the apply
// set; kukeond prunes every kuke-labelled config the set does not include.
func TestReapplyDropsObsoleteConfigsFromTheApplySet(t *testing.T) {
	opts, fake := setupReapply(t)
	editTeam(t, fake, "kuke", func(team *config.Team) {
		team.Spec.Roles[0].Tasks = &[]config.TaskSelection{}
		team.Spec.Harnesses = team.Spec.Harnesses[:1]
	})
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	kuke := fake.byTeam["kuke"]
	if kuke == nil {
		t.Fatal("apply was not scoped to the kuke team (prune key)")
	}
	for _, gone := range []string{"health-sweep", "kuke-majordomo-claude"} {
		if bytes.Contains(kuke, []byte(gone)) {
			t.Errorf("%s is still in the apply set", gone)
		}
	}
	if !bytes.Contains(kuke, []byte("name: kuke-majordomo-codex")) {
		t.Fatal("the remaining config is missing")
	}
	// The plan names what the daemon prunes.
	for _, gone := range []string{"kuke-majordomo-claude", "kuke-majordomo-codex-health-sweep"} {
		if !slices.Contains(out.Plan.Prune, gone) {
			t.Errorf("plan prune = %v, want %s", out.Plan.Prune, gone)
		}
	}
}

func TestReapplyRefusals(t *testing.T) {
	t.Run("no stored team", func(t *testing.T) {
		opts, fake := setupInit(t)
		opts.Reapply = true
		_, err := Init(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "kuke team init") || len(fake.writes) != 0 {
			t.Fatalf("err = %v writes=%v", err, fake.writes)
		}
		if _, err = ApplyStored(t.Context(), opts.Options, "other", ""); !errors.Is(err, errdefs.ErrTeamNotFound) ||
			!strings.Contains(err.Error(), "kuke create team other") {
			t.Fatalf("apply of an unstored team: %v", err)
		}
	})
	t.Run("with force", func(t *testing.T) {
		opts, fake := setupReapply(t)
		opts.Force = true
		applies := fake.applies
		if _, err := Init(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "mutually exclusive") ||
			fake.applies != applies {
			t.Fatalf("err = %v applies=%d", err, fake.applies-applies)
		}
	})
	t.Run("invalid stored team", func(t *testing.T) {
		for name, mutate := range map[string]func(string) string{
			"unknown field": func(s string) string {
				return strings.Replace(s, "spec:\n", "spec:\n    sweepEnabled: true\n", 1)
			},
			"misspelled task key": func(s string) string {
				return strings.Replace(s, "initialPath:", "tasks: [{name: health-sweep, suspended: false}]\n          initialPath:", 1)
			},
			"unknown task": func(s string) string {
				return strings.Replace(s, "initialPath:", "tasks: [nightly-sweep]\n          initialPath:", 1)
			},
		} {
			opts, fake := setupReapply(t)
			raw := string(fake.teams["kuke"])
			edited := mutate(raw)
			if edited == raw {
				t.Fatalf("%s: fixture edit did not apply:\n%s", name, raw)
			}
			fake.teams["kuke"] = []byte(edited)
			applies := fake.applies
			if _, err := Init(t.Context(), opts); err == nil {
				t.Errorf("%s: accepted", name)
			}
			if fake.applies != applies {
				t.Errorf("%s: something was applied", name)
			}
			if string(fake.teams["kuke"]) != edited {
				t.Errorf("%s: the stored team was rewritten", name)
			}
		}
	})
	t.Run("failed apply keeps the stored team", func(t *testing.T) {
		opts, fake := setupReapply(t)
		on := false
		editTeam(t, fake, "kuke", func(team *config.Team) {
			team.Spec.Roles[0].Tasks = &[]config.TaskSelection{{Name: "health-sweep", Suspend: &on}}
		})
		before := slices.Clone(fake.teams["kuke"])
		fake.applyErr = errors.New("daemon refused")
		if _, err := Init(t.Context(), opts); err == nil {
			t.Fatal("apply failure not reported")
		}
		if !bytes.Equal(before, fake.teams["kuke"]) {
			t.Fatal("the stored team was rewritten after a failed apply")
		}
	})
}
