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

// SecretSpec carries the secret material supplied at apply time.
type SecretSpec struct {
	Data string
}
