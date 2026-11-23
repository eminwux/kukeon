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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// secretClient stands in for kukeond behind the secret write route: the
// stored Secrets, keyed by their full coordinates including the cell, and
// every write it received.
type secretClient struct {
	kukeonv1.FakeClient

	stored    map[string]v1beta1.SecretSpec
	created   []v1beta1.SecretDoc
	createErr error
}

func (c *secretClient) Close() error { return nil }

func secretCoordKey(md v1beta1.SecretMetadata) string {
	return strings.Join([]string{md.Realm, md.Space, md.Stack, md.Cell, md.Name}, "/")
}

func (c *secretClient) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	spec, ok := c.stored[secretCoordKey(doc.Metadata)]
	doc.Spec = spec
	return kukeonv1.GetSecretResult{Secret: doc, MetadataExists: ok}, nil
}

func (c *secretClient) CreateSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	if c.createErr != nil {
		return kukeonv1.CreateSecretResult{}, c.createErr
	}
	_, existed := c.stored[secretCoordKey(doc.Metadata)]
	c.created = append(c.created, doc)
	if c.stored == nil {
		c.stored = map[string]v1beta1.SecretSpec{}
	}
	c.stored[secretCoordKey(doc.Metadata)] = doc.Spec
	return kukeonv1.CreateSecretResult{Secret: doc, Created: !existed}, nil
}

func secretHandler(t *testing.T, c *secretClient) http.Handler {
	t.Helper()
	handler, err := newHandler("http://localhost:5129", "test", func(context.Context) (kukeonv1.Client, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func postSecret(t *testing.T, c *secretClient, path, body string) (int, string) {
	t.Helper()
	w := connectorRequest(t, secretHandler(t, c), http.MethodPost, "/api/catalog/secrets/"+path, body)
	return w.Code, w.Body.String()
}

// testKey is a PEM-shaped private key as it appears inside a JSON string.
const testKey = `-----BEGIN OPENSSH PRIVATE KEY-----\nmaterial-one\n-----END OPENSSH PRIVATE KEY-----`

// TestCatalogSecretCreate: New Secret stores an untagged Secret of each type
// at each level and never returns its material.
func TestCatalogSecretCreate(t *testing.T) {
	for name, tc := range map[string]struct {
		path, body string
		want       v1beta1.SecretMetadata
		check      func(v1beta1.SecretSpec) bool
		wantType   string
	}{
		"opaque at realm": {
			"token?realm=dev", `{"value":"material-one\n"}`,
			v1beta1.SecretMetadata{Name: "token", Realm: "dev"},
			func(s v1beta1.SecretSpec) bool { return s.Data == "material-one" && s.Type == "" },
			v1beta1.SecretTypeOpaque,
		},
		"opaque at space": {
			"token?realm=dev&space=web", `{"type":"opaque","value":"material-one"}`,
			v1beta1.SecretMetadata{Name: "token", Realm: "dev", Space: "web"},
			func(s v1beta1.SecretSpec) bool { return s.Data == "material-one" },
			v1beta1.SecretTypeOpaque,
		},
		"opaque at stack": {
			"token?realm=dev&space=web&stack=api", `{"value":"material-one"}`,
			v1beta1.SecretMetadata{Name: "token", Realm: "dev", Space: "web", Stack: "api"},
			func(s v1beta1.SecretSpec) bool { return s.Data == "material-one" },
			v1beta1.SecretTypeOpaque,
		},
		"basic-auth": {
			"login?realm=dev", `{"type":"basic-auth","username":" bot ","password":"material-one"}`,
			v1beta1.SecretMetadata{Name: "login", Realm: "dev"},
			func(s v1beta1.SecretSpec) bool {
				return s.Type == v1beta1.SecretTypeBasicAuth && s.BasicAuth != nil &&
					s.BasicAuth.Username == "bot" && s.BasicAuth.Password == "material-one"
			},
			v1beta1.SecretTypeBasicAuth,
		},
		"ssh-key": {
			"deploy?realm=dev", `{"type":"ssh-key","privateKey":"` + testKey + `"}`,
			v1beta1.SecretMetadata{Name: "deploy", Realm: "dev"},
			func(s v1beta1.SecretSpec) bool {
				return s.Type == v1beta1.SecretTypeSSHKey && s.SSHKey != nil &&
					s.SSHKey.PrivateKey == strings.ReplaceAll(testKey, `\n`, "\n")+"\n"
			},
			v1beta1.SecretTypeSSHKey,
		},
	} {
		c := &secretClient{}
		code, body := postSecret(t, c, tc.path, tc.body)
		if code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, code, body)
		}
		if strings.Contains(body, "material-one") {
			t.Errorf("%s: response echoed material: %s", name, body)
		}
		if len(c.created) != 1 {
			t.Fatalf("%s: created %d secrets", name, len(c.created))
		}
		doc := c.created[0]
		if doc.Metadata != tc.want || doc.Spec.Connector != "" || len(doc.Spec.Identity) != 0 || !tc.check(doc.Spec) {
			t.Errorf("%s: created %+v", name, doc)
		}
		if !strings.Contains(body, `"action":"created"`) || !strings.Contains(body, `"type":"`+tc.wantType+`"`) {
			t.Errorf("%s: result %s", name, body)
		}
	}
}

// TestCatalogSecretCreateExisting: create never overwrites.
func TestCatalogSecretCreateExisting(t *testing.T) {
	c := &secretClient{stored: map[string]v1beta1.SecretSpec{"dev////token": {Data: "old"}}}
	code, body := postSecret(t, c, "token?realm=dev", `{"value":"material-one"}`)
	if code != http.StatusConflict || !strings.Contains(body, `"already_exists"`) {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if len(c.created) != 0 || strings.Contains(body, "material-one") {
		t.Errorf("created=%v body=%s", c.created, body)
	}
}

// TestCatalogSecretReplace: replace keeps the scope and type, refuses a
// missing or connector-tagged Secret and a type change.
func TestCatalogSecretReplace(t *testing.T) {
	stored := func() *secretClient {
		return &secretClient{stored: map[string]v1beta1.SecretSpec{
			"dev////token":          {Data: "old"},
			"dev////login":          {Type: v1beta1.SecretTypeBasicAuth},
			"dev////tagged":         {Data: "old", Connector: "some-connector"},
			"dev/web/api/cell/sock": {Data: "old"},
		}}
	}

	c := stored()
	code, body := postSecret(t, c, "token?realm=dev", `{"replace":true,"value":"material-two"}`)
	if code != http.StatusOK || !strings.Contains(body, `"action":"updated"`) || strings.Contains(body, "material-two") {
		t.Fatalf("replace opaque: status=%d body=%s", code, body)
	}
	if c.created[0].Spec.Data != "material-two" || c.created[0].Spec.Connector != "" {
		t.Errorf("replace stored %+v", c.created[0].Spec)
	}

	c = stored()
	code, body = postSecret(t, c, "login?realm=dev", `{"replace":true,"username":"bot","password":"material-two"}`)
	if code != http.StatusOK || c.created[0].Spec.Type != v1beta1.SecretTypeBasicAuth {
		t.Fatalf("replace keeps the stored type: status=%d body=%s", code, body)
	}

	c = stored()
	code, body = postSecret(t, c, "sock?realm=dev&space=web&stack=api&cell=cell", `{"replace":true,"value":"material-two"}`)
	if code != http.StatusOK || !strings.Contains(body, `"action":"updated"`) {
		t.Fatalf("replace cell-scoped: status=%d body=%s", code, body)
	}
	if got := c.created[0].Metadata; got != (v1beta1.SecretMetadata{
		Name: "sock", Realm: "dev", Space: "web", Stack: "api", Cell: "cell",
	}) {
		t.Errorf("cell coordinate lost: %+v", got)
	}

	c = stored()
	code, body = postSecret(t, c, "missing?realm=dev", `{"replace":true,"value":"material-two"}`)
	if code != http.StatusNotFound || !strings.Contains(body, `"not_found"`) || len(c.created) != 0 {
		t.Errorf("replace missing: status=%d body=%s", code, body)
	}

	c = stored()
	code, body = postSecret(t, c, "tagged?realm=dev", `{"replace":true,"value":"material-two"}`)
	if code != http.StatusBadRequest || !strings.Contains(body, "some-connector") || len(c.created) != 0 {
		t.Errorf("replace tagged: status=%d body=%s", code, body)
	}

	c = stored()
	code, body = postSecret(t, c, "token?realm=dev", `{"replace":true,"type":"ssh-key","privateKey":"material-two"}`)
	if code != http.StatusBadRequest || len(c.created) != 0 || strings.Contains(body, "material-two") {
		t.Errorf("replace with another type: status=%d body=%s", code, body)
	}
}

// TestCatalogSecretRejections: a bad request is a 400 invalid_request that
// writes nothing and never quotes the material.
func TestCatalogSecretRejections(t *testing.T) {
	const material = "leak-me-material"
	for name, tc := range map[string]struct{ path, body string }{
		"missing material":   {"token?realm=dev", `{"type":"opaque"}`},
		"blank value":        {"token?realm=dev", `{"value":"\n"}`},
		"basic-auth no user": {"token?realm=dev", `{"type":"basic-auth","password":"` + material + `"}`},
		"unknown type":       {"token?realm=dev", `{"type":"tls","value":"` + material + `"}`},
		"reserved name":      {".types?realm=dev", `{"value":"` + material + `"}`},
		"dot name":           {".hidden?realm=dev", `{"value":"` + material + `"}`},
		"slash name":         {"a%2Fb?realm=dev", `{"value":"` + material + `"}`},
		"no realm":           {"token", `{"value":"` + material + `"}`},
		"scope gap":          {"token?realm=dev&stack=api", `{"value":"` + material + `"}`},
		"create at cell":     {"token?realm=dev&space=web&stack=api&cell=c", `{"value":"` + material + `"}`},
		"unknown field":      {"token?realm=dev", `{"secret":"` + material + `"}`},
		"two documents":      {"token?realm=dev", `{"value":"` + material + `"}{}`},
		"malformed":          {"token?realm=dev", `{"value":"` + material + `"`},
	} {
		c := &secretClient{}
		code, body := postSecret(t, c, tc.path, tc.body)
		if code != http.StatusBadRequest || !strings.Contains(body, `"invalid_request"`) {
			t.Errorf("%s: status=%d body=%s", name, code, body)
		}
		if strings.Contains(body, material) {
			t.Errorf("%s: echoed material: %s", name, body)
		}
		if len(c.created) != 0 {
			t.Errorf("%s: stored a rejected secret", name)
		}
	}

}

// TestCatalogSecretNonJSON: a request without the JSON content type is a 400.
func TestCatalogSecretNonJSON(t *testing.T) {
	c := &secretClient{}
	handler := secretHandler(t, c)
	req := connectorRequest(t, handler, http.MethodPost, "/api/catalog/secrets/token?realm=dev", `{"value":"x"}`)
	if req.Code != http.StatusOK {
		t.Fatalf("baseline: %d %s", req.Code, req.Body)
	}
	form := httptest.NewRequest(http.MethodPost, "http://localhost:5129/api/catalog/secrets/other?realm=dev",
		strings.NewReader("value=x"))
	form.Header.Set("Origin", "http://localhost:5129")
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, form)
	if w.Code != http.StatusBadRequest || len(c.created) != 1 {
		t.Errorf("form post: status=%d created=%d", w.Code, len(c.created))
	}
}

// TestCatalogSecretDaemonErrorRedacted: a daemon failure that quotes the
// value reaches the browser without it.
func TestCatalogSecretDaemonErrorRedacted(t *testing.T) {
	const material = "leak-me-material"
	c := &secretClient{createErr: errors.New("write failed for " + material)}
	code, body := postSecret(t, c, "token?realm=dev", `{"value":"`+material+`"}`)
	if code == http.StatusOK || strings.Contains(body, material) {
		t.Fatalf("status=%d body=%s", code, body)
	}
	if !strings.Contains(body, "[redacted]") {
		t.Errorf("redaction marker missing: %s", body)
	}
}
