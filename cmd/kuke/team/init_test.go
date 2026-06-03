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
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamhost"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

const projectTeamYAML = `apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: eminwux/agents@v1.4.0
  roles:
    - { ref: dev }
`

// stubGit returns a GitConfigFunc backed by m.
func stubGit(m map[string]string) GitConfigFunc {
	return func(_ context.Context, key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

// writeProject creates a project dir with a kuketeam.yaml and returns its path.
func writeProject(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, projectFileName), []byte(body), 0o600); err != nil {
		t.Fatalf("write project file: %v", err)
	}
	return dir
}

func TestComposeTeamNoProjectFile(t *testing.T) {
	t.Parallel()
	emptyDir := t.TempDir()
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	err := composeTeam(context.Background(), &bytes.Buffer{}, emptyDir, layout, stubGit(nil), false)
	if !errors.Is(err, errdefs.ErrTeamProjectFileNotFound) {
		t.Fatalf("err = %v, want ErrTeamProjectFileNotFound", err)
	}
}

func TestComposeTeamWrongKind(t *testing.T) {
	t.Parallel()
	dir := writeProject(t, "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: {}\n")
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	err := composeTeam(context.Background(), &bytes.Buffer{}, dir, layout, stubGit(nil), false)
	if !errors.Is(err, errdefs.ErrTeamProjectFileKind) {
		t.Fatalf("err = %v, want ErrTeamProjectFileKind", err)
	}
}

func TestComposeTeamScaffoldsAndWrites(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	git := stubGit(map[string]string{
		"user.name":                  "Op Erator",
		"user.email":                 "op@example.com",
		"user.signingkey":            "/home/op/.ssh/id_ed25519.pub",
		"commit.gpgsign":             "true",
		"tag.gpgsign":                "true",
		"gpg.ssh.allowedSignersFile": "/home/op/.ssh/allowed_signers",
	})

	var out bytes.Buffer
	if err := composeTeam(context.Background(), &out, projectDir, layout, git, false); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}

	// Global facts scaffolded with seeded git identity + signing.
	rawGlobal, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	var gc model.TeamsConfig
	if unmarshalErr := yaml.Unmarshal(rawGlobal, &gc); unmarshalErr != nil {
		t.Fatalf("global config parse: %v", unmarshalErr)
	}
	if gc.Spec.Git == nil || gc.Spec.Git.Author == nil || gc.Spec.Git.Author.Email != "op@example.com" {
		t.Fatalf("git identity not seeded: %+v", gc.Spec.Git)
	}
	if gc.Spec.Git.SigningKey != "/home/op/.ssh/id_ed25519.pub" {
		t.Errorf("signing key not seeded: %q", gc.Spec.Git.SigningKey)
	}
	if len(gc.Spec.Git.Sign) != 2 {
		t.Errorf("git.sign = %v, want [commits tags]", gc.Spec.Git.Sign)
	}
	if gc.Spec.Git.AllowedSigners != "/home/op/.ssh/allowed_signers" {
		t.Errorf("allowedSigners not seeded: %q", gc.Spec.Git.AllowedSigners)
	}

	// Per-project entry written with locator + source.
	rawEntry, err := os.ReadFile(layout.EntryPath("sbsh"))
	if err != nil {
		t.Fatalf("entry not written: %v", err)
	}
	var te model.TeamEntry
	if unmarshalErr := yaml.Unmarshal(rawEntry, &te); unmarshalErr != nil {
		t.Fatalf("entry parse: %v", unmarshalErr)
	}
	if te.Metadata.Name != "sbsh" || te.Spec.Path != projectDir || te.Spec.Source != "eminwux/agents@v1.4.0" {
		t.Errorf("entry content wrong: %+v", te)
	}
	if !strings.Contains(out.String(), "wrote team \"sbsh\"") {
		t.Errorf("missing write confirmation in output: %q", out.String())
	}
}

func TestComposeTeamReRunDoesNotRescaffold(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	git := stubGit(map[string]string{"user.name": "A", "user.email": "a@example.com"})

	if err := composeTeam(context.Background(), &bytes.Buffer{}, projectDir, layout, git, false); err != nil {
		t.Fatalf("first composeTeam: %v", err)
	}
	// Tamper the global facts; a re-run must leave them untouched.
	if err := os.WriteFile(layout.GlobalConfigPath(), []byte("sentinel: kept\n"), 0o600); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	var out bytes.Buffer
	if err := composeTeam(context.Background(), &out, projectDir, layout, git, false); err != nil {
		t.Fatalf("second composeTeam: %v", err)
	}
	got, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("read global after re-run: %v", err)
	}
	if string(got) != "sentinel: kept\n" {
		t.Errorf("re-run re-scaffolded global facts: %q", got)
	}
	if strings.Contains(out.String(), "scaffolded") {
		t.Errorf("re-run printed a scaffold message: %q", out.String())
	}
}

func TestComposeTeamNoGitIdentityOmitsGitBlock(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	if err := composeTeam(context.Background(), &bytes.Buffer{}, projectDir, layout, stubGit(nil), false); err != nil {
		t.Fatalf("composeTeam: %v", err)
	}
	raw, err := os.ReadFile(layout.GlobalConfigPath())
	if err != nil {
		t.Fatalf("global config not written: %v", err)
	}
	var gc model.TeamsConfig
	if unmarshalErr := yaml.Unmarshal(raw, &gc); unmarshalErr != nil {
		t.Fatalf("parse: %v", unmarshalErr)
	}
	if gc.Spec.Git != nil {
		t.Errorf("git block seeded with no identity available: %+v", gc.Spec.Git)
	}
}

func TestComposeTeamDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	projectDir := writeProject(t, projectTeamYAML)
	layout := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))

	var out bytes.Buffer
	if err := composeTeam(context.Background(), &out, projectDir, layout, stubGit(nil), true); err != nil {
		t.Fatalf("composeTeam dry-run: %v", err)
	}
	if _, err := os.Stat(layout.GlobalConfigPath()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run wrote global config: err=%v", err)
	}
	if _, err := os.Stat(layout.EntryPath("sbsh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run wrote entry file: err=%v", err)
	}
	if !strings.Contains(out.String(), "dry-run") || !strings.Contains(out.String(), "sbsh") {
		t.Errorf("dry-run output missing expected content: %q", out.String())
	}
}

func TestNewInitCmdParsesDryRunFlag(t *testing.T) {
	t.Parallel()
	cmd := NewInitCmd()
	if cmd.Flags().Lookup("dry-run") == nil {
		t.Fatalf("--dry-run flag not registered")
	}
	if cmd.Name() != "init" {
		t.Errorf("init cmd name = %q", cmd.Name())
	}
}

func TestNewTeamCmdRegistersInit(t *testing.T) {
	t.Parallel()
	cmd := NewTeamCmd()
	if cmd.Name() != "team" {
		t.Errorf("team cmd name = %q", cmd.Name())
	}
	var found bool
	for _, c := range cmd.Commands() {
		if c.Name() == "init" {
			found = true
		}
	}
	if !found {
		t.Errorf("team init subcommand not registered")
	}
}
