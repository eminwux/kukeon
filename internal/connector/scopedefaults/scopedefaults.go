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

// Package scopedefaults reads and edits the default identity each connector
// has on a Realm, Space or Stack (issue #1548, §7). `kuke connector` and the
// web UI's Connectors page both use it (issue #1591), so a default set from
// either is checked the same way and written the same way: read the scope's
// document, change the one entry, re-apply it through the daemon's
// write-through apply path, keeping every other default the scope carries.
//
// It iterates the connector catalog: no function here names a connector.
// It is a sub-package, not part of internal/connector, because it needs the
// catalog (pkg/team/connector), which itself imports internal/connector.
package scopedefaults

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	intconnector "github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"gopkg.in/yaml.v3"
)

// Client is the part of the daemon client the scope defaults need.
type Client interface {
	GetRealm(context.Context, v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error)
	GetSpace(context.Context, v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error)
	GetStack(context.Context, v1beta1.StackDoc) (kukeonv1.GetStackResult, error)
	GetSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error)
	ApplyDocuments(context.Context, []byte) (kukeonv1.ApplyDocumentsResult, error)
}

var _ Client = kukeonv1.Client(nil)

// Scope is the Realm, Space and Stack a default or an identity lives at.
type Scope struct {
	Realm string `json:"realm"`
	Space string `json:"space,omitempty"`
	Stack string `json:"stack,omitempty"`
}

// Level names the deepest coordinate the scope sets.
func (s Scope) Level() string {
	switch {
	case s.Stack != "":
		return v1beta1.ConnectorScopeStack
	case s.Space != "":
		return v1beta1.ConnectorScopeSpace
	default:
		return v1beta1.ConnectorScopeRealm
	}
}

// SafePathSegment accepts empty optional coordinates and rejects values that
// could escape the daemon's metadata directories.
func SafePathSegment(value string) bool {
	if value == "" {
		return true
	}
	return value != "." && value != ".." && filepath.IsLocal(value) &&
		!strings.ContainsAny(value, `/\\`) && !strings.ContainsRune(value, 0)
}

// CheckScope validates user-supplied scope coordinates before any of them
// reaches the daemon's metadata paths.
func CheckScope(s Scope) (Scope, error) {
	realm := strings.TrimSpace(s.Realm)
	rawSpace, rawStack := strings.TrimSpace(s.Space), strings.TrimSpace(s.Stack)
	if realm == "" {
		return Scope{}, errdefs.ErrRealmNameRequired
	}
	if rawStack != "" && rawSpace == "" {
		return Scope{}, fmt.Errorf("%w: scope coordinates cannot contain gaps", errdefs.ErrConfig)
	}
	// Each coordinate is guarded inline (not through a loop, helper or struct
	// field) so the filepath.IsLocal check visibly dominates the returned
	// values for static path-injection analysis. An optional coordinate is
	// copied only once checked, so its unset path carries the empty literal.
	unsafe := func() (Scope, error) {
		return Scope{}, fmt.Errorf("%w: unsafe scope coordinate", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(realm) || !SafePathSegment(realm) {
		return unsafe()
	}
	var space, stack string
	if rawSpace != "" {
		if !filepath.IsLocal(rawSpace) || !SafePathSegment(rawSpace) {
			return unsafe()
		}
		space = rawSpace
	}
	if rawStack != "" {
		if !filepath.IsLocal(rawStack) || !SafePathSegment(rawStack) {
			return unsafe()
		}
		stack = rawStack
	}
	return Scope{Realm: realm, Space: space, Stack: stack}, nil
}

// CheckSecretRef validates a user-supplied Secret reference. A nil reference
// is returned as is: it clears a choice.
func CheckSecretRef(ref *v1beta1.ContainerSecretRef) (*v1beta1.ContainerSecretRef, error) {
	if ref == nil {
		return nil, nil //nolint:nilnil // no reference is a valid input: it clears a choice
	}
	name, realm := strings.TrimSpace(ref.Name), strings.TrimSpace(ref.Realm)
	rawSpace, rawStack := strings.TrimSpace(ref.Space), strings.TrimSpace(ref.Stack)
	if name == "" || realm == "" || (rawStack != "" && rawSpace == "") || ref.Cell != "" {
		return nil, fmt.Errorf("%w: name a secret by name and realm (space and stack optional)", errdefs.ErrConfig)
	}
	// Guarded inline, optional coordinates copied only once checked: see
	// CheckScope.
	unsafe := func() (*v1beta1.ContainerSecretRef, error) {
		return nil, fmt.Errorf("%w: unsafe secret reference", errdefs.ErrConfig)
	}
	if !filepath.IsLocal(name) || !SafePathSegment(name) {
		return unsafe()
	}
	if !filepath.IsLocal(realm) || !SafePathSegment(realm) {
		return unsafe()
	}
	var space, stack string
	if rawSpace != "" {
		if !filepath.IsLocal(rawSpace) || !SafePathSegment(rawSpace) {
			return unsafe()
		}
		space = rawSpace
	}
	if rawStack != "" {
		if !filepath.IsLocal(rawStack) || !SafePathSegment(rawStack) {
			return unsafe()
		}
		stack = rawStack
	}
	return &v1beta1.ContainerSecretRef{Name: name, Realm: realm, Space: space, Stack: stack}, nil
}

// Target is one place a default identity is stored: the secret slot of a
// method, or the git identity reference.
type Target struct {
	Conn   *connector.Connector
	Method connector.Method
	Slot   string
	Git    bool
}

// Descriptor is the connector definition the target's identities are
// checked against.
func (t Target) Descriptor() v1beta1.BlueprintConnector { return t.Conn.Descriptor(t.Method) }

// Read returns the identity a defaults fill sets for the target.
func (t Target) Read(d *v1beta1.ScopeDefaults) *v1beta1.ContainerSecretRef {
	if d == nil {
		return nil
	}
	if t.Git {
		if d.Git == nil {
			return nil
		}
		return d.Git.IdentityRef
	}
	return d.Secrets[t.Slot].SecretRef
}

// Write sets (or, for a nil identity, clears) the target on a defaults fill.
func (t Target) Write(d *v1beta1.ScopeDefaults, identity *v1beta1.ContainerSecretRef) {
	if t.Git {
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
		delete(secrets, t.Slot)
	} else {
		secrets[t.Slot] = v1beta1.CellConfigSecretFill{SecretRef: identity}
	}
	d.Secrets = secrets
	if len(secrets) == 0 {
		d.Secrets = nil
	}
}

// Targets lists every place the catalog's connectors keep a default: one per
// method delivered through a secret slot, one for a git-delivered connector.
// A login kept in the state volume has no identity, and a registry credential
// is bound per server on the Realm, not defaulted.
func Targets(catalog *connector.Catalog) []Target {
	var out []Target
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		if len(conn.Spec.Methods) == 0 {
			if conn.Spec.Delivery == v1beta1.ConnectorDeliveryGit {
				out = append(out, Target{Conn: conn, Git: true})
			}
			continue
		}
		for _, method := range conn.Spec.Methods {
			if method.Secret != nil {
				out = append(out, Target{Conn: conn, Method: method, Slot: method.Secret.Name})
			}
		}
	}
	return out
}

// FindTarget resolves the place a connector and method keep a default.
func FindTarget(catalog *connector.Catalog, conn *connector.Connector, method string) (Target, error) {
	var candidates []Target
	for _, t := range Targets(catalog) {
		if t.Conn.Metadata.Name == conn.Metadata.Name {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 0 {
		return Target{}, fmt.Errorf(
			"%w: connector %q takes no default identity", errdefs.ErrConfig, conn.Metadata.Name)
	}
	method = strings.TrimSpace(method)
	for _, t := range candidates {
		if t.Method.Name == method || (method == "" && len(candidates) == 1) {
			return t, nil
		}
	}
	names := make([]string, 0, len(candidates))
	for _, t := range candidates {
		names = append(names, t.Method.Name)
	}
	return Target{}, fmt.Errorf(
		"%w: connector %q: name the method the default is for (one of: %s)",
		errdefs.ErrConfig, conn.Metadata.Name, strings.Join(names, ", "))
}

// Fills reads the defaults fill of each level of a scope. A level the scope
// does not name is absent.
func Fills(ctx context.Context, c Client, scope Scope) (map[string]*v1beta1.ScopeDefaults, error) {
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

// Default is the default identity of one connector method across the levels
// of a scope: what each level sets, and what a Config in the scope that
// chooses nothing would get.
type Default struct {
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

// Defaults reports, for a checked scope, the default identity of every
// connector method at each level and the one that wins.
func Defaults(ctx context.Context, c Client, catalog *connector.Catalog, scope Scope) ([]Default, error) {
	fills, err := Fills(ctx, c, scope)
	if err != nil {
		return nil, err
	}
	out := []Default{}
	for _, t := range Targets(catalog) {
		row := Default{
			Connector: t.Conn.Metadata.Name,
			Method:    t.Method.Name,
			Delivery:  t.Conn.Delivery(t.Method),
			Slot:      t.Slot,
			Levels:    map[string]v1beta1.ContainerSecretRef{},
		}
		if !t.Git {
			row.Component = t.Conn.ComponentName(t.Method)
		} else {
			row.Component = t.Conn.Metadata.Name
		}
		for _, level := range intconnector.Scopes { // realm, space, stack: deeper wins
			ref := t.Read(fills[level])
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

// CheckIdentity confirms a Secret exists and can be an identity of the
// connector. Only its readable part is looked at.
func CheckIdentity(
	ctx context.Context, c Client, desc v1beta1.BlueprintConnector, ref v1beta1.ContainerSecretRef,
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

// SetResult reports a default written by Set.
type SetResult struct {
	Connector string
	Method    string
	Scope     Scope
	Level     string
	// Identity is the default now set; nil when it was cleared.
	Identity *v1beta1.ContainerSecretRef
	// Previous is the default the level set before; nil when it set none.
	Previous *v1beta1.ContainerSecretRef
	// Action is the apply action: updated or unchanged.
	Action string
}

// Set writes — or, with a nil identity, clears — a connector's default
// identity on the Realm, Space or Stack a checked scope names, after checking
// that the connector takes a default at that level and that the Secret exists
// and is one of its identities. Configs that chose no identity pick the change
// up on their next render; none is rewritten.
func Set(
	ctx context.Context, c Client, catalog *connector.Catalog, conn *connector.Connector,
	scope Scope, method string, identity *v1beta1.ContainerSecretRef,
) (SetResult, error) {
	target, err := FindTarget(catalog, conn, method)
	if err != nil {
		return SetResult{}, err
	}
	desc := target.Descriptor()
	if !intconnector.AcceptsScope(desc, scope.Level()) {
		return SetResult{}, fmt.Errorf(
			"%w: a default for connector %q is set at %s level, not %s",
			errdefs.ErrConfig, desc.Name, strings.Join(desc.Scopes, " or "), scope.Level())
	}
	identity, err = CheckSecretRef(identity)
	if err != nil {
		return SetResult{}, err
	}
	if identity != nil {
		if err = CheckIdentity(ctx, c, desc, *identity); err != nil {
			return SetResult{}, err
		}
	}
	var previous *v1beta1.ContainerSecretRef
	action, err := Edit(ctx, c, scope, func(d *v1beta1.ScopeDefaults) {
		if ref := target.Read(d); ref != nil {
			copied := *ref
			previous = &copied
		}
		target.Write(d, identity)
	})
	if err != nil {
		return SetResult{}, err
	}
	return SetResult{
		Connector: desc.Name, Method: target.Method.Name, Scope: scope, Level: scope.Level(),
		Identity: identity, Previous: previous, Action: action,
	}, nil
}

// Edit applies edit to the defaults fill of the Realm, Space or Stack the
// scope names and re-applies that one document. It returns the apply action
// (updated, unchanged).
func Edit(ctx context.Context, c Client, scope Scope, edit func(*v1beta1.ScopeDefaults)) (string, error) {
	switch scope.Level() {
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

func editStackDefaults(ctx context.Context, c Client, scope Scope, edit func(*v1beta1.ScopeDefaults)) (string, error) {
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
	return ApplyDocument(ctx, c, desired, v1beta1.KindStack)
}

func editSpaceDefaults(ctx context.Context, c Client, scope Scope, edit func(*v1beta1.ScopeDefaults)) (string, error) {
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
	return ApplyDocument(ctx, c, desired, v1beta1.KindSpace)
}

func editRealmDefaults(ctx context.Context, c Client, scope Scope, edit func(*v1beta1.ScopeDefaults)) (string, error) {
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
	return ApplyDocument(ctx, c, desired, v1beta1.KindRealm)
}

// ApplyDocument applies one document and returns its apply action.
func ApplyDocument(ctx context.Context, c Client, doc any, kind v1beta1.Kind) (string, error) {
	action, _, err := ApplyDocumentWarnings(ctx, c, doc, kind)
	return action, err
}

// ApplyDocumentWarnings applies one document and returns its apply action and
// the warnings the daemon raised for it: risks the operator accepted, such as
// a Config naming a login identity another role uses (issue #1789).
func ApplyDocumentWarnings(ctx context.Context, c Client, doc any, kind v1beta1.Kind) (string, []string, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", nil, fmt.Errorf("marshal %s document: %w", kind, err)
	}
	result, err := c.ApplyDocuments(ctx, raw)
	if err != nil {
		return "", nil, err
	}
	for _, resource := range result.Resources {
		if resource.Kind != string(kind) {
			continue
		}
		if resource.Action == "failed" {
			if resource.Error != "" {
				return "", nil, fmt.Errorf(
					"%w: apply %s %q: %s",
					errdefs.ErrConfig,
					kind,
					resource.Name,
					resource.Error,
				)
			}
			return "", nil, fmt.Errorf("apply %s failed", kind)
		}
		return resource.Action, resource.Warnings, nil
	}
	return "applied", nil, nil
}
