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

package scopedefaults_test

import (
	"context"
	"errors"
	"testing"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"gopkg.in/yaml.v3"
)

// realmOnly serves one Realm and the Secrets that exist, and applies Realm
// documents.
type realmOnly struct {
	kukeonv1.FakeClient

	realm   v1beta1.RealmDoc
	secrets map[string]v1beta1.SecretSpec
	applies int
}

func (c *realmOnly) GetRealm(context.Context, v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	return kukeonv1.GetRealmResult{Realm: c.realm, MetadataExists: true}, nil
}

func (c *realmOnly) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	spec, ok := c.secrets[doc.Metadata.Name]
	return kukeonv1.GetSecretResult{Secret: v1beta1.SecretDoc{Spec: spec}, MetadataExists: ok}, nil
}

func (c *realmOnly) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	c.applies++
	var applied v1beta1.RealmDoc
	if err := yaml.Unmarshal(raw, &applied); err != nil {
		return kukeonv1.ApplyDocumentsResult{}, err
	}
	c.realm = applied
	return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
		{Kind: string(v1beta1.KindRealm), Name: c.realm.Metadata.Name, Action: "updated"},
	}}, nil
}

// TestSetGitRealmDefault: Set checks the identity, writes the Realm's git
// default keeping everything else the Realm carries, reports the default it
// replaced, and clears it.
func TestSetGitRealmDefault(t *testing.T) {
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	git, ok := catalog.Find(intconnector.GitIdentityTag)
	if !ok {
		t.Fatal("no git connector")
	}
	c := &realmOnly{
		realm: v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{Name: "dev"},
			Spec: v1beta1.RealmSpec{Namespace: "dev.kukeon.io", Defaults: &v1beta1.ScopeDefaults{
				Values: map[string]string{"REGION": "eu"},
				Git:    &v1beta1.ContainerGit{IdentityRef: &v1beta1.ContainerSecretRef{Name: "old", Realm: "dev"}},
			}},
		},
		secrets: map[string]v1beta1.SecretSpec{
			"me":    {Connector: "git", Identity: map[string]string{"author": "Me <me@example.com>"}},
			"other": {Connector: "elsewhere"},
		},
	}
	scope, err := scopedefaults.CheckScope(scopedefaults.Scope{Realm: " dev "})
	if err != nil || scope.Level() != v1beta1.ConnectorScopeRealm {
		t.Fatalf("scope = %+v, %v", scope, err)
	}
	set, err := scopedefaults.Set(
		t.Context(),
		c,
		catalog,
		git,
		scope,
		"",
		&v1beta1.ContainerSecretRef{Name: "me", Realm: "dev"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if set.Identity == nil || set.Identity.Name != "me" || set.Previous == nil || set.Previous.Name != "old" ||
		set.Level != v1beta1.ConnectorScopeRealm || set.Action != "updated" {
		t.Fatalf("set = %+v", set)
	}
	defaults := c.realm.Spec.Defaults
	if defaults.Git.IdentityRef.Name != "me" || defaults.Values["REGION"] != "eu" ||
		c.realm.Spec.Namespace != "dev.kukeon.io" {
		t.Fatalf("realm = %+v", c.realm.Spec)
	}

	for name, ref := range map[string]*v1beta1.ContainerSecretRef{
		"missing":            {Name: "nope", Realm: "dev"},
		"another connector":  {Name: "other", Realm: "dev"},
		"unsafe coordinates": {Name: "../me", Realm: "dev"},
	} {
		if _, err = scopedefaults.Set(t.Context(), c, catalog, git, scope, "", ref); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if c.applies != 1 {
		t.Fatalf("a rejected default was applied: %d applies", c.applies)
	}

	set, err = scopedefaults.Set(t.Context(), c, catalog, git, scope, "", nil)
	if err != nil || set.Identity != nil || set.Previous == nil || set.Previous.Name != "me" {
		t.Fatalf("clear = %+v, %v", set, err)
	}
	if c.realm.Spec.Defaults == nil || c.realm.Spec.Defaults.Git != nil {
		t.Fatalf("cleared realm defaults = %+v", c.realm.Spec.Defaults)
	}
	if _, err = scopedefaults.CheckScope(scopedefaults.Scope{Realm: "dev", Stack: "s"}); !errors.Is(
		err,
		errdefs.ErrConfig,
	) {
		t.Fatalf("gap in scope: %v", err)
	}
}
