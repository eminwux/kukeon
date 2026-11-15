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

// Package bootstrap initializes operator configuration without composing teams.
package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/pkg/config"
)

const directoryMode = 0o700

// Options supplies the operator configuration directory and global Git reader.
// Root must be absolute. A nil Git reader selects the real Git subprocess reader.
type Options struct {
	Root  string
	Git   GitReader
	Force bool
}

// Result describes the persisted configuration, including a competing creator's
// document when that invocation won publication. Diagnostics contain no key data.
type Result struct {
	Path        string
	Created     bool
	Config      *config.TeamsConfig
	Diagnostics []string
}

// Run preserves an existing valid configuration or creates the initial document.
// Force replaces the document with the initial configuration.
// It never prints, exits, fetches sources, or registers a team.
func Run(ctx context.Context, opts Options) (Result, error) {
	return run(ctx, opts, localFiles())
}

func run(ctx context.Context, opts Options, files fileOps) (Result, error) {
	if !filepath.IsAbs(opts.Root) {
		return Result{}, errors.New("bootstrap: configuration root must be absolute")
	}
	path := filepath.Join(opts.Root, "kuketeams.yaml")
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("bootstrap %s: %w", path, err)
	}
	if !opts.Force {
		existing, err := load(path, files.readFile)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Result{}, err
		}
	}

	reader := opts.Git
	if reader == nil {
		reader = CommandGit{}
	}
	git, diagnostics, err := discoverGit(ctx, reader)
	if err != nil {
		return Result{}, fmt.Errorf("bootstrap %s: %w", path, err)
	}
	cfg := initialConfig(git)
	var encoded bytes.Buffer
	if err = config.Encode(&encoded, cfg); err != nil {
		return Result{}, fmt.Errorf("bootstrap %s: encode initial configuration: %w", path, err)
	}
	if err = ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("bootstrap %s: %w", path, err)
	}
	if err = files.mkdirAll(opts.Root, directoryMode); err != nil {
		return Result{}, fmt.Errorf("bootstrap %s: create configuration directory: %w", path, err)
	}
	if opts.Force {
		err = replace(ctx, path, encoded.Bytes(), files)
	} else {
		err = publish(ctx, path, encoded.Bytes(), files)
	}
	if err != nil {
		if !opts.Force && errors.Is(err, os.ErrExist) {
			return load(path, files.readFile)
		}
		return Result{}, fmt.Errorf("bootstrap %s: publish configuration: %w", path, err)
	}
	return Result{Path: path, Created: true, Config: cfg, Diagnostics: diagnostics}, nil
}

func load(path string, readFile func(string) ([]byte, error)) (Result, error) {
	data, err := readFile(path)
	if err != nil {
		return Result{}, fmt.Errorf("read configuration %s: %w", path, err)
	}
	cfg, err := config.Decode(bytes.NewReader(data))
	if err != nil {
		return Result{}, fmt.Errorf("validate configuration %s: %w", path, err)
	}
	return Result{Path: path, Config: cfg}, nil
}

func initialConfig(git *config.Git) *config.TeamsConfig {
	return &config.TeamsConfig{
		APIVersion: config.APIVersion, Kind: config.Kind,
		Spec: config.Spec{
			Git: git,
		},
	}
}
