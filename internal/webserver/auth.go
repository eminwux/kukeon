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
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	// sessionCookieName names the web UI session cookie (issue #1652).
	sessionCookieName = "kukeon_session"
	// sessionCookieMaxAge is 400 days, the longest lifetime browsers keep.
	sessionCookieMaxAge = 400 * 24 * 60 * 60
	// sessionMessagePrefix versions the HMAC input so a later cookie format
	// never verifies against this one.
	sessionMessagePrefix = "kukeon-web-session-v1:"
	// credentialCacheTTL bounds how long a gated request trusts the last
	// read of the login secret: a changed or deleted secret signs every
	// session out within this window.
	credentialCacheTTL = 5 * time.Second
	// maxLoginBody bounds the login request body.
	maxLoginBody = 4 << 10

	// API error codes of the login gate.
	codeUnauthenticated     = "unauthenticated"
	codeInvalidCredentials  = "invalid_credentials" //nolint:gosec // an error code, not a credential
	codeLoginNotInitialized = "login_not_initialized"
)

// loginReadBack is the command that prints the login again.
const loginReadBack = "kuke get secret " + consts.WebUIAdminSecretName +
	" --realm " + consts.KukeSystemRealmName + " -o yaml"

// credential is the username and password of the web-ui-admin secret.
type credential struct {
	username string
	password string
}

// authenticator checks `kuke serve` API requests against the web-ui-admin
// secret (issue #1652). Sessions are stateless: the cookie is an HMAC keyed
// by the password, so a session survives a server restart and stops
// verifying when the password changes. Nothing is stored server-side beyond a
// short cache of the secret.
type authenticator struct {
	dial   Dial
	secure bool
	now    func() time.Time

	mu   sync.Mutex
	cred credential
	read time.Time // zero = no cache
}

func newAuthenticator(dial Dial, secure bool) *authenticator {
	return &authenticator{dial: dial, secure: secure, now: time.Now}
}

// credential returns the login secret's username and password. A cached
// value younger than credentialCacheTTL is reused unless fresh is set; a
// fresh read refreshes the cache. An absent or unusable secret is
// errdefs.ErrLoginNotInitialized; daemon failures pass through.
func (a *authenticator) credential(ctx context.Context, fresh bool) (credential, error) {
	if !fresh {
		a.mu.Lock()
		cred, read := a.cred, a.read
		a.mu.Unlock()
		if !read.IsZero() && a.now().Sub(read) < credentialCacheTTL {
			return cred, nil
		}
	}
	client, err := a.dial(ctx)
	if err != nil {
		return credential{}, err
	}
	defer func() { _ = client.Close() }()
	result, err := client.ReadSecret(ctx, v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{
			Name:  consts.WebUIAdminSecretName,
			Realm: consts.KukeSystemRealmName,
		},
	})
	if err != nil {
		return credential{}, err
	}
	if !result.MetadataExists {
		a.forget()
		return credential{}, errdefs.ErrLoginNotInitialized
	}
	auth := result.Secret.Spec.BasicAuth
	if auth == nil || auth.Username == "" {
		a.forget()
		return credential{}, fmt.Errorf("%w (secret %s is not a basic-auth secret)",
			errdefs.ErrLoginNotInitialized, consts.WebUIAdminSecretName)
	}
	cred := credential{username: auth.Username, password: auth.Password}
	if cred.password == "" {
		cred.password = auth.Token
	}
	if cred.password == "" {
		a.forget()
		return credential{}, errdefs.ErrLoginNotInitialized
	}
	a.mu.Lock()
	a.cred, a.read = cred, a.now()
	a.mu.Unlock()
	return cred, nil
}

func (a *authenticator) forget() {
	a.mu.Lock()
	a.cred, a.read = credential{}, time.Time{}
	a.mu.Unlock()
}

// sessionValue is the session cookie for c: hex HMAC-SHA256 keyed by the
// password over the versioned username.
func sessionValue(c credential) string {
	mac := hmac.New(sha256.New, []byte(c.password))
	_, _ = mac.Write([]byte(sessionMessagePrefix + c.username))
	return hex.EncodeToString(mac.Sum(nil))
}

func equalSecret(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// authResult is the outcome of authenticate.
type authResult struct {
	ok        bool
	viaCookie bool
	cred      credential
}

// requestCredentials returns the session cookie and bearer token a request
// carries; either may be empty.
func requestCredentials(r *http.Request) (string, string) {
	var cookie, bearer string
	if c, err := r.Cookie(sessionCookieName); err == nil {
		cookie = c.Value
	}
	if scheme, token, found := strings.Cut(r.Header.Get("Authorization"), " "); found &&
		strings.EqualFold(scheme, "Bearer") {
		bearer = strings.TrimSpace(token)
	}
	return cookie, bearer
}

// authenticate verifies a request's session cookie or bearer token. A
// request with neither is refused without calling the daemon. A missing
// login secret refuses the request; a daemon failure is returned so the
// caller answers 503, never 200.
func (a *authenticator) authenticate(r *http.Request) (authResult, error) {
	cookie, bearer := requestCredentials(r)
	if cookie == "" && bearer == "" {
		return authResult{}, nil
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	cred, err := a.credential(ctx, false)
	if err != nil {
		if errors.Is(err, errdefs.ErrLoginNotInitialized) {
			return authResult{}, nil
		}
		return authResult{}, err
	}
	if bearer != "" && equalSecret(bearer, cred.password) {
		return authResult{ok: true, cred: cred}, nil
	}
	if cookie != "" && equalSecret(cookie, sessionValue(cred)) {
		return authResult{ok: true, viaCookie: true, cred: cred}, nil
	}
	return authResult{}, nil
}

// gate wraps the API mux: every /api/ route except the three session routes
// needs a valid session cookie or bearer token. Static assets stay public.
func (a *authenticator) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || isSessionRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		result, err := a.authenticate(r)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		if !result.ok {
			writeAPIError(w, errdefs.ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSessionRoute(path string) bool {
	switch path {
	case "/api/session", "/api/login", "/api/logout":
		return true
	}
	return false
}

// sessionInfo is the body of GET /api/session and POST /api/login.
type sessionInfo struct {
	User string `json:"user"`
}

func (a *authenticator) setSessionCookie(w http.ResponseWriter, cred credential) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionValue(cred),
		Path:     "/",
		MaxAge:   sessionCookieMaxAge,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *authenticator) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

// registerSessionRoutes adds GET /api/session, POST /api/login and
// POST /api/logout.
func (a *authenticator) registerSessionRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session", a.sessionHandler)
	mux.HandleFunc("POST /api/login", a.loginHandler)
	mux.HandleFunc("POST /api/logout", a.logoutHandler)
}

// sessionHandler reports the signed-in user and refreshes the cookie's
// lifetime.
func (a *authenticator) sessionHandler(w http.ResponseWriter, r *http.Request) {
	result, err := a.authenticate(r)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	if !result.ok {
		writeAPIError(w, errdefs.ErrUnauthenticated)
		return
	}
	if result.viaCookie {
		a.setSessionCookie(w, result.cred)
	}
	writeJSON(w, http.StatusOK, sessionInfo{User: result.cred.username})
}

// loginHandler checks a username and password against a fresh read of the
// login secret and sets the session cookie.
func (a *authenticator) loginHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSONBody(r, maxLoginBody, &body); err != nil {
		writeAPIError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	cred, err := a.credential(ctx, true)
	if err != nil {
		writeAPIError(w, err)
		return
	}
	userOK := equalSecret(body.Username, cred.username)
	passwordOK := equalSecret(body.Password, cred.password)
	if !userOK || !passwordOK {
		writeAPIError(w, errdefs.ErrInvalidCredentials)
		return
	}
	a.setSessionCookie(w, cred)
	writeJSON(w, http.StatusOK, sessionInfo{User: cred.username})
}

// logoutHandler expires the session cookie in this browser. Sessions are
// stateless, so other browsers stay signed in until the password changes.
func (a *authenticator) logoutHandler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") != jsonContentType {
		writeAPIError(w, fmt.Errorf("%w: JSON request required", errdefs.ErrConfig))
		return
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}
