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
	"github.com/eminwux/kukeon/pkg/api/version"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"github.com/eminwux/kukeon/team/components"
	"github.com/eminwux/kukeon/team/connectors"
	"gopkg.in/yaml.v3"
)

// Options supplies materialized definition sources. Each key is a selected
// source name; callers own fetching/pinning. Composition only reads these FSes.
type Options struct {
	Sources map[string]fs.FS
	Scope   Scope
	// Secrets are bindings recorded for the team elsewhere, such as by the
	// stored Team a declaration replaces. The declaration's own
	// spec.secrets win over them; both take precedence over the defaults'
	// spec.secrets and generated defaults.
	Secrets []config.SecretBinding
	// Components overrides the bundled component library (forges, and
	// connectors defined outside the catalog). Nil selects the library
	// shipped with kuke.
	Components fs.FS
	// Connectors overrides the bundled connector catalog. Nil selects the
	// catalog shipped with kuke.
	Connectors fs.FS
}

const defaultScope = "default"

// scriptHarness is the non-LLM harness a program role runs on: its cell runs
// the role's command, loads no skills and needs no AGENTS.md.
const scriptHarness = "script"

// errHarnessNotDeclared is planPair's error for a pair whose role does not
// declare the harness. Compose skips such a pair for an unpinned role.
var errHarnessNotDeclared = errors.New("role does not support selected harness")

type Scope struct{ Realm, Space, Stack string }

// Result keeps the authored declaration separate from resolved resources.
// Team is the document to store once application has succeeded: the
// authored declaration with the secret bindings composition used.
type Result struct {
	Team *config.Team
	// Scope is the normalized scope the team's CellConfigs live in.
	Scope Scope
	// Blueprints are the shared realm-wide blueprints the pairs reference, one
	// per harness × security profile, in first-use order.
	Blueprints []*api.CellBlueprintDoc
	Pairs      []Pair
	// SSHSources names the runtime-cloned sources that clone over SSH with no
	// credential of their own: without a git identity their clones cannot
	// authenticate.
	SSHSources []string
	// DefaultSecrets are the secret slots the Configs leave to the scope
	// defaults, each with the Secret the Realm default points at when none is
	// set yet. Sorted by slot.
	DefaultSecrets []DefaultSecret
	// Notes are operator-facing remarks: settings that no longer have an
	// effect.
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
	// Tasks are the pair's headless task configs, one per task the team
	// selects from the role (`<team>-<role>-<harness>-<task>`). They share
	// the Config's blueprint, fills and secrets.
	Tasks   []*api.CellConfigDoc
	Secrets []SecretRequirement
}

// SecretRequirement contains declarations only; Source is an optional creation
// fallback to read at application time, after runtime existence is checked.
type SecretRequirement struct {
	Identifier  string
	Container   string
	Destination api.BlueprintSecretSlot
	Reference   config.SecretRef
	Source      *config.SecretSource
	// Default marks a slot the Config does not fill: Reference is the Secret
	// the Realm default points at unless a default already exists.
	Default bool
}

// recordedBindings is the declaration's bindings followed by the other
// recorded ones it does not override.
func recordedBindings(own, other []config.SecretBinding) []config.SecretBinding {
	out := slices.Clone(own)
	for _, binding := range other {
		if !slices.ContainsFunc(own, func(b config.SecretBinding) bool { return b.Name == binding.Name }) {
			out = append(out, binding)
		}
	}
	return out
}

// Compose resolves whole-list inheritance and selected role/harness pairs. The
// returned objects are independent of inputs and ordered by authored selections.
//
//nolint:gocyclo,cyclop,funlen // pre-existing complexity; issue #1618 renamed its input types only
func Compose(cfg *config.TeamDefaults, team *config.Team, opts Options) (*Result, error) {
	effective, err := config.Resolve(cfg, team)
	if err != nil {
		return nil, err
	}
	scope, err := normalizeScope(opts.Scope)
	if err != nil {
		return nil, err
	}
	opts.Scope = scope
	entry, err := config.Declaration(team)
	if err != nil {
		return nil, err
	}
	library := opts.Components
	if library == nil {
		library = components.FS
	}
	catalogFS := opts.Connectors
	if catalogFS == nil {
		catalogFS = connectors.FS
	}
	catalog, err := connector.Load(catalogFS)
	if err != nil {
		return nil, fmt.Errorf("connector catalog: %w", err)
	}
	if err = checkChoices(catalog, effective); err != nil {
		return nil, err
	}
	lib, err := component.Load(library, catalog.Components()...)
	if err != nil {
		return nil, fmt.Errorf("component library: %w", err)
	}
	result := &Result{Team: entry, Scope: scope}
	secrets, err := newSecretBindings(recordedBindings(team.Spec.Secrets, opts.Secrets), cfg, scope.Realm)
	if err != nil {
		return nil, err
	}
	// Pass 1: resolve every selected pair, then group pairs by the shared
	// blueprint they use so the blueprint is rendered once with everything
	// its pairs need.
	var plans []*pairPlan
	// owners maps each CellConfig name to the pair that takes it: the name
	// joins the parts with `-`, so two pairs can produce the same one.
	owners := map[string]*pairPlan{}
	groups := map[string]*group{}
	var order []string
	for _, role := range effective.Roles {
		if err = checkTaskHarnesses(role, effective.Harnesses); err != nil {
			return nil, err
		}
	}
	for _, role := range effective.Roles {
		if !config.ValidName(role.Name) {
			return nil, errors.New("role: invalid selection name")
		}
		paired := false
		for _, h := range effective.Harnesses {
			if !role.Selects(h) {
				continue
			}
			plan, planErr := planPair(
				cfg, team.Metadata.Name, role, h, effective, opts, lib, catalog, secrets)
			// An unpinned role pairs only with the team harnesses it
			// declares; a pinned one must declare its pin. No note for a
			// skipped pair: every agent role in a team that lists the
			// script harness would produce one.
			if errors.Is(planErr, errHarnessNotDeclared) && role.Harness == "" {
				continue
			}
			if planErr != nil {
				return nil, fmt.Errorf("compose %s/%s: %w", role.Name, h.Name, planErr)
			}
			paired = true
			if err = claimName(owners, plan); err != nil {
				return nil, err
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
		if !paired {
			return nil, fmt.Errorf("compose %s: role %q declares none of the team's harnesses", role.Name, role.Name)
		}
	}
	// Pass 2: one blueprint per group.
	blueprints := map[string]*api.CellBlueprintDoc{}
	for _, key := range order {
		bp, renderErr := render(key, groups[key], lib, catalog, opts)
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
		var note string
		pair.Tasks, note = bindTasks(plan, pair.Config)
		if note != "" {
			result.Notes = append(result.Notes, note)
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
	result.Team.Spec.Secrets = secrets.entry()
	defaults := map[string]config.SecretRef{}
	for _, plan := range plans {
		result.Notes = append(result.Notes, plan.notes...)
		for _, fill := range plan.fills {
			if fill.isDefault {
				defaults[fill.slot.Name] = fill.binding.SecretRef
			}
		}
	}
	for _, slot := range slices.Sorted(maps.Keys(defaults)) {
		result.DefaultSecrets = append(result.DefaultSecrets, DefaultSecret{Slot: slot, Reference: defaults[slot]})
	}
	result.SSHSources = sshSources(effective.Sources)
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

// claimName records the CellConfig name of a pair, and fails when another pair
// of the compose already produced it: the two would share a Config and a
// harness state volume.
func claimName(owners map[string]*pairPlan, plan *pairPlan) error {
	name := plan.configName()
	if other, taken := owners[name]; taken {
		return fmt.Errorf(
			"role %q harness %q and role %q harness %q of team %q both produce the name %q "+
				"(their CellConfig and %q state volume); rename one of the selections",
			other.selection.Name, other.hsel.Name, plan.selection.Name, plan.hsel.Name, plan.team,
			name, name+stateVolumeSuffix,
		)
	}
	owners[name] = plan
	return nil
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

// daemonMounts are the host mounts the `host` profile names: the daemon socket
// and the host's own `kuke` client, which every shipped role selects. Any
// other set — one of the two alone, or with `kukeon-install` — keeps a suffix
// naming its mounts.
//
//nolint:gochecknoglobals // package-level membership list, read-only.
var daemonMounts = []string{"kuke-binary", "kukeond-socket"}

// name is the blueprint-name suffix of the profile: empty for the default
// posture, `root`, `host` (the daemon socket and `kuke` client mounts) or
// both.
func (p profile) name() string {
	var parts []string
	if p.Root {
		parts = append(parts, "root")
	}
	if slices.Equal(p.Mounts, daemonMounts) {
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
	// paramDefaults are the defaults the role declares for its extraParams,
	// by name, before fact interpolation.
	paramDefaults map[string]string
	// tasks are the role tasks the team selects, rendered as task configs.
	tasks []model.RoleTask
	// realm is the team's realm: where an identity named without a scope is
	// looked up.
	realm string
	// choices are the team's spec.connectors; the role entry's override them.
	choices map[string]config.ConnectorChoice
	// git is the Config's git block: the git identity the team chose.
	git *api.ContainerGit
	// harnessConnector is the connector that authenticates the pair's
	// harness and harnessMethod the method the pair uses; empty when the
	// harness has none.
	harnessConnector string
	harnessMethod    connector.Method
	// notes are operator-facing remarks about the pair.
	notes []string
}

type secretFill struct {
	slot    api.BlueprintSecretSlot
	binding config.SecretBinding
	// isDefault marks a slot left to the scope defaults instead of being
	// written into the Config.
	isDefault bool
}

// stateVolumeSuffix ends the name of a pair's harness state volume.
const stateVolumeSuffix = "-state"

// configName is the name of the pair's CellConfig: `<team>-<role>-<harness>`.
func (p *pairPlan) configName() string {
	return resourceName(p.team, p.selection.Name, p.hsel.Name)
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
	cfg *config.TeamDefaults,
	team string,
	selection config.Role,
	hsel config.Harness,
	effective config.Defaults,
	opts Options,
	lib []component.Component,
	connectorCatalog *connector.Catalog,
	secrets *secretBindings,
) (*pairPlan, error) {
	sources := effective.Sources
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
	if version.Check(role.APIVersion) != nil || role.Kind != "Role" || !config.ValidName(role.Metadata.Name) {
		return nil, errors.New("role.yaml: require v1beta1 Role and valid metadata.name")
	}
	settings, ok := role.Spec.Harnesses[hsel.Name]
	if !ok {
		return nil, errHarnessNotDeclared
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
		return nil, fmt.Errorf("sources: missing clonePath for required role source %q; "+
			"set it with spec.sources[].clonePath", selection.Source)
	}
	if err = checkSettings(hsel.Name, settings); err != nil {
		return nil, err
	}
	tasks, err := selectTasks(&role, selection, hsel.Name)
	if err != nil {
		return nil, err
	}
	if !fs.ValidPath(selection.Path) || !config.SafePath("/"+selection.Path) {
		return nil, errors.New("role path: require a safe source-relative path")
	}
	plan := &pairPlan{
		team: team, selection: selection, hsel: hsel, role: &role, settings: settings,
		definition: definition, catalog: catalog, image: image, needs: needs, roleSource: repo,
		profile: profile{Root: slices.Contains(needs, "root")}, tasks: tasks,
		realm: opts.Scope.Realm, choices: effective.Connectors,
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
			return nil, fmt.Errorf("sources: missing clonePath for required repository %q (a role's "+
				"needs.repos); set it with spec.sources[].clonePath", name)
		}
	}
	if err = planSecrets(plan, lib, connectorCatalog, secrets); err != nil {
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
// when the realm already holds a hand-written blueprint under the same name.
func (r *Result) RenameBlueprint(bp *api.CellBlueprintDoc, name string) {
	bp.Metadata.Name = name
	for i := range r.Pairs {
		if r.Pairs[i].Blueprint == bp {
			r.Pairs[i].Config.Spec.Blueprint.Name = name
			for _, task := range r.Pairs[i].Tasks {
				task.Spec.Blueprint.Name = name
			}
		}
	}
}

// Configs counts every CellConfig the result applies: each pair's Config plus
// its task configs.
func (r *Result) Configs() int {
	n := 0
	for _, p := range r.Pairs {
		n += 1 + len(p.Tasks)
	}
	return n
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
		for _, task := range p.Tasks {
			if err := enc.Encode(task); err != nil {
				return nil, err
			}
		}
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
