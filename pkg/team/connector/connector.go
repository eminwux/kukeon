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

// Package connector loads the connector catalog: the `<name>/connector.yaml`
// definitions kuke ships under team/connectors (issue #1548). A connector is a
// kind of access a cell needs, defined as data — which Secret types can be its
// identity, which readable fields an identity carries, where a default may be
// set, and how each of its methods delivers the identity to a cell. Every
// consumer (kuke team, the kuke create commands, the web UI) iterates over
// the catalog, so a new connector needs no code unless it needs a new
// delivery handler.
package connector

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"

	rules "github.com/eminwux/kukeon/internal/connector"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/api/version"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/team/connectors"
	"gopkg.in/yaml.v3"
)

// Kind identifies a connector document.
const Kind = "Connector"

// Connector is one `<name>/connector.yaml` document.
type Connector struct {
	APIVersion string         `json:"apiVersion" yaml:"apiVersion"`
	Kind       string         `json:"kind"       yaml:"kind"`
	Metadata   model.Metadata `json:"metadata"   yaml:"metadata"`
	Spec       Spec           `json:"spec"       yaml:"spec"`
}

// Spec is a connector definition.
type Spec struct {
	// Title is the display name; Description says what the access is for.
	Title       string `json:"title"                 yaml:"title"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Identity says what a stored identity of the connector looks like.
	Identity Identity `json:"identity"              yaml:"identity"`
	// Scopes lists where a default identity may be set: realm, space, stack.
	// Empty allows all three.
	Scopes []string `json:"scopes,omitempty"      yaml:"scopes,omitempty"`
	// Harness names the harness this connector authenticates. kuke team
	// enables exactly one of the connector's methods on every Config of that
	// harness; a connector without one is enabled only when a role or team
	// asks for it.
	Harness string `json:"harness,omitempty"     yaml:"harness,omitempty"`
	// Group is the mutual-exclusion group the method components share.
	// Defaults to the connector's name.
	Group string `json:"group,omitempty"       yaml:"group,omitempty"`
	// Optional lets a cell start without an identity for the connector.
	Optional bool `json:"optional,omitempty"    yaml:"optional,omitempty"`
	// Delivery is the handler of a connector without methods: git or
	// registry. A connector with methods states the delivery on each.
	Delivery string `json:"delivery,omitempty"    yaml:"delivery,omitempty"`
	// Methods are the ways the connector can be delivered to a cell, each a
	// blueprint component. One is the default.
	Methods []Method `json:"methods,omitempty"     yaml:"methods,omitempty"`
}

// Identity describes a stored identity of a connector.
type Identity struct {
	// SecretTypes are the Secret types accepted: opaque, basic-auth, ssh-key.
	SecretTypes []string `json:"secretTypes"      yaml:"secretTypes"`
	// Fields is the schema of the readable identity fields.
	Fields []api.ConnectorField `json:"fields,omitempty" yaml:"fields,omitempty"`
}

// Method is one way to deliver a connector to a cell.
type Method struct {
	// Name is what a team picks (`connectors.<name>.method`).
	Name        string `json:"name"                  yaml:"name"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	// Default marks the method used when a team picks none.
	Default bool `json:"default,omitempty"     yaml:"default,omitempty"`
	// Component is the blueprint component's name; `<connector>-<method>`
	// when empty.
	Component string `json:"component,omitempty"   yaml:"component,omitempty"`
	// Delivery is env, file or stateVolume.
	Delivery string `json:"delivery"              yaml:"delivery"`
	// Requires lists the image capabilities the method needs.
	Requires []string `json:"requires,omitempty"    yaml:"requires,omitempty"`
	// Secret is where the identity lands; absent for stateVolume delivery.
	Secret *MethodSecret `json:"secret,omitempty"      yaml:"secret,omitempty"`
	// Login says how the login of a stateVolume method is detected (marker)
	// and started (command). stateVolume delivery only.
	Login *api.ConnectorLogin `json:"login,omitempty"       yaml:"login,omitempty"`
}

// MethodSecret is the secret slot of an env or file method.
type MethodSecret struct {
	// Name is the blueprint secret slot's name: the key a Config fill and a
	// Realm/Space/Stack default use.
	Name string `json:"name"                 yaml:"name"`
	// EnvName is the environment variable an env method sets.
	EnvName string `json:"envName,omitempty"    yaml:"envName,omitempty"`
	// MountPath is the absolute path a file method mounts the secret at.
	MountPath string `json:"mountPath,omitempty"  yaml:"mountPath,omitempty"`
	// SecretName is the Secret the Realm default points at when the team
	// names no identity; the slot's name when empty.
	SecretName string `json:"secretName,omitempty" yaml:"secretName,omitempty"`
	// Aliases are role-declared secret names that select this method, so a
	// role written before connectors existed keeps working.
	Aliases []string `json:"aliases,omitempty"    yaml:"aliases,omitempty"`
}

var (
	namePattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	envPattern  = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
)

// Catalog is a loaded set of connector definitions, sorted by name.
type Catalog struct {
	Connectors []Connector
}

// Default loads the catalog embedded in kuke.
func Default() (*Catalog, error) { return Load(connectors.FS) }

// Load decodes and validates every `<name>/connector.yaml` in source. A nil
// source is an empty catalog.
func Load(source fs.FS) (*Catalog, error) {
	catalog := &Catalog{}
	if source == nil {
		return catalog, nil
	}
	files, err := fs.Glob(source, "*/connector.yaml")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	components, slots := map[string]string{}, map[string]string{}
	for _, filename := range files {
		var c Connector
		if err = read(source, filename, &c); err != nil {
			return nil, err
		}
		if err = c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", filename, err)
		}
		if c.Metadata.Name != path.Base(path.Dir(filename)) {
			return nil, fmt.Errorf("%s: metadata.name must match its directory", filename)
		}
		// Component and slot names are shared by every blueprint, so they are
		// unique across the catalog.
		for _, method := range c.Spec.Methods {
			name := c.ComponentName(method)
			if owner, dup := components[name]; dup {
				return nil, fmt.Errorf("%s: component %q is already defined by connector %q", filename, name, owner)
			}
			components[name] = c.Metadata.Name
			if method.Secret == nil {
				continue
			}
			for _, key := range append([]string{method.Secret.Name}, method.Secret.Aliases...) {
				if owner, dup := slots[key]; dup {
					return nil, fmt.Errorf("%s: secret name %q is already used by connector %q", filename, key, owner)
				}
				slots[key] = c.Metadata.Name
			}
		}
		if len(c.Spec.Methods) == 0 && c.Spec.Delivery == api.ConnectorDeliveryGit {
			if owner, dup := components[c.Metadata.Name]; dup {
				return nil, fmt.Errorf(
					"%s: component %q is already defined by connector %q", filename, c.Metadata.Name, owner)
			}
			components[c.Metadata.Name] = c.Metadata.Name
		}
		catalog.Connectors = append(catalog.Connectors, c)
	}
	return catalog, nil
}

// Validate checks one connector definition.
func (c *Connector) Validate() error {
	if c == nil || version.Check(c.APIVersion) != nil || c.Kind != Kind || !namePattern.MatchString(c.Metadata.Name) {
		return errors.New("require v1beta1 Connector with valid metadata.name")
	}
	if c.Spec.Title == "" {
		return errors.New("spec.title is required")
	}
	if len(c.Spec.Identity.SecretTypes) == 0 {
		return errors.New("spec.identity.secretTypes: at least one of opaque, basic-auth, ssh-key")
	}
	if c.Spec.Group != "" && !namePattern.MatchString(c.Spec.Group) {
		return errors.New("spec.group: invalid name")
	}
	if c.Spec.Harness != "" && !namePattern.MatchString(c.Spec.Harness) {
		return errors.New("spec.harness: invalid name")
	}
	if len(c.Spec.Methods) == 0 {
		if c.Spec.Delivery != api.ConnectorDeliveryGit && c.Spec.Delivery != api.ConnectorDeliveryRegistry {
			return errors.New("spec.delivery: a connector without methods is delivered by git or registry")
		}
		if c.Spec.Harness != "" {
			return errors.New("spec.harness: only a connector with methods authenticates a harness")
		}
		return rules.ValidateDescriptor(c.Descriptor(Method{}))
	}
	if c.Spec.Delivery != "" {
		return errors.New("spec.delivery: a connector with methods states the delivery on each method")
	}
	names, defaults := map[string]bool{}, 0
	for i, method := range c.Spec.Methods {
		field := fmt.Sprintf("spec.methods[%d]", i)
		if !namePattern.MatchString(method.Name) || names[method.Name] {
			return fmt.Errorf("%s.name: invalid or duplicate method name %q", field, method.Name)
		}
		names[method.Name] = true
		if method.Default {
			defaults++
		}
		if method.Component != "" && !namePattern.MatchString(method.Component) {
			return fmt.Errorf("%s.component: invalid name", field)
		}
		if slices.ContainsFunc(method.Requires, func(r string) bool { return !namePattern.MatchString(r) }) {
			return fmt.Errorf("%s.requires: invalid capability name", field)
		}
		if err := validateMethod(field, method); err != nil {
			return err
		}
		if err := rules.ValidateDescriptor(c.Descriptor(method)); err != nil {
			return err
		}
	}
	if defaults != 1 {
		return errors.New("spec.methods: exactly one method must be the default")
	}
	return nil
}

func validateMethod(field string, method Method) error {
	secret := method.Secret
	switch method.Delivery {
	case api.ConnectorDeliveryStateVolume:
		if secret != nil {
			return fmt.Errorf("%s: a stateVolume method keeps a login and declares no secret", field)
		}
		if login := method.Login; login != nil {
			if login.Marker != "" && !filepath.IsLocal(login.Marker) {
				return fmt.Errorf("%s.login.marker: must be a path inside the state directory", field)
			}
			if slices.Contains(login.Command, "") {
				return fmt.Errorf("%s.login.command: empty argument", field)
			}
		}
		return nil
	case api.ConnectorDeliveryEnv:
		if secret == nil || !envPattern.MatchString(secret.EnvName) || secret.MountPath != "" {
			return fmt.Errorf("%s.secret: an env method requires envName (and no mountPath)", field)
		}
	case api.ConnectorDeliveryFile:
		if secret == nil || !path.IsAbs(secret.MountPath) || secret.EnvName != "" {
			return fmt.Errorf("%s.secret: a file method requires an absolute mountPath (and no envName)", field)
		}
	default:
		return fmt.Errorf("%s.delivery: expected env, file or stateVolume", field)
	}
	if method.Login != nil {
		return fmt.Errorf("%s.login: only a stateVolume method keeps a login", field)
	}
	if !namePattern.MatchString(secret.Name) {
		return fmt.Errorf("%s.secret.name: invalid name", field)
	}
	if secret.SecretName != "" && !namePattern.MatchString(secret.SecretName) {
		return fmt.Errorf("%s.secret.secretName: invalid name", field)
	}
	if slices.ContainsFunc(secret.Aliases, func(alias string) bool { return !namePattern.MatchString(alias) }) {
		return fmt.Errorf("%s.secret.aliases: invalid alias", field)
	}
	return nil
}

// Group is the mutual-exclusion group of the connector's method components.
func (c *Connector) Group() string {
	if c.Spec.Group != "" {
		return c.Spec.Group
	}
	return c.Metadata.Name
}

// ComponentName is the blueprint component a method renders as.
func (c *Connector) ComponentName(method Method) string {
	if method.Component != "" {
		return method.Component
	}
	return c.Metadata.Name + "-" + method.Name
}

// Method returns the named method, or the default one for an empty name.
func (c *Connector) Method(name string) (Method, bool) {
	for _, method := range c.Spec.Methods {
		if (name == "" && method.Default) || (name != "" && method.Name == name) {
			return method, true
		}
	}
	return Method{}, false
}

// MethodNames lists the connector's method names in declaration order.
func (c *Connector) MethodNames() []string {
	out := make([]string, 0, len(c.Spec.Methods))
	for _, method := range c.Spec.Methods {
		out = append(out, method.Name)
	}
	return out
}

// Delivery is the handler of a method, or of the connector itself when it has
// no methods.
func (c *Connector) Delivery(method Method) string {
	if len(c.Spec.Methods) == 0 {
		return c.Spec.Delivery
	}
	return method.Delivery
}

// Descriptor is the definition copy a blueprint component (and every other
// consumer that validates an identity) carries for one method. Pass the zero
// Method for a connector without methods.
func (c *Connector) Descriptor(method Method) api.BlueprintConnector {
	return api.BlueprintConnector{
		Name:        c.Metadata.Name,
		Title:       c.Spec.Title,
		Method:      method.Name,
		Delivery:    c.Delivery(method),
		SecretTypes: slices.Clone(c.Spec.Identity.SecretTypes),
		Scopes:      slices.Clone(c.Spec.Scopes),
		Fields:      slices.Clone(c.Spec.Identity.Fields),
		Optional:    c.Spec.Optional,
		Harness:     c.Spec.Harness,
		Login:       cloneLogin(method.Login),
	}
}

func cloneLogin(in *api.ConnectorLogin) *api.ConnectorLogin {
	if in == nil {
		return nil
	}
	return &api.ConnectorLogin{Marker: in.Marker, Command: slices.Clone(in.Command)}
}

// LoginMethod returns the connector's method that keeps a login in the state
// volume: the named one, or — for an empty name — the default method when it
// is one, else the first that is.
func (c *Connector) LoginMethod(name string) (Method, bool) {
	if name != "" {
		method, ok := c.Method(name)
		return method, ok && method.Delivery == api.ConnectorDeliveryStateVolume
	}
	if method, ok := c.Method(""); ok && method.Delivery == api.ConnectorDeliveryStateVolume {
		return method, true
	}
	for _, method := range c.Spec.Methods {
		if method.Delivery == api.ConnectorDeliveryStateVolume {
			return method, true
		}
	}
	return Method{}, false
}

// Find returns the named connector.
func (c *Catalog) Find(name string) (*Connector, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Connectors {
		if c.Connectors[i].Metadata.Name == name {
			return &c.Connectors[i], true
		}
	}
	return nil, false
}

// Names lists the catalog's connector names, sorted.
func (c *Catalog) Names() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Connectors))
	for i := range c.Connectors {
		out = append(out, c.Connectors[i].Metadata.Name)
	}
	return out
}

// ForHarness returns the connector that authenticates a harness, if any.
func (c *Catalog) ForHarness(harness string) (*Connector, bool) {
	if c == nil {
		return nil, false
	}
	for i := range c.Connectors {
		if c.Connectors[i].Spec.Harness == harness {
			return &c.Connectors[i], true
		}
	}
	return nil, false
}

// Components renders the catalog as blueprint component definitions: one per
// method, plus one for a git-delivered connector (it has no secret slot — it
// only states that the cell needs a git identity). A registry connector is a
// Realm concern and renders no component.
func (c *Catalog) Components() []component.Component {
	if c == nil {
		return nil
	}
	var out []component.Component
	for i := range c.Connectors {
		conn := &c.Connectors[i]
		base := func(name, description string, method Method) component.Component {
			desc := conn.Descriptor(method)
			return component.Component{
				APIVersion: version.V1Beta1, Kind: component.Kind,
				Metadata: model.Metadata{Name: name},
				Spec: component.Spec{
					Kind: api.ComponentKindConnector, Description: description,
					Requires: slices.Clone(method.Requires), Group: conn.Group(), Connector: &desc,
					Harness: conn.Spec.Harness,
				},
			}
		}
		if len(conn.Spec.Methods) == 0 {
			if conn.Spec.Delivery == api.ConnectorDeliveryGit {
				comp := base(conn.Metadata.Name, conn.Spec.Description, Method{})
				comp.Spec.Group = ""
				out = append(out, comp)
			}
			continue
		}
		for _, method := range conn.Spec.Methods {
			description := method.Description
			if description == "" {
				description = conn.Spec.Description
			}
			comp := base(conn.ComponentName(method), description, method)
			if len(conn.Spec.Methods) == 1 && conn.Spec.Group == "" {
				comp.Spec.Group = ""
			}
			if s := method.Secret; s != nil {
				comp.Spec.Secrets = []component.Secret{{
					Name: s.Name, EnvName: s.EnvName, MountPath: s.MountPath, Required: !conn.Spec.Optional,
					SecretName: s.SecretName, Aliases: slices.Clone(s.Aliases),
				}}
			}
			out = append(out, comp)
		}
	}
	return out
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
