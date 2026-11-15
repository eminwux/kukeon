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

// Command images generates release catalogs or a transient Buildx Bake plan.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/team/harnessbuild"
	"github.com/eminwux/kukeon/pkg/team/harness"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected catalog or plan")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	registry := flags.String(
		"registry",
		env("REGISTRY", "ghcr.io/eminwux/kukeon"),
		"registry prefix (empty for local names)",
	)
	tag := flags.String("tag", env("TAG", "latest"), "image tag")
	platforms := flags.String("platforms", env("PLATFORMS", "linux/amd64"), "platform list")
	directory := flags.String("harness", "", "selected harness directory, or base")
	check := flags.Bool("check", false, "check catalogs without writing")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	source := os.DirFS(".")
	switch args[0] {
	case "plan":
		plan, err := harnessbuild.Build(source, *directory, *registry, *tag, *platforms, flags.Args())
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(plan)
	case "catalog":
		if flags.NArg() != 0 {
			return fmt.Errorf("unexpected catalog arguments: %v", flags.Args())
		}
		return writeCatalogs(*registry, *tag, *check)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func writeCatalogs(registry, tag string, check bool) error {
	source := os.DirFS(".")
	definitions, dirs, err := harnessbuild.Definitions(source)
	if err != nil {
		return err
	}
	// Render everything before writing anything, so invalid declarations
	// cannot leave a partially updated release set.
	rendered := map[string][]byte{}
	for _, dir := range dirs {
		catalog, genErr := harness.Generate(definitions[dir], registry, tag)
		if genErr != nil {
			return fmt.Errorf("%s/harness.yaml: %w", dir, genErr)
		}
		raw, marshalErr := harness.MarshalCatalog(catalog)
		if marshalErr != nil {
			return marshalErr
		}
		rendered[filepath.Join(dir, "images.yaml")] = raw
	}
	drift := []string{}
	for _, dir := range dirs {
		filename := filepath.Join(dir, "images.yaml")
		if check {
			current, readErr := os.ReadFile(filename)
			if readErr != nil || !bytes.Equal(current, rendered[filename]) {
				drift = append(drift, filename)
			}
		} else if writeErr := os.WriteFile(filename, rendered[filename], 0o644); writeErr != nil { //nolint:gosec // Release catalogs are public source artifacts.
			return writeErr
		}
	}
	if len(drift) > 0 {
		return fmt.Errorf(
			"catalog drift: %s; run REGISTRY=%q ./harnesses/release-images.sh %s, then commit/merge the catalogs before tagging",
			strings.Join(drift, ", "),
			registry,
			tag,
		)
	}
	return nil
}
