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
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
)

// connectorClient stands in for kukeond behind the connectors routes: scope
// documents and Secrets it serves, and every write it received.
type connectorClient struct {
	kukeonv1.FakeClient

	realm   v1beta1.RealmDoc
	space   v1beta1.SpaceDoc
	stack   v1beta1.StackDoc
	secrets map[string]v1beta1.SecretSpec // "realm/space/stack/name" → readable spec
	configs []v1beta1.CellConfigDoc

	created  []v1beta1.SecretDoc
	applied  [][]byte
	previews []kukeonv1.CellFromConfigRequest
}

func (c *connectorClient) Close() error { return nil }

func (c *connectorClient) GetRealm(context.Context, v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	return kukeonv1.GetRealmResult{Realm: c.realm, MetadataExists: c.realm.Metadata.Name != ""}, nil
}

func (c *connectorClient) GetSpace(context.Context, v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	return kukeonv1.GetSpaceResult{Space: c.space, MetadataExists: c.space.Metadata.Name != ""}, nil
}

func (c *connectorClient) GetStack(context.Context, v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	return kukeonv1.GetStackResult{Stack: c.stack, MetadataExists: c.stack.Metadata.Name != ""}, nil
}

func secretKey(md v1beta1.SecretMetadata) string {
	return strings.Join([]string{md.Realm, md.Space, md.Stack, md.Name}, "/")
}

func (c *connectorClient) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	spec, ok := c.secrets[secretKey(doc.Metadata)]
	doc.Spec = spec
	return kukeonv1.GetSecretResult{Secret: doc, MetadataExists: ok}, nil
}

func (c *connectorClient) ListSecrets(context.Context, string, string, string, string) ([]v1beta1.SecretDoc, error) {
	var out []v1beta1.SecretDoc
	for key, spec := range c.secrets {
		parts := strings.Split(key, "/")
		out = append(out, v1beta1.SecretDoc{
			Metadata: v1beta1.SecretMetadata{Realm: parts[0], Space: parts[1], Stack: parts[2], Name: parts[3]},
			Spec:     spec,
		})
	}
	return out, nil
}

func (c *connectorClient) CreateSecret(
	_ context.Context, doc v1beta1.SecretDoc,
) (kukeonv1.CreateSecretResult, error) {
	_, existed := c.secrets[secretKey(doc.Metadata)]
	c.created = append(c.created, doc)
	if c.secrets == nil {
		c.secrets = map[string]v1beta1.SecretSpec{}
	}
	c.secrets[secretKey(doc.Metadata)] = readableSecret(doc.Spec)
	return kukeonv1.CreateSecretResult{Secret: doc, Created: !existed}, nil
}

func (c *connectorClient) ListConfigs(context.Context, string, string, string) ([]v1beta1.CellConfigDoc, error) {
	return c.configs, nil
}

func (c *connectorClient) GetConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	for _, cfg := range c.configs {
		if cfg.Metadata.Name == doc.Metadata.Name && cfg.Metadata.Realm == doc.Metadata.Realm {
			return kukeonv1.GetConfigResult{Config: cfg, MetadataExists: true}, nil
		}
	}
	return kukeonv1.GetConfigResult{}, nil
}

func (c *connectorClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	c.applied = append(c.applied, raw)
	var head struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	_ = yaml.Unmarshal(raw, &head)
	// A re-applied Config replaces the stored one, like the daemon.
	if head.Kind == string(v1beta1.KindCellConfig) {
		var doc v1beta1.CellConfigDoc
		_ = yaml.Unmarshal(raw, &doc)
		for i := range c.configs {
			if c.configs[i].Metadata.Name == doc.Metadata.Name {
				doc.Status = c.configs[i].Status
				c.configs[i] = doc
			}
		}
	}
	return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
		{Kind: head.Kind, Name: head.Metadata.Name, Action: "updated"},
	}}, nil
}

func (c *connectorClient) GetCell(context.Context, v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
}

func (c *connectorClient) PreviewCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.PreviewCellFromConfigResult, error) {
	c.previews = append(c.previews, req)
	statuses := []v1beta1.ConnectorStatus{{
		Connector: connectortest.Name, Component: connectortest.Name + "-token", Method: "token",
		Delivery: v1beta1.ConnectorDeliveryEnv, Slot: connectortest.Slot, Required: true,
		State: v1beta1.ConnectorResolved, Origin: v1beta1.RenderOriginRealm,
		Identity: &v1beta1.ContainerSecretRef{Name: "example-default", Realm: "dev"},
	}}
	if picked, ok := req.ConnectorOverrides[connectortest.Name]; ok {
		statuses[0].Identity, statuses[0].Origin = &picked, v1beta1.RenderOriginCell
	}
	return kukeonv1.PreviewCellFromConfigResult{
		Cell: v1beta1.CellDoc{
			Metadata: v1beta1.CellMetadata{Name: "cell-1"},
			Spec: v1beta1.CellSpec{
				RealmID: "dev", SpaceID: "default", StackID: "default",
				Provenance: &v1beta1.CellProvenance{ConnectorOverrides: req.ConnectorOverrides},
			},
		},
		Digest:     "sha256:" + connectortest.Name + "/" + statuses[0].Identity.Name,
		Connectors: statuses,
	}, nil
}

func connectorHandler(t *testing.T, c *connectorClient) http.Handler {
	t.Helper()
	policy, err := parseAccessPolicy("http://localhost:5129")
	if err != nil {
		t.Fatal(err)
	}
	// The catalog under test is the embedded one plus the test-only
	// connector: nothing in the web server names either.
	handler, err := newHandlerWithCatalog(
		policy, "test", func(context.Context) (kukeonv1.Client, error) { return c, nil }, connectortest.Catalog(t))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func connectorRequest(t *testing.T, handler http.Handler, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://localhost:5129"+url, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Origin", "http://localhost:5129")
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func decodeBody[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return out
}

func devScopes() *connectorClient {
	return &connectorClient{
		realm: v1beta1.RealmDoc{
			Metadata: v1beta1.RealmMetadata{Name: "dev"},
			Spec: v1beta1.RealmSpec{
				Namespace: "dev.kukeon.io",
				RegistryCredentials: []v1beta1.RegistryCredentials{
					{ServerAddress: "legacy.example.com", Username: "old", Password: "inline-password"},
				},
				Defaults: &v1beta1.ScopeDefaults{
					Values: map[string]string{"REGION": "eu"},
					Secrets: map[string]v1beta1.CellConfigSecretFill{
						connectortest.Slot: {
							SecretRef: &v1beta1.ContainerSecretRef{Name: "example-default", Realm: "dev"},
						},
					},
				},
			},
		},
		space: v1beta1.SpaceDoc{
			Metadata: v1beta1.SpaceMetadata{Name: "default"},
			Spec: v1beta1.SpaceSpec{RealmID: "dev", Defaults: &v1beta1.SpaceDefaults{
				Container: &v1beta1.SpaceContainerDefaults{},
			}},
		},
		stack: v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: "default"},
			Spec:     v1beta1.StackSpec{ID: "default", RealmID: "dev", SpaceID: "default"},
		},
		secrets: map[string]v1beta1.SecretSpec{
			"dev///example-default": {
				Connector: connectortest.Name,
				Identity:  map[string]string{connectortest.Field: "acme"},
			},
			"dev///example-other": {
				Connector: connectortest.Name,
				Identity:  map[string]string{connectortest.Field: "beta"},
			},
			"dev///untagged": {},
			"dev///ssh":      {Type: v1beta1.SecretTypeSSHKey},
		},
	}
}

// TestConnectorCatalogServesEveryConnector: the catalog route lists every
// connector with what the UI needs — including the test-only one, which no
// code names — and carries no secret.
func TestConnectorCatalogServesEveryConnector(t *testing.T) {
	w := connectorRequest(t, connectorHandler(t, devScopes()), http.MethodGet, "/api/connectors", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	views := decodeBody[[]connectorView](t, w)
	byName := map[string]connectorView{}
	for _, view := range views {
		byName[view.Name] = view
	}
	example, ok := byName[connectortest.Name]
	if !ok {
		t.Fatalf("catalog does not list %q: %s", connectortest.Name, w.Body)
	}
	if example.Title != "Example" || !reflect.DeepEqual(example.SecretTypes, []string{v1beta1.SecretTypeOpaque}) ||
		len(example.Fields) != 1 || example.Fields[0].Name != connectortest.Field || !example.Fields[0].Required ||
		len(example.Methods) != 1 || example.Methods[0].Slot != connectortest.Slot ||
		example.Methods[0].EnvName != connectortest.EnvName || example.Methods[0].Delivery != v1beta1.ConnectorDeliveryEnv {
		t.Errorf("example connector view = %+v", example)
	}
	// A connector with several methods exposes its exclusive group; one
	// without methods exposes its delivery handler and its scopes.
	var grouped, methodless int
	for _, view := range views {
		if len(view.Methods) > 1 && view.Group == "" {
			t.Errorf("connector %q has several methods and no group", view.Name)
		}
		if view.Group != "" {
			grouped++
		}
		if len(view.Methods) == 0 {
			methodless++
			if view.Delivery == "" || len(view.Scopes) == 0 {
				t.Errorf("method-less connector %q carries no delivery or scopes: %+v", view.Name, view)
			}
		}
	}
	if grouped == 0 || methodless == 0 {
		t.Errorf("catalog shape: %d grouped, %d method-less connectors", grouped, methodless)
	}
}

// TestConnectorIdentityCreateAndRotate: an identity is a tagged Secret; the
// same route replaces the value under the same name; nothing of the material
// is echoed, and the catalog listing shows the tag and the readable fields.
func TestConnectorIdentityCreateAndRotate(t *testing.T) {
	c := devScopes()
	handler := connectorHandler(t, c)
	body := `{"scope":{"realm":"dev"},"name":"example-new","identity":{"` + connectortest.Field +
		`":" acme "},"value":"tok-material-1\n"}`
	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+connectortest.Name+"/identities", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "tok-material") {
		t.Fatalf("response echoed the value: %s", w.Body)
	}
	result := decodeBody[identityResult](t, w)
	if result.Action != "created" || result.Connector != connectortest.Name ||
		result.Type != v1beta1.SecretTypeOpaque ||
		result.Identity[connectortest.Field] != "acme" {
		t.Errorf("result = %+v", result)
	}
	if len(c.created) != 1 || c.created[0].Spec.Data != "tok-material-1" ||
		c.created[0].Spec.Connector != connectortest.Name ||
		c.created[0].Metadata != (v1beta1.SecretMetadata{Name: "example-new", Realm: "dev"}) {
		t.Fatalf("created = %+v", c.created)
	}

	// Rotation: same name, new value.
	rotate := strings.Replace(body, "tok-material-1", "tok-material-2", 1)
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+connectortest.Name+"/identities", rotate)
	if w.Code != http.StatusOK || decodeBody[identityResult](t, w).Action != "updated" {
		t.Fatalf("rotate: status=%d body=%s", w.Code, w.Body)
	}
	if c.created[1].Spec.Data != "tok-material-2" {
		t.Errorf("rotation stored %q", c.created[1].Spec.Data)
	}

	// The secrets listing shows the tag and identity fields, never material.
	w = connectorRequest(t, handler, http.MethodGet, "/api/catalog/secrets?realm=dev", "")
	listing := w.Body.String()
	if !strings.Contains(listing, `"connector":"`+connectortest.Name+`"`) ||
		!strings.Contains(listing, `"`+connectortest.Field+`":"acme"`) {
		t.Errorf("secrets listing lost the tag or identity: %s", listing)
	}
	if strings.Contains(listing, "tok-material") || strings.Contains(listing, `"data"`) {
		t.Errorf("secrets listing carries material: %s", listing)
	}
}

// TestConnectorIdentityTypes: basic-auth and ssh-key identities are built from
// their own fields, with the type implied when the connector accepts one.
func TestConnectorIdentityTypes(t *testing.T) {
	c := devScopes()
	handler := connectorHandler(t, c)
	const key = "-----BEGIN OPENSSH PRIVATE" + " KEY-----\nQUJDREVGR0hJSktMTU5PUFFSU1Q=\n-----END OPENSSH PRIVATE" + " KEY-----"

	var basicAuth, sshOrBasic string
	for _, view := range decodeBody[[]connectorView](t,
		connectorRequest(t, handler, http.MethodGet, "/api/connectors", "")) {
		if reflect.DeepEqual(view.SecretTypes, []string{v1beta1.SecretTypeBasicAuth}) {
			basicAuth = view.Name
		}
		if len(view.SecretTypes) > 1 && view.Delivery == v1beta1.ConnectorDeliveryGit {
			sshOrBasic = view.Name
		}
	}
	if basicAuth == "" || sshOrBasic == "" {
		t.Fatalf("catalog carries no basic-auth-only (%q) or several-type git (%q) connector", basicAuth, sshOrBasic)
	}

	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+basicAuth+"/identities",
		`{"scope":{"realm":"dev"},"name":"reg","username":"octo","password":"pw-material"}`)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "pw-material") {
		t.Fatalf("basic-auth: status=%d body=%s", w.Code, w.Body)
	}
	if got := c.created[0].Spec; got.Type != v1beta1.SecretTypeBasicAuth || got.BasicAuth == nil ||
		got.BasicAuth.Username != "octo" || got.BasicAuth.Password != "pw-material" {
		t.Errorf("basic-auth secret = %+v", got)
	}

	// A connector that accepts two types needs the type named.
	sshBody := `{"scope":{"realm":"dev"},"name":"git-ssh","identity":{"hosts":"github.com","author":"A <a@example.com>"},` +
		`"privateKey":` + string(
		mustJSON(t, key),
	) + `,"publicKey":"ssh-ed25519 AAAA me"}`
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+sshOrBasic+"/identities", sshBody)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "choose a secret type") {
		t.Fatalf("missing type: status=%d body=%s", w.Code, w.Body)
	}
	typed := strings.Replace(sshBody, `"name":"git-ssh"`, `"name":"git-ssh","type":"ssh-key"`, 1)
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+sshOrBasic+"/identities", typed)
	if w.Code != http.StatusOK {
		t.Fatalf("ssh-key: status=%d body=%s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "QUJDREVG") {
		t.Fatalf("response echoed the key: %s", w.Body)
	}
	got := c.created[len(c.created)-1].Spec
	if got.Type != v1beta1.SecretTypeSSHKey || got.SSHKey == nil ||
		!strings.HasSuffix(got.SSHKey.PrivateKey, "KEY-----\n") ||
		got.SSHKey.PublicKey != "ssh-ed25519 AAAA me" ||
		got.Identity["hosts"] != "github.com" {
		t.Errorf("ssh-key secret = %+v (key %+v)", got, got.SSHKey)
	}

	// An author-only git identity (issue #1591) takes no value and no hosts:
	// it stores the placeholder and must carry an author.
	authorOnly := `{"scope":{"realm":"dev"},"name":"me","type":"opaque","identity":{"author":"Me <me@example.com>"}}`
	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+sshOrBasic+"/identities", authorOnly)
	if w.Code != http.StatusOK {
		t.Fatalf("author-only: status=%d body=%s", w.Code, w.Body)
	}
	got = c.created[len(c.created)-1].Spec
	if got.Data != "kukeon:git-author-only" || got.Connector != sshOrBasic ||
		got.Identity["author"] != "Me <me@example.com>" {
		t.Errorf("author-only secret = %+v", got)
	}
	for body, mention := range map[string]string{
		strings.Replace(authorOnly, `"type"`, `"value":"tok-material","type"`, 1):           "stores no value",
		`{"scope":{"realm":"dev"},"name":"me","type":"opaque"}`:                             `requires identity field \"author\"`,
		strings.Replace(authorOnly, `"identity":{`, `"identity":{"hosts":"github.com",`, 1): `\"hosts\" applies to`,
	} {
		w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+sshOrBasic+"/identities", body)
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), mention) ||
			strings.Contains(w.Body.String(), "tok-material") {
			t.Errorf("author-only %s: status=%d body=%s, want 400 naming %s", body, w.Code, w.Body, mention)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestConnectorIdentityRejectionsNeverEchoMaterial: an identity that does not
// fit its connector is refused before anything is stored, and no error carries
// the material that was sent.
func TestConnectorIdentityRejectionsNeverEchoMaterial(t *testing.T) {
	const material = "leak-me-material"
	field := connectortest.Field
	for name, tc := range map[string]struct{ url, body string }{
		"unknown connector": {"/api/connectors/nope/identities", `{"scope":{"realm":"dev"},"name":"x","value":"` + material + `"}`},
		"missing required field": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","value":"` + material + `"}`,
		},
		"undeclared field": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","identity":{"` + field + `":"a","other":"b"},"value":"` + material + `"}`,
		},
		"wrong type": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","type":"ssh-key","identity":{"` + field + `":"a"},"privateKey":"` + material + `"}`,
		},
		"no value": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","identity":{"` + field + `":"a"}}`,
		},
		"no realm": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{},"name":"x","identity":{"` + field + `":"a"},"value":"` + material + `"}`,
		},
		"unsafe name": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"../x","identity":{"` + field + `":"a"},"value":"` + material + `"}`,
		},
		"reserved name": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":".meta","identity":{"` + field + `":"a"},"value":"` + material + `"}`,
		},
		"unknown field in body": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","secret":"` + material + `"}`,
		},
		"malformed": {
			"/api/connectors/" + connectortest.Name + "/identities",
			`{"scope":{"realm":"dev"},"name":"x","value":"` + material + `"`,
		},
	} {
		c := devScopes()
		w := connectorRequest(t, connectorHandler(t, c), http.MethodPost, tc.url, tc.body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), material) {
			t.Errorf("%s: echoed material: %s", name, w.Body)
		}
		if len(c.created) != 0 {
			t.Errorf("%s: stored a rejected identity", name)
		}
	}
}

// TestConnectorDefaultsAcrossScopes: the defaults view reports each level and
// the winner; setting one edits exactly that entry of the Realm, Space or
// Stack and re-applies the document with everything else it carried.
func TestConnectorDefaultsAcrossScopes(t *testing.T) {
	c := devScopes()
	f := defaultsFixture{client: c, handler: connectorHandler(t, c)}

	row := f.row(t)
	if row.Slot != connectortest.Slot || row.Origin != v1beta1.ConnectorScopeRealm ||
		row.Effective == nil || row.Effective.Name != "example-default" || len(row.Levels) != 1 {
		t.Fatalf("defaults row = %+v", row)
	}
	f.stackDefaultIsSet(t)
	f.spaceDefaultKeepsContainerDefaults(t)
	f.realmDefaultIsCleared(t)
}

// defaultsFixture drives the defaults routes of the test-only connector over
// the dev Realm, Space and Stack.
type defaultsFixture struct {
	client  *connectorClient
	handler http.Handler
}

// row is the test-only connector's defaults row for the dev Stack's scope.
func (f defaultsFixture) row(t *testing.T) connectorDefault {
	t.Helper()
	w := connectorRequest(t, f.handler, http.MethodGet,
		"/api/connectors/defaults?realm=dev&space=default&stack=default", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	for _, row := range decodeBody[[]connectorDefault](t, w) {
		if row.Connector == connectortest.Name {
			return row
		}
	}
	t.Fatalf("no defaults row for %q: %s", connectortest.Name, w.Body)
	return connectorDefault{}
}

// set posts one level's default and requires the route to accept it.
func (f defaultsFixture) set(t *testing.T, scope, identity string) *httptest.ResponseRecorder {
	t.Helper()
	w := connectorRequest(t, f.handler, http.MethodPost, "/api/connectors/"+connectortest.Name+"/defaults",
		`{"scope":`+scope+`,"identity":`+identity+`}`)
	if w.Code != http.StatusOK {
		t.Fatalf("set default at %s: status=%d body=%s", scope, w.Code, w.Body)
	}
	return w
}

// appliedDoc decodes the i-th document the routes applied through the daemon.
func appliedDoc[T any](t *testing.T, f defaultsFixture, i int) T {
	t.Helper()
	var doc T
	if err := yaml.Unmarshal(f.client.applied[i], &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

// Stack level: the stack document gains only this entry.
func (f defaultsFixture) stackDefaultIsSet(t *testing.T) {
	t.Helper()
	w := f.set(t, `{"realm":"dev","space":"default","stack":"default"}`, `{"name":"example-other","realm":"dev"}`)
	if got := decodeBody[defaultResult](t, w); got.Level != v1beta1.ConnectorScopeStack || got.Action != "updated" {
		t.Errorf("result = %+v", got)
	}
	stack := appliedDoc[v1beta1.StackDoc](t, f, 0)
	if stack.Kind != v1beta1.KindStack || stack.Spec.RealmID != "dev" || stack.Spec.SpaceID != "default" ||
		stack.Spec.Defaults == nil || stack.Spec.Defaults.Secrets[connectortest.Slot].SecretRef.Name != "example-other" {
		t.Fatalf("applied stack = %s", f.client.applied[0])
	}
	f.client.stack.Spec.Defaults = stack.Spec.Defaults
	if row := f.row(t); row.Origin != v1beta1.ConnectorScopeStack || row.Effective.Name != "example-other" ||
		len(row.Levels) != 2 {
		t.Errorf("after the stack default: %+v", row)
	}
}

// Space level keeps the Space's container defaults.
func (f defaultsFixture) spaceDefaultKeepsContainerDefaults(t *testing.T) {
	t.Helper()
	f.set(t, `{"realm":"dev","space":"default"}`, `{"name":"example-other","realm":"dev"}`)
	space := appliedDoc[v1beta1.SpaceDoc](t, f, 1)
	if space.Kind != v1beta1.KindSpace || space.Spec.Defaults == nil || space.Spec.Defaults.Container == nil ||
		space.Spec.Defaults.Secrets[connectortest.Slot].SecretRef.Name != "example-other" {
		t.Fatalf("applied space = %s", f.client.applied[1])
	}
}

// Realm level: clearing removes the entry and keeps the Realm's other
// defaults and its registry credentials — inline password included, as the
// document was read from the daemon, not from a redacted view.
func (f defaultsFixture) realmDefaultIsCleared(t *testing.T) {
	t.Helper()
	w := f.set(t, `{"realm":"dev"}`, `null`)
	realm := appliedDoc[v1beta1.RealmDoc](t, f, 2)
	if realm.Kind != v1beta1.KindRealm || realm.Spec.Defaults == nil || len(realm.Spec.Defaults.Secrets) != 0 ||
		realm.Spec.Defaults.Values["REGION"] != "eu" || len(realm.Spec.RegistryCredentials) != 1 ||
		realm.Spec.RegistryCredentials[0].Password != "inline-password" {
		t.Fatalf("applied realm = %s", f.client.applied[2])
	}
	if strings.Contains(w.Body.String(), "inline-password") {
		t.Fatalf("response leaked the registry password: %s", w.Body)
	}
}

// TestConnectorDefaultRejections: a default must name an existing Secret that
// can be an identity of the connector, at a scope the connector allows.
func TestConnectorDefaultRejections(t *testing.T) {
	var realmOnly string
	c := devScopes()
	handler := connectorHandler(t, c)
	for _, view := range decodeBody[[]connectorView](t,
		connectorRequest(t, handler, http.MethodGet, "/api/connectors", "")) {
		if len(view.Methods) == 0 && view.Delivery != v1beta1.ConnectorDeliveryGit {
			realmOnly = view.Name
		}
	}
	for name, tc := range map[string]struct {
		url, body string
		status    int
	}{
		"missing secret": {
			"/api/connectors/" + connectortest.Name + "/defaults",
			`{"scope":{"realm":"dev"},"identity":{"name":"nope","realm":"dev"}}`, http.StatusNotFound,
		},
		"wrong type": {
			"/api/connectors/" + connectortest.Name + "/defaults",
			`{"scope":{"realm":"dev"},"identity":{"name":"ssh","realm":"dev"}}`, http.StatusBadRequest,
		},
		"unknown method": {
			"/api/connectors/" + connectortest.Name + "/defaults",
			`{"scope":{"realm":"dev"},"method":"nope","identity":null}`, http.StatusBadRequest,
		},
		"unsafe reference": {
			"/api/connectors/" + connectortest.Name + "/defaults",
			`{"scope":{"realm":"dev"},"identity":{"name":"../x","realm":"dev"}}`, http.StatusBadRequest,
		},
		"connector without a default": {
			"/api/connectors/" + realmOnly + "/defaults",
			`{"scope":{"realm":"dev"},"identity":null}`, http.StatusBadRequest,
		},
		"unknown connector": {
			"/api/connectors/nope/defaults", `{"scope":{"realm":"dev"},"identity":null}`, http.StatusBadRequest,
		},
	} {
		w := connectorRequest(t, handler, http.MethodPost, tc.url, tc.body)
		if w.Code != tc.status {
			t.Errorf("%s: status=%d want %d body=%s", name, w.Code, tc.status, w.Body)
		}
	}
	if len(c.applied) != 0 {
		t.Errorf("a rejected default was applied: %s", c.applied[0])
	}

	// An untagged Secret of an accepted type can still be made the default.
	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+connectortest.Name+"/defaults",
		`{"scope":{"realm":"dev"},"identity":{"name":"untagged","realm":"dev"}}`)
	if w.Code != http.StatusOK {
		t.Errorf("untagged secret: status=%d body=%s", w.Code, w.Body)
	}
}

func exampleConfig(name, team string, state string) v1beta1.CellConfigDoc {
	labels := map[string]string{}
	if team != "" {
		labels[v1beta1.LabelTeam] = team
	}
	status := v1beta1.ConnectorStatus{
		Connector: connectortest.Name, Component: connectortest.Name + "-token", Method: "token",
		Delivery: v1beta1.ConnectorDeliveryEnv, Slot: connectortest.Slot, Required: true, State: state,
	}
	if state == v1beta1.ConnectorUnresolved {
		status.Message = "no identity fills secret slot"
	}
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{Name: name, Realm: "dev", Labels: labels},
		Spec: v1beta1.CellConfigSpec{
			Blueprint:  v1beta1.CellConfigBlueprintRef{Name: "bp", Realm: "dev"},
			Components: []string{connectortest.Name + "-token"},
			Values:     map[string]string{"KEEP": "me"},
		},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{
			status,
			{
				Connector: "login-only",
				Component: "x-login",
				Method:    "login",
				Delivery:  v1beta1.ConnectorDeliveryStateVolume,
				State:     v1beta1.ConnectorResolved,
			},
		}},
	}
}

// TestConnectorStatusCountsMissingConfigs: the status route counts, per
// connector, the Configs that require it and lists the ones with no identity
// — the number behind the "needs a default" marker.
func TestConnectorStatusCountsMissingConfigs(t *testing.T) {
	c := devScopes()
	c.configs = []v1beta1.CellConfigDoc{
		exampleConfig("a", "", v1beta1.ConnectorUnresolved),
		exampleConfig("b", "kukeon", v1beta1.ConnectorUnresolved),
		exampleConfig("c", "", v1beta1.ConnectorResolved),
	}
	w := connectorRequest(t, connectorHandler(t, c), http.MethodGet, "/api/connectors/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	usage := map[string]connectorUsage{}
	for _, u := range decodeBody[[]connectorUsage](t, w) {
		usage[u.Connector] = u
	}
	example := usage[connectortest.Name]
	if example.Configs != 3 || len(example.Missing) != 2 || example.Missing[0].Config.Name != "a" ||
		example.Missing[1].Config.Name != "b" || example.Missing[0].Slot != connectortest.Slot {
		t.Errorf("usage of %q = %+v", connectortest.Name, example)
	}
	// A connector the catalog does not know (a newer kuke rendered it) is
	// still reported, and a login needs no identity.
	if login := usage["login-only"]; login.Configs != 3 || len(login.Missing) != 0 {
		t.Errorf("usage of an uncatalogued connector = %+v", login)
	}
	// Every catalog connector is present, even unused, so the page can list it.
	for _, view := range decodeBody[[]connectorView](t,
		connectorRequest(t, connectorHandler(t, c), http.MethodGet, "/api/connectors", "")) {
		if _, ok := usage[view.Name]; !ok {
			t.Errorf("status omits catalog connector %q", view.Name)
		}
	}
}

// TestConnectorPickForConfig: "Pick for this Config" saves a Config fill and
// keeps the rest of the Config; clearing it returns the Config to the
// defaults; a team-owned Config is read-only.
func TestConnectorPickForConfig(t *testing.T) {
	c := devScopes()
	c.configs = []v1beta1.CellConfigDoc{
		exampleConfig("mine", "", v1beta1.ConnectorUnresolved),
		exampleConfig("teams", "kukeon", v1beta1.ConnectorUnresolved),
	}
	handler := connectorHandler(t, c)
	pick := func(config, identity string) *httptest.ResponseRecorder {
		return connectorRequest(
			t,
			handler,
			http.MethodPost,
			"/api/configs/connector",
			`{"config":{"name":"`+config+`","realm":"dev"},"connector":"`+connectortest.Name+`","identity":`+identity+`}`,
		)
	}

	w := pick("mine", `{"name":"example-other","realm":"dev"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("pick: status=%d body=%s", w.Code, w.Body)
	}
	var applied v1beta1.CellConfigDoc
	if err := yaml.Unmarshal(c.applied[0], &applied); err != nil {
		t.Fatal(err)
	}
	if applied.Kind != v1beta1.KindCellConfig || applied.Status != nil ||
		applied.Spec.Secrets[connectortest.Slot].SecretRef.Name != "example-other" ||
		applied.Spec.Values["KEEP"] != "me" || applied.Spec.Blueprint.Name != "bp" {
		t.Fatalf("applied config = %s", c.applied[0])
	}

	w = pick("mine", `null`)
	if w.Code != http.StatusOK {
		t.Fatalf("clear: status=%d body=%s", w.Code, w.Body)
	}
	if err := yaml.Unmarshal(c.applied[1], &applied); err != nil {
		t.Fatal(err)
	}
	applied = v1beta1.CellConfigDoc{}
	_ = yaml.Unmarshal(c.applied[1], &applied)
	if len(applied.Spec.Secrets) != 0 {
		t.Errorf("clearing left a fill: %s", c.applied[1])
	}

	before := len(c.applied)
	for name, tc := range map[string]struct {
		w      *httptest.ResponseRecorder
		status int
		want   string
	}{
		"team-owned": {pick("teams", `{"name":"example-other","realm":"dev"}`), http.StatusBadRequest, `set by team \"kukeon\"`},
		"missing":    {pick("nope", `null`), http.StatusNotFound, ""},
		"wrong type": {pick("mine", `{"name":"ssh","realm":"dev"}`), http.StatusBadRequest, "ssh-key"},
		"no such secret": {
			pick("mine", `{"name":"nope","realm":"dev"}`), http.StatusNotFound, "",
		},
	} {
		if tc.w.Code != tc.status || !strings.Contains(tc.w.Body.String(), tc.want) {
			t.Errorf("%s: status=%d want %d body=%s", name, tc.w.Code, tc.status, tc.w.Body)
		}
	}
	w = connectorRequest(t, handler, http.MethodPost, "/api/configs/connector",
		`{"config":{"name":"mine","realm":"dev"},"connector":"other","identity":null}`)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "requires no connector") {
		t.Errorf("unrequired connector: status=%d body=%s", w.Code, w.Body)
	}
	if len(c.applied) != before {
		t.Errorf("a rejected pick was applied")
	}
}

// TestCreateFromConfigSendsConnectorOverrides: the New Cell flow forwards the
// identities picked for the cell, the preview reports the effective identity
// of every connector, and a different pick is a different preview.
func TestCreateFromConfigSendsConnectorOverrides(t *testing.T) {
	c := devScopes()
	c.configs = []v1beta1.CellConfigDoc{exampleConfig("mine", "", v1beta1.ConnectorResolved)}
	handler := connectorHandler(t, c)
	preview := func(connectors string) (map[string]json.RawMessage, int) {
		w := connectorRequest(t, handler, http.MethodPost, "/api/cells/from-config/preview",
			`{"config":{"name":"mine","realm":"dev"},"name":"cell-1","env":[]`+connectors+`}`)
		if w.Code != http.StatusOK {
			return nil, w.Code
		}
		return decodeBody[map[string]json.RawMessage](t, w), w.Code
	}

	plain, code := preview("")
	if code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	var statuses []v1beta1.ConnectorStatus
	if err := json.Unmarshal(plain["connectors"], &statuses); err != nil || len(statuses) != 1 ||
		statuses[0].Identity.Name != "example-default" || statuses[0].Origin != v1beta1.RenderOriginRealm {
		t.Fatalf("preview connectors = %s (%v)", plain["connectors"], err)
	}
	if len(c.previews[0].ConnectorOverrides) != 0 {
		t.Errorf("an unchanged pick was sent as an override: %+v", c.previews[0].ConnectorOverrides)
	}

	picked, code := preview(`,"connectors":{"` + connectortest.Name + `":{"name":"example-other","realm":"dev"}}`)
	if code != http.StatusOK {
		t.Fatalf("preview with a pick status=%d", code)
	}
	want := map[string]v1beta1.ContainerSecretRef{connectortest.Name: {Name: "example-other", Realm: "dev"}}
	if got := c.previews[1].ConnectorOverrides; !reflect.DeepEqual(got, want) {
		t.Errorf("override = %+v, want %+v", got, want)
	}
	_ = json.Unmarshal(picked["connectors"], &statuses)
	if statuses[0].Identity.Name != "example-other" || statuses[0].Origin != v1beta1.RenderOriginCell {
		t.Errorf("preview connectors with a pick = %s", picked["connectors"])
	}
	if string(plain["preview"]) == string(picked["preview"]) {
		t.Error("a different identity produced the same preview token")
	}

	for name, body := range map[string]string{
		"unsafe secret":  `,"connectors":{"` + connectortest.Name + `":{"name":"../x","realm":"dev"}}`,
		"no realm":       `,"connectors":{"` + connectortest.Name + `":{"name":"x"}}`,
		"bad connector":  `,"connectors":{"Not A Name":{"name":"x","realm":"dev"}}`,
		"cell-scoped":    `,"connectors":{"` + connectortest.Name + `":{"name":"x","realm":"dev","cell":"c"}}`,
		"unknown member": `,"connectors":{"` + connectortest.Name + `":{"name":"x","realm":"dev","extra":1}}`,
	} {
		if _, code = preview(body); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", name, code)
		}
	}
}

// TestCreateFromConfigSendsConnectorMethods: the New Cell flow forwards the
// methods picked for the cell (#1551) and refuses malformed names before the
// daemon sees them.
func TestCreateFromConfigSendsConnectorMethods(t *testing.T) {
	c := devScopes()
	c.configs = []v1beta1.CellConfigDoc{exampleConfig("mine", "", v1beta1.ConnectorResolved)}
	handler := connectorHandler(t, c)
	preview := func(methods string) int {
		w := connectorRequest(t, handler, http.MethodPost, "/api/cells/from-config/preview",
			`{"config":{"name":"mine","realm":"dev"},"name":"cell-1","env":[]`+methods+`}`)
		return w.Code
	}
	if code := preview(""); code != http.StatusOK {
		t.Fatalf("preview status=%d", code)
	}
	if len(c.previews[0].ConnectorMethods) != 0 {
		t.Errorf("no pick sent methods: %+v", c.previews[0].ConnectorMethods)
	}
	if code := preview(`,"methods":{"codex":"api-key"}`); code != http.StatusOK {
		t.Fatalf("preview with a method status=%d", code)
	}
	want := map[string]string{"codex": "api-key"}
	if got := c.previews[1].ConnectorMethods; !reflect.DeepEqual(got, want) {
		t.Errorf("methods = %+v, want %+v", got, want)
	}
	for name, body := range map[string]string{
		"bad connector": `,"methods":{"Not A Name":"api-key"}`,
		"bad method":    `,"methods":{"codex":"../x"}`,
		"empty method":  `,"methods":{"codex":""}`,
		"not a string":  `,"methods":{"codex":1}`,
	} {
		if code := preview(body); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", name, code)
		}
	}
}

// TestConnectorErrorCodes: a render that fails on a connector answers with a
// code the UI acts on, not a generic invalid request.
func TestConnectorErrorCodes(t *testing.T) {
	for err, want := range map[error][2]any{
		errdefs.ErrConnectorUnresolved: {http.StatusConflict, "connector_unresolved"},
		errdefs.ErrConnectorIdentity:   {http.StatusBadRequest, "connector_invalid"},
		errdefs.ErrConnectorUnknown:    {http.StatusBadRequest, "connector_invalid"},
		errdefs.ErrConnectorMethod:     {http.StatusBadRequest, "connector_invalid"},
	} {
		status, code := apiErrorStatus(renderError(err))
		if status != want[0] || code != want[1] {
			t.Errorf("%v: status=%d code=%q, want %v", err, status, code, want)
		}
	}
}

// TestCheckScope_GuardsEveryCoordinate pins the inline path guards: a
// traversal or separator in any coordinate is refused, unset optional
// coordinates stay empty, and set ones are trimmed.
func TestCheckScope_GuardsEveryCoordinate(t *testing.T) {
	got, err := checkScope(scopeRef{Realm: " dev ", Space: " s ", Stack: " k "})
	if err != nil || got != (scopeRef{Realm: "dev", Space: "s", Stack: "k"}) {
		t.Fatalf("checkScope(valid) = %+v, %v", got, err)
	}
	if got, err = checkScope(scopeRef{Realm: "dev"}); err != nil || got != (scopeRef{Realm: "dev"}) {
		t.Fatalf("checkScope(realm only) = %+v, %v", got, err)
	}
	for name, s := range map[string]scopeRef{
		"realm traversal": {Realm: ".."},
		"space traversal": {Realm: "dev", Space: ".."},
		"stack traversal": {Realm: "dev", Space: "s", Stack: "../x"},
		"space separator": {Realm: "dev", Space: "a/b"},
	} {
		if _, err = checkScope(s); !errors.Is(err, errdefs.ErrConfig) {
			t.Errorf("%s: checkScope(%+v) = %v, want ErrConfig", name, s, err)
		}
	}
}

// TestCheckSecretRef_GuardsEveryCoordinate is the same pin for a Secret
// reference.
func TestCheckSecretRef_GuardsEveryCoordinate(t *testing.T) {
	got, err := checkSecretRef(&v1beta1.ContainerSecretRef{Name: " gh ", Realm: "dev", Space: "s"})
	if err != nil || *got != (v1beta1.ContainerSecretRef{Name: "gh", Realm: "dev", Space: "s"}) {
		t.Fatalf("checkSecretRef(valid) = %+v, %v", got, err)
	}
	for name, ref := range map[string]v1beta1.ContainerSecretRef{
		"name traversal":  {Name: "..", Realm: "dev"},
		"realm traversal": {Name: "gh", Realm: "../x"},
		"space traversal": {Name: "gh", Realm: "dev", Space: ".."},
		"stack traversal": {Name: "gh", Realm: "dev", Space: "s", Stack: ".."},
	} {
		if _, err = checkSecretRef(&ref); !errors.Is(err, errdefs.ErrConfig) {
			t.Errorf("%s: checkSecretRef(%+v) = %v, want ErrConfig", name, ref, err)
		}
	}
}
