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

// CellConfigDoc is the top-level document for a daemon-stored cell *identity*
// (kind: CellConfig, issue #624, phase 4b-i of #423). Where a CellBlueprint is
// the parametrized template (the "what"), a CellConfig binds that template to a
// concrete instance (the "which one"): it references a Blueprint by name+scope,
// supplies the scalar `values` and the structural repo/secret slot fills, and
// owns the deterministic name of the at-most-one live cell it materializes.
//
// This kind ships the schema, daemon storage, `kuke apply`, slot-fill
// validation against the referenced Blueprint, and the stable-name + back-ref
// identity primitives. The runtime state machine that drives
// materialise/attach/start/refuse — and the `kuke run -c` verb it serves —
// lands in #625; a Config carries no Status because it has no runtime here.
type CellConfigDoc struct {
	APIVersion Version            `json:"apiVersion" yaml:"apiVersion"`
	Kind       Kind               `json:"kind"       yaml:"kind"`
	Metadata   CellConfigMetadata `json:"metadata"   yaml:"metadata"`
	Spec       CellConfigSpec     `json:"spec"       yaml:"spec"`
}

// CellConfigMetadata identifies a Config by name and the scope it is bound to.
// Like a CellBlueprint (and unlike a Secret) a Config is scopable at realm,
// space, or stack only — never cell: a Config materializes a cell, so scoping
// it to a single cell is nonsensical. The scope is the deepest non-empty
// coordinate; a deeper coordinate requires every shallower one. Realm is always
// required. Labels are copied onto the materialized cell in addition to the
// kukeon.io/config back-reference label.
type CellConfigMetadata struct {
	// Name is the config's name, unique within its scope. It is also the
	// deterministic name of the cell this config materializes (see #625) — see
	// internal/cellconfig.StableName for the derivation.
	Name string `json:"name"             yaml:"name"`
	// Realm is the always-required top-level scope coordinate.
	Realm string `json:"realm"            yaml:"realm"`
	// Space, when set, scopes the config to a space within Realm.
	Space string `json:"space,omitempty"  yaml:"space,omitempty"`
	// Stack, when set, scopes the config to a stack within Space.
	Stack string `json:"stack,omitempty"  yaml:"stack,omitempty"`
	// Labels are copied onto the cell materialized from this config, in
	// addition to the kukeon.io/config back-reference label.
	Labels map[string]string `json:"labels,omitempty" yaml:"labels,omitempty"`
}

// CellConfigSpec binds a referenced Blueprint to a concrete instance: the
// scalar `values` filled into the blueprint's `${KEY}` parameters, and the
// structural repo/secret slot fills keyed by the blueprint's slot names. Values
// are stored verbatim and resolved at run time (#625); the repo/secret maps are
// validated against the referenced blueprint's declared slots at apply time.
type CellConfigSpec struct {
	// Blueprint references the CellBlueprint this config instantiates.
	Blueprint CellConfigBlueprintRef `json:"blueprint"         yaml:"blueprint"`
	// Values fill the referenced blueprint's `${KEY}` scalar parameters. Stored
	// verbatim here; resolution happens at run time (#625).
	Values map[string]string `json:"values,omitempty"  yaml:"values,omitempty"`
	// Repos fills the blueprint's structural repo slots, keyed by slot name.
	Repos map[string]CellConfigRepoFill `json:"repos,omitempty"   yaml:"repos,omitempty"`
	// Secrets fills the blueprint's structural secret slots, keyed by slot name.
	Secrets map[string]CellConfigSecretFill `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// CellConfigBlueprintRef references a CellBlueprint by name and scope. Name and
// Realm are required; Space/Stack are optional and follow the blueprint
// scope-coordinate contract (a deeper coordinate requires every shallower one).
// The reference may cross scopes — a Config in one realm may instantiate a
// Blueprint owned by another (e.g. a `default`-realm Config referencing a
// `kuke-system`-scoped template), the same cross-scope freedom a secretRef has.
type CellConfigBlueprintRef struct {
	// Name is the referenced CellBlueprint's name within its scope. Required.
	Name string `json:"name"            yaml:"name"`
	// Realm is the always-required top-level scope coordinate of the blueprint.
	Realm string `json:"realm"           yaml:"realm"`
	// Space, when set, scopes the reference to a space within Realm.
	Space string `json:"space,omitempty" yaml:"space,omitempty"`
	// Stack, when set, scopes the reference to a stack within Space.
	Stack string `json:"stack,omitempty" yaml:"stack,omitempty"`
}

// CellConfigRepoFill fills a structural repo slot the referenced blueprint
// declared (a BlueprintContainer repo with no inline url). It supplies the
// clone source the blueprint deliberately left open. URL is required.
type CellConfigRepoFill struct {
	// URL is the clone URL filling the slot. Required.
	URL string `json:"url"              yaml:"url"`
	// Branch is the branch to check out. Empty clones the remote's default.
	Branch string `json:"branch,omitempty" yaml:"branch,omitempty"`
}

// CellConfigSecretFill fills a structural secret slot the referenced blueprint
// declared (a BlueprintSecretSlot). The blueprint owns the consumption side
// (env var or file mount); this supplies the source side — which kind: Secret
// provides the bytes. SecretRef is required.
type CellConfigSecretFill struct {
	// SecretRef points at the kind: Secret that provides the slot's bytes.
	// Required.
	SecretRef *ContainerSecretRef `json:"secretRef,omitempty" yaml:"secretRef,omitempty"`
}
