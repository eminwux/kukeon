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

package cloudlink

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// signed returns a request as the host receives it, signed for hostID.
func signed(t *testing.T, key ed25519.PrivateKey, hostID, target, user string, at time.Time) *http.Request {
	t.Helper()
	out, err := http.NewRequest(http.MethodGet, "http://kukeon-host"+target, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = SignRequest(out, key, hostID, user, at); err != nil {
		t.Fatalf("SignRequest: %v", err)
	}
	in := httptest.NewRequest(http.MethodGet, target, nil)
	in.Header = out.Header.Clone()
	return in
}

func TestVerifyRequest(t *testing.T) {
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(1_800_000_000, 0)

	user, err := VerifyRequest(
		signed(t, key, "h-1", "/api/cells?realm=a%20b", "alice@example.com", now),
		pub,
		"h-1",
		now,
	)
	if err != nil || user != "alice@example.com" {
		t.Fatalf("valid signature: user=%q err=%v", user, err)
	}

	cases := map[string]struct {
		req  *http.Request
		want error
	}{
		"missing":    {httptest.NewRequest(http.MethodGet, "/api/cells", nil), ErrIdentityMissing},
		"other key":  {signed(t, otherKey, "h-1", "/api/cells", "alice", now), ErrIdentityInvalid},
		"other host": {signed(t, key, "h-2", "/api/cells", "alice", now), ErrIdentityInvalid},
		"too old": {
			signed(t, key, "h-1", "/api/cells", "alice", now.Add(-MaxClockSkew-time.Minute)),
			ErrIdentityInvalid,
		},
		"in the future": {
			signed(t, key, "h-1", "/api/cells", "alice", now.Add(MaxClockSkew+time.Minute)),
			ErrIdentityInvalid,
		},
	}
	moved := signed(t, key, "h-1", "/api/cells", "alice", now)
	moved.RequestURI = "/api/secrets"
	cases["other path"] = struct {
		req  *http.Request
		want error
	}{moved, ErrIdentityInvalid}
	renamed := signed(t, key, "h-1", "/api/cells", "alice", now)
	renamed.Header.Set(HeaderUser, "mallory")
	cases["other user"] = struct {
		req  *http.Request
		want error
	}{renamed, ErrIdentityInvalid}
	doubled := signed(t, key, "h-1", "/api/cells", "alice", now)
	doubled.Header.Add(HeaderUser, "alice")
	cases["repeated header"] = struct {
		req  *http.Request
		want error
	}{doubled, ErrIdentityInvalid}
	partial := signed(t, key, "h-1", "/api/cells", "alice", now)
	partial.Header.Del(HeaderSignature)
	cases["no signature"] = struct {
		req  *http.Request
		want error
	}{partial, ErrIdentityInvalid}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, verifyErr := VerifyRequest(tc.req, pub, "h-1", now); !errors.Is(verifyErr, tc.want) {
				t.Fatalf("VerifyRequest = %v, want %v", verifyErr, tc.want)
			}
		})
	}
}

func TestSignRequestRejectsUnsafeUser(t *testing.T) {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	req, _ := http.NewRequest(http.MethodGet, "http://kukeon-host/api/x", nil)
	for _, user := range []string{"", "two words", "line\nbreak", string(make([]byte, 257))} {
		if err := SignRequest(req, key, "h-1", user, time.Now()); err == nil {
			t.Errorf("SignRequest accepted user %q", user)
		}
	}
}
