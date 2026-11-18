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

// directoryTeam reads only: the checkout and authored declaration stay untouched.
func directoryTeam(ctx context.Context, opts AddOptions) (*config.Team, error) {
	dir, err := filepath.Abs(opts.Directory)
	if err != nil {
		return nil, fmt.Errorf("add directory: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("add directory: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("add: directory input must be a directory")
	}
	team, err := readDirectoryTeam(dir)
	if err != nil {
		return nil, err
	}

	if opts.Name != "" {
		team.Metadata.Name = opts.Name
	}
	if team.Metadata.Name == "" {
		team.Metadata.Name = filepath.Base(dir)
	}
	if err = validateAddName(team.Metadata.Name); err != nil {
		return nil, err
	}
	if team.ProjectSource == nil {
		team.ProjectSource = &config.Source{Name: "project"}
	}
	source := team.ProjectSource
	if source.Name == "" {
		source.Name = "project"
	}
	if source.ClonePath == "" {
		source.ClonePath = "/home/user/" + team.Metadata.Name
	}
	if opts.Source != "" {
		source.URL = opts.Source
	}
	if opts.Branch != "" || opts.Tag != "" || opts.Commit != "" {
		source.Branch, source.Tag, source.Commit = opts.Branch, opts.Tag, opts.Commit
	}
	if err = discoverDirectorySource(ctx, dir, source); err != nil {
		return nil, err
	}
	return team, nil
}

func readDirectoryTeam(dir string) (*config.Team, error) {
	team := &config.Team{APIVersion: config.APIVersion, Kind: config.KindTeam}
	path := filepath.Join(dir, "kuketeam.yaml")
	_, err := os.Lstat(path)
	if err == nil {
		f, openErr := os.Open(path)
		if openErr != nil {
			return nil, fmt.Errorf("add: cannot read local kuketeam.yaml: %w", openErr)
		}
		team, err = config.DecodeTeamDeclaration(f)
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("add local kuketeam.yaml: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("add: cannot read local kuketeam.yaml: %w", err)
	}

	return team, nil
}

func discoverDirectorySource(ctx context.Context, dir string, source *config.Source) error {
	// URL and revision discovery are independent: fully explicit inputs need no Git.
	if source.URL == "" {
		output, runErr := gitremote.Run(ctx, "-C", dir, "config", "--local", "--get", "remote.origin.url")
		if runErr != nil || strings.TrimSpace(string(output)) == "" {
			return errors.New(
				"add: cannot discover Git origin; check the directory's Git metadata and origin or supply --source",
			)
		}
		source.URL = strings.TrimSpace(string(output))
	}
	if source.Branch == "" && source.Tag == "" && source.Commit == "" {
		commit, runErr := gitremote.Run(ctx, "-C", dir, "rev-parse", "--verify", "HEAD^{commit}")
		if runErr != nil {
			return errors.New(
				"add: cannot discover a checked-out Git commit; check Git metadata/HEAD or supply --branch, --tag, or --commit",
			)
		}
		branch, branchErr := gitremote.Run(ctx, "-C", dir, "rev-parse", "--symbolic-full-name", "HEAD")
		if branchErr != nil {
			return errors.New("add: cannot read Git HEAD; supply --branch, --tag, or --commit")
		}
		ref := strings.TrimSpace(string(branch))
		if ref == "HEAD" {
			source.Commit = strings.TrimSpace(string(commit))
		} else if branchName, ok := strings.CutPrefix(ref, "refs/heads/"); ok && branchName != "" {
			source.Branch = branchName
		} else {
			return errors.New("add: HEAD is not a local branch; supply --branch, --tag, or --commit")
		}
	}
	return nil
}

func validateAddName(name string) error {
	if !config.ValidName(name) {
		return errors.New(
			"add: require a lowercase team name containing letters, digits, dots or hyphens, starting and ending with a letter/digit; root and kuke are reserved; use --name",
		)
	}
	if name == rootName || name == majordomoName {
		return fmt.Errorf("add: team name %q is reserved (root and kuke are reserved); use --name", name)
	}
	return nil
}
