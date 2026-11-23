// Copyright 2026 Emiliano Spinella (eminwux)
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

import "github.com/eminwux/kukeon/pkg/team/config"

// TeamDoc is a team's declaration (kind: Team, issue #1618). Its spec is the
// declaration schema of pkg/team/config, so a stored Team, a repository's
// kuketeam.yaml and what kuke composes from are one document.
//
// The spec types carry YAML tags only and keep omitted lists apart from
// empty ones in their YAML form; a TeamDoc is therefore exchanged as YAML
// bytes, never JSON-encoded.
type TeamDoc struct {
	APIVersion Version         `yaml:"apiVersion"`
	Kind       Kind            `yaml:"kind"`
	Metadata   TeamMetadata    `yaml:"metadata"`
	Spec       config.TeamSpec `yaml:"spec"`
}

// TeamMetadata names a Team, or the TeamDefaults, and the realm it lives in.
// Both kinds are realm-scoped only.
type TeamMetadata struct {
	Name  string `yaml:"name,omitempty"`
	Realm string `yaml:"realm,omitempty"`
}

// TeamDefaultsDoc is the settings every team of a realm inherits (kind:
// TeamDefaults, issue #1618). A realm holds at most one, named default. Like
// TeamDoc it is exchanged as YAML bytes only.
type TeamDefaultsDoc struct {
	APIVersion Version      `yaml:"apiVersion"`
	Kind       Kind         `yaml:"kind"`
	Metadata   TeamMetadata `yaml:"metadata"`
	Spec       config.Spec  `yaml:"spec"`
}

// TeamDefaultsName is the name of a realm's only TeamDefaults.
const TeamDefaultsName = config.DefaultsName
