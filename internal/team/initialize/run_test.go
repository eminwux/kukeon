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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// setupInit is init against an empty fake daemon with the plain definitions.
func setupInit(t *testing.T) (InitOptions, *fakeClient) {
	t.Helper()
	f := newFake()
	return InitOptions{Options: Options{Client: f, Sources: sourceFS()}}, f
}

// TestInitAppliesTheKukeTeamAndCreatesDefaults is AC7: init applies the kuke
// Team in one team apply, stores it, and gives a realm without TeamDefaults
// one with the kukeon source and its claude and codex harnesses. It reads
// nothing of the host's git configuration.
func TestInitAppliesTheKukeTeamAndCreatesDefaults(t *testing.T) {
	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gitconfig, []byte("[user]\n\tname = Dev\n\temail = dev@example.com\n"+
		"\tsigningkey = ~/.ssh/id_ed25519.pub\n[commit]\n\tgpgsign = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	opts, f := setupInit(t)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	// Two blueprints, two configs, the TeamDefaults, the kukeon Source and
	// the Team.
	if out.Result == nil || out.Result.Resources != 7 || out.Result.Team != "kuke" {
		t.Fatalf("result = %+v", out.Result)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke"}) {
		t.Fatalf("writes = %v, want one team apply", f.writes)
	}
	kinds, _ := appliedDocs(t, f.raw)
	if kinds[0] != "TeamDefaults" || kinds[len(kinds)-1] != "Team" {
		t.Fatalf("apply stream kinds = %v", kinds)
	}
	team := storedTeam(t, f, "kuke")
	if len(team.Spec.Roles) != 1 || team.Spec.Roles[0].Name != "majordomo" || len(team.Spec.Sources) != 1 ||
		len(team.Spec.Harnesses) != 2 || team.Metadata.Realm != "default" {
		t.Fatalf("stored kuke team = %+v", team.Spec)
	}
	defaults := storedDefaults(t, f)
	d := defaults.Spec.Defaults
	if d.Sources != nil || d.Roles != nil || !slices.Equal(d.Harnesses, config.MajordomoHarnesses()) {
		t.Fatalf("initial defaults = %+v", d)
	}
	// The kukeon Source is stored beside them, at the build's revision
	// (#1643).
	if got := storedSource(t, f, config.KukeonSourceName); !reflect.DeepEqual(got, config.KukeonSourceSpec()) {
		t.Fatalf("kukeon Source = %+v, want %+v", got, config.KukeonSourceSpec())
	}
	if team.Spec.Sources[0] != (config.Source{Name: config.KukeonSourceName, ClonePath: "/home/user/kukeon-src"}) {
		t.Fatalf("kuke team kukeon entry = %+v, want a reference", team.Spec.Sources[0])
	}
	for _, note := range out.Result.Notes {
		if strings.Contains(strings.ToLower(note), "git") {
			t.Fatalf("init printed a git note: %q", note)
		}
	}
	if f.creates != 0 || f.stackDefaults != nil {
		t.Fatalf("init stored a secret or stack git defaults: creates=%d stack=%+v", f.creates, f.stackDefaults)
	}
}

// TestInitAlreadyInitialized: "already initialized" is the stored kuke Team.
func TestInitAlreadyInitialized(t *testing.T) {
	opts, f := setupInit(t)
	if _, err := Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	before, writes := f.teams["kuke"], len(f.writes)
	_, err := Init(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "already initialized") ||
		!strings.Contains(err.Error(), "--force") || !strings.Contains(err.Error(), "--reapply") {
		t.Fatalf("expected already-initialized guidance, got %v", err)
	}
	if !bytes.Equal(before, f.teams["kuke"]) || len(f.writes) != writes {
		t.Fatal("existing team or daemon changed")
	}
}

// TestInitForceResetsTheTeamAndNeverTheDefaults: --force replaces the stored
// kuke Team with the initial declaration; an existing TeamDefaults is never
// changed or re-sent (AC7).
func TestInitForceResetsTheTeamAndNeverTheDefaults(t *testing.T) {
	opts, f := setupInit(t)
	custom := config.Majordomo()
	custom.Spec.Roles = []config.Role{
		{Name: "custom", Source: "kukeon", Path: "team/roles/majordomo", Harness: "codex", InitialPath: "/opt/custom"},
	}
	storeTeam(t, f, custom)
	defaults := config.EmptyDefaults(config.DefaultRealm)
	defaults.Spec.Defaults.Sources = []config.Source{config.MajordomoSource()}
	defaults.Spec.Defaults.Harnesses = config.MajordomoHarnesses()[:1]
	storeDefaults(t, f, defaults)
	original := slices.Clone(f.defaults)
	opts.Force = true
	for range 2 {
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if out.Result.Resources != 5 {
			t.Fatalf("resources = %d, want 2 blueprints, 2 configs and the Team", out.Result.Resources)
		}
		if kinds, _ := appliedDocs(t, f.raw); slices.Contains(kinds, "TeamDefaults") {
			t.Fatal("init re-sent an existing TeamDefaults")
		}
		if bytes.Contains(f.raw, []byte("kuke-custom-codex")) {
			t.Fatal("authored majordomo selection retained")
		}
		if team := storedTeam(t, f, "kuke"); team.Spec.Roles[0].Name != "majordomo" {
			t.Fatal("stored team was not reset")
		}
		if !bytes.Equal(original, f.defaults) {
			t.Fatal("init changed the TeamDefaults")
		}
	}
	if f.applies != 2 || f.team != "kuke" || len(f.plain) != 0 {
		t.Fatalf("applies=%d team=%q plain=%d", f.applies, f.team, len(f.plain))
	}
}

// TestInitForceReplacesAnUndecodableTeam: a stored kuke Team that no longer
// decodes blocks a plain init but not a forced one.
func TestInitForceReplacesAnUndecodableTeam(t *testing.T) {
	opts, f := setupInit(t)
	f.teams["kuke"] = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: kuke}\nspec: {unknown: true}\n")
	if _, err := Init(t.Context(), opts); err == nil || f.applies != 0 {
		t.Fatalf("undecodable stored team accepted: %v", err)
	}
	opts.Force = true
	if _, err := Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if team := storedTeam(t, f, "kuke"); team.Spec.Roles[0].Name != "majordomo" {
		t.Fatal("the undecodable team was not replaced")
	}
}

// TestInitRejectsStoredDefaultsWithSpecGit is D7: a TeamDefaults that still
// carries spec.git fails before anything reaches the daemon.
func TestInitRejectsStoredDefaultsWithSpecGit(t *testing.T) {
	for _, reapplying := range []bool{false, true} {
		opts, f := setupInit(t)
		if reapplying {
			if _, err := Init(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			opts.Reapply = true
		}
		f.defaults = []byte("apiVersion: v1beta1\nkind: TeamDefaults\nmetadata: {name: default, realm: default}\n" +
			"spec:\n  git:\n    author: {name: Dev, email: dev@example.com}\n")
		applies := f.applies
		_, err := Init(t.Context(), opts)
		if err == nil || !strings.Contains(err.Error(), "kuke connector import git") || f.applies != applies {
			t.Fatalf("reapply=%v: err=%v applies=%d", reapplying, err, f.applies-applies)
		}
	}
}

// TestInitFailuresStoreNothing is AC4 through init: a failed apply leaves the
// stored Team as it was, or absent.
func TestInitFailuresStoreNothing(t *testing.T) {
	for _, existing := range []bool{false, true} {
		for _, mode := range []string{"rpc", "total", "partial", "preflight"} {
			t.Run(fmt.Sprintf("existing-%t/%s", existing, mode), func(t *testing.T) {
				opts, f := setupInit(t)
				opts.Force = true
				if existing {
					if _, err := Init(t.Context(), opts); err != nil {
						t.Fatal(err)
					}
				}
				before, had := f.teams["kuke"]
				switch mode {
				case "rpc":
					f.applyErr = io.EOF
				case "total":
					f.resources = []kukeonv1.ApplyResourceResult{
						{Action: "failed", Error: "bad blueprint"},
						{Action: "failed", Error: "bad config"},
					}
				case "partial":
					f.resources = []kukeonv1.ApplyResourceResult{
						{Action: "updated"},
						{Action: "failed", Error: "bad config"},
					}
				case "preflight":
					f.missingRealm = true
				}
				out, err := Init(t.Context(), opts)
				if err == nil || out.Result != nil {
					t.Fatalf("claimed success: %+v, %v", out.Result, err)
				}
				after, has := f.teams["kuke"]
				if had != has || !bytes.Equal(before, after) {
					t.Fatal("the stored team changed on a failed init")
				}
			})
		}
	}
}

// TestInitDryRunWritesNothing is AC10 through init.
func TestInitDryRunWritesNothing(t *testing.T) {
	opts, f := setupInit(t)
	opts.DryRun = true
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != nil || len(f.writes) != 0 || f.defaults != nil || len(f.teams) != 0 {
		t.Fatalf("dry run wrote: result=%+v writes=%v", out.Result, f.writes)
	}
	p := out.Plan
	if p.Origin.Kind != OriginInitial || len(p.Pairs) != 2 ||
		!slices.Equal(p.Create, []string{"kuke-majordomo-claude", "kuke-majordomo-codex"}) ||
		len(p.Rerender) != 0 || len(p.Prune) != 0 {
		t.Fatalf("plan = %+v", p)
	}
}
