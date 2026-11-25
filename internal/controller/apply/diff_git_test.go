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

package apply_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func diffGitRef(name string) *intmodel.ContainerSecretRef {
	return &intmodel.ContainerSecretRef{Name: name, Realm: "default"}
}

// diffStoredGit returns a fresh ContainerGit using the #1472 stored-credential
// fields; every call allocates new pointers so equality is by value.
func diffStoredGit() *intmodel.ContainerGit {
	return &intmodel.ContainerGit{
		Author: &intmodel.GitIdentity{Name: "Dev", Email: "dev@example.com"},
		Sign:   []string{intmodel.GitSignCommits},
		Signing: &intmodel.GitSigning{
			SecretRef:         diffGitRef("signing-key"),
			AllowedSignersRef: diffGitRef("signers"),
		},
		Credentials: []intmodel.GitCredential{
			{Host: "github.com", SecretRef: diffGitRef("gh-key"), User: "git"},
			{Host: "https://gitlab.example.com", SecretRef: diffGitRef("gl-token")},
		},
	}
}

func diffGitContainer(git *intmodel.ContainerGit, repos ...intmodel.ContainerRepo) intmodel.Container {
	return intmodel.Container{
		Metadata: intmodel.ContainerMetadata{Name: "web"},
		Spec: intmodel.ContainerSpec{
			ID:        "web",
			RealmName: "default", SpaceName: "default", StackName: "default", CellName: "hello-world",
			Image: "nginx:1.27",
			Git:   git,
			Repos: repos,
		},
	}
}

// TestDiffContainer_GitCredentialsAndSigning covers the stored-credential
// fields of the git block (#1472): a change to any of them is a compatible
// change that makes `kuke apply` reconcile, and two value-equal blocks built
// from distinct pointers settle to a no-op.
func TestDiffContainer_GitCredentialsAndSigning(t *testing.T) {
	if diff := apply.DiffContainer(diffGitContainer(diffStoredGit()), diffGitContainer(diffStoredGit())); diff.HasChanges {
		t.Fatalf("value-equal git blocks must not diff, got %+v", diff)
	}

	tests := []struct {
		name string
		mut  func(*intmodel.ContainerGit)
	}{
		{"credential added", func(g *intmodel.ContainerGit) {
			g.Credentials = append(
				g.Credentials,
				intmodel.GitCredential{Host: "bitbucket.org", SecretRef: diffGitRef("bb")},
			)
		}},
		{"credential removed", func(g *intmodel.ContainerGit) { g.Credentials = g.Credentials[:1] }},
		{"all credentials removed", func(g *intmodel.ContainerGit) { g.Credentials = nil }},
		{
			"credential secret renamed",
			func(g *intmodel.ContainerGit) { g.Credentials[0].SecretRef = diffGitRef("gh-key-2") },
		},
		{"credential secret rescoped", func(g *intmodel.ContainerGit) {
			g.Credentials[0].SecretRef = &intmodel.ContainerSecretRef{Name: "gh-key", Realm: "default", Space: "team-a"}
		}},
		{
			"credential host",
			func(g *intmodel.ContainerGit) { g.Credentials[1].Host = "https://gitlab.example.com/team" },
		},
		{"credential user", func(g *intmodel.ContainerGit) { g.Credentials[0].User = "deploy" }},
		{"credential port", func(g *intmodel.ContainerGit) { g.Credentials[0].Port = 2222 }},
		{"credential hostKeyPolicy", func(g *intmodel.ContainerGit) {
			g.Credentials[0].HostKeyPolicy = intmodel.GitHostKeyPolicyAcceptNew
		}},
		{"credentials reordered", func(g *intmodel.ContainerGit) {
			g.Credentials[0], g.Credentials[1] = g.Credentials[1], g.Credentials[0]
		}},
		{"signing secret", func(g *intmodel.ContainerGit) { g.Signing.SecretRef = diffGitRef("other-key") }},
		{"signing allowed signers", func(g *intmodel.ContainerGit) { g.Signing.AllowedSignersRef = nil }},
		{"signing removed", func(g *intmodel.ContainerGit) { g.Signing, g.Sign = nil, nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			desired := diffStoredGit()
			tt.mut(desired)
			diff := apply.DiffContainer(diffGitContainer(desired), diffGitContainer(diffStoredGit()))
			if diff.ChangeType != apply.ChangeTypeCompatible {
				t.Fatalf("change type = %v, want compatible; diff: %+v", diff.ChangeType, diff)
			}
			if !hasChangedField(diff, "git") {
				t.Errorf("ChangedFields = %v, want git", diff.ChangedFields)
			}
		})
	}
}

// TestDiffContainer_GitOnlyCredentialsIsNotZero guards gitIsZero: a block that
// declares nothing but a credential (or nothing but a stored signing key, or
// nothing but an identity Secret ref, #1683) is real configuration, not an
// empty `git:` equal to none.
func TestDiffContainer_GitOnlyCredentialsIsNotZero(t *testing.T) {
	blocks := map[string]*intmodel.ContainerGit{
		"credentials only": {Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: diffGitRef("k")}}},
		"signing only":     {Signing: &intmodel.GitSigning{SecretRef: diffGitRef("k")}},
		"identityRef only": {IdentityRef: diffGitRef("git-ssh")},
	}
	for name, git := range blocks {
		t.Run(name, func(t *testing.T) {
			diff := apply.DiffContainer(diffGitContainer(git), diffGitContainer(nil))
			if !hasChangedField(diff, "git") {
				t.Errorf("adding a %s block is not seen as a change: %+v", name, diff)
			}
			diff = apply.DiffContainer(diffGitContainer(nil), diffGitContainer(git))
			if !hasChangedField(diff, "git") {
				t.Errorf("removing a %s block is not seen as a change: %+v", name, diff)
			}
		})
	}
	// An explicitly empty block (including an empty credentials list) still
	// equals none.
	empty := &intmodel.ContainerGit{Credentials: []intmodel.GitCredential{}}
	if diff := apply.DiffContainer(diffGitContainer(empty), diffGitContainer(nil)); diff.HasChanges {
		t.Errorf("empty git block must equal none, got %+v", diff)
	}
}

// TestDiffContainer_RepoGit covers the per-source git block (#1472, step 7).
// It is a pointer on ContainerRepo, so equality must be by value: two equal
// blocks behind different pointers are no change, and an edit inside the
// block is a repos change.
func TestDiffContainer_RepoGit(t *testing.T) {
	repoGit := func() *intmodel.RepoGit {
		return &intmodel.RepoGit{
			Author:     &intmodel.GitIdentity{Name: "Repo Dev", Email: "repo@example.com"},
			Credential: &intmodel.GitCredential{SecretRef: diffGitRef("repo-key")},
			Signing:    &intmodel.GitSigning{SecretRef: diffGitRef("repo-signing")},
			Sign:       []string{intmodel.GitSignCommits},
		}
	}
	repo := func(git *intmodel.RepoGit) intmodel.ContainerRepo {
		return intmodel.ContainerRepo{Name: "app", URL: "git@github.com:o/r.git", Target: "/src", Git: git}
	}

	if diff := apply.DiffContainer(diffGitContainer(nil, repo(repoGit())), diffGitContainer(nil, repo(repoGit()))); diff.HasChanges {
		t.Fatalf("value-equal repo git blocks must not diff, got %+v", diff)
	}

	tests := []struct {
		name string
		mut  func(*intmodel.RepoGit) *intmodel.RepoGit
	}{
		{"block added to a plain repo", func(*intmodel.RepoGit) *intmodel.RepoGit { return nil }},
		{"author", func(g *intmodel.RepoGit) *intmodel.RepoGit { g.Author.Email = "other@example.com"; return g }},
		{"committer added", func(g *intmodel.RepoGit) *intmodel.RepoGit {
			g.Committer = &intmodel.GitIdentity{Name: "Bot", Email: "bot@example.com"}
			return g
		}},
		{"credential secret", func(g *intmodel.RepoGit) *intmodel.RepoGit {
			g.Credential.SecretRef = diffGitRef("other-key")
			return g
		}},
		{"credential removed", func(g *intmodel.RepoGit) *intmodel.RepoGit { g.Credential = nil; return g }},
		{"signing secret", func(g *intmodel.RepoGit) *intmodel.RepoGit {
			g.Signing.SecretRef = diffGitRef("other-signing")
			return g
		}},
		{"sign targets", func(g *intmodel.RepoGit) *intmodel.RepoGit {
			g.Sign = []string{intmodel.GitSignCommits, intmodel.GitSignTags}
			return g
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.mut(repoGit())
			diff := apply.DiffContainer(diffGitContainer(nil, repo(repoGit())), diffGitContainer(nil, repo(actual)))
			if diff.ChangeType != apply.ChangeTypeCompatible {
				t.Fatalf("change type = %v, want compatible; diff: %+v", diff.ChangeType, diff)
			}
			if !hasChangedField(diff, "repos") {
				t.Errorf("ChangedFields = %v, want repos", diff.ChangedFields)
			}
		})
	}
}

// TestDiff_ScopeDefaultsGit covers defaults.git on Realm, Space and Stack
// (#1472, step 6): setting or editing it is a compatible change `kuke apply`
// writes through, value-equal blocks are no change, and an empty block equals
// none.
func TestDiff_ScopeDefaultsGit(t *testing.T) {
	edited := func() *intmodel.ContainerGit {
		g := diffStoredGit()
		g.Credentials[0].SecretRef = diffGitRef("rotated-key")
		return g
	}
	realm := func(git *intmodel.ContainerGit) intmodel.Realm {
		r := intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: "r"}}
		if git != nil {
			r.Spec.Defaults = &intmodel.ScopeDefaults{Git: git}
		}
		return r
	}
	space := func(git *intmodel.ContainerGit) intmodel.Space {
		s := intmodel.Space{Metadata: intmodel.SpaceMetadata{Name: "s"}, Spec: intmodel.SpaceSpec{RealmName: "r"}}
		if git != nil {
			s.Spec.Defaults = &intmodel.SpaceDefaults{Git: git}
		}
		return s
	}
	stack := func(git *intmodel.ContainerGit) intmodel.Stack {
		st := intmodel.Stack{
			Metadata: intmodel.StackMetadata{Name: "st"},
			Spec:     intmodel.StackSpec{RealmName: "r", SpaceName: "s"},
		}
		if git != nil {
			st.Spec.Defaults = &intmodel.ScopeDefaults{Git: git}
		}
		return st
	}
	diffs := map[string]func(desired, actual *intmodel.ContainerGit) apply.DiffResult{
		"realm": func(d, a *intmodel.ContainerGit) apply.DiffResult { return apply.DiffRealm(realm(d), realm(a)) },
		"space": func(d, a *intmodel.ContainerGit) apply.DiffResult { return apply.DiffSpace(space(d), space(a)) },
		"stack": func(d, a *intmodel.ContainerGit) apply.DiffResult { return apply.DiffStack(stack(d), stack(a)) },
	}
	for kind, diff := range diffs {
		t.Run(kind, func(t *testing.T) {
			if d := diff(diffStoredGit(), nil); !d.HasChanges || d.ChangeType != apply.ChangeTypeCompatible {
				t.Errorf("adding defaults.git = %+v, want a compatible change", d)
			}
			if d := diff(edited(), diffStoredGit()); !d.HasChanges || d.ChangeType != apply.ChangeTypeCompatible {
				t.Errorf("editing a defaults.git credential = %+v, want a compatible change", d)
			}
			if d := diff(diffStoredGit(), diffStoredGit()); d.HasChanges {
				t.Errorf("value-equal defaults.git must not diff, got %+v", d)
			}
			if d := diff(&intmodel.ContainerGit{}, nil); d.HasChanges {
				t.Errorf("an empty defaults.git must equal none, got %+v", d)
			}
		})
	}
}
