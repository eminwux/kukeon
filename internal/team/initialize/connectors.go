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

package initialize

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// Per-role connectors (issue #1649): which connectors a team gives a role on
// top of what the role asks for itself. A role entry enables one with a
// connectors entry or, in the older spelling, with a secret name the
// connector's methods answer to in needs.secrets. These functions read and
// change that one field of a stored Team; `kuke team connector` and the web
// server's team form both call them.

// Togglable reports whether a team can give a role this connector (D3): one
// that authenticates no harness and is not a registry credential. A
// harness's connector and a registry credential are chosen in the team's
// spec.connectors.
func Togglable(conn *connector.Connector) bool {
	return conn != nil && conn.Spec.Harness == "" && conn.Spec.Delivery != api.ConnectorDeliveryRegistry
}

// TogglableConnectors lists the catalog's togglable connectors, sorted by
// name.
func TogglableConnectors(catalog *connector.Catalog) []*connector.Connector {
	var out []*connector.Connector
	if catalog == nil {
		return nil
	}
	for i := range catalog.Connectors {
		if conn := &catalog.Connectors[i]; Togglable(conn) {
			out = append(out, conn)
		}
	}
	return out
}

// secretNames are the needs.secrets names that select conn: every method's
// secret slot and its aliases.
func secretNames(conn *connector.Connector) []string {
	var out []string
	for _, method := range conn.Spec.Methods {
		if method.Secret == nil {
			continue
		}
		out = append(out, method.Secret.Name)
		out = append(out, method.Secret.Aliases...)
	}
	return out
}

// componentNames are the blueprint components that deliver conn: one per
// method, or the connector's own name for a git-delivered one.
func componentNames(conn *connector.Connector) []string {
	if len(conn.Spec.Methods) == 0 {
		return []string{conn.Metadata.Name}
	}
	out := make([]string, 0, len(conn.Spec.Methods))
	for _, method := range conn.Spec.Methods {
		out = append(out, conn.ComponentName(method))
	}
	return out
}

// RoleConnectorSet reports whether the role entry enables conn: a connectors
// key, or a secret name of conn in needs.secrets.
func RoleConnectorSet(role *config.Role, conn *connector.Connector) bool {
	if role == nil || conn == nil {
		return false
	}
	if _, ok := role.Connectors[conn.Metadata.Name]; ok {
		return true
	}
	if role.Needs == nil {
		return false
	}
	names := secretNames(conn)
	return slices.ContainsFunc(role.Needs.Secrets, func(name string) bool { return slices.Contains(names, name) })
}

// EnableRoleConnector gives role the connector name: it adds a connectors
// entry when the role entry does not enable it yet. identity nil keeps the
// entry's identity; a pointer sets it, and "" removes it.
func EnableRoleConnector(team *config.Team, catalog *connector.Catalog, role, name string, identity *string) error {
	conn, err := togglableConnector(catalog, name)
	if err != nil {
		return err
	}
	entry, err := roleEntry(team, role)
	if err != nil {
		return err
	}
	var value string
	if identity != nil {
		value = strings.TrimSpace(*identity)
		if value != "" {
			if _, err = config.ParseIdentity(value, teamRealm(team)); err != nil {
				return fmt.Errorf("%w: --identity: %w", errdefs.ErrInvalidTeam, err)
			}
		}
	}
	choice, listed := entry.Connectors[conn.Metadata.Name]
	switch {
	case listed && identity == nil:
		return nil
	case !listed && identity == nil && RoleConnectorSet(entry, conn):
		// The older needs.secrets spelling already enables it (D1).
		return nil
	case listed && choice.Identity == value:
		return nil
	}
	choice.Identity = value
	if entry.Connectors == nil {
		entry.Connectors = map[string]config.ConnectorChoice{}
	}
	entry.Connectors[conn.Metadata.Name] = choice
	return nil
}

// DisableRoleConnector takes the connector name away from role's entry: its
// connectors entry and every secret name of it in needs.secrets (D2). A
// needs left empty, and an empty connectors map, are omitted.
func DisableRoleConnector(team *config.Team, catalog *connector.Catalog, role, name string) error {
	conn, err := togglableConnector(catalog, name)
	if err != nil {
		return err
	}
	entry, err := roleEntry(team, role)
	if err != nil {
		return err
	}
	if _, ok := entry.Connectors[conn.Metadata.Name]; ok {
		delete(entry.Connectors, conn.Metadata.Name)
		if len(entry.Connectors) == 0 {
			entry.Connectors = nil
		}
	}
	if entry.Needs == nil {
		return nil
	}
	names := secretNames(conn)
	if !slices.ContainsFunc(entry.Needs.Secrets, func(s string) bool { return slices.Contains(names, s) }) {
		return nil
	}
	needs := *entry.Needs
	needs.Secrets = slices.DeleteFunc(slices.Clone(needs.Secrets), func(s string) bool {
		return slices.Contains(names, s)
	})
	if len(needs.Secrets) == 0 {
		needs.Secrets = nil
	}
	if len(needs.Image) == 0 && len(needs.Secrets) == 0 {
		entry.Needs = nil
		return nil
	}
	entry.Needs = &needs
	return nil
}

// togglableConnector finds name in the catalog and checks a team can give
// it to a role (AC3).
func togglableConnector(catalog *connector.Catalog, name string) (*connector.Connector, error) {
	conn, ok := catalog.Find(name)
	if !ok {
		return nil, fmt.Errorf("%w: unknown connector %q; known connectors: %s",
			errdefs.ErrInvalidTeam, name, strings.Join(catalog.Names(), ", "))
	}
	if !Togglable(conn) {
		what := "is a registry credential"
		if conn.Spec.Harness != "" {
			what = "authenticates the " + conn.Spec.Harness + " harness"
		}
		return nil, fmt.Errorf("%w: connector %s %s, so a role entry does not enable it; "+
			"choose its method and identity in the team's spec.connectors (kuke team apply -f, "+
			"or the Edit team form's Credentials step)", errdefs.ErrInvalidTeam, name, what)
	}
	return conn, nil
}

// roleEntry is the team's entry for role. A team that inherits its role list
// has no entries to change (D5).
func roleEntry(team *config.Team, role string) (*config.Role, error) {
	name := team.Metadata.Name
	if team.Spec.Roles == nil {
		return nil, fmt.Errorf("%w: team %s inherits its roles from the defaults, so it has no role entry to "+
			"change; declare its roles first: kuke team export %s --host-fields --explicit-defaults > %s.yaml, "+
			"then kuke team apply -f %s.yaml", errdefs.ErrInvalidTeam, name, name, name, name)
	}
	names := make([]string, 0, len(team.Spec.Roles))
	for i := range team.Spec.Roles {
		if team.Spec.Roles[i].Name == role {
			return &team.Spec.Roles[i], nil
		}
		names = append(names, team.Spec.Roles[i].Name)
	}
	return nil, fmt.Errorf("%w: team %s selects no role %q; its roles: %s",
		errdefs.ErrInvalidTeam, name, role, strings.Join(names, ", "))
}

func teamRealm(team *config.Team) string {
	if team.Metadata.Realm == "" {
		return config.DefaultRealm
	}
	return team.Metadata.Realm
}

// ConnectorChange is one `kuke team connector enable|disable`: the connector
// given to (Enable) or taken from each of Roles of a stored team.
type ConnectorChange struct {
	Team, Connector string
	Roles           []string
	Enable          bool
	// Identity, on an enable, sets the entry's identity ("" removes it);
	// nil keeps it.
	Identity *string
}

// command is the change as the CLI line that makes it, for rerun hints.
func (c ConnectorChange) command() string {
	verb := "disable"
	if c.Enable {
		verb = "enable"
	}
	parts := []string{"kuke team connector", verb, c.Team, c.Connector}
	for _, role := range c.Roles {
		parts = append(parts, "--role "+role)
	}
	if c.Enable && c.Identity != nil {
		parts = append(parts, fmt.Sprintf("--identity %q", *c.Identity))
	}
	return strings.Join(parts, " ")
}

// SetRoleConnector changes the role entries of a stored Team and applies the
// team, exactly as kuke team apply -f of the edited declaration would. A
// disable that the composition contradicts — a Config of the role still
// renders the connector because the role's definition or the team's harness
// entry asks for it — writes nothing (D4).
func SetRoleConnector(ctx context.Context, opts Options, change ConnectorChange) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	if len(change.Roles) == 0 {
		return Outcome{}, fmt.Errorf("%w: name at least one role with --role", errdefs.ErrInvalidTeam)
	}
	catalog, err := connector.Default()
	if err != nil {
		return Outcome{}, fmt.Errorf("load connector catalog: %w", err)
	}
	stored, err := StoredTeam(ctx, opts.Client, change.Team)
	if err != nil {
		return Outcome{}, err
	}
	if stored == nil {
		return Outcome{}, notStored(change.Team, config.DefaultRealm)
	}
	for _, role := range change.Roles {
		if change.Enable {
			err = EnableRoleConnector(stored, catalog, role, change.Connector, change.Identity)
		} else {
			err = DisableRoleConnector(stored, catalog, role, change.Connector)
		}
		if err != nil {
			return Outcome{}, err
		}
	}
	defaults, _, err := readDefaults(ctx, opts.Client, stored.Metadata.Realm)
	if err != nil {
		return Outcome{}, err
	}
	req := request{
		command: change.command(), origin: Origin{Kind: OriginStored}, defaults: defaults, team: stored,
		have: injectedSources(opts.Sources),
	}
	p, err := plan(ctx, opts.Client, req)
	if err != nil {
		return Outcome{}, withRerun(err, req.command)
	}
	if !change.Enable {
		conn, _ := catalog.Find(change.Connector)
		if err = stillRendered(p, conn, change.Roles); err != nil {
			return Outcome{}, err
		}
	}
	if opts.DryRun {
		return Outcome{Plan: p}, nil
	}
	if err = ctx.Err(); err != nil {
		return Outcome{Plan: p}, err
	}
	result, err := commit(ctx, opts.Client, p)
	if err != nil {
		return Outcome{Plan: p}, withRerun(err, req.command)
	}
	return Outcome{Plan: p, Result: &result}, nil
}

// stillRendered fails when a Config of one of roles still renders a
// component of conn after the role entries stopped asking for it (D4).
func stillRendered(p *Plan, conn *connector.Connector, roles []string) error {
	components := componentNames(conn)
	var configs []string
	for _, pair := range p.Pairs {
		if !slices.Contains(roles, pair.Role) {
			continue
		}
		if slices.ContainsFunc(pair.Components, func(c string) bool { return slices.Contains(components, c) }) {
			configs = append(configs, pair.Config)
		}
	}
	if len(configs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: connector %s is still rendered on %s after the change: the role's definition "+
		"or the team's harness entry requires it, not the role entry; nothing was written",
		errdefs.ErrInvalidTeam, conn.Metadata.Name, strings.Join(configs, ", "))
}

// RoleConnector is one connector a Config of a team resolves (D8): the row
// of kuke team connector list and of the team page's Connectors section.
type RoleConnector struct {
	Role      string `json:"role"`
	Config    string `json:"config"`
	Task      string `json:"task,omitempty"`
	Connector string `json:"connector"`
	Method    string `json:"method,omitempty"`
	// State is resolved or unresolved, as status.connectors reports it.
	State string `json:"state"`
	// Identity is the Secret that fills the connector, as name[@scope]
	// (the scope only when it is not the Config's realm); empty when
	// nothing does, or when the method keeps a login.
	Identity string `json:"identity,omitempty"`
	// Origin is the level the identity came from: config, stack, space or
	// realm.
	Origin   string `json:"origin,omitempty"`
	Required bool   `json:"required,omitempty"`
	// Fill is what fills the connector as kuke get configs -o wide prints
	// it, with "-" for a login.
	Fill string `json:"fill"`
	// SetByTeam reports that the team's role entry enables the connector;
	// otherwise the role definition or the harness asks for it.
	SetByTeam bool `json:"setByTeam"`
}

// RoleConnectors reports what each Config of a stored team resolves, from
// the stored Team and its Configs' status.connectors, with no fetch (D8).
func RoleConnectors(ctx context.Context, client Client, team string) ([]RoleConnector, error) {
	catalog, err := connector.Default()
	if err != nil {
		return nil, fmt.Errorf("load connector catalog: %w", err)
	}
	stored, err := StoredTeam(ctx, client, team)
	if err != nil {
		return nil, err
	}
	if stored == nil {
		return nil, notStored(team, config.DefaultRealm)
	}
	configs, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, requestError("lookup", "configs", err)
	}
	return RoleConnectorsOf(stored, configs, catalog), nil
}

// RoleConnectorsOf builds the rows of RoleConnectors from a stored Team and
// configs (any Configs: only the team's are read), sorted by role, Config
// and connector.
func RoleConnectorsOf(stored *config.Team, configs []api.CellConfigDoc, catalog *connector.Catalog) []RoleConnector {
	out := []RoleConnector{}
	entries := map[string]*config.Role{}
	for i := range stored.Spec.Roles {
		entries[stored.Spec.Roles[i].Name] = &stored.Spec.Roles[i]
	}
	for _, doc := range configs {
		labels := doc.Metadata.Labels
		if labels[api.LabelTeam] != stored.Metadata.Name || doc.Status == nil {
			continue
		}
		role := labels[api.LabelRole]
		for _, status := range doc.Status.Connectors {
			row := RoleConnector{
				Role: role, Config: doc.Metadata.Name, Task: labels[api.LabelTask], Connector: status.Connector,
				Method: status.Method, State: status.State, Origin: status.Origin, Required: status.Required,
				Fill: status.Fill(),
			}
			if status.Identity != nil {
				row.Identity = identityText(*status.Identity, doc.Metadata.Realm)
			}
			if status.State == api.ConnectorResolved && status.Identity == nil {
				row.Fill = "-"
			}
			if conn, ok := catalog.Find(status.Connector); ok {
				row.SetByTeam = RoleConnectorSet(entries[role], conn)
			}
			out = append(out, row)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Role != b.Role {
			return a.Role < b.Role
		}
		if a.Config != b.Config {
			return a.Config < b.Config
		}
		return a.Connector < b.Connector
	})
	return out
}

// identityText is a Secret reference as name[@realm[/space[/stack]]], the
// scope left out when it is the Config's realm.
func identityText(ref api.ContainerSecretRef, realm string) string {
	if realm == "" {
		realm = config.DefaultRealm
	}
	if ref.Space == "" && (ref.Realm == "" || ref.Realm == realm) {
		return ref.Name
	}
	scope := []string{ref.Realm}
	for _, part := range []string{ref.Space, ref.Stack} {
		if part != "" {
			scope = append(scope, part)
		}
	}
	return ref.Name + "@" + strings.Join(scope, "/")
}
