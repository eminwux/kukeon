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

package compose

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
)

// planSecrets decides, for one pair, which optional components it enables and
// which runtime secret fills each blueprint secret slot.
//
// A team entry may add secret names of its own (`roles[].needs.secrets`); they
// resolve like the role's.
//
// A role names what it needs three ways: `connectors` / `forges` (connectors
// of the catalog and components of the library), and the secret names in
// `harnesses.<h>.secrets` / `needs.secrets`. A secret name selects, in order:
// the component slot of that name or that lists it as an alias (`gh-token`
// enables the github connector), the harness or harness-mode slot of that
// name, and otherwise an optional slot the shared blueprint gains for it.
//
// A slot is filled in the Config only when its identity was chosen: by the
// team's `connectors`, or by a TeamEntry / TeamsConfig binding that names
// something other than the default secret. Every other slot is left to the
// Realm → Space → Stack defaults (issue #1548), so it resolves when a cell is
// created and follows a later change of a default.
//
//nolint:funlen,gocyclo,cyclop // the ways a role names a secret, resolved in their documented order.
func planSecrets(
	plan *pairPlan, lib []component.Component, catalog *connector.Catalog, secrets *secretBindings,
) error {
	enable := func(name string) error {
		i := slices.IndexFunc(lib, func(c component.Component) bool { return c.Metadata.Name == name })
		if i < 0 {
			return fmt.Errorf("role names unknown component %q", name)
		}
		for _, need := range append([]string{name}, lib[i].Spec.Needs...) {
			if !slices.Contains(plan.components, need) {
				plan.components = append(plan.components, need)
			}
		}
		return nil
	}
	names := union(plan.role.Spec.Needs.Secrets, plan.settings.Secrets)
	// Secrets the team adds for this deployment (issue #1509).
	if plan.selection.Needs != nil {
		names = union(names, plan.selection.Needs.Secrets)
	}
	if err := planConnectors(plan, lib, catalog, names, enable); err != nil {
		return err
	}
	for _, name := range plan.role.Spec.Forges {
		if err := enableKind(lib, name, api.ComponentKindForge, "forges", enable); err != nil {
			return err
		}
	}

	harnessSlots := slices.Clone(plan.definition.Spec.Secrets)
	for _, mode := range plan.definition.Spec.Modes {
		harnessSlots = append(harnessSlots, mode.Secrets...)
	}
	bound := map[string]bool{}
	fill := func(slot api.BlueprintSecretSlot, binding config.SecretBinding, isDefault bool) {
		if slot.Mode == "" {
			slot.Mode = api.BlueprintSecretModeEnv
		}
		plan.fills = append(plan.fills, secretFill{slot: slot, binding: binding, isDefault: isDefault})
		bound[slot.Name] = true
	}
	groupTaken := func(c component.Component) bool {
		if c.Spec.Group == "" {
			return false
		}
		return slices.ContainsFunc(lib, func(other component.Component) bool {
			return other.Spec.Group == c.Spec.Group && other.Metadata.Name != c.Metadata.Name &&
				slices.Contains(plan.components, other.Metadata.Name)
		})
	}
	// A role-listed secret name that selects a component slot keeps the
	// binding recorded under that name.
	named := map[string]namedBinding{}
	for _, name := range names {
		binding, isDefault, err := secrets.lookup(name)
		if err != nil {
			return err
		}
		if c, slot, ok := aliasSlot(lib, name); ok && rendersInto(c, plan.definition, catalog) {
			// The method the team chose (or an earlier name selected) wins
			// over another method of the same group.
			if groupTaken(c) {
				continue
			}
			if err = enable(c.Metadata.Name); err != nil {
				return err
			}
			secretName := slot.SecretName
			if secretName == "" {
				secretName = slot.Name
			}
			if _, set := named[slot.Name]; !set {
				named[slot.Name] = namedBinding{binding: binding, isDefault: secrets.isDefault(binding, secretName)}
			}
			continue
		}
		if i := slices.IndexFunc(harnessSlots, func(s api.BlueprintSecretSlot) bool { return s.Name == name }); i >= 0 {
			if binding.EnvName != harnessSlots[i].EnvName {
				return fmt.Errorf(
					"secret %q: binding envName %s differs from the harness destination %s; a shared blueprint fixes secret destinations",
					name,
					binding.EnvName,
					harnessSlots[i].EnvName,
				)
			}
			fill(harnessSlots[i], binding, isDefault)
			continue
		}
		slot := api.BlueprintSecretSlot{Name: name, Mode: api.BlueprintSecretModeEnv, EnvName: binding.EnvName}
		plan.extraSecrets = append(plan.extraSecrets, slot)
		fill(slot, binding, isDefault)
	}
	// The slots of the enabled components.
	for _, c := range lib {
		if !slices.Contains(plan.components, c.Metadata.Name) {
			continue
		}
		for _, slot := range c.Spec.Secrets {
			if bound[slot.Name] {
				continue
			}
			rendered := componentSlot(slot)
			chosen, ok, err := identityChoice(plan, c)
			if err != nil {
				return err
			}
			if ok {
				fill(rendered, config.SecretBinding{Name: slot.Name, SecretRef: chosen, EnvName: slot.EnvName}, false)
				continue
			}
			if nb, set := named[slot.Name]; set {
				fill(rendered, nb.binding, nb.isDefault)
				continue
			}
			binding, isDefault, err := secrets.component(slot)
			if err != nil {
				return err
			}
			fill(rendered, binding, isDefault)
		}
	}
	// Keep the library's order so a Config lists components deterministically.
	ordered := make([]string, 0, len(plan.components))
	for _, c := range lib {
		if slices.Contains(plan.components, c.Metadata.Name) {
			ordered = append(ordered, c.Metadata.Name)
		}
	}
	plan.components = ordered
	return nil
}

type namedBinding struct {
	binding   config.SecretBinding
	isDefault bool
}

// componentSlot renders a library secret slot as the blueprint slot it
// becomes: an env variable, or a mounted file for a file-delivered connector.
func componentSlot(slot component.Secret) api.BlueprintSecretSlot {
	out := api.BlueprintSecretSlot{Name: slot.Name, EnvName: slot.EnvName, Required: slot.Required}
	if slot.MountPath != "" {
		out.Mode, out.MountPath, out.EnvName = api.BlueprintSecretModeFile, slot.MountPath, ""
	}
	return out
}

func enableKind(lib []component.Component, name, kind, field string, enable func(string) error) error {
	i := slices.IndexFunc(lib, func(c component.Component) bool { return c.Metadata.Name == name })
	if i < 0 || lib[i].Spec.Kind != kind {
		return fmt.Errorf("role %s: unknown %s component %q", field, kind, name)
	}
	return enable(name)
}

// aliasSlot finds the component secret slot a role secret name selects: the
// slot of that name, or one that lists it as a transition-era alias.
func aliasSlot(lib []component.Component, name string) (component.Component, component.Secret, bool) {
	for _, c := range lib {
		for _, slot := range c.Spec.Secrets {
			if slot.Name == name || slices.Contains(slot.Aliases, name) {
				return c, slot, true
			}
		}
	}
	return component.Component{}, component.Secret{}, false
}

// cellEnvironment are the cell environment names the shared blueprint or the
// runtime sets; a role parameter, exported under its own name, must not
// replace them.
//
//nolint:gochecknoglobals // package-level membership list, read-only.
var cellEnvironment = []string{
	"PATH", "HOME", "IS_SANDBOX", "KUKEON_HOST", "GIT_SSH_COMMAND", "GIT_CONFIG_SYSTEM",
}

// planParameters records the role-declared parameters the shared blueprint
// does not already declare, and checks each has a value: the operator's, or
// the default the role declares for it.
func planParameters(plan *pairPlan, cfg *config.TeamsConfig) error {
	known := map[string]bool{"PROMPT": true, "AGENT_NAME": true}
	for name := range composed {
		known[name] = true
	}
	for _, p := range plan.definition.Spec.Parameters {
		known[p.Name] = true
	}
	for _, mode := range plan.definition.Spec.Modes {
		for _, p := range mode.Parameters {
			known[p.Name] = true
		}
	}
	for _, param := range plan.role.Spec.Needs.Params {
		name := param.Name
		if !parameterName.MatchString(name) {
			return fmt.Errorf("role needs.params: invalid parameter name %q", name)
		}
		if slices.Contains(cellEnvironment, name) {
			return fmt.Errorf("role needs.params: %q is reserved in the cell environment", name)
		}
		if known[name] {
			if param.Default != nil {
				return fmt.Errorf("role needs.params: %q is declared by the blueprint; a role cannot default it", name)
			}
			continue
		}
		if param.Default != nil {
			if _, dup := plan.paramDefaults[name]; dup {
				return fmt.Errorf("role needs.params: duplicate parameter %q", name)
			}
			if plan.paramDefaults == nil {
				plan.paramDefaults = map[string]string{}
			}
			plan.paramDefaults[name] = *param.Default
		} else if _, supplied := cfg.Spec.Parameters[name]; !supplied && name != "ROLE" {
			return fmt.Errorf("spec.parameters: missing required parameter %q", name)
		}
		if !slices.Contains(plan.extraParams, name) {
			plan.extraParams = append(plan.extraParams, name)
		}
	}
	return nil
}

// bind writes the CellConfig of one pair: the answers to the shared
// blueprint's form.
func bind(
	plan *pairPlan, bp *api.CellBlueprintDoc, cfg *config.TeamsConfig, team *config.Team, opts Options,
) (Pair, error) {
	name := plan.configName()
	cc := &api.CellConfigDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig,
		Metadata: api.CellConfigMetadata{
			Name: name, Realm: opts.Scope.Realm, Space: opts.Scope.Space, Stack: opts.Scope.Stack,
			Labels: map[string]string{api.LabelTeam: plan.team, api.LabelRole: plan.selection.Name},
		},
		Spec: api.CellConfigSpec{
			Blueprint:  api.CellConfigBlueprintRef{Name: bp.Metadata.Name, Realm: bp.Metadata.Realm},
			Values:     map[string]string{},
			Repos:      map[string]api.CellConfigRepoFill{},
			Secrets:    map[string]api.CellConfigSecretFill{},
			Components: slices.Clone(plan.components),
		},
	}
	pair := Pair{Role: plan.selection.Name, Harness: plan.hsel.Name, Image: plan.image, Blueprint: bp, Config: cc}

	// Capabilities the role needs beyond what the blueprint already requires.
	for _, need := range plan.needs {
		if !slices.Contains(bp.Spec.Requires, need) && need != plan.hsel.Name {
			cc.Spec.Requires = append(cc.Spec.Requires, need)
		}
	}
	if err := bindValues(plan, bp, cfg, cc); err != nil {
		return Pair{}, err
	}
	if err := bindRepos(plan, cc, team); err != nil {
		return Pair{}, err
	}
	// The identity the team chose for git goes into the Config (issue #1548).
	if plan.git != nil {
		git := *plan.git
		cc.Spec.Git = &git
	}
	for _, fill := range plan.fills {
		ref := fill.binding.SecretRef
		// A default is not a fill: the Config inherits it from the scope
		// defaults when a cell is created.
		if !fill.isDefault {
			cc.Spec.Secrets[fill.slot.Name] = api.CellConfigSecretFill{
				SecretRef: &api.ContainerSecretRef{
					Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
				},
			}
		}
		var source *config.SecretSource
		if fill.binding.Source != nil {
			copySource := *fill.binding.Source
			source = &copySource
		}
		pair.Secrets = append(pair.Secrets, SecretRequirement{
			Identifier: fill.slot.Name, Container: workContainer, Destination: fill.slot,
			Reference: ref, Source: source, Default: fill.isDefault,
		})
	}
	return pair, nil
}

func bindValues(plan *pairPlan, bp *api.CellBlueprintDoc, cfg *config.TeamsConfig, cc *api.CellConfigDoc) error {
	settings := plan.settings
	root := plan.profile.Root
	sandbox, approval := settings.Sandbox, settings.Approval
	if sandbox == "" || root {
		sandbox = "danger-full-access"
	}
	if approval == "" || root {
		approval = "never"
	}
	initial := plan.selection.InitialPath
	if initial == "" {
		initial = home
	}
	initial = strings.Replace(initial, "$HOME", home, 1)
	if initial != "/" && !config.SafePath(initial) {
		return errors.New("INITIAL_PATH: require absolute safe launch directory")
	}
	if !config.SafePath(plan.roleSource.ClonePath) {
		return errors.New("ROLE_SOURCE_DIR: require absolute safe checkout path")
	}
	facts := map[string]string{
		"IMAGE":              plan.image.Image,
		"ROLE_SOURCE_DIR":    plan.roleSource.ClonePath,
		"ROLE_PATH":          plan.selection.Path,
		"INITIAL_PATH":       initial,
		"HARNESS_SETTINGS":   settings.Settings,
		"ROLE_NAME":          plan.role.Metadata.Name,
		"SANDBOX":            sandbox,
		"APPROVAL":           approval,
		"PERMISSIONS":        settings.Permissions,
		"COMMAND":            settings.Command,
		"AGENT_NAME":         cc.Metadata.Name,
		"STATE_VOLUME":       plan.stateVolume(),
		"STATE_VOLUME_REALM": cc.Metadata.Realm,
		"STATE_VOLUME_SPACE": cc.Metadata.Space,
		"STATE_VOLUME_STACK": cc.Metadata.Stack,
	}
	if project, ok := runtimeSource(plan.sources, projectSlot); ok {
		facts["PROJECT_DIR"] = project.ClonePath
	}
	// ROLE_SOURCE_REPO is a fact for role defaults to reference, not a
	// blueprint parameter.
	defaultFacts := maps.Clone(facts)
	defaultFacts[roleSourceRepoFact] = sourceRepo(plan.roleSource.URL)
	defaults, err := roleDefaults(plan, defaultFacts, cfg.Spec.Parameters)
	if err != nil {
		return err
	}
	declared := allParameters(bp)
	for name, param := range declared {
		value, ok := facts[name]
		if def, set := defaults[name]; set {
			value, ok = def, true
		}
		if override, set := cfg.Spec.Parameters[name]; set && !composed[name] {
			value, ok = override, true
		}
		if !ok && name == "ROLE" {
			value, ok = plan.selection.Path, true
		}
		if !ok {
			continue
		}
		// An empty fact leaves the blueprint default in charge (e.g. a role
		// with no OpenCode permissions setting).
		if value == "" && param.Default != nil {
			continue
		}
		cc.Spec.Values[name] = value
	}
	for _, name := range plan.role.Spec.Needs.ParamNames() {
		if _, ok := declared[name]; !ok {
			return fmt.Errorf("template did not declare required parameter %q", name)
		}
	}
	return nil
}

// stateVolume names the Volume holding the pair's harness state: the pair's
// CellConfig name plus `-state`. The pair config, its task configs and the
// sweep cell share it, so one harness login covers every cell of the role ×
// harness. Compose rejects two pairs whose names join to the same string, so
// two pairs never share a state volume by accident.
func (p *pairPlan) stateVolume() string {
	return p.configName() + stateVolumeSuffix
}

// bindRepos fills the blueprint's repo slots from the team's runtime sources:
// the role's own source into role-source, the project into project, and any
// other cloned source into the slot named after it.
func bindRepos(plan *pairPlan, cc *api.CellConfigDoc, team *config.Team) error {
	for _, source := range plan.sources {
		slot := source.Name
		if source.Name == plan.roleSource.Name {
			slot = roleSourceSlot
		}
		ref := source.Tag
		if source.Commit != "" {
			ref = source.Commit
		}
		// Add's project declaration distinguishes a tag from a same-named
		// branch all the way through the runtime checkout contract.
		if source.Name == projectSlot && team.ProjectSource != nil && team.ProjectSource.Tag != "" {
			ref = "refs/tags/" + team.ProjectSource.Tag
		}
		url := source.URL
		if strings.HasPrefix(url, "github.com/") {
			url = "https://" + url
		}
		cc.Spec.Repos[slot] = api.CellConfigRepoFill{
			URL: url, Branch: source.Branch, Ref: ref, Git: repoGit(source.Git),
		}
	}
	if _, ok := cc.Spec.Repos[roleSourceSlot]; !ok {
		return errors.New("sources: role source is not cloned at runtime")
	}
	return nil
}

func identity(in *config.Identity) *api.GitIdentity {
	if in == nil {
		return nil
	}
	return &api.GitIdentity{Name: in.Name, Email: in.Email}
}

func secretRef(in config.SecretRef) *api.ContainerSecretRef {
	return &api.ContainerSecretRef{Name: in.Name, Realm: in.Realm, Space: in.Space, Stack: in.Stack, Cell: in.Cell}
}

func signing(in *config.GitSigning) *api.GitSigning {
	if in == nil {
		return nil
	}
	out := &api.GitSigning{SecretRef: secretRef(in.SecretRef)}
	if in.AllowedSignersRef != nil {
		out.AllowedSignersRef = secretRef(*in.AllowedSignersRef)
	}
	return out
}

// repoGit renders the identity one source states for itself into its repo
// fill. Unset fields fall back to the cell's default identity at run time.
func repoGit(in *config.SourceGit) *api.RepoGit {
	if in == nil {
		return nil
	}
	out := &api.RepoGit{
		Author: identity(in.Author), Committer: identity(in.Committer),
		Signing: signing(in.Signing), Sign: slices.Clone(in.Sign),
	}
	if c := in.Credential; c != nil {
		out.Credential = &api.GitCredential{
			SecretRef: secretRef(c.SecretRef), User: c.User, Port: c.Port, HostKeyPolicy: c.HostKeyPolicy,
		}
	}
	return out
}

// sshSources names every runtime-cloned source that clones over SSH and
// states no credential of its own: its cells need a git identity with a key
// for the host (issue #1591).
func sshSources(sources []config.Source) []string {
	var names []string
	for _, source := range sources {
		if source.ClonePath == "" || (source.Git != nil && source.Git.Credential != nil) {
			continue
		}
		url := source.URL
		if strings.HasPrefix(url, "github.com/") {
			url = "https://" + url
		}
		if gitcred.ParseRemote(url).Transport == gitcred.TransportSSH {
			names = append(names, source.Name)
		}
	}
	return names
}

func envName(value string) string { name, _, _ := strings.Cut(value, "="); return name }
