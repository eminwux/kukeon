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
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/team/gitremote"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// Sources are registered in the realm's TeamDefaults (issue #1618, D8): its
// spec.defaults.sources are the sources every team may use, and its default
// roles and harnesses are what a team that omits a list inherits. Every edit
// here is a read of the TeamDefaults, an edit, and an apply of the result.

// DefaultsChange reports an edit of the TeamDefaults.
type DefaultsChange struct {
	// Action is created, updated or removed.
	Action string
	// Inheriting are the stored teams that inherit from the defaults: a
	// change reaches them only when each is recomposed with
	// kuke team apply NAME.
	Inheriting []string
	// Discovered is what a source add found in the source.
	Discovered *SourceContents
}

// SourceOptions register a source.
type SourceOptions struct {
	Options

	Name, URL, Branch, Tag, Commit string
	// ClonePath is where team cells clone the source. Empty sets
	// /home/user/<name> when the source offers roles, and none otherwise.
	ClonePath string
}

// SourceContents is what a source offers: its roles and harnesses, found by
// walking the fetched tree (D9).
type SourceContents struct {
	Source    config.Source `json:"-"`
	Commit    string        `json:"commit,omitempty"`
	Roles     []SourceRole  `json:"roles"`
	Harnesses []SourceItem  `json:"harnesses"`
}

// SourceRole is one role a source offers.
type SourceRole struct {
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Harnesses []string `json:"harnesses"`
	Tasks     []string `json:"tasks"`
	// Default reports that the TeamDefaults' default roles select it.
	Default bool `json:"default"`
}

// SourceItem is one harness a source offers.
type SourceItem struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Default bool   `json:"default"`
}

// Defaults reads the realm default's TeamDefaults; empty when none is stored.
func Defaults(ctx context.Context, client Client) (*config.TeamDefaults, error) {
	defaults, _, err := readDefaults(ctx, client, config.DefaultRealm)
	return defaults, err
}

// AddSource registers a source (AC13): it checks the URL, discovers the
// default branch when no ref is given, fetches the source once, and adds it
// to the TeamDefaults. An existing name is updated.
func AddSource(ctx context.Context, opts SourceOptions) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	if !config.ValidName(opts.Name) || opts.Name == config.ProjectSourceName {
		return DefaultsChange{}, fmt.Errorf(
			"source add: invalid source name %q (lowercase letters, digits, dots, hyphens; project is reserved)",
			opts.Name)
	}
	if _, err := gitremote.URL(opts.URL); err != nil {
		return DefaultsChange{}, err
	}
	if err := validateRevision(AddOptions{Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit}); err != nil {
		return DefaultsChange{}, err
	}
	if opts.ClonePath != "" && !config.SafePath(opts.ClonePath) {
		return DefaultsChange{}, errors.New("source add: --clone-path must be a clean absolute path")
	}
	source := config.Source{
		Name: opts.Name, URL: opts.URL, Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit,
		ClonePath: opts.ClonePath,
	}
	if source.Branch == "" && source.Tag == "" && source.Commit == "" {
		branch, err := gitremote.DefaultBranch(ctx, opts.URL)
		if err != nil {
			return DefaultsChange{}, err
		}
		source.Branch = branch
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	contents, err := inspect(ctx, opts.Options, source, defaults)
	if err != nil {
		return DefaultsChange{}, err
	}
	if source.ClonePath == "" && len(contents.Roles) > 0 {
		// A role source must be cloned into the cells that run its roles;
		// any other source would be cloned into every cell for nothing.
		source.ClonePath = "/home/user/" + source.Name
	}
	contents.Source = source
	action := "created"
	list := defaults.Spec.Defaults.Sources
	if i := slices.IndexFunc(list, func(s config.Source) bool { return s.Name == source.Name }); i >= 0 {
		list = slices.Clone(list)
		source.Git = list[i].Git
		list[i] = source
		action = "updated"
	} else {
		list = append(slices.Clone(list), source)
	}
	defaults.Spec.Defaults.Sources = list
	change, err := writeDefaults(ctx, opts.Client, defaults)
	if err != nil {
		return change, err
	}
	change.Action, change.Discovered = action, contents
	return change, nil
}

// ShowSource fetches a registered source and lists the roles and harnesses it
// offers, marking the defaults (AC13).
func ShowSource(ctx context.Context, opts Options, name string) (*SourceContents, error) {
	if err := opts.check(); err != nil {
		return nil, err
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(defaults.Spec.Defaults.Sources, func(s config.Source) bool { return s.Name == name })
	if i < 0 {
		return nil, fmt.Errorf("source %s is not registered; add it with kuke team source add %s --url URL", name, name)
	}
	source := defaults.Spec.Defaults.Sources[i]
	contents, err := inspect(ctx, opts, source, defaults)
	if err != nil {
		return nil, err
	}
	contents.Source = source
	return contents, nil
}

// RemoveSource unregisters a source. It is refused while a default role or
// harness, or a stored team that inherits the sources, references it; the
// error names them. Nothing is fetched.
func RemoveSource(ctx context.Context, opts Options, name string) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	list := defaults.Spec.Defaults.Sources
	i := slices.IndexFunc(list, func(s config.Source) bool { return s.Name == name })
	if i < 0 {
		return DefaultsChange{}, fmt.Errorf("source %s is not registered", name)
	}
	var users []string
	for _, role := range defaults.Spec.Defaults.Roles {
		if role.Source == name {
			users = append(users, "default role "+role.Name)
		}
	}
	for _, h := range defaults.Spec.Defaults.Harnesses {
		if h.Source == name {
			users = append(users, "default harness "+h.Name)
		}
	}
	teams, err := storedTeams(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	for _, team := range teams {
		if team.Spec.Sources != nil {
			continue // The team declares its own sources.
		}
		effective, resolveErr := config.Resolve(defaults, team)
		if resolveErr != nil {
			users = append(users, "team "+team.Metadata.Name+" (does not resolve: "+resolveErr.Error()+")")
			continue
		}
		if slices.ContainsFunc(effective.Roles, func(r config.Role) bool { return r.Source == name }) ||
			slices.ContainsFunc(effective.Harnesses, func(h config.Harness) bool { return h.Source == name }) {
			users = append(users, "team "+team.Metadata.Name)
		}
	}
	if len(users) > 0 {
		return DefaultsChange{}, fmt.Errorf("source remove: source %s is referenced by %s",
			name, strings.Join(users, ", "))
	}
	defaults.Spec.Defaults.Sources = slices.Delete(slices.Clone(list), i, i+1)
	change, err := writeDefaults(ctx, opts.Client, defaults)
	change.Action = "removed"
	return change, err
}

// SetDefaults replaces the default role list, the default harness list, or
// both (AC14); a nil list is left as it is. Each entry is a current
// default's name or [NAME=]SOURCE:PATH in a registered source.
func SetDefaults(ctx context.Context, opts Options, roles, harnesses []string) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	if roles == nil && harnesses == nil {
		return DefaultsChange{}, errors.New("defaults set: name --role or --harness")
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	scratch := &config.Team{}
	if err = applyOverrides(scratch, defaults, roles, harnesses); err != nil {
		return DefaultsChange{}, err
	}
	if roles != nil {
		defaults.Spec.Defaults.Roles = scratch.Spec.Roles
	}
	if harnesses != nil {
		defaults.Spec.Defaults.Harnesses = scratch.Spec.Harnesses
	}
	change, err := writeDefaults(ctx, opts.Client, defaults)
	change.Action = "updated"
	return change, err
}

// InheritingTeams names the stored teams that inherit at least one list from
// the TeamDefaults, sorted.
func InheritingTeams(ctx context.Context, client Client) ([]string, error) {
	teams, err := storedTeams(ctx, client)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, team := range teams {
		s := team.Spec
		if s.Sources == nil || s.Roles == nil || s.Harnesses == nil || s.Connectors == nil {
			out = append(out, team.Metadata.Name)
		}
	}
	sort.Strings(out)
	return out, nil
}

func storedTeams(ctx context.Context, client Client) ([]*config.Team, error) {
	listed, err := client.ListTeams(ctx, config.DefaultRealm)
	if err != nil {
		return nil, requestError("lookup", "teams", err)
	}
	out := make([]*config.Team, 0, len(listed))
	for _, got := range listed {
		team, decodeErr := config.DecodeTeam(bytes.NewReader(got.Document))
		if decodeErr != nil {
			return nil, fmt.Errorf("stored team %s: %w", got.Summary.Name, decodeErr)
		}
		out = append(out, team)
	}
	return out, nil
}

// writeDefaults validates and applies a TeamDefaults document.
func writeDefaults(ctx context.Context, client Client, defaults *config.TeamDefaults) (DefaultsChange, error) {
	if err := config.Validate(defaults); err != nil {
		return DefaultsChange{}, err
	}
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		return DefaultsChange{}, fmt.Errorf("encode TeamDefaults: %w", err)
	}
	response, err := client.ApplyDocuments(ctx, raw)
	if err != nil {
		return DefaultsChange{}, fmt.Errorf("apply TeamDefaults: %w", err)
	}
	if err = checkResults(response, 1, "the command"); err != nil {
		return DefaultsChange{}, err
	}
	inheriting, err := InheritingTeams(ctx, client)
	return DefaultsChange{Inheriting: inheriting}, err
}

// inspect fetches a source, unless trees were injected, and walks it.
func inspect(
	ctx context.Context, opts Options, source config.Source, defaults *config.TeamDefaults,
) (*SourceContents, error) {
	var tree fs.FS
	var commit string
	if injected := opts.Sources[source.Name]; injected != nil {
		tree = injected
	} else {
		got, err := fetchSources(ctx, config.Defaults{Sources: []config.Source{source}},
			map[string]bool{source.Name: true}, nil)
		if err != nil {
			return nil, err
		}
		defer got.cleanup()
		tree, commit = got.trees[source.Name], got.commits[source.Name]
	}
	contents, err := Discover(tree, source.Name, defaults)
	if err != nil {
		return nil, err
	}
	contents.Commit = commit
	return contents, nil
}

// Discover walks a source tree for role.yaml and harness.yaml documents of
// the right kind, skipping dot-directories and testdata (D9). A role's name
// is its metadata.name, its path the directory. Each is marked Default when
// the defaults select it from this source.
func Discover(tree fs.FS, sourceName string, defaults *config.TeamDefaults) (*SourceContents, error) {
	out := &SourceContents{Roles: []SourceRole{}, Harnesses: []SourceItem{}}
	err := fs.WalkDir(tree, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			base := d.Name()
			if p != "." && (strings.HasPrefix(base, ".") || base == "testdata") {
				return fs.SkipDir
			}
			return nil
		}
		dir := path.Dir(p)
		switch d.Name() {
		case "role.yaml":
			var role model.Role
			if !readKind(tree, p, &role, &role.APIVersion, &role.Kind, string(model.KindRole)) {
				return nil
			}
			if role.Metadata.Name == "" {
				return nil
			}
			entry := SourceRole{Name: role.Metadata.Name, Path: dir, Harnesses: []string{}, Tasks: []string{}}
			for name := range role.Spec.Harnesses {
				entry.Harnesses = append(entry.Harnesses, name)
			}
			sort.Strings(entry.Harnesses)
			for _, task := range role.Spec.Tasks {
				entry.Tasks = append(entry.Tasks, task.Name)
			}
			entry.Default = slices.ContainsFunc(defaults.Spec.Defaults.Roles, func(r config.Role) bool {
				return r.Source == sourceName && path.Clean(r.Path) == dir
			})
			out.Roles = append(out.Roles, entry)
		case "harness.yaml":
			var h struct {
				APIVersion string         `yaml:"apiVersion"`
				Kind       string         `yaml:"kind"`
				Metadata   model.Metadata `yaml:"metadata"`
			}
			if !readKind(tree, p, &h, &h.APIVersion, &h.Kind, string(model.KindHarness)) {
				return nil
			}
			out.Harnesses = append(out.Harnesses, SourceItem{
				Name: h.Metadata.Name, Path: dir,
				Default: slices.ContainsFunc(defaults.Spec.Defaults.Harnesses, func(x config.Harness) bool {
					return x.Source == sourceName && path.Clean(x.Path) == dir
				}),
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("source %s: walk: %w", sourceName, err)
	}
	sort.Slice(out.Roles, func(i, j int) bool { return out.Roles[i].Path < out.Roles[j].Path })
	sort.Slice(out.Harnesses, func(i, j int) bool { return out.Harnesses[i].Path < out.Harnesses[j].Path })
	return out, nil
}

// readKind decodes a candidate definition leniently and reports whether it is
// a v1beta1 document of kind with a name. Anything else is not a definition.
func readKind(tree fs.FS, p string, out any, apiVersion, kind *string, want string) bool {
	raw, err := fs.ReadFile(tree, p)
	if err != nil || yaml.Unmarshal(raw, out) != nil {
		return false
	}
	return *apiVersion == config.APIVersion && *kind == want
}

// ApplyDefaults stores a whole TeamDefaults document (AC14): what sets the
// connectors, parameters and secret bindings that `defaults set` does not.
// An omitted metadata is the realm default's one TeamDefaults.
func ApplyDefaults(ctx context.Context, opts Options, raw []byte) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	defaults, err := config.DecodeDefaults(bytes.NewReader(raw))
	if err != nil {
		return DefaultsChange{}, fmt.Errorf("TeamDefaults: %w", err)
	}
	if defaults.Metadata.Name == "" {
		defaults.Metadata.Name = config.DefaultsName
	}
	if defaults.Metadata.Realm == "" {
		defaults.Metadata.Realm = config.DefaultRealm
	}
	if defaults.Metadata.Realm != config.DefaultRealm {
		return DefaultsChange{}, fmt.Errorf("TeamDefaults: realm %q: teams are managed in realm %s",
			defaults.Metadata.Realm, config.DefaultRealm)
	}
	_, found, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return DefaultsChange{}, err
	}
	change, err := writeDefaults(ctx, opts.Client, defaults)
	change.Action = "updated"
	if !found {
		change.Action = "created"
	}
	return change, err
}
