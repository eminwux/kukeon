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

package apischeme

import (
	"bytes"
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// NormalizeTeam converts a parsed Team document to the carrier kukeond
// stores (issue #1618): the document re-encoded in canonical form, which
// keeps an omitted list apart from an explicit empty one, with the apiVersion
// defaulted.
func NormalizeTeam(doc ext.TeamDoc) (intmodel.Team, error) {
	doc.APIVersion = DefaultVersion(doc.APIVersion)
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return intmodel.Team{}, fmt.Errorf("encode team %q: %w", doc.Metadata.Name, err)
	}
	return intmodel.Team{
		Metadata: intmodel.TeamMetadata{Name: doc.Metadata.Name, Realm: doc.Metadata.Realm},
		Document: raw,
	}, nil
}

// NormalizeTeamDefaults converts a parsed TeamDefaults document to the carrier
// kukeond stores, like NormalizeTeam.
func NormalizeTeamDefaults(doc ext.TeamDefaultsDoc) (intmodel.TeamDefaults, error) {
	doc.APIVersion = DefaultVersion(doc.APIVersion)
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return intmodel.TeamDefaults{}, fmt.Errorf("encode team defaults: %w", err)
	}
	return intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: doc.Metadata.Name, Realm: doc.Metadata.Realm},
		Document: raw,
	}, nil
}

// ConvertTeamToExternal decodes a stored Team document.
func ConvertTeamToExternal(in intmodel.Team) (ext.TeamDoc, error) {
	var doc ext.TeamDoc
	if err := config.DecodeStrict(bytes.NewReader(in.Document), &doc); err != nil {
		return ext.TeamDoc{}, fmt.Errorf("decode stored team %q: %w", in.Metadata.Name, err)
	}
	return doc, nil
}

// ConvertTeamDefaultsToExternal decodes a stored TeamDefaults document.
func ConvertTeamDefaultsToExternal(in intmodel.TeamDefaults) (ext.TeamDefaultsDoc, error) {
	var doc ext.TeamDefaultsDoc
	if err := config.DecodeStrict(bytes.NewReader(in.Document), &doc); err != nil {
		return ext.TeamDefaultsDoc{}, fmt.Errorf("decode stored team defaults: %w", err)
	}
	return doc, nil
}
