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

package gitcred

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

// Transports a git remote can use.
const (
	TransportSSH   = "ssh"
	TransportHTTPS = "https"
	TransportHTTP  = "http"
	// TransportOther is a local path, file://, git:// or anything else no
	// stored credential applies to.
	TransportOther = "other"
)

const (
	defaultSSHUser = "git"
	defaultSSHPort = 22
)

// Remote is a parsed git remote URL.
type Remote struct {
	Transport string
	User      string
	Host      string
	Port      int
	// Path is the repository path without a leading slash.
	Path string
}

// ParseRemote parses a clone URL in any of the forms git accepts: scp-like
// (`[user@]host:path`), ssh://, http(s)://. Anything else — a local path,
// file://, git:// — parses as TransportOther with only Path set, so callers
// leave it untouched.
func ParseRemote(raw string) Remote {
	raw = strings.TrimSpace(raw)
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		switch strings.ToLower(scheme) {
		case "ssh", "git+ssh", "ssh+git":
			return parseURLRemote(raw, TransportSSH)
		case "https":
			return parseURLRemote(raw, TransportHTTPS)
		case "http":
			return parseURLRemote(raw, TransportHTTP)
		default:
			_ = rest
			return Remote{Transport: TransportOther, Path: raw}
		}
	}
	// scp-like: a colon before any slash, and the part before it is not a
	// path (git's own rule for telling `host:path` from `./dir:with:colon`).
	colon := strings.IndexByte(raw, ':')
	slash := strings.IndexByte(raw, '/')
	if colon <= 0 || (slash >= 0 && slash < colon) || strings.HasPrefix(raw, ".") {
		return Remote{Transport: TransportOther, Path: raw}
	}
	hostPart, repoPath := raw[:colon], raw[colon+1:]
	user := ""
	if at := strings.LastIndexByte(hostPart, '@'); at >= 0 {
		user, hostPart = hostPart[:at], hostPart[at+1:]
	}
	// The user and host of an scp-like remote end up in a generated
	// ssh_config; one carrying whitespace or a control character is not a
	// host git could reach anyway, and must not be able to add a line there.
	if hostPart == "" || strings.ContainsFunc(user+hostPart, unsafeHostRune) {
		return Remote{Transport: TransportOther, Path: raw}
	}
	return Remote{
		Transport: TransportSSH,
		User:      user,
		Host:      strings.ToLower(hostPart),
		Path:      strings.TrimPrefix(repoPath, "/"),
	}
}

func unsafeHostRune(r rune) bool {
	return unicode.IsSpace(r) || unicode.IsControl(r)
}

func parseURLRemote(raw, transport string) Remote {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return Remote{Transport: TransportOther, Path: raw}
	}
	port, _ := strconv.Atoi(u.Port())
	return Remote{
		Transport: transport,
		User:      u.User.Username(),
		Host:      strings.ToLower(u.Hostname()),
		Port:      port,
		Path:      strings.TrimPrefix(u.Path, "/"),
	}
}

// SSHURL renders the remote for the SSH transport. The conventional scp-like
// form is used on the default port; a custom port needs the ssh:// form.
func (r Remote) SSHURL(user string, port int) string {
	if user == "" {
		user = defaultSSHUser
	}
	if port == 0 || port == defaultSSHPort {
		return user + "@" + r.Host + ":" + r.Path
	}
	return "ssh://" + user + "@" + r.Host + ":" + strconv.Itoa(port) + "/" + r.Path
}

// HTTPURL renders the remote for an HTTP transport, never carrying userinfo:
// the credential helper supplies the user, so no secret is embedded in a URL.
func (r Remote) HTTPURL(scheme string, port int) string {
	return scheme + "://" + hostPort(r.Host, port) + "/" + r.Path
}

func hostPort(host string, port int) string {
	if port == 0 {
		return host
	}
	return host + ":" + strconv.Itoa(port)
}

// credentialTarget is a parsed GitCredential.Host: an optional scheme, a host,
// an optional port and an optional path prefix.
type credentialTarget struct {
	Scheme string
	Host   string
	Port   int
	Path   string
}

// parseCredentialTarget parses "host", "host:port", "host/prefix" or a full
// "scheme://host[:port][/prefix]".
func parseCredentialTarget(raw string) credentialTarget {
	raw = strings.TrimSpace(raw)
	var t credentialTarget
	if scheme, rest, ok := strings.Cut(raw, "://"); ok {
		t.Scheme = strings.ToLower(scheme)
		raw = rest
	}
	if at := strings.LastIndexByte(raw, '@'); at >= 0 && !strings.Contains(raw[:at], "/") {
		raw = raw[at+1:]
	}
	hostPart, pathPart, _ := strings.Cut(raw, "/")
	if host, port, ok := strings.Cut(hostPart, ":"); ok {
		if n, err := strconv.Atoi(port); err == nil {
			t.Port = n
			hostPart = host
		}
	}
	t.Host = strings.ToLower(hostPart)
	t.Path = strings.Trim(pathPart, "/")
	return t
}

// matches reports whether the target covers the remote, and how specific the
// match is (the length of the matched path prefix), so the longest prefix
// wins. A host-only target matches every repository on the host.
func (t credentialTarget) matches(r Remote) (int, bool) {
	if r.Transport == TransportOther || t.Host == "" || t.Host != r.Host {
		return 0, false
	}
	if t.Path == "" {
		return 0, true
	}
	repoPath := strings.Trim(r.Path, "/")
	if repoPath == t.Path || strings.HasPrefix(repoPath, t.Path+"/") {
		return len(t.Path) + 1, true
	}
	// A prefix naming the repository itself may omit the .git suffix.
	if strings.TrimSuffix(repoPath, ".git") == strings.TrimSuffix(t.Path, ".git") {
		return len(t.Path) + 1, true
	}
	return 0, false
}

// CloneURL expands the short form of an address, `host.tld/owner/repo` (a
// first path segment that names a host: it contains a dot and no colon or
// user), to `https://host.tld/owner/repo`, the URL git clones. Any other
// address — scp-like, ssh://, http(s)://, a local path — is returned as it is
// (#1643, D9). The transport of a clone that has a stored credential follows
// the credential, not this URL.
func CloneURL(raw string) string {
	if shortHost(raw) == "" {
		return raw
	}
	return "https://" + raw
}

// shortHost returns the host of a short-form address, or "" when raw is not
// one.
func shortHost(raw string) string {
	if raw == "" || strings.Contains(raw, "://") || strings.HasPrefix(raw, ".") {
		return ""
	}
	host, rest, ok := strings.Cut(raw, "/")
	if !ok || strings.Trim(rest, "/") == "" || !strings.Contains(host, ".") ||
		strings.ContainsAny(host, ":@") || strings.ContainsFunc(raw, unsafeHostRune) {
		return ""
	}
	return host
}

// SameRepository reports whether two addresses name the same repository: the
// same host (case-insensitive) and path, whatever the transport, user or port,
// with a `.git` suffix and a trailing slash ignored. An address that is not a
// remote (a local path) is only the same as an identical one.
func SameRepository(a, b string) bool {
	ra, rb := ParseRemote(CloneURL(a)), ParseRemote(CloneURL(b))
	if ra.Transport == TransportOther || rb.Transport == TransportOther {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}
	return ra.Host == rb.Host && repositoryPath(ra.Path) == repositoryPath(rb.Path)
}

func repositoryPath(p string) string {
	return strings.TrimSuffix(strings.Trim(p, "/"), ".git")
}

// ShortForm writes an address as `host/path` — the form a kuketeam.yaml uses
// for any host — when that loses nothing: an ssh or https remote with no port
// and no user other than git. Any other address is returned as it is.
func ShortForm(raw string) string {
	remote := ParseRemote(CloneURL(raw))
	switch {
	case remote.Transport != TransportSSH && remote.Transport != TransportHTTPS,
		remote.Port != 0, remote.User != "" && remote.User != defaultSSHUser,
		!strings.Contains(remote.Host, "."), strings.Trim(remote.Path, "/") == "":
		return raw
	}
	return remote.Host + "/" + strings.Trim(remote.Path, "/")
}
