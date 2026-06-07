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

package cellconfig

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// minimalBlueprint returns a CellBlueprintDoc with a single container declaring
// one scalar param, one structural repo slot, one optional repo slot, and one
// required + one optional env-mode secret slot. The shape exercises every
// branch in materializeContainer.
func minimalBlueprint() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "bp-realm",
		},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{
					ID:    "main",
					Image: "registry.example.com/web:${TAG}",
					Repos: []v1beta1.ContainerRepo{
						{Name: "src", Target: "/srv", Required: true}, // required slot
						{Name: "extra", Target: "/extra"},             // optional slot
					},
					Secrets: []v1beta1.BlueprintSecretSlot{
						{Name: "token", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "TOKEN", Required: true},
						{Name: "ca", Mode: v1beta1.BlueprintSecretModeFile, MountPath: "/etc/ca.pem"},
					},
				}},
			},
		},
	}
}

func TestMaterialize_HappyPath_FillsRepoAndSecretSlots(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:   "prod",
			Realm:  "cfg-realm",
			Space:  "cfg-space",
			Labels: map[string]string{"app": "web"},
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src":   {URL: "https://example.com/src.git", Branch: "main"},
				"extra": {URL: "https://example.com/extra.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
				"ca":    {SecretRef: &v1beta1.ContainerSecretRef{Name: "ca-bundle", Realm: "cfg-realm"}},
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Deterministic name + back-ref label.
	if got := cell.Metadata.Name; got != "prod" {
		t.Errorf("cell name=%q want prod (Prefix(config))", got)
	}
	if got := cell.Metadata.Labels[LabelConfig]; got != "prod" {
		t.Errorf("LabelConfig=%q want prod", got)
	}
	if got := cell.Metadata.Labels["app"]; got != "web" {
		t.Errorf("operator label app=%q want web (copied from cfg.Metadata.Labels)", got)
	}

	// Cell scope is the Config's, not the blueprint's.
	if got := cell.Spec.RealmID; got != "cfg-realm" {
		t.Errorf("RealmID=%q want cfg-realm (config scope wins)", got)
	}
	if got := cell.Spec.SpaceID; got != "cfg-space" {
		t.Errorf("SpaceID=%q want cfg-space", got)
	}

	if len(cell.Spec.Containers) != 1 {
		t.Fatalf("containers=%d want 1", len(cell.Spec.Containers))
	}
	c := cell.Spec.Containers[0]

	// Scalar substitution ran (${TAG} → v2).
	if c.Image != "registry.example.com/web:v2" {
		t.Errorf("image=%q want ${TAG} substituted to v2", c.Image)
	}

	// Both repo slots filled — required + optional.
	if len(c.Repos) != 2 {
		t.Fatalf("repos=%d want 2", len(c.Repos))
	}
	repoByName := map[string]v1beta1.ContainerRepo{}
	for _, r := range c.Repos {
		repoByName[r.Name] = r
	}
	if got := repoByName["src"].URL; got != "https://example.com/src.git" {
		t.Errorf("src repo URL=%q", got)
	}
	if got := repoByName["src"].Branch; got != "main" {
		t.Errorf("src repo Branch=%q want main", got)
	}
	if got := repoByName["extra"].URL; got != "https://example.com/extra.git" {
		t.Errorf("extra repo URL=%q", got)
	}

	// Both secret slots translated to ContainerSecret.
	if len(c.Secrets) != 2 {
		t.Fatalf("secrets=%d want 2", len(c.Secrets))
	}
	secretByMountOrEnv := map[string]v1beta1.ContainerSecret{}
	for _, s := range c.Secrets {
		// env-mode key uses Name (== env var name); file-mode key uses MountPath.
		if s.MountPath != "" {
			secretByMountOrEnv[s.MountPath] = s
		} else {
			secretByMountOrEnv["env:"+s.Name] = s
		}
	}
	envSecret, ok := secretByMountOrEnv["env:TOKEN"]
	if !ok {
		t.Fatalf("token env secret (Name=TOKEN, MountPath=\"\") not emitted; got %+v", c.Secrets)
	}
	if envSecret.SecretRef == nil || envSecret.SecretRef.Name != "api-token" {
		t.Errorf("token secretRef=%+v want api-token", envSecret.SecretRef)
	}
	fileSecret, ok := secretByMountOrEnv["/etc/ca.pem"]
	if !ok {
		t.Fatalf("ca file secret (MountPath=/etc/ca.pem) not emitted; got %+v", c.Secrets)
	}
	if fileSecret.Name != "ca" {
		t.Errorf("ca secret Name=%q want ca (slot name for file mode)", fileSecret.Name)
	}
	if fileSecret.SecretRef == nil || fileSecret.SecretRef.Name != "ca-bundle" {
		t.Errorf("ca secretRef=%+v want ca-bundle", fileSecret.SecretRef)
	}
}

func TestMaterialize_RequiredRepoSlotUnfilled_Errors(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			// "src" required slot deliberately unfilled.
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "cfg-realm"}},
			},
		},
	}

	_, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err == nil || !errors.Is(err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Fatalf("err=%v want ErrConfigRequiredSlotUnfilled", err)
	}
}

func TestMaterialize_UnknownSlotFill_Errors(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src":     {URL: "https://example.com/src.git"},
				"nonsuch": {URL: "https://example.com/x.git"}, // not declared by blueprint
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "cfg-realm"}},
			},
		},
	}

	_, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err == nil || !errors.Is(err, errdefs.ErrConfigUnknownRepoSlot) {
		t.Fatalf("err=%v want ErrConfigUnknownRepoSlot", err)
	}
}

func TestMaterialize_OptionalSlotUnfilled_Drops(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"}, // required; "extra" left unfilled
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "cfg-realm"}},
				// "ca" optional, left unfilled
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	c := cell.Spec.Containers[0]
	if len(c.Repos) != 1 || c.Repos[0].Name != "src" {
		t.Errorf("repos=%+v want only src (extra is optional, dropped)", c.Repos)
	}
	if len(c.Secrets) != 1 || c.Secrets[0].Name != "TOKEN" {
		t.Errorf("secrets=%+v want only TOKEN (ca is optional, dropped)", c.Secrets)
	}
}

func TestMaterialize_RepoFillRefCarriesThrough(t *testing.T) {
	// A CellConfig repo fill with `ref` (immutable pin) must carry the ref
	// into the materialized ContainerRepo so kuketty's clone path detaches
	// HEAD at the pinned tag/SHA (#1034).
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git", Ref: "v0.1.0"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "cfg-realm"}},
			},
		},
	}
	cell, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	c := cell.Spec.Containers[0]
	var src v1beta1.ContainerRepo
	for _, r := range c.Repos {
		if r.Name == "src" {
			src = r
		}
	}
	if src.Ref != "v0.1.0" {
		t.Errorf("src repo Ref=%q want v0.1.0 (fill ref must carry through)", src.Ref)
	}
	if src.Branch != "" {
		t.Errorf("src repo Branch=%q want empty (only ref was filled)", src.Branch)
	}
}

func TestMaterialize_ScalarRepoPassesThrough(t *testing.T) {
	// A blueprint repo with an inline URL is scalar-mode (not a fillable slot);
	// the Config must not be required to mention it, and Materialize must keep
	// it in the runtime ContainerSpec.Repos.
	bp := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "lib", Realm: "bp-realm"},
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{
					ID:    "main",
					Image: "scratch",
					Repos: []v1beta1.ContainerRepo{
						{Name: "vendored", Target: "/v", URL: "https://example.com/v.git"},
					},
				}},
			},
		},
	}
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec:       v1beta1.CellConfigSpec{Blueprint: v1beta1.CellConfigBlueprintRef{Name: "lib", Realm: "bp-realm"}},
	}
	cell, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	c := cell.Spec.Containers[0]
	if len(c.Repos) != 1 || c.Repos[0].URL != "https://example.com/v.git" {
		t.Errorf("repos=%+v want scalar-mode vendored repo carried through", c.Repos)
	}
}

func TestMaterialize_PropagatesResolveError(t *testing.T) {
	// A blueprint with a required parameter the Config does not fill should
	// surface a blueprint-resolve error, not silently succeed with an empty
	// substitution.
	bp := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "web", Realm: "bp-realm"},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Required: true}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "${TAG}"}},
			},
		},
	}
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			// Values: TAG deliberately absent.
		},
	}

	_, err := MaterializeWithName(cfg, bp, Prefix(cfg))
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err=%v want ErrBlueprintInvalid (required param TAG unset)", err)
	}
}

// TestMaterializeWithName_UsesNameVerbatim pins that the caller-supplied name
// is used verbatim for both metadata.name and spec.ID, and that the
// kukeon.io/config lineage label still points at the Config (lineage preserved
// independently of the cell name).
func TestMaterializeWithName_UsesNameVerbatim(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, "prod-ab12cd")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got := cell.Metadata.Name; got != "prod-ab12cd" {
		t.Errorf("cell name=%q want prod-ab12cd (override)", got)
	}
	if got := cell.Spec.ID; got != "prod-ab12cd" {
		t.Errorf("cell spec.ID=%q want prod-ab12cd (override)", got)
	}
	if got := cell.Metadata.Labels[LabelConfig]; got != "prod" {
		t.Errorf("LabelConfig=%q want prod (back-reference preserved across override)", got)
	}
}

// TestMaterializeWithName_EmptyNameNotDerivedFromConfig pins the severed
// assumption (epic:cell-identity #1021): materialization no longer reaches back
// to cfg.Metadata.Name when the caller passes an empty name. The empty name is
// the caller's responsibility — materialize must NOT silently substitute
// Prefix(cfg) the way the pre-#1021 path did. The lineage
// label is still stamped regardless of the name.
func TestMaterializeWithName_EmptyNameNotDerivedFromConfig(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, "")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got := cell.Metadata.Name; got != "" {
		t.Errorf("cell name=%q want \"\" (name not derived from config name post-#1021)", got)
	}
	if got := cell.Metadata.Labels[LabelConfig]; got != "prod" {
		t.Errorf("LabelConfig=%q want prod (lineage stamped regardless of cell name)", got)
	}
}

// TestMaterializeWithName_StampsConfigProvenance pins the provenance block a
// Config-materialized cell carries (issue #1021): bindingKind=config, the
// Config's scoped name as the binding ref, and the Config's spec.values as the
// recorded params.
func TestMaterializeWithName_StampsConfigProvenance(t *testing.T) {
	bp := minimalBlueprint()
	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm", Space: "team-a"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, "prod")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	prov := cell.Spec.Provenance
	if prov == nil {
		t.Fatalf("cell.Spec.Provenance = nil; want a config provenance block")
	}
	if prov.BindingKind != v1beta1.BindingKindConfig {
		t.Errorf("bindingKind=%q want %q", prov.BindingKind, v1beta1.BindingKindConfig)
	}
	wantRef := v1beta1.CellBindingRef{Name: "prod", Realm: "cfg-realm", Space: "team-a"}
	if prov.BindingRef != wantRef {
		t.Errorf("bindingRef=%+v want %+v", prov.BindingRef, wantRef)
	}
	if got := prov.Params["TAG"]; got != "v2" {
		t.Errorf("params[TAG]=%q want v2", got)
	}
}

// TestMaterialize_ToleratesUndeclaredValues is the #1124 end-to-end
// regression: a team-rendered CellConfig carries operator facts (ROLE, GIT_*,
// HARNESS, PROJECT) that the blueprint never declares as spec.parameters[].
// Before the fix the first undeclared key aborted materialization with
// ErrBlueprintInvalid, making every team-init config un-instantiable. The
// Config channel now resolves leniently: undeclared keys are tolerated, the
// declared param still substitutes, and an undeclared ${ROLE} the body
// references resolves rather than surviving literal.
func TestMaterialize_ToleratesUndeclaredValues(t *testing.T) {
	bp := minimalBlueprint()
	// Drop the structural slots so this test isolates the values channel.
	bp.Spec.Cell.Containers[0].Repos = nil
	bp.Spec.Cell.Containers[0].Secrets = nil
	bp.Spec.Cell.Containers[0].WorkingDir = "/work/${ROLE}"

	cfg := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "dev-claude", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values: map[string]string{
				"TAG":                 "v2",
				"ROLE":                "dev",
				"HARNESS":             "claude",
				"PROJECT":             "kukeon",
				"GIT_ALLOWED_SIGNERS": "/keys/allowed_signers",
			},
		},
	}

	cell, err := MaterializeWithName(cfg, bp, "dev-claude")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v (undeclared values must not abort the Config channel)", err)
	}
	c := cell.Spec.Containers[0]
	if c.Image != "registry.example.com/web:v2" {
		t.Errorf("image=%q want declared ${TAG} substituted to v2", c.Image)
	}
	if c.WorkingDir != "/work/dev" {
		t.Errorf("workingDir=%q want undeclared ${ROLE} substituted to dev", c.WorkingDir)
	}
}
