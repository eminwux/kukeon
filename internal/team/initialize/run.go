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

// Package initialize applies the kuke team's configuration without managing cells.
package initialize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

const (
	majordomoName = config.SystemTeamName
	rootName      = "root" // Reserved legacy identity; never migrated by init.
	// legacyName is what the init team was called before #1527. A host
	// initialized then holds kuketeam.d/kukeon.yaml and kukeon-majordomo-*
	// configs, which init --force migrates; kukeon is otherwise an ordinary
	// project team name (kukeon's own kuketeam.yaml uses it).
	legacyName = "kukeon"
)

// Options supplies operator paths and optional test/embedding dependencies.
// An injected Client remains caller-owned; otherwise Run opens and closes it.
type Options struct {
	Root, Home, Host, Configuration string
	Force                           bool
	// Reapply recomposes the kuke team from its stored entry instead of
	// initializing. It excludes Force.
	Reapply bool
	Sources map[string]fs.FS
	Client  Client

	// legacyTeam, when set, names a pre-#1527 init team whose configs runTeam
	// deletes once the replacement is applied; until then they do not hold the
	// replacement's shared blueprints. Set only by a forced init that found
	// that team.
	legacyTeam string
}

type Result struct {
	// ConfigPath is the operator's kuketeams.yaml when one exists and was
	// read; empty when there is none (issue #1591: the file is optional).
	ConfigPath string
	EntryPath  string
	Resources  int
	// MissingSecrets lists one remediation line per required runtime secret that
	// does not exist yet; cells using it fail to start until it is created.
	MissingSecrets []string
	// Notes are operator-facing remarks: settings that no longer have an
	// effect, a missing git identity, renamed blueprints.
	Notes []string
}

// loadTeamsConfig reads the operator's kuketeams.yaml. The file is optional
// and hand-written (issue #1591): kuke reads it and never writes it, and a
// missing file is the empty TeamsConfig. The second return is the path when
// the file exists.
func loadTeamsConfig(root string) (*config.TeamsConfig, string, error) {
	path := filepath.Join(root, "kuketeams.yaml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &config.TeamsConfig{APIVersion: config.APIVersion, Kind: config.Kind}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read global configuration %s: %w", path, err)
	}
	cfg, err := config.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, "", fmt.Errorf("global configuration %s: %w", path, err)
	}
	return cfg, path, nil
}

// Run initializes the kuke team when it is not registered yet, unless Force
// resets it. "Already initialized" is the kuke team entry
// (kuketeam.d/kuke.yaml), not kuketeams.yaml, which init never writes.
// Partial daemon application is not rolled back; a forced rerun converges the team.
func Run(ctx context.Context, opts Options) (Result, error) {
	if opts.Reapply {
		return reapply(ctx, opts)
	}
	entryPath := filepath.Join(opts.Root, "kuketeam.d", majordomoName+".yaml")
	legacyPath := filepath.Join(opts.Root, "kuketeam.d", legacyName+".yaml")
	legacy, err := isLegacyEntry(legacyPath)
	if err != nil {
		return Result{}, err
	}
	if !opts.Force {
		if _, statErr := os.Lstat(entryPath); statErr == nil {
			return Result{}, fmt.Errorf(
				"init: already initialized (%s); use --force to reset or --reapply to recompose", entryPath)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return Result{}, fmt.Errorf("init: inspect %s: %w", entryPath, statErr)
		}
		if legacy {
			return Result{}, fmt.Errorf(
				"init: already initialized under the old kukeon team name (%s); use --force to migrate it",
				legacyPath)
		}
	}
	if legacy {
		opts.legacyTeam = legacyName
	}
	cfg, configPath, err := loadTeamsConfig(opts.Root)
	if err != nil {
		return Result{}, fmt.Errorf("init: %w", err)
	}
	result, err := runTeam(ctx, opts, cfg, config.Majordomo(), nil, Result{ConfigPath: configPath}, "kuke team init")
	if err == nil && opts.legacyTeam != "" {
		if err = os.Remove(legacyPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return result, fmt.Errorf(
				"team %s applied but the legacy entry %s was not removed: %w; remove it by hand",
				majordomoName, legacyPath, err,
			)
		}
		err = nil
		result.Notes = append(result.Notes, migrationNote(legacyPath))
	}
	return result, err
}

// reapply recomposes the kuke team from its stored entry and applies it
// (issue #1509). The entry is the team's editable source: the place to switch
// the health sweep on, for one. The daemon prunes the configs the new
// composition no longer renders, and runTeam rewrites the entry only after a
// fully successful apply. kuketeams.yaml is read when present, never written.
func reapply(ctx context.Context, opts Options) (Result, error) {
	if opts.Force {
		return Result{}, errors.New("init: --reapply and --force are mutually exclusive: " +
			"--reapply keeps the stored kuke entry, --force replaces it with the initial declaration")
	}
	entryPath := filepath.Join(opts.Root, "kuketeam.d", majordomoName+".yaml")
	if _, err := os.Stat(entryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf(
				"init: --reapply needs the stored kuke entry at %s, which does not exist; "+
					"run kuke team init first (or kuke team init --force to start over, "+
					"which also migrates a host initialized under the old kukeon name)", entryPath)
		}
		return Result{}, fmt.Errorf("init: inspect %s: %w", entryPath, err)
	}
	// Strict decode: an edit with an unknown or misspelled field stops here,
	// before any source is fetched or anything reaches the daemon.
	cfg, configPath, err := loadTeamsConfig(opts.Root)
	if err != nil {
		return Result{}, fmt.Errorf("init: %w", err)
	}
	team, err := readNamedTeam(entryPath, majordomoName)
	if err != nil {
		return Result{}, err
	}
	recorded, err := readEntrySecrets(entryPath)
	if err != nil {
		return Result{}, err
	}
	return runTeam(ctx, opts, cfg, team, recorded, Result{ConfigPath: configPath}, "kuke team init --reapply")
}

// runTeam is shared by init and add; staging precedes all daemon writes.
func runTeam(
	ctx context.Context,
	opts Options,
	cfg *config.TeamsConfig,
	team *config.Team,
	recorded []config.SecretBinding,
	result Result,
	command string,
) (Result, error) {
	name := team.Metadata.Name
	entryPath := filepath.Join(opts.Root, "kuketeam.d", name+".yaml")
	effective, err := config.Resolve(cfg, team)
	if err != nil {
		return result, err
	}
	sources := opts.Sources
	if sources == nil {
		var cleanup func()
		sources, cleanup, err = materializeSources(ctx, effective, team.ProjectSource != nil)
		if err != nil {
			return result, err
		}
		defer cleanup()
	}
	composed, err := compose.Compose(cfg, team, compose.Options{
		Sources: sources,
		Secrets: recorded,
	})
	if err != nil {
		return result, fmt.Errorf("preflight composition: %w", err)
	}
	if len(composed.Pairs) == 0 {
		return result, errors.New("preflight: team selects no role/harness pairs")
	}
	// Encode and validate local entry publication before any daemon mutation.
	entryBytes, err := yaml.Marshal(composed.Entry)
	if err != nil {
		return result, fmt.Errorf("encode TeamEntry: %w", err)
	}
	stage, err := stageEntry(entryPath, entryBytes)
	if err != nil {
		return result, err
	}
	defer func() { _ = os.Remove(stage) }()
	client := opts.Client
	if client == nil {
		host, hostErr := resolveHost(opts)
		if hostErr != nil {
			return result, hostErr
		}
		client, err = kukeonv1.Dial(ctx, host)
		if err != nil {
			return result, fmt.Errorf("kukeond connection configuration: %w", err)
		}
		defer func() { _ = client.Close() }()
	}
	done, err := apply(ctx, client, composed, command, opts.legacyTeam)
	if err != nil {
		if !strings.HasPrefix(command, "kuke team init") {
			return result, fmt.Errorf(
				"%w; after fixing the prerequisite, rerun %s with the same revision flags",
				err,
				command,
			)
		}
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, fmt.Errorf("resources applied but registration unchanged: %w; rerun %s", err, command)
	}
	// The replacement is applied, so removing the legacy team's configs can no
	// longer leave the host with neither team: a preflight or apply failure
	// above returned with them intact.
	if opts.legacyTeam != "" {
		if err = removeTeamConfigs(ctx, client, opts.legacyTeam); err != nil {
			return result, err
		}
	}
	if err = os.Rename(stage, entryPath); err != nil {
		return result, fmt.Errorf(
			"resources applied but registration failed at %s: %w; rerun %s",
			entryPath,
			err, command,
		)
	}
	result.EntryPath, result.Resources = entryPath, done.resources
	result.MissingSecrets = done.missing
	result.Notes = append(slices.Clone(composed.Notes), done.notes...)
	return result, nil
}

// readEntrySecrets returns the bindings recorded by a previous registration so
// operator edits to them survive recomposition. An absent entry records none.
func readEntrySecrets(path string) ([]config.SecretBinding, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read team registration %s: %w", path, err)
	}
	entry, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("read team registration %s: %w", path, err)
	}
	return entry.Secrets, nil
}

func readNamedTeam(path, name string) (*config.Team, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &config.Team{
			APIVersion: config.APIVersion,
			Kind:       config.KindTeam,
			Metadata:   config.Metadata{Name: name},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read team registration %s: %w", path, err)
	}
	entry, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("read team registration %s: %w", path, err)
	}
	if entry.Metadata.Name != name {
		return nil, errors.New("registration must match the requested team name")
	}
	return entry.Team(), nil
}

func stageEntry(path string, data []byte) (string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("prepare team registration: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+strings.TrimSuffix(filepath.Base(path), ".yaml")+"-*.yaml")
	if err != nil {
		return "", fmt.Errorf("stage team registration: %w", err)
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("stage team registration: %w", err)
	}
	return f.Name(), nil
}
