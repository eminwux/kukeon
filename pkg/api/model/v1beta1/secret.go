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

package v1beta1

// SecretDoc is the top-level document for a named, scoped, daemon-managed
// credential (kind: Secret, issue #619). Unlike the realm/space/stack/cell
// hierarchy resources it carries no Status: a Secret has no runtime — its
// bytes are written to a root-only, daemon-owned file under the scope's
// metadata tree. They never appear in a log, an audit trail, a list, table
// output or a web API response; the only read-back is a named
// `kuke get secret <name> -o yaml|json` (issue #1652). Phase 3a ships only the schema + storage primitive + `kuke apply`;
// the `kuke get`/`kuke delete` verbs (#622) and the `ContainerSecret.secretRef`
// source (#623) build on this foundation.
type SecretDoc struct {
	APIVersion Version        `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind           `json:"kind"       yaml:"kind"`
	Metadata   SecretMetadata `json:"metadata"   yaml:"metadata"`
	Spec       SecretSpec     `json:"spec"       yaml:"spec"`
}

// SecretMetadata identifies a Secret by name and the scope it is bound to.
// The scope is the deepest non-empty coordinate: a Secret with only Realm
// set is realm-scoped; one with Realm+Space+Stack set is stack-scoped; and
// so on. A deeper coordinate requires every shallower one (a Cell-scoped
// Secret must also name its Stack, Space, and Realm) — apply rejects a gap.
// Scope coordinates live on the metadata (not the spec) so the Secret's
// full identity is its scope plus its name.
type SecretMetadata struct {
	// Name is the secret's name, unique within its scope.
	Name string `json:"name"            yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"           yaml:"realm"`
	// Space, when set, scopes the secret to a space within Realm.
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	// Stack, when set, scopes the secret to a stack within Space.
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
	// Cell, when set, scopes the secret to a cell within Stack.
	Cell string `json:"cell,omitempty"  yaml:"cell,omitempty"`
}

// Secret types (issue #1472, step 1). The type selects which spec block carries
// the material and how the daemon may consume it: an opaque secret is raw bytes
// injected through `containers[].secrets[]`; the typed git credentials are
// consumed only through `git.credentials[]` / `git.signing` and are never put in
// an environment variable or a URL.
const (
	// SecretTypeOpaque is the default: Spec.Data holds the raw bytes.
	SecretTypeOpaque = "opaque"
	// SecretTypeBasicAuth is a username plus a password or a token
	// (Spec.BasicAuth): an HTTPS git credential, or the `kuke serve` login.
	SecretTypeBasicAuth = "basic-auth"
	// SecretTypeSSHKey is an SSH git credential and signing key: an OpenSSH
	// private key with an optional passphrase, public key, certificate and
	// pinned known_hosts (Spec.SSHKey).
	SecretTypeSSHKey = "ssh-key"
)

// SecretSpec carries the secret material supplied at apply time. The material
// is persisted to the daemon-managed file. Type, Connector and Identity are
// the readable fields every `kuke get secret` reports; the material is printed
// only by a named `kuke get secret <name> -o yaml|json` (issue #1652), never by
// a list, table output or a web API response.
type SecretSpec struct {
	// Type is one of opaque (default when empty), basic-auth or ssh-key.
	Type string `json:"type,omitempty"      yaml:"type,omitempty"`
	// Connector tags the secret as an identity of one connector (for example
	// "claude", "github", "git", "registry"; issue #1548). It is readable and
	// an open string: the daemon keeps no connector catalog, so a secret may
	// name a connector a newer kuke defines. Pickers list the secrets whose
	// tag matches; an untagged secret can still be named by hand.
	Connector string `json:"connector,omitempty" yaml:"connector,omitempty"`
	// Identity holds the readable, non-secret facts of the identity, keyed by
	// the field names the connector declares (a git identity carries author,
	// committer, hosts, sign and allowedSignersRef). Every value is a string;
	// the connector's field type says how it is read (a list is
	// comma-separated). Never put key material here.
	Identity map[string]string `json:"identity,omitempty"  yaml:"identity,omitempty"`
	// Data is the raw secret material of an opaque secret.
	Data string `json:"data,omitempty"      yaml:"data,omitempty"`
	// BasicAuth is the material of a basic-auth secret.
	BasicAuth *SecretBasicAuth `json:"basicAuth,omitempty" yaml:"basicAuth,omitempty"`
	// SSHKey is the material of an ssh-key secret.
	SSHKey *SecretSSHKey `json:"sshKey,omitempty"    yaml:"sshKey,omitempty"`
}

// SecretBasicAuth is a username plus a password or a token: an HTTPS git
// credential (both are sent as the HTTP basic-auth password) or the
// `kuke serve` login (issue #1652). Exactly one of Password or Token is set.
type SecretBasicAuth struct {
	// Username is the HTTP basic-auth user. Required.
	Username string `json:"username"           yaml:"username"`
	// Password is the account password.
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
	// Token is a personal access token used in place of a password.
	Token string `json:"token,omitempty"    yaml:"token,omitempty"`
}

// SecretSSHKey is an SSH git credential, also usable as an SSH signing key.
type SecretSSHKey struct {
	// PrivateKey is the OpenSSH (or PEM) private key, any key type. Required.
	PrivateKey string `json:"privateKey"            yaml:"privateKey"`
	// Passphrase unlocks an encrypted PrivateKey. kuketty loads such a key
	// into a per-container ssh-agent; the passphrase never reaches the
	// workload's environment.
	Passphrase string `json:"passphrase,omitempty"  yaml:"passphrase,omitempty"`
	// PublicKey is the matching public key line. Required for signing with a
	// passphrase-protected key (git names the agent key by its public half);
	// optional otherwise.
	PublicKey string `json:"publicKey,omitempty"   yaml:"publicKey,omitempty"`
	// Certificate is an OpenSSH user certificate for PrivateKey.
	Certificate string `json:"certificate,omitempty" yaml:"certificate,omitempty"`
	// KnownHosts pins the host keys the credential trusts, in known_hosts
	// format. When empty the built-in keys for github.com, gitlab.com and
	// bitbucket.org apply.
	KnownHosts string `json:"knownHosts,omitempty"  yaml:"knownHosts,omitempty"`
}
