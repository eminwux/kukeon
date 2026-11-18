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

// AddOptions names a remote project or a local Directory and optional overrides.
// Options supplies the same connection and application dependencies as init.
type AddOptions struct {
	Options

	Name, Source, Branch, Tag, Commit string
	Directory                         string
}

// Add assembles an inheriting declaration without bootstrapping/resetting global
// configuration. Every remote is validated before transport; project access is checked
// even when it contributes no definitions.
func Add(ctx context.Context, opts AddOptions) (Result, error) {
	if !filepath.IsAbs(opts.Root) {
		return Result{}, errors.New("add: configuration root must be absolute")
	}
	var team *config.Team
	var err error
	if opts.Directory != "" {
		team, err = directoryTeam(ctx, opts)
	} else {
		if err = validateAddName(opts.Name); err != nil {
			return Result{}, err
		}
		team, err = readNamedTeam(filepath.Join(opts.Root, "kuketeam.d", opts.Name+".yaml"), opts.Name)
		if err == nil {
			team.ProjectSource = &config.Source{
				Name: "project", URL: opts.Source,
				Branch: opts.Branch, Tag: opts.Tag, Commit: opts.Commit, ClonePath: "/home/user/" + opts.Name,
			}
		}
	}
	if err != nil {
		return Result{}, err
	}
	opts.Name = team.Metadata.Name
	if err = validateAddName(opts.Name); err != nil {
		return Result{}, err
	}
	// Registering over the pre-#1527 init entry would record a project source
	// on it, after which init --force no longer recognizes and migrates it.
	legacy, err := isLegacyEntry(filepath.Join(opts.Root, "kuketeam.d", opts.Name+".yaml"))
	if err != nil {
		return Result{}, err
	}
	if legacy {
		return Result{}, fmt.Errorf(
			"add: kuketeam.d/%s.yaml is the init team from before its rename to %s; run kuke team init --force first",
			opts.Name, majordomoName,
		)
	}
	source := team.ProjectSource
	remote, err := gitremote.URL(source.URL)
	if err != nil {
		return Result{}, err
	}
	revision := AddOptions{Branch: source.Branch, Tag: source.Tag, Commit: source.Commit}
	if err = validateRevision(revision); err != nil {
		return Result{}, err
	}
	missingRevision := source.Branch == "" && source.Tag == "" && source.Commit == ""
	path := filepath.Join(opts.Root, "kuketeams.yaml")
	f, err := os.Open(path)
	if err != nil {
		return Result{}, errors.New("add: cannot read ~/.kuke/kuketeams.yaml; run kuke team init and check permissions")
	}
	cfg, err := config.Decode(f)
	_ = f.Close()
	if err != nil {
		return Result{}, fmt.Errorf("add global configuration: %w", err)
	}
	// Resolve validates collisions and references before any network operation.
	if missingRevision {
		team.ProjectSource.Branch = "pending-default-discovery"
	}
	effective, err := config.Resolve(cfg, team)
	if err != nil {
		return Result{}, err
	}
	for _, source := range effective.Sources {
		if _, err = gitremote.URL(source.URL); err != nil {
			return Result{}, fmt.Errorf("source %q: %w", source.Name, err)
		}
	}
	if opts.Directory != "" {
		ctx, err = gitremote.WithRepositoryConfig(ctx, opts.Directory)
		if err != nil {
			return Result{}, err
		}
	}
	if missingRevision {
		team.ProjectSource.Branch, err = gitremote.DefaultBranch(ctx, remote)
		if err != nil {
			return Result{}, err
		}
		revision.Branch = team.ProjectSource.Branch
		if err = validateRevision(revision); err != nil {
			return Result{}, err
		}
	}
	// Embedders may supply definition files, but cannot bypass project access.
	if opts.Sources != nil {
		d := config.Defaults{Sources: []config.Source{*team.ProjectSource}}
		_, cleanup, fetchErr := materializeSources(ctx, d, true)
		if fetchErr != nil {
			return Result{}, fetchErr
		}
		cleanup()
	}
	command := "kuke team add " + opts.Name + " --source <repository>"
	if opts.Directory != "" {
		command = "kuke team add <directory> --name " + opts.Name
	}
	recorded, err := readEntrySecrets(filepath.Join(opts.Root, "kuketeam.d", opts.Name+".yaml"))
	if err != nil {
		return Result{}, err
	}
	return runTeam(ctx, opts.Options, cfg, team, recorded, Result{}, command, nil)
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
