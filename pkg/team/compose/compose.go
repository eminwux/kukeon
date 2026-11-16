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

// Package compose produces team-owned kukeon documents without contacting a
// daemon, fetching sources, reading credentials, or writing files.
//
// A team renders onto two kinds. A CellBlueprint is a shared, realm-wide form:
// one per harness × security profile, self-describing (typed parameters,
// components, modes) and free of role facts, so every role and team in the
// realm — and the web UI — can use the same one. A CellConfig is the answers:
// one per selected role/harness pair, carrying everything that differs between
// two cells (role, repos, image, secrets, enabled connectors).
package compose

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/distribution/reference"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"github.com/eminwux/kukeon/team/components"
	"gopkg.in/yaml.v3"
)

// Options supplies materialized definition sources. Each key is a selected
// source name; callers own fetching/pinning. Composition only reads these FSes.
type Options struct {
	Sources map[string]fs.FS
	Scope   Scope
	// Secrets are the bindings recorded in the team's existing TeamEntry; they
	// take precedence over global spec.secrets and generated defaults.
	Secrets []config.SecretBinding
	// Components overrides the bundled component library (connectors and
	// forges). Nil selects the library shipped with kuke.
	Components fs.FS
}

const defaultScope = "default"

type Scope struct{ Realm, Space, Stack string }

// Result keeps the authored declaration separate from resolved resources.
// Entry is a candidate for #13 to persist only after successful application.
type Result struct {
	Entry *config.TeamEntry
	// Scope is the normalized scope the team's CellConfigs live in.
	Scope Scope
	// Blueprints are the shared realm-wide blueprints the pairs reference, one
	// per harness × security profile, in first-use order.
	Blueprints []*api.CellBlueprintDoc
	Pairs      []Pair
	// Git is the operator's default git identity, credentials and signing
	// config, to be written as scope defaults. Nil when none is configured.
	Git *api.ContainerGit
	// Notes are operator-facing remarks: settings that no longer have an
	// effect and sources no stored credential covers.
	Notes []string
}

type Pair struct {
	Role    string
	Harness string
	Image   harness.CatalogImage
	// Blueprint is the shared blueprint the Config references; several pairs
	// point at the same document.
	Blueprint *api.CellBlueprintDoc
	Config    *api.CellConfigDoc
	Secrets   []SecretRequirement
}

// SecretRequirement contains declarations only; Source is an optional creation
// fallback to read at application time, after runtime existence is checked.
type SecretRequirement struct {
	Identifier  string
	Container   string
	Destination api.BlueprintSecretSlot
	Reference   config.SecretRef
	Source      *config.SecretSource
}

// Compose resolves whole-list inheritance and selected role/harness pairs. The
// returned objects are independent of inputs and ordered by authored selections.
func Compose(cfg *config.TeamsConfig, team *config.Team, opts Options) (*Result, error) {
	effective, err := config.Resolve(cfg, team)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(opts.Scope)
	if err != nil {
		return nil, err
	}
	opts.Scope = scope
	entry, err := config.Entry(team)
	if err != nil {
		return nil, err
	}
	library := opts.Components
	if library == nil {
		library = components.FS
	}
	lib, err := component.Load(library)
	if err != nil {
		return nil, fmt.Errorf("component library: %w", err)
	}
	result := &Result{Entry: entry, Scope: scope}
	secrets, err := newSecretBindings(opts.Secrets, cfg, scope.Realm)
	if err != nil {
		return nil, err
	}
	// Pass 1: resolve every selected pair, then group pairs by the shared
	// blueprint they use so the blueprint is rendered once with everything
	// its pairs need.
	var plans []*pairPlan
	groups := map[string]*group{}
	var order []string
	for _, role := range effective.Roles {
		if !config.ValidName(role.Name) {
			return nil, errors.New("role: invalid selection name")
		}
		for _, h := range effective.Harnesses {
			if !role.Selects(h) {
				continue
			}
			plan, planErr := planPair(cfg, team.Metadata.Name, role, h, effective.Sources, opts, lib, secrets)
			if planErr != nil {
				return nil, fmt.Errorf("compose %s/%s: %w", role.Name, h.Name, planErr)
			}
			plans = append(plans, plan)
			key := plan.blueprintName()
			if groups[key] == nil {
				groups[key] = &group{}
				order = append(order, key)
			}
			if groupErr := groups[key].add(plan); groupErr != nil {
				return nil, fmt.Errorf("compose %s/%s: %w", role.Name, h.Name, groupErr)
			}
		}
	}
	// Pass 2: one blueprint per group.
	blueprints := map[string]*api.CellBlueprintDoc{}
	for _, key := range order {
		bp, renderErr := render(key, groups[key], lib, opts)
		if renderErr != nil {
			return nil, fmt.Errorf("blueprint %s: %w", key, renderErr)
		}
		blueprints[key] = bp
		result.Blueprints = append(result.Blueprints, bp)
	}
	// Pass 3: one config per pair.
	declared := map[string]bool{}
	for _, plan := range plans {
		bp := blueprints[plan.blueprintName()]
		pair, bindErr := bind(plan, bp, cfg, team, opts)
		if bindErr != nil {
			return nil, fmt.Errorf("compose %s/%s: %w", plan.selection.Name, plan.hsel.Name, bindErr)
		}
		for name := range allParameters(bp) {
			declared[name] = true
		}
		result.Pairs = append(result.Pairs, pair)
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Spec.Parameters)) {
		if !declared[name] {
			return nil, fmt.Errorf("spec.parameters: unsupported parameter %q", name)
		}
		if composed[name] {
			return nil, fmt.Errorf(
				"spec.parameters: %q is set by composition from the role and source selections",
				name,
			)
		}
	}
	if err = secrets.checkParameters(cfg.Spec.Parameters); err != nil {
		return nil, err
	}
	result.Entry.Secrets = secrets.entry()
	result.Git, result.Notes = defaultGit(cfg.Spec.Git)
	result.Notes = append(result.Notes, credentialNotes(result.Git, effective.Sources)...)
	for _, plan := range plans {
		if slices.Contains(plan.role.Spec.Needs.Mounts, "ssh") {
			result.Notes = append(result.Notes, fmt.Sprintf(
				"role %s: needs.mounts ssh no longer mounts the host ~/.ssh; cells clone, push and sign with stored git credentials",
				plan.selection.Name,
			))
		}
	}
	slices.Sort(result.Notes)
	result.Notes = slices.Compact(result.Notes)
	return result, nil
}

func normalizeScope(s Scope) (Scope, error) {
	if s.Realm == "" {
		s.Realm = defaultScope
	}
	if s.Space == "" {
		s.Space = defaultScope
	}
	if s.Stack == "" {
		s.Stack = defaultScope
	}
	if !config.ValidName(s.Realm) || !config.ValidName(s.Space) || !config.ValidName(s.Stack) {
		return Scope{}, errors.New("scope: require valid realm, space, and stack names")
	}
	return s, nil
}

// profile is the security posture of a blueprint. It is fixed in the
// blueprint — never a Config toggle — so each distinct posture is its own
// blueprint variant.
type profile struct {
	// Root selects a privileged cell with a nested container runtime.
	Root bool
	// Mounts are the host-mount selectors, sorted.
	Mounts []string
}

// hostMounts are the selectors that bind a host path into the cell. `ssh` is
// deliberately absent: it is still accepted on a role, but mounts nothing.
//
//nolint:gochecknoglobals // package-level membership list, read-only.
var hostMounts = []string{"kuke-binary", "kukeon-install", "kukeond-socket"}

// name is the blueprint-name suffix of the profile: empty for the default
// posture, `root`, `host` (all kukeon host mounts) or both.
func (p profile) name() string {
	var parts []string
	if p.Root {
		parts = append(parts, "root")
	}
	if slices.Equal(p.Mounts, hostMounts) {
		parts = append(parts, "host")
	} else {
		parts = append(parts, p.Mounts...)
	}
	return strings.Join(parts, "-")
}

// pairPlan is one selected role/harness pair with everything read from its
// definitions, before any document is rendered.
type pairPlan struct {
	team       string
	selection  config.Role
	hsel       config.Harness
	role       *model.Role
	settings   model.RoleHarness
	definition *harness.Harness
	catalog    *harness.ImageCatalog
	image      harness.CatalogImage
	needs      []string
	profile    profile
	// roleSource is the runtime source the role is read from in the cell.
	roleSource config.Source
	// sources are every runtime-cloned source of the team.
	sources []config.Source
	// components are the optional components the pair enables, by name.
	components []string
	// fills binds blueprint secret slots to runtime secrets.
	fills []secretFill
	// extraSecrets are role-declared secrets no component or harness slot
	// covers; the shared blueprint gains an optional slot for each.
	extraSecrets []api.BlueprintSecretSlot
	// extraParams are role-declared parameters the blueprint does not declare.
	extraParams []string
}

type secretFill struct {
	slot    api.BlueprintSecretSlot
	binding config.SecretBinding
}

func (p *pairPlan) blueprintName() string {
	return resourceName(p.hsel.Name, p.profile.name())
}

// group collects the pairs sharing one blueprint. Role-specific leftovers
// (extra secret slots, extra parameters, extra repos) are unioned so the
// blueprint serves every pair of the team that uses it.
type group struct {
	plans        []*pairPlan
	extraSecrets []api.BlueprintSecretSlot
	extraParams  []string
	extraRepos   []config.Source
}

func (g *group) add(plan *pairPlan) error {
	g.plans = append(g.plans, plan)
	for _, slot := range plan.extraSecrets {
		i := slices.IndexFunc(g.extraSecrets, func(s api.BlueprintSecretSlot) bool { return s.Name == slot.Name })
		if i < 0 {
			g.extraSecrets = append(g.extraSecrets, slot)
			continue
		}
		if g.extraSecrets[i].EnvName != slot.EnvName {
			return fmt.Errorf("secret %q: conflicting environment destinations across roles", slot.Name)
		}
	}
	for _, name := range plan.extraParams {
		if !slices.Contains(g.extraParams, name) {
			g.extraParams = append(g.extraParams, name)
		}
	}
	for _, source := range plan.extraRepos() {
		i := slices.IndexFunc(g.extraRepos, func(s config.Source) bool { return s.Name == source.Name })
		if i < 0 {
			g.extraRepos = append(g.extraRepos, source)
		}
	}
	return nil
}

// extraRepos are the runtime sources beyond the two generic slots every
// blueprint declares (role-source and project).
func (p *pairPlan) extraRepos() []config.Source {
	var out []config.Source
	for _, source := range p.sources {
		if source.Name == p.roleSource.Name || source.Name == projectSlot {
			continue
		}
		out = append(out, source)
	}
	return out
}

// planPair reads and validates everything one selected role/harness pair
// needs before any document is rendered.
//
//nolint:funlen // linear load-and-validate of one pair's definitions.
func planPair(
	cfg *config.TeamsConfig,
	team string,
	selection config.Role,
	hsel config.Harness,
	sources []config.Source,
	opts Options,
	lib []component.Component,
	secrets *secretBindings,
) (*pairPlan, error) {
	if !config.ValidName(hsel.Name) {
		return nil, errors.New("invalid harness selection name")
	}
	rfs := opts.Sources[selection.Source]
	hfs := opts.Sources[hsel.Source]
	if rfs == nil || hfs == nil {
		return nil, errors.New("missing materialized role or harness source")
	}
	var role model.Role
	if err := load(rfs, path.Join(selection.Path, "role.yaml"), &role); err != nil {
		return nil, err
	}
	if role.APIVersion != config.APIVersion || role.Kind != "Role" || !config.ValidName(role.Metadata.Name) {
		return nil, errors.New("role.yaml: require kuketeams.io/v1 Role and valid metadata.name")
	}
	settings, ok := role.Spec.Harnesses[hsel.Name]
	if !ok {
		return nil, errors.New("role does not support selected harness")
	}
	definition, err := harness.Load(hfs, hsel.Path)
	if err != nil {
		return nil, err
	}
	if definition.Metadata.Name != hsel.Name {
		return nil, fmt.Errorf("%s/harness.yaml: metadata.name must match selected harness %q", hsel.Path, hsel.Name)
	}
	if err = checkAssets(rfs, selection.Path, &role, settings, hsel.Name); err != nil {
		return nil, err
	}
	catalog, err := harness.LoadCatalog(hfs, hsel.Path, definition.Metadata.Name)
	if err != nil {
		return nil, err
	}
	var extra []string
	if selection.Needs != nil {
		extra = selection.Needs.Image
	}
	needs := union(role.Spec.Needs.Image, extra)
	image, err := SelectImage(catalog, hsel.Name, needs)
	if err != nil {
		return nil, fmt.Errorf("%s/images.yaml: %w", hsel.Path, err)
	}
	repo, ok := runtimeSource(sources, selection.Source)
	if !ok {
		return nil, fmt.Errorf("sources: missing clonePath for required role source %q", selection.Source)
	}
	if err = checkSettings(hsel.Name, settings); err != nil {
		return nil, err
	}
	if !fs.ValidPath(selection.Path) || !config.SafePath("/"+selection.Path) {
		return nil, errors.New("role path: require a safe source-relative path")
	}
	plan := &pairPlan{
		team: team, selection: selection, hsel: hsel, role: &role, settings: settings,
		definition: definition, catalog: catalog, image: image, needs: needs, roleSource: repo,
		profile: profile{Root: slices.Contains(needs, "root")},
	}
	for _, mount := range role.Spec.Needs.Mounts {
		if slices.Contains(hostMounts, mount) && !slices.Contains(plan.profile.Mounts, mount) {
			plan.profile.Mounts = append(plan.profile.Mounts, mount)
		}
	}
	slices.Sort(plan.profile.Mounts)
	for _, source := range sources {
		if source.ClonePath != "" {
			plan.sources = append(plan.sources, source)
		}
	}
	for _, name := range union(role.Spec.Needs.Repos, []string{selection.Source}) {
		if _, found := runtimeSource(sources, name); !found {
			return nil, fmt.Errorf("sources: missing clonePath for required repository %q", name)
		}
	}
	if err = planSecrets(plan, lib, secrets); err != nil {
		return nil, err
	}
	if err = planParameters(plan, cfg); err != nil {
		return nil, err
	}
	return plan, nil
}

// resourceName joins authored components and normalizes punctuation runs so cell
// identifiers remain readable and valid for the container runtime.
func resourceName(parts ...string) string {
	return strings.Join(strings.FieldsFunc(strings.Join(parts, "-"), func(r rune) bool {
		return r == '-' || r == '.'
	}), "-")
}

// stateVolumeName retains the original component boundaries in a digest so
// readable resource aliases cannot share persistent state accidentally.
func stateVolumeName(parts ...string) string {
	name := resourceName(parts...)
	if !strings.ContainsAny(strings.Join(parts, ""), ".-") {
		return name
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%s-%x", name, digest[:8])
}

func load(source fs.FS, filename string, out any) error {
	raw, err := fs.ReadFile(source, path.Clean(filename))
	if err != nil {
		return fmt.Errorf("read definition %s: %w", filename, err)
	}
	return decode(raw, filename, out)
}

func decode(raw []byte, name string, out any) error {
	d := yaml.NewDecoder(bytes.NewReader(raw))
	d.KnownFields(true)
	if err := d.Decode(out); err != nil {
		return fmt.Errorf("%s: invalid document: %w", name, err)
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: expected exactly one YAML document", name)
	}
	return nil
}

// runtimeSource resolves only explicitly requested clones from effective selections.
func runtimeSource(sources []config.Source, name string) (config.Source, bool) {
	for _, source := range sources {
		if source.Name == name && source.ClonePath != "" {
			return source, true
		}
	}
	return config.Source{}, false
}

// SelectImage preserves catalog order and requires one published image to carry
// the selected harness plus all mandatory and additive capabilities.
func SelectImage(c *harness.ImageCatalog, name string, needs []string) (harness.CatalogImage, error) {
	if err := c.Validate(name); err != nil {
		return harness.CatalogImage{}, err
	}
	required := union(needs, []string{name})
	for _, image := range c.Spec.Images {
		if slices.Contains(image.Capabilities, "root") && !slices.Contains(needs, "root") {
			continue
		}
		if !slices.ContainsFunc(required, func(s string) bool { return !slices.Contains(image.Capabilities, s) }) {
			if !published(image.Image) {
				return harness.CatalogImage{}, errors.New(
					"matching catalog image requires a registry-qualified published reference",
				)
			}
			return image, nil
		}
	}
	return harness.CatalogImage{}, fmt.Errorf(
		"no published image for harness %q with all capabilities %v; update the selected harness images.yaml",
		name,
		required,
	)
}

func published(ref string) bool {
	named, err := reference.ParseNormalizedNamed(ref)
	if err != nil {
		return false
	}
	// Require an authored registry-qualified reference, not Docker's implicit
	// docker.io/library expansion or an unversioned local image name.
	if !strings.Contains(ref, "/") || !strings.HasPrefix(ref, reference.Domain(named)+"/") {
		return false
	}
	_, tagged := named.(reference.Tagged)
	_, pinned := named.(reference.Canonical)
	return tagged || pinned
}

func union(a, b []string) []string {
	out := append(append([]string{}, a...), b...)
	slices.Sort(out)
	return slices.Compact(out)
}

// RenameBlueprint gives a shared blueprint a new name and repoints every
// Config that references it. Application uses it to append a content digest
// when the realm already holds a different blueprint under the same name.
func (r *Result) RenameBlueprint(bp *api.CellBlueprintDoc, name string) {
	bp.Metadata.Name = name
	for i := range r.Pairs {
		if r.Pairs[i].Blueprint == bp {
			r.Pairs[i].Config.Spec.Blueprint.Name = name
		}
	}
}

// MarshalYAML returns only Blueprints then Configs. Secret values and creation
// source declarations are never part of the daemon document stream.
func (r *Result) MarshalYAML() ([]byte, error) {
	if r == nil {
		return nil, errors.New("nil composition result")
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	const yamlIndent = 2
	enc.SetIndent(yamlIndent)
	for _, bp := range r.Blueprints {
		if err := enc.Encode(bp); err != nil {
			return nil, err
		}
	}
	for _, p := range r.Pairs {
		if err := enc.Encode(p.Config); err != nil {
			return nil, err
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
