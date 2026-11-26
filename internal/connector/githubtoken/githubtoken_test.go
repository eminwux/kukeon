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

package githubtoken_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/connector/githubtoken"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

// realm is the scope every test stores in.
func realm() scopedefaults.Scope { return scopedefaults.Scope{Realm: "default"} }

func gitSecret(name string, identity map[string]string) v1beta1.SecretDoc {
	return v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: name, Realm: "default"},
		Spec:     v1beta1.SecretSpec{Type: v1beta1.SecretTypeBasicAuth, Connector: "git", Identity: identity},
	}
}

// TestBuild: one token, the github identity and — with git — a basic-auth git
// identity for github.com that names it.
func TestBuild(t *testing.T) {
	plan, err := githubtoken.Build(connectortest.Catalog(t), githubtoken.Options{
		Scope: realm(), Token: " tok \n", Git: &githubtoken.Git{Hosts: []string{"github.com, ghe.example.com", "github.com"}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Token.Metadata.Name != githubtoken.DefaultName || plan.Token.Spec.Data != "tok" ||
		plan.Token.Spec.Connector != "github" {
		t.Errorf("github identity = %+v", plan.Token)
	}
	if plan.Git == nil || plan.Linked || plan.Git.Metadata.Name != githubtoken.DefaultGitName {
		t.Fatalf("git identity = %+v (linked %v)", plan.Git, plan.Linked)
	}
	want := map[string]string{"hosts": "github.com,ghe.example.com", "kukeon.io/token-from": "github-token@default"}
	if !reflect.DeepEqual(plan.Git.Spec.Identity, want) ||
		*plan.Git.Spec.BasicAuth != (v1beta1.SecretBasicAuth{Username: "x-access-token", Token: "tok"}) {
		t.Errorf("git identity = %+v (basic-auth %+v)", plan.Git.Spec.Identity, plan.Git.Spec.BasicAuth)
	}
	if !reflect.DeepEqual(plan.Material(), []string{"tok"}) {
		t.Errorf("material = %v", plan.Material())
	}
}

// TestBuildRotatesTheLinkedIdentity: the git identity linked to the github
// identity is rotated under its own name with its hosts, author and committer.
func TestBuildRotatesTheLinkedIdentity(t *testing.T) {
	existing := []v1beta1.SecretDoc{
		gitSecret("unrelated", map[string]string{"hosts": "github.com"}),
		gitSecret("work-git", map[string]string{
			"hosts": "ghe.example.com", "author": "A <a@example.com>", "committer": "C <c@example.com>",
			"kukeon.io/token-from": "github-token@default",
		}),
	}
	plan, err := githubtoken.Build(connectortest.Catalog(t), githubtoken.Options{
		Scope: realm(), Token: "tok", Git: &githubtoken.Git{},
	}, existing)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Linked || plan.Git.Metadata.Name != "work-git" || plan.Git.Spec.Identity["hosts"] != "ghe.example.com" ||
		plan.Git.Spec.Identity["author"] != "A <a@example.com>" || plan.Git.Spec.Identity["committer"] != "C <c@example.com>" {
		t.Errorf("rotation = %+v (linked %v)", plan.Git, plan.Linked)
	}
}

// TestBuildRejections: nothing that cannot be stored safely is planned.
func TestBuildRejections(t *testing.T) {
	taken := []v1beta1.SecretDoc{gitSecret("github-git", map[string]string{"hosts": "github.com"})}
	for name, tc := range map[string]struct {
		opts     githubtoken.Options
		existing []v1beta1.SecretDoc
		want     string
	}{
		"no token":      {githubtoken.Options{Scope: realm(), Token: " \n"}, nil, "the token is required"},
		"unsafe name":   {githubtoken.Options{Scope: realm(), Token: "secret-canary", Name: "../x"}, nil, "unsafe github identity name"},
		"same names":    {githubtoken.Options{Scope: realm(), Token: "secret-canary", Name: "x", Git: &githubtoken.Git{Name: "x"}}, nil, "a name of its own"},
		"unlinked name": {githubtoken.Options{Scope: realm(), Token: "secret-canary", Git: &githubtoken.Git{}}, taken, "not linked to github identity"},
		"bad author": {
			githubtoken.Options{Scope: realm(), Token: "secret-canary", Git: &githubtoken.Git{Author: "nobody"}}, nil, "want `Name <email>`",
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := githubtoken.Build(connectortest.Catalog(t), tc.opts, tc.existing)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "secret-canary") {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestPartialErrorNamesWhatWasStored: the message lists what was stored and
// what was not, and unwraps to the cause.
func TestPartialErrorNamesWhatWasStored(t *testing.T) {
	err := &githubtoken.PartialError{
		Result: githubtoken.Result{Secrets: []githubtoken.Stored{
			{Name: "github-token", Scope: realm(), Connector: "github", Action: "created"},
		}},
		Missing: []string{"git identity github-git@default"},
		Err:     errdefs.ErrRealmNotFound,
	}
	want := "stored github identity github-token@default (created); not done: git identity github-git@default: "
	if !strings.HasPrefix(err.Error(), want) || !errors.Is(err, errdefs.ErrRealmNotFound) {
		t.Errorf("err = %q", err)
	}
}
