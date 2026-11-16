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

// gitBlueprint is minimalBlueprint with a root container next to the workload
// and an inline-URL repo, so the git layering has every container kind and
// both repo channels to act on.
func gitBlueprint() v1beta1.CellBlueprintDoc {
	bp := minimalBlueprint()
	main := bp.Spec.Cell.Containers[0]
	main.Repos = append(main.Repos, v1beta1.ContainerRepo{
		Name: "inline", Target: "/inline", URL: "https://example.com/inline.git",
		Git: &v1beta1.RepoGit{Author: identity("inline")},
	})
	bp.Spec.Cell.Containers = []v1beta1.BlueprintContainer{
		{ID: "root", Root: true, Image: "registry.example.com/pause:1"},
		main,
		{ID: "side", Image: "registry.example.com/side:1"},
	}
	return bp
}

func gitConfig() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "r"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src":   {URL: "https://example.com/src.git"},
				"extra": {URL: "https://example.com/extra.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: secretRef("t")}},
		},
	}
}

func cellContainer(t *testing.T, cell v1beta1.CellDoc, id string) v1beta1.ContainerSpec {
	t.Helper()
	for _, c := range cell.Spec.Containers {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("container %q not in materialized cell", id)
	return v1beta1.ContainerSpec{}
}

func cellRepo(t *testing.T, c v1beta1.ContainerSpec, name string) v1beta1.ContainerRepo {
	t.Helper()
	for _, r := range c.Repos {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("repo %q not on container %q", name, c.ID)
	return v1beta1.ContainerRepo{}
}

// TestMaterialize_RepoFillGitCopiedToRepo pins #1472 step 7: a source's own
// identity and credential travel on its fill and land on that repo only.
func TestMaterialize_RepoFillGitCopiedToRepo(t *testing.T) {
	cfg := gitConfig()
	fill := cfg.Spec.Repos["src"]
	cred := credential("", "src-key")
	fill.Git = &v1beta1.RepoGit{
		Author:     identity("src"),
		Credential: &cred,
		Signing:    &v1beta1.GitSigning{SecretRef: secretRef("src-signing")},
		Sign:       []string{v1beta1.GitSignCommits},
	}
	cfg.Spec.Repos["src"] = fill

	cell, err := MaterializeWithName(cfg, gitBlueprint(), "prod-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	main := cellContainer(t, cell, "main")

	src := cellRepo(t, main, "src")
	if src.Git == nil || src.Git.Author.Name != "src" || src.Git.Credential == nil ||
		src.Git.Credential.SecretRef.Name != "src-key" || src.Git.Signing.SecretRef.Name != "src-signing" {
		t.Errorf("src repo git = %+v, want the fill's identity, credential and signing key", src.Git)
	}
	if extra := cellRepo(t, main, "extra"); extra.Git != nil {
		t.Errorf("extra repo git = %+v, want nil (its fill states none)", extra.Git)
	}
	// An inline-URL repo is not a slot: it keeps what the blueprint declared.
	if inline := cellRepo(t, main, "inline"); inline.Git == nil || inline.Git.Author.Name != "inline" {
		t.Errorf("inline repo git = %+v, want the blueprint's own", inline.Git)
	}
	// A per-source identity is not the cell's default identity.
	if main.Git != nil {
		t.Errorf("container git = %+v, want nil when only a repo fill states git", main.Git)
	}
}

// TestMaterialize_SlotRepoKeepsBlueprintGitWithoutFillGit asserts a fill that
// states no git leaves a slot's blueprint-declared git in place.
func TestMaterialize_SlotRepoKeepsBlueprintGitWithoutFillGit(t *testing.T) {
	bp := gitBlueprint()
	bp.Spec.Cell.Containers[1].Repos[0].Git = &v1beta1.RepoGit{Author: identity("slot-default")}

	cell, err := MaterializeWithName(gitConfig(), bp, "prod-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	src := cellRepo(t, cellContainer(t, cell, "main"), "src")
	if src.Git == nil || src.Git.Author.Name != "slot-default" {
		t.Errorf("src repo git = %+v, want the blueprint slot's git", src.Git)
	}
}

// TestMaterialize_ConfigGitLayeredOverBlueprint pins the container-level rule:
// the Config's (already scope-merged) git wins per field over what the
// blueprint container declares, reaches every workload container, and never
// reaches the root container.
func TestMaterialize_ConfigGitLayeredOverBlueprint(t *testing.T) {
	bp := gitBlueprint()
	bp.Spec.Cell.Containers[1].Git = &v1beta1.ContainerGit{
		Author:    identity("blueprint"),
		Committer: identity("blueprint"),
		Credentials: []v1beta1.GitCredential{
			credential("github.com", "bp-github"),
			credential("gitlab.com", "bp-gitlab"),
		},
	}
	cfg := gitConfig()
	cfg.Spec.Git = &v1beta1.ContainerGit{
		Author:      identity("config"),
		Signing:     &v1beta1.GitSigning{SecretRef: secretRef("signing")},
		Credentials: []v1beta1.GitCredential{credential("github.com", "cfg-github")},
	}

	cell, err := MaterializeWithName(cfg, bp, "prod-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}

	main := cellContainer(t, cell, "main").Git
	if main == nil || main.Author.Name != "config" || main.Committer.Name != "blueprint" {
		t.Fatalf("main git = %+v, want config author over blueprint committer", main)
	}
	if main.Signing == nil || main.Signing.SecretRef.Name != "signing" {
		t.Errorf("main signing = %+v, want the Config's", main.Signing)
	}
	if len(main.Credentials) != 2 || main.Credentials[0].SecretRef.Name != "cfg-github" ||
		main.Credentials[1].SecretRef.Name != "bp-gitlab" {
		t.Errorf(
			"main credentials = %+v, want config's github replacing the blueprint's, gitlab kept",
			main.Credentials,
		)
	}

	side := cellContainer(t, cell, "side").Git
	if side == nil || side.Author.Name != "config" || side.Committer != nil || len(side.Credentials) != 1 {
		t.Errorf("side git = %+v, want exactly the Config's block", side)
	}
	if root := cellContainer(t, cell, "root").Git; root != nil {
		t.Errorf("root git = %+v, want nil: the root container runs no git", root)
	}

	// The blueprint document is not edited by the layering.
	if got := bp.Spec.Cell.Containers[1].Git; got.Author.Name != "blueprint" || len(got.Credentials) != 2 ||
		got.Credentials[0].SecretRef.Name != "bp-github" {
		t.Errorf("blueprint container git was modified: %+v", got)
	}
}

// TestMaterialize_NoConfigGitPassesBlueprintGitThrough locks the pre-#1472
// behaviour for a Config that states no git.
func TestMaterialize_NoConfigGitPassesBlueprintGitThrough(t *testing.T) {
	bp := gitBlueprint()
	bp.Spec.Cell.Containers[1].Git = &v1beta1.ContainerGit{Author: identity("blueprint")}

	cell, err := MaterializeWithName(gitConfig(), bp, "prod-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got := cellContainer(t, cell, "main").Git; got == nil || got.Author.Name != "blueprint" || got.Committer != nil {
		t.Errorf("main git = %+v, want the blueprint's block unchanged", got)
	}
	if got := cellContainer(t, cell, "side").Git; got != nil {
		t.Errorf("side git = %+v, want nil", got)
	}
}

// TestMaterialize_ParameterizedRepoTarget: a repo target left to a ${PARAM}
// is resolved from the Config's values, and must then be an absolute path —
// the check the parser makes on a literal target, deferred to here.
func TestMaterialize_ParameterizedRepoTarget(t *testing.T) {
	bp := gitBlueprint()
	bp.Spec.Parameters = append(bp.Spec.Parameters, v1beta1.CellBlueprintParameter{Name: "CHECKOUT", Required: true})
	main := &bp.Spec.Cell.Containers[1]
	main.Repos = append(main.Repos, v1beta1.ContainerRepo{Name: "role-source", Target: "${CHECKOUT}"})
	cfg := gitConfig()
	cfg.Spec.Repos["role-source"] = v1beta1.CellConfigRepoFill{URL: "https://example.com/role.git"}

	cfg.Spec.Values = map[string]string{"CHECKOUT": "/home/user/role"}
	cell, err := MaterializeWithName(cfg, bp, "cell-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got := cellRepo(t, cellContainer(t, cell, main.ID), "role-source").Target; got != "/home/user/role" {
		t.Errorf("target = %q, want the resolved parameter", got)
	}

	cfg.Spec.Values = map[string]string{"CHECKOUT": "role"}
	if _, err = MaterializeWithName(cfg, bp, "cell-1"); !errors.Is(err, errdefs.ErrRepoTargetNotAbsolute) {
		t.Fatalf("err = %v, want ErrRepoTargetNotAbsolute for a relative resolved target", err)
	}
}
