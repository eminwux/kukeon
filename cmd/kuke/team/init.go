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

package team

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/kuketeams"
	"github.com/eminwux/kukeon/internal/teamhost"
	"github.com/eminwux/kukeon/internal/teamrender"
	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// projectFileName is the per-project roster committed in each project repo.
const projectFileName = "kuketeam.yaml"

// registryEnv seeds TeamsConfig.spec.registry on first-run scaffold.
const registryEnv = "KUKEON_REGISTRY"

// GitConfigFunc reads a single `git config --global <key>` value, reporting
// whether the key was set. Injected via MockGitConfigKey in tests so the
// scaffold is hermetic and does not read the host operator's real git config.
type GitConfigFunc func(ctx context.Context, key string) (string, bool)

// MockGitConfigKey injects a GitConfigFunc via context for tests.
type MockGitConfigKey struct{}

// ProjectRepoURLFunc resolves the clone URL of the project repo whose
// kuketeam.yaml is at projectDir. The default implementation runs
// `git -C <projectDir> remote get-url origin`; tests inject a stub via
// MockProjectRepoURLKey so the render path stays hermetic. A missing
// remote returns ok=false; the render pipeline then leaves the project
// repo slot unfilled rather than failing the whole `kuke team init`
// (the operator may be init-ing a project that has not yet been pushed).
type ProjectRepoURLFunc func(ctx context.Context, projectDir string) (string, bool)

// MockProjectRepoURLKey injects a ProjectRepoURLFunc via context for tests.
type MockProjectRepoURLKey struct{}

// ResolveFunc materializes the agents source and loads the role/harness/image
// documents the project's roster references. The default implementation
// delegates to teamsource.Resolve against a real on-disk cache; tests inject
// a stub via MockResolveKey so the render path can run without cloning git.
type ResolveFunc func(
	ctx context.Context,
	cache teamsource.Cache,
	tc *model.TeamsConfig,
	pt *model.ProjectTeam,
) (*teamsource.Bundle, error)

// MockResolveKey injects a ResolveFunc via context for tests.
type MockResolveKey struct{}

// NewInitCmd builds the `kuke team init` subcommand. It reads the current
// project's kuketeam.yaml roster, scaffolds the operator-global facts file on
// first run, writes the per-project drop-in entry, materializes the pinned
// agents source, and renders the per-(role × harness) CellBlueprint/CellConfig
// pairs. `--dry-run` stops after render, prints the rendered objects to
// stdout, and touches no files on disk (apply lands in step 4 #1043).
func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Compose this project's team into the host ~/.kuke drop-in",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			return runInit(cmd, dryRun)
		},
	}

	cmd.Flags().Bool(
		"dry-run", false,
		"Render the project's blueprints/configs to stdout without writing the drop-in entry or applying to kukeond",
	)

	return cmd
}

// runInit gathers the cobra-coupled inputs (current directory, ~/.kuke layout,
// git-config reader, project-repo-URL reader) and hands them to composeTeam,
// which holds the testable lifecycle.
func runInit(cmd *cobra.Command, dryRun bool) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	layout := teamhost.NewLayout(config.DefaultKukeDir())
	return composeTeam(
		cmd.Context(), cmd.OutOrStdout(), projectDir, layout,
		gitConfigFromCmd(cmd), projectRepoURLFromCmd(cmd), resolveFromCmd(cmd),
		dryRun,
	)
}

// composeTeam runs the team-init lifecycle against explicit inputs:
//
//  1. Read the project roster from <projectDir>/kuketeam.yaml.
//  2. Load (or scaffold-and-load) the operator-global facts file.
//  3. Resolve the pinned agents source into the on-disk cache and load every
//     Role / Harness / ImageCatalog the roster references.
//  4. Render the per-(role × harness) CellBlueprint/CellConfig pairs.
//  5. Either print the rendered objects to stdout (--dry-run) or write the
//     per-project drop-in entry (step 4 in #1043 will apply the rendered
//     objects to kukeond after this point).
//
// composeTeam takes no cobra dependency so it is unit-testable with a temp
// layout, stub git-config / project-repo-URL readers, and a stub resolver, no
// live kukeond required.
func composeTeam(
	ctx context.Context,
	out io.Writer,
	projectDir string,
	layout teamhost.Layout,
	getGit GitConfigFunc,
	getProjectURL ProjectRepoURLFunc,
	resolve ResolveFunc,
	dryRun bool,
) error {
	pt, err := readProjectTeam(projectDir)
	if err != nil {
		return err
	}

	project := strings.TrimSpace(pt.Metadata.Name)
	if project == "" {
		// Defensive: the parser already rejects an empty metadata.name, but
		// guard the filename key explicitly.
		return errdefs.ErrTeamMetadataNameRequired
	}

	tc, scaffolded, err := loadOrScaffoldGlobalConfig(ctx, layout, getGit, dryRun)
	if err != nil {
		return err
	}
	if scaffolded {
		fmt.Fprintf(out, "scaffolded operator-global facts at %s\n", layout.GlobalConfigPath())
	}

	res, err := renderTeam(ctx, layout, projectDir, pt, tc, project, getProjectURL, resolve)
	if err != nil {
		return err
	}

	if dryRun {
		return emitDryRun(out, project, layout, res)
	}

	entry := &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: project},
		Spec: model.TeamEntrySpec{
			Path:   projectDir,
			Source: strings.TrimSpace(pt.Spec.Source),
		},
	}
	if writeErr := teamhost.WriteEntry(layout, entry); writeErr != nil {
		return fmt.Errorf("write team entry: %w", writeErr)
	}
	fmt.Fprintf(out, "wrote team %q to %s\n", project, layout.EntryPath(project))
	fmt.Fprintf(out, "rendered %d blueprint/%d config object(s) (apply lands in step 4)\n",
		len(res.Blueprints), len(res.Configs))
	return nil
}

// loadOrScaffoldGlobalConfig returns the TeamsConfig the render pipeline
// consumes. When the file already exists, it is parsed off disk and
// returned with scaffolded=false. When it is absent, a scaffold is
// composed from the operator's git config + KUKEON_REGISTRY; in non-dry-run
// mode it is written to disk (scaffolded=true), in dry-run mode it is held
// only in memory (scaffolded=false) so dry-run honors the AC's "neither
// applies nor writes" contract.
func loadOrScaffoldGlobalConfig(
	ctx context.Context,
	layout teamhost.Layout,
	getGit GitConfigFunc,
	dryRun bool,
) (*model.TeamsConfig, bool, error) {
	path := layout.GlobalConfigPath()
	raw, readErr := os.ReadFile(path)
	if readErr == nil {
		doc, parseErr := kuketeams.Parse(raw)
		if parseErr != nil {
			return nil, false, fmt.Errorf("parse %q: %w", path, parseErr)
		}
		if doc.TeamsConfig == nil {
			return nil, false, fmt.Errorf("%s: expected TeamsConfig, got kind %q", path, doc.Kind)
		}
		return doc.TeamsConfig, false, nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("read %q: %w", path, readErr)
	}

	cfg := buildGlobalConfig(ctx, getGit)
	if dryRun {
		return cfg, false, nil
	}
	created, err := teamhost.EnsureGlobalConfig(layout, cfg)
	if err != nil {
		return nil, false, fmt.Errorf("ensure global config: %w", err)
	}
	return cfg, created, nil
}

// renderTeam resolves the bundle and runs the per-(role × harness) render.
// When the project declares no harness defaults there is nothing to render
// and the bundle is not resolved — keeping `kuke team init` against a
// harness-less roster fast and offline.
func renderTeam(
	ctx context.Context,
	layout teamhost.Layout,
	projectDir string,
	pt *model.ProjectTeam,
	tc *model.TeamsConfig,
	project string,
	getProjectURL ProjectRepoURLFunc,
	resolve ResolveFunc,
) (*teamrender.Result, error) {
	if len(pt.Spec.Defaults.Harnesses) == 0 || len(pt.Spec.Roles) == 0 {
		return &teamrender.Result{}, nil
	}

	cache := teamsource.NewCache(layout.CacheDir())
	bundle, err := resolve(ctx, cache, tc, pt)
	if err != nil {
		return nil, fmt.Errorf("resolve agents source: %w", err)
	}

	projectURL, _ := getProjectURL(ctx, projectDir)
	in := teamrender.Inputs{
		Project:        project,
		ProjectRepoURL: strings.TrimSpace(projectURL),
	}
	res, err := teamrender.Render(bundle, pt, tc, in)
	if err != nil {
		return nil, fmt.Errorf("render team: %w", err)
	}
	return res, nil
}

// emitDryRun prints the rendered objects to out as a multi-document YAML
// stream prefixed by a dry-run header. It writes nothing to disk.
func emitDryRun(out io.Writer, project string, layout teamhost.Layout, res *teamrender.Result) error {
	entry := &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: project},
		Spec:       model.TeamEntrySpec{},
	}
	rawEntry, err := yaml.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal team entry: %w", err)
	}
	fmt.Fprintf(out, "# dry-run: would write %s\n%s",
		filepath.Join("~/.kuke/kuketeam.d", project+".yaml"), rawEntry)

	if res == nil || (len(res.Blueprints) == 0 && len(res.Configs) == 0) {
		fmt.Fprintf(out, "# dry-run: no (role × harness) pairs to render\n")
		_ = layout // reserved for future cache-dir reporting
		return nil
	}

	rawRender, err := teamrender.MarshalYAML(res)
	if err != nil {
		return fmt.Errorf("marshal rendered objects: %w", err)
	}
	fmt.Fprintf(out, "---\n# dry-run: rendered %d blueprint/%d config object(s) (apply lands in step 4)\n",
		len(res.Blueprints), len(res.Configs))
	if _, writeErr := out.Write(rawRender); writeErr != nil {
		return writeErr
	}
	return nil
}

// readProjectTeam loads <projectDir>/kuketeam.yaml and returns the parsed
// ProjectTeam. A missing file surfaces ErrTeamProjectFileNotFound; a parsed
// document of the wrong kind surfaces ErrTeamProjectFileKind.
func readProjectTeam(projectDir string) (*model.ProjectTeam, error) {
	path := filepath.Join(projectDir, projectFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w (looked in %q)", errdefs.ErrTeamProjectFileNotFound, projectDir)
		}
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	doc, err := kuketeams.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	if doc.ProjectTeam == nil {
		return nil, fmt.Errorf("%w (got kind %q in %q)", errdefs.ErrTeamProjectFileKind, doc.Kind, path)
	}
	return doc.ProjectTeam, nil
}

// buildGlobalConfig assembles the operator-global TeamsConfig scaffolded on
// first run: git identity/signing seeded from `git config --global`, registry
// from the KUKEON_REGISTRY env. Sources and secrets are left empty for the
// operator to fill in (no interactive prompt — the verb is non-interactive).
func buildGlobalConfig(ctx context.Context, getGit GitConfigFunc) *model.TeamsConfig {
	cfg := &model.TeamsConfig{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamsConfig,
		Spec:       model.TeamsConfigSpec{},
	}

	if reg := strings.TrimSpace(os.Getenv(registryEnv)); reg != "" {
		cfg.Spec.Registry = reg
	}

	if git := seedGit(ctx, getGit); git != nil {
		cfg.Spec.Git = git
	}

	return cfg
}

// seedGit builds a TeamsConfigGit from the operator's global git config. It
// returns nil when no usable identity is present, so the scaffold omits the git
// block entirely rather than emitting a half-populated one. Sign entries are
// only seeded when a signing key is present, keeping the scaffold valid against
// the parser's git.sign-requires-signingKey rule.
func seedGit(ctx context.Context, getGit GitConfigFunc) *model.TeamsConfigGit {
	name, hasName := getGit(ctx, "user.name")
	email, hasEmail := getGit(ctx, "user.email")
	signingKey, _ := getGit(ctx, "user.signingkey")
	allowedSigners, _ := getGit(ctx, "gpg.ssh.allowedSignersFile")

	git := &model.TeamsConfigGit{}
	populated := false

	if hasName && hasEmail && strings.TrimSpace(name) != "" && strings.TrimSpace(email) != "" {
		// Construct two distinct values: a future load → mutate → write path in
		// step 2/3/4 must not silently couple author and committer through a
		// shared pointer.
		git.Author = &v1beta1.GitIdentity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(email)}
		git.Committer = &v1beta1.GitIdentity{Name: strings.TrimSpace(name), Email: strings.TrimSpace(email)}
		populated = true
	}
	if s := strings.TrimSpace(signingKey); s != "" {
		git.SigningKey = s
		populated = true
		var sign []string
		if v, _ := getGit(ctx, "commit.gpgsign"); strings.EqualFold(strings.TrimSpace(v), "true") {
			sign = append(sign, model.GitSignCommits)
		}
		if v, _ := getGit(ctx, "tag.gpgsign"); strings.EqualFold(strings.TrimSpace(v), "true") {
			sign = append(sign, model.GitSignTags)
		}
		git.Sign = sign
	}
	if a := strings.TrimSpace(allowedSigners); a != "" {
		git.AllowedSigners = a
		populated = true
	}

	if !populated {
		return nil
	}
	return git
}

// gitConfigFromCmd returns the GitConfigFunc the init flow uses — the test
// mock from context when present, otherwise the real `git config --global`
// reader.
func gitConfigFromCmd(cmd *cobra.Command) GitConfigFunc {
	if mock, ok := cmd.Context().Value(MockGitConfigKey{}).(GitConfigFunc); ok && mock != nil {
		return mock
	}
	return realGitConfig
}

// projectRepoURLFromCmd returns the ProjectRepoURLFunc the init flow uses
// — the test mock from context when present, otherwise the real
// `git -C <projectDir> remote get-url origin` reader.
func projectRepoURLFromCmd(cmd *cobra.Command) ProjectRepoURLFunc {
	if mock, ok := cmd.Context().Value(MockProjectRepoURLKey{}).(ProjectRepoURLFunc); ok && mock != nil {
		return mock
	}
	return realProjectRepoURL
}

// resolveFromCmd returns the ResolveFunc the init flow uses — the test
// mock from context when present, otherwise teamsource.Resolve against the
// real layout's cache.
func resolveFromCmd(cmd *cobra.Command) ResolveFunc {
	if mock, ok := cmd.Context().Value(MockResolveKey{}).(ResolveFunc); ok && mock != nil {
		return mock
	}
	return realResolve
}

// realGitConfig reads a single `git config --global <key>` value. A non-zero
// exit (key unset) reports ok=false; the value is whitespace-trimmed.
func realGitConfig(ctx context.Context, key string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "config", "--global", key).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// realProjectRepoURL reads the project's clone URL via
// `git -C <projectDir> remote get-url origin`. A non-zero exit (no remote
// configured, not a git repo) reports ok=false; the value is
// whitespace-trimmed.
func realProjectRepoURL(ctx context.Context, projectDir string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "-C", projectDir, "remote", "get-url", "origin").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// realResolve materializes the agents source via teamsource.Resolve against
// the given cache, mirroring the production path step 4's apply phase will
// consume.
func realResolve(
	ctx context.Context,
	cache teamsource.Cache,
	tc *model.TeamsConfig,
	pt *model.ProjectTeam,
) (*teamsource.Bundle, error) {
	return teamsource.Resolve(ctx, cache, tc, pt)
}
