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
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/errdefs"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// oldTeamHeader is the team apiVersion before v1beta1 (issue #1618, AC20),
// built here so the retired spelling appears nowhere in the tree.
const oldTeamHeader = "kuketeams" + ".io/v1"

// workerFile is a repository kuketeam.yaml that names one role.
const workerFile = "apiVersion: v1beta1\nkind: Team\nmetadata: {name: authored}\nspec:\n" +
	"  roles:\n    - {name: worker, source: kukeon, path: team/roles/majordomo, harness: codex}\n"

// setupProject is an add in NAME mode whose project tree is injected, so
// nothing is fetched: file, when set, is the repository's kuketeam.yaml.
func setupProject(t *testing.T, file string) (AddOptions, *fakeClient) {
	t.Helper()
	f := newFake()
	storeDefaults(t, f, addDefaults())
	sources := sourceFS()
	tree := fstest.MapFS{"README": {Data: []byte("project")}}
	if file != "" {
		tree["kuketeam.yaml"] = &fstest.MapFile{Data: []byte(file)}
	}
	sources["project"] = tree
	return AddOptions{
		Options: Options{Client: f, Sources: sources},
		Name:    "chosen", Source: "https://example.test/owner/project.git", Branch: "main",
	}, f
}

func roleNames(roles []config.Role) []string {
	var out []string
	for _, role := range roles {
		out = append(out, role.Name)
	}
	return out
}

// TestAddReadsTheRepositoryTeamFile is AC8: the repository's kuketeam.yaml
// is the declaration, NAME wins over its metadata.name, and without one the
// team inherits the defaults.
func TestAddReadsTheRepositoryTeamFile(t *testing.T) {
	opts, f := setupProject(t, workerFile)
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan.Origin.Kind != OriginRepository || !strings.HasPrefix(out.Plan.Origin.Detail, "kuketeam.yaml at") {
		t.Fatalf("origin = %+v", out.Plan.Origin)
	}
	team := storedTeam(t, f, "chosen")
	if f.team != "chosen" || !slices.Equal(roleNames(team.Spec.Roles), []string{"worker"}) ||
		team.Spec.Harnesses != nil || team.Spec.Sources != nil {
		t.Fatalf("stored team = %+v", team.Spec)
	}
	project := team.Spec.ProjectSource
	if project == nil || project.URL != opts.Source || project.Branch != "main" ||
		project.ClonePath != "/home/user/chosen" {
		t.Fatalf("project = %+v", project)
	}
	if !bytes.Contains(f.raw, []byte("name: chosen-worker-codex")) {
		t.Fatalf("composition did not use the repository's roles:\n%s", f.raw)
	}
	// The stored Team is what the plan composed.
	planned, err := yaml.Marshal(out.Plan.Team)
	if err != nil {
		t.Fatal(err)
	}
	if again, decodeErr := config.DecodeTeam(bytes.NewReader(planned)); decodeErr != nil ||
		!slices.Equal(roleNames(again.Spec.Roles), roleNames(team.Spec.Roles)) {
		t.Fatalf("plan team and stored team differ: %v", decodeErr)
	}

	opts, f = setupProject(t, "")
	if out, err = Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	team = storedTeam(t, f, "chosen")
	if out.Plan.Origin.Kind != OriginDefaults || team.Spec.Roles != nil ||
		!bytes.Contains(f.raw, []byte("name: chosen-majordomo-codex")) {
		t.Fatalf("origin=%+v roles=%v", out.Plan.Origin, team.Spec.Roles)
	}
}

// TestAddRejectsTheOldHeader is AC20 through a repository file.
func TestAddRejectsTheOldHeader(t *testing.T) {
	opts, f := setupProject(t, strings.Replace(workerFile, "apiVersion: v1beta1", "apiVersion: "+oldTeamHeader, 1))
	if _, err := Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "require v1beta1 Team") {
		t.Fatalf("old header accepted or unexplained: %v", err)
	}
	if len(f.writes) != 0 || len(f.teams) != 0 {
		t.Fatalf("writes = %v", f.writes)
	}
}

// TestAddDeclarationPrecedence is D10 and AC9: -f replaces the declaration,
// --no-repo-file ignores the repository's, and --role / --harness replace a
// list of whichever declaration was picked. The stored Team is what was
// applied, overrides included.
func TestAddDeclarationPrecedence(t *testing.T) {
	filed := "apiVersion: v1beta1\nkind: Team\nmetadata: {name: other}\nspec:\n" +
		"  roles:\n    - {name: filed, source: kukeon, path: team/roles/majordomo, harness: codex}\n"
	for _, tc := range []struct {
		name      string
		change    func(*AddOptions)
		origin    string
		roles     []string
		harnesses []string
		config    string
	}{
		{"repository", func(*AddOptions) {}, OriginRepository, []string{"worker"}, nil, "chosen-worker-codex"},
		{
			"file", func(o *AddOptions) { o.File, o.FileName = []byte(filed), "team.yaml" },
			OriginFile,
			[]string{"filed"},
			nil, "chosen-filed-codex",
		},
		{"no-repo-file", func(o *AddOptions) { o.NoRepoFile = true }, OriginDefaults, nil, nil, "chosen-majordomo-codex"},
		{
			"bare-default-role", func(o *AddOptions) { o.Roles = []string{"majordomo"} },
			OriginRepository,
			[]string{"majordomo"},
			nil, "chosen-majordomo-codex",
		},
		{
			"named-source-path", func(o *AddOptions) { o.Roles = []string{"extra=kukeon:team/roles/majordomo"} },
			OriginRepository,
			[]string{"extra"},
			nil, "chosen-extra-codex",
		},
		{
			"source-path", func(o *AddOptions) { o.Roles = []string{"kukeon:team/roles/majordomo/"} },
			OriginRepository,
			[]string{"majordomo"},
			nil, "chosen-majordomo-codex",
		},
		{
			"harness", func(o *AddOptions) { o.Harnesses = []string{"codex"} },
			OriginRepository,
			[]string{"worker"},
			[]string{"codex"},
			"chosen-worker-codex",
		},
		{"file-and-role", func(o *AddOptions) {
			o.File, o.FileName = []byte(filed), "team.yaml"
			o.Roles = []string{"majordomo"}
		}, OriginFile, []string{"majordomo"}, nil, "chosen-majordomo-codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, f := setupProject(t, workerFile)
			tc.change(&opts)
			out, err := Add(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			team := storedTeam(t, f, "chosen")
			var harnesses []string
			for _, h := range team.Spec.Harnesses {
				harnesses = append(harnesses, h.Name)
			}
			if out.Plan.Origin.Kind != tc.origin || !slices.Equal(roleNames(team.Spec.Roles), tc.roles) ||
				!slices.Equal(harnesses, tc.harnesses) {
				t.Fatalf("origin=%s roles=%v harnesses=%v", out.Plan.Origin.Kind, roleNames(team.Spec.Roles), harnesses)
			}
			if !bytes.Contains(f.raw, []byte("name: "+tc.config)) {
				t.Fatalf("config %s not applied:\n%s", tc.config, f.raw)
			}
			if tc.name == "bare-default-role" &&
				(team.Spec.Roles[0].Needs == nil || team.Spec.Roles[0].InitialPath != "$HOME") {
				t.Fatalf("a bare role name must copy the default role whole: %+v", team.Spec.Roles[0])
			}
		})
	}
}

func TestAddOverrideErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts func(*AddOptions)
		want string
	}{
		{"unknown default role", func(o *AddOptions) { o.Roles = []string{"ghost"} }, "no default role of that name"},
		{"unknown default harness", func(o *AddOptions) { o.Harnesses = []string{"ghost"} }, "no default harness"},
		{"malformed selection", func(o *AddOptions) { o.Roles = []string{"name=nopath"} }, "[NAME=]SOURCE:PATH"},
		{"unregistered source", func(o *AddOptions) { o.Roles = []string{"nope:roles/x"} }, "nope"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, f := setupProject(t, workerFile)
			tc.opts(&opts)
			if _, err := Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.writes) != 0 {
				t.Fatalf("writes = %v", f.writes)
			}
		})
	}
}

// TestAddExistingTeamNeedsForce is AC8a: add creates; replacing needs --force,
// which does not consult the stored declaration but keeps its recorded
// secret bindings.
func TestAddExistingTeamNeedsForce(t *testing.T) {
	opts, f := setupProject(t, workerFile)
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Harnesses = []config.Harness{
			{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"},
		}
	})
	opts.Sources["project"] = fstest.MapFS{"kuketeam.yaml": {Data: []byte(strings.ReplaceAll(
		workerFile, "harness: codex", "harness: opencode"))}}
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	editTeam(t, f, "chosen", func(team *config.Team) {
		team.Spec.Roles = append(
			team.Spec.Roles,
			config.Role{Name: "stale", Source: "kukeon", Path: "team/roles/majordomo"},
		)
		team.Spec.Secrets[0].SecretRef.Name = "kept-router-key"
	})
	before, writes := slices.Clone(f.teams["chosen"]), len(f.writes)
	_, err := Add(t.Context(), opts)
	if !errors.Is(err, errdefs.ErrTeamExists) || !strings.Contains(err.Error(), "kuke team apply chosen") ||
		!strings.Contains(err.Error(), "--force") {
		t.Fatalf("err = %v", err)
	}
	if len(f.writes) != writes || !bytes.Equal(before, f.teams["chosen"]) {
		t.Fatal("a refused add wrote")
	}
	opts.Force = true
	if _, err = Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	team := storedTeam(t, f, "chosen")
	if !slices.Equal(roleNames(team.Spec.Roles), []string{"worker"}) {
		t.Fatalf("--force consulted the stored declaration: %v", roleNames(team.Spec.Roles))
	}
	if len(team.Spec.Secrets) != 1 || team.Spec.Secrets[0].SecretRef.Name != "kept-router-key" {
		t.Fatalf("recorded bindings lost: %+v", team.Spec.Secrets)
	}
}

// TestDryRunWritesNothing is AC10: a dry run plans the whole application
// (origin, sources, pairs, the Configs created, re-rendered and pruned, the
// secrets it would create and the missing ones) and creates no Secret,
// applies no document and reads no secret value.
func TestDryRunWritesNothing(t *testing.T) {
	opts, f := setupProject(t, workerFile)
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Harnesses = append(cfg.Spec.Defaults.Harnesses,
			config.Harness{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"})
		// The variable is unset: reading it would fail the command.
		cfg.Spec.Secrets = []config.SecretBinding{binding(&config.SecretSource{Env: "DRY_RUN_UNSET_TOKEN"})}
	})
	t.Setenv("DRY_RUN_UNSET_TOKEN", "")
	opts.Sources["project"] = fstest.MapFS{"kuketeam.yaml": {Data: []byte(
		strings.Replace(workerFile, ", harness: codex", "", 1))}}
	opts.DryRun = true
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if out.Result != nil || len(f.writes) != 0 || len(f.teams) != 0 {
		t.Fatalf("dry run wrote: %v", f.writes)
	}
	p := out.Plan
	if p.Origin.Kind != OriginRepository || p.Team.Metadata.Name != "chosen" ||
		!slices.Equal(p.Create, []string{"chosen-worker-codex", "chosen-worker-opencode"}) ||
		!slices.Equal(p.CreateSecrets, []string{"provider-account"}) ||
		!slices.ContainsFunc(
			p.Sources,
			func(s PlannedSource) bool { return s.Name == "project" && s.Ref == "branch main" },
		) {
		t.Fatalf("plan = %+v", p)
	}
	if len(p.Pairs) != 2 || p.Pairs[0].Config != "chosen-worker-codex" || p.Pairs[0].Image == "" {
		t.Fatalf("pairs = %+v", p.Pairs)
	}

	// Applied for real (with the value set), then planned again: the team's
	// Configs are re-rendered, and one the declaration no longer renders is
	// pruned.
	t.Setenv("DRY_RUN_UNSET_TOKEN", "value")
	opts.DryRun = false
	if _, err = Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	editTeam(t, f, "chosen", func(team *config.Team) {
		team.Spec.Harnesses = []config.Harness{{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"}}
	})
	writes := len(f.writes)
	plan := Options{Client: f, Sources: opts.Sources, DryRun: true}
	if out, err = ApplyStored(t.Context(), plan, "chosen", ""); err != nil {
		t.Fatal(err)
	}
	if len(f.writes) != writes || out.Result != nil {
		t.Fatalf("dry run wrote: %v", f.writes[writes:])
	}
	if !slices.Equal(out.Plan.Rerender, []string{"chosen-worker-codex"}) ||
		!slices.Equal(out.Plan.Prune, []string{"chosen-worker-opencode"}) || len(out.Plan.Create) != 0 {
		t.Fatalf("plan = %+v", out.Plan)
	}
}

// TestEmptySelectionExplainsItself is AC11.
func TestEmptySelectionExplainsItself(t *testing.T) {
	for _, tc := range []struct {
		name   string
		file   string
		change func(*config.TeamDefaults)
		want   string
	}{
		{
			"no file and no default roles", "", func(cfg *config.TeamDefaults) { cfg.Spec.Defaults.Roles = nil },
			"the repository has no kuketeam.yaml and the defaults have no default roles",
		},
		{
			"no default harnesses", strings.Replace(workerFile, ", harness: codex", "", 1),
			func(cfg *config.TeamDefaults) { cfg.Spec.Defaults.Harnesses = nil },
			"the defaults have no default harnesses",
		},
		{
			"empty roles list", "apiVersion: v1beta1\nkind: Team\nspec: {roles: []}\n", nil,
			"the declaration's roles list is empty",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts, f := setupProject(t, tc.file)
			if tc.change != nil {
				changeAddConfig(t, opts, tc.change)
			}
			_, err := Add(t.Context(), opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "--role") ||
				!strings.Contains(err.Error(), "kuke team source add") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if len(f.writes) != 0 {
				t.Fatalf("writes = %v", f.writes)
			}
		})
	}
}

// TestApplyStoredIgnoresTheRepositoryFile is AC12: kuke team apply NAME
// recomposes from the stored Team only.
func TestApplyStoredIgnoresTheRepositoryFile(t *testing.T) {
	opts, f := setupProject(t, "")
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	opts.Sources["project"] = fstest.MapFS{"kuketeam.yaml": {Data: []byte(workerFile)}}
	out, err := ApplyStored(t.Context(), opts.Options, "chosen", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan.Origin.Kind != OriginStored || out.Plan.Command != "kuke team apply chosen" ||
		bytes.Contains(
			f.raw,
			[]byte("chosen-worker-codex"),
		) || !bytes.Contains(f.raw, []byte("chosen-majordomo-codex")) {
		t.Fatalf("apply consulted the repository file: plan=%+v", out.Plan)
	}
}

// TestApplyFile is AC12: kuke team apply -f composes the declaration in the
// file and stores it, creating the team when it is new; without
// spec.projectSource the stored one is kept, and so are the stored secret
// bindings beneath the file's own.
func TestApplyFile(t *testing.T) {
	f := newFake()
	storeDefaults(t, f, addDefaults())
	opts := Options{Client: f, Sources: sourceFS()}
	raw := []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: filed}\nspec:\n" +
		"  projectSource: {url: 'https://example.test/owner/filed.git', branch: main}\n")
	out, err := ApplyFile(t.Context(), opts, raw, "filed.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan.Origin != (Origin{Kind: OriginFile, Detail: "filed.yaml"}) ||
		out.Plan.Command != "kuke team apply -f filed.yaml" || f.team != "filed" {
		t.Fatalf("plan = %+v team=%q", out.Plan, f.team)
	}
	team := storedTeam(t, f, "filed")
	if team.Metadata.Realm != "default" || team.Spec.ProjectSource == nil ||
		team.Spec.ProjectSource.Name != "project" || team.Spec.ProjectSource.URL != "https://example.test/owner/filed.git" {
		t.Fatalf("stored team = %+v", team)
	}
	editTeam(t, f, "filed", func(team *config.Team) {
		team.Spec.Secrets = []config.SecretBinding{{
			Name: "openrouter-api-key", EnvName: "OPENROUTER_API_KEY",
			SecretRef: config.SecretRef{Name: "kept-key", Realm: "default"},
		}}
	})
	// The edit drops projectSource and selects a harness that needs the
	// recorded binding.
	raw = []byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: filed}\nspec:\n" +
		"  harnesses:\n    - {name: opencode, source: kukeon, path: team/harnesses/opencode}\n")
	f.secrets[api.SecretMetadata{Name: "kept-key", Realm: "default"}] = "present"
	if out, err = ApplyFile(t.Context(), opts, raw, "filed.yaml"); err != nil {
		t.Fatal(err)
	}
	team = storedTeam(t, f, "filed")
	if team.Spec.ProjectSource == nil || team.Spec.ProjectSource.URL != "https://example.test/owner/filed.git" {
		t.Fatalf("stored projectSource not kept: %+v", team.Spec.ProjectSource)
	}
	if len(team.Spec.Secrets) != 1 || team.Spec.Secrets[0].SecretRef.Name != "kept-key" ||
		len(out.Result.MissingSecrets) != 0 {
		t.Fatalf("stored bindings not kept: %+v missing=%v", team.Spec.Secrets, out.Result.MissingSecrets)
	}
	for _, bad := range []string{
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: root}\n",
		"apiVersion: v1beta1\nkind: Team\nspec: {}\n",
		"apiVersion: " + oldTeamHeader + "\nkind: Team\nmetadata: {name: filed}\n",
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: filed, realm: other}\n",
	} {
		writes := len(f.writes)
		if _, err = ApplyFile(t.Context(), opts, []byte(bad), "bad.yaml"); err == nil ||
			!strings.Contains(err.Error(), "bad.yaml") || len(f.writes) != writes {
			t.Fatalf("accepted %q: %v", bad, err)
		}
	}
	// Teams live in realm default (D14), as Add enforces: a file naming
	// another realm is refused rather than merged against default's stored
	// team and stored where ApplyStored and Add can never find it.
	_, err = ApplyFile(t.Context(), opts,
		[]byte("apiVersion: v1beta1\nkind: Team\nmetadata: {name: filed, realm: other}\n"), "other.yaml")
	if err == nil || !strings.Contains(err.Error(), `remove metadata.realm "other"`) {
		t.Fatalf("non-default realm: %v", err)
	}
}
