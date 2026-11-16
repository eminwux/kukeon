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
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const storedToken = "stored-secret-token"

type credentialClient struct {
	kukeonv1.FakeClient

	realm    v1beta1.RealmDoc
	missing  bool
	getErr   error
	applyErr error
	result   kukeonv1.ApplyDocumentsResult
	applied  []byte
}

func (c *credentialClient) Close() error { return nil }

func (c *credentialClient) GetRealm(context.Context, v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	if c.getErr != nil {
		return kukeonv1.GetRealmResult{}, c.getErr
	}
	return kukeonv1.GetRealmResult{Realm: c.realm, MetadataExists: !c.missing}, nil
}

func (c *credentialClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return []v1beta1.RealmDoc{c.realm}, nil
}

func (c *credentialClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	c.applied = raw
	if c.applyErr != nil {
		return kukeonv1.ApplyDocumentsResult{}, c.applyErr
	}
	if c.result.Resources == nil {
		return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
			{Kind: string(v1beta1.KindRealm), Name: c.realm.Metadata.Name, Action: "updated"},
		}}, nil
	}
	return c.result, nil
}

func credentialRealm() v1beta1.RealmDoc {
	return v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: "dev"},
		Spec: v1beta1.RealmSpec{
			Namespace: "dev.kukeon.io",
			RegistryCredentials: []v1beta1.RegistryCredentials{
				{Username: "bot", Password: storedToken, ServerAddress: "ghcr.io"},
				{Username: "fallback", Password: storedToken + "-2"},
			},
		},
		Status: v1beta1.RealmStatus{State: v1beta1.RealmStateReady},
	}
}

func serveCredential(
	t *testing.T,
	c *credentialClient,
	method, url, body string,
	mutate func(*http.Request),
) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler(
		"http://localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "http://localhost:5129"+url, strings.NewReader(body))
	if method == http.MethodPost {
		req.Header.Set("Origin", "http://localhost:5129")
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	return w
}

func appliedCredentials(t *testing.T, raw []byte) []v1beta1.RegistryCredentials {
	t.Helper()
	var doc v1beta1.RealmDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status.State == v1beta1.RealmStateReady || doc.Kind != v1beta1.KindRealm {
		t.Fatalf("applied doc kind=%q status=%v", doc.Kind, doc.Status)
	}
	return doc.Spec.RegistryCredentials
}

func TestRealmResponsesRedactRegistryPasswords(t *testing.T) {
	for _, url := range []string{"/api/realms", "/api/realms/dev"} {
		c := &credentialClient{realm: credentialRealm()}
		w := serveCredential(t, c, http.MethodGet, url, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", url, w.Code, w.Body)
		}
		body := w.Body.String()
		if strings.Contains(body, storedToken) {
			t.Fatalf("%s leaked token: %s", url, body)
		}
		if !strings.Contains(body, `"serverAddress":"ghcr.io"`) || !strings.Contains(body, `"username":"fallback"`) {
			t.Fatalf("%s dropped server or username: %s", url, body)
		}
		// The client's document is not mutated by redaction.
		if c.realm.Spec.RegistryCredentials[0].Password != storedToken {
			t.Fatal("redaction mutated the source document")
		}
	}
}

func TestRegistryCredentialReplacesSameServerAndPreservesOthers(t *testing.T) {
	c := &credentialClient{realm: credentialRealm()}
	w := serveCredential(t, c, http.MethodPost, "/api/realms/dev/registry-credentials",
		`{"server":" ghcr.io ","username":"robot","password":"new-token\n"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "new-token") {
		t.Fatalf("response echoed token: %s", w.Body)
	}
	if !strings.Contains(w.Body.String(), `"action":"updated"`) {
		t.Fatalf("body=%s", w.Body)
	}
	creds := appliedCredentials(t, c.applied)
	want := []v1beta1.RegistryCredentials{
		{Username: "robot", Password: "new-token", ServerAddress: "ghcr.io"},
		{Username: "fallback", Password: storedToken + "-2"},
	}
	if fmt.Sprint(creds) != fmt.Sprint(want) {
		t.Fatalf("applied=%v want=%v", creds, want)
	}
}

func TestRegistryCredentialAppendsAndUpsertsEmptyServerDefault(t *testing.T) {
	c := &credentialClient{realm: credentialRealm()}
	w := serveCredential(t, c, http.MethodPost, "/api/realms/dev/registry-credentials",
		`{"server":"registry.example.com:5000","username":"u","password":"p"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	if creds := appliedCredentials(t, c.applied); len(creds) != 3 ||
		creds[2].ServerAddress != "registry.example.com:5000" {
		t.Fatalf("applied=%v", creds)
	}

	c = &credentialClient{realm: credentialRealm()}
	w = serveCredential(t, c, http.MethodPost, "/api/realms/dev/registry-credentials",
		`{"server":"","username":"default-user","password":"p"}`, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	creds := appliedCredentials(t, c.applied)
	if len(creds) != 2 || creds[1].Username != "default-user" || creds[0].Password != storedToken {
		t.Fatalf("applied=%v", creds)
	}
}

func TestRegistryCredentialRejectsInvalidRequestsWithoutEchoingToken(t *testing.T) {
	const token = "leak-me-token"
	for name, tc := range map[string]struct {
		body   string
		mutate func(*http.Request)
	}{
		"missing username": {body: `{"server":"ghcr.io","password":"` + token + `"}`},
		"missing token":    {body: `{"server":"ghcr.io","username":"u"}`},
		"server with path": {body: `{"server":"ghcr.io/org","username":"u","password":"` + token + `"}`},
		"unknown field":    {body: `{"username":"u","password":"` + token + `","token":"` + token + `"}`},
		"malformed":        {body: `{"username":"u","password":"` + token + `"`},
		"two documents":    {body: `{"username":"u","password":"` + token + `"}{}`},
		"cross origin": {
			body:   `{"username":"u","password":"` + token + `"}`,
			mutate: func(r *http.Request) { r.Header.Set("Origin", "http://evil.test") },
		},
		"not json": {
			body:   `{"username":"u","password":"` + token + `"}`,
			mutate: func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") },
		},
	} {
		c := &credentialClient{realm: credentialRealm()}
		w := serveCredential(t, c, http.MethodPost, "/api/realms/dev/registry-credentials", tc.body, tc.mutate)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusForbidden {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Errorf("%s: echoed token: %s", name, w.Body)
		}
		if c.applied != nil {
			t.Errorf("%s: applied an invalid request", name)
		}
	}
}

func TestRegistryCredentialErrorsAreClassifiedAndRedacted(t *testing.T) {
	const token = "apply-secret"
	body := `{"server":"ghcr.io","username":"u","password":"` + token + `"}`
	for name, tc := range map[string]struct {
		client *credentialClient
		status int
		code   string
	}{
		"missing realm": {
			client: &credentialClient{realm: credentialRealm(), missing: true},
			status: http.StatusNotFound, code: "not_found",
		},
		"permission": {
			client: &credentialClient{realm: credentialRealm(), getErr: fmt.Errorf("dial: %w", fs.ErrPermission)},
			status: http.StatusForbidden, code: "permission_denied",
		},
		"apply error": {
			client: &credentialClient{realm: credentialRealm(), applyErr: errors.New("rejected password " + token)},
			status: http.StatusServiceUnavailable, code: "unavailable",
		},
		"apply failed resource": {
			client: &credentialClient{realm: credentialRealm(), result: kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: string(v1beta1.KindRealm), Name: "dev", Action: "failed", Error: "bad " + token},
				},
			}},
			status: http.StatusServiceUnavailable, code: "unavailable",
		},
	} {
		w := serveCredential(t, tc.client, http.MethodPost, "/api/realms/dev/registry-credentials", body, nil)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), `"code":"`+tc.code+`"`) {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body)
		}
		if strings.Contains(w.Body.String(), token) {
			t.Errorf("%s: echoed token: %s", name, w.Body)
		}
	}
}
