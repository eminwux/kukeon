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
// bytes are written once to a daemon-owned file under the scope's metadata
// tree and are never round-tripped back into any output, log, or audit
// trail. Phase 3a ships only the schema + storage primitive + `kuke apply`;
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

// SecretSpec carries the secret material supplied at apply time. Data is
// write-only from the operator's perspective: it is persisted to the
// daemon-managed file and never echoed back. The omitempty tag keeps a
// zero value out of any incidental serialization, but the value itself is
// never serialized into a result or status by design.
type SecretSpec struct {
	// Data is the raw secret material supplied at apply time.
	Data string `json:"data,omitempty" yaml:"data,omitempty"`
}
