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
	"maps"
	"path"
	"slices"
	"sort"
	"strings"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"github.com/eminwux/kukeon/pkg/api/version"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// The realm's TeamDefaults (issue #1618, D8) holds the default roles and
// harnesses, what a team that omits a list inherits; the sources they name
// are stored Sources (#1643). Every edit here is a read of the TeamDefaults,
// an edit, and an apply of the result.

// DefaultsChange reports an edit of the TeamDefaults.
type DefaultsChange struct {
	// Action is created, updated or removed.
	Action string
	// Inheriting are the stored teams that inherit from the defaults: a
	// change reaches them only when each is recomposed with
	// kuke team apply NAME.
	Inheriting []string
	// Notes are remarks on the write: a conversion of the stored defaults.
	Notes []string
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
	Name      string       `json:"name"`
	Path      string       `json:"path"`
	Harnesses []string     `json:"harnesses"`
	Tasks     []SourceTask `json:"tasks"`
	// Default reports that the TeamDefaults' default roles select it.
	Default bool `json:"default"`
}

// SourceTask is one task a role declares (role.yaml spec.tasks), with the
// defaults a team's task entry overrides (#1644 Step 1).
type SourceTask struct {
	Name string `json:"name"`
	// Schedule is the role's default cadence; empty runs the task by hand.
	Schedule string `json:"schedule,omitempty"`
	// Suspend ships the schedule switched off.
	Suspend bool `json:"suspend,omitempty"`
	// Args is the role's argument text for the task.
	Args string `json:"args,omitempty"`
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

// SetDefaults replaces the default role list, the default harness list, or
// both (AC14); a nil list is left as it is. Each entry is a current
// default's name or [NAME=]SOURCE:PATH in a stored Source.
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
	// kukeond stores a default naming any Source (D8); the command checks
	// the ones it sets against the stored Sources.
	stored, err := Sources(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	all, _ := withLegacy(defaults, stored)
	if err = checkDefaultsSources(defaults, all); err != nil {
		return DefaultsChange{}, fmt.Errorf("defaults set: %w", err)
	}
	change, err := writeDefaults(ctx, opts.Client, defaults)
	change.Action = actionUpdated
	return change, err
}

// SetDefaultConnectors changes the TeamDefaults' connector choices (issue
// #1790), the defaults every team that declares no connectors of its own
// inherits: a choice replaces that connector's entry, nil removes it, and
// every other entry is kept. The roles and harnesses are left as they are.
func SetDefaultConnectors(
	ctx context.Context, opts Options, changes map[string]*config.ConnectorChoice,
) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	if len(changes) == 0 {
		return DefaultsChange{}, errors.New("defaults set: name a connector to change")
	}
	defaults, err := Defaults(ctx, opts.Client)
	if err != nil {
		return DefaultsChange{}, err
	}
	connectors := maps.Clone(defaults.Spec.Defaults.Connectors)
	for _, name := range slices.Sorted(maps.Keys(changes)) {
		choice := changes[name]
		if choice == nil {
			delete(connectors, name)
			continue
		}
		if connectors == nil {
			connectors = map[string]config.ConnectorChoice{}
		}
		connectors[name] = *choice
	}
	if len(connectors) == 0 {
		connectors = nil
	}
	defaults.Spec.Defaults.Connectors = connectors
	if err = config.ValidateStoredDefaults(defaults); err != nil {
		return DefaultsChange{}, fmt.Errorf("defaults set: %w", err)
	}
	change, err := writeDefaults(ctx, opts.Client, defaults)
	change.Action = actionUpdated
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
		// Sources are stored apart from the defaults (#1643): a team reads
		// every stored Source whatever it lists, so its sources inherit
		// nothing.
		s := team.Spec
		if s.Roles == nil || s.Harnesses == nil || s.Connectors == nil {
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

// writeDefaults validates and applies a TeamDefaults document. A document
// written before #1643 is converted first (AC13): its sources are stored as
// Sources in the same apply, and the notes say so.
func writeDefaults(ctx context.Context, client Client, defaults *config.TeamDefaults) (DefaultsChange, error) {
	var writes map[string]config.SourceSpec
	var notes []string
	if defaults.Spec.Defaults.Sources != nil {
		stored, err := Sources(ctx, client)
		if err != nil {
			return DefaultsChange{}, err
		}
		defaults, writes, notes = convertDefaults(defaults, stored)
	}
	if err := applySources(ctx, client, writes, defaults, true); err != nil {
		return DefaultsChange{}, err
	}
	inheriting, err := InheritingTeams(ctx, client)
	return DefaultsChange{Inheriting: inheriting, Notes: notes}, err
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
		got, err := fetchSources(ctx, identityOf(opts.Client, config.DefaultRealm),
			config.Defaults{Sources: []config.Source{source}},
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
			entry := SourceRole{Name: role.Metadata.Name, Path: dir, Harnesses: []string{}, Tasks: []SourceTask{}}
			for name := range role.Spec.Harnesses {
				entry.Harnesses = append(entry.Harnesses, name)
			}
			sort.Strings(entry.Harnesses)
			for _, task := range role.Spec.Tasks {
				entry.Tasks = append(entry.Tasks, SourceTask{
					Name: task.Name, Schedule: task.Schedule, Suspend: task.Suspend, Args: task.Args,
				})
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
	return version.Check(*apiVersion) == nil && *kind == want
}

// ApplyDefaults stores a whole TeamDefaults document (AC14): what sets the
// connectors, parameters and secret bindings that `defaults set` does not.
// An omitted metadata is the realm default's one TeamDefaults.
func ApplyDefaults(ctx context.Context, opts Options, raw []byte) (DefaultsChange, error) {
	if err := opts.check(); err != nil {
		return DefaultsChange{}, err
	}
	// A TeamDefaults lists no sources (AC2): they are stored as Sources.
	defaults, err := config.DecodeStoredDefaults(bytes.NewReader(raw))
	if err == nil {
		err = config.ValidateStoredDefaults(defaults)
	}
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
	change.Action = actionUpdated
	if !found {
		change.Action = actionCreated
	}
	return change, err
}
