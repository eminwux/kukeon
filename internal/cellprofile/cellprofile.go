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

// Package cellprofile loads per-user CellProfile templates from
// $HOME/.kuke/profiles.d/*.yaml (or $KUKE_PROFILES_DIR) and materializes them
// into CellDocs that `kuke run -p` then drives along the same path as `-f`.
package cellprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// EnvProfilesDir is the env var that overrides the default user profiles
// directory. Picked up by ResolveDir; takes precedence over $HOME/.kuke/profiles.d.
const EnvProfilesDir = "KUKE_PROFILES_DIR"

// DefaultDirSuffix is appended to $HOME when KUKE_PROFILES_DIR is unset.
// Matches sbsh's `~/.sbsh/profiles.d` layout.
const DefaultDirSuffix = ".kuke/profiles.d"

// ResolveDir returns the active profiles directory: $KUKE_PROFILES_DIR if set,
// otherwise $HOME/.kuke/profiles.d. The directory is not required to exist —
// callers either tolerate ENOENT (List) or surface ProfileNotFound (Load).
func ResolveDir() (string, error) {
	if dir := strings.TrimSpace(os.Getenv(EnvProfilesDir)); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, DefaultDirSuffix), nil
}

// Load reads $dir/<name>.yaml (or its .yml sibling) and returns the parsed
// profile. When neither exists, it falls back to scanning every *.yaml /
// *.yml file in the directory and matching on metadata.name — the file basename
// is the convenient case but not authoritative, so two profiles can share a
// directory regardless of how they were named on disk.
//
// Wraps errdefs.ErrProfileNotFound when the name does not resolve, including
// the directory in the error so the operator knows where to drop the file.
func Load(dir, name string) (*v1beta1.CellProfileDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: empty name", errdefs.ErrProfileInvalid)
	}

	for _, ext := range []string{".yaml", ".yml"} {
		candidate := filepath.Join(dir, name+ext)
		if _, statErr := os.Stat(candidate); statErr == nil {
			profile, err := loadFile(candidate)
			if err != nil {
				return nil, err
			}
			if matchesName(profile, name) {
				return profile, nil
			}
		}
	}

	profiles, err := List(dir)
	if err != nil {
		if errors.Is(err, errdefs.ErrProfileNotFound) {
			return nil, fmt.Errorf("profile %q not found in %s: %w", name, dir, errdefs.ErrProfileNotFound)
		}
		return nil, err
	}
	for _, p := range profiles {
		if p.Metadata.Name == name {
			out := p
			return &out, nil
		}
	}
	return nil, fmt.Errorf("profile %q not found in %s: %w", name, dir, errdefs.ErrProfileNotFound)
}

// List returns every parseable CellProfile in dir, sorted by metadata.name.
// Used by both the `-p` autocomplete handler and the fallback name lookup in
// Load. Files that fail to parse are skipped silently — the operator finds
// out via Load when they actually try to use the broken profile.
func List(dir string) ([]v1beta1.CellProfileDoc, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read profiles dir %q: %w", dir, err)
	}

	out := make([]v1beta1.CellProfileDoc, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}
		profile, loadErr := loadFile(filepath.Join(dir, name))
		if loadErr != nil {
			continue
		}
		out = append(out, *profile)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Metadata.Name < out[j].Metadata.Name
	})
	return out, nil
}

func loadFile(path string) (*v1beta1.CellProfileDoc, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	var profile v1beta1.CellProfileDoc
	if unmarshalErr := yaml.Unmarshal(raw, &profile); unmarshalErr != nil {
		return nil, fmt.Errorf("parse profile %q: %w: %w", path, errdefs.ErrProfileInvalid, unmarshalErr)
	}
	if profile.Kind != v1beta1.KindCellProfile {
		return nil, fmt.Errorf(
			"profile %q has kind %q, want %q: %w",
			path, profile.Kind, v1beta1.KindCellProfile, errdefs.ErrProfileInvalid,
		)
	}
	if strings.TrimSpace(profile.Metadata.Name) == "" {
		return nil, fmt.Errorf("profile %q: metadata.name is required: %w", path, errdefs.ErrProfileInvalid)
	}
	return &profile, nil
}

func matchesName(profile *v1beta1.CellProfileDoc, name string) bool {
	return profile != nil && profile.Metadata.Name == name
}

// Materialize converts a CellProfile into a CellDoc suitable for the same
// path `kuke run -f` drives. The cell name comes from cellNameOverride if
// non-empty, else the profile's metadata.name; the realm/space/stack triple
// is taken from the profile spec verbatim (callers layer --realm/--space/--stack
// flag overrides separately, mirroring the existing -f resolution).
func Materialize(profile *v1beta1.CellProfileDoc, cellNameOverride string) v1beta1.CellDoc {
	cellName := strings.TrimSpace(cellNameOverride)
	if cellName == "" {
		cellName = profile.Metadata.Name
	}

	spec := profile.Spec.Cell
	spec.RealmID = strings.TrimSpace(profile.Spec.Realm)
	spec.SpaceID = strings.TrimSpace(profile.Spec.Space)
	spec.StackID = strings.TrimSpace(profile.Spec.Stack)
	if strings.TrimSpace(spec.ID) == "" {
		spec.ID = cellName
	}

	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   cellName,
			Labels: cloneLabels(profile.Metadata.Labels),
		},
		Spec: spec,
	}
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
