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
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// Link states a Client reports.
const (
	StateConnecting   = "connecting"
	StateConnected    = "connected"
	StateDisconnected = "disconnected"
)

const (
	defaultMinBackoff = time.Second
	defaultMaxBackoff = 30 * time.Second
	// backoffFactor multiplies the reconnect delay after each failure.
	backoffFactor = 2
	dialTimeout   = 30 * time.Second
	// streamHeaderTimeout bounds reading a proxied request's header.
	streamHeaderTimeout = 30 * time.Second
)

// State is a snapshot of a Client's link.
type State struct {
	// Status is StateConnecting, StateConnected or StateDisconnected.
	Status string `json:"status"`
	// Since is when the link entered Status.
	Since time.Time `json:"since"`
	// Error is the last dial or link failure, when there was one.
	Error string `json:"error,omitempty"`
	// Retry is when the next dial starts, while disconnected.
	Retry time.Time `json:"retry,omitzero"`
}

// Client keeps a host's link to a relay up and serves the streams the relay
// opens with Handler.
type Client struct {
	// URL is the relay's wss:// link URL.
	URL string
	// TLSConfig carries the client certificate and the trusted roots.
	TLSConfig *tls.Config
	// Handler serves every proxied request.
	Handler http.Handler
	// OnState, when set, is called on every state change.
	OnState func(State)
	// Logger receives link events; nil discards them.
	Logger *slog.Logger
	// MinBackoff and MaxBackoff bound the reconnect delay (defaults 1s and
	// 30s). The delay doubles after each failed dial, with jitter.
	MinBackoff, MaxBackoff time.Duration
}

// ValidateLinkURL accepts an absolute wss:// URL.
func ValidateLinkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("cloud link URL %q is not an absolute URL", raw)
	}
	if u.Scheme != "wss" {
		return fmt.Errorf("cloud link URL %q must use wss", raw)
	}
	if u.User != nil || u.Fragment != "" {
		return fmt.Errorf("cloud link URL %q must not carry userinfo or a fragment", raw)
	}
	return nil
}

// Run keeps the link up until ctx is done, reconnecting with backoff. It
// returns nil when ctx is done and an error only for an invalid Client.
func (c *Client) Run(ctx context.Context) error {
	if err := ValidateLinkURL(c.URL); err != nil {
		return err
	}
	if c.Handler == nil || c.TLSConfig == nil {
		return errors.New("cloud link: client needs a handler and a TLS configuration")
	}
	logger := c.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	minBackoff, maxBackoff := c.MinBackoff, c.MaxBackoff
	if minBackoff <= 0 {
		minBackoff = defaultMinBackoff
	}
	if maxBackoff < minBackoff {
		maxBackoff = max(defaultMaxBackoff, minBackoff)
	}
	backoff := minBackoff
	for {
		c.report(State{Status: StateConnecting, Since: time.Now()})
		session, err := c.dial(ctx)
		if err == nil {
			logger.InfoContext(ctx, "cloud link connected", "url", c.URL)
			c.report(State{Status: StateConnected, Since: time.Now()})
			backoff = minBackoff
			err = c.serve(ctx, session)
		}
		if ctx.Err() != nil {
			c.report(State{Status: StateDisconnected, Since: time.Now()})
			return nil //nolint:nilerr // a cancelled Run is a clean stop, whatever ended the last link
		}
		// Jitter: a delay in [backoff/2, backoff].
		half := backoff / backoffFactor
		delay := half + rand.N(half+1) //nolint:gosec // jitter, not a secret
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		logger.WarnContext(ctx, "cloud link down", "url", c.URL, "error", msg, "retry", delay.Round(time.Millisecond))
		now := time.Now()
		c.report(State{Status: StateDisconnected, Since: now, Error: msg, Retry: now.Add(delay)})
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.report(State{Status: StateDisconnected, Since: time.Now()})
			return nil
		case <-timer.C:
		}
		backoff = min(backoff*backoffFactor, maxBackoff)
	}
}

func (c *Client) report(state State) {
	if c.OnState != nil {
		c.OnState(state)
	}
}

func (c *Client) dial(ctx context.Context) (*Session, error) {
	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		TLSClientConfig:  c.TLSConfig.Clone(),
		HandshakeTimeout: dialTimeout,
	}
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	conn, resp, err := dialer.DialContext(dialCtx, c.URL, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial %s: %s: %w", c.URL, resp.Status, err)
		}
		return nil, fmt.Errorf("dial %s: %w", c.URL, err)
	}
	return NewSession(conn, true), nil
}

// serve runs an HTTP server over the session's streams until the session
// closes or ctx is done, then closes every stream still open, so in-flight
// requests end instead of hanging.
func (c *Client) serve(ctx context.Context, session *Session) error {
	server := &http.Server{
		Handler:           c.Handler,
		ReadHeaderTimeout: streamHeaderTimeout,
		BaseContext:       func(_ net.Listener) context.Context { return ctx },
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(session.Listener()) }()
	select {
	case <-ctx.Done():
	case <-session.Done():
	}
	_ = server.Close()
	_ = session.Close()
	<-served
	return session.Err()
}
