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
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/teamtest"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
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
	if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) {
		t.Fatalf("writes = %v, want the kuke team's apply, then the default team's", f.writes)
	}
	kinds, _ := appliedDocs(t, f.byTeam["kuke"])
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
	if d.Sources != nil || d.Roles != nil || !slices.Equal(d.Harnesses, config.InitialHarnesses()) {
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

// TestInitAppliesTheDefaultTeam (issue #1692): after the kuke team, init
// applies the system default team in its own team apply: the agent role on
// claude and codex, as the two agent templates.
func TestInitAppliesTheDefaultTeam(t *testing.T) {
	opts, f := setupInit(t)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Also) != 1 || out.Also[0].Result == nil || out.Also[0].Result.Team != "default" {
		t.Fatalf("also = %+v", out.Also)
	}
	team := storedTeam(t, f, "default")
	if !config.IsSystemDefaultTeam(team) || len(team.Spec.Roles) != 1 || team.Spec.Roles[0].Name != "agent" ||
		team.Spec.ProjectSource != nil {
		t.Fatalf("stored default team = %+v", team.Spec)
	}
	_, names := appliedDocs(t, f.byTeam["default"])
	if !slices.Equal(names["CellConfig"], []string{"default-agent-codex", "default-agent-claude"}) {
		t.Fatalf("default team configs = %v", names["CellConfig"])
	}
	templates := 0
	for _, cfg := range f.configs {
		if !strings.HasPrefix(cfg.Metadata.Name, "default-agent-") {
			if _, ok := cfg.Metadata.Labels[api.LabelTemplate]; ok {
				t.Fatalf("%s is stamped as a template", cfg.Metadata.Name)
			}
			continue
		}
		templates++
		if cfg.Metadata.Labels[api.LabelTemplate] != "true" || cfg.Metadata.Labels[api.LabelTeam] != "default" ||
			cfg.Metadata.Labels[api.LabelRole] != "agent" {
			t.Fatalf("%s labels = %v", cfg.Metadata.Name, cfg.Metadata.Labels)
		}
	}
	if templates != 2 {
		t.Fatalf("stored %d agent templates, want 2", templates)
	}
}

// TestInitLeavesAUserDefaultTeam (issue #1692): a team named default the
// user added before default became a system team is left untouched, with how
// to rename it, and the kuke team is still applied; with --force too.
func TestInitLeavesAUserDefaultTeam(t *testing.T) {
	for _, force := range []bool{false, true} {
		opts, f := setupInit(t)
		mine := inheritingTeam("default")
		storeTeam(t, f, mine)
		before := slices.Clone(f.teams["default"])
		opts.Force = force
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(f.writes, []string{"apply-team kuke"}) || storedTeam(t, f, "kuke") == nil {
			t.Fatalf("force=%v writes = %v, want the kuke team only", force, f.writes)
		}
		if !bytes.Equal(before, f.teams["default"]) {
			t.Fatalf("force=%v: the user's default team was changed", force)
		}
		if len(out.Also) != 1 || !hasNote(out.Also[0].Notes, "kuke team export default") ||
			!hasNote(out.Also[0].Notes, "kuke delete team default") {
			t.Fatalf("force=%v also = %+v", force, out.Also)
		}
	}
}

// TestInitKeepsAStoredDefaultTeam: a plain init (kuke not stored yet) keeps a
// stored system default team; --force resets it to the initial declaration.
func TestInitKeepsAStoredDefaultTeam(t *testing.T) {
	opts, f := setupInit(t)
	edited := config.DefaultTeam()
	edited.Spec.Harnesses = config.MajordomoHarnesses()[:1]
	storeTeam(t, f, edited)
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.writes, []string{"apply-team kuke"}) || !hasNote(out.Also[0].Notes, "--reapply") {
		t.Fatalf("writes = %v also = %+v", f.writes, out.Also)
	}
	opts.Force = true
	if _, err = Init(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if team := storedTeam(t, f, "default"); len(team.Spec.Harnesses) != 2 {
		t.Fatalf("--force did not reset the default team: %+v", team.Spec.Harnesses)
	}
}

// TestInitReappliesAndUpgradesTheDefaultTeam: --reapply recomposes the
// stored default team after the kuke team; --reapply and --upgrade create it
// on an install that has only the kuke team.
func TestInitReappliesAndUpgradesTheDefaultTeam(t *testing.T) {
	t.Run("reapply recomposes the stored team", func(t *testing.T) {
		opts, f := setupInit(t)
		if _, err := Init(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		f.writes = nil
		opts.Reapply = true
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) ||
			out.Also[0].Plan.Origin.Kind != OriginStored {
			t.Fatalf("writes = %v also = %+v", f.writes, out.Also)
		}
	})
	for _, mode := range []string{"reapply", "upgrade"} {
		t.Run(mode+" creates a missing team", func(t *testing.T) {
			var opts InitOptions
			var f *fakeClient
			if mode == "upgrade" {
				a := kukeonAt(commitA, "")
				opts, f = setupUpgrade(t, true, &a)
				delete(f.teams, "default")
			} else {
				opts, f = setupInit(t)
				if _, err := Init(t.Context(), opts); err != nil {
					t.Fatal(err)
				}
				delete(f.teams, "default")
				opts.Reapply = true
			}
			f.writes = nil
			out, err := Init(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(f.writes, []string{"apply-team kuke", "apply-team default"}) ||
				out.Also[0].Plan.Origin.Kind != OriginInitial || !config.IsSystemDefaultTeam(storedTeam(t, f, "default")) {
				t.Fatalf("writes = %v also = %+v", f.writes, out.Also)
			}
		})
	}
	t.Run("upgrade at the pin creates a missing team", func(t *testing.T) {
		b := kukeonAt(commitB, "")
		opts, f := setupUpgrade(t, true, &b)
		delete(f.teams, "default")
		if _, err := Init(t.Context(), opts); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(f.writes, []string{"apply-team default"}) {
			t.Fatalf("writes = %v, want the default team only", f.writes)
		}
	})
	t.Run("upgrade without the kuke team applies neither", func(t *testing.T) {
		a := kukeonAt(commitA, "")
		opts, f := setupUpgrade(t, false, &a)
		out, err := Init(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		if slices.ContainsFunc(f.writes, func(w string) bool { return strings.HasPrefix(w, "apply-team") }) ||
			!hasNote(out.Also[0].Notes, "kuke team init applies both") {
			t.Fatalf("writes = %v also = %+v", f.writes, out.Also)
		}
	})
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
	// The web UI's Initialize answers this sentinel with a 409 (#1694).
	if !errors.Is(err, errdefs.ErrTeamInitialized) {
		t.Fatalf("err = %v, want it to wrap errdefs.ErrTeamInitialized", err)
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
		kukeKinds, _ := appliedDocs(t, f.byTeam["kuke"])
		defaultKinds, _ := appliedDocs(t, f.byTeam["default"])
		if slices.Contains(kukeKinds, "TeamDefaults") || slices.Contains(defaultKinds, "TeamDefaults") {
			t.Fatal("init re-sent an existing TeamDefaults")
		}
		if bytes.Contains(f.byTeam["kuke"], []byte("kuke-custom-codex")) {
			t.Fatal("authored majordomo selection retained")
		}
		if team := storedTeam(t, f, "kuke"); team.Spec.Roles[0].Name != "majordomo" {
			t.Fatal("stored team was not reset")
		}
		if !bytes.Equal(original, f.defaults) {
			t.Fatal("init changed the TeamDefaults")
		}
	}
	// Each forced init applies the kuke team, then the default team.
	if f.applies != 4 || f.byTeam["kuke"] == nil || f.team != "default" || len(f.plain) != 0 {
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
	if len(out.Also) != 1 || out.Also[0].Result != nil || out.Also[0].Plan == nil ||
		!slices.Equal(out.Also[0].Plan.Create, []string{"default-agent-claude", "default-agent-codex"}) {
		t.Fatalf("default team plan = %+v", out.Also)
	}
}

// programRoleTeam is a project team that omits harnesses and selects an
// agent role (claude and codex) and the pipeline program role (script only,
// unpinned), from the agents fixture tree.
const programRoleTeam = `apiVersion: v1beta1
kind: Team
metadata: { name: app }
spec:
  sources:
    - { name: agents, url: git@github.com:eminwux/agents, branch: main, clonePath: /home/user/agents }
  roles:
    - { name: dev, source: agents, path: dev }
    - { name: pipeline, source: agents, path: pipeline }
`

// addProgramRoleTeam composes programRoleTeam, dry-run, over defaults.
func addProgramRoleTeam(t *testing.T, defaults *config.TeamDefaults) (Outcome, error) {
	t.Helper()
	kukeon, err := teamtest.PlainSource("../../..")
	if err != nil {
		t.Fatal(err)
	}
	f := newFake()
	f.storeSource(config.KukeonSourceName, config.KukeonSourceSpec())
	storeDefaults(t, f, defaults)
	sources := map[string]fs.FS{"kukeon": kukeon, "agents": fixtureAgentsTree(), "project": fstest.MapFS{}}
	return Add(t.Context(), AddOptions{
		Options: Options{Client: f, Sources: sources, DryRun: true},
		Name:    "app", Source: "git@github.com:acme/app", Branch: "main",
		File: []byte(programRoleTeam), FileName: "app.yaml",
	})
}

// TestInitialDefaultsRunAProgramRole is #1728: a team inheriting the
// TeamDefaults init creates composes a script-only role's script pair, while
// its agent role renders only its codex and claude pairs. Over the old
// codex/claude seed the same team fails to compose.
func TestInitialDefaultsRunAProgramRole(t *testing.T) {
	out, err := addProgramRoleTeam(t, InitialDefaults(config.DefaultRealm))
	if err != nil {
		t.Fatal(err)
	}
	var pairs, configs []string
	for _, pair := range out.Plan.composed.Pairs {
		pairs = append(pairs, pair.Role+"/"+pair.Harness)
		configs = append(configs, pair.Config.Metadata.Name)
	}
	if want := []string{"dev/codex", "dev/claude", "pipeline/script"}; !slices.Equal(pairs, want) {
		t.Fatalf("pairs = %v, want %v", pairs, want)
	}
	if want := []string{"app-dev-codex", "app-dev-claude", "app-pipeline-script"}; !slices.Equal(configs, want) {
		t.Fatalf("configs = %v, want %v", configs, want)
	}

	legacy := config.EmptyDefaults(config.DefaultRealm)
	legacy.Spec.Defaults = config.Defaults{Harnesses: config.MajordomoHarnesses()}
	if _, err = addProgramRoleTeam(t, legacy); err == nil ||
		!strings.Contains(err.Error(), `role "pipeline" declares none of the team's harnesses`) {
		t.Fatalf("over codex/claude defaults: err = %v, want the pipeline role to pair with none", err)
	}
}
