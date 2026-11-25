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

package compose

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

// DefaultSecret is a secret slot a team Config leaves to the scope defaults
// (issue #1548): the slot's name and the Secret the Realm default points at
// when no Realm, Space or Stack default exists for it yet. Application writes
// that default once, so the Config inherits it live and a later change of the
// default reaches every Config that did not choose an identity of its own.
type DefaultSecret struct {
	Slot      string
	Reference config.SecretRef
}

// checkChoices validates the team's connector choices against the catalog:
// every named connector and method exists, and a choice asks only for what
// the connector can do. It runs before any pair is planned, so a typo is
// reported once and by its field.
func checkChoices(catalog *connector.Catalog, d config.Defaults) error {
	check := func(field string, choices map[string]config.ConnectorChoice) error {
		for _, name := range slices.Sorted(maps.Keys(choices)) {
			if err := checkChoice(catalog, field+"."+name, name, choices[name]); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check("spec.connectors", d.Connectors); err != nil {
		return err
	}
	for _, role := range d.Roles {
		merged := make(map[string]config.ConnectorChoice, len(role.Connectors))
		for name, choice := range role.Connectors {
			merged[name] = mergeChoice(d.Connectors[name], choice)
		}
		if err := check("roles "+role.Name+": connectors", merged); err != nil {
			return err
		}
	}
	return nil
}

// mergeChoice layers a role entry's choice over the team's. A role entry that
// names no method keeps the team's method, and with it the team's identity
// unless it names its own; one that names a method states the whole choice,
// because an identity belongs to the method it was picked for.
func mergeChoice(team, role config.ConnectorChoice) config.ConnectorChoice {
	if role.Method != "" {
		return role
	}
	role.Method = team.Method
	if role.Identity == "" {
		role.Identity = team.Identity
	}
	if role.State == "" {
		role.State = team.State
	}
	return role
}

func checkChoice(catalog *connector.Catalog, field, name string, choice config.ConnectorChoice) error {
	conn, ok := catalog.Find(name)
	if !ok {
		return fmt.Errorf("%s: unknown connector %q (known: %s)", field, name, strings.Join(catalog.Names(), ", "))
	}
	if len(conn.Spec.Methods) == 0 {
		if conn.Spec.Delivery == api.ConnectorDeliveryRegistry {
			return fmt.Errorf(
				"%s: a %s identity is set on a Realm (kuke create registry-credential), not on a team", field, name)
		}
		if choice.Method != "" {
			return fmt.Errorf("%s.method: connector %q has no methods", field, name)
		}
		return nil
	}
	method, ok := conn.Method(choice.Method)
	if !ok {
		return fmt.Errorf("%s.method: connector %q has no method %q (methods: %s)",
			field, name, choice.Method, strings.Join(conn.MethodNames(), ", "))
	}
	// With a method that keeps its login in the state volume the identity
	// names a login identity — a Volume (#1685) — so any method takes one.
	if choice.State != "" && conn.Spec.Harness == "" {
		return fmt.Errorf("%s.state: connector %q authenticates no harness, so it keeps no harness state",
			field, name)
	}
	if choice.State == config.StateEphemeral && method.Delivery == api.ConnectorDeliveryStateVolume {
		return fmt.Errorf(
			"%s.state: ephemeral cannot be used with method %q of connector %q: "+
				"the login is kept in the state volume and would not survive the cell",
			field, method.Name, name)
	}
	return nil
}

// choice is the team's decision for one connector on this pair: the role
// entry's wins over the team's. The second result reports whether either set
// one.
func (p *pairPlan) choice(name string) (config.ConnectorChoice, bool) {
	if choice, ok := p.selection.Connectors[name]; ok {
		return mergeChoice(p.choices[name], choice), true
	}
	choice, ok := p.choices[name]
	return choice, ok
}

// harnessDeclares reports whether a harness definition still declares one of
// the connector's secret slots itself — a harness.yaml written before harness
// auth moved into the connector catalog. Such a harness keeps its own slots;
// rendering the connector's components next to them would declare each slot
// twice.
func harnessDeclares(h *harness.Harness, conn *connector.Connector) bool {
	slots := slices.Clone(h.Spec.Secrets)
	for _, mode := range h.Spec.Modes {
		slots = append(slots, mode.Secrets...)
	}
	for _, method := range conn.Spec.Methods {
		if method.Secret == nil {
			continue
		}
		if slices.ContainsFunc(slots, func(s api.BlueprintSecretSlot) bool { return s.Name == method.Secret.Name }) {
			return true
		}
	}
	return false
}

// rendersInto reports whether a library component belongs in the blueprint of
// a harness: a harness's auth methods go only into that harness's blueprints,
// and not at all when the harness still declares the slots itself.
func rendersInto(c component.Component, h *harness.Harness, catalog *connector.Catalog) bool {
	if c.Spec.Harness == "" {
		return true
	}
	if c.Spec.Harness != h.Metadata.Name {
		return false
	}
	conn, ok := catalog.ForHarness(h.Metadata.Name)
	return ok && !harnessDeclares(h, conn)
}

// pickMethod decides which method of a connector a pair uses: the one the
// team chose, else the one a secret name the role lists selects (a role
// written before connectors existed names `claude-code-oauth-token` to mean
// the subscription method), else the connector's default.
func pickMethod(plan *pairPlan, conn *connector.Connector, names []string) (connector.Method, error) {
	if choice, ok := plan.choice(conn.Metadata.Name); ok && choice.Method != "" {
		method, found := conn.Method(choice.Method)
		if !found {
			return connector.Method{}, fmt.Errorf("connector %q has no method %q", conn.Metadata.Name, choice.Method)
		}
		return method, nil
	}
	var named []connector.Method
	for _, method := range conn.Spec.Methods {
		if method.Secret == nil {
			continue
		}
		keys := append([]string{method.Secret.Name}, method.Secret.Aliases...)
		if slices.ContainsFunc(keys, func(key string) bool { return slices.Contains(names, key) }) {
			named = append(named, method)
		}
	}
	if len(named) > 0 {
		if len(named) > 1 {
			var listed []string
			for _, method := range named {
				listed = append(listed, method.Name)
			}
			plan.notes = append(plan.notes, fmt.Sprintf(
				"role %s: its secrets select methods %s of connector %s, which are alternatives; "+
					"using %s (set spec.connectors.%s.method to choose)",
				plan.selection.Name, strings.Join(listed, " and "), conn.Metadata.Name, named[0].Name,
				conn.Metadata.Name,
			))
		}
		return named[0], nil
	}
	method, found := conn.Method("")
	if !found {
		return connector.Method{}, fmt.Errorf("connector %q declares no default method", conn.Metadata.Name)
	}
	return method, nil
}

// planConnectors enables the connector components a pair requires (issue
// #1548): the connectors the role lists, the ones the team's role entry adds,
// the method that authenticates the harness, and a git identity when the role
// works on repositories. names are the secret names the role lists, which may
// select a method.
func planConnectors(
	plan *pairPlan, lib []component.Component, catalog *connector.Catalog, names []string, enable func(string) error,
) error {
	require := func(conn *connector.Connector) error {
		if len(conn.Spec.Methods) == 0 {
			if conn.Spec.Delivery != api.ConnectorDeliveryGit {
				return fmt.Errorf("connector %q is not delivered to a cell", conn.Metadata.Name)
			}
			return enable(conn.Metadata.Name)
		}
		if conn.Spec.Harness != "" &&
			(conn.Spec.Harness != plan.hsel.Name || harnessDeclares(plan.definition, conn)) {
			return nil
		}
		method, err := pickMethod(plan, conn, names)
		if err != nil {
			return err
		}
		if conn.Spec.Harness != "" {
			// The connector that authenticates this pair's harness, and the
			// method it uses: what decides the state volume (#1685).
			plan.harnessConnector, plan.harnessMethod = conn.Metadata.Name, method
		}
		return enable(conn.ComponentName(method))
	}
	for _, name := range plan.role.Spec.Connectors {
		if conn, ok := catalog.Find(name); ok {
			if err := require(conn); err != nil {
				return fmt.Errorf("role connectors: %w", err)
			}
			continue
		}
		// A connector the component library defines directly.
		if err := enableKind(lib, name, api.ComponentKindConnector, "connectors", enable); err != nil {
			return err
		}
	}
	for _, name := range slices.Sorted(maps.Keys(plan.selection.Connectors)) {
		conn, ok := catalog.Find(name)
		if !ok {
			return fmt.Errorf("roles %s: connectors: unknown connector %q", plan.selection.Name, name)
		}
		if err := require(conn); err != nil {
			return fmt.Errorf("roles %s: connectors: %w", plan.selection.Name, err)
		}
	}
	if conn, ok := catalog.ForHarness(plan.hsel.Name); ok {
		if err := require(conn); err != nil {
			return err
		}
	}
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		if len(conn.Spec.Methods) != 0 || conn.Spec.Delivery != api.ConnectorDeliveryGit {
			continue
		}
		if len(plan.role.Spec.Needs.Repos) > 0 {
			if err := enable(conn.Metadata.Name); err != nil {
				return err
			}
		}
		// The identity the team chose goes into the Config, never the Stack:
		// Stack defaults are shared by every team that lives in the stack.
		if choice, ok := plan.choice(conn.Metadata.Name); ok && choice.Identity != "" {
			ref, err := config.ParseIdentity(choice.Identity, plan.realm)
			if err != nil {
				return fmt.Errorf("connector %s: identity: %w", conn.Metadata.Name, err)
			}
			plan.git = &api.ContainerGit{IdentityRef: secretRef(ref)}
		}
	}
	return nil
}

// identityChoice returns the identity the team chose for the connector a
// component delivers, if any.
func identityChoice(plan *pairPlan, c component.Component) (config.SecretRef, bool, error) {
	if c.Spec.Connector == nil {
		return config.SecretRef{}, false, nil
	}
	choice, ok := plan.choice(c.Spec.Connector.Name)
	// The identity of a login method is a Volume, not a slot fill (#1685).
	if !ok || choice.Identity == "" || c.Spec.Connector.Delivery == api.ConnectorDeliveryStateVolume {
		return config.SecretRef{}, false, nil
	}
	ref, err := config.ParseIdentity(choice.Identity, plan.realm)
	if err != nil {
		return config.SecretRef{}, false, fmt.Errorf("connector %s: identity: %w", c.Spec.Connector.Name, err)
	}
	return ref, true, nil
}
