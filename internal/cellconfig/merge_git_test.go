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
	"reflect"
	"testing"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func identity(name string) *v1beta1.GitIdentity {
	return &v1beta1.GitIdentity{Name: name, Email: name + "@example.com"}
}

func credential(host, secret string) v1beta1.GitCredential {
	return v1beta1.GitCredential{Host: host, SecretRef: secretRef(secret)}
}

// gitLayers is the full #1472 step-6 hierarchy: every level sets something a
// deeper one overrides and something only it sets.
func gitLayers() []GitLayer {
	return []GitLayer{
		{Origin: v1beta1.RenderOriginRealm, Git: &v1beta1.ContainerGit{
			Author:    identity("realm"),
			Committer: identity("realm"),
			Signing:   &v1beta1.GitSigning{SecretRef: secretRef("realm-signing")},
			Sign:      []string{v1beta1.GitSignCommits, v1beta1.GitSignTags},
			Credentials: []v1beta1.GitCredential{
				credential("github.com", "realm-github"),
				credential("gitlab.com", "realm-gitlab"),
			},
		}},
		{Origin: v1beta1.RenderOriginSpace, Git: &v1beta1.ContainerGit{
			Committer:   identity("space"),
			Credentials: []v1beta1.GitCredential{credential("github.com", "space-github")},
		}},
		// A level that declares no git block is skipped, not treated as a reset.
		{Origin: v1beta1.RenderOriginStack, Git: nil},
		{Origin: v1beta1.RenderOriginConfig, Git: &v1beta1.ContainerGit{
			Author: identity("config"),
			Sign:   []string{v1beta1.GitSignCommits},
			Credentials: []v1beta1.GitCredential{
				credential("bitbucket.org", "config-bitbucket"),
				credential(" gitlab.com ", "config-gitlab"),
			},
		}},
	}
}

func TestMergeGit_DeeperLevelWinsPerField(t *testing.T) {
	merged, origins := MergeGit(gitLayers())
	if merged == nil {
		t.Fatal("MergeGit returned nil for populated layers")
	}
	if merged.Author.Name != "config" || origins.Author != v1beta1.RenderOriginConfig {
		t.Errorf("author = %+v from %q, want the Config's", merged.Author, origins.Author)
	}
	if merged.Committer.Name != "space" || origins.Committer != v1beta1.RenderOriginSpace {
		t.Errorf("committer = %+v from %q, want the Space's", merged.Committer, origins.Committer)
	}
	if merged.Signing == nil || merged.Signing.SecretRef.Name != "realm-signing" ||
		origins.Signing != v1beta1.RenderOriginRealm {
		t.Errorf("signing = %+v from %q, want the Realm's (no deeper level sets a key)",
			merged.Signing, origins.Signing)
	}
	if !reflect.DeepEqual(merged.Sign, []string{v1beta1.GitSignCommits}) || origins.Sign != v1beta1.RenderOriginConfig {
		t.Errorf("sign = %v from %q, want the Config's list replacing the Realm's whole", merged.Sign, origins.Sign)
	}
}

// TestMergeGit_CredentialsMergeByHost pins the by-host rule: a deeper level
// replaces the entry of a host a shallower one set (in place, keeping the
// first-seen order) and otherwise appends.
func TestMergeGit_CredentialsMergeByHost(t *testing.T) {
	merged, origins := MergeGit(gitLayers())
	type entry struct{ host, secret string }
	var got []entry
	for _, c := range merged.Credentials {
		got = append(got, entry{c.Host, c.SecretRef.Name})
	}
	want := []entry{
		{"github.com", "space-github"},
		{" gitlab.com ", "config-gitlab"},
		{"bitbucket.org", "config-bitbucket"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("credentials = %v, want %v", got, want)
	}
	wantOrigins := map[string]string{
		"github.com":    v1beta1.RenderOriginSpace,
		"gitlab.com":    v1beta1.RenderOriginConfig,
		"bitbucket.org": v1beta1.RenderOriginConfig,
	}
	if !reflect.DeepEqual(origins.Credentials, wantOrigins) {
		t.Errorf("credential origins = %v, want %v", origins.Credentials, wantOrigins)
	}
}

// TestMergeGit_SigningGroupReplacedWhole asserts a level that names a signing
// key replaces the shallower key group in either form, so a stored key never
// ends up paired with another level's path-based key or allowed signers.
func TestMergeGit_SigningGroupReplacedWhole(t *testing.T) {
	t.Run("secret key replaces path key", func(t *testing.T) {
		merged, origins := MergeGit([]GitLayer{
			{Origin: v1beta1.RenderOriginRealm, Git: &v1beta1.ContainerGit{
				SigningKey: "/home/u/.ssh/id.pub", AllowedSigners: "/home/u/.ssh/allowed",
			}},
			{Origin: v1beta1.RenderOriginConfig, Git: &v1beta1.ContainerGit{
				Signing: &v1beta1.GitSigning{SecretRef: secretRef("key")},
			}},
		})
		if merged.SigningKey != "" || merged.AllowedSigners != "" || merged.Signing == nil {
			t.Errorf("merged = %+v, want only the stored signing key", merged)
		}
		if origins.Signing != v1beta1.RenderOriginConfig {
			t.Errorf("signing origin = %q, want config", origins.Signing)
		}
	})
	t.Run("path key replaces secret key", func(t *testing.T) {
		merged, _ := MergeGit([]GitLayer{
			{Origin: v1beta1.RenderOriginRealm, Git: &v1beta1.ContainerGit{
				Signing: &v1beta1.GitSigning{SecretRef: secretRef("key")},
			}},
			{Origin: v1beta1.RenderOriginConfig, Git: &v1beta1.ContainerGit{SigningKey: "/k.pub"}},
		})
		if merged.Signing != nil || merged.SigningKey != "/k.pub" {
			t.Errorf("merged = %+v, want only the path key", merged)
		}
	})
	t.Run("allowed signers alone joins the shallower key", func(t *testing.T) {
		merged, origins := MergeGit([]GitLayer{
			{Origin: v1beta1.RenderOriginRealm, Git: &v1beta1.ContainerGit{SigningKey: "/k.pub"}},
			{Origin: v1beta1.RenderOriginConfig, Git: &v1beta1.ContainerGit{AllowedSigners: "/allowed"}},
		})
		if merged.SigningKey != "/k.pub" || merged.AllowedSigners != "/allowed" {
			t.Errorf("merged = %+v, want the realm key with the config's allowed signers", merged)
		}
		if origins.Signing != v1beta1.RenderOriginRealm {
			t.Errorf("signing origin = %q, want realm", origins.Signing)
		}
	})
}

func TestMergeGit_NothingSetIsNil(t *testing.T) {
	merged, origins := MergeGit([]GitLayer{{Origin: v1beta1.RenderOriginRealm}, {Origin: v1beta1.RenderOriginConfig}})
	if merged != nil {
		t.Errorf("MergeGit = %+v, want nil when no layer declares git", merged)
	}
	if origins.Credentials == nil {
		t.Error("origins.Credentials must be a usable map even when nothing merged")
	}
	if merged, _ = MergeGit(nil); merged != nil {
		t.Errorf("MergeGit(nil) = %+v, want nil", merged)
	}
}

// TestMergeGit_DoesNotAliasLayers guards the copy semantics: editing the merged
// block must not reach back into a Realm/Space/Stack document.
func TestMergeGit_DoesNotAliasLayers(t *testing.T) {
	layers := gitLayers()
	merged, _ := MergeGit(layers)
	merged.Author.Name = "edited"
	merged.Committer.Name = "edited"
	merged.Sign[0] = "edited"
	merged.Signing.AllowedSignersRef = secretRef("edited")
	merged.Credentials[0].User = "edited"

	want := gitLayers()
	for i := range layers {
		if !reflect.DeepEqual(layers[i], want[i]) {
			t.Errorf("layer %q was modified through the merged block:\n got %+v\nwant %+v",
				layers[i].Origin, layers[i].Git, want[i].Git)
		}
	}
}

// TestMergeDefaults_Git covers the wiring: scope defaults' git blocks and the
// Config's own are merged into the returned Config, the input is untouched, and
// the explain view names each part's origin without any secret material.
func TestMergeDefaults_Git(t *testing.T) {
	all := gitLayers()
	layers := []DefaultsLayer{
		{Origin: v1beta1.RenderOriginRealm, Defaults: &v1beta1.ScopeDefaults{Git: all[0].Git}},
		{Origin: v1beta1.RenderOriginSpace, Defaults: &v1beta1.ScopeDefaults{Git: all[1].Git}},
		{Origin: v1beta1.RenderOriginStack, Defaults: nil},
	}
	cfg := mergeConfig()
	cfg.Spec.Git = all[3].Git

	merged, values := MergeDefaults(cfg, minimalBlueprint(), layers)

	if merged.Spec.Git == nil || merged.Spec.Git.Author.Name != "config" ||
		merged.Spec.Git.Committer.Name != "space" || len(merged.Spec.Git.Credentials) != 3 {
		t.Fatalf("merged git = %+v, want the layered identity with three credentials", merged.Spec.Git)
	}
	if !reflect.DeepEqual(cfg.Spec.Git, gitLayers()[3].Git) {
		t.Errorf("MergeDefaults modified the input Config's git block: %+v", cfg.Spec.Git)
	}

	for key, want := range map[string]v1beta1.RenderedValue{
		"default.author":                    {Value: "config <config@example.com>", Origin: v1beta1.RenderOriginConfig},
		"default.committer":                 {Value: "space <space@example.com>", Origin: v1beta1.RenderOriginSpace},
		"default.signing":                   {Value: "secret:r/realm-signing", Origin: v1beta1.RenderOriginRealm},
		"default.credential[github.com]":    {Value: "secret:r/space-github", Origin: v1beta1.RenderOriginSpace},
		"default.credential[gitlab.com]":    {Value: "secret:r/config-gitlab", Origin: v1beta1.RenderOriginConfig},
		"default.credential[bitbucket.org]": {Value: "secret:r/config-bitbucket", Origin: v1beta1.RenderOriginConfig},
	} {
		got := originOf(t, values, v1beta1.RenderKindGit, key)
		if got.Value != want.Value || got.Origin != want.Origin {
			t.Errorf("explain %s = %q from %q, want %q from %q", key, got.Value, got.Origin, want.Value, want.Origin)
		}
	}
	// The combined explain list stays sorted by kind then key.
	sorted := append([]v1beta1.RenderedValue(nil), values...)
	SortRenderedValues(sorted)
	if !reflect.DeepEqual(values, sorted) {
		t.Errorf("explain values are not sorted after the git entries were appended: %+v", values)
	}
}

// TestMergeDefaults_NoGitAnywhere locks the no-op: a Config with no git at any
// level renders exactly as before #1472 — no git block, no git explain rows.
func TestMergeDefaults_NoGitAnywhere(t *testing.T) {
	layers := []DefaultsLayer{{Origin: v1beta1.RenderOriginRealm, Defaults: &v1beta1.ScopeDefaults{
		Values: map[string]string{"REGION": "realm"},
	}}}
	merged, values := MergeDefaults(mergeConfig(), minimalBlueprint(), layers)
	if merged.Spec.Git != nil {
		t.Errorf("merged git = %+v, want nil", merged.Spec.Git)
	}
	for _, v := range values {
		if v.Kind == v1beta1.RenderKindGit {
			t.Errorf("unexpected git explain row %+v", v)
		}
	}
}

// TestMergeDefaults_PathSigningKeyExplained covers the pre-#1472 path form in
// the explain view: the key path is shown as-is.
func TestMergeDefaults_PathSigningKeyExplained(t *testing.T) {
	cfg := mergeConfig()
	cfg.Spec.Git = &v1beta1.ContainerGit{SigningKey: "/home/u/.ssh/id.pub", Sign: []string{v1beta1.GitSignCommits}}
	_, values := MergeDefaults(cfg, minimalBlueprint(), nil)
	got := originOf(t, values, v1beta1.RenderKindGit, "default.signing")
	if got.Value != "/home/u/.ssh/id.pub" || got.Origin != v1beta1.RenderOriginConfig {
		t.Errorf("default.signing = %+v, want the key path from config", got)
	}
}
