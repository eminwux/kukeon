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

package modelhub

// Secret is the internal carrier for a named, scoped, daemon-managed
// credential (kind: Secret, issue #619). It has no Status because a Secret
// has no runtime: the bytes are written once to a daemon-owned file and the
// model exists only to ferry the scope coordinates and the material from the
// apply parser to the storage runner. Spec.Data is never round-tripped back
// out of the daemon.
type Secret struct {
	Metadata SecretMetadata
	Spec     SecretSpec
}

// SecretMetadata identifies a Secret by name and the scope it binds to. The
// scope is the deepest non-empty coordinate; a deeper coordinate requires
// every shallower one. See the external v1beta1.SecretMetadata for the full
// contract.
type SecretMetadata struct {
	Name  string
	Realm string
	Space string
	Stack string
	Cell  string
}

// Secret types; see the external v1beta1.SecretType* constants.
const (
	SecretTypeOpaque    = "opaque"
	SecretTypeBasicAuth = "basic-auth"
	SecretTypeSSHKey    = "ssh-key"
)

// SecretSpec carries the secret material supplied at apply time. Type selects
// which block is set; an empty Type is opaque.
type SecretSpec struct {
	Type      string
	Data      string
	BasicAuth *SecretBasicAuth
	SSHKey    *SecretSSHKey
}

// SecretBasicAuth mirrors v1beta1.SecretBasicAuth.
type SecretBasicAuth struct {
	Username string `json:"username"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
}

// SecretSSHKey mirrors v1beta1.SecretSSHKey.
type SecretSSHKey struct {
	PrivateKey  string `json:"privateKey"`
	Passphrase  string `json:"passphrase,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	KnownHosts  string `json:"knownHosts,omitempty"`
}

// EffectiveType returns the secret type with the empty default resolved to
// opaque.
func (s SecretSpec) EffectiveType() string {
	if s.Type == "" {
		return SecretTypeOpaque
	}
	return s.Type
}

// HasMaterial reports whether the spec carries any secret material (as opposed
// to the metadata-only view the get/list verbs return).
func (s SecretSpec) HasMaterial() bool {
	return s.Data != "" || s.BasicAuth != nil || s.SSHKey != nil
}
