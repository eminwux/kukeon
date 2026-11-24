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
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

type fakeClient struct {
	kukeonv1.FakeClient

	pingErr, lookupErr, createErr, applyErr               error
	missingRealm, missingSpace, missingStack, missingCell bool
	secrets                                               map[api.SecretMetadata]string
	lookups, creates, applies                             int
	competeAt                                             int
	resources                                             []kukeonv1.ApplyResourceResult
	raw                                                   []byte
	team                                                  string

	// Daemon state the shared-blueprint flow reads: stored blueprints keyed by
	// realm/name, stored configs, and the team stack's defaults. A successful
	// apply (without canned resources) persists what it was sent.
	blueprints    map[string]api.CellBlueprintDoc
	configs       []api.CellConfigDoc
	stackDefaults *api.ScopeDefaults
	spaceDefaults *api.SpaceDefaults
	stackLabels   map[string]string
	// The team realm's spec, as far as an applied Realm document can change it.
	realmDefaults *api.ScopeDefaults
	realmCreds    []api.RegistryCredentials
	blueprintErr  error
	// created records every secret document sent to CreateSecret.
	created []api.SecretDoc
	// deleted records every config name sent to DeleteConfig.
	deleted []string
	// prune makes a team apply delete that team's stored configs the applied
	// set leaves out, as kukeond's per-team prune does.
	prune bool

	// The team store, as kukeond keeps it: Team documents by name and the
	// realm default's TeamDefaults document (nil when none is stored).
	teams    map[string][]byte
	defaults []byte
	teamErr  error
	// writes records every write in order ("secret NAME", "apply",
	// "apply-team NAME", "delete-config NAME"); a dry run must leave it empty.
	writes []string
	// plain records the document stream of each ApplyDocuments call.
	plain [][]byte

	// stage answers StageGitFetch (nil: nothing to stage, as for a public
	// source); staged records each request and released each released id.
	stage    func(kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error)
	staged   []kukeonv1.GitFetchRequest
	released []string
}

func (f *fakeClient) StageGitFetch(
	_ context.Context, req kukeonv1.GitFetchRequest,
) (kukeonv1.StageGitFetchResult, error) {
	f.staged = append(f.staged, req)
	if f.stage == nil {
		return kukeonv1.StageGitFetchResult{}, nil
	}
	return f.stage(req)
}

func (f *fakeClient) ReleaseGitFetch(_ context.Context, id string) error {
	f.released = append(f.released, id)
	return nil
}

func newFake() *fakeClient {
	return &fakeClient{
		secrets: map[api.SecretMetadata]string{}, blueprints: map[string]api.CellBlueprintDoc{},
		teams: map[string][]byte{},
	}
}

func blueprintKey(md api.CellBlueprintMetadata) string { return md.Realm + "/" + md.Name }

func (f *fakeClient) GetBlueprint(
	_ context.Context, doc api.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if doc.Metadata.Space != "" || doc.Metadata.Stack != "" {
		panic("shared blueprint looked up below realm scope")
	}
	stored, ok := f.blueprints[blueprintKey(doc.Metadata)]
	return kukeonv1.GetBlueprintResult{Blueprint: stored, MetadataExists: ok}, f.blueprintErr
}

// ListConfigs is metadata-only, like the daemon's.
func (f *fakeClient) ListConfigs(context.Context, string, string, string) ([]api.CellConfigDoc, error) {
	out := make([]api.CellConfigDoc, 0, len(f.configs))
	for _, cfg := range f.configs {
		out = append(out, api.CellConfigDoc{APIVersion: cfg.APIVersion, Kind: cfg.Kind, Metadata: cfg.Metadata})
	}
	return out, nil
}

func (f *fakeClient) GetConfig(_ context.Context, doc api.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	for _, cfg := range f.configs {
		md := cfg.Metadata
		if md.Name == doc.Metadata.Name && md.Realm == doc.Metadata.Realm && md.Space == doc.Metadata.Space &&
			md.Stack == doc.Metadata.Stack {
			return kukeonv1.GetConfigResult{Config: cfg, MetadataExists: true}, nil
		}
	}
	return kukeonv1.GetConfigResult{}, nil
}

// persist stores an applied document the way the daemon would.
func (f *fakeClient) persist(kind string, node *yaml.Node, team string) {
	switch kind {
	case "CellBlueprint":
		var doc api.CellBlueprintDoc
		if err := node.Decode(&doc); err != nil {
			panic(err)
		}
		f.blueprints[blueprintKey(doc.Metadata)] = doc
	case "CellConfig":
		var doc api.CellConfigDoc
		if err := node.Decode(&doc); err != nil {
			panic(err)
		}
		if doc.Metadata.Labels[api.LabelTeam] != team {
			panic("config applied without its team label")
		}
		for i := range f.configs {
			if f.configs[i].Metadata.Name == doc.Metadata.Name {
				f.configs[i] = doc
				return
			}
		}
		f.configs = append(f.configs, doc)
	case "Stack":
		var doc api.StackDoc
		if err := node.Decode(&doc); err != nil {
			panic(err)
		}
		f.stackDefaults = doc.Spec.Defaults
	case "Realm":
		var doc api.RealmDoc
		if err := node.Decode(&doc); err != nil {
			panic(err)
		}
		f.realmDefaults, f.realmCreds = doc.Spec.Defaults, doc.Spec.RegistryCredentials
	case "TeamDefaults":
		raw, err := yaml.Marshal(node)
		if err != nil {
			panic(err)
		}
		f.defaults = raw
	}
}

func (f *fakeClient) DeleteConfig(_ context.Context, doc api.CellConfigDoc) (kukeonv1.DeleteConfigResult, error) {
	f.writes = append(f.writes, "delete-config "+doc.Metadata.Name)
	f.deleted = append(f.deleted, doc.Metadata.Name)
	kept := f.configs[:0]
	for _, cfg := range f.configs {
		md := cfg.Metadata
		if md.Name != doc.Metadata.Name || md.Realm != doc.Metadata.Realm || md.Space != doc.Metadata.Space ||
			md.Stack != doc.Metadata.Stack {
			kept = append(kept, cfg)
		}
	}
	f.configs = kept
	return kukeonv1.DeleteConfigResult{Config: doc, Deleted: true}, nil
}

func (f *fakeClient) Close() error               { return nil }
func (f *fakeClient) Ping(context.Context) error { return f.pingErr }
func (f *fakeClient) ListRealms(context.Context) ([]api.RealmDoc, error) {
	if f.missingRealm {
		return nil, nil
	}
	return []api.RealmDoc{
		{
			Metadata: api.RealmMetadata{Name: "default"},
			Spec: api.RealmSpec{
				Namespace: "default.kukeon.io", RegistryCredentials: f.realmCreds, Defaults: f.realmDefaults,
			},
		},
		{Metadata: api.RealmMetadata{Name: "auth"}},
	}, nil
}

func (f *fakeClient) ListSpaces(context.Context, string) ([]api.SpaceDoc, error) {
	if f.missingSpace {
		return nil, nil
	}
	return []api.SpaceDoc{
		{Metadata: api.SpaceMetadata{Name: "default"}, Spec: api.SpaceSpec{Defaults: f.spaceDefaults}},
		{Metadata: api.SpaceMetadata{Name: "shared"}},
	}, nil
}

func (f *fakeClient) ListStacks(context.Context, string, string) ([]api.StackDoc, error) {
	if f.missingStack {
		return nil, nil
	}
	return []api.StackDoc{
		{
			Metadata: api.StackMetadata{Name: "default", Labels: f.stackLabels},
			Spec:     api.StackSpec{ID: "default", RealmID: "default", SpaceID: "default", Defaults: f.stackDefaults},
		},
		{Metadata: api.StackMetadata{Name: "operators"}},
	}, nil
}

func (f *fakeClient) ListCells(context.Context, string, string, string) ([]api.CellDoc, error) {
	if f.missingCell {
		return nil, nil
	}
	return []api.CellDoc{{Metadata: api.CellMetadata{Name: "parent"}}}, nil
}

func (f *fakeClient) GetSecret(_ context.Context, doc api.SecretDoc) (kukeonv1.GetSecretResult, error) {
	if doc.Spec.Data != "" || doc.Spec.SSHKey != nil || doc.Spec.BasicAuth != nil || doc.Spec.Type != "" {
		panic("lookup sent secret bytes")
	}
	f.lookups++
	if f.lookups == f.competeAt {
		f.secrets[doc.Metadata] = "concurrent-value"
	}
	_, exists := f.secrets[doc.Metadata]
	return kukeonv1.GetSecretResult{MetadataExists: exists}, f.lookupErr
}

func (f *fakeClient) CreateSecret(_ context.Context, doc api.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	f.writes = append(f.writes, "secret "+doc.Metadata.Name)
	f.creates++
	f.created = append(f.created, doc)
	// Model an ambiguous failure after the daemon already accepted the write.
	f.secrets[doc.Metadata] = doc.Spec.Data
	return kukeonv1.CreateSecretResult{Created: true}, f.createErr
}

func (f *fakeClient) ApplyDocumentsForTeam(
	_ context.Context,
	raw []byte,
	team string,
) (kukeonv1.ApplyDocumentsResult, error) {
	f.writes = append(f.writes, "apply-team "+team)
	f.applies++
	f.raw = raw
	f.team = team
	nodes := decodeStream(raw)
	// D6: a Team document is accepted only in the team apply for its own name,
	// and stored only when every other document applied.
	var teamDoc *yaml.Node
	for i := range nodes {
		if kind, name := docHeader(&nodes[i]); kind == "Team" {
			if name != team || teamDoc != nil {
				return kukeonv1.ApplyDocumentsResult{}, fmt.Errorf("team %q in the apply for %q: %w",
					name, team, errTeamApplyOnly)
			}
			teamDoc = &nodes[i]
		}
	}
	resources := f.resources
	if resources == nil {
		previous := f.teamBlueprintRefs(team)
		for i := range nodes {
			kind, name := docHeader(&nodes[i])
			if f.applyErr == nil && kind != "Team" {
				f.persist(kind, &nodes[i], team)
			}
			resources = append(resources, kukeonv1.ApplyResourceResult{Kind: kind, Name: name, Action: "created"})
		}
		if f.prune && f.applyErr == nil {
			applied := map[string]bool{}
			for _, r := range resources {
				applied[r.Kind+"/"+r.Name] = true
			}
			kept := f.configs[:0]
			for _, cfg := range f.configs {
				if cfg.Metadata.Labels[api.LabelTeam] != team || applied["CellConfig/"+cfg.Metadata.Name] {
					kept = append(kept, cfg)
				}
			}
			f.configs = kept
			resources = append(resources, f.pruneShared(previous, applied)...)
		}
	}
	failed := slices.ContainsFunc(resources, func(r kukeonv1.ApplyResourceResult) bool {
		return r.Action == "failed" || r.Error != ""
	})
	if teamDoc != nil && f.applyErr == nil && !failed {
		stored, err := yaml.Marshal(teamDoc)
		if err != nil {
			panic(err)
		}
		f.teams[team] = stored
	}
	return kukeonv1.ApplyDocumentsResult{Resources: resources}, f.applyErr
}

// errTeamApplyOnly stands in for kukeond's rejection of a Team document
// outside its own team apply.
var errTeamApplyOnly = errors.New("a Team document is applied only with kuke apply -f or kuke team apply -f")

// ApplyDocuments is the plain apply: it stores a TeamDefaults and rejects a
// Team document, as kukeond does.
func (f *fakeClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	f.writes = append(f.writes, "apply")
	f.plain = append(f.plain, raw)
	nodes := decodeStream(raw)
	var resources []kukeonv1.ApplyResourceResult
	for i := range nodes {
		if kind, _ := docHeader(&nodes[i]); kind == "Team" {
			return kukeonv1.ApplyDocumentsResult{}, errTeamApplyOnly
		}
	}
	for i := range nodes {
		kind, name := docHeader(&nodes[i])
		if f.applyErr == nil {
			f.persist(kind, &nodes[i], "")
		}
		resources = append(resources, kukeonv1.ApplyResourceResult{Kind: kind, Name: name, Action: "created"})
	}
	return kukeonv1.ApplyDocumentsResult{Resources: resources}, f.applyErr
}

func decodeStream(raw []byte) []yaml.Node {
	var nodes []yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); errors.Is(err, io.EOF) {
			return nodes
		} else if err != nil {
			panic(err)
		}
		nodes = append(nodes, node)
	}
}

func docHeader(node *yaml.Node) (string, string) {
	var doc struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	if err := node.Decode(&doc); err != nil {
		panic(err)
	}
	return doc.Kind, doc.Metadata.Name
}

func (f *fakeClient) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	if f.teamErr != nil {
		return kukeonv1.GetTeamResult{}, f.teamErr
	}
	raw, ok := f.teams[ref.Name]
	if !ok {
		return kukeonv1.GetTeamResult{}, nil
	}
	return kukeonv1.GetTeamResult{
		Exists: true, Document: raw, Summary: kukeonv1.TeamSummary{Name: ref.Name, Realm: "default"},
	}, nil
}

func (f *fakeClient) ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error) {
	names := slices.Sorted(maps.Keys(f.teams))
	out := make([]kukeonv1.GetTeamResult, 0, len(names))
	for _, name := range names {
		out = append(out, kukeonv1.GetTeamResult{
			Exists: true, Document: f.teams[name], Summary: kukeonv1.TeamSummary{Name: name, Realm: "default"},
		})
	}
	return out, nil
}

func (f *fakeClient) GetTeamDefaults(_ context.Context, realm string) (kukeonv1.GetTeamDefaultsResult, error) {
	if f.defaults == nil {
		return kukeonv1.GetTeamDefaultsResult{Realm: realm}, nil
	}
	return kukeonv1.GetTeamDefaultsResult{Exists: true, Realm: realm, Document: f.defaults}, nil
}

// teamBlueprintRefs lists the realm/name of each blueprint team's stored
// configs reference.
func (f *fakeClient) teamBlueprintRefs(team string) []string {
	var out []string
	for _, cfg := range f.configs {
		if team == "" || cfg.Metadata.Labels[api.LabelTeam] == team {
			ref := cfg.Spec.Blueprint
			out = append(out, ref.Realm+"/"+ref.Name)
		}
	}
	return out
}

// pruneShared removes the shared blueprints the team referenced before the
// apply, did not apply, and no stored config references any more, as
// kukeond's per-team prune does.
func (f *fakeClient) pruneShared(previous []string, applied map[string]bool) []kukeonv1.ApplyResourceResult {
	inUse := f.teamBlueprintRefs("")
	var out []kukeonv1.ApplyResourceResult
	for _, key := range previous {
		stored, ok := f.blueprints[key]
		if !ok || stored.Metadata.Labels[api.LabelShared] != "true" ||
			applied["CellBlueprint/"+stored.Metadata.Name] || slices.Contains(inUse, key) {
			continue
		}
		delete(f.blueprints, key)
		out = append(out, kukeonv1.ApplyResourceResult{
			Kind: "CellBlueprint", Name: stored.Metadata.Name, Action: "pruned",
			Details: map[string]string{"realm": stored.Metadata.Realm},
		})
	}
	return out
}

func composition(t *testing.T, harness string, bindings []config.SecretBinding) *compose.Result {
	t.Helper()
	return compositionFor(t, harness, bindings, inheritingTeam("root"))
}

func compositionFor(t *testing.T, harness string, bindings []config.SecretBinding, team *config.Team) *compose.Result {
	t.Helper()
	cfg := &config.TeamDefaults{APIVersion: config.APIVersion, Kind: config.KindTeamDefaults, Spec: config.Spec{
		Defaults: config.Defaults{
			Sources: []config.Source{
				{
					Name:      "kukeon",
					URL:       "github.com/eminwux/kukeon",
					Branch:    "main",
					ClonePath: "/home/user/kukeon-src",
				},
			},
			Roles: []config.Role{{Name: "majordomo", Source: "kukeon", Path: "team/roles/majordomo"}},
			Harnesses: []config.Harness{
				{Name: harness, Source: "kukeon", Path: "team/harnesses/" + harness},
			},
		}, Secrets: bindings,
	}}
	result, err := compose.Compose(
		cfg,
		team,
		compose.Options{Sources: sourceFS()},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBaseMajordomoHasNoSecretPrerequisite(t *testing.T) {
	f := newFake()
	if _, err := applyMissing(t.Context(), f, composition(t, "codex", nil)); err != nil {
		t.Fatal(err)
	}
	if f.lookups != 0 || f.creates != 0 || f.applies != 1 || f.team != "root" {
		t.Fatalf("unexpected calls: %+v", f)
	}
	if strings.Contains(string(f.raw), "gh-token") || strings.Contains(string(f.raw), "kind: Cell\n") {
		t.Fatal("unexpected credential or cell resource")
	}
}

func binding(source *config.SecretSource) config.SecretBinding {
	return config.SecretBinding{
		Name:      "openrouter-api-key",
		EnvName:   "OPENROUTER_API_KEY",
		SecretRef: config.SecretRef{Name: "provider-account", Realm: "auth", Space: "shared", Stack: "operators"},
		Source:    source,
	}
}

func metadata(ref config.SecretRef) api.SecretMetadata {
	return api.SecretMetadata{Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell}
}

func TestSecretReuseCreationAndRerun(t *testing.T) {
	for _, mode := range []string{"existing", "env", "file", "concurrent"} {
		t.Run(mode, func(t *testing.T) {
			const value = "credential-must-never-appear"
			t.Setenv("INIT_TEST_TOKEN", value)
			b := binding(&config.SecretSource{Env: "INIT_TEST_TOKEN"})
			f := newFake()
			if mode == "existing" {
				f.secrets[metadata(b.SecretRef)] = "keep-old"
				b.Source = &config.SecretSource{File: "/does/not/exist"}
			}
			if mode == "file" {
				path := filepath.Join(t.TempDir(), "token")
				if err := os.WriteFile(path, []byte(value+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				b.Source = &config.SecretSource{File: path}
			}
			if mode == "concurrent" {
				f.competeAt = 2
			}
			result := composition(t, "opencode", []config.SecretBinding{b})
			// Duplicate requirements across containers must create the identity once.
			result.Pairs[0].Secrets = append(result.Pairs[0].Secrets, result.Pairs[0].Secrets[0])
			for range 2 {
				if _, err := applyMissing(t.Context(), f, result); err != nil {
					t.Fatal(err)
				}
			}
			wantCreates := 1
			want := value
			if mode == "existing" {
				wantCreates = 0
				want = "keep-old"
			}
			if mode == "concurrent" {
				wantCreates = 0
				want = "concurrent-value"
			}
			if f.creates != wantCreates || f.secrets[metadata(b.SecretRef)] != want || f.applies != 2 {
				t.Fatalf(
					"creates=%d value changed=%v applies=%d",
					f.creates,
					f.secrets[metadata(b.SecretRef)] != want,
					f.applies,
				)
			}
			// The stored Team records where the value comes from, never the
			// value; the composed documents carry neither.
			if strings.Contains(string(f.raw), value) ||
				strings.Contains(composedStream(t, f.raw), "INIT_TEST_TOKEN") ||
				strings.Contains(string(f.raw), "kind: Secret") {
				t.Fatal("credential/source leaked into apply stream")
			}
		})
	}
}

func TestMissingSecretWarnsAndStillApplies(t *testing.T) {
	b := binding(nil)
	f := newFake()
	result := composition(t, "opencode", []config.SecretBinding{b})
	// The same runtime identity required twice is reported once.
	result.Pairs[0].Secrets = append(result.Pairs[0].Secrets, result.Pairs[0].Secrets[0])
	missing, err := applyMissing(t.Context(), f, result)
	want := "provider-account  →  kuke create secret provider-account --realm auth --space shared --stack operators --from-file /path/to/token"
	if err != nil || len(missing) != 1 || missing[0] != want || f.applies != 1 || f.creates != 0 {
		t.Fatalf("missing=%q err=%v creates=%d applies=%d", missing, err, f.creates, f.applies)
	}
}

func TestFailuresNeverApplyDependentResourcesOrLeakCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		setup      func(*testing.T, *fakeClient, *config.SecretBinding)
	}{
		{"lookup-permission", "daemon rejected", func(_ *testing.T, f *fakeClient, _ *config.SecretBinding) {
			f.lookupErr = errors.New("permission denied: sensitive-value")
		}},
		{"lookup-transport", "transport", func(_ *testing.T, f *fakeClient, _ *config.SecretBinding) { f.lookupErr = io.EOF }},
		{"create", "not retried", func(_ *testing.T, f *fakeClient, _ *config.SecretBinding) {
			f.createErr = errors.New("sensitive-value")
		}},
		{"missing-env", "missing or empty", func(t *testing.T, _ *fakeClient, _ *config.SecretBinding) { t.Setenv("INIT_TEST_TOKEN", "") }},
		{"missing-file", "cannot read", func(_ *testing.T, _ *fakeClient, b *config.SecretBinding) {
			b.Source = &config.SecretSource{File: "/absent/token"}
		}},
		{"missing-parent", "required stack", func(_ *testing.T, f *fakeClient, _ *config.SecretBinding) { f.missingStack = true }},
		{"ping", "connection/preflight", func(_ *testing.T, f *fakeClient, _ *config.SecretBinding) { f.pingErr = io.EOF }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("INIT_TEST_TOKEN", "sensitive-value")
			f := newFake()
			b := binding(&config.SecretSource{Env: "INIT_TEST_TOKEN"})
			tc.setup(t, f, &b)
			_, err := applyMissing(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b}))
			if err == nil || !strings.Contains(err.Error(), tc.want) ||
				strings.Contains(err.Error(), "sensitive-value") ||
				f.applies != 0 {
				t.Fatalf("err=%v applies=%d", err, f.applies)
			}
			if tc.name == "create" && f.creates != 1 {
				t.Fatal("create was retried")
			}
			if tc.name != "create" && f.creates != 0 {
				t.Fatal("preflight mutated daemon")
			}
		})
	}
}

func TestCreateWriteFailureThenRerunReusesSecret(t *testing.T) {
	t.Setenv("INIT_TEST_TOKEN", "first-value")
	f := newFake()
	f.createErr = io.EOF
	b := binding(&config.SecretSource{Env: "INIT_TEST_TOKEN"})
	result := composition(t, "opencode", []config.SecretBinding{b})
	if _, err := applyMissing(t.Context(), f, result); err == nil {
		t.Fatal("expected error")
	}
	f.createErr = nil
	t.Setenv("INIT_TEST_TOKEN", "replacement-value")
	if _, err := applyMissing(t.Context(), f, result); err != nil {
		t.Fatal(err)
	}
	if f.creates != 1 || f.secrets[metadata(b.SecretRef)] != "first-value" {
		t.Fatal("rerun rotated an accepted secret")
	}
}

func TestAllSecretsPreflightBeforeAnyCreate(t *testing.T) {
	t.Setenv("INIT_TEST_TOKEN", "value")
	f := newFake()
	result := composition(t, "opencode", []config.SecretBinding{binding(&config.SecretSource{Env: "INIT_TEST_TOKEN"})})
	result.Pairs[0].Secrets = append(
		result.Pairs[0].Secrets,
		compose.SecretRequirement{Reference: config.SecretRef{Name: "other", Realm: "auth"}},
	)
	result.Pairs[0].Secrets[1].Source = &config.SecretSource{Env: "INIT_TEST_EMPTY"}
	t.Setenv("INIT_TEST_EMPTY", "")
	if _, err := applyMissing(t.Context(), f, result); err == nil || f.creates != 0 || f.applies != 0 {
		t.Fatalf("preflight did not gate writes: %v", err)
	}
}

func TestApplyOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		resources []kukeonv1.ApplyResourceResult
		err       error
		want      string
	}{
		{name: "rpc", err: io.EOF, want: "apply RPC failed"},
		{name: "total", resources: []kukeonv1.ApplyResourceResult{{Action: "failed", Error: "invalid blueprint"}, {Action: "failed", Error: "missing config"}}, want: "0 accepted, 2 failures"},
		{name: "partial", resources: []kukeonv1.ApplyResourceResult{{Action: "created"}, {Action: "failed", Error: "bad config"}}, want: "1 accepted, 1 failures"},
		{name: "missing-replies", resources: []kukeonv1.ApplyResourceResult{}, want: "incomplete apply response"},
		{name: "pruned", resources: []kukeonv1.ApplyResourceResult{{Action: "updated"}, {Action: "unchanged"}, {Action: "pruned"}, {Kind: "Team", Action: "created"}}},
		{name: "prune-failure", resources: []kukeonv1.ApplyResourceResult{{Action: "updated"}, {Action: "unchanged"}, {Action: "failed", Error: "prune denied"}}, want: "prune denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.resources, f.applyErr = tc.resources, tc.err
			_, err := applyMissing(t.Context(), f, composition(t, "codex", nil))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want=%s", err, tc.want)
			}
		})
	}
}

func TestSecretErrorsClassifyNetworkAndShutdown(t *testing.T) {
	for _, err := range []error{&net.OpError{Op: "read", Net: "unix", Err: io.EOF}, rpc.ErrShutdown, context.Canceled} {
		got := requestError("lookup", "secret token", err)
		if !strings.Contains(got.Error(), "transport") || !errors.Is(got, errdefs.ErrDaemonTransport) {
			t.Fatal("transport not identified")
		}
	}
	// An outdated daemon keeps its sentinel; any other remote error is
	// reported by class only, its text dropped.
	outdated := requestError("lookup", "team kuke", fmt.Errorf("x: %w", errdefs.ErrDaemonMethodUnsupported))
	if !errors.Is(outdated, errdefs.ErrDaemonMethodUnsupported) {
		t.Fatalf("outdated daemon not identified: %v", outdated)
	}
	rejected := requestError("lookup", "team kuke", errors.New("remote says SECRET"))
	if strings.Contains(rejected.Error(), "SECRET") || errors.Is(rejected, errdefs.ErrDaemonTransport) {
		t.Fatalf("rejected request kept remote text or misclassified: %v", rejected)
	}
}

func TestSecretScopesRemainExplicit(t *testing.T) {
	for _, ref := range []config.SecretRef{
		{Name: "provider", Realm: "auth"},
		{Name: "provider", Realm: "auth", Space: "shared"},
		{Name: "provider", Realm: "auth", Space: "shared", Stack: "operators", Cell: "parent"},
	} {
		t.Run(fmt.Sprintf("%+v", ref), func(t *testing.T) {
			f := newFake()
			b := binding(nil)
			b.SecretRef = ref
			missing, err := applyMissing(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b}))
			if err != nil || f.creates != 0 || len(missing) != 1 {
				t.Fatalf("missing=%v err=%v creates=%d", missing, err, f.creates)
			}
			if ref.Cell != "" {
				if !strings.Contains(missing[0], "no --cell flag") {
					t.Fatal(missing[0])
				}
			} else {
				if !strings.Contains(missing[0], "--realm auth") {
					t.Fatal(missing[0])
				}
				if ref.Stack == "" && strings.Contains(missing[0], "--stack") {
					t.Fatal("invented stack scope")
				}
			}
			f.secrets[metadata(ref)] = "preserved"
			if missing, err = applyMissing(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b})); err != nil {
				t.Fatal(err)
			}
			if f.creates != 0 || len(missing) != 0 {
				t.Fatal("rotated scoped credential or kept reporting it missing")
			}
		})
	}
}

// composedStream is an apply stream without its Team document: the documents
// composed from the declaration.
func composedStream(t *testing.T, raw []byte) string {
	t.Helper()
	var out []string
	for _, node := range decodeStream(raw) {
		if kind, _ := docHeader(&node); kind == "Team" {
			continue
		}
		encoded, err := yaml.Marshal(&node)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(encoded))
	}
	return strings.Join(out, "---\n")
}

// realmDocument returns the Realm document of an apply stream, if it has one.
func realmDocument(t *testing.T, raw []byte) (api.RealmDoc, bool) {
	t.Helper()
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); errors.Is(err, io.EOF) {
			return api.RealmDoc{}, false
		} else if err != nil {
			t.Fatal(err)
		}
		var doc api.RealmDoc
		if err := node.Decode(&doc); err != nil {
			t.Fatal(err)
		}
		if doc.Kind == api.KindRealm {
			return doc, true
		}
	}
}

// TestDefaultIdentityIsWrittenOnce is the application side of issue #1548: a
// slot the team's Configs leave to the scope defaults gets a Realm default the
// first time, and never again — so the default the operator changes later is
// the one every Config inherits, with no re-render.
func TestDefaultIdentityIsWrittenOnce(t *testing.T) {
	const slot = "openrouter-api-key"
	result := composition(t, "opencode", nil)
	if len(result.DefaultSecrets) != 1 || result.DefaultSecrets[0].Slot != slot {
		t.Fatalf("precondition: default secrets = %+v", result.DefaultSecrets)
	}
	if fill, filled := result.Pairs[0].Config.Spec.Secrets[slot]; filled {
		t.Fatalf("precondition: the Config fills the slot itself: %+v", fill)
	}

	f := newFake()
	// What the Realm already carries must survive the apply that adds a default.
	f.realmCreds = []api.RegistryCredentials{{ServerAddress: "ghcr.io", Username: "bot", Password: "inline"}}
	f.realmDefaults = &api.ScopeDefaults{
		Values: map[string]string{"REGION": "eu"},
		Secrets: map[string]api.CellConfigSecretFill{
			"other": {SecretRef: &api.ContainerSecretRef{Name: "o", Realm: "default"}},
		},
	}
	missing, err := applyMissing(t.Context(), f, result)
	if err != nil {
		t.Fatal(err)
	}
	realm, written := realmDocument(t, f.raw)
	if !written {
		t.Fatalf("no Realm document in the first apply:\n%s", f.raw)
	}
	fill := realm.Spec.Defaults.Secrets[slot]
	if realm.Metadata.Name != "default" || fill.SecretRef == nil || fill.SecretRef.Name != slot ||
		fill.SecretRef.Realm != "default" {
		t.Fatalf("realm default = %+v", realm.Spec.Defaults)
	}
	if realm.Spec.Defaults.Values["REGION"] != "eu" || realm.Spec.Defaults.Secrets["other"].SecretRef.Name != "o" ||
		realm.Spec.Namespace != "default.kukeon.io" || len(realm.Spec.RegistryCredentials) != 1 ||
		realm.Spec.RegistryCredentials[0].Password != "inline" {
		t.Fatalf("the Realm's existing spec was not carried: %+v", realm.Spec)
	}
	// The Secret behind the new default does not exist yet: say how to create it.
	if len(missing) != 1 || !strings.Contains(missing[0], "kuke create secret "+slot+" --realm default") {
		t.Fatalf("missing = %q", missing)
	}

	// Applied: the next run finds the default and leaves the Realm alone. The
	// default is the one this team wrote, so its Secret is still the team's:
	// still missing, it is still reported.
	if missing, err = applyMissing(t.Context(), f, result); err != nil {
		t.Fatal(err)
	}
	if _, written = realmDocument(t, f.raw); written {
		t.Fatalf("the Realm default was written a second time:\n%s", f.raw)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "kuke create secret "+slot+" --realm default") {
		t.Fatalf("second run: missing = %q, want the team's own default Secret reported", missing)
	}

	// The operator points the default at another identity. Re-running keeps
	// it, and no longer asks for the Secret the first default named.
	f.realmDefaults.Secrets[slot] = api.CellConfigSecretFill{
		SecretRef: &api.ContainerSecretRef{Name: "work-account", Realm: "default"},
	}
	if missing, err = applyMissing(t.Context(), f, result); err != nil {
		t.Fatal(err)
	}
	if _, written = realmDocument(t, f.raw); written || len(missing) != 0 {
		t.Fatalf("an operator's default was not respected: written=%v missing=%q", written, missing)
	}
	if f.realmDefaults.Secrets[slot].SecretRef.Name != "work-account" {
		t.Fatalf("realm default = %+v", f.realmDefaults.Secrets[slot])
	}
	// The Config still has no fill: it inherits whatever the default is.
	if composed := composedStream(t, f.raw); strings.Contains(composed, "work-account") ||
		strings.Contains(composed, "secretRef:") {
		t.Fatalf("an identity was baked into the Configs:\n%s", f.raw)
	}

	// A default at a deeper level counts too: nothing is added to the Realm.
	g := newFake()
	g.stackDefaults = &api.ScopeDefaults{Secrets: map[string]api.CellConfigSecretFill{
		slot: {SecretRef: &api.ContainerSecretRef{Name: "stack-account", Realm: "default"}},
	}}
	if missing, err = applyMissing(t.Context(), g, result); err != nil {
		t.Fatal(err)
	}
	if _, written = realmDocument(t, g.raw); written || len(missing) != 0 || g.realmDefaults != nil {
		t.Fatalf("a Stack default did not stop the Realm default: written=%v missing=%q", written, missing)
	}
}

// TestMissingGitIdentityHint: a missing git.identityRef Secret — on a Config or
// on one of its repo fills — is reported with the `--connector git` command
// that creates a tagged identity the render accepts; a missing credential named
// directly keeps the plain ssh-key form.
func TestMissingGitIdentityHint(t *testing.T) {
	identity := &api.ContainerSecretRef{Name: "bot-key", Realm: "default", Stack: "team"}
	repoIdentity := &api.ContainerSecretRef{Name: "repo-key", Realm: "default"}
	plainKey := &api.ContainerSecretRef{Name: "plain-key", Realm: "default"}
	result := &compose.Result{
		Pairs: []compose.Pair{{Config: &api.CellConfigDoc{Spec: api.CellConfigSpec{
			Git: &api.ContainerGit{IdentityRef: identity},
			Repos: map[string]api.CellConfigRepoFill{
				"project": {Git: &api.RepoGit{IdentityRef: repoIdentity}},
				"other":   {Git: &api.RepoGit{Credential: &api.GitCredential{SecretRef: plainKey}}},
			},
		}}}},
	}
	missing, err := missingGitSecrets(t.Context(), newFake(), result)
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]string{}
	for _, line := range missing {
		name, _, _ := strings.Cut(line, "  →  ")
		lines[name] = line
	}
	for name, want := range map[string]string{
		"bot-key":  "kuke create secret bot-key --realm default --stack team --connector git --type ssh-key",
		"repo-key": "kuke create secret repo-key --realm default --connector git --type ssh-key",
	} {
		if !strings.Contains(lines[name], want) || !strings.Contains(lines[name], "--hosts ") {
			t.Errorf("%s hint = %q, want it to contain %q and --hosts", name, lines[name], want)
		}
	}
	plain := lines["plain-key"]
	if !strings.Contains(plain, "kuke create secret plain-key --realm default --type ssh-key") ||
		strings.Contains(plain, "--connector") {
		t.Errorf("plain-key hint = %q, want the untagged credential form", plain)
	}
	if len(missing) != 3 {
		t.Errorf("missing = %q, want three lines", missing)
	}
}

// TestNoGitIdentityHint is AC13: a team that clones over SSH, with no git
// identity named by its Configs or by the Realm, Space or Stack defaults, is
// told once how to store one; a team that has one, or clones nothing over SSH,
// is told nothing.
func TestNoGitIdentityHint(t *testing.T) {
	identity := &api.ContainerSecretRef{Name: "git-ssh", Realm: "default"}
	gitDefault := &api.ScopeDefaults{Git: &api.ContainerGit{IdentityRef: identity}}
	for _, tc := range []struct {
		name   string
		ssh    bool
		setup  func(*fakeClient, *compose.Result)
		hinted bool
	}{
		{"ssh, no identity", true, func(*fakeClient, *compose.Result) {}, true},
		{"no ssh source", false, func(*fakeClient, *compose.Result) {}, false},
		{"realm default", true, func(f *fakeClient, _ *compose.Result) { f.realmDefaults = gitDefault }, false},
		{"space default", true, func(f *fakeClient, _ *compose.Result) {
			f.spaceDefaults = &api.SpaceDefaults{Git: gitDefault.Git}
		}, false},
		{"stack default", true, func(f *fakeClient, _ *compose.Result) { f.stackDefaults = gitDefault }, false},
		{"team identity", true, func(_ *fakeClient, r *compose.Result) {
			r.Pairs[0].Config.Spec.Git = &api.ContainerGit{IdentityRef: identity}
		}, false},
		// A default that sets other git fields but names no identity is none.
		{"realm git without identity", true, func(f *fakeClient, _ *compose.Result) {
			f.realmDefaults = &api.ScopeDefaults{Git: &api.ContainerGit{Author: &api.GitIdentity{Name: "A", Email: "a@x"}}}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := composition(t, "codex", nil)
			if tc.ssh {
				result.SSHSources = []string{"agents"}
			}
			f := newFake()
			tc.setup(f, result)
			out, err := apply(t.Context(), f, result)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, note := range out.notes {
				if note == "no git identity is set; run kuke connector import git --default" {
					count++
				}
			}
			if want := map[bool]int{true: 1, false: 0}[tc.hinted]; count != want {
				t.Fatalf("hint printed %d times, want %d: %v", count, want, out.notes)
			}
		})
	}
}
