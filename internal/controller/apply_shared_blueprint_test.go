//go:build !integration

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

package controller_test

import (
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// teamStore is a stateful stand-in for the daemon's blueprint/config storage,
// so a team apply's prune pass sees what the apply loop just wrote and deleted.
type teamStore struct {
	t          *testing.T
	blueprints map[string]intmodel.CellBlueprint
	configs    map[string]intmodel.CellConfig
	deleted    []string
	// unreadable names configs whose document cannot be read.
	unreadable map[string]bool
}

func newTeamStore(t *testing.T) *teamStore {
	return &teamStore{
		t: t, blueprints: map[string]intmodel.CellBlueprint{}, configs: map[string]intmodel.CellConfig{},
		unreadable: map[string]bool{},
	}
}

func blueprintStoreKey(md intmodel.CellBlueprintMetadata) string {
	return strings.Join([]string{md.Realm, md.Space, md.Stack, md.Name}, "/")
}

func configStoreKey(md intmodel.CellConfigMetadata) string {
	return strings.Join([]string{md.Realm, md.Space, md.Stack, md.Name}, "/")
}

func (s *teamStore) runner() *fakeRunner {
	return &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) { return r, nil },
		GetSpaceFn: func(sp intmodel.Space) (intmodel.Space, error) { return sp, nil },
		GetStackFn: func(st intmodel.Stack) (intmodel.Stack, error) { return st, nil },
		WriteBlueprintFn: func(bp intmodel.CellBlueprint) (bool, error) {
			_, existed := s.blueprints[blueprintStoreKey(bp.Metadata)]
			s.blueprints[blueprintStoreKey(bp.Metadata)] = bp
			return !existed, nil
		},
		GetBlueprintFn: func(bp intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
			stored, ok := s.blueprints[blueprintStoreKey(bp.Metadata)]
			if !ok {
				return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
			}
			return stored, nil
		},
		// Listings are metadata-only, as the storage runner's are.
		ListBlueprintsFn: func(_, _, _ string) ([]intmodel.CellBlueprint, error) {
			out := make([]intmodel.CellBlueprint, 0, len(s.blueprints))
			for _, bp := range s.blueprints {
				out = append(out, intmodel.CellBlueprint{Metadata: bp.Metadata})
			}
			return out, nil
		},
		DeleteBlueprintFn: func(bp intmodel.CellBlueprint) error {
			delete(s.blueprints, blueprintStoreKey(bp.Metadata))
			s.deleted = append(s.deleted, "CellBlueprint/"+bp.Metadata.Name)
			return nil
		},
		WriteConfigFn: func(cfg intmodel.CellConfig) (bool, error) {
			_, existed := s.configs[configStoreKey(cfg.Metadata)]
			s.configs[configStoreKey(cfg.Metadata)] = cfg
			return !existed, nil
		},
		GetConfigFn: func(cfg intmodel.CellConfig) (intmodel.CellConfig, error) {
			if s.unreadable[cfg.Metadata.Name] {
				return intmodel.CellConfig{}, errors.New("i/o error")
			}
			stored, ok := s.configs[configStoreKey(cfg.Metadata)]
			if !ok {
				return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
			}
			return stored, nil
		},
		ListConfigsFn: func(_, _, _ string) ([]intmodel.CellConfig, error) {
			out := make([]intmodel.CellConfig, 0, len(s.configs))
			for _, cfg := range s.configs {
				out = append(out, intmodel.CellConfig{Metadata: cfg.Metadata})
			}
			return out, nil
		},
		DeleteConfigFn: func(cfg intmodel.CellConfig) error {
			delete(s.configs, configStoreKey(cfg.Metadata))
			s.deleted = append(s.deleted, "CellConfig/"+cfg.Metadata.Name)
			return nil
		},
	}
}

// sharedBlueprint is a realm-wide blueprint as `kuke team` renders it: marked
// shared, carrying its content digest, and owned by no team.
func sharedBlueprint(name string) v1beta1.CellBlueprintDoc {
	doc := *blueprintDoc(0, name, "default").CellBlueprintDoc
	doc.Metadata.Labels = map[string]string{
		v1beta1.LabelShared: "true", v1beta1.LabelContentDigest: "0123456789abcdef",
	}
	return doc
}

func teamConfig(name, team string, ref v1beta1.CellConfigBlueprintRef) v1beta1.CellConfigDoc {
	var labels map[string]string
	if team != "" {
		labels = map[string]string{v1beta1.LabelTeam: team}
	}
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name: name, Realm: "default", Space: "default", Stack: "default", Labels: labels,
		},
		Spec: v1beta1.CellConfigSpec{Blueprint: ref},
	}
}

func sharedRef(name string) v1beta1.CellConfigBlueprintRef {
	return v1beta1.CellConfigBlueprintRef{Name: name, Realm: "default"}
}

// storedConfigCarrier is a stored CellConfig, with its document, referencing a
// realm-wide blueprint.
func storedConfigCarrier(t *testing.T, name, team, blueprint string) intmodel.CellConfig {
	t.Helper()
	carrier, _, err := apischeme.NormalizeCellConfig(teamConfig(name, team, sharedRef(blueprint)))
	if err != nil {
		t.Fatal(err)
	}
	return carrier
}

func (s *teamStore) storeBlueprint(doc v1beta1.CellBlueprintDoc) {
	s.t.Helper()
	carrier, _, err := apischeme.NormalizeCellBlueprint(doc)
	if err != nil {
		s.t.Fatal(err)
	}
	s.blueprints[blueprintStoreKey(carrier.Metadata)] = carrier
}

func (s *teamStore) storeConfig(doc v1beta1.CellConfigDoc) {
	s.t.Helper()
	carrier, _, err := apischeme.NormalizeCellConfig(doc)
	if err != nil {
		s.t.Fatal(err)
	}
	s.configs[configStoreKey(carrier.Metadata)] = carrier
}

func (s *teamStore) blueprintNames() []string {
	var out []string
	for _, bp := range s.blueprints {
		out = append(out, bp.Metadata.Name)
	}
	sort.Strings(out)
	return out
}

func blueprintDocument(doc v1beta1.CellBlueprintDoc) parser.Document {
	return parser.Document{
		Kind: v1beta1.KindCellBlueprint, APIVersion: v1beta1.APIVersionV1Beta1, CellBlueprintDoc: &doc,
	}
}

func configDocument(doc v1beta1.CellConfigDoc) parser.Document {
	return parser.Document{
		Kind: v1beta1.KindCellConfig, APIVersion: v1beta1.APIVersionV1Beta1, CellConfigDoc: &doc,
	}
}

func prunedResources(result controller.ApplyResult) []string {
	var out []string
	for _, r := range result.Resources {
		if r.Action == "pruned" {
			out = append(out, r.Kind+"/"+r.Name)
		}
	}
	sort.Strings(out)
	return out
}

func failedResources(result controller.ApplyResult) []string {
	var out []string
	for _, r := range result.Resources {
		if r.Action == "failed" {
			out = append(out, r.Kind+"/"+r.Name+": "+r.Error.Error())
		}
	}
	return out
}

// TestApplyDocuments_SharedBlueprintIsNotTeamStamped: a shared blueprint is a
// realm-wide form every team may reference, so a team apply must not claim it
// with `kukeon.io/team` — the label that scopes the per-team prune. Team-owned
// documents in the same apply are still stamped.
func TestApplyDocuments_SharedBlueprintIsNotTeamStamped(t *testing.T) {
	store := newTeamStore(t)
	exec := setupTestController(t, store.runner())
	owned := *blueprintDoc(0, "alpha-private", "default").CellBlueprintDoc
	// A team apply strips nothing the composer set, and adds no team label.
	unshared := sharedBlueprint("not-really-shared")
	unshared.Metadata.Labels[v1beta1.LabelShared] = "false"

	result, err := exec.ApplyDocuments([]parser.Document{
		blueprintDocument(sharedBlueprint("codex")),
		blueprintDocument(owned),
		blueprintDocument(unshared),
		configDocument(teamConfig("alpha-dev-codex", "", sharedRef("codex"))),
	}, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if failed := failedResources(result); len(failed) != 0 {
		t.Fatalf("failed resources: %v", failed)
	}
	codex := store.blueprints["default///codex"]
	if _, stamped := codex.Metadata.Labels[v1beta1.LabelTeam]; stamped ||
		strings.Contains(string(codex.Document), v1beta1.LabelTeam) {
		t.Fatalf("shared blueprint was team-stamped:\n%s", codex.Document)
	}
	if codex.Metadata.Labels[v1beta1.LabelShared] != "true" ||
		codex.Metadata.Labels[v1beta1.LabelContentDigest] != "0123456789abcdef" {
		t.Fatalf("shared labels lost: %v", codex.Metadata.Labels)
	}
	for _, name := range []string{"alpha-private", "not-really-shared"} {
		bp := store.blueprints["default///"+name]
		if bp.Metadata.Labels[v1beta1.LabelTeam] != "alpha" ||
			!strings.Contains(string(bp.Document), v1beta1.LabelTeam+": alpha") {
			t.Fatalf("team-owned blueprint %s not stamped: %v", name, bp.Metadata.Labels)
		}
	}
	cfg := store.configs["default/default/default/alpha-dev-codex"]
	if cfg.Metadata.Labels[v1beta1.LabelTeam] != "alpha" {
		t.Fatalf("config not stamped: %v", cfg.Metadata.Labels)
	}
	if len(store.deleted) != 0 {
		t.Fatalf("first apply deleted %v", store.deleted)
	}
	// The same documents applied without a team are stored exactly as authored.
	plain := newTeamStore(t)
	if _, err = setupTestController(t, plain.runner()).ApplyDocuments(
		[]parser.Document{blueprintDocument(sharedBlueprint("codex")), blueprintDocument(owned)}, "",
	); err != nil {
		t.Fatal(err)
	}
	for key, bp := range plain.blueprints {
		if _, stamped := bp.Metadata.Labels[v1beta1.LabelTeam]; stamped {
			t.Fatalf("%s stamped without a team", key)
		}
	}
}

// TestApplyDocuments_SharedBlueprintPrune pins when a team apply removes a
// shared blueprint: only one the team's configs referenced before the apply,
// that none of its documents carries now, and that no stored CellConfig —
// another team's or a hand-written one — still references.
func TestApplyDocuments_SharedBlueprintPrune(t *testing.T) {
	for _, tc := range []struct {
		name string
		// others are configs stored besides alpha's own.
		others []v1beta1.CellConfigDoc
		// docs is what alpha applies; nil means its roster is now empty.
		docs       func() []parser.Document
		wantPruned []string
		wantKept   []string
	}{
		{
			name: "team-moves-to-another-blueprint",
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("claude")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
				}
			},
			wantPruned: []string{"CellBlueprint/codex"},
			wantKept:   []string{"claude", "custom", "opencode"},
		},
		{
			name:   "another-team-still-references-it",
			others: []v1beta1.CellConfigDoc{teamConfig("beta-dev", "beta", sharedRef("codex"))},
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("claude")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
				}
			},
			wantKept: []string{"claude", "codex", "custom", "opencode"},
		},
		{
			name:   "a-hand-written-config-still-references-it",
			others: []v1beta1.CellConfigDoc{teamConfig("mine", "", sharedRef("codex"))},
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("claude")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
				}
			},
			wantKept: []string{"claude", "codex", "custom", "opencode"},
		},
		{
			// A same-named blueprint elsewhere is a different blueprint.
			name: "another-team-references-a-same-named-blueprint-in-another-scope",
			others: []v1beta1.CellConfigDoc{
				teamConfig("beta-realm", "beta", v1beta1.CellConfigBlueprintRef{Name: "codex", Realm: "auth"}),
				teamConfig("beta-space", "beta", v1beta1.CellConfigBlueprintRef{Name: "codex", Realm: "default", Space: "default"}),
			},
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("claude")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
				}
			},
			wantPruned: []string{"CellBlueprint/codex"},
			wantKept:   []string{"claude", "custom", "opencode"},
		},
		{
			name: "team-keeps-using-it",
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("codex")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("codex"))),
				}
			},
			wantKept: []string{"codex", "custom", "opencode"},
		},
		{
			// A content conflict renames this team's blueprint; the one it used
			// before goes once nothing references it.
			name: "team-moves-to-a-digest-named-blueprint",
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("codex-0123456789abcdef")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("codex-0123456789abcdef"))),
				}
			},
			wantPruned: []string{"CellBlueprint/codex"},
			wantKept:   []string{"codex-0123456789abcdef", "custom", "opencode"},
		},
		{
			// One of two configs still references it after the apply.
			name: "one-of-the-team's-configs-still-references-it",
			docs: func() []parser.Document {
				return []parser.Document{
					blueprintDocument(sharedBlueprint("claude")),
					configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
					configDocument(teamConfig("alpha-pm", "alpha", sharedRef("codex"))),
				}
			},
			wantKept: []string{"claude", "codex", "custom", "opencode"},
		},
		{
			name:       "team-roster-emptied",
			docs:       func() []parser.Document { return nil },
			wantPruned: []string{"CellBlueprint/codex", "CellConfig/alpha-dev"},
			wantKept:   []string{"custom", "opencode"},
		},
		{
			name:       "team-roster-emptied-while-another-team-uses-it",
			others:     []v1beta1.CellConfigDoc{teamConfig("beta-dev", "beta", sharedRef("codex"))},
			docs:       func() []parser.Document { return nil },
			wantPruned: []string{"CellConfig/alpha-dev"},
			wantKept:   []string{"codex", "custom", "opencode"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTeamStore(t)
			// codex: what alpha uses today. opencode: a shared blueprint alpha never
			// referenced, used by nobody. custom: a blueprint kuke team did not render.
			store.storeBlueprint(sharedBlueprint("codex"))
			store.storeBlueprint(sharedBlueprint("opencode"))
			store.storeBlueprint(*blueprintDoc(0, "custom", "default").CellBlueprintDoc)
			store.storeConfig(teamConfig("alpha-dev", "alpha", sharedRef("codex")))
			for _, other := range tc.others {
				store.storeConfig(other)
			}
			exec := setupTestController(t, store.runner())

			result, err := exec.ApplyDocuments(tc.docs(), "alpha")
			if err != nil {
				t.Fatal(err)
			}
			if failed := failedResources(result); len(failed) != 0 {
				t.Fatalf("failed resources: %v", failed)
			}
			if got := prunedResources(result); !slices.Equal(got, tc.wantPruned) {
				t.Fatalf("pruned = %v, want %v", got, tc.wantPruned)
			}
			sort.Strings(store.deleted)
			if !slices.Equal(store.deleted, tc.wantPruned) {
				t.Fatalf("deleted = %v, want %v", store.deleted, tc.wantPruned)
			}
			if got := store.blueprintNames(); !slices.Equal(got, tc.wantKept) {
				t.Fatalf("stored blueprints = %v, want %v", got, tc.wantKept)
			}
			// Other owners' configs are never touched by alpha's apply.
			for _, other := range tc.others {
				if _, ok := store.configs[configStoreKey(intmodel.CellConfigMetadata{
					Name: other.Metadata.Name, Realm: "default", Space: "default", Stack: "default",
				})]; !ok {
					t.Fatalf("config %s of another owner was removed", other.Metadata.Name)
				}
			}
			// Converged: applying the same set again prunes nothing more.
			store.deleted = nil
			again, err := exec.ApplyDocuments(tc.docs(), "alpha")
			if err != nil || len(prunedResources(again)) != 0 || len(store.deleted) != 0 {
				t.Fatalf("second apply pruned %v (err=%v)", store.deleted, err)
			}
		})
	}
}

// A team that referenced a blueprint kuke team did not render (no shared label)
// leaves it alone when it moves away: only shared blueprints are reference-
// counted, everything else follows the team label or is the operator's.
func TestApplyDocuments_UnsharedBlueprintIsNeverReferencePruned(t *testing.T) {
	store := newTeamStore(t)
	store.storeBlueprint(*blueprintDoc(0, "custom", "default").CellBlueprintDoc)
	store.storeBlueprint(sharedBlueprint("codex"))
	store.storeConfig(teamConfig("alpha-dev", "alpha", sharedRef("custom")))
	exec := setupTestController(t, store.runner())
	result, err := exec.ApplyDocuments([]parser.Document{
		configDocument(teamConfig("alpha-dev", "alpha", sharedRef("codex"))),
	}, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(prunedResources(result)) != 0 || !slices.Equal(store.blueprintNames(), []string{"codex", "custom"}) {
		t.Fatalf("pruned=%v stored=%v", prunedResources(result), store.blueprintNames())
	}
}

// When the daemon cannot tell whether a shared blueprint is still referenced —
// another owner's config is unreadable — it deletes nothing and says so.
func TestApplyDocuments_SharedPruneFailsClosed(t *testing.T) {
	store := newTeamStore(t)
	store.storeBlueprint(sharedBlueprint("codex"))
	store.storeBlueprint(sharedBlueprint("claude"))
	store.storeConfig(teamConfig("alpha-dev", "alpha", sharedRef("codex")))
	store.storeConfig(teamConfig("beta-dev", "beta", sharedRef("codex")))
	store.unreadable["beta-dev"] = true
	exec := setupTestController(t, store.runner())
	_, err := exec.ApplyDocuments([]parser.Document{
		blueprintDocument(sharedBlueprint("claude")),
		configDocument(teamConfig("alpha-dev", "alpha", sharedRef("claude"))),
	}, "alpha")
	if err == nil || !strings.Contains(err.Error(), `read config "beta-dev"`) {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(store.deleted, "CellBlueprint/codex") ||
		!slices.Equal(store.blueprintNames(), []string{"claude", "codex"}) {
		t.Fatalf("shared blueprint deleted without knowing its references: %v", store.deleted)
	}
	// The team's own documents were still applied.
	stored, convErr := apischeme.ConvertCellConfigToExternal(store.configs["default/default/default/alpha-dev"])
	if convErr != nil || stored.Spec.Blueprint.Name != "claude" {
		t.Fatalf("alpha's config not applied: %+v err=%v", stored.Spec.Blueprint, convErr)
	}
}
