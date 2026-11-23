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

// Package component owns the Component documents of the kuke team component
// library: connectors (an external service's API credentials) and forges (an
// issue/change tracker). kuke team copies them into the shared blueprints it
// renders as optional components a CellConfig switches on by name. These are
// kuketeams composition inputs, not kukeon daemon documents.
package component

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

const (
	APIVersion = "v1beta1"
	Kind       = "Component"
)

type Component struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind"       yaml:"kind"`
	Metadata   model.Metadata `json:"metadata"   yaml:"metadata"`
	Spec       Spec           `json:"spec"       yaml:"spec"`
}

type Spec struct {
	// Kind is connector or forge. The harness component of a blueprint comes
	// from harness.yaml, never from this library.
	Kind        string `json:"kind"                  yaml:"kind"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Requires lists the image capabilities the component needs.
	Requires []string `json:"requires,omitempty"    yaml:"requires,omitempty"`
	// Needs lists the other components that must be enabled with this one.
	Needs       []string                     `json:"needs,omitempty"       yaml:"needs,omitempty"`
	Parameters  []api.CellBlueprintParameter `json:"parameters,omitempty"  yaml:"parameters,omitempty"`
	Environment map[string]string            `json:"environment,omitempty" yaml:"environment,omitempty"`
	Secrets     []Secret                     `json:"secrets,omitempty"     yaml:"secrets,omitempty"`

	// The fields below are set only on a component rendered from the
	// connector catalog (team/connectors); a component.yaml cannot state them.

	// Group is the mutual-exclusion group of a connector's method components.
	Group string `json:"-" yaml:"-"`
	// Connector is the connector definition the component delivers.
	Connector *api.BlueprintConnector `json:"-" yaml:"-"`
	// Harness names the harness the connector authenticates; such a component
	// is rendered only into that harness's blueprints.
	Harness string `json:"-" yaml:"-"`
}

// Secret is a secret slot with a fixed env destination. A component never
// declares a git credential: git authenticates only from spec.git.
type Secret struct {
	Name     string `json:"name"                 yaml:"name"`
	EnvName  string `json:"envName"              yaml:"envName"`
	Required bool   `json:"required,omitempty"   yaml:"required,omitempty"`
	// MountPath is set only on a file-delivered connector method's slot: the
	// secret is mounted there instead of exported as EnvName.
	MountPath string `json:"-"                    yaml:"-"`
	// SecretName is the runtime secret bound by default; empty binds a secret
	// named like the slot.
	SecretName string `json:"secretName,omitempty" yaml:"secretName,omitempty"`
	// Aliases are role-declared secret names that select this slot, so a role
	// written before components existed keeps working.
	Aliases []string `json:"aliases,omitempty"    yaml:"aliases,omitempty"`
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	envPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Load decodes and validates every `<name>/component.yaml` in source,
// connectors before forges and by name within a kind. A nil source is an empty
// library. catalog holds the components rendered from the connector catalog:
// they come first, a library component may need one, and none may share a
// name with one.
func Load(source fs.FS, catalog ...Component) ([]Component, error) {
	names := map[string]bool{}
	out := slices.Clone(catalog)
	for _, c := range catalog {
		names[c.Metadata.Name] = true
	}
	var files []string
	if source != nil {
		var err error
		if files, err = fs.Glob(source, "*/component.yaml"); err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	for _, filename := range files {
		var c Component
		if err := read(source, filename, &c); err != nil {
			return nil, err
		}
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", filename, err)
		}
		if c.Metadata.Name != path.Base(path.Dir(filename)) {
			return nil, fmt.Errorf("%s: metadata.name must match its directory", filename)
		}
		if names[c.Metadata.Name] {
			return nil, fmt.Errorf("%s: a connector already defines component %q", filename, c.Metadata.Name)
		}
		names[c.Metadata.Name] = true
		out = append(out, c)
	}
	for _, c := range out {
		for _, need := range c.Spec.Needs {
			if !names[need] || need == c.Metadata.Name {
				return nil, fmt.Errorf("component %q: needs unknown component %q", c.Metadata.Name, need)
			}
		}
	}
	// Connectors first: a forge builds on one.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Spec.Kind == api.ComponentKindConnector && out[j].Spec.Kind != api.ComponentKindConnector
	})
	return out, nil
}

func (c *Component) Validate() error {
	if c == nil || c.APIVersion != APIVersion || c.Kind != Kind || !namePattern.MatchString(c.Metadata.Name) {
		return errors.New("require v1beta1 Component with valid metadata.name")
	}
	if c.Spec.Kind != api.ComponentKindConnector && c.Spec.Kind != api.ComponentKindForge {
		return fmt.Errorf("spec.kind: expected %s or %s", api.ComponentKindConnector, api.ComponentKindForge)
	}
	for _, list := range [][]string{c.Spec.Requires, c.Spec.Needs} {
		if err := uniqueNames(list); err != nil {
			return err
		}
	}
	params := map[string]bool{}
	for _, param := range c.Spec.Parameters {
		if !envPattern.MatchString(param.Name) || params[param.Name] {
			return errors.New("spec.parameters: invalid or duplicate name")
		}
		params[param.Name] = true
	}
	destinations := map[string]bool{}
	for name := range c.Spec.Environment {
		if !envPattern.MatchString(name) {
			return errors.New("spec.environment: invalid name")
		}
		destinations[name] = true
	}
	return validateSecrets(c.Spec.Secrets, destinations)
}

func uniqueNames(list []string) error {
	seen := map[string]bool{}
	for _, value := range list {
		if !namePattern.MatchString(value) || seen[value] {
			return fmt.Errorf("duplicate or invalid name %q", value)
		}
		seen[value] = true
	}
	return nil
}

// validateSecrets checks the secret slots; destinations already holds the env
// keys spec.environment sets, so a slot cannot collide with one.
func validateSecrets(secrets []Secret, destinations map[string]bool) error {
	names := map[string]bool{}
	for _, secret := range secrets {
		if !namePattern.MatchString(secret.Name) || names[secret.Name] {
			return fmt.Errorf("spec.secrets: invalid or duplicate secret name %q", secret.Name)
		}
		names[secret.Name] = true
		if !envPattern.MatchString(secret.EnvName) || destinations[secret.EnvName] {
			return fmt.Errorf("spec.secrets: secret %q: invalid or duplicate envName", secret.Name)
		}
		destinations[secret.EnvName] = true
		if secret.SecretName != "" && !namePattern.MatchString(secret.SecretName) {
			return fmt.Errorf("spec.secrets: secret %q: invalid secretName", secret.Name)
		}
		if slices.ContainsFunc(secret.Aliases, func(alias string) bool { return !namePattern.MatchString(alias) }) {
			return fmt.Errorf("spec.secrets: secret %q: invalid alias", secret.Name)
		}
	}
	return nil
}

func read(source fs.FS, filename string, out any) error {
	raw, err := fs.ReadFile(source, filename)
	if err != nil {
		return fmt.Errorf("%s: %w", filename, err)
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if decodeErr := d.Decode(out); decodeErr != nil {
		return fmt.Errorf("%s: %w", filename, decodeErr)
	}
	var extra any
	if extraErr := d.Decode(&extra); !errors.Is(extraErr, io.EOF) {
		return fmt.Errorf("%s: expected exactly one YAML document", filename)
	}
	return nil
}
