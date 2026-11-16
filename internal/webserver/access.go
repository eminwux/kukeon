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
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// jsonContentType is the media type every JSON mutation must declare.
const jsonContentType = "application/json"

// listenTarget is a parsed --listen value.
type listenTarget struct {
	// bind is the address handed to net.Listen.
	bind string
	// host is the host as printed in the startup URL.
	host string
	// loopback reports whether the bind stays on the local machine.
	loopback bool
}

// parseListen accepts localhost:<port>, <ipv4>:<port>, and [<ipv6>]:<port>.
// localhost is pinned to 127.0.0.1 so the bind never depends on DNS.
func parseListen(raw string) (listenTarget, error) {
	host, portText, err := net.SplitHostPort(raw)
	if err != nil {
		return listenTarget{}, fmt.Errorf("%w: --listen must be host:port (got %q): %w", errdefs.ErrConfig, raw, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return listenTarget{}, fmt.Errorf(
			"%w: --listen port must be between 0 and 65535 (got %q)",
			errdefs.ErrConfig,
			portText,
		)
	}
	if host == "localhost" {
		return listenTarget{bind: net.JoinHostPort("127.0.0.1", portText), host: host, loopback: true}, nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return listenTarget{}, fmt.Errorf(
			"%w: --listen host must be localhost or a literal IP address (got %q)", errdefs.ErrConfig, host)
	}
	return listenTarget{
		bind:     net.JoinHostPort(ip.String(), portText),
		host:     ip.String(),
		loopback: ip.IsLoopback(),
	}, nil
}

// accessPolicy is the single Host/Origin rule every route shares. The zero
// value accepts any Host and any Origin.
type accessPolicy struct {
	// authority is the required Host header; empty means any.
	authority string
	// origin is the only Origin accepted when one is sent; empty means any.
	origin string
}

// parseAccessPolicy turns a --url value into the policy. An empty value
// yields the accept-any policy.
func parseAccessPolicy(raw string) (accessPolicy, error) {
	if raw == "" {
		return accessPolicy{}, nil
	}
	invalid := func(reason string) error {
		return fmt.Errorf("%w: --url %q %s", errdefs.ErrConfig, raw, reason)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return accessPolicy{}, invalid("is not a valid URL")
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme != "http" && scheme != "https":
		return accessPolicy{}, invalid("must use the http or https scheme")
	case u.Opaque != "" || u.Host == "":
		return accessPolicy{}, invalid("must include a host")
	case u.User != nil:
		return accessPolicy{}, invalid("must not include userinfo")
	case u.Path != "" || u.RawPath != "":
		return accessPolicy{}, invalid("must not include a path")
	case u.RawQuery != "" || u.ForceQuery:
		return accessPolicy{}, invalid("must not include a query")
	case u.Fragment != "" || strings.Contains(raw, "#"):
		return accessPolicy{}, invalid("must not include a fragment")
	}
	host, port := strings.ToLower(u.Hostname()), u.Port()
	if host == "" {
		return accessPolicy{}, invalid("must include a host")
	}
	if strings.HasSuffix(u.Host, ":") {
		return accessPolicy{}, invalid("has an empty port")
	}
	if port != "" {
		n, convErr := strconv.Atoi(port)
		if convErr != nil || n < 1 || n > 65535 {
			return accessPolicy{}, invalid("has an out-of-range port")
		}
		// Browsers omit the scheme-default port from both Host and Origin.
		if (scheme == "http" && n == 80) || (scheme == "https" && n == 443) {
			port = ""
		} else {
			port = strconv.Itoa(n)
		}
	}
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}
	return accessPolicy{authority: authority, origin: scheme + "://" + authority}, nil
}

// String names the accepted browser origin for the startup banner.
func (p accessPolicy) String() string {
	if p.origin == "" {
		return "any"
	}
	return p.origin
}

// allows pins Host (the DNS-rebinding guard for same-origin GETs, which
// carry no Origin) and, when an Origin is sent, the origin itself.
func (p accessPolicy) allows(r *http.Request) bool {
	origins := r.Header.Values("Origin")
	if len(origins) > 1 {
		return false
	}
	if p.authority == "" {
		return true
	}
	if !strings.EqualFold(r.Host, p.authority) {
		return false
	}
	return len(origins) == 0 || strings.EqualFold(origins[0], p.origin)
}
