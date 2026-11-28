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
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The reference relay's routes, relative to its URL.
const (
	// LinkPath is the WebSocket endpoint hosts dial.
	LinkPath = "/v1/link"
	// HostsPath prefixes the user-facing proxy: /v1/hosts/<host ID>/api/...
	// reaches the host's /api/....
	HostsPath = "/v1/hosts/"
)

const (
	// DefaultTokenTTL is how long an enrollment token stays usable.
	DefaultTokenTTL = time.Hour
	// hostCertValidity is the lifetime of an issued host certificate.
	hostCertValidity = 365 * 24 * time.Hour
	// caValidity is the lifetime of the relay's host CA.
	caValidity = 10 * hostCertValidity
	// serialBits sizes certificate serial numbers.
	serialBits = 127
	// tokenBytes and hostIDBytes size the random part of tokens and host IDs.
	tokenBytes  = 24
	hostIDBytes = 8
	// maxEnrollRequest bounds the enrollment request body.
	maxEnrollRequest = 64 << 10
	// streamHost is the Host of a request the relay forwards on a link.
	streamHost = "kukeon-host"
)

// Relay is the reference relay: it enrolls hosts, accepts their links and
// proxies authenticated users' requests to a host's /api over its link,
// signing the user's identity. It keeps its state in memory.
//
// Serve Handler over TLS configured by ConfigureTLS: the link endpoint
// requires a host's client certificate.
type Relay struct {
	// Authenticate returns the user a proxied request belongs to; an error
	// refuses it with 401. It only identifies the user: whether that user may
	// reach the host the request names is Authorize's decision. Required to
	// serve the proxy.
	Authenticate func(*http.Request) (string, error)
	// Authorize decides whether user may reach host hostID, the <host>
	// segment of /v1/hosts/<host>/api/...; an error refuses the request with
	// 403 before the relay opens a stream to the host. A user who reaches a
	// host drives its whole /api, cell attach and exec included, so a
	// production relay must check here that the host belongs to the user.
	// Required to serve the proxy: without it the relay refuses every
	// proxied request rather than let any authenticated user reach every
	// linked host.
	Authorize func(user, hostID string) error
	// LinkURL is the wss:// URL enrolled hosts are told to dial. When empty
	// it is derived from the enrollment request's Host.
	LinkURL string
	// Logger receives relay events; nil discards them.
	Logger *slog.Logger

	now         func() time.Time
	ca          *x509.Certificate
	caKey       *ecdsa.PrivateKey
	identityKey ed25519.PrivateKey
	identityPub ed25519.PublicKey

	mu     sync.Mutex
	tokens map[string]time.Time
	hosts  map[string]*Session
	waits  map[string][]chan struct{}
}

// NewRelay creates a relay with a fresh host CA and identity key.
func NewRelay() (*Relay, error) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               pkix.Name{CommonName: "kukeon cloud link host CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	identityPub, identityKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Relay{
		now:         time.Now,
		ca:          ca,
		caKey:       caKey,
		identityKey: identityKey,
		identityPub: identityPub,
		tokens:      map[string]time.Time{},
		hosts:       map[string]*Session{},
		waits:       map[string][]chan struct{}{},
	}, nil
}

func randomSerial() *big.Int {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), serialBits))
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return serial
}

func randomHex(n int) string {
	buf := make([]byte, n)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func (r *Relay) logger() *slog.Logger {
	if r.Logger == nil {
		return slog.New(slog.DiscardHandler)
	}
	return r.Logger
}

// IdentityKey is the private key that signs forwarded identities; a test can
// use it to sign a request by hand.
func (r *Relay) IdentityKey() ed25519.PrivateKey { return r.identityKey }

// ClientCAs is the pool of the CA that signs host certificates.
func (r *Relay) ClientCAs() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(r.ca)
	return pool
}

// ConfigureTLS makes cfg request and verify host client certificates. The
// certificate is optional at the TLS layer so users reach the proxy without
// one; the link endpoint refuses a connection that did not present one.
func (r *Relay) ConfigureTLS(cfg *tls.Config) {
	cfg.ClientAuth = tls.VerifyClientCertIfGiven
	cfg.ClientCAs = r.ClientCAs()
}

// NewEnrollmentToken issues a one-time token valid for ttl (DefaultTokenTTL
// when ttl <= 0).
func (r *Relay) NewEnrollmentToken(ttl time.Duration) string {
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	token := "kct_" + randomHex(tokenBytes)
	r.mu.Lock()
	r.tokens[token] = r.now().Add(ttl)
	r.mu.Unlock()
	return token
}

// consumeToken spends token; false when it is unknown, spent or expired.
func (r *Relay) consumeToken(token string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for known, expires := range r.tokens {
		if subtle.ConstantTimeCompare([]byte(known), []byte(token)) == 1 {
			delete(r.tokens, known)
			return r.now().Before(expires)
		}
	}
	return false
}

// Connected reports whether host hostID has a link up.
func (r *Relay) Connected(hostID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.hosts[hostID]
	return ok
}

// WaitConnected returns a channel closed once host hostID has a link up.
func (r *Relay) WaitConnected(hostID string) <-chan struct{} {
	ch := make(chan struct{})
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[hostID]; ok {
		close(ch)
		return ch
	}
	r.waits[hostID] = append(r.waits[hostID], ch)
	return ch
}

// Disconnect drops host hostID's link, as a network failure would; the host
// reconnects.
func (r *Relay) Disconnect(hostID string) bool {
	r.mu.Lock()
	session := r.hosts[hostID]
	delete(r.hosts, hostID)
	r.mu.Unlock()
	if session == nil {
		return false
	}
	_ = session.Close()
	return true
}

// Handler serves the enrollment endpoint, the link endpoint and the
// user-facing proxy.
func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+EnrollPath, r.enrollHandler)
	mux.HandleFunc("GET "+LinkPath, r.linkHandler)
	mux.HandleFunc(HostsPath, r.proxyHandler)
	return mux
}

func writeRelayError(w http.ResponseWriter, status int, code, message string) {
	var body errorResponse
	body.Error.Code, body.Error.Message = code, message
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (r *Relay) enrollHandler(w http.ResponseWriter, req *http.Request) {
	var body EnrollRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxEnrollRequest)).Decode(&body); err != nil {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "the enrollment request is not JSON")
		return
	}
	if !r.consumeToken(body.Token) {
		writeRelayError(w, http.StatusUnauthorized, "invalid_token",
			"the enrollment token is unknown, already used or expired")
		return
	}
	block, _ := pem.Decode([]byte(body.CSR))
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "csr is not a PEM certificate request")
		return
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err == nil {
		err = csr.CheckSignature()
	}
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, "invalid_request", "csr does not verify: "+err.Error())
		return
	}
	hostID := "h-" + randomHex(hostIDBytes)
	now := r.now()
	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      pkix.Name{CommonName: hostID},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(hostCertValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}, r.ca, csr.PublicKey, r.caKey)
	if err != nil {
		writeRelayError(w, http.StatusInternalServerError, "internal", "issue certificate: "+err.Error())
		return
	}
	linkURL := r.LinkURL
	if linkURL == "" {
		linkURL = "wss://" + req.Host + LinkPath
	}
	r.logger().Info("cloud relay enrolled host", "host", hostID, "hostname", body.Hostname)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(EnrollResponse{
		HostID:      hostID,
		Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		IdentityKey: base64.StdEncoding.EncodeToString(r.identityPub),
		LinkURL:     linkURL,
	})
}

func (r *Relay) linkHandler(w http.ResponseWriter, req *http.Request) {
	if req.TLS == nil || len(req.TLS.VerifiedChains) == 0 || len(req.TLS.VerifiedChains[0]) == 0 {
		writeRelayError(w, http.StatusUnauthorized, "client_certificate_required",
			"the link needs the host's enrolled client certificate")
		return
	}
	hostID := req.TLS.VerifiedChains[0][0].Subject.CommonName
	if hostID == "" {
		writeRelayError(w, http.StatusForbidden, "invalid_certificate", "the client certificate names no host")
		return
	}
	// Hosts dial the link; the browser Origin check does not apply.
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		return
	}
	session := NewSession(conn, false)
	r.mu.Lock()
	previous := r.hosts[hostID]
	r.hosts[hostID] = session
	waits := r.waits[hostID]
	delete(r.waits, hostID)
	r.mu.Unlock()
	for _, ch := range waits {
		close(ch)
	}
	if previous != nil {
		_ = previous.Close()
	}
	r.logger().Info("cloud relay link up", "host", hostID)
	<-session.Done()
	r.mu.Lock()
	if r.hosts[hostID] == session {
		delete(r.hosts, hostID)
	}
	r.mu.Unlock()
	r.logger().Info("cloud relay link down", "host", hostID, "error", session.Err())
}

// proxyHandler forwards /v1/hosts/<id>/api/... to the host's /api/... over
// its link, with the authenticated user signed into the request.
func (r *Relay) proxyHandler(w http.ResponseWriter, req *http.Request) {
	if r.Authenticate == nil {
		writeRelayError(w, http.StatusNotImplemented, "no_authenticator", "the relay has no user authentication")
		return
	}
	if r.Authorize == nil {
		writeRelayError(w, http.StatusNotImplemented, "no_authorizer", "the relay has no host authorization")
		return
	}
	escaped := strings.TrimPrefix(req.URL.EscapedPath(), HostsPath)
	hostID, rest, found := strings.Cut(escaped, "/")
	if hostID == "" || !found || !strings.HasPrefix(rest, "api/") {
		writeRelayError(w, http.StatusNotFound, "not_found", "the path is not /v1/hosts/<host>/api/...")
		return
	}
	user, err := r.Authenticate(req)
	if err != nil {
		writeRelayError(w, http.StatusUnauthorized, "unauthenticated", err.Error())
		return
	}
	// Authorize before looking the host up, so a user cannot tell which
	// hosts they may not reach are online.
	if err = r.Authorize(user, hostID); err != nil {
		writeRelayError(w, http.StatusForbidden, "forbidden", err.Error())
		return
	}
	r.mu.Lock()
	session := r.hosts[hostID]
	r.mu.Unlock()
	if session == nil {
		writeRelayError(w, http.StatusServiceUnavailable, "host_offline", "host "+hostID+" has no link up")
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = streamHost
			pr.Out.Host = streamHost
			pr.Out.URL.RawPath = "/" + rest
			pr.Out.URL.Path = "/" + strings.TrimPrefix(pr.In.URL.Path, HostsPath+hostID+"/")
			// The user's relay credentials stay at the relay.
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("Authorization")
			StripIdentity(pr.Out.Header)
			if signErr := SignRequest(pr.Out, r.identityKey, hostID, user, r.now()); signErr != nil {
				r.logger().Warn("cloud relay could not sign identity", "user", user, "error", signErr)
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return session.Open(ctx)
			},
			DisableKeepAlives: true,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeRelayError(w, http.StatusBadGateway, "link_failed", "the host's link failed: "+err.Error())
		},
	}
	proxy.ServeHTTP(w, req)
}

// ErrNoUser is an Authenticate failure for a request without credentials.
var ErrNoUser = errors.New("no user credentials")

// HeaderAuthenticator is an Authenticate for tests: the user is the value of
// header name. Never use it in production.
func HeaderAuthenticator(name string) func(*http.Request) (string, error) {
	return func(req *http.Request) (string, error) {
		user := req.Header.Get(name)
		if user == "" {
			return "", fmt.Errorf("%w: no %s header", ErrNoUser, name)
		}
		return user, nil
	}
}

// AllowAnyHost is an Authorize for tests: every user may reach every host.
// Never use it in production.
func AllowAnyHost(string, string) error { return nil }
