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

	cell, err := Materialize(cfg, bp)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	// Deterministic name + back-ref label.
	if got := cell.Metadata.Name; got != "prod" {
		t.Errorf("cell name=%q want prod (StableName(config.Name))", got)
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

	_, err := Materialize(cfg, bp)
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

	_, err := Materialize(cfg, bp)
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

	cell, err := Materialize(cfg, bp)
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
	cell, err := Materialize(cfg, bp)
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

	_, err := Materialize(cfg, bp)
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintInvalid) {
		t.Fatalf("err=%v want ErrBlueprintInvalid (required param TAG unset)", err)
	}
}

// TestMaterializeWithName_OverrideWinsOverStableName pins the #754 escape hatch:
// when the caller pins a name, the materialized cell uses it verbatim instead of
// the StableName(cfg.Metadata.Name) used on the empty-override path. The
// kukeon.io/config back-reference label still points at the Config (lineage
// preserved across generated-name spawns).
func TestMaterializeWithName_OverrideWinsOverStableName(t *testing.T) {
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

// TestMaterializeWithName_EmptyOverrideUsesStableName guards the no-regression
// path: callers that pass "" still get the deterministic stable-name behavior
// the #742 idempotent-attach contract depends on.
func TestMaterializeWithName_EmptyOverrideUsesStableName(t *testing.T) {
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
	if got := cell.Metadata.Name; got != "prod" {
		t.Errorf("cell name=%q want prod (StableName fallback on empty override)", got)
	}
}

// TestGenerateName_ShapeAndUniqueness pins the `<config-name>-<6hex>` shape
// and verifies independent calls return distinct suffixes.
func TestGenerateName_ShapeAndUniqueness(t *testing.T) {
	const cfgName = "prod"
	seen := make(map[string]struct{}, 16)
	for range 16 {
		got, err := GenerateName(cfgName)
		if err != nil {
			t.Fatalf("GenerateName: %v", err)
		}
		prefix := cfgName + "-"
		if len(got) != len(prefix)+6 {
			t.Errorf("GenerateName len=%d (%q) want %d (`<config-name>-<6hex>`)", len(got), got, len(prefix)+6)
		}
		if got[:len(prefix)] != prefix {
			t.Errorf("GenerateName prefix=%q want %q", got[:len(prefix)], prefix)
		}
		for _, r := range got[len(prefix):] {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				t.Errorf("GenerateName suffix rune %q in %q not lowercase hex", r, got)
				break
			}
		}
		if _, dup := seen[got]; dup {
			t.Errorf("GenerateName produced duplicate name %q across 16 calls", got)
		}
		seen[got] = struct{}{}
	}
}
