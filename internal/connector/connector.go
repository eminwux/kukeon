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

// Package connector holds the connector rules the daemon, kuke and the web UI
// share (issue #1548): how a Secret is checked against a connector definition,
// how the readable identity fields are read, and how a git identity expands
// into a git block. It works on the definition copy a blueprint component
// carries (v1beta1.BlueprintConnector), so it needs no catalog.
package connector

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

var (
	nameRE   = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)
	fieldRE  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	personRE = regexp.MustCompile(`^\s*(.*\S)\s*<\s*([^<>\s]+@[^<>\s]+)\s*>\s*$`)
)

// SecretTypes are the Secret types a connector may accept.
//
//nolint:gochecknoglobals // package-level enumeration, read-only.
var SecretTypes = []string{v1beta1.SecretTypeOpaque, v1beta1.SecretTypeBasicAuth, v1beta1.SecretTypeSSHKey}

// Scopes are the scopes a connector default may be set at, shallowest first.
//
//nolint:gochecknoglobals // package-level enumeration, read-only.
var Scopes = []string{v1beta1.ConnectorScopeRealm, v1beta1.ConnectorScopeSpace, v1beta1.ConnectorScopeStack}

// NeedsIdentity reports whether a delivery handler consumes a stored identity.
func NeedsIdentity(delivery string) bool {
	return delivery != v1beta1.ConnectorDeliveryStateVolume
}

// ValidName reports whether name can be a connector name.
func ValidName(name string) bool { return nameRE.MatchString(name) }

// ValidateDescriptor checks one connector definition: a valid name, a known
// delivery handler, known Secret types and scopes, and a well-formed field
// schema.
func ValidateDescriptor(desc v1beta1.BlueprintConnector) error {
	if !nameRE.MatchString(desc.Name) {
		return fmt.Errorf(
			"%w: connector name %q must be a lowercase identifier",
			errdefs.ErrConnectorInvalid,
			desc.Name,
		)
	}
	switch desc.Delivery {
	case v1beta1.ConnectorDeliveryEnv, v1beta1.ConnectorDeliveryFile, v1beta1.ConnectorDeliveryStateVolume,
		v1beta1.ConnectorDeliveryGit, v1beta1.ConnectorDeliveryRegistry:
	default:
		return fmt.Errorf(
			"%w: connector %q: delivery %q must be env, file, stateVolume, git or registry",
			errdefs.ErrConnectorInvalid, desc.Name, desc.Delivery)
	}
	for _, t := range desc.SecretTypes {
		if !slices.Contains(SecretTypes, t) {
			return fmt.Errorf("%w: connector %q: unknown secret type %q", errdefs.ErrConnectorInvalid, desc.Name, t)
		}
	}
	for _, scope := range desc.Scopes {
		if !slices.Contains(Scopes, scope) {
			return fmt.Errorf(
				"%w: connector %q: scope %q must be realm, space or stack",
				errdefs.ErrConnectorInvalid,
				desc.Name,
				scope,
			)
		}
	}
	seen := map[string]struct{}{}
	for _, field := range desc.Fields {
		if !fieldRE.MatchString(field.Name) {
			return fmt.Errorf(
				"%w: connector %q: field name %q is not an identifier",
				errdefs.ErrConnectorInvalid,
				desc.Name,
				field.Name,
			)
		}
		if _, dup := seen[field.Name]; dup {
			return fmt.Errorf(
				"%w: connector %q: field %q is declared twice", errdefs.ErrConnectorInvalid, desc.Name, field.Name)
		}
		seen[field.Name] = struct{}{}
		switch field.Type {
		case "", v1beta1.ConnectorFieldString, v1beta1.ConnectorFieldList,
			v1beta1.ConnectorFieldPerson, v1beta1.ConnectorFieldSecret:
		default:
			return fmt.Errorf(
				"%w: connector %q: field %q: type %q must be string, list, person or secret",
				errdefs.ErrConnectorInvalid, desc.Name, field.Name, field.Type)
		}
		for _, t := range field.SecretTypes {
			if !slices.Contains(SecretTypes, t) {
				return fmt.Errorf(
					"%w: connector %q: field %q: unknown secret type %q",
					errdefs.ErrConnectorInvalid, desc.Name, field.Name, t)
			}
		}
	}
	return nil
}

// AcceptsScope reports whether a default identity of the connector may be set
// at scope. A connector that lists no scopes accepts every one.
func AcceptsScope(desc v1beta1.BlueprintConnector, scope string) bool {
	return len(desc.Scopes) == 0 || slices.Contains(desc.Scopes, scope)
}

// effectiveType resolves the empty Secret type to opaque.
func effectiveType(secretType string) string {
	if strings.TrimSpace(secretType) == "" {
		return v1beta1.SecretTypeOpaque
	}
	return strings.TrimSpace(secretType)
}

// CheckSecret checks that a Secret can be an identity of the connector: its
// tag, when it has one, names this connector; its type is one the connector
// accepts; and its readable identity fields fit the connector's schema. An
// untagged Secret is accepted, so one created before connectors existed can
// still be named by hand. Only the readable part of the Secret is looked at.
func CheckSecret(desc v1beta1.BlueprintConnector, secret v1beta1.SecretSpec) error {
	if tag := strings.TrimSpace(secret.Connector); tag != "" && tag != desc.Name {
		return fmt.Errorf(
			"%w: it is an identity of connector %q, not %q", errdefs.ErrConnectorIdentity, tag, desc.Name)
	}
	secretType := effectiveType(secret.Type)
	if len(desc.SecretTypes) > 0 && !slices.Contains(desc.SecretTypes, secretType) {
		return fmt.Errorf(
			"%w: connector %q accepts a %s secret, not %s",
			errdefs.ErrConnectorIdentity, desc.Name, strings.Join(desc.SecretTypes, " or "), secretType)
	}
	if strings.TrimSpace(secret.Connector) == "" {
		return nil
	}
	return ValidateIdentity(desc, secretType, secret.Identity)
}

// ValidateIdentity checks the readable identity fields of a Secret against the
// connector's schema: no unknown field, every required field set, and every
// value readable as its declared type.
func ValidateIdentity(desc v1beta1.BlueprintConnector, secretType string, identity map[string]string) error {
	secretType = effectiveType(secretType)
	fields := map[string]v1beta1.ConnectorField{}
	for _, field := range desc.Fields {
		fields[field.Name] = field
	}
	names := make([]string, 0, len(identity))
	for name := range identity {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		field, ok := fields[name]
		if !ok {
			return fmt.Errorf(
				"%w: connector %q declares no identity field %q", errdefs.ErrConnectorIdentity, desc.Name, name)
		}
		value := strings.TrimSpace(identity[name])
		if value == "" {
			continue
		}
		if len(field.SecretTypes) > 0 && !slices.Contains(field.SecretTypes, secretType) {
			return fmt.Errorf(
				"%w: identity field %q applies to a %s secret, not %s",
				errdefs.ErrConnectorIdentity, name, strings.Join(field.SecretTypes, " or "), secretType)
		}
		if err := validateField(field, value); err != nil {
			return fmt.Errorf("%w: identity field %q: %w", errdefs.ErrConnectorIdentity, name, err)
		}
	}
	for _, field := range desc.Fields {
		if !field.Required || (len(field.SecretTypes) > 0 && !slices.Contains(field.SecretTypes, secretType)) {
			continue
		}
		if strings.TrimSpace(identity[field.Name]) == "" {
			return fmt.Errorf(
				"%w: connector %q requires identity field %q", errdefs.ErrConnectorIdentity, desc.Name, field.Name)
		}
	}
	return nil
}

func validateField(field v1beta1.ConnectorField, value string) error {
	checkEnum := func(entry string) error {
		if len(field.Enum) > 0 && !slices.Contains(field.Enum, entry) {
			return fmt.Errorf("%q must be one of %s", entry, strings.Join(field.Enum, ", "))
		}
		return nil
	}
	switch field.Type {
	case v1beta1.ConnectorFieldList:
		for _, entry := range ParseList(value) {
			if strings.ContainsAny(entry, " \t\"'\\") {
				return fmt.Errorf("%q must not contain spaces or quotes", entry)
			}
			if err := checkEnum(entry); err != nil {
				return err
			}
		}
	case v1beta1.ConnectorFieldPerson:
		if _, _, err := ParsePerson(value); err != nil {
			return err
		}
	case v1beta1.ConnectorFieldSecret:
		if _, err := ParseSecretRef(value, v1beta1.ContainerSecretRef{Realm: "default"}); err != nil {
			return err
		}
	default:
		return checkEnum(value)
	}
	return nil
}

// ParseList splits a comma-separated identity field, dropping empty entries.
func ParseList(value string) []string {
	var out []string
	for _, entry := range strings.Split(value, ",") {
		if entry = strings.TrimSpace(entry); entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// ParsePerson reads a `Name <email>` identity field.
func ParsePerson(value string) (string, string, error) {
	m := personRE.FindStringSubmatch(value)
	if m == nil {
		return "", "", errors.New("want `Name <email>`")
	}
	return m[1], m[2], nil
}

// FormatPerson is the inverse of ParsePerson.
func FormatPerson(name, email string) string { return name + " <" + email + ">" }

// ParseSecretRef reads a Secret reference written
// `name[@realm[/space[/stack]]]`. Without a scope the reference takes base's
// scope (its Name is ignored).
func ParseSecretRef(value string, base v1beta1.ContainerSecretRef) (v1beta1.ContainerSecretRef, error) {
	name, scope, scoped := strings.Cut(strings.TrimSpace(value), "@")
	ref := v1beta1.ContainerSecretRef{Name: strings.TrimSpace(name)}
	if scoped {
		parts := strings.Split(scope, "/")
		const maxScope = 3 // realm/space/stack
		if len(parts) > maxScope {
			return ref, fmt.Errorf("secret reference %q: want name[@realm[/space[/stack]]]", value)
		}
		for len(parts) < maxScope {
			parts = append(parts, "")
		}
		ref.Realm, ref.Space, ref.Stack = strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]),
			strings.TrimSpace(parts[2])
	} else {
		ref.Realm, ref.Space, ref.Stack, ref.Cell = base.Realm, base.Space, base.Stack, base.Cell
	}
	for _, segment := range []string{ref.Name, ref.Realm, ref.Space, ref.Stack} {
		if segment == "." || segment == ".." || strings.ContainsAny(segment, "/\\\x00 \t") {
			return ref, fmt.Errorf("secret reference %q: want name[@realm[/space[/stack]]]", value)
		}
	}
	if ref.Name == "" || ref.Realm == "" || (ref.Stack != "" && ref.Space == "") {
		return ref, fmt.Errorf("secret reference %q: want name[@realm[/space[/stack]]]", value)
	}
	return ref, nil
}

// FormatSecretRef writes a Secret reference as `name@realm[/space[/stack]]`.
func FormatSecretRef(ref v1beta1.ContainerSecretRef) string {
	scope := []string{ref.Realm}
	for _, part := range []string{ref.Space, ref.Stack, ref.Cell} {
		if strings.TrimSpace(part) == "" {
			break
		}
		scope = append(scope, part)
	}
	return ref.Name + "@" + strings.Join(scope, "/")
}

// SameSecret reports whether two references name the same stored Secret.
func SameSecret(a, b v1beta1.ContainerSecretRef) bool {
	trim := strings.TrimSpace
	return trim(a.Name) == trim(b.Name) && trim(a.Realm) == trim(b.Realm) && trim(a.Space) == trim(b.Space) &&
		trim(a.Stack) == trim(b.Stack) && trim(a.Cell) == trim(b.Cell)
}

// ParseOverrides reads per-cell connector overrides written
// `<connector>=<secret>[@realm[/space[/stack]]]` — the argument of
// `--connector` — into a map keyed by connector name. A secret named without
// a scope is looked up in realm.
func ParseOverrides(args []string, realm string) (map[string]v1beta1.ContainerSecretRef, error) {
	if len(args) == 0 {
		return nil, nil //nolint:nilnil // no override is a valid input, not an error
	}
	out := make(map[string]v1beta1.ContainerSecretRef, len(args))
	for _, arg := range args {
		name, value, ok := strings.Cut(arg, "=")
		name = strings.TrimSpace(name)
		if !ok || !ValidName(name) || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf(
				"%w: --connector %q: want <connector>=<secret>[@realm[/space[/stack]]]",
				errdefs.ErrConnectorInvalid, arg)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%w: --connector names %q more than once", errdefs.ErrConnectorInvalid, name)
		}
		ref, err := ParseSecretRef(value, v1beta1.ContainerSecretRef{Realm: strings.TrimSpace(realm)})
		if err != nil {
			return nil, fmt.Errorf("%w: --connector %s: %w", errdefs.ErrConnectorInvalid, name, err)
		}
		out[name] = ref
	}
	return out, nil
}
