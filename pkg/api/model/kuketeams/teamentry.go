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

// TeamEntry is the host-side, per-project composition record written by
// `kuke team init` to ~/.kuke/kuketeam.d/<project>.yaml — one document per
// file. It replaces the former TeamsConfig.spec.teams[] array: a drop-in
// directory (the systemd/sudoers.d pattern) keeps each project isolated, so a
// corrupt or partial write touches one project and two concurrent inits never
// race on a shared array.
type TeamEntry struct {
	APIVersion string        `json:"apiVersion" yaml:"apiVersion"`
	Kind       string        `json:"kind"       yaml:"kind"`
	Metadata   Metadata      `json:"metadata"   yaml:"metadata"`
	Spec       TeamEntrySpec `json:"spec"       yaml:"spec"`
}

// TeamEntrySpec carries the per-project locator + agents pin. Path is an
// init-time LOCATOR (where the project's kuketeam.yaml was read and from where
// its clone URL is resolved via `git remote`), not a bind-mount source. Source
// pins the agents repository as `<owner>/<repo>@vX.Y.Z`, copied from the
// project's kuketeam.yaml at init time.
type TeamEntrySpec struct {
	Path   string `json:"path"             yaml:"path"`
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
}
