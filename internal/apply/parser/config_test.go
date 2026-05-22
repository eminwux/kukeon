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

package parser_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func parseConfig(t *testing.T, yaml string) *parser.Document {
	t.Helper()
	doc, err := parser.ParseDocument(0, []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDocument failed: %v", err)
	}
	if doc.Kind != v1beta1.KindCellConfig {
		t.Fatalf("kind = %q, want CellConfig", doc.Kind)
	}
	return doc
}

const validConfig = `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: kuke-system
  space: default
spec:
  blueprint:
    name: dev
    realm: kuke-system
  values:
    PROJECT_DIR: kukeon
  repos:
    project:
      url: git@github.com:eminwux/kukeon.git
      branch: main
  secrets:
    anthropic-token:
      secretRef:
        name: anthropic
        realm: kuke-system
`

func TestValidateDocument_Config_Valid(t *testing.T) {
	doc := parseConfig(t, validConfig)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected valid config, got: %v", err)
	}
}

func TestValidateDocument_Config_MissingName(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  realm: kuke-system
spec:
  blueprint:
    name: dev
    realm: kuke-system
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigNameRequired)
}

func TestValidateDocument_Config_MissingRealm(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
spec:
  blueprint:
    name: dev
    realm: kuke-system
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigRealmRequired)
}

func TestValidateDocument_Config_StackWithoutSpace(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: kuke-system
  stack: claude
spec:
  blueprint:
    name: dev
    realm: kuke-system
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigScopeIncomplete)
}

func TestValidateDocument_Config_MissingBlueprintRef(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: kuke-system
spec:
  blueprint:
    name: dev
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigBlueprintRefRequired)
}

func TestValidateDocument_Config_RepoFillMissingURL(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: kuke-system
spec:
  blueprint:
    name: dev
    realm: kuke-system
  repos:
    project:
      branch: main
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigRepoFillURLRequired)
}

func TestValidateDocument_Config_SecretFillMissingRef(t *testing.T) {
	yaml := `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kukeon-dev
  realm: kuke-system
spec:
  blueprint:
    name: dev
    realm: kuke-system
  secrets:
    anthropic-token: {}
`
	doc := parseConfig(t, yaml)
	requireValidationErr(t, parser.ValidateDocument(doc), errdefs.ErrConfigSecretFillRefRequired)
}
