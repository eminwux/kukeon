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

import (
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TeamsConfig is the host-side, operator-owned composition document, stored at
// ~/.kuke/kuketeams.yaml and maintained by `kuke team init`. It carries the
// operator facts (git identity/signing, registry, source clone URLs, secret
// sources) and the list of composed teams.
type TeamsConfig struct {
	APIVersion string          `json:"apiVersion" yaml:"apiVersion"`
	Kind       string          `json:"kind"       yaml:"kind"`
	Spec       TeamsConfigSpec `json:"spec"       yaml:"spec"`
}

// TeamsConfigSpec carries the operator-owned composition.
type TeamsConfigSpec struct {
	// Git is the operator's git identity + signing config rendered into every
	// team cell. It is a strict superset of v1beta1.ContainerGit.
	Git *TeamsConfigGit `json:"git,omitempty"      yaml:"git,omitempty"`
	// Registry is the default container registry for resolving images.
	Registry string `json:"registry,omitempty" yaml:"registry,omitempty"`
	// Sources maps `<owner>/<repo>` keys to clone URLs (e.g.
	// eminwux/agents -> git@github.com:eminwux/agents.git).
	Sources map[string]string `json:"sources,omitempty"  yaml:"sources,omitempty"`
	// Secrets maps a secret name to its source declaration. A secret never
	// carries an inline value — only where to read it from.
	Secrets map[string]TeamsConfigSecret `json:"secrets,omitempty"  yaml:"secrets,omitempty"`
	// Teams lists the composed teams. Each entry's path is an init-time
	// LOCATOR (where to read kuketeam.yaml and resolve the project's clone URL
	// from `git remote`), not a bind-mount source.
	Teams []TeamsConfigTeam `json:"teams,omitempty"    yaml:"teams,omitempty"`
}

// TeamsConfigGit is a strict superset of v1beta1.ContainerGit — the embedded
// runtime type contributes author, committer, signingKey, sign, and
// allowedSigners — plus the SSHKey clone identity used to clone/push project and
// agents repos. Embedding (rather than redeclaring) keeps it drift-proof: any
// field added to ContainerGit is automatically carried here.
type TeamsConfigGit struct {
	v1beta1.ContainerGit `yaml:",inline"`

	// SSHKey is the host path to the SSH private key used as the
	// GIT_SSH_COMMAND identity for clone/push (distinct from the signing key,
	// which signs commits/tags). Optional.
	SSHKey string `json:"sshKey,omitempty" yaml:"sshKey,omitempty"`
}

// TeamsConfigSecret declares where a secret's value is read from. `From` is one
// of env or file; `Key` is the environment-variable name or file path. The
// value itself is never stored here.
type TeamsConfigSecret struct {
	From string `json:"from" yaml:"from"`
	Key  string `json:"key"  yaml:"key"`
}

// TeamsConfigTeam is one composed team. Path is an init-time locator, not a
// bind-mount source — the cell clones the project fresh from the URL resolved
// from the project's `git remote` at init time.
type TeamsConfigTeam struct {
	Name   string `json:"name"   yaml:"name"`
	Path   string `json:"path"   yaml:"path"`
	Source string `json:"source" yaml:"source"`
}
