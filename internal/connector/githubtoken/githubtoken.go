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

// Package githubtoken stores a GitHub token as one identity or, when git is
// asked for, as two (issue #1693). The token is sent once and every Secret is
// built from that one request: nothing stored is ever read back.
//
//  1. A github identity: an opaque Secret tagged github, delivered as GH_TOKEN.
//  2. With git: a git identity, a basic-auth Secret tagged git whose username
//     is x-access-token and whose token is the same token, limited to its
//     hosts (github.com by default), with the identity key
//     kukeon.io/token-from naming the github identity it was copied from.
//
// The second identity is explicit and host-limited, never a fallback: git
// still uses only a git identity bound to its host (#1472). Storing the github
// identity again with git on rotates the linked git identity, found by its
// kukeon.io/token-from key. Storing it with git off leaves the linked git
// identity on the token it was stored with, and the plan and result name it
// (issue #1707) so the caller can warn: git fails in cells once that token is
// revoked.
//
// `kuke connector import github` and the web UI's GitHub identity form both use
// it, so they check, store and default the same way.
package githubtoken

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// Default identity names.
const (
	// DefaultName is the github identity's name when none is given.
	DefaultName = intconnector.GitHubTokenName
	// DefaultGitName is the git identity's name when none is given and no git
	// identity is linked to the github identity yet.
	DefaultGitName = intconnector.GitHubGitName
)

// Client is the part of the daemon client storing a token needs.
type Client interface {
	scopedefaults.Client
	ListSecrets(ctx context.Context, realm, space, stack, cell string) ([]v1beta1.SecretDoc, error)
	CreateSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error)
}

var _ Client = kukeonv1.Client(nil)

// Git asks for the git identity. Every field is optional.
type Git struct {
	// Name is the git identity's name. Empty: the linked identity's, else
	// github-git.
	Name string `json:"name,omitempty"`
	// Hosts are the hosts or URL prefixes git uses the token for. Empty: the
	// linked identity's, else github.com.
	Hosts []string `json:"hosts,omitempty"`
	// Author is the commit author, `Name <email>`. Empty: the linked
	// identity's, else none.
	Author string `json:"author,omitempty"`
	// Username is the basic-auth username. Empty: x-access-token.
	Username string `json:"username,omitempty"`
}

// Options is one request: the token, where it is stored, and whether git
// uses it too.
type Options struct {
	Scope scopedefaults.Scope
	// Name is the github identity's name. Empty: github-token.
	Name  string
	Token string
	// Git, when set, also stores the git identity.
	Git *Git
}

// Plan is the Secrets a request stores, checked against their connectors.
type Plan struct {
	Scope scopedefaults.Scope
	Token v1beta1.SecretDoc
	// Git is the git identity; nil when git was not asked for.
	Git *v1beta1.SecretDoc
	// Linked reports that Git is a git identity already linked to the github
	// identity: storing the plan rotates it.
	Linked bool
	// StaleGit, when git was not asked for, is the git identity linked to the
	// github identity: storing the plan does not rotate it, so it keeps the
	// token it was stored with (issue #1707).
	StaleGit *v1beta1.ContainerSecretRef
	token    string
}

// Material lists the token, for masking errors that might quote it.
func (p Plan) Material() []string { return []string{p.token} }

// Ref names one of the plan's Secrets.
func Ref(doc v1beta1.SecretDoc) v1beta1.ContainerSecretRef {
	return v1beta1.ContainerSecretRef{
		Name: doc.Metadata.Name, Realm: doc.Metadata.Realm, Space: doc.Metadata.Space, Stack: doc.Metadata.Stack,
	}
}

// Existing lists the Secrets stored at exactly the scope: the place a linked
// git identity, and a name the git identity would take, are looked for. Only
// their readable part is ever returned by the daemon.
func Existing(ctx context.Context, c Client, scope scopedefaults.Scope) ([]v1beta1.SecretDoc, error) {
	docs, err := c.ListSecrets(ctx, scope.Realm, scope.Space, scope.Stack, "")
	if err != nil {
		return nil, err
	}
	out := make([]v1beta1.SecretDoc, 0, len(docs))
	for _, doc := range docs {
		md := doc.Metadata
		if md.Realm == scope.Realm && md.Space == scope.Space && md.Stack == scope.Stack && md.Cell == "" {
			out = append(out, doc)
		}
	}
	return out, nil
}

// checkName validates an identity name before it reaches the daemon's
// metadata paths.
func checkName(name, what string) error {
	// Guarded inline so the check dominates the name for static path-injection
	// analysis: see scopedefaults.CheckScope.
	if !filepath.IsLocal(name) || !scopedefaults.SafePathSegment(name) || strings.HasPrefix(name, ".") {
		return fmt.Errorf("%w: unsafe %s name", errdefs.ErrGitHubToken, what)
	}
	return nil
}

// find resolves a connector the plan needs and its default method's
// descriptor, checked to accept the scope.
func find(catalog *connector.Catalog, name string, scope scopedefaults.Scope) (v1beta1.BlueprintConnector, error) {
	conn, ok := catalog.Find(name)
	if !ok {
		return v1beta1.BlueprintConnector{}, fmt.Errorf(
			"%w: the connector catalog has no %s connector", errdefs.ErrConnectorInvalid, name)
	}
	method, _ := conn.Method("")
	desc := conn.Descriptor(method)
	if !intconnector.AcceptsScope(desc, scope.Level()) {
		return desc, fmt.Errorf("%w: an identity of connector %q is stored at %s level, not %s",
			errdefs.ErrConfig, desc.Name, strings.Join(desc.Scopes, " or "), scope.Level())
	}
	return desc, nil
}

// Build checks a request and builds the Secrets it stores. existing is what
// Existing returned for the scope: with git, the linked git identity it holds
// is rotated; without, it is named in Plan.StaleGit. Both Secrets are checked
// before either is stored.
func Build(catalog *connector.Catalog, opts Options, existing []v1beta1.SecretDoc) (Plan, error) {
	scope, err := scopedefaults.CheckScope(opts.Scope)
	if err != nil {
		return Plan{}, err
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = DefaultName
	}
	if err = checkName(name, "github identity"); err != nil {
		return Plan{}, err
	}
	token := strings.TrimSpace(opts.Token)
	if token == "" {
		return Plan{}, fmt.Errorf("%w: the token is required", errdefs.ErrGitHubToken)
	}
	desc, err := find(catalog, intconnector.GitHubIdentityTag, scope)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Scope: scope, token: token, Token: v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata:   v1beta1.SecretMetadata{Name: name, Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack},
		Spec:       v1beta1.SecretSpec{Connector: desc.Name, Data: token},
	}}
	if err = intconnector.CheckSecret(desc, plan.Token.Spec); err != nil {
		return Plan{}, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	if opts.Git == nil {
		plan.StaleGit = LinkedGit(existing, plan.Token)
		return plan, nil
	}
	gitDoc, linked, err := buildGit(catalog, plan, *opts.Git, existing)
	if err != nil {
		return Plan{}, err
	}
	plan.Git, plan.Linked = &gitDoc, linked
	return plan, nil
}

// buildGit builds the git identity of a plan: the linked one again, rotated,
// or a new one.
func buildGit(
	catalog *connector.Catalog, plan Plan, opts Git, existing []v1beta1.SecretDoc,
) (v1beta1.SecretDoc, bool, error) {
	desc, err := find(catalog, intconnector.GitIdentityTag, plan.Scope)
	if err != nil {
		return v1beta1.SecretDoc{}, false, err
	}
	link := intconnector.FormatSecretRef(Ref(plan.Token))
	linked := linkedIdentity(existing, link)
	name := strings.TrimSpace(opts.Name)
	switch {
	case name != "":
	case linked != nil:
		name = linked.Metadata.Name
	default:
		name = DefaultGitName
	}
	if err = checkName(name, "git identity"); err != nil {
		return v1beta1.SecretDoc{}, false, err
	}
	if name == plan.Token.Metadata.Name {
		return v1beta1.SecretDoc{}, false, fmt.Errorf(
			"%w: the git identity needs a name of its own, not %q", errdefs.ErrGitHubToken, name)
	}
	if linked != nil && linked.Metadata.Name != name {
		linked = nil
	}
	if linked == nil {
		// A Secret of that name that the token did not write is never
		// overwritten: it may be a key or a password the user stored.
		for _, doc := range existing {
			if doc.Metadata.Name == name {
				return v1beta1.SecretDoc{}, false, fmt.Errorf(
					"%w: a secret %q already exists in %s and is not linked to github identity %q; "+
						"name the git identity", errdefs.ErrGitHubToken, name, scopePath(plan.Scope), plan.Token.Metadata.Name)
			}
		}
	}

	identity := map[string]string{intconnector.IdentityTokenFrom: link}
	hosts := cleanList(opts.Hosts)
	author := strings.TrimSpace(opts.Author)
	if linked != nil {
		if len(hosts) == 0 {
			hosts = intconnector.ParseList(linked.Spec.Identity[intconnector.GitFieldHosts])
		}
		if author == "" {
			author = strings.TrimSpace(linked.Spec.Identity[intconnector.GitFieldAuthor])
		}
		if committer := strings.TrimSpace(linked.Spec.Identity[intconnector.GitFieldCommitter]); committer != "" {
			identity[intconnector.GitFieldCommitter] = committer
		}
	}
	if len(hosts) == 0 {
		hosts = []string{intconnector.GitHubTokenHost}
	}
	identity[intconnector.GitFieldHosts] = strings.Join(hosts, ",")
	if author != "" {
		identity[intconnector.GitFieldAuthor] = author
	}
	username := strings.TrimSpace(opts.Username)
	if username == "" {
		username = intconnector.GitHubTokenUsername
	}
	doc := v1beta1.SecretDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSecret,
		Metadata: v1beta1.SecretMetadata{
			Name: name, Realm: plan.Scope.Realm, Space: plan.Scope.Space, Stack: plan.Scope.Stack,
		},
		Spec: v1beta1.SecretSpec{
			Type:      v1beta1.SecretTypeBasicAuth,
			Connector: desc.Name,
			Identity:  identity,
			BasicAuth: &v1beta1.SecretBasicAuth{Username: username, Token: plan.token},
		},
	}
	if err = intconnector.CheckSecret(desc, doc.Spec); err != nil {
		return v1beta1.SecretDoc{}, false, fmt.Errorf("%w: git identity: %w", errdefs.ErrConfig, err)
	}
	return doc, linked != nil, nil
}

// LinkedGit names the git identity in existing (what Existing returned for
// the github identity's scope) that is linked to the github identity by its
// kukeon.io/token-from key, or nil when none is.
func LinkedGit(existing []v1beta1.SecretDoc, token v1beta1.SecretDoc) *v1beta1.ContainerSecretRef {
	linked := linkedIdentity(existing, intconnector.FormatSecretRef(Ref(token)))
	if linked == nil {
		return nil
	}
	ref := Ref(*linked)
	return &ref
}

// linkedIdentity finds the git identity whose kukeon.io/token-from key names
// the github identity: the first by name when, by hand, there are several.
func linkedIdentity(existing []v1beta1.SecretDoc, link string) *v1beta1.SecretDoc {
	var found []v1beta1.SecretDoc
	for _, doc := range existing {
		if doc.Spec.Connector == intconnector.GitIdentityTag &&
			strings.TrimSpace(doc.Spec.Identity[intconnector.IdentityTokenFrom]) == link {
			found = append(found, doc)
		}
	}
	if len(found) == 0 {
		return nil
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Metadata.Name < found[j].Metadata.Name })
	return &found[0]
}

func cleanList(values []string) []string {
	var out []string
	for _, value := range values {
		for _, entry := range intconnector.ParseList(value) {
			if !slices.Contains(out, entry) {
				out = append(out, entry)
			}
		}
	}
	return out
}

func scopePath(scope scopedefaults.Scope) string {
	parts := []string{scope.Realm}
	for _, part := range []string{scope.Space, scope.Stack} {
		if part == "" {
			break
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "/")
}

// Stored is one Secret a request wrote.
type Stored struct {
	Name      string              `json:"name"`
	Scope     scopedefaults.Scope `json:"scope"`
	Connector string              `json:"connector"`
	Type      string              `json:"type"`
	// Identity is the Secret's readable identity fields.
	Identity map[string]string `json:"identity,omitempty"`
	// Action is created, or updated when a Secret of that name existed.
	Action string `json:"action"`
}

// DefaultSet is one default a request set.
type DefaultSet struct {
	Connector string                      `json:"connector"`
	Method    string                      `json:"method,omitempty"`
	Scope     scopedefaults.Scope         `json:"scope"`
	Level     string                      `json:"level"`
	Identity  *v1beta1.ContainerSecretRef `json:"identity"`
	Previous  *v1beta1.ContainerSecretRef `json:"previous,omitempty"`
	Action    string                      `json:"action"`
}

// Result is what a request stored and set.
type Result struct {
	Secrets  []Stored     `json:"secrets"`
	Defaults []DefaultSet `json:"defaults,omitempty"`
	// StaleGit is Plan.StaleGit once the github identity is stored: the
	// linked git identity the request left on its old token.
	StaleGit *v1beta1.ContainerSecretRef `json:"staleGit,omitempty"`
}

// PartialError is a failure after something was stored: it names what was
// stored and set, and what was not. It never carries the token (the caller
// masks the cause with Plan.Material).
type PartialError struct {
	Result Result
	// Missing names, in order, what was not stored or set.
	Missing []string
	Err     error
}

func (e *PartialError) Error() string {
	done := make([]string, 0, len(e.Result.Secrets)+len(e.Result.Defaults))
	for _, s := range e.Result.Secrets {
		done = append(done, fmt.Sprintf("%s identity %s@%s (%s)", s.Connector, s.Name, scopePath(s.Scope), s.Action))
	}
	for _, d := range e.Result.Defaults {
		done = append(done, fmt.Sprintf("%s default at %s", d.Connector, describeLevel(d.Scope)))
	}
	return fmt.Sprintf("stored %s; not done: %s: %v",
		strings.Join(done, ", "), strings.Join(e.Missing, ", "), e.Err)
}

func (e *PartialError) Unwrap() error { return e.Err }

func describeLevel(scope scopedefaults.Scope) string {
	return scope.Level() + " " + scopePath(scope)
}

// Store writes the plan's Secrets — the github identity first — and, with
// makeDefault, makes each the default of its connector at the plan's scope.
// A failure before anything is stored is returned as is; one after is a
// *PartialError naming what was stored.
func Store(
	ctx context.Context, c Client, catalog *connector.Catalog, plan Plan, makeDefault bool,
) (Result, error) {
	result := Result{Secrets: []Stored{}}
	docs := []v1beta1.SecretDoc{plan.Token}
	if plan.Git != nil {
		docs = append(docs, *plan.Git)
	}
	var defaults []v1beta1.SecretDoc
	if makeDefault {
		defaults = docs
	}
	missing := func(from int) []string {
		var out []string
		for _, doc := range docs[from:] {
			out = append(
				out,
				fmt.Sprintf("%s identity %s@%s", doc.Spec.Connector, doc.Metadata.Name, scopePath(plan.Scope)),
			)
		}
		for _, doc := range defaults {
			out = append(out, fmt.Sprintf("%s default at %s", doc.Spec.Connector, describeLevel(plan.Scope)))
		}
		return out
	}
	for i, doc := range docs {
		created, err := c.CreateSecret(ctx, doc)
		if err != nil {
			if i == 0 {
				return result, fmt.Errorf("store %s identity %s@%s: %w",
					doc.Spec.Connector, doc.Metadata.Name, scopePath(plan.Scope), err)
			}
			return result, &PartialError{Result: result, Missing: missing(i), Err: err}
		}
		action := "updated"
		if created.Created {
			action = "created"
		}
		secretType := doc.Spec.Type
		if secretType == "" {
			secretType = v1beta1.SecretTypeOpaque
		}
		result.Secrets = append(result.Secrets, Stored{
			Name: doc.Metadata.Name, Scope: plan.Scope, Connector: doc.Spec.Connector, Type: secretType,
			Identity: doc.Spec.Identity, Action: action,
		})
	}
	result.StaleGit = plan.StaleGit
	for len(defaults) > 0 {
		doc := defaults[0]
		conn, _ := catalog.Find(doc.Spec.Connector) // Build found both
		ref := Ref(doc)
		set, err := scopedefaults.Set(ctx, c, catalog, conn, plan.Scope, "", &ref)
		if err != nil {
			return result, &PartialError{Result: result, Missing: missing(len(docs)), Err: err}
		}
		defaults = defaults[1:]
		result.Defaults = append(result.Defaults, DefaultSet{
			Connector: set.Connector, Method: set.Method, Scope: set.Scope, Level: set.Level,
			Identity: set.Identity, Previous: set.Previous, Action: set.Action,
		})
	}
	return result, nil
}
