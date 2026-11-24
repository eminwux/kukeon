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
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// teamFileName is the Team document a repository may carry at its root.
const teamFileName = "kuketeam.yaml"

// AddOptions names a remote project or a local Directory and optional
// overrides (issue #1618, D10 and AC9).
type AddOptions struct {
	Options

	Name, Source, Branch, Tag, Commit string
	Directory                         string
	// Roles and Harnesses, when non-nil, replace the declaration's lists.
	// Each entry is a default's name or [NAME=]SOURCE:PATH.
	Roles, Harnesses []string
	// File, when set, is a Team document that replaces the whole
	// declaration; FileName is how to name it in messages.
	File     []byte
	FileName string
	// NoRepoFile ignores the repository's kuketeam.yaml.
	NoRepoFile bool
	// Force replaces a stored team of the same name.
	Force bool
}

// Add creates a team (AC8, AC8a). In NAME mode it fetches the repository and
// uses its kuketeam.yaml from the fetched revision when there is one; in
// directory mode it reads the local file. Without a file the team inherits
// the defaults. A stored team of the name is an error unless Force, checked
// before anything is fetched; with Force the stored declaration is not
// consulted, only its recorded secret bindings are kept.
func Add(ctx context.Context, opts AddOptions) (Outcome, error) {
	if err := opts.check(); err != nil {
		return Outcome{}, err
	}
	if err := validateRevision(opts); err != nil {
		return Outcome{}, err
	}
	var local *config.Team
	var dir string
	name := opts.Name
	if opts.Directory != "" {
		var err error
		if dir, err = checkDirectory(opts.Directory); err != nil {
			return Outcome{}, err
		}
		if !opts.NoRepoFile && opts.File == nil {
			if local, err = readDirectoryTeam(dir); err != nil {
				return Outcome{}, err
			}
		}
		if name == "" && local != nil {
			name = local.Metadata.Name
		}
		if name == "" {
			name = filepath.Base(dir)
		}
	}
	if err := validateAddName(name); err != nil {
		return Outcome{}, err
	}
	stored, err := StoredTeam(ctx, opts.Client, name)
	if err != nil {
		return Outcome{}, err
	}
	var recorded []config.SecretBinding
	if stored != nil {
		if !opts.Force {
			return Outcome{}, fmt.Errorf(
				"add: %w: %s; run kuke team apply %s to recompose it from its stored declaration, "+
					"or add it again with --force to replace the declaration", errdefs.ErrTeamExists, name, name)
		}
		recorded = stored.Spec.Secrets
	}
	defaults, _, err := readDefaults(ctx, opts.Client, config.DefaultRealm)
	if err != nil {
		return Outcome{}, err
	}
	if opts.Directory != "" {
		return addDirectory(ctx, opts, name, dir, local, defaults, recorded)
	}
	return addRemote(ctx, opts, name, defaults, recorded)
}

// addRemote is NAME mode: the project is fetched once, its kuketeam.yaml read
// from that revision, and the fetched tree reused by composition.
func addRemote(
	ctx context.Context, opts AddOptions, name string, defaults *config.TeamDefaults, recorded []config.SecretBinding,
) (Outcome, error) {
	if _, err := gitremote.URL(opts.Source); err != nil {
		return Outcome{}, err
	}
	project := &config.Source{
		Name: config.ProjectSourceName, URL: opts.Source, Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit,
		ClonePath: "/home/user/" + name,
	}
	if project.Branch == "" && project.Tag == "" && project.Commit == "" {
		branch, err := defaultBranch(ctx, identityOf(opts.Client, config.DefaultRealm), *project)
		if err != nil {
			return Outcome{}, err
		}
		project.Branch = branch
		if err = validateRevision(AddOptions{Branch: branch}); err != nil {
			return Outcome{}, err
		}
	}
	have, cleanup, err := withProject(ctx, opts.Client, opts.Sources, *project)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()
	var repo *config.Team
	if tree := have.trees[config.ProjectSourceName]; tree != nil && opts.File == nil && !opts.NoRepoFile {
		if repo, err = readTreeTeam(tree); err != nil {
			return Outcome{}, err
		}
	}
	origin := Origin{Kind: OriginDefaults}
	if repo != nil {
		origin = Origin{
			Kind:   OriginRepository,
			Detail: teamFileName + " at " + shortCommit(have.commits[config.ProjectSourceName]),
		}
	}
	team, origin, err := declaration(opts, name, repo, origin)
	if err != nil {
		return Outcome{}, err
	}
	team.Spec.ProjectSource = mergeProject(team.Spec.ProjectSource, project)
	return addRun(ctx, opts, name, team, origin, defaults, recorded, have, repo == nil)
}

// addDirectory is directory mode: the local kuketeam.yaml, the origin URL
// and the checked-out branch or commit.
func addDirectory(
	ctx context.Context, opts AddOptions, name, dir string, local *config.Team,
	defaults *config.TeamDefaults, recorded []config.SecretBinding,
) (Outcome, error) {
	origin := Origin{Kind: OriginDefaults}
	if local != nil {
		origin = Origin{Kind: OriginDirectory, Detail: filepath.Join(dir, teamFileName)}
	}
	team, origin, err := declaration(opts, name, local, origin)
	if err != nil {
		return Outcome{}, err
	}
	source := team.Spec.ProjectSource
	if source == nil {
		source = &config.Source{}
		team.Spec.ProjectSource = source
	}
	source.Name = config.ProjectSourceName
	if source.ClonePath == "" {
		source.ClonePath = "/home/user/" + name
	}
	if opts.Source != "" {
		source.URL = opts.Source
	}
	if opts.Branch != "" || opts.Tag != "" || opts.Commit != "" {
		source.Branch, source.Tag, source.Commit = opts.Branch, opts.Tag, opts.Commit
	}
	if err = discoverDirectorySource(ctx, dir, source); err != nil {
		return Outcome{}, err
	}
	if _, err = gitremote.URL(source.URL); err != nil {
		return Outcome{}, err
	}
	if err = validateRevision(AddOptions{Branch: source.Branch, Tag: source.Tag, Commit: source.Commit}); err != nil {
		return Outcome{}, err
	}
	ctx, err = gitremote.WithRepositoryConfig(ctx, dir)
	if err != nil {
		return Outcome{}, err
	}
	have, cleanup, err := withProject(ctx, opts.Client, opts.Sources, *source)
	if err != nil {
		return Outcome{}, err
	}
	defer cleanup()
	return addRun(ctx, opts, name, team, origin, defaults, recorded, have, local == nil)
}

// withProject fetches an add's project once, unless a tree was injected under
// its name: injected definition trees replace fetching only for the sources
// they name, so an add checks the revision it pins, and NAME mode reads the
// repository's kuketeam.yaml from it, even when the definitions are supplied.
func withProject(
	ctx context.Context, client Client, injected map[string]fs.FS, project config.Source,
) (*fetched, func(), error) {
	have := injectedSources(injected)
	if have != nil && have.trees[config.ProjectSourceName] != nil {
		return have, func() {}, nil
	}
	got, err := fetchSources(ctx, identityOf(client, config.DefaultRealm),
		config.Defaults{Sources: []config.Source{project}},
		map[string]bool{config.ProjectSourceName: true}, nil)
	if err != nil {
		return nil, nil, err
	}
	if have == nil {
		return got, got.cleanup, nil
	}
	have.trees = maps.Clone(have.trees)
	have.trees[config.ProjectSourceName], have.commits[config.ProjectSourceName] = got.trees[config.ProjectSourceName], got.commits[config.ProjectSourceName]
	return have, got.cleanup, nil
}

func addRun(
	ctx context.Context, opts AddOptions, name string, team *config.Team, origin Origin,
	defaults *config.TeamDefaults, recorded []config.SecretBinding, have *fetched, noFile bool,
) (Outcome, error) {
	command := "kuke team add " + name + " --source <repository>"
	if opts.Directory != "" {
		command = "kuke team add <directory> --name " + name
	}
	if err := applyOverrides(team, defaults, opts.Roles, opts.Harnesses); err != nil {
		return Outcome{}, err
	}
	req := request{
		command: command, origin: origin, defaults: defaults, team: team, recorded: recorded, have: have,
	}
	if noFile && opts.File == nil && team.Spec.Roles == nil && len(defaults.Spec.Defaults.Roles) == 0 {
		req.emptyCause = "the repository has no " + teamFileName + " and the defaults have no default roles"
	}
	return run(ctx, opts.Options, req)
}

// declaration picks the declaration per D10, first match wins: -f FILE, then
// the repository's (or directory's) kuketeam.yaml, then an empty inheriting
// declaration. NAME wins over the file's metadata.name.
func declaration(opts AddOptions, name string, repo *config.Team, origin Origin) (*config.Team, Origin, error) {
	var team *config.Team
	switch {
	case opts.File != nil:
		decoded, err := config.DecodeTeamDeclaration(bytes.NewReader(opts.File))
		if err != nil {
			return nil, origin, fmt.Errorf("add %s: %w", opts.FileName, err)
		}
		team, origin = decoded, Origin{Kind: OriginFile, Detail: opts.FileName}
	case repo != nil:
		team = repo
	default:
		team = &config.Team{APIVersion: config.APIVersion, Kind: config.KindTeam}
	}
	team.Metadata.Name = name
	if team.Metadata.Realm == "" {
		team.Metadata.Realm = config.DefaultRealm
	}
	if team.Metadata.Realm != config.DefaultRealm {
		return nil, origin, fmt.Errorf("add: teams live in realm %s; remove metadata.realm %q",
			config.DefaultRealm, team.Metadata.Realm)
	}
	return team, origin, nil
}

// mergeProject sets the project source to the repository named on the command
// line, keeping what the declaration states besides its URL and revision.
func mergeProject(declared, project *config.Source) *config.Source {
	if declared == nil {
		return project
	}
	out := *declared
	out.Name, out.URL = config.ProjectSourceName, project.URL
	out.Branch, out.Tag, out.Commit = project.Branch, project.Tag, project.Commit
	if out.ClonePath == "" {
		out.ClonePath = project.ClonePath
	}
	return &out
}

// applyOverrides replaces the declaration's role and harness lists with the
// command line's (AC9). A bare NAME picks the default of that name;
// [NAME=]SOURCE:PATH names any role or harness in a registered source.
func applyOverrides(team *config.Team, defaults *config.TeamDefaults, roles, harnesses []string) error {
	if roles != nil {
		list := []config.Role{}
		for _, spec := range roles {
			role, err := parseRole(spec, defaults.Spec.Defaults.Roles)
			if err != nil {
				return err
			}
			list = append(list, role)
		}
		team.Spec.Roles = list
	}
	if harnesses != nil {
		list := []config.Harness{}
		for _, spec := range harnesses {
			h, err := parseHarness(spec, defaults.Spec.Defaults.Harnesses)
			if err != nil {
				return err
			}
			list = append(list, h)
		}
		team.Spec.Harnesses = list
	}
	return nil
}

// selectionSpec is one parsed --role or --harness value.
type selectionSpec struct {
	name, source, dir string
	// qualified is false for a bare name, which picks a default.
	qualified bool
}

// parseSelection splits [NAME=]SOURCE:PATH.
func parseSelection(spec string) (selectionSpec, error) {
	rest := spec
	var out selectionSpec
	if before, after, named := strings.Cut(spec, "="); named {
		out.name, rest = before, after
	}
	source, dir, qualified := strings.Cut(rest, ":")
	if !qualified {
		if out.name != "" {
			return selectionSpec{}, fmt.Errorf("%q: want NAME or [NAME=]SOURCE:PATH", spec)
		}
		return selectionSpec{name: spec}, nil
	}
	dir = strings.Trim(dir, "/")
	if source == "" || dir == "" {
		return selectionSpec{}, fmt.Errorf("%q: want NAME or [NAME=]SOURCE:PATH", spec)
	}
	if out.name == "" {
		out.name = path.Base(dir)
	}
	out.source, out.dir, out.qualified = source, dir, true
	return out, nil
}

func parseRole(spec string, defaults []config.Role) (config.Role, error) {
	sel, err := parseSelection(spec)
	if err != nil {
		return config.Role{}, fmt.Errorf("--role %w", err)
	}
	name := sel.name
	if !sel.qualified {
		for _, role := range defaults {
			if role.Name == name {
				return role, nil
			}
		}
		return config.Role{}, fmt.Errorf(
			"--role %s: no default role of that name; name it as SOURCE:PATH in a source registered with "+
				"kuke team source add", name)
	}
	return config.Role{Name: name, Source: sel.source, Path: sel.dir}, nil
}

func parseHarness(spec string, defaults []config.Harness) (config.Harness, error) {
	sel, err := parseSelection(spec)
	if err != nil {
		return config.Harness{}, fmt.Errorf("--harness %w", err)
	}
	name := sel.name
	if !sel.qualified {
		for _, h := range defaults {
			if h.Name == name {
				return h, nil
			}
		}
		return config.Harness{}, fmt.Errorf(
			"--harness %s: no default harness of that name; name it as SOURCE:PATH in a registered source", name)
	}
	return config.Harness{Name: name, Source: sel.source, Path: sel.dir}, nil
}

// readTreeTeam reads the kuketeam.yaml at the root of a fetched tree, or nil
// when it has none.
func readTreeTeam(tree fs.FS) (*config.Team, error) {
	raw, err := fs.ReadFile(tree, teamFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // a repository without kuketeam.yaml inherits the defaults
	}
	if err != nil {
		return nil, fmt.Errorf("add: cannot read the repository's %s: %w", teamFileName, err)
	}
	team, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("add: the repository's %s: %w", teamFileName, err)
	}
	return team, nil
}

func shortCommit(commit string) string {
	const short = 12
	if len(commit) > short {
		return commit[:short]
	}
	if commit == "" {
		return "the fetched revision"
	}
	return commit
}

func checkDirectory(raw string) (string, error) {
	dir, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("add directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("add directory: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("add: directory input must be a directory")
	}
	return dir, nil
}

func validateRevision(opts AddOptions) error {
	count := 0
	for _, ref := range []string{opts.Branch, opts.Tag, opts.Commit} {
		if ref == "" {
			continue
		}
		count++
		if strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, ".") || strings.Contains(ref, "..") ||
			strings.Contains(ref, "@{") ||
			strings.HasSuffix(ref, ".lock") ||
			strings.ContainsAny(ref, " ~^:?*[\\\t\r\n\x00") {
			return errors.New("add: invalid revision selector; use a branch/tag name or a full commit object ID")
		}
	}
	if count > 1 {
		return errors.New("add: --branch, --tag, and --commit are mutually exclusive")
	}
	if opts.Commit != "" {
		if len(opts.Commit) != 40 && len(opts.Commit) != 64 {
			return errors.New("add: --commit requires a full hexadecimal object ID")
		}
		for _, c := range opts.Commit {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return errors.New("add: --commit requires a full hexadecimal object ID")
			}
		}
	}
	return nil
}
