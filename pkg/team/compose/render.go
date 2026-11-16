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
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/compose/renderers"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"gopkg.in/yaml.v3"
)

const (
	home = "/home/user"
	// roleSourceSlot and projectSlot are the two repo slots every shared
	// blueprint declares; a Config fills them with URLs and revisions.
	roleSourceSlot = "role-source"
	projectSlot    = "project"
	workContainer  = "work"

	groupRole    = "role"
	groupHarness = "harness"
	groupImage   = "image"
	groupState   = "state"
)

// composed names the parameters whose value composition derives from the role
// and source selections; an operator's spec.parameters cannot set them.
//
//nolint:gochecknoglobals // package-level membership set, read-only.
var composed = map[string]bool{
	"IMAGE": true, "ROLE_SOURCE_DIR": true, "ROLE_PATH": true, "INITIAL_PATH": true, "PROJECT_DIR": true,
	"HARNESS_SETTINGS": true, "STATE_VOLUME": true, "ROLE_NAME": true, "SANDBOX": true, "APPROVAL": true,
	"PERMISSIONS": true,
}

func checkAssets(source fs.FS, rolePath string, role *model.Role, settings model.RoleHarness, harness string) error {
	for _, name := range append([]string{path.Join(rolePath, "AGENTS.md")}, role.Spec.Skills...) {
		if !fs.ValidPath(name) {
			return errors.New("role asset: require source-relative paths")
		}
		file := name
		if name != path.Join(rolePath, "AGENTS.md") {
			file = path.Join(name, "SKILL.md")
		}
		if _, err := fs.Stat(source, file); err != nil {
			return fmt.Errorf("missing role asset %s: %w", file, err)
		}
	}
	if settings.Settings != "" {
		if !fs.ValidPath(settings.Settings) || !config.SafePath("/"+settings.Settings) {
			return errors.New("harness settings: require a safe role-relative path")
		}
		if _, err := fs.Stat(source, path.Join(rolePath, settings.Settings)); err != nil {
			return errors.New("missing selected role harness settings file")
		}
	}
	if harness == "opencode" {
		raw, readErr := fs.ReadFile(source, path.Join(rolePath, "config/opencode.json"))
		if readErr != nil || !json.Valid(raw) {
			return errors.New("opencode requires a valid role config/opencode.json")
		}
	}
	for _, mount := range role.Spec.Needs.Mounts {
		// `ssh` stays accepted so roles written before stored git credentials
		// still compose; it selects no mount.
		if mount != "ssh" && !slices.Contains(hostMounts, mount) {
			return fmt.Errorf("unsupported mount selector %q", mount)
		}
	}
	return nil
}

// The values each role-specific harness setting accepts.
//
//nolint:gochecknoglobals // package-level enumerations, read-only.
var (
	codexSandboxes  = []string{"read-only", "workspace-write", "danger-full-access"}
	codexApprovals  = []string{"untrusted", "on-failure", "on-request", "never"}
	opencodePermits = []string{"ask", "skip"}
)

func checkSettings(h string, s model.RoleHarness) error {
	switch h {
	case "codex":
		if s.Sandbox != "" && !slices.Contains(codexSandboxes, s.Sandbox) {
			return errors.New("unsupported Codex sandbox setting")
		}
		if s.Approval != "" && !slices.Contains(codexApprovals, s.Approval) {
			return errors.New("unsupported Codex approval setting")
		}
	case "claude":
		if s.Settings == "" {
			return errors.New("claude requires a role-relative settings path")
		}
	case "opencode":
		if s.Permissions != "" && !slices.Contains(opencodePermits, s.Permissions) {
			return errors.New("unsupported OpenCode permissions setting")
		}
	}
	return nil
}

// rendererInput is contract revision 2 for the bundled kukeon/v1beta1 set: a
// shared blueprint, free of role facts. Lists and mounts remain structured;
// scripts are opaque shell source.
type rendererInput struct {
	Name, Realm, Shell, Prompt string
	Root                       bool
	Requires, MountSelectors   []string
	Environment                []string
	Parameters                 []api.CellBlueprintParameter
	Components                 []api.BlueprintComponent
	Secrets                    []api.BlueprintSecretSlot
	Mounts                     []harness.Mount
	StatePath                  string
	ExtraRepos                 []extraRepo
	OnInit                     []harness.Script
}

type extraRepo struct{ Name, Target string }

// render builds the shared blueprint one group of pairs uses. Everything in it
// comes from the harness definition, the security profile and the component
// library; the only role-derived content is the union of leftovers no shared
// declaration covers (see group).
func render(name string, g *group, lib []component.Component, opts Options) (*api.CellBlueprintDoc, error) {
	first := g.plans[0]
	h := first.definition
	if err := h.Validate(); err != nil {
		return nil, err
	}
	const filename = "blueprint.tmpl.yaml"
	funcs := template.FuncMap{
		"upper":      strings.ToUpper,
		"replace":    func(old, newValue, value string) string { return strings.ReplaceAll(value, old, newValue) },
		"quote":      strconv.Quote,
		"yamlIndent": yamlIndent,
	}
	tpl, err := template.New(filename).
		Funcs(funcs).
		Option("missingkey=error").
		ParseFS(renderers.Templates, "kukeon/v1beta1/*.tmpl.yaml")
	if err != nil {
		return nil, fmt.Errorf("bundled renderer: %w", err)
	}
	ctx, err := blueprintInputs(name, g, lib, opts)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err = tpl.ExecuteTemplate(&output, path.Base(filename), ctx); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", filename, err)
	}
	var bp api.CellBlueprintDoc
	if err = decode(output.Bytes(), filename, &bp); err != nil {
		return nil, err
	}
	if bp.APIVersion != api.APIVersionV1Beta1 || bp.Kind != api.KindCellBlueprint || len(bp.Spec.Cell.Containers) == 0 {
		return nil, errors.New("template must render a v1beta1 CellBlueprint with containers")
	}
	// Catch a component/template collision here, where the message can name
	// the harness or component document, rather than at daemon apply.
	if err = cellblueprint.ValidateDeclaration(bp); err != nil {
		return nil, err
	}
	if _, err = cellblueprint.Flatten(bp, cellblueprint.FlattenOptions{All: true}); err != nil {
		return nil, err
	}
	if err = checkDestinations(bp); err != nil {
		return nil, err
	}
	digest, err := contentDigest(&bp)
	if err != nil {
		return nil, err
	}
	bp.Metadata.Labels = map[string]string{api.LabelShared: "true", api.LabelContentDigest: digest}
	return &bp, nil
}

// checkDestinations refuses a blueprint in which two declarations write the
// same environment variable. The validation view tolerates repeats (alternative
// modes legitimately repeat each other), so the cell template is checked here
// and every mode is flattened strictly with every optional component enabled:
// a collision would otherwise surface only when a cell is created in the one
// mode, or with the one connector, that collides.
func checkDestinations(bp api.CellBlueprintDoc) error {
	for _, c := range bp.Spec.Cell.Containers {
		taken := map[string]bool{}
		for _, entry := range c.Env {
			taken[envName(entry)] = true
		}
		destinations := map[string]bool{}
		for _, slot := range c.Secrets {
			if taken[slot.EnvName] {
				return fmt.Errorf(
					"secret %q: destination %s conflicts with template environment",
					slot.Name,
					slot.EnvName,
				)
			}
			if destinations[slot.EnvName] {
				return fmt.Errorf(
					"secret %q: conflicting secret environment destinations (%s)",
					slot.Name,
					slot.EnvName,
				)
			}
			destinations[slot.EnvName] = true
		}
	}
	var optional []string
	for _, c := range bp.Spec.Components {
		if c.Optional {
			optional = append(optional, c.Name)
		}
	}
	modes := cellblueprint.Modes(bp)
	if len(modes) == 0 {
		modes = []string{""}
	}
	for _, mode := range modes {
		opts := cellblueprint.FlattenOptions{Components: optional, Mode: mode}
		if _, err := cellblueprint.Flatten(bp, opts); err != nil {
			return err
		}
	}
	return nil
}

// contentDigest identifies what a shared blueprint renders, independent of the
// name it is stored under.
func contentDigest(bp *api.CellBlueprintDoc) (string, error) {
	raw, err := yaml.Marshal(bp.Spec)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	const digestBytes = 8
	return hex.EncodeToString(sum[:digestBytes]), nil
}

// DigestName is the name a shared blueprint takes when the realm already holds
// a different blueprint under its plain name.
func DigestName(bp *api.CellBlueprintDoc) string {
	return bp.Spec.Prefix + "-" + bp.Metadata.Labels[api.LabelContentDigest]
}

// yamlIndent serializes structural inputs, including opaque shell scripts, rather
// than constructing YAML or shell source by concatenating user-supplied values.
func yamlIndent(indent int, value any) (string, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return "", err
	}
	prefix := strings.Repeat(" ", indent)
	return prefix + strings.ReplaceAll(strings.TrimSuffix(string(raw), "\n"), "\n", "\n"+prefix), nil
}

var inputToken = regexp.MustCompile(`\$\{([^}]*)\}`)

var parameterName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

func text(value string) *string { return &value }

// blueprintInputs assembles the renderer input of one shared blueprint from
// the harness definition, the security profile and the component library.
//
//nolint:gocyclo,cyclop,funlen // one linear assembly; each step feeds the next.
func blueprintInputs(name string, g *group, lib []component.Component, opts Options) (rendererInput, error) {
	first := g.plans[0]
	h := first.definition
	prof := first.profile
	legacy := len(h.Spec.Modes) == 0

	// Documented harness inputs. A role fact is no longer a literal: it
	// resolves to the blueprint parameter the Config fills.
	values := map[string]string{
		"HOME": home, "ROLE_SOURCE_DIR": "${ROLE_SOURCE_DIR}", "ROLE_PATH": "${ROLE_PATH}",
		"INITIAL_PATH": "${INITIAL_PATH}", "ROLE_NAME": "${ROLE_NAME}", "SETTINGS": "${HARNESS_SETTINGS}",
		"SANDBOX": "${SANDBOX}", "APPROVAL": "${APPROVAL}", "PERMISSIONS": "${PERMISSIONS}",
	}
	used := map[string]bool{}
	resolve := inputResolver(values, used)

	// Blueprint-level parameters: the image and the role inputs.
	image := api.CellBlueprintParameter{
		Name: "IMAGE", Type: api.ParameterTypeImage, Group: groupImage,
		Description: "Container image; it must provide every required capability.",
	}
	var defaultNeeds []string
	if prof.Root {
		defaultNeeds = []string{"root"}
	}
	if fallback, err := SelectImage(first.catalog, h.Metadata.Name, defaultNeeds); err == nil {
		image.Default = text(fallback.Image)
	} else {
		image.Required = true
	}
	parameters := []api.CellBlueprintParameter{
		image,
		{
			Name: "ROLE_SOURCE_DIR", Type: api.ParameterTypePath, Required: true, Group: groupRole,
			Description: "In-cell checkout of the repository the role is read from.",
		},
		{
			Name: "ROLE_PATH", Type: api.ParameterTypePath, Required: true, Group: groupRole,
			Description: "Role directory inside the role source.",
		},
		{
			Name: "INITIAL_PATH", Type: api.ParameterTypePath, Default: text(home), Group: groupRole,
			Description: "Directory the harness starts in.",
		},
		{
			Name: "PROJECT_DIR", Type: api.ParameterTypePath, Default: text(home + "/project"), Group: groupRole,
			Description: "In-cell checkout of the project repository.",
		},
		{
			Name: "HARNESS_SETTINGS", Default: text(""), Group: groupRole,
			Description: "Harness settings file, relative to the role directory.",
		},
		{Name: "AGENT_NAME", Default: text(""), Group: groupRole, Description: "Name the agent reports as."},
		{
			Name: "AGENTS_REPO", Default: text(""), Group: groupRole,
			Description: "Repository the role files feedback against.",
		},
	}
	environment := []string{
		"ROLE_SOURCE_DIR=${ROLE_SOURCE_DIR}", "ROLE_PATH=${ROLE_PATH}", "INITIAL_PATH=${INITIAL_PATH}",
		"HARNESS_SETTINGS=${HARNESS_SETTINGS}", "AGENT_NAME=${AGENT_NAME}", "AGENTS_REPO=${AGENTS_REPO}",
	}
	if slices.Contains(prof.Mounts, "kukeond-socket") {
		// The socket is reached through its directory mount (issue #1483).
		environment = append(environment, "KUKEON_HOST=unix:///run/kukeon/kukeond/kukeond.sock")
	}
	if legacy {
		// A harness without modes still branches on $PROMPT in its one onInit.
		parameters = append(parameters, api.CellBlueprintParameter{
			Name: "PROMPT", Default: text(""), Group: groupHarness,
			Description: "Prompt of an unattended run; empty starts an interactive session.",
		})
		environment = append(environment, "PROMPT=${PROMPT}")
	}
	if prof.Root {
		environment = append(environment, "IS_SANDBOX=1")
	}

	// The harness component: its own parameters, env, secrets and modes.
	comp := api.BlueprintComponent{
		Kind: api.ComponentKindHarness, Name: h.Metadata.Name, Requires: []string{h.Metadata.Name},
		Secrets: slices.Clone(h.Spec.Secrets),
	}
	reserved := maps.Clone(values)
	for _, p := range parameters {
		reserved[p.Name] = ""
	}
	declare := func(list []api.CellBlueprintParameter, scope map[string]string) ([]api.CellBlueprintParameter, error) {
		out := make([]api.CellBlueprintParameter, 0, len(list))
		for _, p := range list {
			if _, taken := reserved[p.Name]; taken {
				return nil, fmt.Errorf("parameter %s conflicts with startup input", p.Name)
			}
			if p.Group == "" {
				p.Group = groupHarness
			}
			scope[p.Name] = "${" + p.Name + "}"
			out = append(out, p)
		}
		return out, nil
	}
	var err error
	if comp.Parameters, err = declare(h.Spec.Parameters, values); err != nil {
		return rendererInput{}, err
	}
	for _, extra := range g.extraParams {
		if _, taken := values[extra]; taken {
			continue
		}
		values[extra] = "${" + extra + "}"
		parameters = append(parameters, api.CellBlueprintParameter{
			Name: extra, Default: text(""), Group: groupRole,
			Description: "Declared by a role's needs.params.",
		})
	}
	if comp.Env, err = resolveEnvironment(h.Spec.Environment, resolve); err != nil {
		return rendererInput{}, err
	}
	var onInit []harness.Script
	if legacy {
		onInit = h.Spec.OnInit
	}
	for _, modeName := range slices.Sorted(maps.Keys(h.Spec.Modes)) {
		mode := h.Spec.Modes[modeName]
		// A mode's parameters are visible only to that mode's environment.
		scope := maps.Clone(values)
		rendered := api.BlueprintComponentMode{
			Description: mode.Description, Secrets: slices.Clone(mode.Secrets),
		}
		if rendered.Parameters, err = declare(mode.Parameters, scope); err != nil {
			return rendererInput{}, fmt.Errorf("spec.modes.%s: %w", modeName, err)
		}
		if rendered.Env, err = resolveEnvironment(mode.Environment, inputResolver(scope, used)); err != nil {
			return rendererInput{}, fmt.Errorf("spec.modes.%s: %w", modeName, err)
		}
		// Scripts read parameters from the environment, never by interpolation,
		// so every mode parameter is exported under its own name.
		for _, p := range rendered.Parameters {
			if _, set := mode.Environment[p.Name]; !set {
				rendered.Env = append(rendered.Env, p.Name+"=${"+p.Name+"}")
			}
		}
		for _, stage := range slices.Concat(h.Spec.OnInit, mode.OnInit) {
			rendered.OnInit = append(rendered.OnInit, api.TtyStage{Script: stage.Script})
		}
		if comp.Modes == nil {
			comp.Modes = map[string]api.BlueprintComponentMode{}
		}
		comp.Modes[modeName] = rendered
	}

	statePath, err := resolve(h.Spec.StatePath)
	if err != nil {
		return rendererInput{}, err
	}
	if statePath != "" {
		if !config.SafePath(statePath) {
			return rendererInput{}, errors.New("spec.statePath: require safe absolute or $HOME path")
		}
		parameters = append(parameters, api.CellBlueprintParameter{
			Name: "STATE_VOLUME", Required: true, Group: groupState,
			Description: "Volume holding the harness state (logins, sessions) across cells.",
		})
	}
	mounts := slices.Clone(h.Spec.Mounts)
	for i := range mounts {
		if mounts[i].Source, err = resolve(mounts[i].Source); err != nil {
			return rendererInput{}, err
		}
		if mounts[i].Target, err = resolve(mounts[i].Target); err != nil {
			return rendererInput{}, err
		}
		if !config.SafePath(mounts[i].Source) || !config.SafePath(mounts[i].Target) {
			return rendererInput{}, errors.New("spec.mounts: require safe absolute source/target paths")
		}
	}
	// Role-specific harness settings become typed parameters, declared only
	// when the harness actually reads them.
	if used["ROLE_NAME"] {
		parameters = append(parameters, api.CellBlueprintParameter{
			Name: "ROLE_NAME", Required: true, Group: groupRole, Description: "Name of the role.",
		})
	}
	for _, setting := range []struct {
		name, description, fallback string
		enum                        []string
	}{
		{"SANDBOX", "Codex sandbox mode.", "danger-full-access", codexSandboxes},
		{"APPROVAL", "Codex approval policy.", "never", codexApprovals},
		{"PERMISSIONS", "OpenCode permission mode.", "ask", opencodePermits},
	} {
		if used[setting.name] {
			comp.Parameters = append(comp.Parameters, api.CellBlueprintParameter{
				Name: setting.name, Type: api.ParameterTypeEnum, Enum: setting.enum,
				Default: text(setting.fallback), Group: groupHarness, Description: setting.description,
			})
		}
	}
	if err = checkScripts(h, parameters, comp); err != nil {
		return rendererInput{}, err
	}

	components := []api.BlueprintComponent{comp}
	for _, c := range lib {
		rendered := api.BlueprintComponent{
			Kind: c.Spec.Kind, Name: c.Metadata.Name, Description: c.Spec.Description, Optional: true,
			Requires: slices.Clone(c.Spec.Requires), Parameters: slices.Clone(c.Spec.Parameters),
		}
		for _, key := range slices.Sorted(maps.Keys(c.Spec.Environment)) {
			rendered.Env = append(rendered.Env, key+"="+c.Spec.Environment[key])
		}
		for _, secret := range c.Spec.Secrets {
			rendered.Secrets = append(rendered.Secrets, api.BlueprintSecretSlot{
				Name: secret.Name, EnvName: secret.EnvName, Required: secret.Required,
			})
		}
		components = append(components, rendered)
	}

	requires := []string{"git"}
	if prof.Root {
		requires = append(requires, "root")
	}
	in := rendererInput{
		Name: name, Realm: opts.Scope.Realm, Shell: h.Spec.Shell, Prompt: h.Metadata.Name + "> ",
		Root: prof.Root, Requires: requires, MountSelectors: prof.Mounts, Environment: environment,
		Parameters: parameters, Components: components, Secrets: g.extraSecrets, Mounts: mounts,
		StatePath: statePath, OnInit: onInit,
	}
	for _, source := range g.extraRepos {
		if !config.ValidName(source.Name) || !config.SafePath(source.ClonePath) {
			return rendererInput{}, fmt.Errorf("sources: %q requires a safe name and clonePath", source.Name)
		}
		in.ExtraRepos = append(in.ExtraRepos, extraRepo{Name: source.Name, Target: source.ClonePath})
	}
	return in, nil
}

func resolveEnvironment(values map[string]string, resolve func(string) (string, error)) ([]string, error) {
	var out []string
	for _, key := range slices.Sorted(maps.Keys(values)) {
		value, err := resolve(values[key])
		if err != nil {
			return nil, err
		}
		out = append(out, key+"="+value)
	}
	return out, nil
}

// checkScripts refuses a script that interpolates a blueprint parameter into
// shell source: kukeon substitutes ${PARAM} in every scalar, so the value
// would be spliced into the script unquoted.
func checkScripts(h *harness.Harness, parameters []api.CellBlueprintParameter, comp api.BlueprintComponent) error {
	names := map[string]bool{}
	for _, p := range slices.Concat(parameters, comp.Parameters) {
		names[p.Name] = true
	}
	scripts := slices.Clone(h.Spec.OnInit)
	for _, mode := range comp.Modes {
		for _, p := range mode.Parameters {
			names[p.Name] = true
		}
	}
	for _, mode := range h.Spec.Modes {
		scripts = append(scripts, mode.OnInit...)
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		for _, stage := range scripts {
			if strings.Contains(stage.Script, "${"+name+"}") {
				return fmt.Errorf(
					"onInit: ${%s} would interpolate a blueprint parameter into shell source; use $%s and pass values through environment",
					name,
					name,
				)
			}
		}
	}
	return nil
}

// inputResolver substitutes the documented ${INPUT} tokens of a harness value
// and records which inputs were read.
func inputResolver(values map[string]string, used map[string]bool) func(string) (string, error) {
	return func(raw string) (string, error) {
		var invalid bool
		result := inputToken.ReplaceAllStringFunc(raw, func(token string) string {
			name := token[2 : len(token)-1]
			value, ok := values[name]
			if !ok {
				invalid = true
			}
			used[name] = true
			return value
		})
		if invalid {
			return "", errors.New(
				"harness renderer input: unsupported environment substitution; use documented inputs or declared parameters",
			)
		}
		return strings.Replace(result, "$HOME", home, 1), nil
	}
}

// allParameters lists every parameter a blueprint can take: its own, its
// components' and its harness modes'.
func allParameters(bp *api.CellBlueprintDoc) map[string]api.CellBlueprintParameter {
	out := map[string]api.CellBlueprintParameter{}
	for _, p := range bp.Spec.Parameters {
		out[p.Name] = p
	}
	for _, c := range bp.Spec.Components {
		for _, p := range c.Parameters {
			out[p.Name] = p
		}
		for _, mode := range c.Modes {
			for _, p := range mode.Parameters {
				out[p.Name] = p
			}
		}
	}
	return out
}
