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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// composeTeam composes the operator role on codex and claude for the team
// "root", after change adjusts the global configuration.
func composeTeam(t *testing.T, change func(*config.TeamsConfig)) *compose.Result {
	t.Helper()
	const name = "root"
	cfg := &config.TeamsConfig{APIVersion: config.APIVersion, Kind: config.Kind, Spec: config.Spec{
		Defaults: config.Defaults{
			Sources: []config.Source{{
				Name: "kukeon", URL: "github.com/eminwux/kukeon", Branch: "main", ClonePath: "/home/user/kukeon-src",
			}},
			Roles: []config.Role{{Name: "operator", Source: "kukeon", Path: "team/roles/operator"}},
			Harnesses: []config.Harness{
				{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"},
				{Name: "claude", Source: "kukeon", Path: "team/harnesses/claude"},
			},
		},
	}}
	if change != nil {
		change(cfg)
	}
	team := config.Root()
	team.Metadata.Name = name
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
	result := composeTeam(t, func(cfg *config.TeamsConfig) {
		cfg.Spec.Defaults.Roles = append(cfg.Spec.Defaults.Roles,
			config.Role{Name: "second", Source: "kukeon", Path: "team/roles/operator"})
	})
	if len(result.Pairs) != 4 || len(result.Blueprints) != 2 {
		t.Fatalf("pairs=%d blueprints=%d", len(result.Pairs), len(result.Blueprints))
	}
	done, err := apply(t.Context(), f, result, "kuke team init", nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds, names := appliedDocs(t, f.raw)
	if done.resources != 6 || len(kinds) != 6 ||
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
	if _, err = apply(t.Context(), f, result, "kuke team init", nil); err == nil ||
		!strings.Contains(err.Error(), "4 of 6 resources accepted") {
		t.Fatalf("err=%v", err)
	}
}

// TestSharedBlueprintNames covers how application names a shared blueprint when
// the realm already holds one under its plain name.
func TestSharedBlueprintNames(t *testing.T) {
	plain := api.CellConfigBlueprintRef{Name: "codex", Realm: "default"}
	for _, tc := range []struct {
		name    string
		prepare func(*fakeClient, *compose.Result)
		renamed bool
	}{
		{"absent", func(*fakeClient, *compose.Result) {}, false},
		{"same-content", func(f *fakeClient, r *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", r.Blueprints[0].Metadata.Labels[api.LabelContentDigest])
			// Even when another team uses it: identical content is the same blueprint.
			f.configs = []api.CellConfigDoc{configReferencing("other-operator-codex", "other", plain)}
		}, false},
		{"different-content-used-by-another-team", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{
				configReferencing("root-operator-codex", "root", plain),
				configReferencing("other-operator-codex", "other", plain),
			}
		}, true},
		{"different-content-used-by-a-hand-written-config", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{configReferencing("mine", "", plain)}
		}, true},
		{"different-content-used-only-by-this-team", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{configReferencing("root-operator-codex", "root", plain)}
		}, false},
		{"different-content-unreferenced", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
		}, false},
		{"another-team-references-a-same-named-blueprint-elsewhere", func(f *fakeClient, _ *compose.Result) {
			f.blueprints["default/codex"] = storedShared("codex", "0000000000000000")
			f.configs = []api.CellConfigDoc{
				configReferencing("other-realm", "other", api.CellConfigBlueprintRef{Name: "codex", Realm: "auth"}),
				configReferencing("other-scope", "other", api.CellConfigBlueprintRef{Name: "codex", Realm: "default", Space: "default"}),
				configReferencing("other-name", "other", api.CellConfigBlueprintRef{Name: "claude", Realm: "default"}),
			}
		}, false},
		// A blueprint kuke team did not render is never overwritten, used or not.
		{"not-shared", func(f *fakeClient, _ *compose.Result) {
			doc := storedShared("codex", "")
			doc.Metadata.Labels = map[string]string{api.LabelTeam: "legacy"}
			f.blueprints["default/codex"] = doc
		}, true},
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
				done, err := apply(t.Context(), f, result, "kuke team init", nil)
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
				noted := slices.ContainsFunc(done.notes, func(n string) bool {
					return strings.Contains(n, "blueprint codex already exists in realm default") &&
						strings.Contains(n, want)
				})
				if noted != tc.renamed || len(done.notes) != map[bool]int{true: 1, false: 0}[tc.renamed] {
					t.Fatalf("attempt %d: notes = %v", attempt, done.notes)
				}
				if done.resources != 4 {
					t.Fatalf("resources = %d", done.resources)
				}
				if tc.renamed {
					// The other owner's blueprint is left exactly as it was.
					if kept := f.blueprints["default/codex"]; kept.Metadata.Labels[api.LabelContentDigest] == digest {
						t.Fatal("foreign blueprint overwritten")
					}
					if f.blueprints["default/"+want].Metadata.Labels[api.LabelContentDigest] != digest {
						t.Fatal("digest-named blueprint not stored")
					}
				} else if f.blueprints["default/codex"].Metadata.Labels[api.LabelContentDigest] != digest {
					t.Fatal("plain-named blueprint does not hold this team's content")
				}
			}
		})
	}
}

func TestSharedBlueprintLookupFailureAppliesNothing(t *testing.T) {
	f := newFake()
	f.blueprintErr = errors.New("permission denied: sensitive-detail")
	_, err := apply(t.Context(), f, composeTeam(t, nil), "kuke team init", nil)
	if err == nil || !strings.Contains(err.Error(), "blueprint codex lookup") ||
		strings.Contains(err.Error(), "sensitive-detail") || f.applies != 0 || f.creates != 0 {
		t.Fatalf("err=%v applies=%d", err, f.applies)
	}
}

// The rename note reaches the operator through the run result.
func TestRunReportsRenamedSharedBlueprint(t *testing.T) {
	opts := setupRun(t)
	f := opts.Client.(*fakeClient)
	f.blueprints["default/claude"] = storedShared("claude", "0000000000000000")
	f.configs = []api.CellConfigDoc{
		configReferencing("other-dev-claude", "other", api.CellConfigBlueprintRef{Name: "claude", Realm: "default"}),
	}
	result, err := Run(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Notes) != 1 || !strings.Contains(result.Notes[0], "blueprint claude already exists") ||
		result.Resources != 4 {
		t.Fatalf("notes=%v resources=%d", result.Notes, result.Resources)
	}
	if !bytes.Contains(f.raw, []byte("name: claude-")) {
		t.Fatal("digest-named blueprint not applied")
	}
}

func gitConfig(cfg *config.TeamsConfig) {
	key := config.SecretRef{Name: "git-ssh", Realm: "default"}
	cfg.Spec.Git = &config.Git{
		Author:      &config.Identity{Name: "Dev Bot", Email: "dev-bot@example.com"},
		Credentials: []config.GitCredential{{Host: "github.com", SecretRef: key}},
		Signing:     &config.GitSigning{SecretRef: key},
		Sign:        []string{"commits"},
	}
}

// TestGitDefaultsAreWrittenToTheTeamStack is step 13's application half: the
// operator's default identity becomes the stack's defaults.git, so every team
// cell inherits it.
func TestGitDefaultsAreWrittenToTheTeamStack(t *testing.T) {
	f := newFake()
	f.secrets[api.SecretMetadata{Name: "git-ssh", Realm: "default"}] = "present"
	f.stackLabels = map[string]string{"owner": "ops"}
	f.stackDefaults = &api.ScopeDefaults{
		Values: map[string]string{"ANTHROPIC_MODEL": "sonnet"},
		Repos:  map[string]api.CellConfigRepoFill{"project": {URL: "https://example.test/p"}},
		Secrets: map[string]api.CellConfigSecretFill{
			"github-token": {SecretRef: &api.ContainerSecretRef{Name: "gh", Realm: "default"}},
		},
		Git: &api.ContainerGit{
			Author:    &api.GitIdentity{Name: "Old", Email: "old@example.com"},
			Committer: &api.GitIdentity{Name: "Kept Committer", Email: "kept@example.com"},
			Credentials: []api.GitCredential{
				{Host: "github.com", SecretRef: &api.ContainerSecretRef{Name: "old-key", Realm: "default"}, Port: 22},
				{Host: "gitlab.com", SecretRef: &api.ContainerSecretRef{Name: "gitlab-key", Realm: "default"}},
			},
			SigningKey:     "/home/dev/.ssh/id_ed25519.pub",
			AllowedSigners: "/home/dev/.ssh/allowed_signers",
		},
	}
	kept := *f.stackDefaults
	result := composeTeam(t, gitConfig)
	done, err := apply(t.Context(), f, result, "kuke team init", nil)
	if err != nil {
		t.Fatal(err)
	}
	kinds, _ := appliedDocs(t, f.raw)
	if done.resources != 5 || len(kinds) != 5 || kinds[0] != "Stack" {
		t.Fatalf("resources=%d kinds=%v: the stack document must lead the stream", done.resources, kinds)
	}
	var stack api.StackDoc
	if err = yaml.NewDecoder(bytes.NewReader(f.raw)).Decode(&stack); err != nil {
		t.Fatal(err)
	}
	if stack.APIVersion != api.APIVersionV1Beta1 || stack.Metadata.Name != "default" ||
		stack.Metadata.Labels["owner"] != "ops" || stack.Spec.ID != "default" ||
		stack.Spec.RealmID != "default" || stack.Spec.SpaceID != "default" {
		t.Fatalf("stack identity changed: %+v %+v", stack.Metadata, stack.Spec)
	}
	got := stack.Spec.Defaults
	if !reflect.DeepEqual(got.Values, kept.Values) || !reflect.DeepEqual(got.Repos, kept.Repos) ||
		!reflect.DeepEqual(got.Secrets, kept.Secrets) {
		t.Fatalf("non-git defaults changed: %+v", got)
	}
	want := &api.ContainerGit{
		Author:    &api.GitIdentity{Name: "Dev Bot", Email: "dev-bot@example.com"},
		Committer: &api.GitIdentity{Name: "Kept Committer", Email: "kept@example.com"},
		Credentials: []api.GitCredential{
			{Host: "github.com", SecretRef: &api.ContainerSecretRef{Name: "git-ssh", Realm: "default"}},
			{Host: "gitlab.com", SecretRef: &api.ContainerSecretRef{Name: "gitlab-key", Realm: "default"}},
		},
		Signing: &api.GitSigning{SecretRef: &api.ContainerSecretRef{Name: "git-ssh", Realm: "default"}},
		Sign:    []string{"commits"},
	}
	if !reflect.DeepEqual(got.Git, want) {
		t.Fatalf("stack git defaults = %+v, want %+v", got.Git, want)
	}
	// The caller's stack document was not mutated through shared slices.
	if kept.Git.Credentials[0].SecretRef.Name != "old-key" || kept.Git.Author.Name != "Old" {
		t.Fatal("merge aliased the daemon's stored defaults")
	}
	// Once written, a rerun sends no stack document.
	done, err = apply(t.Context(), f, composeTeam(t, gitConfig), "kuke team init", nil)
	if err != nil {
		t.Fatal(err)
	}
	if kinds, _ = appliedDocs(t, f.raw); done.resources != 4 || slices.Contains(kinds, "Stack") {
		t.Fatalf("rerun resources=%d kinds=%v", done.resources, kinds)
	}
}

func TestNoGitIdentityLeavesTheStackAlone(t *testing.T) {
	f := newFake()
	f.stackDefaults = &api.ScopeDefaults{
		Git: &api.ContainerGit{Author: &api.GitIdentity{Name: "Kept", Email: "k@example.com"}},
	}
	done, err := apply(t.Context(), f, composeTeam(t, nil), "kuke team init", nil)
	if err != nil {
		t.Fatal(err)
	}
	if kinds, _ := appliedDocs(t, f.raw); done.resources != 4 || slices.Contains(kinds, "Stack") ||
		f.stackDefaults.Git.Author.Name != "Kept" {
		t.Fatalf("resources=%d kinds=%v", done.resources, kinds)
	}
}

func TestMergeGit(t *testing.T) {
	ref := func(name string) *api.ContainerSecretRef {
		return &api.ContainerSecretRef{Name: name, Realm: "default"}
	}
	team := &api.ContainerGit{
		Committer: &api.GitIdentity{Name: "Team", Email: "team@example.com"},
		Credentials: []api.GitCredential{
			{Host: "b.example", SecretRef: ref("new-b")},
			{Host: "c.example", SecretRef: ref("c")},
		},
	}
	// Nothing stored yet: the team's block is the result.
	if got := mergeGit(nil, team); !reflect.DeepEqual(got, team) {
		t.Fatalf("merge onto nothing = %+v", got)
	}
	existing := &api.ContainerGit{
		Author: &api.GitIdentity{Name: "Stored", Email: "stored@example.com"},
		Credentials: []api.GitCredential{
			{Host: "a.example", SecretRef: ref("a")},
			{Host: "b.example", SecretRef: ref("old-b")},
		},
		Signing: &api.GitSigning{SecretRef: ref("stored-signing")},
		Sign:    []string{"tags"},
	}
	got := mergeGit(existing, team)
	want := &api.ContainerGit{
		Author:    &api.GitIdentity{Name: "Stored", Email: "stored@example.com"},
		Committer: &api.GitIdentity{Name: "Team", Email: "team@example.com"},
		Credentials: []api.GitCredential{
			{
				Host:      "a.example",
				SecretRef: ref("a"),
			}, {Host: "b.example", SecretRef: ref("new-b")}, {Host: "c.example", SecretRef: ref("c")},
		},
		// The team states no signing, so the stored one stands.
		Signing: &api.GitSigning{SecretRef: ref("stored-signing")},
		Sign:    []string{"tags"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %+v, want %+v", got, want)
	}
	if existing.Credentials[1].SecretRef.Name != "old-b" || len(existing.Credentials) != 2 {
		t.Fatal("merge mutated the stored block")
	}
	// Merging is idempotent, which is what makes a rerun send no stack document.
	if again := mergeGit(got, team); !reflect.DeepEqual(again, got) {
		t.Fatalf("second merge changed the block: %+v", again)
	}
}

// Git credential and signing secrets that do not exist are reported with the
// typed-secret command, once each, without blocking application.
func TestMissingGitSecretsAreReported(t *testing.T) {
	deploy := config.SecretRef{Name: "deploy-key", Realm: "default"}
	withSecrets := func(cfg *config.TeamsConfig) {
		gitConfig(cfg)
		signers := config.SecretRef{Name: "signers", Realm: "default"}
		cfg.Spec.Git.Signing.AllowedSignersRef = &signers
		cfg.Spec.Defaults.Sources[0].Git = &config.SourceGit{
			Credential: &config.SourceCredential{SecretRef: deploy},
			Signing:    &config.GitSigning{SecretRef: deploy},
		}
	}
	f := newFake()
	f.secrets[api.SecretMetadata{Name: "signers", Realm: "default"}] = "present"
	done, err := apply(t.Context(), f, composeTeam(t, withSecrets), "kuke team init", nil)
	if err != nil || f.applies != 1 || f.creates != 0 {
		t.Fatalf("err=%v applies=%d creates=%d", err, f.applies, f.creates)
	}
	if len(done.missing) != 2 ||
		!strings.HasPrefix(done.missing[0], "git-ssh  →  kuke create secret git-ssh --realm default --type ssh-key") ||
		!strings.HasPrefix(
			done.missing[1],
			"deploy-key  →  kuke create secret deploy-key --realm default --type ssh-key",
		) {
		t.Fatalf("missing = %q", done.missing)
	}
	// A secret about to be imported in this run is not reported missing.
	imported := []api.SecretDoc{{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
		Metadata: api.SecretMetadata{Name: "git-ssh", Realm: "default"},
		Spec:     api.SecretSpec{Type: api.SecretTypeSSHKey, SSHKey: &api.SecretSSHKey{PrivateKey: "material"}},
	}}
	f = newFake()
	done, err = apply(t.Context(), f, composeTeam(t, withSecrets), "kuke team init", imported)
	if err != nil || len(done.missing) != 2 || strings.Contains(strings.Join(done.missing, "\n"), "git-ssh") {
		t.Fatalf("missing=%q err=%v", done.missing, err)
	}
	if len(f.created) != 1 || f.created[0].Spec.SSHKey.PrivateKey != "material" ||
		bytes.Contains(f.raw, []byte("material")) {
		t.Fatal("imported secret not created out of band")
	}
	// Existing secrets are neither recreated nor reported.
	f = newFake()
	for _, name := range []string{"git-ssh", "signers", "deploy-key"} {
		f.secrets[api.SecretMetadata{Name: name, Realm: "default"}] = "present"
	}
	done, err = apply(t.Context(), f, composeTeam(t, withSecrets), "kuke team init", nil)
	if err != nil || len(done.missing) != 0 || f.creates != 0 || len(done.notes) != 0 {
		t.Fatalf("missing=%q creates=%d notes=%q err=%v", done.missing, f.creates, done.notes, err)
	}
	// An import over an existing secret replaces it and says so: skipping it
	// would report success while cells kept the old key.
	done, err = apply(t.Context(), f, composeTeam(t, withSecrets), "kuke team init", imported)
	if err != nil || len(done.missing) != 0 || f.creates != 1 ||
		f.created[0].Spec.SSHKey.PrivateKey != "material" {
		t.Fatalf("missing=%q creates=%d err=%v", done.missing, f.creates, err)
	}
	if !slices.ContainsFunc(done.notes, func(n string) bool {
		return strings.Contains(n, `secret "git-ssh" already existed and was replaced`)
	}) {
		t.Fatalf("notes = %q, want the replacement reported", done.notes)
	}
	// A failed lookup gates the apply and reports no daemon text.
	f = newFake()
	f.lookupErr = errors.New("denied: sensitive-detail")
	if _, err = apply(t.Context(), f, composeTeam(t, withSecrets), "kuke team init", nil); err == nil ||
		f.applies != 0 || strings.Contains(err.Error(), "sensitive-detail") {
		t.Fatalf("err=%v applies=%d", err, f.applies)
	}
}

// The operator sees composition notes (settings without effect, uncovered SSH
// sources) on the result of a successful add.
func TestAddReportsCompositionNotes(t *testing.T) {
	opts, _ := setupAdd(t)
	changeAddConfig(t, opts, func(cfg *config.TeamsConfig) {
		cfg.Spec.Git = &config.Git{
			Author:     &config.Identity{Name: "Dev", Email: "dev@example.com"},
			SigningKey: "/home/dev/.ssh/id_ed25519.pub",
		}
	})
	fake := newFake()
	opts.Client = fake
	result, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(result.Notes, func(n string) bool { return strings.Contains(n, "host paths") }) {
		t.Fatalf("notes = %v", result.Notes)
	}
	if bytes.Contains(fake.raw, []byte("id_ed25519")) || !bytes.Contains(fake.raw, []byte("kind: Stack")) ||
		!bytes.Contains(fake.raw, []byte("dev@example.com")) {
		t.Fatal("identity must reach the stack defaults without the host key path")
	}
	if entry, readErr := os.ReadFile(filepath.Join(opts.Root, "kuketeam.d", opts.Name+".yaml")); readErr != nil ||
		bytes.Contains(entry, []byte("dev@example.com")) {
		t.Fatalf("team registration must not carry the operator identity: %v", readErr)
	}
}
