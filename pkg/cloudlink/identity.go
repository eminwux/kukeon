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
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The headers of a relay-signed user identity.
const (
	// HeaderUser names the user the relay authenticated.
	HeaderUser = "Kukeon-Cloud-User"
	// HeaderTime is the signing time in Unix seconds.
	HeaderTime = "Kukeon-Cloud-Time"
	// HeaderSignature is the base64url (unpadded) Ed25519 signature.
	HeaderSignature = "Kukeon-Cloud-Signature"
)

// MaxClockSkew is how far the signing time may be from the verifier's clock.
const MaxClockSkew = 5 * time.Minute

// identityPrefix versions the signed message.
const identityPrefix = "kukeon-cloud-identity-v1"

var (
	// ErrIdentityMissing is a request without a signed identity.
	ErrIdentityMissing = errors.New("cloud link request has no signed user identity")
	// ErrIdentityInvalid is a request whose signed identity does not verify.
	ErrIdentityInvalid = errors.New("cloud link request identity signature is invalid")
)

// userRE is the accepted user name: 1 to 256 visible ASCII characters, so it
// travels in a header and a log line unchanged.
var userRE = regexp.MustCompile(`^[\x21-\x7e]{1,256}$`)

// ValidUser reports whether user can be signed.
func ValidUser(user string) bool { return userRE.MatchString(user) }

// identityMessage is the signed message. It binds the user to one host, one
// method and one request URI at one time, so a signature cannot be moved to
// another host or request.
func identityMessage(hostID, method, requestURI, user, ts string) []byte {
	return []byte(strings.Join([]string{identityPrefix, hostID, method, requestURI, user, ts}, "\x00"))
}

// SignRequest sets the identity headers on r, a request to host hostID,
// signed with the relay's identity key. The signature covers
// r.URL.RequestURI(), which is the request line an http.Transport sends.
func SignRequest(r *http.Request, key ed25519.PrivateKey, hostID, user string, now time.Time) error {
	if !ValidUser(user) {
		return fmt.Errorf("cloud link: user %q is not 1 to 256 visible ASCII characters", user)
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	sig := ed25519.Sign(key, identityMessage(hostID, r.Method, r.URL.RequestURI(), user, ts))
	r.Header.Set(HeaderUser, user)
	r.Header.Set(HeaderTime, ts)
	r.Header.Set(HeaderSignature, base64.RawURLEncoding.EncodeToString(sig))
	return nil
}

// StripIdentity removes any identity headers from r, as a relay must before
// it signs: a client-supplied identity is never forwarded.
func StripIdentity(h http.Header) {
	h.Del(HeaderUser)
	h.Del(HeaderTime)
	h.Del(HeaderSignature)
}

// VerifyRequest checks the identity headers of r, a request served by host
// hostID, against the relay's public key and returns the user. The request
// URI checked is r.RequestURI, the request line as received.
func VerifyRequest(r *http.Request, pub ed25519.PublicKey, hostID string, now time.Time) (string, error) {
	users, times, sigs := r.Header.Values(HeaderUser), r.Header.Values(HeaderTime), r.Header.Values(HeaderSignature)
	if len(users) == 0 && len(times) == 0 && len(sigs) == 0 {
		return "", ErrIdentityMissing
	}
	if len(users) != 1 || len(times) != 1 || len(sigs) != 1 {
		return "", fmt.Errorf("%w: each identity header must appear once", ErrIdentityInvalid)
	}
	user, ts := users[0], times[0]
	if !ValidUser(user) {
		return "", fmt.Errorf("%w: malformed user", ErrIdentityInvalid)
	}
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return "", fmt.Errorf("%w: malformed time", ErrIdentityInvalid)
	}
	if skew := now.Sub(time.Unix(secs, 0)); skew > MaxClockSkew || skew < -MaxClockSkew {
		return "", fmt.Errorf("%w: signed %s from this host's clock", ErrIdentityInvalid, skew.Round(time.Second))
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigs[0])
	if err != nil || len(sig) != ed25519.SignatureSize || len(pub) != ed25519.PublicKeySize {
		return "", fmt.Errorf("%w: malformed signature", ErrIdentityInvalid)
	}
	uri := r.RequestURI
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	if !ed25519.Verify(pub, identityMessage(hostID, r.Method, uri, user, ts), sig) {
		return "", ErrIdentityInvalid
	}
	return user, nil
}
