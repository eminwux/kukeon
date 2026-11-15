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

// Package harnessbuild derives Buildx Bake graphs from authored Harness images.
package harnessbuild

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/pkg/team/harness"
)

const baseName = "base"

type Target struct {
	Context    string            `json:"context"`
	Dockerfile string            `json:"dockerfile"`
	Args       map[string]string `json:"args"`
	Platforms  []string          `json:"platforms"`
	Tags       []string          `json:"tags"`
	Contexts   map[string]string `json:"contexts,omitempty"`
	Output     []string          `json:"output"`
}

type Group struct {
	Targets []string `json:"targets"`
}
type Plan struct {
	Group  map[string]Group  `json:"group"`
	Target map[string]Target `json:"target"`
}

// Definitions returns packaged declarations in path order, rejecting global
// image-name collisions before any build or catalog write.
func Definitions(source fs.FS) (map[string]*harness.Harness, []string, error) {
	files, err := fs.Glob(source, "harnesses/*/harness.yaml")
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, errors.New("harnesses: no harness.yaml definitions")
	}
	definitions := map[string]*harness.Harness{}
	directories := []string{}
	names := map[string]bool{baseName: true, "base-user": true, "base-root": true}
	identities := map[string]bool{}
	for _, filename := range files {
		directory := path.Dir(filename)
		h, loadErr := harness.Load(source, directory)
		if loadErr != nil {
			return nil, nil, loadErr
		}
		if identities[h.Metadata.Name] {
			return nil, nil, fmt.Errorf("%s: duplicate Harness identity %q", filename, h.Metadata.Name)
		}
		identities[h.Metadata.Name] = true
		if namesErr := uniqueNames(h, names, filename); namesErr != nil {
			return nil, nil, namesErr
		}
		definitions[directory] = h
		directories = append(directories, directory)
	}
	return definitions, directories, nil
}

// Build selects targets while retaining their linked base dependency closure.
// Contexts stay repository-relative; callers execute Bake from the source root.
func Build(source fs.FS, directory, registry, tag, platforms string, subset []string) (*Plan, error) {
	definitions, directories, err := Definitions(source)
	if err != nil {
		return nil, err
	}
	if directory != "" && directory != baseName && definitions[directory] == nil {
		return nil, fmt.Errorf("unknown harness path %q", directory)
	}
	images := baseImages()
	owners := map[string]string{}
	for _, dir := range directories {
		for _, image := range definitions[dir].Spec.Images {
			images = append(images, image)
			owners[image.Name] = dir
		}
	}
	selected, err := selectNames(images, owners, directory, subset)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if registry != "" {
		prefix = registry + "/"
	}
	plan := &Plan{Group: map[string]Group{"default": {Targets: selected}}, Target: map[string]Target{}}
	for _, image := range images {
		target, targetErr := buildTarget(
			source,
			image.Build.Context,
			image.Build.Dockerfile,
			image.Name,
			registry,
			tag,
			prefix,
			platforms,
		)
		if targetErr != nil {
			return nil, fmt.Errorf("image %q (%s): %w", image.Name, image.Build.Context, targetErr)
		}
		plan.Target[image.Name] = target
	}
	return plan, nil
}

func baseImages() []harness.Image {
	images := []harness.Image{}
	for _, name := range []string{baseName, "base-user", "base-root"} {
		dockerfile := "Dockerfile"
		if name != baseName {
			dockerfile += "." + strings.TrimPrefix(name, "base-")
		}
		images = append(
			images,
			harness.Image{Name: name, Build: harness.Build{Context: "harnesses/base", Dockerfile: dockerfile}},
		)
	}
	return images
}

func uniqueNames(h *harness.Harness, names map[string]bool, filename string) error {
	for _, image := range h.Spec.Images {
		if names[image.Name] {
			return fmt.Errorf("%s: duplicate image name %q", filename, image.Name)
		}
		names[image.Name] = true
	}
	return nil
}

func selectNames(
	images []harness.Image,
	owners map[string]string,
	directory string,
	subset []string,
) ([]string, error) {
	known := map[string]bool{}
	selected := []string{}
	for _, image := range images {
		known[image.Name] = true
		owner := owners[image.Name]
		if owner == "" {
			owner = baseName
		}
		if directory != "" && directory != owner {
			continue
		}
		if len(subset) > 0 && !slices.Contains(subset, image.Name) {
			continue
		}
		selected = append(selected, image.Name)
	}
	for _, name := range subset {
		if !known[name] {
			return nil, fmt.Errorf("unknown image subset %q", name)
		}
	}
	return selected, nil
}

func buildTarget(source fs.FS, context, dockerfile, name, registry, tag, prefix, platforms string) (Target, error) {
	ref, err := harness.Reference(registry, name, tag)
	if err != nil {
		return Target{}, err
	}
	raw, err := fs.ReadFile(source, path.Join(context, dockerfile))
	if err != nil {
		return Target{}, err
	}
	target := Target{
		Context: context, Dockerfile: dockerfile, Tags: []string{ref}, Platforms: strings.Split(platforms, ","),
		Args: map[string]string{
			"REGISTRY":     registry,
			"TAG":          tag,
			"IMAGE_PREFIX": prefix,
		}, Output: []string{"type=cacheonly"},
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "FROM" || !strings.HasPrefix(fields[1], "${IMAGE_PREFIX}base") {
			continue
		}
		base := strings.TrimSuffix(strings.TrimPrefix(fields[1], "${IMAGE_PREFIX}"), ":${TAG}")
		if base != baseName && base != "base-user" && base != "base-root" {
			return Target{}, fmt.Errorf("unknown shared base %q", base)
		}
		baseRef, refErr := harness.Reference(registry, base, tag)
		if refErr != nil {
			return Target{}, refErr
		}
		// BuildKit normalizes :latest away for named context lookup.
		target.Contexts = map[string]string{strings.TrimSuffix(baseRef, ":latest"): "target:" + base}
	}
	return target, nil
}
