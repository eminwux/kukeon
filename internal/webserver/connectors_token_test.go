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

package webserver

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

// The GitHub token for git (issue #1693): one request, one token, two
// identities.

const (
	githubIdentities = "/api/connectors/github/identities"
	tokenCanary      = "ghp-token-canary"
)

// tokenBody is a github identity request for the dev Realm.
func tokenBody(token, extra string) string {
	return `{"scope":{"realm":"dev"},"value":"` + token + `"` + extra + `}`
}

func postToken(t *testing.T, c *connectorClient, body string) (*httptest.ResponseRecorder, identityResult) {
	t.Helper()
	w := connectorRequest(t, connectorHandler(t, c), http.MethodPost, githubIdentities, body)
	if strings.Contains(w.Body.String(), tokenCanary) {
		t.Fatalf("response carries the token: %s", w.Body)
	}
	if w.Code != http.StatusOK {
		return w, identityResult{}
	}
	return w, decodeBody[identityResult](t, w)
}

// TestConnectorCatalogOffersTokenForGit: only the github connector says its
// token can also serve git, with the defaults the server applies.
func TestConnectorCatalogOffersTokenForGit(t *testing.T) {
	w := connectorRequest(t, connectorHandler(t, devScopes()), http.MethodGet, "/api/connectors", "")
	for _, view := range decodeBody[[]connectorView](t, w) {
		if view.Name != "github" {
			if view.TokenForGit != nil {
				t.Errorf("connector %q offers its token for git", view.Name)
			}
			continue
		}
		want := &tokenForGitView{
			Connector: "git", Hosts: []string{"github.com"}, Username: "x-access-token", Name: "github-git", TokenName: "github-token",
		}
		if !reflect.DeepEqual(view.TokenForGit, want) {
			t.Errorf("github tokenForGit = %+v, want %+v", view.TokenForGit, want)
		}
	}
}

// TestConnectorTokenForGitStoresBothAndDefaults: with git and makeDefault,
// the one token is stored as the github identity and as a basic-auth git
// identity for github.com that names it, and both become the Realm defaults.
func TestConnectorTokenForGitStoresBothAndDefaults(t *testing.T) {
	c := devScopes()
	w, result := postToken(t, c, tokenBody(tokenCanary+`\n`, `,"git":{},"makeDefault":true`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 2 {
		t.Fatalf("created %d secrets, want 2: %+v", len(c.created), c.created)
	}
	token, git := c.created[0], c.created[1]
	if token.Metadata != (v1beta1.SecretMetadata{Name: "github-token", Realm: "dev"}) ||
		token.Spec.Connector != "github" || token.Spec.Type != "" || token.Spec.Data != tokenCanary {
		t.Errorf("github identity = %+v", token)
	}
	wantIdentity := map[string]string{"hosts": "github.com", "kukeon.io/token-from": "github-token@dev"}
	if git.Metadata != (v1beta1.SecretMetadata{Name: "github-git", Realm: "dev"}) ||
		git.Spec.Connector != "git" || git.Spec.Type != v1beta1.SecretTypeBasicAuth ||
		git.Spec.BasicAuth == nil || git.Spec.BasicAuth.Username != "x-access-token" ||
		git.Spec.BasicAuth.Token != tokenCanary || git.Spec.BasicAuth.Password != "" ||
		!reflect.DeepEqual(git.Spec.Identity, wantIdentity) {
		t.Errorf("git identity = %+v (basic-auth %+v)", git, git.Spec.BasicAuth)
	}
	if result.Name != "github-token" || result.Action != "created" || result.Git == nil ||
		result.Git.Name != "github-git" || result.Git.Type != v1beta1.SecretTypeBasicAuth || len(result.Defaults) != 2 {
		t.Errorf("result = %+v", result)
	}
	defaults := c.realm.Spec.Defaults
	if defaults == nil || defaults.Secrets["github-token"].SecretRef == nil ||
		defaults.Secrets["github-token"].SecretRef.Name != "github-token" ||
		defaults.Git == nil || defaults.Git.IdentityRef == nil || defaults.Git.IdentityRef.Name != "github-git" {
		t.Fatalf("realm defaults = %+v", defaults)
	}
	// The Realm's other defaults are kept.
	if defaults.Values["REGION"] != "eu" || defaults.Secrets[connectortest.Slot].SecretRef == nil {
		t.Errorf("realm defaults lost an entry: %+v", defaults)
	}
}

// TestConnectorTokenWithoutGit: with git unchecked only the github identity
// is stored, and nothing about git changes.
func TestConnectorTokenWithoutGit(t *testing.T) {
	c := devScopes()
	w, result := postToken(t, c, tokenBody(tokenCanary, `,"name":"github-token","makeDefault":true`))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 1 || c.created[0].Spec.Connector != "github" || result.Git != nil {
		t.Fatalf("created = %+v, result = %+v", c.created, result)
	}
	if c.realm.Spec.Defaults == nil || c.realm.Spec.Defaults.Git != nil ||
		c.realm.Spec.Defaults.Secrets["github-token"].SecretRef == nil {
		t.Errorf("realm defaults = %+v, want the github default and no git default", c.realm.Spec.Defaults)
	}
	if result.StaleGit != nil {
		t.Errorf("no git identity is linked, yet the result names %+v", result.StaleGit)
	}
}

// TestConnectorTokenWithoutGitNamesTheLinkedIdentity: rotating the github
// identity with git unchecked leaves its linked git identity on the old token,
// and the result names it so the form can warn (issue #1707). A git identity
// linked to another github identity is not named.
func TestConnectorTokenWithoutGitNamesTheLinkedIdentity(t *testing.T) {
	c := devScopes()
	c.secrets["dev///other-git"] = v1beta1.SecretSpec{
		Type: v1beta1.SecretTypeBasicAuth, Connector: "git",
		Identity: map[string]string{"hosts": "github.com", "kukeon.io/token-from": "other@dev"},
	}
	if w, _ := postToken(t, c, tokenBody(tokenCanary+"-1", `,"name":"work","git":{"name":"work-git"}`)); w.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body)
	}
	w, result := postToken(t, c, tokenBody(tokenCanary+"-2", `,"name":"work"`))
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 3 || c.created[2].Metadata.Name != "work" || result.Action != "updated" || result.Git != nil {
		t.Fatalf("created = %+v, result = %+v", c.created, result)
	}
	want := &v1beta1.ContainerSecretRef{Name: "work-git", Realm: "dev"}
	if !reflect.DeepEqual(result.StaleGit, want) {
		t.Errorf("stale git = %+v, want %+v", result.StaleGit, want)
	}
	if !strings.Contains(w.Body.String(), `"staleGit":{"name":"work-git","realm":"dev"}`) {
		t.Errorf("response lacks staleGit: %s", w.Body)
	}
}

// TestConnectorTokenRotationKeepsTheLink: storing the github identity again
// with git on rotates the git identity linked to it — under its own name,
// with its hosts and author — and nothing else.
func TestConnectorTokenRotationKeepsTheLink(t *testing.T) {
	c := devScopes()
	first := `,"name":"work","git":{"name":"work-git","hosts":["github.com","ghe.example.com/org"],` +
		`"author":"Dev <dev@example.com>","username":"octo"}`
	if w, _ := postToken(t, c, tokenBody(tokenCanary+"-1", first)); w.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", w.Code, w.Body)
	}
	w, result := postToken(t, c, tokenBody(tokenCanary+"-2", `,"name":"work","git":{}`))
	if w.Code != http.StatusOK {
		t.Fatalf("rotate: status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 4 {
		t.Fatalf("created %d secrets, want 4", len(c.created))
	}
	token, git := c.created[2], c.created[3]
	if token.Metadata.Name != "work" || token.Spec.Data != tokenCanary+"-2" || result.Action != "updated" {
		t.Errorf("rotated github identity = %+v, result %+v", token, result)
	}
	if git.Metadata.Name != "work-git" || git.Spec.BasicAuth.Token != tokenCanary+"-2" ||
		git.Spec.Identity["hosts"] != "github.com,ghe.example.com/org" ||
		git.Spec.Identity["author"] != "Dev <dev@example.com>" ||
		git.Spec.Identity["kukeon.io/token-from"] != "work@dev" ||
		result.Git == nil || result.Git.Action != "updated" {
		t.Errorf("rotated git identity = %+v, result %+v", git, result.Git)
	}
	// The username is material: it is not read back, so the rotation stores
	// the default again unless it is given.
	if git.Spec.BasicAuth.Username != "x-access-token" {
		t.Errorf("rotated username = %q", git.Spec.BasicAuth.Username)
	}
}

// TestConnectorTokenNeverOverwritesAnUnlinkedSecret: a git identity name
// already taken by a Secret the token did not write fails the request before
// anything is stored.
func TestConnectorTokenNeverOverwritesAnUnlinkedSecret(t *testing.T) {
	c := devScopes()
	c.secrets["dev///github-git"] = v1beta1.SecretSpec{
		Type: v1beta1.SecretTypeSSHKey, Connector: "git", Identity: map[string]string{"hosts": "github.com"},
	}
	w, _ := postToken(t, c, tokenBody(tokenCanary, `,"git":{}`))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "not linked to github identity") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 0 {
		t.Fatalf("stored %+v", c.created)
	}
}

// TestConnectorTokenPartialFailure: a failure after the first write names
// what was stored and what was not, and never the token.
func TestConnectorTokenPartialFailure(t *testing.T) {
	leak := errors.New("daemon refused " + tokenCanary)
	for name, tc := range map[string]struct {
		setup  func(*connectorClient)
		stored int
		want   []string
	}{
		"git identity": {
			setup:  func(c *connectorClient) { c.createErr = map[string]error{"github-git": leak} },
			stored: 1,
			want: []string{
				"stored github identity github-token@dev (created)",
				"not done: git identity github-git@dev, github default at realm dev, git default at realm dev",
			},
		},
		"defaults": {
			setup:  func(c *connectorClient) { c.applyErr = map[string]error{string(v1beta1.KindRealm): leak} },
			stored: 2,
			want: []string{
				"stored github identity github-token@dev (created), git identity github-git@dev (created)",
				"not done: github default at realm dev, git default at realm dev",
			},
		},
		"github identity": {
			setup:  func(c *connectorClient) { c.createErr = map[string]error{"github-token": leak} },
			stored: 0,
			want:   []string{"store github identity github-token@dev"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := devScopes()
			tc.setup(c)
			w, _ := postToken(t, c, tokenBody(tokenCanary, `,"git":{},"makeDefault":true`))
			if w.Code == http.StatusOK {
				t.Fatalf("status=%d body=%s", w.Code, w.Body)
			}
			for _, want := range tc.want {
				if !strings.Contains(w.Body.String(), want) {
					t.Errorf("body = %s, want %q", w.Body, want)
				}
			}
			if len(c.created) != tc.stored {
				t.Errorf("stored %d secrets, want %d", len(c.created), tc.stored)
			}
		})
	}
}

// TestConnectorTokenRejectionsNeverEchoMaterial: a token request that cannot
// be stored is refused before anything is written, without the token.
func TestConnectorTokenRejectionsNeverEchoMaterial(t *testing.T) {
	for name, tc := range map[string]struct{ url, body string }{
		"git on another connector": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","identity":{"` + connectortest.Field + `":"a"},"value":"` +
				tokenCanary + `","git":{}}`,
		},
		"no token":         {githubIdentities, `{"scope":{"realm":"dev"},"git":{}}`},
		"bad author":       {githubIdentities, tokenBody(tokenCanary, `,"git":{"author":"nobody"}`)},
		"bad host":         {githubIdentities, tokenBody(tokenCanary, `,"git":{"hosts":["bad host"]}`)},
		"unsafe git name":  {githubIdentities, tokenBody(tokenCanary, `,"git":{"name":"../x"}`)},
		"same name":        {githubIdentities, tokenBody(tokenCanary, `,"name":"one","git":{"name":"one"}`)},
		"basic-auth input": {githubIdentities, tokenBody(tokenCanary, `,"username":"u","git":{}`)},
		"unknown git field": {
			githubIdentities, tokenBody(tokenCanary, `,"git":{"password":"`+tokenCanary+`"}`),
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := devScopes()
			w := connectorRequest(t, connectorHandler(t, c), http.MethodPost, tc.url, tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("status=%d body=%s", w.Code, w.Body)
			}
			if strings.Contains(w.Body.String(), tokenCanary) {
				t.Errorf("echoed the token: %s", w.Body)
			}
			if len(c.created) != 0 {
				t.Errorf("stored %+v", c.created)
			}
		})
	}
}

// TestConnectorIdentityMakeDefault: any identity form can make the identity
// its connector's default at its own scope; a connector with several methods
// names the one, and a failure of the default says the identity was stored.
func TestConnectorIdentityMakeDefault(t *testing.T) {
	c := devScopes()
	body := `{"scope":{"realm":"dev"},"name":"example-new","identity":{"` + connectortest.Field +
		`":"acme"},"value":"` + tokenCanary + `","makeDefault":true}`
	w := connectorRequest(t, connectorHandler(t, c), http.MethodPost,
		"/api/connectors/"+connectortest.Name+"/identities", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	result := decodeBody[identityResult](t, w)
	if len(result.Defaults) != 1 || result.Defaults[0].Identity == nil ||
		result.Defaults[0].Identity.Name != "example-new" || result.Defaults[0].Previous == nil ||
		result.Defaults[0].Previous.Name != "example-default" {
		t.Errorf("defaults = %+v", result.Defaults)
	}
	if ref := c.realm.Spec.Defaults.Secrets[connectortest.Slot].SecretRef; ref == nil || ref.Name != "example-new" {
		t.Errorf("realm default = %+v", ref)
	}

	c = devScopes()
	c.applyErr = map[string]error{string(v1beta1.KindRealm): errdefs.ErrRealmNotFound}
	w = connectorRequest(t, connectorHandler(t, c), http.MethodPost,
		"/api/connectors/"+connectortest.Name+"/identities", body)
	if w.Code == http.StatusOK || !strings.Contains(w.Body.String(), "stored "+connectortest.Name+
		" identity example-new@dev (created); not done: "+connectortest.Name+" default at realm dev") ||
		strings.Contains(w.Body.String(), tokenCanary) {
		t.Errorf("failed default: status=%d body=%s", w.Code, w.Body)
	}
	if len(c.created) != 1 {
		t.Errorf("stored %d secrets, want the identity", len(c.created))
	}

	// A method that takes no default fails before anything is stored.
	c = devScopes()
	w = connectorRequest(
		t,
		connectorHandler(t, c),
		http.MethodPost,
		"/api/connectors/"+connectortest.Name+"/identities",
		strings.Replace(body, `"makeDefault":true`, `"makeDefault":true,"method":"nope"`, 1),
	)
	if w.Code != http.StatusBadRequest || len(c.created) != 0 {
		t.Errorf("unknown method: status=%d body=%s stored=%d", w.Code, w.Body, len(c.created))
	}
}
