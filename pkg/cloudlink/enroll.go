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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EnrollPath is the relay endpoint that exchanges an enrollment token for a
// host credential, relative to the relay URL.
const EnrollPath = "/v1/enroll"

// maxEnrollResponse bounds the enrollment response body.
const maxEnrollResponse = 1 << 20

// enrollTimeout bounds the enrollment exchange of the default client.
const enrollTimeout = 30 * time.Second

// ErrNotEnrolled is a credential that is missing or incomplete.
var ErrNotEnrolled = errors.New("host is not enrolled with a cloud relay")

// EnrollRequest is the body of POST /v1/enroll.
type EnrollRequest struct {
	// Token is the one-time enrollment token.
	Token string `json:"token"`
	// CSR is a PEM certificate request for the host's link key.
	CSR string `json:"csr"`
	// Hostname is the host's name, for the relay's records.
	Hostname string `json:"hostname,omitempty"`
}

// EnrollResponse is the body of a successful enrollment.
type EnrollResponse struct {
	// HostID is the identity the relay gave the host.
	HostID string `json:"hostId"`
	// Certificate is the PEM client certificate for the link.
	Certificate string `json:"certificate"`
	// IdentityKey is the base64 Ed25519 public key that signs user
	// identities on forwarded requests.
	IdentityKey string `json:"identityKey"`
	// LinkURL is the wss:// URL the host dials.
	LinkURL string `json:"linkUrl"`
}

// errorResponse is the body of a failed relay request.
type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Credential is what a host keeps after enrollment.
type Credential struct {
	// HostID is the identity the relay gave the host; its certificate's
	// common name.
	HostID string `json:"hostId"`
	// RelayURL is the relay the host enrolled with.
	RelayURL string `json:"relayUrl"`
	// LinkURL is the wss:// URL the relay asked the host to dial.
	LinkURL string `json:"linkUrl"`
	// Certificate and PrivateKey are the PEM client certificate and its
	// PKCS #8 key.
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
	// IdentityKey is the base64 Ed25519 key that signs user identities.
	IdentityKey string `json:"identityKey"`
	// ServerCA is an optional PEM bundle trusted for the relay's server
	// certificate in addition to the system roots.
	ServerCA string `json:"serverCA,omitempty"`
	// EnrolledAt is when the host enrolled.
	EnrolledAt time.Time `json:"enrolledAt"`
}

// Validate checks that the credential is complete and parses.
func (c *Credential) Validate() error {
	if c == nil || c.HostID == "" || c.Certificate == "" || c.PrivateKey == "" || c.IdentityKey == "" {
		return ErrNotEnrolled
	}
	if _, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey)); err != nil {
		return fmt.Errorf("%w: client certificate: %w", ErrNotEnrolled, err)
	}
	if _, err := c.IdentityPublicKey(); err != nil {
		return err
	}
	if c.ServerCA != "" {
		if _, err := certPool([]byte(c.ServerCA)); err != nil {
			return err
		}
	}
	return nil
}

// IdentityPublicKey decodes IdentityKey.
func (c *Credential) IdentityPublicKey() (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(c.IdentityKey)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: identity key is not a base64 Ed25519 public key", ErrNotEnrolled)
	}
	return ed25519.PublicKey(raw), nil
}

// NotAfter is the client certificate's expiry.
func (c *Credential) NotAfter() (time.Time, error) {
	block, _ := pem.Decode([]byte(c.Certificate))
	if block == nil {
		return time.Time{}, fmt.Errorf("%w: client certificate is not PEM", ErrNotEnrolled)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: client certificate: %w", ErrNotEnrolled, err)
	}
	return cert.NotAfter, nil
}

// ClientTLSConfig is the TLS configuration of the link dial: the client
// certificate, and the system roots plus ServerCA.
func (c *Credential) ClientTLSConfig() (*tls.Config, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	pair, err := tls.X509KeyPair([]byte(c.Certificate), []byte(c.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("%w: client certificate: %w", ErrNotEnrolled, err)
	}
	roots, err := RootsWith([]byte(c.ServerCA))
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: roots, MinVersion: tls.VersionTLS12}, nil
}

// RootsWith returns the system roots plus the PEM certificates in extra.
func RootsWith(extra []byte) (*x509.CertPool, error) {
	roots, err := x509.SystemCertPool()
	if err != nil || roots == nil {
		roots = x509.NewCertPool()
	}
	if len(bytes.TrimSpace(extra)) > 0 && !roots.AppendCertsFromPEM(extra) {
		return nil, errors.New("cloud link: the relay CA bundle has no PEM certificate")
	}
	return roots, nil
}

func certPool(pemData []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, errors.New("cloud link: the relay CA bundle has no PEM certificate")
	}
	return pool, nil
}

// EnrollOptions are the inputs of Enroll.
type EnrollOptions struct {
	// RelayURL is the relay's https:// base URL.
	RelayURL string
	// Token is the one-time enrollment token.
	Token string
	// Hostname is reported to the relay.
	Hostname string
	// ServerCA is an optional PEM bundle trusted for the relay's server
	// certificate in addition to the system roots. It is kept in the
	// credential for the link dial.
	ServerCA string
	// HTTPClient overrides the client; when nil one trusting the system
	// roots plus ServerCA is used.
	HTTPClient *http.Client
	// Now overrides the clock.
	Now func() time.Time
}

// Enroll generates a link key on this host, sends its certificate request
// with the token to the relay, and returns the credential.
func Enroll(ctx context.Context, opts EnrollOptions) (*Credential, error) {
	base, err := parseRelayURL(opts.RelayURL)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return nil, errors.New("cloud link: the enrollment token is empty")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("cloud link: generate link key: %w", err)
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: "kukeon-host"},
	}, key)
	if err != nil {
		return nil, fmt.Errorf("cloud link: create certificate request: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("cloud link: encode link key: %w", err)
	}
	client, err := enrollClient(opts)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(EnrollRequest{
		Token:    token,
		CSR:      string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})),
		Hostname: opts.Hostname,
	})
	if err != nil {
		return nil, err
	}
	enrolled, err := postEnroll(ctx, client, base, body)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	cred := &Credential{
		HostID:      enrolled.HostID,
		RelayURL:    base,
		LinkURL:     enrolled.LinkURL,
		Certificate: enrolled.Certificate,
		PrivateKey:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
		IdentityKey: enrolled.IdentityKey,
		ServerCA:    opts.ServerCA,
		EnrolledAt:  now().UTC(),
	}
	if err = cred.Validate(); err != nil {
		return nil, fmt.Errorf("cloud link: the relay returned an unusable credential: %w", err)
	}
	if err = checkIssuedFor(cred, &key.PublicKey); err != nil {
		return nil, err
	}
	if cred.LinkURL == "" {
		cred.LinkURL = DefaultLinkURL(base)
	}
	return cred, nil
}

// enrollClient is opts.HTTPClient, or a client trusting the system roots
// plus opts.ServerCA.
func enrollClient(opts EnrollOptions) (*http.Client, error) {
	if opts.HTTPClient != nil {
		return opts.HTTPClient, nil
	}
	roots, err := RootsWith([]byte(opts.ServerCA))
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: enrollTimeout,
		Transport: &http.Transport{
			Proxy:           http.ProxyFromEnvironment,
			TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
		},
	}, nil
}

// postEnroll sends the enrollment request and decodes the answer.
func postEnroll(ctx context.Context, client *http.Client, base string, body []byte) (EnrollResponse, error) {
	var enrolled EnrollResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+EnrollPath, bytes.NewReader(body))
	if err != nil {
		return enrolled, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return enrolled, fmt.Errorf("cloud link: enroll with %s: %w", base, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxEnrollResponse))
	if err != nil {
		return enrolled, fmt.Errorf("cloud link: read enrollment response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var failure errorResponse
		if json.Unmarshal(data, &failure) == nil && failure.Error.Message != "" {
			return enrolled, fmt.Errorf("cloud link: relay refused enrollment (%s): %s",
				resp.Status, failure.Error.Message)
		}
		return enrolled, fmt.Errorf("cloud link: relay refused enrollment: %s", resp.Status)
	}
	if err = json.Unmarshal(data, &enrolled); err != nil {
		return enrolled, fmt.Errorf("cloud link: decode enrollment response: %w", err)
	}
	return enrolled, nil
}

// checkIssuedFor confirms the certificate is for the key generated here and
// names the host ID the relay reported.
func checkIssuedFor(cred *Credential, pub *ecdsa.PublicKey) error {
	block, _ := pem.Decode([]byte(cred.Certificate))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("cloud link: parse issued certificate: %w", err)
	}
	certKey, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !certKey.Equal(pub) {
		return errors.New("cloud link: the relay issued a certificate for another key")
	}
	if cert.Subject.CommonName != cred.HostID {
		return fmt.Errorf("cloud link: the certificate names host %q, the relay reported %q",
			cert.Subject.CommonName, cred.HostID)
	}
	return nil
}

// parseRelayURL accepts an https:// URL with no query or fragment and
// returns it without a trailing slash.
func parseRelayURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("cloud link: relay URL %q is not an absolute URL", raw)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("cloud link: relay URL %q must use https", raw)
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("cloud link: relay URL %q must not carry userinfo, a query or a fragment", raw)
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// DefaultLinkURL is the link URL of a relay that follows the reference
// layout: wss://<relay>/v1/link.
func DefaultLinkURL(relayURL string) string {
	return "wss://" + strings.TrimPrefix(strings.TrimSuffix(relayURL, "/"), "https://") + LinkPath
}
