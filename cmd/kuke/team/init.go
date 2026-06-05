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
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/kuketeams"
	"github.com/eminwux/kukeon/internal/teambuild"
	"github.com/eminwux/kukeon/internal/teamhost"
	"github.com/eminwux/kukeon/internal/teamrender"
	"github.com/eminwux/kukeon/internal/teamsource"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
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

// ApplyForTeamFunc applies a per-(role × harness) rendered set to kukeond
// under the project's team label, pruning that team's stale objects in the
// same call (the per-team prune-apply contract from #1029). The default
// implementation dials kukeond and invokes ApplyDocumentsForTeam; tests
// inject a stub via MockApplyForTeamKey so the apply path can run hermetically.
type ApplyForTeamFunc func(
	ctx context.Context, rawYAML []byte, team string,
) (kukeonv1.ApplyDocumentsResult, error)

// MockApplyForTeamKey injects an ApplyForTeamFunc via context for tests.
type MockApplyForTeamKey struct{}

// BuildAllFunc runs the local-build path for `kuke team init --build`: it
// walks the FROM directives of the selected catalog leaves under cacheDir,
// derives the base-before-leaves order, and invokes kukebuild once per
// image into the target realm's containerd namespace (no registry push).
// The default implementation delegates to teambuild.BuildAll; tests inject a
// stub via MockBuildAllKey so the build path can run without a kukebuild
// binary on PATH.
type BuildAllFunc func(
	ctx context.Context,
	cacheDir, sourceRef, realm string,
	leaves []*model.ImageCatalogEntry,
	progressW, stdout, stderr io.Writer,
) error

// MockBuildAllKey injects a BuildAllFunc via context for tests.
type MockBuildAllKey struct{}

// NewInitCmd builds the `kuke team init` subcommand. It reads the current
// project's kuketeam.yaml roster, scaffolds the operator-global facts file on
// first run, materializes the pinned agents source, renders the per-(role ×
// harness) CellBlueprint/CellConfig pairs, applies them to kukeond under the
// project's team label (per-team prune via #1029), and writes the per-project
// drop-in entry. `--dry-run` stops after render, prints the rendered objects
// to stdout, applies nothing, and touches no files on disk.
func NewInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "init",
		Short:         "Compose this project's team into the host ~/.kuke drop-in and apply to kukeond",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dryRun, err := cmd.Flags().GetBool("dry-run")
			if err != nil {
				return err
			}
			build, err := cmd.Flags().GetBool("build")
			if err != nil {
				return err
			}
			return runInit(cmd, dryRun, build)
		},
	}

	cmd.Flags().Bool(
		"dry-run", false,
		"Render the project's blueprints/configs to stdout without writing the drop-in entry or applying to kukeond",
	)
	cmd.Flags().Bool(
		"build", false,
		"Locally build the selected catalog images (kukebuild) into the target realm's containerd namespace; no registry push",
	)

	return cmd
}

// runInit gathers the cobra-coupled inputs (current directory, ~/.kuke layout,
// git-config reader, project-repo-URL reader, daemon apply reader) and hands
// them to composeTeam, which holds the testable lifecycle.
func runInit(cmd *cobra.Command, dryRun, build bool) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	layout := teamhost.NewLayout(config.DefaultKukeDir())
	return composeTeam(
		cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), projectDir, layout,
		gitConfigFromCmd(cmd), projectRepoURLFromCmd(cmd), resolveFromCmd(cmd),
		applyForTeamFromCmd(cmd), buildAllFromCmd(cmd),
		dryRun, build,
	)
}

// composeTeam runs the team-init lifecycle against explicit inputs:
//
//  1. Read the project roster from <projectDir>/kuketeam.yaml.
//  2. Load (or scaffold-and-load) the operator-global facts file.
//  3. Resolve the pinned agents source into the on-disk cache and load every
//     Role / Harness / ImageCatalog the roster references.
//  4. Render the per-(role × harness) CellBlueprint/CellConfig pairs.
//  5. Either print the rendered objects to stdout (--dry-run, nothing is
//     applied and no files are written) or apply the labeled set to kukeond
//     via ApplyDocumentsForTeam (per-team prune via #1029) and then write
//     the per-project drop-in entry. Nothing is written under
//     ~/.kuke/rendered/ — the on-disk record of an applied team is the
//     drop-in entry alone; the daemon owns the persisted blueprints/configs.
//
// composeTeam takes no cobra dependency so it is unit-testable with a temp
// layout, stub git-config / project-repo-URL readers, a stub resolver, and a
// stub apply, no live kukeond required.
func composeTeam(
	ctx context.Context,
	out, errOut io.Writer,
	projectDir string,
	layout teamhost.Layout,
	getGit GitConfigFunc,
	getProjectURL ProjectRepoURLFunc,
	resolve ResolveFunc,
	apply ApplyForTeamFunc,
	build BuildAllFunc,
	dryRun, doBuild bool,
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

	emitSourceKind(out, pt.Spec.Source)

	tc, scaffolded, err := loadOrScaffoldGlobalConfig(ctx, layout, getGit, dryRun)
	if err != nil {
		return err
	}
	if scaffolded {
		fmt.Fprintf(out, "scaffolded operator-global facts at %s\n", layout.GlobalConfigPath())
	}

	res, bundle, err := renderTeam(ctx, layout, projectDir, pt, tc, project, getProjectURL, resolve, doBuild)
	if err != nil {
		return err
	}

	if doBuild {
		if dryRun {
			fmt.Fprintf(out, "# dry-run: --build skipped (no kukebuild invocation); %d catalog leaf(s) selected\n",
				len(res.Selections))
		} else if bundle != nil && len(res.Selections) > 0 {
			realm := teamrender.DefaultRealm
			if buildErr := build(
				ctx, bundle.CacheDir, bundle.Source.Ref, realm,
				res.Selections, out, out, errOut,
			); buildErr != nil {
				return fmt.Errorf("build team images: %w", buildErr)
			}
		}
	}

	if dryRun {
		return emitDryRun(out, project, layout, res)
	}

	applyResult, applied, err := applyTeam(ctx, project, res, apply)
	if err != nil {
		return err
	}

	source := pt.Spec.Source
	entry := &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: project},
		Spec: model.TeamEntrySpec{
			Path:   projectDir,
			Source: &source,
		},
	}
	if writeErr := teamhost.WriteEntry(layout, entry); writeErr != nil {
		return fmt.Errorf("write team entry: %w", writeErr)
	}
	fmt.Fprintf(out, "wrote team %q to %s\n", project, layout.EntryPath(project))
	if applied {
		emitApplySummary(out, project, applyResult, len(res.Blueprints), len(res.Configs))
	} else {
		fmt.Fprintf(out, "rendered %d blueprint/%d config object(s) (no apply: no (role × harness) pairs)\n",
			len(res.Blueprints), len(res.Configs))
	}
	return nil
}

// applyTeam marshals the rendered set and hands it to apply with the project
// as the team label. Returns applied=false (with a zero result) when there is
// nothing to render — harness-less rosters skip the apply hop. A non-nil
// error from apply propagates verbatim so the drop-in entry write upstream
// only fires on a successful apply.
func applyTeam(
	ctx context.Context, project string, res *teamrender.Result, apply ApplyForTeamFunc,
) (kukeonv1.ApplyDocumentsResult, bool, error) {
	if res == nil || (len(res.Blueprints) == 0 && len(res.Configs) == 0) {
		return kukeonv1.ApplyDocumentsResult{}, false, nil
	}
	rawYAML, err := teamrender.MarshalYAML(res)
	if err != nil {
		return kukeonv1.ApplyDocumentsResult{}, false, fmt.Errorf("marshal rendered objects: %w", err)
	}
	applyResult, err := apply(ctx, rawYAML, project)
	if err != nil {
		return kukeonv1.ApplyDocumentsResult{}, false, fmt.Errorf("apply team %q to kukeond: %w", project, err)
	}
	return applyResult, true, nil
}

// emitApplySummary prints one line per applied resource, mirroring the
// `kuke apply -f` human-readable output, then a one-line aggregate so the
// operator sees both the per-object outcome and the overall counts.
func emitApplySummary(
	out io.Writer, project string, result kukeonv1.ApplyDocumentsResult, blueprints, configs int,
) {
	for _, r := range result.Resources {
		switch r.Action {
		case "created":
			fmt.Fprintf(out, "  %s %q: created\n", r.Kind, r.Name)
		case "updated":
			fmt.Fprintf(out, "  %s %q: updated\n", r.Kind, r.Name)
		case "unchanged":
			fmt.Fprintf(out, "  %s %q: unchanged\n", r.Kind, r.Name)
		case "pruned":
			fmt.Fprintf(out, "  %s %q: pruned\n", r.Kind, r.Name)
		case "failed":
			fmt.Fprintf(out, "  %s %q: failed", r.Kind, r.Name)
			if r.Error != "" {
				fmt.Fprintf(out, " (%s)", r.Error)
			}
			fmt.Fprintln(out)
		}
	}
	fmt.Fprintf(out, "applied %d blueprint/%d config object(s) to kukeond under team %q\n",
		blueprints, configs, project)
}

// emitSourceKind prints the resolved agents source's pinned-vs-floating
// classification, so a non-reproducible (floating-branch) roster is visible at
// init time. The key name carries the intent — a tag/commit is pinned, a
// branch floats — so this is derivable without interrogating git. A malformed
// source (exactly-one-ref violated) prints nothing; the parser has already
// rejected it upstream by the time render runs.
func emitSourceKind(out io.Writer, src model.TeamSource) {
	value, kind := src.Ref()
	if kind == "" {
		return
	}
	host, ownerRepo := src.Normalized()
	fmt.Fprintf(out, "agents source: %s/%s @ %s=%s (%s)\n",
		host, ownerRepo, kind, value, src.KindLabel())
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
// harness-less roster fast and offline. The resolved bundle is returned to
// the caller so `--build` can drive teambuild from the same materialized
// agents source.
//
// doBuild flips the render's image bind: in `--build` mode the rendered
// blueprints bind the locally-built `kukeon.internal/<ref>:<version>` refs
// (version = the agents source's pinned ref, matching the tag teambuild lands
// each image under) rather than the catalog's published images, so the bound
// ref and the just-built tag agree.
func renderTeam(
	ctx context.Context,
	layout teamhost.Layout,
	projectDir string,
	pt *model.ProjectTeam,
	tc *model.TeamsConfig,
	project string,
	getProjectURL ProjectRepoURLFunc,
	resolve ResolveFunc,
	doBuild bool,
) (*teamrender.Result, *teamsource.Bundle, error) {
	if len(pt.Spec.Defaults.Harnesses) == 0 || len(pt.Spec.Roles) == 0 {
		return &teamrender.Result{}, nil, nil
	}

	cache := teamsource.NewCache(layout.CacheDir())
	bundle, err := resolve(ctx, cache, tc, pt)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve agents source: %w", err)
	}

	projectURL, _ := getProjectURL(ctx, projectDir)
	in := teamrender.Inputs{
		Project:        project,
		ProjectRepoURL: strings.TrimSpace(projectURL),
		Build:          doBuild,
		SourceRef:      bundle.Source.Ref,
	}
	res, err := teamrender.Render(bundle, pt, tc, in)
	if err != nil {
		return nil, nil, fmt.Errorf("render team: %w", err)
	}
	return res, bundle, nil
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
	fmt.Fprintf(out, "---\n# dry-run: rendered %d blueprint/%d config object(s) (not applied to kukeond)\n",
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

// applyForTeamFromCmd returns the ApplyForTeamFunc the init flow uses — the
// test mock from context when present, otherwise a function that dials
// kukeond per invocation and forwards to ApplyDocumentsForTeam. The dial
// happens on call (rather than eagerly at command-setup time) so a roster
// with no (role × harness) pairs never opens the daemon connection.
func applyForTeamFromCmd(cmd *cobra.Command) ApplyForTeamFunc {
	if mock, ok := cmd.Context().Value(MockApplyForTeamKey{}).(ApplyForTeamFunc); ok && mock != nil {
		return mock
	}
	return func(ctx context.Context, rawYAML []byte, team string) (kukeonv1.ApplyDocumentsResult, error) {
		client, err := kukshared.DaemonClientFromCmd(cmd)
		if err != nil {
			return kukeonv1.ApplyDocumentsResult{}, err
		}
		defer func() { _ = client.Close() }()
		return client.ApplyDocumentsForTeam(ctx, rawYAML, team)
	}
}

// buildAllFromCmd returns the BuildAllFunc the init flow uses — the test
// mock from context when present, otherwise teambuild.BuildAll.
func buildAllFromCmd(cmd *cobra.Command) BuildAllFunc {
	if mock, ok := cmd.Context().Value(MockBuildAllKey{}).(BuildAllFunc); ok && mock != nil {
		return mock
	}
	return teambuild.BuildAll
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
