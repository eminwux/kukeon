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

package kuketeams

// TeamsConfig is the host-side, operator-owned global-facts document, stored at
// ~/.kuke/kuketeams.yaml. It is optional and hand-written: kuke reads it and
// never writes it (issue #1591). It carries the operator facts only — registry,
// source clone URLs, and secret sources. A git identity is not one of them: it
// is a git connector identity (`kuke connector import git`). Per-team composition lives in its own TeamEntry document
// under the ~/.kuke/kuketeam.d/ drop-in directory (one <team>.yaml file per
// team), so a corrupt or partial write touches a single team rather than the
// shared roster.
type TeamsConfig struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind"       yaml:"kind"`
	Spec       TeamsConfigSpec `json:"spec"       yaml:"spec"`
}

// TeamsConfigSpec carries the operator-owned composition.
type TeamsConfigSpec struct {
	// Registry is the default container registry for resolving images.
	Registry string `json:"registry,omitempty"  yaml:"registry,omitempty"`
	// HomeDir is an explicit override for the `.operator.HOME_DIR` blueprint
	// fact. Optional; when unset the renderer falls back to `$HOME` at render
	// time so the scaffolded global config stays minimal. Distinct from the
	// per-team Layout root (which is per-team and carried on TeamEntry).
	HomeDir string `json:"homeDir,omitempty"   yaml:"homeDir,omitempty"`
	// RepoOwner is an explicit override for the `.operator.REPO_OWNER`
	// blueprint fact (the owner segment of `<owner>/<repo>`). Optional; when
	// unset the renderer derives it from the agents source's owner segment so
	// the common single-owner case needs no entry. Set this when the operator's
	// identity differs from the agents source owner (e.g., forked agents).
	RepoOwner string `json:"repoOwner,omitempty" yaml:"repoOwner,omitempty"`
	// Sources maps `<owner>/<repo>` keys to clone URLs (e.g.
	// eminwux/agents -> git@github.com:eminwux/agents.git).
	Sources map[string]string `json:"sources,omitempty"   yaml:"sources,omitempty"`
	// Secrets maps a secret name to its source declaration. A secret never
	// carries an inline value — only where to read it from.
	Secrets map[string]TeamsConfigSecret `json:"secrets,omitempty"   yaml:"secrets,omitempty"`
}

// TeamsConfigSecret declares where a secret's value is read from. `From` is one
// of env or file; `Key` is the environment-variable name or file path. The
// value itself is never stored here.
type TeamsConfigSecret struct {
	From string `json:"from" yaml:"from"`
	Key  string `json:"key"  yaml:"key"`
}
