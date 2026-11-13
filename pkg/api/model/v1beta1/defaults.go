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

// ScopeDefaults is the CellConfig fill a Realm, Space or Stack contributes to
// every cell the daemon renders from a CellConfig inside that scope (issue
// #1418). It has the same shape as the fill part of CellConfigSpec — scalar
// `values`, and the structural `repos` / `secrets` slot fills keyed by slot
// name — and merges Realm → Space → Stack → CellConfig, with deeper levels
// winning per key.
//
// Repo and secret fills a blueprint does not declare as slots are ignored
// (a realm-wide default is not a typo against any one blueprint), whereas the
// CellConfig's own fills stay strictly validated against the blueprint.
type ScopeDefaults struct {
	// Values fill blueprint `${KEY}` parameters.
	Values map[string]string `json:"values,omitempty"  yaml:"values,omitempty"`
	// Repos fill blueprint repo slots, keyed by slot name.
	Repos map[string]CellConfigRepoFill `json:"repos,omitempty"   yaml:"repos,omitempty"`
	// Secrets fill blueprint secret slots, keyed by slot name.
	Secrets map[string]CellConfigSecretFill `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// IsZero reports whether the defaults carry no fill at all.
func (d *ScopeDefaults) IsZero() bool {
	return d == nil || (len(d.Values) == 0 && len(d.Repos) == 0 && len(d.Secrets) == 0)
}

// RealmImage names an image the realm provides to cells rendered from a
// CellConfig (issue #1418). A container whose (resolved) `image` equals Name
// renders with the entry's reference, and the image's digest in the realm's
// containerd namespace is recorded in the cell's provenance so a rebuild with
// a new digest marks the cell OutOfSync.
//
// Exactly one of Ref or Build is set.
type RealmImage struct {
	// Name is the handle Configs and blueprints refer to. Required, unique
	// within the realm.
	Name string `json:"name"            yaml:"name"`
	// Ref is a built-in or registry image reference (e.g.
	// docker.io/library/alpine:3.20).
	Ref string `json:"ref,omitempty"   yaml:"ref,omitempty"`
	// Build declares a Dockerfile built into the realm with `kuke build
	// --realm-image <name>`.
	Build *RealmImageBuild `json:"build,omitempty" yaml:"build,omitempty"`
}

// RealmImageBuild declares a Dockerfile build for a RealmImage.
type RealmImageBuild struct {
	// Context is the build context directory. Required.
	Context string `json:"context"              yaml:"context"`
	// Dockerfile is the Dockerfile path; empty means <context>/Dockerfile.
	Dockerfile string `json:"dockerfile,omitempty" yaml:"dockerfile,omitempty"`
	// Args are build-time variables (`--build-arg KEY=VALUE`).
	Args map[string]string `json:"args,omitempty"       yaml:"args,omitempty"`
	// Tag is the image reference the build writes; empty means
	// kukeon.internal/<realm>/<name>:latest.
	Tag string `json:"tag,omitempty"        yaml:"tag,omitempty"`
}

// RealmImageStatus is the daemon's view of one RealmImage: the reference it
// resolves to and that reference's digest in the realm's containerd namespace
// (empty when the image has not been pulled or built yet).
type RealmImageStatus struct {
	Name   string `json:"name"             yaml:"name"`
	Ref    string `json:"ref"              yaml:"ref"`
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
	Built  bool   `json:"built,omitempty"  yaml:"built,omitempty"`
}

// Render-value kinds and origins reported by the daemon's CellConfig render
// (`kuke create cell --from-config --dry-run --explain`, the web UI preview).
const (
	RenderKindValue  = "value"
	RenderKindRepo   = "repo"
	RenderKindSecret = "secret"
	RenderKindImage  = "image"

	RenderOriginRealm     = "realm"
	RenderOriginSpace     = "space"
	RenderOriginStack     = "stack"
	RenderOriginConfig    = "config"
	RenderOriginBlueprint = "blueprint"
)

// RenderedValue is one resolved input of a daemon-side CellConfig render
// together with the level that set it (issue #1418). Value is display-safe:
// a secret fill renders as its secretRef coordinates, never the secret bytes.
type RenderedValue struct {
	Kind   string `json:"kind"   yaml:"kind"`
	Key    string `json:"key"    yaml:"key"`
	Value  string `json:"value"  yaml:"value"`
	Origin string `json:"origin" yaml:"origin"`
}
