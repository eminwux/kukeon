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
	"net"
	"net/rpc"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	stackLabels   map[string]string
	blueprintErr  error
	// created records every secret document sent to CreateSecret.
	created []api.SecretDoc
}

func newFake() *fakeClient {
	return &fakeClient{secrets: map[api.SecretMetadata]string{}, blueprints: map[string]api.CellBlueprintDoc{}}
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
	}
}
func (f *fakeClient) Close() error               { return nil }
func (f *fakeClient) Ping(context.Context) error { return f.pingErr }
func (f *fakeClient) ListRealms(context.Context) ([]api.RealmDoc, error) {
	if f.missingRealm {
		return nil, nil
	}
	return []api.RealmDoc{
		{Metadata: api.RealmMetadata{Name: "default"}},
		{Metadata: api.RealmMetadata{Name: "auth"}},
	}, nil
}

func (f *fakeClient) ListSpaces(context.Context, string) ([]api.SpaceDoc, error) {
	if f.missingSpace {
		return nil, nil
	}
	return []api.SpaceDoc{
		{Metadata: api.SpaceMetadata{Name: "default"}},
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
	f.applies++
	f.raw = raw
	f.team = team
	if f.resources == nil {
		var resources []kukeonv1.ApplyResourceResult
		decoder := yaml.NewDecoder(bytes.NewReader(raw))
		for {
			var node yaml.Node
			if err := decoder.Decode(&node); errors.Is(err, io.EOF) {
				break
			} else if err != nil {
				panic(err)
			}
			var doc struct {
				Kind     string `yaml:"kind"`
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
			}
			if err := node.Decode(&doc); err != nil {
				panic(err)
			}
			if f.applyErr == nil {
				f.persist(doc.Kind, &node, team)
			}
			resources = append(
				resources,
				kukeonv1.ApplyResourceResult{Kind: doc.Kind, Name: doc.Metadata.Name, Action: "created"},
			)
		}
		return kukeonv1.ApplyDocumentsResult{Resources: resources}, f.applyErr
	}
	return kukeonv1.ApplyDocumentsResult{Resources: f.resources}, f.applyErr
}

func composition(t *testing.T, harness string, bindings []config.SecretBinding) *compose.Result {
	t.Helper()
	return compositionFor(t, harness, bindings, config.Root())
}

func compositionFor(t *testing.T, harness string, bindings []config.SecretBinding, team *config.Team) *compose.Result {
	t.Helper()
	cfg := &config.TeamsConfig{APIVersion: config.APIVersion, Kind: config.Kind, Spec: config.Spec{
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
	if _, err := Apply(t.Context(), f, composition(t, "codex", nil)); err != nil {
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
				if _, err := Apply(t.Context(), f, result); err != nil {
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
			if strings.Contains(string(f.raw), value) || strings.Contains(string(f.raw), "INIT_TEST_TOKEN") ||
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
	missing, err := Apply(t.Context(), f, result)
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
			_, err := Apply(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b}))
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
	if _, err := Apply(t.Context(), f, result); err == nil {
		t.Fatal("expected error")
	}
	f.createErr = nil
	t.Setenv("INIT_TEST_TOKEN", "replacement-value")
	if _, err := Apply(t.Context(), f, result); err != nil {
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
	if _, err := Apply(t.Context(), f, result); err == nil || f.creates != 0 || f.applies != 0 {
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
		{name: "pruned", resources: []kukeonv1.ApplyResourceResult{{Action: "updated"}, {Action: "unchanged"}, {Action: "pruned"}}},
		{name: "prune-failure", resources: []kukeonv1.ApplyResourceResult{{Action: "updated"}, {Action: "unchanged"}, {Action: "failed", Error: "prune denied"}}, want: "prune denied"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFake()
			f.resources, f.applyErr = tc.resources, tc.err
			_, err := Apply(t.Context(), f, composition(t, "codex", nil))
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
		if !strings.Contains(requestError("lookup", "secret token", err).Error(), "transport") {
			t.Fatal("transport not identified")
		}
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
			missing, err := Apply(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b}))
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
			if missing, err = Apply(t.Context(), f, composition(t, "opencode", []config.SecretBinding{b})); err != nil {
				t.Fatal(err)
			}
			if f.creates != 0 || len(missing) != 0 {
				t.Fatal("rotated scoped credential or kept reporting it missing")
			}
		})
	}
}
