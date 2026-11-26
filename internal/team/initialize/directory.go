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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// readDirectoryTeam reads a local checkout's kuketeam.yaml, or nil when it has
// none. The checkout stays untouched.
func readDirectoryTeam(dir string) (*config.Team, error) {
	path := filepath.Join(dir, teamFileName)
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil //nolint:nilnil // a directory without kuketeam.yaml inherits the defaults
	}
	if err != nil {
		return nil, fmt.Errorf("create team: cannot read local kuketeam.yaml: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("create team: cannot read local kuketeam.yaml: %w", err)
	}
	defer func() { _ = f.Close() }()
	team, err := config.DecodeTeamDeclaration(f)
	if err != nil {
		return nil, fmt.Errorf("add local kuketeam.yaml: %w", err)
	}
	return team, nil
}

func discoverDirectorySource(ctx context.Context, dir string, source *config.Source) error {
	// URL and revision discovery are independent: fully explicit inputs need no Git.
	if source.URL == "" {
		output, runErr := gitremote.Run(ctx, "-C", dir, "config", "--local", "--get", "remote.origin.url")
		if runErr != nil || strings.TrimSpace(string(output)) == "" {
			return errors.New(
				"create team: cannot discover Git origin; check the directory's Git metadata and origin, " +
					"or name the project with --source or spec.projectSource.url",
			)
		}
		source.URL = strings.TrimSpace(string(output))
	}
	if source.Branch == "" && source.Tag == "" && source.Commit == "" {
		commit, runErr := gitremote.Run(ctx, "-C", dir, "rev-parse", "--verify", "HEAD^{commit}")
		if runErr != nil {
			return errors.New(
				"create team: cannot discover a checked-out Git commit; check Git metadata/HEAD or supply --branch, --tag, or --commit",
			)
		}
		branch, branchErr := gitremote.Run(ctx, "-C", dir, "rev-parse", "--symbolic-full-name", "HEAD")
		if branchErr != nil {
			return errors.New("create team: cannot read Git HEAD; supply --branch, --tag, or --commit")
		}
		ref := strings.TrimSpace(string(branch))
		if ref == "HEAD" {
			source.Commit = strings.TrimSpace(string(commit))
		} else if branchName, ok := strings.CutPrefix(ref, "refs/heads/"); ok && branchName != "" {
			source.Branch = branchName
		} else {
			return errors.New("create team: HEAD is not a local branch; supply --branch, --tag, or --commit")
		}
	}
	return nil
}

func validateAddName(name string) error {
	if !config.ValidName(name) {
		return errors.New(
			"create team: require a lowercase team name containing letters, digits, dots or hyphens, starting and ending with a letter/digit; root, kuke and default are reserved; use --name",
		)
	}
	if name == rootName || name == majordomoName || name == config.DefaultTeamName {
		return fmt.Errorf(
			"create team: team name %q is reserved (root, kuke and default are reserved); use --name",
			name,
		)
	}
	return nil
}
