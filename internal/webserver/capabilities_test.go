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
	"regexp"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

func TestCapabilitiesAPI(t *testing.T) {
	dialed := false
	handler, err := newHandler("http://localhost:5129", "unix:///test", func(context.Context) (kukeonv1.Client, error) {
		dialed = true
		return nil, errors.New("capabilities must not dial kukeond")
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129/api/capabilities", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d: %s", w.Code, w.Body.String())
	}
	if dialed {
		t.Fatal("GET /api/capabilities dialed kukeond")
	}
	var got struct {
		Version  *string  `json:"version"`
		Features []string `json:"features"`
	}
	if decodeErr := json.Unmarshal(w.Body.Bytes(), &got); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if got.Version == nil || *got.Version != config.Version {
		t.Fatalf("version = %v, want %q", got.Version, config.Version)
	}
	// The existing API areas are listed under stable, versioned identifiers.
	want := []string{"teams.v1", "attach.v1", "exec.v1", "health.v1"}
	listed := map[string]bool{}
	id := regexp.MustCompile(`^[a-z][a-z0-9-]*\.v[1-9][0-9]*$`)
	for _, feature := range got.Features {
		if listed[feature] {
			t.Fatalf("feature %q listed twice: %v", feature, got.Features)
		}
		if !id.MatchString(feature) {
			t.Fatalf("feature %q is not <area>.v<n>", feature)
		}
		listed[feature] = true
	}
	for _, feature := range want {
		if !listed[feature] {
			t.Fatalf("feature %q missing from %v", feature, got.Features)
		}
	}
}

// The capabilities route sits behind the login gate like every other API
// route: an extension reads it after sign-in.
func TestCapabilitiesBehindLoginGate(t *testing.T) {
	f := newAuthFixture(t, "")
	resp := f.do(f.handler, http.MethodGet, "/api/capabilities", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without credentials = %d, want 401", resp.StatusCode)
	}
	cookie := sessionCookie(t, f.login(f.handler, "root", testPassword))
	if got := f.do(f.handler, http.MethodGet, "/api/capabilities", "", withCookie(cookie.Value)); got.StatusCode != http.StatusOK {
		t.Fatalf("status with the session cookie = %d, want 200", got.StatusCode)
	}
}
