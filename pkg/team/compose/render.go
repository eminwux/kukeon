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

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose/renderers"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/harness"
	"gopkg.in/yaml.v3"
)

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
		if !slices.Contains([]string{"ssh", "kukeond-socket", "kuke-binary", "kukeon-install"}, mount) {
			return fmt.Errorf("unsupported mount selector %q", mount)
		}
	}
	return nil
}

func checkSettings(h string, s model.RoleHarness) error {
	switch h {
	case "codex":
		if s.Sandbox != "" &&
			!slices.Contains([]string{"read-only", "workspace-write", "danger-full-access"}, s.Sandbox) {
			return errors.New("unsupported Codex sandbox setting")
		}
		if s.Approval != "" &&
			!slices.Contains([]string{"untrusted", "on-failure", "on-request", "never"}, s.Approval) {
			return errors.New("unsupported Codex approval setting")
		}
	case "claude":
		if s.Settings == "" {
			return errors.New("claude requires a role-relative settings path")
		}
	case "opencode":
		if s.Permissions != "" && !slices.Contains([]string{"ask", "skip"}, s.Permissions) {
			return errors.New("unsupported OpenCode permissions setting")
		}
	}
	return nil
}

// rendererInput is contract revision 1 for the bundled kukeon/v1beta1 set.
// Lists and mounts remain structured; scripts are opaque shell source.
type rendererInput struct {
	Realm, Space, Stack, Image, Harness, Shell, Prompt string
	RoleName, ProjectName, HostHome                    string
	ProjectBound                                       bool
	Root                                               bool
	Repos, MountSelectors, Secrets                     []string
	Environment                                        []string
	Parameters                                         []api.CellBlueprintParameter
	Mounts                                             []harness.Mount
	StatePath                                          string
	OnInit                                             []harness.Script
}

func render(h *harness.Harness, role *model.Role, selection config.Role,
	repo config.Source, image harness.CatalogImage, needs []string, sources []config.Source, opts Options,
) (*api.CellBlueprintDoc, error) {
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
	settings := role.Spec.Harnesses[h.Metadata.Name]
	secretNames := union(role.Spec.Needs.Secrets, settings.Secrets)
	sourceRepos := union(role.Spec.Needs.Repos, []string{selection.Source})
	for _, source := range sources {
		if source.ClonePath != "" {
			sourceRepos = union(sourceRepos, []string{source.Name})
		}
	}
	for _, name := range sourceRepos {
		if _, ok := runtimeSource(sources, name); !ok {
			return nil, fmt.Errorf("sources: missing clonePath for required repository %q", name)
		}
	}
	projectName := ""
	projectBound := slices.Contains(role.Spec.Needs.Repos, "project")
	if projectBound {
		r, _ := runtimeSource(sources, "project")
		projectName = path.Base(r.ClonePath)
	}
	for _, mount := range role.Spec.Needs.Mounts {
		if mount == "ssh" && !config.SafePath(opts.HomeDir) {
			return nil, errors.New("HomeDir: explicit absolute path required by ssh mount")
		}
	}
	if !fs.ValidPath(selection.Path) || !config.SafePath("/"+selection.Path) {
		return nil, errors.New("role path: require a safe source-relative path")
	}
	environment, parameters, mounts, statePath, err := startupInputs(h, role, selection, repo, needs, opts)
	if err != nil {
		return nil, err
	}
	ctx := rendererInput{
		Realm: opts.Scope.Realm, Space: opts.Scope.Space, Stack: opts.Scope.Stack, Image: image.Image,
		Harness: h.Metadata.Name, Shell: h.Spec.Shell, Prompt: h.Metadata.Name + "> ",
		RoleName: role.Metadata.Name, ProjectName: projectName, ProjectBound: projectBound, HostHome: opts.HomeDir,
		Root: slices.Contains(needs, "root"), Repos: sourceRepos, MountSelectors: role.Spec.Needs.Mounts,
		Secrets: secretNames, Environment: environment, Parameters: parameters, Mounts: mounts, StatePath: statePath, OnInit: h.Spec.OnInit,
	}
	var output bytes.Buffer
	if err := tpl.ExecuteTemplate(&output, path.Base(filename), ctx); err != nil {
		return nil, fmt.Errorf("execute template %s: %w", filename, err)
	}
	var bp api.CellBlueprintDoc
	if err := decode(output.Bytes(), filename, &bp); err != nil {
		return nil, err
	}
	if bp.APIVersion != api.APIVersionV1Beta1 || bp.Kind != api.KindCellBlueprint || len(bp.Spec.Cell.Containers) == 0 {
		return nil, errors.New("template must render a v1beta1 CellBlueprint with containers")
	}
	return &bp, nil
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

func startupInputs(h *harness.Harness, role *model.Role, selection config.Role, repo config.Source,
	needs []string, opts Options,
) ([]string, []api.CellBlueprintParameter, []harness.Mount, string, error) {
	const home = "/home/user"
	settings := role.Spec.Harnesses[h.Metadata.Name]
	sandbox, approval := settings.Sandbox, settings.Approval
	if sandbox == "" || slices.Contains(needs, "root") {
		sandbox = "danger-full-access"
	}
	if approval == "" || slices.Contains(needs, "root") {
		approval = "never"
	}
	initial := selection.InitialPath
	if initial == "" {
		initial = home
	}
	initial = strings.Replace(initial, "$HOME", home, 1)
	if initial != "/" && !config.SafePath(initial) {
		return nil, nil, nil, "", errors.New("INITIAL_PATH: require absolute safe launch directory")
	}
	if !config.SafePath(repo.ClonePath) {
		return nil, nil, nil, "", errors.New("ROLE_SOURCE_DIR: require absolute safe checkout path")
	}
	values := map[string]string{
		"HOME": home, "ROLE_SOURCE_DIR": repo.ClonePath, "ROLE_PATH": selection.Path, "INITIAL_PATH": initial,
		"ROLE_NAME": role.Metadata.Name, "SETTINGS": settings.Settings, "SANDBOX": sandbox, "APPROVAL": approval,
		"PERMISSIONS": settings.Permissions, "TEAM_ROOT": opts.TeamDir,
	}
	parameters, err := startupParameters(h, role, values)
	if err != nil {
		return nil, nil, nil, "", err
	}
	resolve := inputResolver(values)
	env := []string{
		"ROLE_SOURCE_DIR=" + repo.ClonePath, "ROLE_PATH=" + selection.Path, "INITIAL_PATH=" + initial,
		"HARNESS_SETTINGS=" + settings.Settings, "PROMPT=${PROMPT}", "AGENT_NAME=${AGENT_NAME}",
	}
	for _, key := range slices.Sorted(maps.Keys(h.Spec.Environment)) {
		value, resolveErr := resolve(h.Spec.Environment[key])
		if resolveErr != nil {
			return nil, nil, nil, "", resolveErr
		}
		env = append(env, key+"="+value)
	}
	if slices.Contains(role.Spec.Needs.Repos, "project") {
		env = append(env, "AGENTS_REPO=")
	}
	ssh := "ssh"
	if slices.Contains(needs, "root") {
		env = append(env, "IS_SANDBOX=1")
		ssh += ` -F "$(if [ -f /home/user/.ssh/config ]; then printf %s /home/user/.ssh/config; else printf %s /dev/null; fi)"`
	}
	env = append(
		env,
		"GIT_SSH_COMMAND="+ssh+" -i /home/user/.ssh/id_ed25519 -o StrictHostKeyChecking=accept-new -o UserKnownHostsFile=/home/user/.ssh/known_hosts",
	)
	statePath, err := resolve(h.Spec.StatePath)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if statePath != "" && !config.SafePath(statePath) {
		return nil, nil, nil, "", errors.New("spec.statePath: require safe absolute or $HOME path")
	}
	mounts := slices.Clone(h.Spec.Mounts)
	for i := range mounts {
		mounts[i].Source, err = resolve(mounts[i].Source)
		if err != nil {
			return nil, nil, nil, "", err
		}
		mounts[i].Target, err = resolve(mounts[i].Target)
		if err != nil {
			return nil, nil, nil, "", err
		}
		if !config.SafePath(mounts[i].Source) || !config.SafePath(mounts[i].Target) {
			return nil, nil, nil, "", errors.New("spec.mounts: require safe absolute source/target paths")
		}
	}
	return env, parameters, mounts, statePath, nil
}

func startupParameters(
	h *harness.Harness,
	role *model.Role,
	values map[string]string,
) ([]api.CellBlueprintParameter, error) {
	parameters := []api.CellBlueprintParameter{
		{Name: "PROMPT", Default: new(string)}, {Name: "AGENT_NAME", Default: new(string)},
	}
	parameters = append(parameters, h.Spec.Parameters...)
	for _, name := range role.Spec.Needs.Params {
		if !parameterName.MatchString(name) {
			return nil, fmt.Errorf("role needs.params: invalid parameter name %q", name)
		}
		if !slices.ContainsFunc(parameters, func(p api.CellBlueprintParameter) bool { return p.Name == name }) {
			parameters = append(parameters, api.CellBlueprintParameter{Name: name, Required: true})
		}
	}
	for _, p := range parameters {
		for _, stage := range h.Spec.OnInit {
			if strings.Contains(stage.Script, "${"+p.Name+"}") {
				return nil, fmt.Errorf(
					"spec.onInit: ${%s} would interpolate a blueprint parameter into shell source; use $%s and pass values through environment",
					p.Name,
					p.Name,
				)
			}
		}
		if _, reserved := values[p.Name]; reserved {
			return nil, fmt.Errorf("parameter %s conflicts with startup input", p.Name)
		}
		values[p.Name] = "${" + p.Name + "}"
	}
	return parameters, nil
}

func inputResolver(values map[string]string) func(string) (string, error) {
	const home = "/home/user"
	return func(raw string) (string, error) {
		var invalid bool
		result := inputToken.ReplaceAllStringFunc(raw, func(token string) string {
			name := token[2 : len(token)-1]
			value, ok := values[name]
			if !ok {
				invalid = true
			}
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
