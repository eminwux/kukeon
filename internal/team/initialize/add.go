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
	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
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
	// Identity names the stored git identity of the project's Source when
	// this command creates it (spec.git.identityRef, #1643).
	Identity *config.SecretRef
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
				"create team: %w: %s; run kuke team apply %s to recompose it from its stored declaration, "+
					"or create it again with --force to replace the declaration", errdefs.ErrTeamExists, name, name)
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
// from that revision, and the fetched tree reused by composition. The project
// is --source, else the -f FILE's spec.projectSource (AC10).
func addRemote(
	ctx context.Context, opts AddOptions, name string, defaults *config.TeamDefaults, recorded []config.SecretBinding,
) (Outcome, error) {
	var file *config.Team
	if opts.File != nil {
		decoded, err := config.DecodeTeamDeclaration(bytes.NewReader(opts.File))
		if err != nil {
			return Outcome{}, fmt.Errorf("add %s: %w", opts.FileName, err)
		}
		file = decoded
	}
	project := &config.Source{
		Name: config.ProjectSourceName, URL: opts.Source, Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit,
		ClonePath: "/home/user/" + name,
	}
	if opts.Source == "" {
		declared := (*config.Source)(nil)
		if file != nil {
			declared = file.Spec.ProjectSource
		}
		if declared == nil || (declared.URL == "" && declared.Stored == "") {
			return Outcome{}, errors.New("create team: name the project with --source ADDRESS|SOURCE, " +
				"or spec.projectSource.url in the -f file")
		}
		project.URL, project.Stored, project.Git = declared.URL, declared.Stored, declared.Git.Clone()
		if declared.ClonePath != "" {
			project.ClonePath = declared.ClonePath
		}
		if !hasRef(*project) {
			project.Branch, project.Tag, project.Commit = declared.Branch, declared.Tag, declared.Commit
		}
	}
	fetch, err := projectSource(ctx, opts, name, project)
	if err != nil {
		return Outcome{}, err
	}
	if !hasRef(fetch) {
		branch, branchErr := defaultBranch(ctx, identityOf(opts.Client, config.DefaultRealm), fetch)
		if branchErr != nil {
			return Outcome{}, branchErr
		}
		if err = validateRevision(AddOptions{Branch: branch}); err != nil {
			return Outcome{}, err
		}
		project.Branch, fetch.Branch = branch, branch
	}
	have, cleanup, err := withProject(ctx, opts.Client, opts.Sources, fetch)
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
	var notes []string
	team.Spec.ProjectSource, notes = mergeProject(team.Spec.ProjectSource, project, fetch.URL)
	return addRun(ctx, opts, name, team, origin, defaults, recorded, have, repo == nil, notes)
}

// projectSource checks the project an add names and returns it as fetched:
// a stored Source's address and git when the project names one (--source
// NAME, or a declaration's source:), else its own address. --identity sets
// the git identity of the project's Source when the add creates it; a stored
// one keeps its own.
func projectSource(ctx context.Context, opts AddOptions, team string, project *config.Source) (config.Source, error) {
	if project.URL != "" && project.Stored == "" && config.ValidName(project.URL) {
		// --source NAME names a stored Source (#1643 Step 5).
		got, err := opts.Client.GetSource(ctx, kukeonv1.SourceRef{Name: project.URL, Realm: config.DefaultRealm})
		if err != nil {
			return config.Source{}, requestError("lookup", "source "+project.URL, err)
		}
		if got.Exists {
			project.Stored, project.URL = project.URL, ""
		}
	}
	target := storedName(*project, team, true)
	if project.URL == "" {
		spec, err := storedSourceSpec(ctx, opts.Client, target)
		if err != nil {
			return config.Source{}, err
		}
		if opts.Identity != nil {
			return config.Source{}, identityOnStored(target)
		}
		return fill(*project, spec), nil
	}
	if _, err := gitremote.URL(project.URL); err != nil {
		if config.ValidName(project.URL) {
			return config.Source{}, fmt.Errorf("%w: --source %s: no Source of that name is stored and it is "+
				"not an address; store it with kuke create source %s --url ADDRESS",
				errdefs.ErrSourceNotFound, project.URL, project.URL)
		}
		return config.Source{}, err
	}
	if opts.Identity != nil {
		got, err := opts.Client.GetSource(ctx, kukeonv1.SourceRef{Name: target, Realm: config.DefaultRealm})
		if err != nil {
			return config.Source{}, requestError("lookup", "source "+target, err)
		}
		if got.Exists {
			return config.Source{}, identityOnStored(target)
		}
		ref := *opts.Identity
		if ref.Realm == "" {
			ref.Realm = config.DefaultRealm
		}
		project.Git = &config.SourceGit{IdentityRef: &ref}
	}
	out := *project
	out.Git = project.Git.Clone()
	return out, nil
}

func identityOnStored(name string) error {
	return fmt.Errorf("create team: --identity: Source %s is stored and keeps its own identity; set it with "+
		"kuke create source %s --url ADDRESS --identity ID --force", name, name)
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
	return addRun(ctx, opts, name, team, origin, defaults, recorded, have, local == nil, nil)
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
	defaults *config.TeamDefaults, recorded []config.SecretBinding, have *fetched, noFile bool, notes []string,
) (Outcome, error) {
	command := "kuke create team " + name + " --source <repository>"
	if opts.Directory != "" {
		command = "kuke create team <directory> --name " + name
	}
	if err := applyOverrides(team, defaults, opts.Roles, opts.Harnesses); err != nil {
		return Outcome{}, err
	}
	req := request{
		command: command, origin: origin, defaults: defaults, team: team, recorded: recorded, have: have,
		notes: notes,
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
		return nil, origin, fmt.Errorf("create team: teams live in realm %s; remove metadata.realm %q",
			config.DefaultRealm, team.Metadata.Realm)
	}
	return team, origin, nil
}

// mergeProject sets the project source to the one the add resolved (the
// command line's, else the declaration's), keeping what the declaration
// states besides its address and revision. When the declaration names
// another repository than the command line, a note names both (AC10).
func mergeProject(declared, project *config.Source, address string) (*config.Source, []string) {
	if declared == nil {
		return project, nil
	}
	var notes []string
	if declared.URL != "" && address != "" && !gitcred.SameRepository(declared.URL, address) {
		notes = append(notes, fmt.Sprintf("the project is %s (--source); the declaration's "+
			"spec.projectSource.url names %s", gitcred.ShortForm(address), gitcred.ShortForm(declared.URL)))
	}
	out := *declared
	out.Name, out.URL, out.Stored, out.Git = config.ProjectSourceName, project.URL, project.Stored, project.Git.Clone()
	out.Branch, out.Tag, out.Commit = project.Branch, project.Tag, project.Commit
	if out.ClonePath == "" {
		out.ClonePath = project.ClonePath
	}
	return &out, notes
}

// applyOverrides replaces the declaration's role and harness lists with the
// command line's (AC9). A bare NAME picks the default of that name;
// [NAME=]SOURCE:PATH names any role or harness in a source the team lists or
// a stored Source.
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
			"--role %s: no default role of that name; name it as SOURCE:PATH in a source the team lists "+
				"or one stored with kuke create source", name)
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
			"--harness %s: no default harness of that name; name it as SOURCE:PATH in a source the team "+
				"lists or one stored with kuke create source", name)
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
		return nil, fmt.Errorf("create team: cannot read the repository's %s: %w", teamFileName, err)
	}
	team, err := config.DecodeTeamDeclaration(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("create team: the repository's %s: %w", teamFileName, err)
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
		return "", errors.New("create team: directory input must be a directory")
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
			return errors.New(
				"create team: invalid revision selector; use a branch/tag name or a full commit object ID",
			)
		}
	}
	if count > 1 {
		return errors.New("create team: --branch, --tag, and --commit are mutually exclusive")
	}
	if opts.Commit != "" {
		if len(opts.Commit) != 40 && len(opts.Commit) != 64 {
			return errors.New("create team: --commit requires a full hexadecimal object ID")
		}
		for _, c := range opts.Commit {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return errors.New("create team: --commit requires a full hexadecimal object ID")
			}
		}
	}
	return nil
}
