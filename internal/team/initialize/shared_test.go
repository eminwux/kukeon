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
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// composeTeam composes the majordomo role on codex and claude for the team
// "root", after change adjusts the global configuration.
func composeTeam(t *testing.T, change func(*config.TeamDefaults)) *compose.Result {
	t.Helper()
	const name = "root"
	cfg := &config.TeamDefaults{APIVersion: config.APIVersion, Kind: config.KindTeamDefaults, Spec: config.Spec{
		Defaults: config.Defaults{
			Sources: []config.Source{{
				Name: "kukeon", URL: "github.com/eminwux/kukeon", Branch: "main", ClonePath: "/home/user/kukeon-src",
			}},
			Roles: []config.Role{{Name: "majordomo", Source: "kukeon", Path: "team/roles/majordomo"}},
			Harnesses: []config.Harness{
				{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"},
				{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"},
			},
		},
	}}
	if change != nil {
		change(cfg)
	}
	team := inheritingTeam(name)
	result, err := compose.Compose(cfg, team, compose.Options{Sources: sourceFS()})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// appliedDocs decodes the document stream the fake daemon received.
func appliedDocs(t *testing.T, raw []byte) ([]string, map[string][]string) {
	t.Helper()
	var kinds []string
	names := map[string][]string{}
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := decoder.Decode(&doc); errors.Is(err, io.EOF) {
			return kinds, names
		} else if err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, doc.Kind)
		names[doc.Kind] = append(names[doc.Kind], doc.Metadata.Name)
	}
}

func storedShared(name, digest string) api.CellBlueprintDoc {
	return api.CellBlueprintDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellBlueprint,
		Metadata: api.CellBlueprintMetadata{
			Name: name, Realm: "default",
			Labels: map[string]string{api.LabelShared: "true", api.LabelContentDigest: digest},
		},
	}
}

func configReferencing(name, team string, ref api.CellConfigBlueprintRef) api.CellConfigDoc {
	labels := map[string]string{}
	if team != "" {
		labels[api.LabelTeam] = team
	}
	return api.CellConfigDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig,
		Metadata: api.CellConfigMetadata{
			Name:   name,
			Realm:  "default",
			Space:  "default",
			Stack:  "default",
			Labels: labels,
		},
		Spec: api.CellConfigSpec{Blueprint: ref},
	}
}

// Resources are counted per document applied: each shared blueprint once, each
// config once. Two roles on one harness share its blueprint.
func TestResourceAccountingCountsSharedBlueprintsOnce(t *testing.T) {
	f := newFake()
	result := composeTeam(t, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
			config.Role{Name: "second", Source: "kukeon", Path: "team/roles/majordomo"})
	})
	if len(result.Pairs) != 4 || len(result.Blueprints) != 2 {
		t.Fatalf("pairs=%d blueprints=%d", len(result.Pairs), len(result.Blueprints))
	}
	done, err := apply(t.Context(), f, result)
	if err != nil {
		t.Fatal(err)
	}
	kinds, names := appliedDocs(t, f.raw)
	// Two shared blueprints, four configs and the Team.
	if done.resources != 7 || len(kinds) != 7 || kinds[6] != "Team" ||
		!slices.Equal(names["CellBlueprint"], []string{"codex", "claude"}) || len(names["CellConfig"]) != 4 {
		t.Fatalf("resources=%d docs=%v", done.resources, names)
	}
	if len(done.notes) != 0 || len(done.missing) != 0 {
		t.Fatalf("notes=%v missing=%v", done.notes, done.missing)
	}
	// A daemon that accepts fewer documents than were sent is an incomplete apply.
	f.resources = []kukeonv1.ApplyResourceResult{
		{Action: "created"},
		{Action: "created"},
		{Action: "created"},
		{Action: "created"},
	}
	if _, err = apply(t.Context(), f, result); err == nil ||
		!strings.Contains(err.Error(), "4 of 7 resources accepted") {
		t.Fatalf("err=%v", err)
	}
}

// TestSharedBlueprintNames covers how application names a shared blueprint when
// the realm already holds one under its plain name (#1560): a blueprint kuke
// team rendered is replaced in place, whoever uses it; only a hand-written one
// makes this team's copy take a content-digest suffix.
func TestSharedBlueprintNames(t *testing.T) {
	plain := api.CellConfigBlueprintRef{Name: "codex", Realm: "default"}
	for _, tc := range []struct {
		name    string
		prepare func(*fakeClient, *compose.Result)
		renamed bool
		// users is the other configs the replacement note names; empty means
		// no replacement note.
		users string
	}{
		{"absent", func(*fakeClient, *compose.Result) {}, false, ""},
		{"same-content", func(f *fakeClient, r *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", r.Blueprints[0].Metadata.Labels[api.LabelContentDigest])
			// Even when another team uses it: identical content is the same blueprint.
			f.configs = []api.CellConfigDoc{configReferencing("other-majordomo-codex", "other", plain)}
		}, false, ""},
		{"different-content-used-by-another-team", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{
				configReferencing("root-majordomo-codex", "root", plain),
				configReferencing("other-majordomo-codex", "other", plain),
				configReferencing("other-pm-codex", "other", plain),
			}
		}, false, "team other (other-majordomo-codex, other-pm-codex)"},
		{"different-content-used-by-a-hand-written-config", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{configReferencing("mine", "", plain)}
		}, false, "configs without a team (mine)"},
		{"different-content-used-only-by-this-team", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{configReferencing("root-majordomo-codex", "root", plain)}
		}, false, ""},
		{"different-content-unreferenced", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
		}, false, ""},
		{"another-team-references-a-same-named-blueprint-elsewhere", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{
				configReferencing("other-realm", "other", api.CellConfigBlueprintRef{Name: "codex", Realm: "auth"}),
				configReferencing("other-scope", "other", api.CellConfigBlueprintRef{Name: "codex", Realm: "default", Space: "default"}),
				configReferencing("other-name", "other", api.CellConfigBlueprintRef{Name: "claude", Realm: "default"}),
			}
		}, false, ""},
		// A blueprint kuke team did not render is never overwritten, used or not.
		{"not-shared", func(f *fakeClient, _ *compose.Result) {
			doc := storedShared("codex", "")
			doc.Metadata.Labels = map[string]string{api.LabelTeam: "legacy"}
			f.blueprints["default/codex"] = doc
		}, true, ""},
		{"not-shared-used-by-another-team", func(f *fakeClient, _ *compose.Result) {
			doc := storedShared("codex", "")
			doc.Metadata.Labels = nil
			f.blueprints["default/codex"] = doc
			f.configs = []api.CellConfigDoc{configReferencing("other-majordomo-codex", "other", plain)}
		}, true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			result := composeTeam(t, nil)
			codex := result.Blueprints[0]
			digest := codex.Metadata.Labels[api.LabelContentDigest]
			tc.prepare(f, result)
			for attempt := range 2 {
				if attempt == 1 {
					// A rerun recomposes from scratch and must settle on the same name.
					result = composeTeam(t, nil)
					codex = result.Blueprints[0]
				}
				done, err := apply(t.Context(), f, result)
				if err != nil {
					t.Fatal(err)
				}
				want := "codex"
				if tc.renamed {
					want = "codex-" + digest
				}
				_, names := appliedDocs(t, f.raw)
				if codex.Metadata.Name != want || !slices.Equal(names["CellBlueprint"], []string{want, "claude"}) {
					t.Fatalf("attempt %d: blueprint applied as %v, want %s", attempt, names["CellBlueprint"], want)
				}
				for _, pair := range result.Pairs {
					wantRef := pair.Harness
					if pair.Harness == "codex" {
						wantRef = want
					}
					if pair.Config.Spec.Blueprint.Name != wantRef {
						t.Fatalf(
							"%s references %q, want %q",
							pair.Config.Metadata.Name,
							pair.Config.Spec.Blueprint.Name,
							wantRef,
						)
					}
				}
				var wantNote string
				switch {
				case tc.renamed:
					wantNote = "blueprint codex already exists in realm default with different content; " +
						"applied this team's as " + want
				case tc.users != "" && attempt == 0:
					// The rerun finds its own content in place: nothing to report.
					wantNote = "blueprint codex in realm default replaced in place (content 0000000000000000 → " +
						digest + "); also used by " + tc.users + ", whose cells pick up the new content on restart"
				}
				if wantNote == "" && len(done.notes) != 0 ||
					wantNote != "" && !slices.Equal(done.notes, []string{wantNote}) {
					t.Fatalf("attempt %d: notes = %q, want %q", attempt, done.notes, wantNote)
				}
				if done.resources != 5 {
					t.Fatalf("resources = %d", done.resources)
				}
				if tc.renamed {
					// The hand-written blueprint is left exactly as it was.
					if kept := f.blueprints["default/codex"]; kept.Metadata.Labels[api.LabelContentDigest] == digest {
						t.Fatal("hand-written blueprint overwritten")
					}
					if f.blueprints["default/"+want].Metadata.Labels[api.LabelContentDigest] != digest {
						t.Fatal("digest-named blueprint not stored")
					}
				} else if f.blueprints["default/codex"].Metadata.Labels[api.LabelContentDigest] != digest {
					t.Fatal("plain-named blueprint does not hold this team's content")
				}
				if _, forked := f.blueprints["default/codex-"+digest]; forked && !tc.renamed {
					t.Fatal("a digest-named copy was created for a shared blueprint")
				}
			}
		})
	}
}

// forkedRealm is the realm state #1560 reports: another team on the plain
// shared blueprint, and this team (root) on the digest-named copy an earlier
// application forked under the pre-#1560 rule.
func forkedRealm(t *testing.T, f *fakeClient, result *compose.Result, otherOnFork bool) string {
	t.Helper()
	digest := result.Blueprints[0].Metadata.Labels[api.LabelContentDigest]
	fork := "codex-" + digest
	f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
	f.blueprints["default/"+fork] = storedShared(fork, digest)
	f.configs = []api.CellConfigDoc{
		configReferencing("root-majordomo-codex", "root", api.CellConfigBlueprintRef{Name: fork, Realm: "default"}),
		configReferencing("other-pm-codex", "other", api.CellConfigBlueprintRef{Name: "codex", Realm: "default"}),
	}
	if otherOnFork {
		f.configs = append(f.configs, configReferencing(
			"third-dev-codex", "third", api.CellConfigBlueprintRef{Name: fork, Realm: "default"}))
	}
	return fork
}

// A team still on a digest-named fork is moved back onto the plain name, which
// it replaces in place; the fork, once nothing references it, is removed by
// the daemon's per-team prune, and the note says so.
func TestSharedBlueprintForkIsMigrated(t *testing.T) {
	f := newFake()
	f.prune = true
	result := composeTeam(t, nil)
	digest := result.Blueprints[0].Metadata.Labels[api.LabelContentDigest]
	fork := forkedRealm(t, f, result, false)
	done, err := apply(t.Context(), f, result)
	if err != nil {
		t.Fatal(err)
	}
	_, names := appliedDocs(t, f.raw)
	if !slices.Equal(names["CellBlueprint"], []string{"codex", "claude"}) {
		t.Fatalf("blueprints applied as %v", names["CellBlueprint"])
	}
	for _, cfg := range f.configs {
		if cfg.Metadata.Name == "root-majordomo-codex" && cfg.Spec.Blueprint.Name != "codex" {
			t.Fatalf("this team's config still references %q", cfg.Spec.Blueprint.Name)
		}
	}
	if f.blueprints["default/codex"].Metadata.Labels[api.LabelContentDigest] != digest {
		t.Fatal("plain-named blueprint not replaced in place")
	}
	if _, kept := f.blueprints["default/"+fork]; kept {
		t.Fatal("unreferenced fork not pruned")
	}
	want := []string{
		"blueprint codex in realm default replaced in place (content 0000000000000000 → " + digest +
			"); also used by team other (other-pm-codex), whose cells pick up the new content on restart",
		"blueprint " + fork + " in realm default removed: this team's configs now use codex",
	}
	if !slices.Equal(done.notes, want) {
		t.Fatalf("notes = %q, want %q", done.notes, want)
	}
	// Once migrated, a rerun has nothing left to report.
	if done, err = apply(t.Context(), f, composeTeam(t, nil)); err != nil ||
		len(done.notes) != 0 {
		t.Fatalf("rerun notes = %q err=%v", done.notes, err)
	}
}

// A fork another team still uses is kept, and the note names who uses it.
func TestSharedBlueprintForkStillReferencedIsReported(t *testing.T) {
	f := newFake()
	f.prune = true
	result := composeTeam(t, nil)
	fork := forkedRealm(t, f, result, true)
	done, err := apply(t.Context(), f, result)
	if err != nil {
		t.Fatal(err)
	}
	if _, kept := f.blueprints["default/"+fork]; !kept {
		t.Fatal("a fork another team references was pruned")
	}
	want := "blueprint " + fork + " in realm default is no longer used by this team (its configs now use codex) " +
		"but is kept: still referenced by team third (third-dev-codex)"
	if len(done.notes) != 2 || done.notes[1] != want {
		t.Fatalf("notes = %q, want %q last", done.notes, want)
	}
}

// When the daemon does not report the fork pruned and nothing else references
// it, the note says how to remove it.
func TestSharedBlueprintForkNotPrunedIsReported(t *testing.T) {
	f := newFake()
	result := composeTeam(t, nil)
	fork := forkedRealm(t, f, result, false)
	done, err := apply(t.Context(), f, result)
	if err != nil {
		t.Fatal(err)
	}
	want := "blueprint " + fork + " in realm default is no longer used by this team (its configs now use codex); " +
		"once nothing references it, remove it with kuke delete blueprint " + fork + " --realm default"
	if len(done.notes) != 2 || done.notes[1] != want {
		t.Fatalf("notes = %q, want %q last", done.notes, want)
	}
}

// Only a shared `<prefix>-<16 hex>` name is treated as a fork: a hand-written
// blueprint this team's configs happen to reference is not reported.
func TestIsDigestName(t *testing.T) {
	for name, want := range map[string]bool{
		"codex-0123456789abcdef":      true,
		"codex-host-0123456789abcdef": false,
		"codex":                       false,
		"codex-0123456789abcde":       false,
		"codex-0123456789ABCDEF":      false,
		"codex-host":                  false,
		"claude-0123456789abcdef":     false,
		"codex-0123456789abcdef0":     false,
		"codex-host-0123456789abcdeg": false,
		"codexx-0123456789abcdef":     false,
		"codex--0123456789abcdef":     false,
	} {
		if got := isDigestName(name, "codex"); got != want {
			t.Errorf("isDigestName(%q, codex) = %v, want %v", name, got, want)
		}
	}
	if !isDigestName("codex-host-0123456789abcdef", "codex-host") || isDigestName("x-0123456789abcdef", "") {
		t.Error("prefix handling")
	}
}

func TestSharedBlueprintLookupFailureAppliesNothing(t *testing.T) {
	f := newFake()
	f.blueprintErr = errors.New("permission denied: sensitive-detail")
	_, err := apply(t.Context(), f, composeTeam(t, nil))
	if err == nil || !strings.Contains(err.Error(), "blueprint codex lookup") ||
		strings.Contains(err.Error(), "sensitive-detail") || f.applies != 0 || f.creates != 0 {
		t.Fatalf("err=%v applies=%d", err, f.applies)
	}
}

// The replacement note reaches the operator through the init result.
func TestInitReportsReplacedSharedBlueprint(t *testing.T) {
	opts, f := setupInit(t)
	f.blueprints["default/claude"] = storedShared("claude", "0000000000000000")
	f.configs = []api.CellConfigDoc{
		configReferencing("other-dev-claude", "other", api.CellConfigBlueprintRef{Name: "claude", Realm: "default"}),
	}
	out, err := Init(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	result := out.Result
	const replaced = "blueprint claude in realm default replaced in place (content 0000000000000000 → "
	if len(result.Notes) != 1 || !strings.HasPrefix(result.Notes[0], replaced) ||
		!strings.Contains(result.Notes[0], "also used by team other (other-dev-claude)") ||
		result.Resources != 7 {
		t.Fatalf("notes=%v resources=%d", result.Notes, result.Resources)
	}
	// The plan carried the same note before anything was written.
	if !slices.Equal(out.Plan.Notes, result.Notes) {
		t.Fatalf("plan notes = %v", out.Plan.Notes)
	}
	if _, names := appliedDocs(t, f.raw); !slices.Contains(names["CellBlueprint"], "claude") {
		t.Fatalf("blueprints applied as %v, want claude under its plain name", names["CellBlueprint"])
	}
}

// TestApplyLeavesTheStackGitAlone: kuke team writes no git defaults (issue
// #1591): a stack's git block is the operator's, and no Stack document is sent.
func TestApplyLeavesTheStackGitAlone(t *testing.T) {
	f := newFake()
	f.stackDefaults = &api.ScopeDefaults{
		Git: &api.ContainerGit{Author: &api.GitIdentity{Name: "Kept", Email: "k@example.com"}},
	}
	done, err := apply(t.Context(), f, composeTeam(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	if kinds, _ := appliedDocs(t, f.raw); done.resources != 5 || slices.Contains(kinds, "Stack") ||
		f.stackDefaults.Git.Author.Name != "Kept" {
		t.Fatalf("resources=%d kinds=%v", done.resources, kinds)
	}
}

// Git credential and signing secrets a source names that do not exist are
// reported with the typed-secret command, once each, without blocking
// application.
func TestMissingGitSecretsAreReported(t *testing.T) {
	deploy := config.SecretRef{Name: "deploy-key", Realm: "default"}
	signers := config.SecretRef{Name: "signers", Realm: "default"}
	withSecrets := func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Sources[0].Git = &config.SourceGit{
			Credential: &config.SourceCredential{SecretRef: deploy},
			Signing:    &config.GitSigning{SecretRef: deploy, AllowedSignersRef: &signers},
		}
	}
	f := newFake()
	f.secrets[api.SecretMetadata{Name: "signers", Realm: "default"}] = "present"
	done, err := apply(t.Context(), f, composeTeam(t, withSecrets))
	if err != nil || f.applies != 1 || f.creates != 0 {
		t.Fatalf("err=%v applies=%d creates=%d", err, f.applies, f.creates)
	}
	if len(done.missing) != 1 ||
		!strings.HasPrefix(
			done.missing[0],
			"deploy-key  →  kuke create secret deploy-key --realm default --type ssh-key",
		) {
		t.Fatalf("missing = %q", done.missing)
	}
	// Existing secrets are neither recreated nor reported.
	f = newFake()
	for _, name := range []string{"signers", "deploy-key"} {
		f.secrets[api.SecretMetadata{Name: name, Realm: "default"}] = "present"
	}
	done, err = apply(t.Context(), f, composeTeam(t, withSecrets))
	if err != nil || len(done.missing) != 0 || f.creates != 0 || len(done.notes) != 0 {
		t.Fatalf("missing=%q creates=%d notes=%q err=%v", done.missing, f.creates, done.notes, err)
	}
	// A failed lookup gates the apply and reports no daemon text.
	f = newFake()
	f.lookupErr = errors.New("denied: sensitive-detail")
	if _, err = apply(t.Context(), f, composeTeam(t, withSecrets)); err == nil ||
		f.applies != 0 || strings.Contains(err.Error(), "sensitive-detail") {
		t.Fatalf("err=%v applies=%d", err, f.applies)
	}
}

// TestAddHintsAtAMissingGitIdentity is AC13 through `kuke create team`: the
// project clones over SSH and nothing names a git identity, so the result says
// once how to store one; with a Realm default it says nothing.
func TestAddHintsAtAMissingGitIdentity(t *testing.T) {
	const hint = "no git identity is set; run kuke connector import git --default"
	for _, withDefault := range []bool{false, true} {
		opts, _ := setupAdd(t)
		fake := opts.Client.(*fakeClient)
		if withDefault {
			fake.realmDefaults = &api.ScopeDefaults{Git: &api.ContainerGit{
				IdentityRef: &api.ContainerSecretRef{Name: "git-ssh", Realm: "default"},
			}}
		}
		out, err := Add(t.Context(), opts)
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, note := range out.Result.Notes {
			if note == hint {
				count++
			}
		}
		if want := map[bool]int{false: 1, true: 0}[withDefault]; count != want {
			t.Fatalf("default=%v: notes = %v, want the hint %d time(s)", withDefault, out.Result.Notes, want)
		}
		if bytes.Contains(fake.raw, []byte("kind: Stack")) {
			t.Fatal("add wrote stack git defaults")
		}
	}
}
