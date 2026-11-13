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
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func secretRef(name string) *v1beta1.ContainerSecretRef {
	return &v1beta1.ContainerSecretRef{Name: name, Realm: "r"}
}

func mergeConfig() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "prod", Realm: "r"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"OWNER": "config"},
			Repos:     map[string]v1beta1.CellConfigRepoFill{"src": {URL: "https://config/src.git"}},
		},
	}
}

func originOf(t *testing.T, values []v1beta1.RenderedValue, kind, key string) v1beta1.RenderedValue {
	t.Helper()
	for _, v := range values {
		if v.Kind == kind && v.Key == key {
			return v
		}
	}
	t.Fatalf("no rendered value %s:%s in %+v", kind, key, values)
	return v1beta1.RenderedValue{}
}

// TestMergeDefaults_DeeperLevelWinsPerKey pins the #1418 precedence: Realm →
// Space → Stack → Config, merged per key, with the origin of each value.
func TestMergeDefaults_DeeperLevelWinsPerKey(t *testing.T) {
	layers := []DefaultsLayer{
		{Origin: v1beta1.RenderOriginRealm, Defaults: &v1beta1.ScopeDefaults{
			Values:  map[string]string{"OWNER": "realm", "REGION": "realm", "TIER": "realm"},
			Secrets: map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: secretRef("realm-token")}},
		}},
		{Origin: v1beta1.RenderOriginSpace, Defaults: &v1beta1.ScopeDefaults{
			Values: map[string]string{"REGION": "space", "TIER": "space"},
		}},
		{Origin: v1beta1.RenderOriginStack, Defaults: &v1beta1.ScopeDefaults{
			Values:  map[string]string{"TIER": "stack"},
			Secrets: map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: secretRef("stack-token")}},
		}},
	}

	merged, values := MergeDefaults(mergeConfig(), minimalBlueprint(), layers)

	wantValues := map[string]string{"OWNER": "config", "REGION": "space", "TIER": "stack"}
	for k, want := range wantValues {
		if got := merged.Spec.Values[k]; got != want {
			t.Errorf("values[%s] = %q, want %q", k, got, want)
		}
	}
	if got := merged.Spec.Secrets["token"].SecretRef.Name; got != "stack-token" {
		t.Errorf("secret token = %q, want stack-token (stack beats realm)", got)
	}
	if got := merged.Spec.Repos["src"].URL; got != "https://config/src.git" {
		t.Errorf("repo src = %q, want the Config's own fill", got)
	}

	for key, origin := range map[string]string{"OWNER": "config", "REGION": "space", "TIER": "stack"} {
		if got := originOf(t, values, v1beta1.RenderKindValue, key).Origin; got != origin {
			t.Errorf("origin of %s = %q, want %q", key, got, origin)
		}
	}
	if got := originOf(t, values, v1beta1.RenderKindSecret, "token"); got.Origin != "stack" ||
		got.Value != "secret:r/stack-token" {
		t.Errorf("secret token explain = %+v, want stack origin and ref description", got)
	}
	// TAG is never set by a level, so the blueprint default is reported.
	if got := originOf(t, values, v1beta1.RenderKindValue, "TAG"); got.Origin != "blueprint" || got.Value != "latest" {
		t.Errorf("TAG explain = %+v, want blueprint default latest", got)
	}
}

// TestMergeDefaults_IgnoresUndeclaredSlotDefaults: a realm-wide repo or secret
// default the blueprint does not declare is dropped, so it never trips the
// Config's strict unknown-slot validation.
func TestMergeDefaults_IgnoresUndeclaredSlotDefaults(t *testing.T) {
	layers := []DefaultsLayer{{Origin: v1beta1.RenderOriginRealm, Defaults: &v1beta1.ScopeDefaults{
		Repos:   map[string]v1beta1.CellConfigRepoFill{"unrelated": {URL: "https://x/y.git"}},
		Secrets: map[string]v1beta1.CellConfigSecretFill{"other": {SecretRef: secretRef("other")}},
	}}}
	cfg := mergeConfig()
	cfg.Spec.Secrets = map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: secretRef("t")}}

	merged, _ := MergeDefaults(cfg, minimalBlueprint(), layers)
	if _, ok := merged.Spec.Repos["unrelated"]; ok {
		t.Error("undeclared repo default leaked into the merged Config")
	}
	if _, ok := merged.Spec.Secrets["other"]; ok {
		t.Error("undeclared secret default leaked into the merged Config")
	}
	if _, err := MaterializeWithName(merged, minimalBlueprint(), "prod-1"); err != nil {
		t.Fatalf("merged Config failed to materialize: %v", err)
	}
}

// TestMergeDefaults_DefaultsFillRequiredSlot: a required slot the Config
// leaves open is satisfied by a level default.
func TestMergeDefaults_DefaultsFillRequiredSlot(t *testing.T) {
	cfg := mergeConfig() // fills src, leaves the required token secret open
	if _, err := MaterializeWithName(cfg, minimalBlueprint(), "prod-1"); err == nil {
		t.Fatal("precondition: the bare Config should fail the required token slot")
	}
	layers := []DefaultsLayer{{Origin: v1beta1.RenderOriginSpace, Defaults: &v1beta1.ScopeDefaults{
		Secrets: map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: secretRef("space-token")}},
	}}}
	merged, _ := MergeDefaults(cfg, minimalBlueprint(), layers)
	cell, err := MaterializeWithName(merged, minimalBlueprint(), "prod-1")
	if err != nil {
		t.Fatalf("materialize with space default: %v", err)
	}
	if got := cell.Spec.Containers[0].Secrets[0].SecretRef.Name; got != "space-token" {
		t.Errorf("token secretRef = %q, want space-token", got)
	}
}

// TestMergeDefaults_DoesNotMutateConfig guards the copy contract.
func TestMergeDefaults_DoesNotMutateConfig(t *testing.T) {
	cfg := mergeConfig()
	layers := []DefaultsLayer{{Origin: v1beta1.RenderOriginRealm, Defaults: &v1beta1.ScopeDefaults{
		Values: map[string]string{"EXTRA": "x"},
	}}}
	_, _ = MergeDefaults(cfg, minimalBlueprint(), layers)
	if _, ok := cfg.Spec.Values["EXTRA"]; ok {
		t.Error("MergeDefaults mutated the input Config's values")
	}
}

func TestRealmImageRef(t *testing.T) {
	for _, tc := range []struct {
		img  v1beta1.RealmImage
		want string
	}{
		{v1beta1.RealmImage{Name: "base", Ref: "docker.io/library/alpine:3.20"}, "docker.io/library/alpine:3.20"},
		{v1beta1.RealmImage{Name: "agent", Build: &v1beta1.RealmImageBuild{Context: ".", Tag: "kukeon.internal/x:1"}}, "kukeon.internal/x:1"},
		{v1beta1.RealmImage{Name: "agent", Build: &v1beta1.RealmImageBuild{Context: "."}}, "kukeon.internal/default/agent:latest"},
	} {
		if got := RealmImageRef("default", tc.img); got != tc.want {
			t.Errorf("RealmImageRef(%+v) = %q, want %q", tc.img, got, tc.want)
		}
	}
}
