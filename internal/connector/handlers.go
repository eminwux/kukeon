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

package connector

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// The two special delivery handlers — git and registry — are code, and this
// file is what they know about their own identities: the tag their Secrets
// carry and, for git, the identity fields the handler expands. Everything
// else about a connector is data in the catalog (team/connectors); no other
// package names a connector.

// GitIdentityTag is the Secret tag of a git identity, used when a
// `git.identityRef` is expanded for a blueprint that carries no git connector
// component (a scope default serves every blueprint in the scope).
const GitIdentityTag = "git"

// Readable identity fields the git delivery handler expands.
const (
	// GitFieldAuthor is the commit author, `Name <email>`.
	GitFieldAuthor = "author"
	// GitFieldCommitter is the committer, `Name <email>`.
	GitFieldCommitter = "committer"
	// GitFieldHosts lists the hosts or URL prefixes the Secret authenticates.
	GitFieldHosts = "hosts"
	// GitFieldSign lists what to sign with an SSH key: commits, tags.
	GitFieldSign = "sign"
	// GitFieldAllowedSignersRef names the Secret holding the allowed-signers
	// file used to verify signatures.
	GitFieldAllowedSignersRef = "allowedSignersRef"
)

// The GitHub token for git (issue #1693) is the one place code names a
// connector other than the two special handlers: one request stores a GitHub
// API token as an identity of the github connector and, on explicit request,
// as a second, host-limited basic-auth identity of the git connector. Git
// never picks the API token up by itself (#1472): the second identity exists
// only because the user asked for it, and it names the hosts it serves.
const (
	// GitHubIdentityTag is the Secret tag of a GitHub API token identity.
	GitHubIdentityTag = "github"
	// GitHubTokenHost is the host a GitHub token authenticates git on by default.
	GitHubTokenHost = "github.com"
	// GitHubTokenUsername is the basic-auth username stored with a GitHub token
	// for git. GitHub accepts any non-empty username with a personal access
	// token; this is the one its own tooling uses.
	GitHubTokenUsername = "x-access-token"
	// GitHubTokenName is the github identity's name when none is given.
	GitHubTokenName = "github-token"
	// GitHubGitName is the name of the git identity stored from a GitHub token
	// when none is given and none is linked to it yet.
	GitHubGitName = "github-git"
)

// IdentityTokenFrom is a reserved identity key (issue #1693): set on a git
// identity derived from a GitHub token, it names the github identity the token
// came from (`name@realm[/space[/stack]]`), so rotating that identity rotates
// this one too. A reserved key starts with ReservedIdentityPrefix and is
// accepted on every connector's identities without being declared as a field:
// it is metadata kukeon writes, not a fact a delivery handler expands, and a
// Blueprint composed before the key existed still accepts the Secret.
const IdentityTokenFrom = ReservedIdentityPrefix + "token-from"

// ReservedIdentityPrefix starts the identity keys kukeon reserves for itself.
const ReservedIdentityPrefix = "kukeon.io/"

// GitAuthorOnlyValue is the stored value of an author-only git identity: an
// opaque Secret tagged `git` that carries an author and committer but no key
// or hosts (issue #1591). The daemon's secret store rejects an empty opaque
// value, so this fixed placeholder stands in; nothing reads it.
const GitAuthorOnlyValue = "kukeon:git-author-only"

// IsGitAuthorOnly reports whether a Secret is an author-only git identity: an
// opaque Secret tagged for the git connector.
func IsGitAuthorOnly(spec v1beta1.SecretSpec) bool {
	return strings.TrimSpace(spec.Connector) == GitIdentityTag &&
		effectiveType(spec.Type) == v1beta1.SecretTypeOpaque
}

// CheckGitAuthorOnly checks the one rule the connector schema cannot state:
// an author-only git identity must carry an author, since it has nothing else
// to give a cell. Every other Secret passes.
func CheckGitAuthorOnly(spec v1beta1.SecretSpec) error {
	if !IsGitAuthorOnly(spec) {
		return nil
	}
	if strings.TrimSpace(spec.Identity[GitFieldAuthor]) == "" {
		return fmt.Errorf(
			"%w: an author-only git identity (an opaque secret) requires identity field %q",
			errdefs.ErrConnectorIdentity, GitFieldAuthor)
	}
	return nil
}

// RegistryIdentityTag is the Secret tag of a registry credential.
const RegistryIdentityTag = "registry"

// RegistrySecretName is the name `kuke create registry-credential` and the
// web UI give the Secret holding the credential of a registry server:
// `registry-` followed by the server address with everything but letters and
// digits folded to a dash (`ghcr.io` becomes `registry-ghcr-io`). The empty
// server — the entry that matches the registry of the image reference — is
// `registry-default`.
func RegistrySecretName(server string) string {
	var b strings.Builder
	dash := true // no leading dash
	for _, r := range strings.ToLower(strings.TrimSpace(server)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if slug == "" {
		slug = "default"
	}
	return "registry-" + slug
}

// RegistrySecret builds the realm-scoped basic-auth Secret holding a registry
// credential, tagged as an identity of the registry connector.
func RegistrySecret(realm, name, username, password string) v1beta1.SecretDoc {
	return v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata:   v1beta1.SecretMetadata{Name: name, Realm: realm},
		Spec: v1beta1.SecretSpec{
			Type:      v1beta1.SecretTypeBasicAuth,
			Connector: RegistryIdentityTag,
			BasicAuth: &v1beta1.SecretBasicAuth{Username: username, Password: password},
		},
	}
}

// UpsertRegistryCredential binds a registry server to the Secret holding its
// credential on a Realm's spec.registryCredentials: the entry for the server
// is replaced in place — which is how an inline username and password move
// into a Secret — or appended when the Realm has none. The input slice is not
// modified.
func UpsertRegistryCredential(
	existing []v1beta1.RegistryCredentials, server, realm, secretName string,
) []v1beta1.RegistryCredentials {
	entry := v1beta1.RegistryCredentials{
		ServerAddress: server,
		SecretRef:     &v1beta1.ContainerSecretRef{Name: secretName, Realm: realm},
	}
	out := append([]v1beta1.RegistryCredentials(nil), existing...)
	for i := range out {
		if out[i].ServerAddress == server {
			out[i] = entry
			return out
		}
	}
	return append(out, entry)
}

// RedactRegistryPasswords returns the credentials with every inline password
// removed, for output: a registry password is write-only (issue #1548). The
// input slice is not modified.
func RedactRegistryPasswords(creds []v1beta1.RegistryCredentials) []v1beta1.RegistryCredentials {
	if len(creds) == 0 {
		return creds
	}
	out := append([]v1beta1.RegistryCredentials(nil), creds...)
	for i := range out {
		out[i].Password = ""
	}
	return out
}
