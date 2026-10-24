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

// Workspace is the single user-scoped operator configuration at
// ~/.kuke/workspace.yaml. It carries Git identity/signing, registry, source
// transport overrides, secret sources, credential bindings and team defaults.
// On first init, existing Git settings are seeded using git config --global.
type Workspace struct {
	APIVersion string        `json:"apiVersion" yaml:"apiVersion"`
	Kind       string        `json:"kind"       yaml:"kind"`
	Spec       WorkspaceSpec `json:"spec"       yaml:"spec"`
}

// WorkspaceSpec carries the operator-owned composition.
type WorkspaceSpec struct {
	Defaults    TeamSelections      `json:"defaults,omitempty"    yaml:"defaults,omitempty"`
	Credentials []CredentialBinding `json:"credentials,omitempty" yaml:"credentials,omitempty"`
	// Git is the operator's git identity + signing config rendered into every
	// team cell. It is a strict superset of v1beta1.ContainerGit.
	Git *WorkspaceGit `json:"git,omitempty"         yaml:"git,omitempty"`
	// Registry is the default container registry for resolving images.
	Registry string `json:"registry,omitempty"    yaml:"registry,omitempty"`
	// HomeDir is an explicit override for the `.operator.HOME_DIR` blueprint
	// fact. Optional; when unset the renderer falls back to `$HOME` at render
	// time so the scaffolded global config stays minimal. Distinct from the
	// per-team Layout root (which is per-team and carried on TeamEntry).
	HomeDir string `json:"homeDir,omitempty"     yaml:"homeDir,omitempty"`
	// RepoOwner is an explicit override for the `.operator.REPO_OWNER`
	// blueprint fact (the owner segment of `<owner>/<repo>`). Optional; when
	// unset the renderer derives it from the agents source's owner segment so
	// the common single-owner case needs no entry. Set this when the operator's
	// identity differs from the agents source owner (e.g., forked agents).
	RepoOwner string `json:"repoOwner,omitempty"   yaml:"repoOwner,omitempty"`
	// Sources maps `<owner>/<repo>` keys to clone URLs (e.g.
	// eminwux/agents -> git@github.com:eminwux/agents.git).
	Sources map[string]string `json:"sources,omitempty"     yaml:"sources,omitempty"`
	// Secrets maps a secret name to its source declaration. A secret never
	// carries an inline value — only where to read it from.
	Secrets map[string]WorkspaceSecret `json:"secrets,omitempty"     yaml:"secrets,omitempty"`
}

// WorkspaceGit is a strict superset of v1beta1.ContainerGit — the embedded
// runtime type contributes author, committer, signingKey, sign, and
// allowedSigners — plus the SSHKey clone identity used to clone/push project and
// agents repos. Embedding (rather than redeclaring) keeps it drift-proof: any
// field added to ContainerGit is automatically carried here.
type WorkspaceGit struct {
	v1beta1.ContainerGit `yaml:",inline"`

	// SSHKey is the host path to the SSH private key used as the
	// GIT_SSH_COMMAND identity for clone/push (distinct from the signing key,
	// which signs commits/tags). Optional.
	SSHKey string `json:"sshKey,omitempty" yaml:"sshKey,omitempty"`
}

// WorkspaceSecret declares where a secret's value is read from. `From` is one
// of env or file; `Key` is the environment-variable name or file path. The
// value itself is never stored here.
type WorkspaceSecret struct {
	From string `json:"from" yaml:"from"`
	Key  string `json:"key"  yaml:"key"`
}

// CredentialBinding names a grant independently of the underlying secret.
type CredentialBinding struct {
	Name        string           `json:"name"                  yaml:"name"`
	SecretRef   string           `json:"secretRef"             yaml:"secretRef"`
	VolumeMount *CredentialMount `json:"volumeMount,omitempty" yaml:"volumeMount,omitempty"`
}

// CredentialMount describes container placement only. Nil ReadOnly means true;
// a pointer preserves an explicitly writable mount through serialization.
type CredentialMount struct {
	Target   string `json:"target"             yaml:"target"`
	ReadOnly *bool  `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`
}
