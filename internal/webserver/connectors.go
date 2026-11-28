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
	"github.com/eminwux/kukeon/internal/connector/githubtoken"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// The connectors API (issue #1548, §7). Everything here iterates the connector
// catalog embedded in kuke: no route, payload or branch names a connector, so
// a connector added as one connector.yaml is served — listed, given
// identities, defaulted, picked — without a change to this file or to the UI.
// The exceptions are the git connector's import from the host (issue #1591,
// connector_import.go): the git delivery handler is code, and so is
// discovering its identity; and the GitHub token for git (issue #1693,
// internal/connector/githubtoken), which turns one github identity request
// into a second, git identity.
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
	// TokenForGit is set on a connector whose token can also be stored as a
	// git identity from the same request (issue #1693): the github connector.
	TokenForGit *tokenForGitView `json:"tokenForGit,omitempty"`
}

// tokenForGitView is what the identity form needs to offer "Also use this
// token for git": the defaults the server applies to the git identity.
type tokenForGitView struct {
	// Connector is the git-delivered connector the second identity is of.
	Connector string   `json:"connector"`
	Hosts     []string `json:"hosts"`
	Username  string   `json:"username"`
	Name      string   `json:"name"`
	// TokenName is the github identity's default name.
	TokenName string `json:"tokenName"`
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
	// Login reports a method whose identities are login identities —
	// Volumes — that the browser can log in to (issue #1685).
	Login bool `json:"login,omitempty"`
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
	if conn.Metadata.Name == intconnector.GitHubIdentityTag {
		view.TokenForGit = &tokenForGitView{
			Connector: intconnector.GitIdentityTag,
			Hosts:     []string{intconnector.GitHubTokenHost}, Username: intconnector.GitHubTokenUsername,
			Name: githubtoken.DefaultGitName, TokenName: githubtoken.DefaultName,
		}
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
		m.Login = method.Delivery == v1beta1.ConnectorDeliveryStateVolume &&
			method.Login != nil && len(method.Login.Command) > 0
		view.Methods = append(view.Methods, m)
	}
	return view
}

// scopeRef is the Realm, Space and Stack a default or an identity lives at.
type scopeRef = scopedefaults.Scope

// checkScope validates browser-supplied scope coordinates before any of them
// reaches the daemon's metadata paths.
func checkScope(s scopeRef) (scopeRef, error) { return scopedefaults.CheckScope(s) }

// checkSecretRef validates a browser-supplied Secret reference.
func checkSecretRef(ref *v1beta1.ContainerSecretRef) (*v1beta1.ContainerSecretRef, error) {
	return scopedefaults.CheckSecretRef(ref)
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
	mux.HandleFunc(gitImportPath, resourceRoute(dial, api.importGit))
	mux.HandleFunc("POST /api/configs/connector", resourceRoute(dial, api.pickForConfig))
	registerLoginIdentityRoutes(mux, dial, api)
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
	return status.State != v1beta1.ConnectorResolved &&
		(status.Required || status.Identity != nil || status.Delivery == v1beta1.ConnectorDeliveryStateVolume)
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
type connectorDefault = scopedefaults.Default

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
	return scopedefaults.Defaults(ctx, c, a.catalog, scope)
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
	set, err := scopedefaults.Set(ctx, c, a.catalog, conn, scope, input.Method, input.Identity)
	if err != nil {
		return nil, err
	}
	return defaultResult{
		Connector: set.Connector, Method: set.Method, Scope: set.Scope, Level: set.Level,
		Identity: set.Identity, Action: set.Action,
	}, nil
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

	// MakeDefault also makes the stored identity the connector's default at
	// the identity's scope; Method names the method it is the default of, for
	// a connector with several.
	MakeDefault bool   `json:"makeDefault,omitempty"`
	Method      string `json:"method,omitempty"`
	// Git, on a connector that offers it (tokenForGit), also stores the token
	// as a git identity limited to its hosts (issue #1693). The token is the
	// request's value: it is sent once, and both Secrets are built from it.
	Git *githubtoken.Git `json:"git,omitempty"`
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
	// Git is the git identity stored from the same token (issue #1693).
	Git *identityResult `json:"git,omitempty"`
	// Defaults lists the defaults makeDefault set.
	Defaults []githubtoken.DefaultSet `json:"defaults,omitempty"`
	// StaleGit, on a github identity stored without git, is the git identity
	// linked to it: it was not rotated and keeps the token it was stored with
	// (issue #1707).
	StaleGit *v1beta1.ContainerSecretRef `json:"staleGit,omitempty"`
}

func storedResult(s githubtoken.Stored) identityResult {
	return identityResult{
		Name: s.Name, Scope: s.Scope, Connector: s.Connector, Type: s.Type, Identity: s.Identity, Action: s.Action,
	}
}

// createIdentity stores a Secret tagged as an identity of the connector,
// after checking it against the connector's definition exactly as
// `kuke create secret --connector` does: an accepted type, declared fields,
// required fields set, values readable as their declared types. With
// makeDefault the identity then becomes the connector's default at its own
// scope — the git import's create-then-default — and a failure there says
// the identity was stored.
func (a connectorAPI) createIdentity(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	conn, err := a.find(r)
	if err != nil {
		return nil, err
	}
	var input identityRequest
	if err = decodeJSONBody(r, maxIdentityBody, &input); err != nil {
		return nil, err
	}
	if input.Git != nil {
		result, tokenErr := a.createTokenForGit(ctx, c, conn, input)
		return result, redactCreationError(tokenErr, input.material())
	}
	doc, err := a.identitySecret(conn, input)
	if err != nil {
		return nil, redactCreationError(err, input.material())
	}
	var defaultTarget scopedefaults.Target
	if input.MakeDefault {
		// The default is checked before anything is stored: a connector or a
		// method that takes no default fails the request whole.
		if defaultTarget, err = scopedefaults.FindTarget(a.catalog, conn, input.Method); err != nil {
			return nil, err
		}
	}
	created, err := c.CreateSecret(ctx, doc)
	if err != nil {
		return nil, redactCreationError(err, input.material())
	}
	secretType := doc.Spec.Type
	if secretType == "" {
		secretType = v1beta1.SecretTypeOpaque
	}
	stored := githubtoken.Stored{
		Name: doc.Metadata.Name, Scope: scopeRef{Realm: doc.Metadata.Realm, Space: doc.Metadata.Space, Stack: doc.Metadata.Stack},
		Connector: doc.Spec.Connector, Type: secretType, Identity: doc.Spec.Identity,
		Action: secretWriteAction(created.Created),
	}
	result := storedResult(stored)
	result.StaleGit = staleGit(ctx, c, conn, doc, stored.Scope)
	if !input.MakeDefault {
		return result, nil
	}
	ref := githubtoken.Ref(doc)
	set, err := scopedefaults.Set(ctx, c, a.catalog, conn, stored.Scope, defaultTarget.Method.Name, &ref)
	if err != nil {
		return nil, redactCreationError(&githubtoken.PartialError{
			Result: githubtoken.Result{Secrets: []githubtoken.Stored{stored}},
			Missing: []string{
				fmt.Sprintf("%s default at %s %s", conn.Metadata.Name, stored.Scope.Level(), scopeText(stored.Scope)),
			},
			Err: err,
		}, input.material())
	}
	result.Defaults = []githubtoken.DefaultSet{{
		Connector: set.Connector, Method: set.Method, Scope: set.Scope, Level: set.Level,
		Identity: set.Identity, Previous: set.Previous, Action: set.Action,
	}}
	return result, nil
}

// staleGit names the git identity linked to a github identity stored without
// git (issue #1707): storing the github identity did not rotate it. The
// lookup only adds a warning to the result, so a failed one is dropped: the
// identity is stored either way.
func staleGit(
	ctx context.Context, c kukeonv1.Client, conn *connector.Connector, doc v1beta1.SecretDoc, scope scopeRef,
) *v1beta1.ContainerSecretRef {
	if conn.Metadata.Name != intconnector.GitHubIdentityTag {
		return nil
	}
	existing, err := githubtoken.Existing(ctx, c, scope)
	if err != nil {
		return nil
	}
	return githubtoken.LinkedGit(existing, doc)
}

// scopeText renders a scope as realm[/space[/stack]].
func scopeText(s scopeRef) string {
	return strings.Join(
		slices.DeleteFunc([]string{s.Realm, s.Space, s.Stack}, func(v string) bool { return v == "" }),
		"/",
	)
}

// createTokenForGit stores a GitHub token as its github identity and, from
// the same request, as a git identity limited to its hosts (issue #1693).
// Both Secrets are built and checked before either is written; the token is
// never read back, and no error carries it.
func (a connectorAPI) createTokenForGit(
	ctx context.Context, c kukeonv1.Client, conn *connector.Connector, input identityRequest,
) (any, error) {
	if conn.Metadata.Name != intconnector.GitHubIdentityTag {
		return nil, fmt.Errorf("%w: a %s identity cannot also be stored for git", errdefs.ErrConfig, conn.Metadata.Name)
	}
	if secretType := strings.TrimSpace(input.Type); secretType != "" && secretType != v1beta1.SecretTypeOpaque {
		return nil, fmt.Errorf("%w: a token for git is an opaque secret, not %s", errdefs.ErrConfig, secretType)
	}
	if len(input.Identity) > 0 || input.Username != "" || input.Password != "" || input.PrivateKey != "" {
		return nil, fmt.Errorf("%w: a token for git takes only a name, a value and its git options", errdefs.ErrConfig)
	}
	scope, err := checkScope(input.Scope)
	if err != nil {
		return nil, err
	}
	existing, err := githubtoken.Existing(ctx, c, scope)
	if err != nil {
		return nil, err
	}
	plan, err := githubtoken.Build(a.catalog, githubtoken.Options{
		Scope: scope, Name: input.Name, Token: input.Value, Git: input.Git,
	}, existing)
	if err != nil {
		return nil, tokenError(err)
	}
	stored, err := githubtoken.Store(ctx, c, a.catalog, plan, input.MakeDefault)
	if err != nil {
		return nil, err
	}
	result := storedResult(stored.Secrets[0])
	if len(stored.Secrets) > 1 {
		git := storedResult(stored.Secrets[1])
		result.Git = &git
	}
	result.Defaults = stored.Defaults
	return result, nil
}

// tokenError classifies a rejected token request as a bad request.
func tokenError(err error) error {
	if errors.Is(err, errdefs.ErrGitHubToken) {
		return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	return err
}

// identityMaterial reads the material of one secret type out of a request. A
// rejection names what is missing and never the material itself. An
// author-only git identity (issue #1591) takes no value: it stores the fixed
// placeholder.
func identityMaterial(input identityRequest, secretType string, authorOnly bool) (v1beta1.SecretSpec, error) {
	var spec v1beta1.SecretSpec
	switch secretType {
	case v1beta1.SecretTypeOpaque:
		if authorOnly {
			if input.Value != "" {
				return spec, fmt.Errorf(
					"%w: an author-only git identity stores no value; use type ssh-key for a key", errdefs.ErrConfig)
			}
			spec.Data = intconnector.GitAuthorOnlyValue
			break
		}
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
	if !intconnector.AcceptsScope(desc, scope.Level()) {
		return empty, fmt.Errorf(
			"%w: an identity of connector %q is stored at %s level, not %s",
			errdefs.ErrConfig, desc.Name, strings.Join(desc.Scopes, " or "), scope.Level())
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
	authorOnly := desc.Name == intconnector.GitIdentityTag && secretType == v1beta1.SecretTypeOpaque
	spec, err := identityMaterial(input, secretType, authorOnly)
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
	if checkErr := intconnector.CheckGitAuthorOnly(spec); checkErr != nil {
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
// `kuke create team` re-renders it from the team definition, so the choice
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
			if status.Delivery == v1beta1.ConnectorDeliveryStateVolume {
				err = checkLoginIdentity(ctx, c, conn.Metadata.Name, *identity)
			} else {
				err = scopedefaults.CheckIdentity(ctx, c, conn.Descriptor(method), *identity)
			}
			if err != nil {
				return nil, err
			}
		}
	}
	if err = writeConfigIdentity(&cfg, *status, identity); err != nil {
		return nil, err
	}
	cfg.APIVersion, cfg.Kind = v1beta1.APIVersionV1Beta1, v1beta1.KindCellConfig
	cfg.Status = nil
	_, warnings, err := scopedefaults.ApplyDocumentWarnings(ctx, c, cfg, v1beta1.KindCellConfig)
	if err != nil {
		return nil, err
	}
	updated, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: md})
	if err != nil {
		return nil, err
	}
	if !updated.MetadataExists {
		return nil, errdefs.ErrConfigNotFound
	}
	return savedConfig{CellConfigDoc: updated.Config, Warnings: warnings}, nil
}

// checkLoginIdentity checks that a Volume picked as the login identity of a
// Config exists and is not another connector's. The daemon checks the same
// when it renders a cell, and warns when another role uses the Volume.
func checkLoginIdentity(
	ctx context.Context, c kukeonv1.Client, connectorName string, ref v1beta1.ContainerSecretRef,
) error {
	if ref.Cell != "" {
		return fmt.Errorf("%w: a login identity is a Volume, never cell-scoped", errdefs.ErrVolumeIdentity)
	}
	got, err := c.GetVolume(ctx, v1beta1.VolumeDoc{Metadata: v1beta1.VolumeMetadata{
		Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack,
	}})
	if err != nil && !errors.Is(err, errdefs.ErrVolumeNotFound) {
		return err
	}
	if err != nil || !got.MetadataExists {
		return fmt.Errorf("%w: volume %q does not exist", errdefs.ErrVolumeIdentity, ref.Name)
	}
	if tag := got.Volume.Spec.Connector; tag != "" && tag != connectorName {
		return fmt.Errorf("%w: volume %q is a login identity of connector %q, not %q",
			errdefs.ErrVolumeIdentity, ref.Name, tag, connectorName)
	}
	return nil
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
	case v1beta1.ConnectorDeliveryStateVolume:
		// The identity of a login method is a Volume: the Config names it as
		// its state volume (issue #1685). There is no default to return to,
		// so clearing it is refused.
		if identity == nil {
			return fmt.Errorf("%w: connector %q keeps its login in the state volume; pick another login identity "+
				"instead of clearing it", errdefs.ErrConfig, status.Connector)
		}
		values := make(map[string]string, len(cfg.Spec.Values)+4) //nolint:mnd // the four state-volume values
		for key, value := range cfg.Spec.Values {
			values[key] = value
		}
		values["STATE_VOLUME"] = identity.Name
		values["STATE_VOLUME_REALM"] = identity.Realm
		values["STATE_VOLUME_SPACE"] = identity.Space
		values["STATE_VOLUME_STACK"] = identity.Stack
		cfg.Spec.Values = values
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

// connectorMethods validates the per-cell method picks of a from-config
// create (#1551): connector and method names are passed through — the daemon
// checks them against the Config's blueprint — once they are well formed.
func connectorMethods(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil //nolint:nilnil // no pick is a valid input, not an error
	}
	out := make(map[string]string, len(in))
	for name, method := range in {
		if !intconnector.ValidName(name) {
			return nil, fmt.Errorf("%w: invalid connector name", errdefs.ErrConfig)
		}
		if !intconnector.ValidName(method) {
			return nil, fmt.Errorf("%w: invalid method name for connector %q", errdefs.ErrConfig, name)
		}
		out[name] = method
	}
	return out, nil
}

// connectorErrorStatus classifies the connector failures of a render so the
// UI can tell "this Config needs an identity" from a malformed request.
func connectorErrorStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errdefs.ErrConnectorUnresolved):
		return http.StatusConflict, "connector_unresolved", true
	case errors.Is(err, errdefs.ErrConnectorIdentity),
		errors.Is(err, errdefs.ErrVolumeIdentity),
		errors.Is(err, errdefs.ErrConnectorUnknown),
		errors.Is(err, errdefs.ErrConnectorMethod),
		errors.Is(err, errdefs.ErrConnectorInvalid):
		return http.StatusBadRequest, "connector_invalid", true
	}
	return 0, "", false
}
