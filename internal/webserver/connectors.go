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

package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"gopkg.in/yaml.v3"
)

// The connectors API (issue #1548, §7). Everything here iterates the connector
// catalog embedded in kuke: no route, payload or branch names a connector, so
// a connector added as one connector.yaml is served — listed, given
// identities, defaulted, picked — without a change to this file or to the UI.
//
// Secret material is write-only on every route: it travels in a request body,
// is never read back, and is masked out of any error.

const (
	// maxIdentityBody bounds an identity create: an SSH key with its
	// certificate and known_hosts fits with room to spare.
	maxIdentityBody = 256 << 10
	// maxConnectorBody bounds the small coordinate-only mutations.
	maxConnectorBody = 16 << 10
)

// connectorView is one connector of the catalog as the browser sees it: what
// the UI needs to render its identities, its default pickers and its create
// form. It is the connector.yaml, reshaped — nothing is added per connector.
type connectorView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// Delivery is the handler of a connector without methods (git, registry).
	Delivery string `json:"delivery,omitempty"`
	// Group is the mutual-exclusion group of the method components.
	Group string `json:"group,omitempty"`
	// Harness is the harness the connector authenticates, if any.
	Harness string `json:"harness,omitempty"`
	// Optional reports a connector a cell starts without.
	Optional    bool                     `json:"optional,omitempty"`
	Scopes      []string                 `json:"scopes"`
	SecretTypes []string                 `json:"secretTypes"`
	Fields      []v1beta1.ConnectorField `json:"fields"`
	Methods     []connectorMethodView    `json:"methods"`
}

// connectorMethodView is one way a connector reaches a cell.
type connectorMethodView struct {
	Name        string `json:"name"`
	Component   string `json:"component"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
	Delivery    string `json:"delivery"`
	// Slot is the secret slot the identity fills (env and file delivery).
	Slot      string `json:"slot,omitempty"`
	EnvName   string `json:"envName,omitempty"`
	MountPath string `json:"mountPath,omitempty"`
}

func viewConnector(conn *connector.Connector) connectorView {
	scopes := conn.Spec.Scopes
	if len(scopes) == 0 {
		scopes = intconnector.Scopes
	}
	view := connectorView{
		Name:        conn.Metadata.Name,
		Title:       conn.Spec.Title,
		Description: conn.Spec.Description,
		Harness:     conn.Spec.Harness,
		Optional:    conn.Spec.Optional,
		Scopes:      slices.Clone(scopes),
		SecretTypes: slices.Clone(conn.Spec.Identity.SecretTypes),
		Fields:      slices.Clone(conn.Spec.Identity.Fields),
		Methods:     []connectorMethodView{},
	}
	if view.Fields == nil {
		view.Fields = []v1beta1.ConnectorField{}
	}
	if len(conn.Spec.Methods) == 0 {
		view.Delivery = conn.Spec.Delivery
		return view
	}
	if len(conn.Spec.Methods) > 1 || conn.Spec.Group != "" {
		view.Group = conn.Group()
	}
	for _, method := range conn.Spec.Methods {
		m := connectorMethodView{
			Name:        method.Name,
			Component:   conn.ComponentName(method),
			Description: method.Description,
			Default:     method.Default,
			Delivery:    method.Delivery,
		}
		if s := method.Secret; s != nil {
			m.Slot, m.EnvName, m.MountPath = s.Name, s.EnvName, s.MountPath
		}
		view.Methods = append(view.Methods, m)
	}
	return view
}

// scopeRef is the Realm, Space and Stack a default or an identity lives at.
type scopeRef struct {
	Realm string `json:"realm"`
	Space string `json:"space,omitempty"`
	Stack string `json:"stack,omitempty"`
}

// level names the deepest coordinate the scope sets.
func (s scopeRef) level() string {
	switch {
	case s.Stack != "":
		return v1beta1.ConnectorScopeStack
	case s.Space != "":
		return v1beta1.ConnectorScopeSpace
	default:
		return v1beta1.ConnectorScopeRealm
	}
}

// checkScope validates browser-supplied scope coordinates before any of them
// reaches the daemon's metadata paths.
func checkScope(s scopeRef) (scopeRef, error) {
	realm := strings.TrimSpace(s.Realm)
	rawSpace, rawStack := strings.TrimSpace(s.Space), strings.TrimSpace(s.Stack)
	if realm == "" {
		return scopeRef{}, errdefs.ErrRealmNameRequired
	}
	if rawStack != "" && rawSpace == "" {
		return scopeRef{}, fmt.Errorf("%w: scope coordinates cannot contain gaps", errdefs.ErrConfig)
	}
	// Each coordinate is guarded inline (not through a loop, helper or struct
	// field) so the filepath.IsLocal check visibly dominates the returned
	// values for static path-injection analysis, matching configLookup. An
	// optional coordinate is copied only once checked, so its unset path
	// carries the empty literal.
	unsafe := func() (scopeRef, error) {
		return scopeRef{}, fmt.Errorf("%w: unsafe scope coordinate", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(realm) || !safePathSegment(realm) {
		return unsafe()
	}
	var space, stack string
	if rawSpace != "" {
		if !filepath.IsLocal(rawSpace) || !safePathSegment(rawSpace) {
			return unsafe()
		}
		space = rawSpace
	}
	if rawStack != "" {
		if !filepath.IsLocal(rawStack) || !safePathSegment(rawStack) {
			return unsafe()
		}
		stack = rawStack
	}
	return scopeRef{Realm: realm, Space: space, Stack: stack}, nil
}

// checkSecretRef validates a browser-supplied Secret reference.
func checkSecretRef(ref *v1beta1.ContainerSecretRef) (*v1beta1.ContainerSecretRef, error) {
	if ref == nil {
		return nil, nil //nolint:nilnil // no reference is a valid input: it clears a choice
	}
	name, realm := strings.TrimSpace(ref.Name), strings.TrimSpace(ref.Realm)
	rawSpace, rawStack := strings.TrimSpace(ref.Space), strings.TrimSpace(ref.Stack)
	if name == "" || realm == "" || (rawStack != "" && rawSpace == "") || ref.Cell != "" {
		return nil, fmt.Errorf("%w: name a secret by name and realm (space and stack optional)", errdefs.ErrConfig)
	}
	// Guarded inline, optional coordinates copied only once checked: see
	// checkScope.
	unsafe := func() (*v1beta1.ContainerSecretRef, error) {
		return nil, fmt.Errorf("%w: unsafe secret reference", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(name) || !safePathSegment(name) {
		return unsafe()
	}
	if !filepath.IsLocal(realm) || !safePathSegment(realm) {
		return unsafe()
	}
	var space, stack string
	if rawSpace != "" {
		if !filepath.IsLocal(rawSpace) || !safePathSegment(rawSpace) {
			return unsafe()
		}
		space = rawSpace
	}
	if rawStack != "" {
		if !filepath.IsLocal(rawStack) || !safePathSegment(rawStack) {
			return unsafe()
		}
		stack = rawStack
	}
	return &v1beta1.ContainerSecretRef{Name: name, Realm: realm, Space: space, Stack: stack}, nil
}

// decodeJSONBody decodes exactly one JSON document of at most limit bytes
// into out. Decoder errors can quote the offending input, so they are not
// forwarded: a malformed body may carry secret material.
func decodeJSONBody(r *http.Request, limit int64, out any) error {
	if r.Header.Get("Content-Type") != jsonContentType {
		return fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid request", errdefs.ErrConfig)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
	}
	return nil
}

// connectorAPI serves the connectors routes over one catalog.
type connectorAPI struct {
	catalog *connector.Catalog
}

func registerConnectorRoutes(mux *http.ServeMux, dial Dial, catalog *connector.Catalog) {
	api := connectorAPI{catalog: catalog}
	mux.HandleFunc("GET /api/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, api.list())
	})
	mux.HandleFunc("GET /api/connectors/status", resourceRoute(dial, api.status))
	mux.HandleFunc("GET /api/connectors/defaults", resourceRoute(dial, api.defaults))
	mux.HandleFunc("POST /api/connectors/{connector}/defaults", resourceRoute(dial, api.setDefault))
	mux.HandleFunc("POST /api/connectors/{connector}/identities", resourceRoute(dial, api.createIdentity))
	mux.HandleFunc("POST /api/configs/connector", resourceRoute(dial, api.pickForConfig))
}

func (a connectorAPI) list() []connectorView {
	out := make([]connectorView, 0, len(a.catalog.Connectors))
	for i := range a.catalog.Connectors {
		out = append(out, viewConnector(&a.catalog.Connectors[i]))
	}
	return out
}

// find resolves the {connector} path value against the catalog.
func (a connectorAPI) find(r *http.Request) (*connector.Connector, error) {
	name := r.PathValue("connector")
	conn, ok := a.catalog.Find(name)
	if !ok {
		return nil, fmt.Errorf("%w: unknown connector %q", errdefs.ErrConfig, name)
	}
	return conn, nil
}

// connectorUsage is how the stored Configs use one connector: how many
// require it, and which of them have nothing to fill it with.
type connectorUsage struct {
	Connector string `json:"connector"`
	// Configs counts the Configs that require the connector.
	Configs int `json:"configs"`
	// Missing lists the Configs a cell cannot be created from because this
	// connector has no usable identity.
	Missing []connectorMissing `json:"missing"`
}

// connectorMissing names one Config and why its connector does not resolve.
type connectorMissing struct {
	Config    v1beta1.CellConfigMetadata `json:"config"`
	Component string                     `json:"component"`
	Method    string                     `json:"method,omitempty"`
	Slot      string                     `json:"slot,omitempty"`
	Delivery  string                     `json:"delivery"`
	Message   string                     `json:"message,omitempty"`
}

// blocksCells reports whether a connector status keeps a cell from being
// created: a required connector nothing fills, or a named identity that
// cannot be used.
func blocksCells(status v1beta1.ConnectorStatus) bool {
	return status.State != v1beta1.ConnectorResolved && (status.Required || status.Identity != nil)
}

// status aggregates the connector status of every stored Config, per
// connector: the count behind the Connectors page's "needs a default" marker.
func (a connectorAPI) status(ctx context.Context, c kukeonv1.Client, _ *http.Request) (any, error) {
	configs, err := c.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	usage := map[string]*connectorUsage{}
	var order []string
	entry := func(name string) *connectorUsage {
		u, ok := usage[name]
		if !ok {
			u = &connectorUsage{Connector: name, Missing: []connectorMissing{}}
			usage[name] = u
			order = append(order, name)
		}
		return u
	}
	for _, name := range a.catalog.Names() {
		entry(name)
	}
	for _, cfg := range configs {
		if cfg.Status == nil {
			continue
		}
		for _, status := range cfg.Status.Connectors {
			u := entry(status.Connector)
			u.Configs++
			if !blocksCells(status) {
				continue
			}
			u.Missing = append(u.Missing, connectorMissing{
				Config: v1beta1.CellConfigMetadata{
					Name: cfg.Metadata.Name, Realm: cfg.Metadata.Realm,
					Space: cfg.Metadata.Space, Stack: cfg.Metadata.Stack,
				},
				Component: status.Component, Method: status.Method, Slot: status.Slot,
				Delivery: status.Delivery, Message: status.Message,
			})
		}
	}
	out := make([]connectorUsage, 0, len(order))
	for _, name := range order {
		out = append(out, *usage[name])
	}
	return out, nil
}

// connectorDefault is the default identity of one connector method across the
// levels of a scope: what each level sets, and what a Config in the scope
// that chooses nothing would get.
type connectorDefault struct {
	Connector string `json:"connector"`
	Method    string `json:"method,omitempty"`
	Component string `json:"component,omitempty"`
	Delivery  string `json:"delivery"`
	Slot      string `json:"slot,omitempty"`
	// Levels maps realm, space and stack to the identity that level sets;
	// a level that sets none, or that the request did not name, is absent.
	Levels map[string]v1beta1.ContainerSecretRef `json:"levels"`
	// Effective is the identity the deepest level sets; Origin is that level.
	Effective *v1beta1.ContainerSecretRef `json:"effective,omitempty"`
	Origin    string                      `json:"origin,omitempty"`
}

// defaultTarget is one place a default identity is stored: the secret slot of
// a method, or the git identity reference.
type defaultTarget struct {
	conn   *connector.Connector
	method connector.Method
	slot   string
	git    bool
}

func (t defaultTarget) descriptor() v1beta1.BlueprintConnector { return t.conn.Descriptor(t.method) }

// read returns the identity a defaults fill sets for the target.
func (t defaultTarget) read(d *v1beta1.ScopeDefaults) *v1beta1.ContainerSecretRef {
	if d == nil {
		return nil
	}
	if t.git {
		if d.Git == nil {
			return nil
		}
		return d.Git.IdentityRef
	}
	return d.Secrets[t.slot].SecretRef
}

// write sets (or, for a nil identity, clears) the target on a defaults fill.
func (t defaultTarget) write(d *v1beta1.ScopeDefaults, identity *v1beta1.ContainerSecretRef) {
	if t.git {
		var git v1beta1.ContainerGit
		if d.Git != nil {
			git = *d.Git
		}
		git.IdentityRef = identity
		d.Git = &git
		if identity == nil && git.Author == nil && git.Committer == nil && git.SigningKey == "" &&
			len(git.Sign) == 0 && git.AllowedSigners == "" && len(git.Credentials) == 0 && git.Signing == nil {
			d.Git = nil
		}
		return
	}
	secrets := make(map[string]v1beta1.CellConfigSecretFill, len(d.Secrets)+1)
	for slot, fill := range d.Secrets {
		secrets[slot] = fill
	}
	if identity == nil {
		delete(secrets, t.slot)
	} else {
		secrets[t.slot] = v1beta1.CellConfigSecretFill{SecretRef: identity}
	}
	d.Secrets = secrets
	if len(secrets) == 0 {
		d.Secrets = nil
	}
}

// targets lists every place the catalog's connectors keep a default: one per
// method delivered through a secret slot, one for a git-delivered connector.
// A login kept in the state volume has no identity, and a registry credential
// is bound per server on the Realm, not defaulted.
func (a connectorAPI) targets() []defaultTarget {
	var out []defaultTarget
	for i := range a.catalog.Connectors {
		conn := &a.catalog.Connectors[i]
		if len(conn.Spec.Methods) == 0 {
			if conn.Spec.Delivery == v1beta1.ConnectorDeliveryGit {
				out = append(out, defaultTarget{conn: conn, git: true})
			}
			continue
		}
		for _, method := range conn.Spec.Methods {
			if method.Secret != nil {
				out = append(out, defaultTarget{conn: conn, method: method, slot: method.Secret.Name})
			}
		}
	}
	return out
}

// target resolves the place a request's connector and method keep a default.
func (a connectorAPI) target(conn *connector.Connector, method string) (defaultTarget, error) {
	var candidates []defaultTarget
	for _, t := range a.targets() {
		if t.conn.Metadata.Name == conn.Metadata.Name {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return defaultTarget{}, fmt.Errorf(
			"%w: connector %q takes no default identity", errdefs.ErrConfig, conn.Metadata.Name)
	}
	method = strings.TrimSpace(method)
	for _, t := range candidates {
		if t.method.Name == method || (method == "" && len(candidates) == 1) {
			return t, nil
		}
	}
	names := make([]string, 0, len(candidates))
	for _, t := range candidates {
		names = append(names, t.method.Name)
	}
	return defaultTarget{}, fmt.Errorf(
		"%w: connector %q: name the method the default is for (one of: %s)",
		errdefs.ErrConfig, conn.Metadata.Name, strings.Join(names, ", "))
}

// scopeFills reads the defaults fill of each level of a scope. A level the
// scope does not name is absent.
func scopeFills(ctx context.Context, c kukeonv1.Client, scope scopeRef) (map[string]*v1beta1.ScopeDefaults, error) {
	fills := map[string]*v1beta1.ScopeDefaults{}
	realm, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: scope.Realm}})
	if err != nil {
		return nil, err
	}
	if !realm.MetadataExists {
		return nil, errdefs.ErrRealmNotFound
	}
	fills[v1beta1.ConnectorScopeRealm] = realm.Realm.Spec.Defaults
	if scope.Space == "" {
		return fills, nil
	}
	space, err := c.GetSpace(ctx, v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{Name: scope.Space},
		Spec:     v1beta1.SpaceSpec{RealmID: scope.Realm},
	})
	if err != nil {
		return nil, err
	}
	if !space.MetadataExists {
		return nil, errdefs.ErrSpaceNotFound
	}
	fills[v1beta1.ConnectorScopeSpace] = space.Space.Spec.Defaults.Fill()
	if scope.Stack == "" {
		return fills, nil
	}
	stack, err := c.GetStack(ctx, v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: scope.Stack},
		Spec:     v1beta1.StackSpec{RealmID: scope.Realm, SpaceID: scope.Space},
	})
	if err != nil {
		return nil, err
	}
	if !stack.MetadataExists {
		return nil, errdefs.ErrStackNotFound
	}
	fills[v1beta1.ConnectorScopeStack] = stack.Stack.Spec.Defaults
	return fills, nil
}

// defaults reports, for the scope named by ?realm[&space[&stack]], the
// default identity of every connector method at each level and the one that
// wins — what the pickers show as "Default: <identity> (<level>)".
func (a connectorAPI) defaults(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	query := r.URL.Query()
	scope, err := checkScope(scopeRef{
		Realm: query.Get("realm"), Space: query.Get("space"), Stack: query.Get("stack"),
	})
	if err != nil {
		return nil, err
	}
	fills, err := scopeFills(ctx, c, scope)
	if err != nil {
		return nil, err
	}
	out := []connectorDefault{}
	for _, t := range a.targets() {
		row := connectorDefault{
			Connector: t.conn.Metadata.Name,
			Method:    t.method.Name,
			Delivery:  t.conn.Delivery(t.method),
			Slot:      t.slot,
			Levels:    map[string]v1beta1.ContainerSecretRef{},
		}
		if !t.git {
			row.Component = t.conn.ComponentName(t.method)
		} else {
			row.Component = t.conn.Metadata.Name
		}
		for _, level := range intconnector.Scopes { // realm, space, stack: deeper wins
			ref := t.read(fills[level])
			if ref == nil {
				continue
			}
			row.Levels[level] = *ref
			effective := *ref
			row.Effective, row.Origin = &effective, level
		}
		out = append(out, row)
	}
	return out, nil
}

// defaultRequest sets or clears the default identity of a connector at one
// scope. A null identity clears it.
type defaultRequest struct {
	Scope    scopeRef                    `json:"scope"`
	Method   string                      `json:"method,omitempty"`
	Identity *v1beta1.ContainerSecretRef `json:"identity"`
}

type defaultResult struct {
	Connector string                      `json:"connector"`
	Method    string                      `json:"method,omitempty"`
	Scope     scopeRef                    `json:"scope"`
	Level     string                      `json:"level"`
	Identity  *v1beta1.ContainerSecretRef `json:"identity"`
	Action    string                      `json:"action"`
}

// setDefault writes a connector's default identity onto the Realm, Space or
// Stack: read the scope's document, change the one entry, re-apply it through
// the daemon's write-through apply path — the same read-modify-apply the
// registry credential route uses, extended to Space and Stack. Every other
// default the scope carries is kept. Configs that chose no identity pick the
// change up on their next render; none is rewritten.
func (a connectorAPI) setDefault(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	conn, err := a.find(r)
	if err != nil {
		return nil, err
	}
	var input defaultRequest
	if err = decodeJSONBody(r, maxConnectorBody, &input); err != nil {
		return nil, err
	}
	scope, err := checkScope(input.Scope)
	if err != nil {
		return nil, err
	}
	target, err := a.target(conn, input.Method)
	if err != nil {
		return nil, err
	}
	desc := target.descriptor()
	if !intconnector.AcceptsScope(desc, scope.level()) {
		return nil, fmt.Errorf(
			"%w: a default for connector %q is set at %s level, not %s",
			errdefs.ErrConfig, desc.Name, strings.Join(desc.Scopes, " or "), scope.level())
	}
	identity, err := checkSecretRef(input.Identity)
	if err != nil {
		return nil, err
	}
	if identity != nil {
		if err = checkIdentity(ctx, c, desc, *identity); err != nil {
			return nil, err
		}
	}
	action, err := editScopeDefaults(ctx, c, scope, func(d *v1beta1.ScopeDefaults) { target.write(d, identity) })
	if err != nil {
		return nil, err
	}
	return defaultResult{
		Connector: desc.Name, Method: target.method.Name, Scope: scope, Level: scope.level(),
		Identity: identity, Action: action,
	}, nil
}

// checkIdentity confirms a Secret exists and can be an identity of the
// connector. Only its readable part is looked at.
func checkIdentity(
	ctx context.Context, c kukeonv1.Client, desc v1beta1.BlueprintConnector, ref v1beta1.ContainerSecretRef,
) error {
	got, err := c.GetSecret(ctx, v1beta1.SecretDoc{Metadata: v1beta1.SecretMetadata{
		Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack,
	}})
	if err != nil {
		return err
	}
	if !got.MetadataExists {
		return fmt.Errorf("%w: secret %s", errdefs.ErrSecretNotFound, intconnector.FormatSecretRef(ref))
	}
	if checkErr := intconnector.CheckSecret(desc, got.Secret.Spec); checkErr != nil {
		return fmt.Errorf("%w: secret %s: %w", errdefs.ErrConfig, intconnector.FormatSecretRef(ref), checkErr)
	}
	return nil
}

// editScopeDefaults applies edit to the defaults fill of the Realm, Space or
// Stack the scope names and re-applies that one document. It returns the
// apply action (updated, unchanged).
func editScopeDefaults(
	ctx context.Context, c kukeonv1.Client, scope scopeRef, edit func(*v1beta1.ScopeDefaults),
) (string, error) {
	switch scope.level() {
	case v1beta1.ConnectorScopeStack:
		return editStackDefaults(ctx, c, scope, edit)
	case v1beta1.ConnectorScopeSpace:
		return editSpaceDefaults(ctx, c, scope, edit)
	default:
		return editRealmDefaults(ctx, c, scope, edit)
	}
}

// editedFill applies edit to a copy of a scope's defaults fill and returns it,
// or nil when the edit leaves nothing set.
func editedFill(current *v1beta1.ScopeDefaults, edit func(*v1beta1.ScopeDefaults)) *v1beta1.ScopeDefaults {
	fill := v1beta1.ScopeDefaults{}
	if current != nil {
		fill = *current
	}
	edit(&fill)
	if fill.IsZero() {
		return nil
	}
	return &fill
}

func editStackDefaults(
	ctx context.Context, c kukeonv1.Client, scope scopeRef, edit func(*v1beta1.ScopeDefaults),
) (string, error) {
	current, err := c.GetStack(ctx, v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: scope.Stack},
		Spec:     v1beta1.StackSpec{RealmID: scope.Realm, SpaceID: scope.Space},
	})
	if err != nil {
		return "", err
	}
	if !current.MetadataExists {
		return "", errdefs.ErrStackNotFound
	}
	desired := current.Stack
	desired.APIVersion, desired.Kind = v1beta1.APIVersionV1Beta1, v1beta1.KindStack
	desired.Status = v1beta1.StackStatus{}
	desired.Spec.RealmID, desired.Spec.SpaceID = scope.Realm, scope.Space
	if desired.Spec.ID == "" {
		desired.Spec.ID = desired.Metadata.Name
	}
	desired.Spec.Defaults = editedFill(desired.Spec.Defaults, edit)
	return applyDocument(ctx, c, desired, v1beta1.KindStack)
}

func editSpaceDefaults(
	ctx context.Context, c kukeonv1.Client, scope scopeRef, edit func(*v1beta1.ScopeDefaults),
) (string, error) {
	current, err := c.GetSpace(ctx, v1beta1.SpaceDoc{
		Metadata: v1beta1.SpaceMetadata{Name: scope.Space},
		Spec:     v1beta1.SpaceSpec{RealmID: scope.Realm},
	})
	if err != nil {
		return "", err
	}
	if !current.MetadataExists {
		return "", errdefs.ErrSpaceNotFound
	}
	desired := current.Space
	desired.APIVersion, desired.Kind = v1beta1.APIVersionV1Beta1, v1beta1.KindSpace
	desired.Status = v1beta1.SpaceStatus{}
	desired.Spec.RealmID = scope.Realm
	// A Space's defaults also carry container defaults; only the CellConfig
	// fill part is edited.
	defaults := v1beta1.SpaceDefaults{}
	if desired.Spec.Defaults != nil {
		defaults = *desired.Spec.Defaults
	}
	fill := v1beta1.ScopeDefaults{
		Values: defaults.Values, Repos: defaults.Repos, Secrets: defaults.Secrets, Git: defaults.Git,
	}
	edit(&fill)
	defaults.Values, defaults.Repos, defaults.Secrets, defaults.Git = fill.Values, fill.Repos, fill.Secrets, fill.Git
	desired.Spec.Defaults = nil
	if defaults.Container != nil || !fill.IsZero() {
		desired.Spec.Defaults = &defaults
	}
	return applyDocument(ctx, c, desired, v1beta1.KindSpace)
}

func editRealmDefaults(
	ctx context.Context, c kukeonv1.Client, scope scopeRef, edit func(*v1beta1.ScopeDefaults),
) (string, error) {
	current, err := c.GetRealm(ctx, v1beta1.RealmDoc{Metadata: v1beta1.RealmMetadata{Name: scope.Realm}})
	if err != nil {
		return "", err
	}
	if !current.MetadataExists {
		return "", errdefs.ErrRealmNotFound
	}
	// The apply diff replaces the Realm's compatible fields whole, so the live
	// document — registry credentials and images included — is carried forward.
	desired := current.Realm
	desired.APIVersion, desired.Kind = v1beta1.APIVersionV1Beta1, v1beta1.KindRealm
	desired.Status = v1beta1.RealmStatus{}
	desired.Spec.Defaults = editedFill(desired.Spec.Defaults, edit)
	return applyDocument(ctx, c, desired, v1beta1.KindRealm)
}

// applyDocument applies one document and returns its apply action.
func applyDocument(ctx context.Context, c kukeonv1.Client, doc any, kind v1beta1.Kind) (string, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", fmt.Errorf("marshal %s document: %w", kind, err)
	}
	result, err := c.ApplyDocuments(ctx, raw)
	if err != nil {
		return "", err
	}
	for _, resource := range result.Resources {
		if resource.Kind != string(kind) {
			continue
		}
		if resource.Action == "failed" {
			if resource.Error != "" {
				return "", fmt.Errorf("%w: apply %s %q: %s", errdefs.ErrConfig, kind, resource.Name, resource.Error)
			}
			return "", fmt.Errorf("apply %s failed", kind)
		}
		return resource.Action, nil
	}
	return "applied", nil
}

// identityRequest creates an identity of a connector — a tagged Secret — or,
// when a Secret of that name already exists in the scope, replaces its value
// (rotation). The material fields are write-only.
type identityRequest struct {
	Scope scopeRef `json:"scope"`
	Name  string   `json:"name"`
	// Type is the Secret type; empty selects the connector's when it accepts
	// exactly one.
	Type string `json:"type,omitempty"`
	// Identity are the readable identity fields the connector declares.
	Identity map[string]string `json:"identity,omitempty"`

	// Value is the bytes of an opaque secret.
	Value string `json:"value,omitempty"`
	// Username and Password are a basic-auth secret; Password carries the
	// password or a token.
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// PrivateKey and the fields after it are an ssh-key secret.
	PrivateKey  string `json:"privateKey,omitempty"`
	Passphrase  string `json:"passphrase,omitempty"`
	PublicKey   string `json:"publicKey,omitempty"`
	Certificate string `json:"certificate,omitempty"`
	KnownHosts  string `json:"knownHosts,omitempty"`
}

// material lists the request's secret material, for masking errors.
func (r identityRequest) material() []string {
	out := []string{r.Value, r.Password, r.PrivateKey, r.Passphrase}
	// A multi-line key is also masked line by line: an error may quote a
	// single line of it.
	for _, line := range strings.Split(r.PrivateKey, "\n") {
		if line = strings.TrimSpace(line); len(line) >= 16 && !strings.HasPrefix(line, "-----") {
			out = append(out, line)
		}
	}
	return out
}

type identityResult struct {
	Name      string            `json:"name"`
	Scope     scopeRef          `json:"scope"`
	Connector string            `json:"connector"`
	Type      string            `json:"type"`
	Identity  map[string]string `json:"identity,omitempty"`
	// Action is created, or updated when an existing identity was rotated.
	Action string `json:"action"`
}

// createIdentity stores a Secret tagged as an identity of the connector,
// after checking it against the connector's definition exactly as
// `kuke create secret --connector` does: an accepted type, declared fields,
// required fields set, values readable as their declared types.
func (a connectorAPI) createIdentity(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	conn, err := a.find(r)
	if err != nil {
		return nil, err
	}
	var input identityRequest
	if err = decodeJSONBody(r, maxIdentityBody, &input); err != nil {
		return nil, err
	}
	doc, err := a.identitySecret(conn, input)
	if err != nil {
		return nil, redactCreationError(err, input.material())
	}
	created, err := c.CreateSecret(ctx, doc)
	if err != nil {
		return nil, redactCreationError(err, input.material())
	}
	action := "updated"
	if created.Created {
		action = "created"
	}
	secretType := doc.Spec.Type
	if secretType == "" {
		secretType = v1beta1.SecretTypeOpaque
	}
	return identityResult{
		Name:      doc.Metadata.Name,
		Scope:     scopeRef{Realm: doc.Metadata.Realm, Space: doc.Metadata.Space, Stack: doc.Metadata.Stack},
		Connector: doc.Spec.Connector,
		Type:      secretType,
		Identity:  doc.Spec.Identity,
		Action:    action,
	}, nil
}

// identityMaterial reads the material of one secret type out of a request. A
// rejection names what is missing and never the material itself.
func identityMaterial(input identityRequest, secretType string) (v1beta1.SecretSpec, error) {
	var spec v1beta1.SecretSpec
	switch secretType {
	case v1beta1.SecretTypeOpaque:
		value := strings.TrimRight(input.Value, "\r\n")
		if value == "" {
			return spec, fmt.Errorf("%w: the secret value is required", errdefs.ErrConfig)
		}
		spec.Data = value
	case v1beta1.SecretTypeBasicAuth:
		username := strings.TrimSpace(input.Username)
		password := strings.TrimRight(input.Password, "\r\n")
		if username == "" || password == "" {
			return spec, fmt.Errorf("%w: a username and a password or token are required", errdefs.ErrConfig)
		}
		spec.Type = secretType
		spec.BasicAuth = &v1beta1.SecretBasicAuth{Username: username, Password: password}
	case v1beta1.SecretTypeSSHKey:
		if strings.TrimSpace(input.PrivateKey) == "" {
			return spec, fmt.Errorf("%w: the private key is required", errdefs.ErrConfig)
		}
		key := input.PrivateKey
		if !strings.HasSuffix(key, "\n") {
			key += "\n"
		}
		spec.Type = secretType
		spec.SSHKey = &v1beta1.SecretSSHKey{
			PrivateKey:  key,
			Passphrase:  strings.TrimRight(input.Passphrase, "\r\n"),
			PublicKey:   strings.TrimSpace(input.PublicKey),
			Certificate: strings.TrimSpace(input.Certificate),
			KnownHosts:  input.KnownHosts,
		}
	default:
		return spec, fmt.Errorf("%w: unknown secret type %q", errdefs.ErrConfig, secretType)
	}
	return spec, nil
}

// identitySecret builds the tagged Secret document of an identity request.
func (a connectorAPI) identitySecret(conn *connector.Connector, input identityRequest) (v1beta1.SecretDoc, error) {
	var empty v1beta1.SecretDoc
	scope, err := checkScope(input.Scope)
	if err != nil {
		return empty, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return empty, errdefs.ErrSecretNameRequired
	}
	// Guarded inline so the check dominates the name for static
	// path-injection analysis: see checkScope.
	if !filepath.IsLocal(name) || !safePathSegment(name) || strings.HasPrefix(name, ".") {
		return empty, fmt.Errorf("%w: unsafe secret name", errdefs.ErrConfig)
	}
	method, _ := conn.Method("")
	desc := conn.Descriptor(method)
	if !intconnector.AcceptsScope(desc, scope.level()) {
		return empty, fmt.Errorf(
			"%w: an identity of connector %q is stored at %s level, not %s",
			errdefs.ErrConfig, desc.Name, strings.Join(desc.Scopes, " or "), scope.level())
	}
	secretType := strings.TrimSpace(input.Type)
	if secretType == "" {
		if len(desc.SecretTypes) != 1 {
			return empty, fmt.Errorf(
				"%w: connector %q: choose a secret type (one of: %s)",
				errdefs.ErrConfig, desc.Name, strings.Join(desc.SecretTypes, ", "))
		}
		secretType = desc.SecretTypes[0]
	}
	spec, err := identityMaterial(input, secretType)
	if err != nil {
		return empty, err
	}
	spec.Connector = desc.Name
	identity := map[string]string{}
	for field, value := range input.Identity {
		if value = strings.TrimSpace(value); value != "" {
			identity[strings.TrimSpace(field)] = value
		}
	}
	if len(identity) > 0 {
		spec.Identity = identity
	}
	if checkErr := intconnector.CheckSecret(desc, spec); checkErr != nil {
		return empty, fmt.Errorf("%w: %w", errdefs.ErrConfig, checkErr)
	}
	return v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata:   v1beta1.SecretMetadata{Name: name, Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack},
		Spec:       spec,
	}, nil
}

// configConnectorRequest picks — or, with a null identity, un-picks — the
// identity one Config uses for a connector it requires.
type configConnectorRequest struct {
	Config    v1beta1.CellConfigMetadata  `json:"config"`
	Connector string                      `json:"connector"`
	Identity  *v1beta1.ContainerSecretRef `json:"identity"`
}

// pickForConfig saves a Config fill for one connector ("Pick for this
// Config"): the identity goes into the Config's own secret fill — or its git
// block, for the git connector — and wins over every scope default. Clearing
// it returns the Config to the defaults. A Config a team owns is refused:
// `kuke team add` re-renders it from the team definition, so the choice
// belongs there (or in a scope default).
func (a connectorAPI) pickForConfig(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	var input configConnectorRequest
	if err := decodeJSONBody(r, maxConnectorBody, &input); err != nil {
		return nil, err
	}
	md, err := configLookup(input.Config)
	if err != nil {
		return nil, err
	}
	identity, err := checkSecretRef(input.Identity)
	if err != nil {
		return nil, err
	}
	current, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: md})
	if err != nil {
		return nil, err
	}
	if !current.MetadataExists {
		return nil, errdefs.ErrConfigNotFound
	}
	cfg := current.Config
	if team := cfg.Metadata.Labels[v1beta1.LabelTeam]; team != "" {
		return nil, fmt.Errorf(
			"%w: config %q is set by team %q; choose the identity in the team definition or set a scope default",
			errdefs.ErrConfig, cfg.Metadata.Name, team)
	}
	var status *v1beta1.ConnectorStatus
	if cfg.Status != nil {
		for i := range cfg.Status.Connectors {
			if cfg.Status.Connectors[i].Connector == input.Connector {
				status = &cfg.Status.Connectors[i]
			}
		}
	}
	if status == nil {
		return nil, fmt.Errorf("%w: config %q requires no connector %q",
			errdefs.ErrConfig, cfg.Metadata.Name, input.Connector)
	}
	if identity != nil {
		// The daemon's own status names the connector's accepted types only
		// through the blueprint; the catalog entry, when kuke has one, lets
		// a wrong pick fail here with the same message the CLI gives.
		if conn, known := a.catalog.Find(status.Connector); known {
			method, _ := conn.Method(status.Method)
			if err = checkIdentity(ctx, c, conn.Descriptor(method), *identity); err != nil {
				return nil, err
			}
		}
	}
	if err = writeConfigIdentity(&cfg, *status, identity); err != nil {
		return nil, err
	}
	cfg.APIVersion, cfg.Kind = v1beta1.APIVersionV1Beta1, v1beta1.KindCellConfig
	cfg.Status = nil
	if _, err = applyDocument(ctx, c, cfg, v1beta1.KindCellConfig); err != nil {
		return nil, err
	}
	updated, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: md})
	if err != nil {
		return nil, err
	}
	if !updated.MetadataExists {
		return nil, errdefs.ErrConfigNotFound
	}
	return updated.Config, nil
}

// writeConfigIdentity sets (or, for a nil identity, clears) the identity a
// Config picks for one connector, where the connector's delivery keeps it: the
// git block or the secret fill of its slot.
func writeConfigIdentity(
	cfg *v1beta1.CellConfigDoc, status v1beta1.ConnectorStatus, identity *v1beta1.ContainerSecretRef,
) error {
	switch status.Delivery {
	case v1beta1.ConnectorDeliveryGit:
		var git v1beta1.ContainerGit
		if cfg.Spec.Git != nil {
			git = *cfg.Spec.Git
		}
		git.IdentityRef = identity
		cfg.Spec.Git = &git
		if identity == nil && git.Author == nil && git.Committer == nil && git.SigningKey == "" &&
			len(git.Sign) == 0 && git.AllowedSigners == "" && len(git.Credentials) == 0 && git.Signing == nil {
			cfg.Spec.Git = nil
		}
	case v1beta1.ConnectorDeliveryEnv, v1beta1.ConnectorDeliveryFile:
		secrets := make(map[string]v1beta1.CellConfigSecretFill, len(cfg.Spec.Secrets)+1)
		for slot, fill := range cfg.Spec.Secrets {
			secrets[slot] = fill
		}
		if identity == nil {
			delete(secrets, status.Slot)
		} else {
			secrets[status.Slot] = v1beta1.CellConfigSecretFill{SecretRef: identity}
		}
		cfg.Spec.Secrets = secrets
		if len(secrets) == 0 {
			cfg.Spec.Secrets = nil
		}
	default:
		return fmt.Errorf("%w: connector %q uses its %q method, which takes no identity",
			errdefs.ErrConfig, status.Connector, status.Method)
	}
	return nil
}

// connectorOverrides validates the per-cell identity picks of a from-config
// create: connector names are passed through — the daemon checks them against
// the Config's blueprint — and every Secret reference is path-safe.
func connectorOverrides(
	in map[string]v1beta1.ContainerSecretRef,
) (map[string]v1beta1.ContainerSecretRef, error) {
	if len(in) == 0 {
		return nil, nil //nolint:nilnil // no override is a valid input, not an error
	}
	names := make([]string, 0, len(in))
	for name := range in {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make(map[string]v1beta1.ContainerSecretRef, len(in))
	for _, name := range names {
		if !intconnector.ValidName(name) {
			return nil, fmt.Errorf("%w: invalid connector name", errdefs.ErrConfig)
		}
		picked := in[name]
		ref, err := checkSecretRef(&picked)
		if err != nil {
			return nil, err
		}
		out[name] = *ref
	}
	return out, nil
}

// readableSecret is the part of a Secret a browser may see: its type, the
// connector it is an identity of and its readable identity fields. The
// material is never part of it.
func readableSecret(spec v1beta1.SecretSpec) v1beta1.SecretSpec {
	return v1beta1.SecretSpec{Type: spec.Type, Connector: spec.Connector, Identity: spec.Identity}
}

// connectorErrorStatus classifies the connector failures of a render so the
// UI can tell "this Config needs an identity" from a malformed request.
func connectorErrorStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errdefs.ErrConnectorUnresolved):
		return http.StatusConflict, "connector_unresolved", true
	case errors.Is(err, errdefs.ErrConnectorIdentity),
		errors.Is(err, errdefs.ErrConnectorUnknown),
		errors.Is(err, errdefs.ErrConnectorInvalid):
		return http.StatusBadRequest, "connector_invalid", true
	}
	return 0, "", false
}
