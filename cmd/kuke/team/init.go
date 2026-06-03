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

// NewInitCmd builds the `kuke team init` subcommand. It reads the current
// project's kuketeam.yaml roster, scaffolds the operator-global facts file on
// first run, and writes the per-project drop-in entry. Source resolution,
// render, and apply land in steps 2–4 — `--dry-run` is reserved for step 3's
// render output; in step 1 it prints the per-project entry that would be
// written and touches no files on disk.
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
		"Print the per-project entry that would be written without touching disk (full render lands in a later step)",
	)

	return cmd
}

// runInit gathers the cobra-coupled inputs (current directory, ~/.kuke layout,
// git-config reader) and hands them to composeTeam, which holds the testable
// lifecycle.
func runInit(cmd *cobra.Command, dryRun bool) error {
	projectDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	layout := teamhost.NewLayout(config.DefaultKukeDir())
	return composeTeam(
		cmd.Context(), cmd.OutOrStdout(), projectDir, layout, gitConfigFromCmd(cmd), dryRun,
	)
}

// composeTeam runs the team-init lifecycle against explicit inputs: read the
// project roster, scaffold the operator-global facts on first run, and write
// the per-project drop-in entry. `--dry-run` prints the entry that would be
// written and touches no files (full render lands in step 3). It takes no
// cobra dependency so it is unit-testable with a temp layout and a stub
// git-config reader, no live kukeond required.
func composeTeam(
	ctx context.Context,
	out io.Writer,
	projectDir string,
	layout teamhost.Layout,
	getGit GitConfigFunc,
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

	entry := &model.TeamEntry{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindTeamEntry,
		Metadata:   model.Metadata{Name: project},
		Spec: model.TeamEntrySpec{
			Path:   projectDir,
			Source: strings.TrimSpace(pt.Spec.Source),
		},
	}

	if dryRun {
		raw, marshalErr := yaml.Marshal(entry)
		if marshalErr != nil {
			return fmt.Errorf("marshal team entry: %w", marshalErr)
		}
		fmt.Fprintf(out, "# dry-run: would write %s\n%s",
			filepath.Join("~/.kuke/kuketeam.d", project+".yaml"), raw)
		return nil
	}

	created, err := teamhost.EnsureGlobalConfig(layout, buildGlobalConfig(ctx, getGit))
	if err != nil {
		return fmt.Errorf("ensure global config: %w", err)
	}
	if created {
		fmt.Fprintf(out, "scaffolded operator-global facts at %s\n", layout.GlobalConfigPath())
	}

	if writeErr := teamhost.WriteEntry(layout, entry); writeErr != nil {
		return fmt.Errorf("write team entry: %w", writeErr)
	}
	fmt.Fprintf(out, "wrote team %q to %s\n", project, layout.EntryPath(project))
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

// realGitConfig reads a single `git config --global <key>` value. A non-zero
// exit (key unset) reports ok=false; the value is whitespace-trimmed.
func realGitConfig(ctx context.Context, key string) (string, bool) {
	out, err := exec.CommandContext(ctx, "git", "config", "--global", key).Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
