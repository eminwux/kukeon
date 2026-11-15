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

// Package initialize applies the operator team's configuration without managing cells.
package initialize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/bootstrap"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/compose"
	"github.com/eminwux/kukeon/pkg/config"
	"gopkg.in/yaml.v3"
)

const (
	operatorName     = "kukeon"
	rootName         = "root" // Reserved legacy identity; never migrated by init.
	resourcesPerPair = 2      // One blueprint and one config.
)

// Options supplies operator paths and optional test/embedding dependencies.
// An injected Client remains caller-owned; otherwise Run opens and closes it.
type Options struct {
	Root, Home, Host, Configuration string
	Force                           bool
	Sources                         map[string]fs.FS
	Client                          Client
}

type Result struct {
	Bootstrap bootstrap.Result
	EntryPath string
	Resources int
}

// Run initializes only when configuration is absent, unless Force resets it.
// Partial daemon application is not rolled back; a forced rerun converges the team.
func Run(ctx context.Context, opts Options) (Result, error) {
	configPath := filepath.Join(opts.Root, "kuketeams.yaml")
	if !opts.Force {
		if _, err := os.Lstat(configPath); err == nil {
			return Result{}, fmt.Errorf("init: configuration already exists at %s; use --force to reset it", configPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Result{}, fmt.Errorf("init: inspect configuration %s: %w", configPath, err)
		}
	}
	boot, err := bootstrap.Run(ctx, bootstrap.Options{Root: opts.Root, Force: opts.Force})
	result := Result{Bootstrap: boot}
	if err != nil {
		return result, err
	}
	if !opts.Force && !boot.Created {
		return result, fmt.Errorf("init: configuration already exists at %s; use --force to reset it", configPath)
	}
	entryPath := filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml")
	team := config.Operator()
	if !opts.Force {
		team, err = readTeam(entryPath)
		if err != nil {
			return result, err
		}
	}
	return runTeam(ctx, opts, boot.Config, team, result, "kuke team init")
}

// runTeam is shared by init and add; staging precedes all daemon writes.
func runTeam(
	ctx context.Context,
	opts Options,
	cfg *config.TeamsConfig,
	team *config.Team,
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
		Sources: sources, HomeDir: opts.Home, TeamDir: filepath.Join(opts.Root, "teams", name),
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
	if err = apply(ctx, client, composed, command); err != nil {
		if command != "kuke team init" {
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
	if err = os.Rename(stage, entryPath); err != nil {
		return result, fmt.Errorf(
			"resources applied but registration failed at %s: %w; rerun %s",
			entryPath,
			err, command,
		)
	}
	result.EntryPath, result.Resources = entryPath, len(composed.Pairs)*resourcesPerPair
	return result, nil
}

func readTeam(path string) (*config.Team, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return config.Operator(), nil
	}
	return readNamedTeam(path, operatorName)
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
