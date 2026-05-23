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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// LabelProfile is the cell label that records the CellProfile a cell was
// materialized from. Set on every cell produced by Materialize so operators
// can list all instances with `kuke get cells -l kukeon.io/profile=<name>`.
const LabelProfile = "kukeon.io/profile"

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
// `${KEY}` references in the body remain literal — substitution is a `kuke
// run -p`-time concern. Use LoadResolved when the caller has --param values
// to apply.
//
// Wraps errdefs.ErrProfileNotFound when the name does not resolve, including
// the directory in the error so the operator knows where to drop the file.
func Load(dir, name string) (*v1beta1.CellProfileDoc, error) {
	profile, _, err := locate(dir, name)
	return profile, err
}

// locate is the shared core for Load and LoadResolved: resolve the named
// profile to a file, parse it, and return both the typed profile and the raw
// yaml.Node tree. The node lets LoadResolved substitute scalars in the same
// document tree the caller will eventually decode against — bypassing a YAML
// → struct → YAML round-trip that would lose comments and re-quote scalars.
func locate(dir, name string) (*v1beta1.CellProfileDoc, *yaml.Node, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil, fmt.Errorf("%w: empty name", errdefs.ErrProfileInvalid)
	}

	for _, ext := range []string{".yaml", ".yml"} {
		candidate := filepath.Join(dir, name+ext)
		if _, statErr := os.Stat(candidate); statErr == nil {
			profile, node, err := loadFile(candidate)
			if err != nil {
				return nil, nil, err
			}
			if matchesName(profile, name) {
				return profile, node, nil
			}
		}
	}

	// Fallback: basename did not match. Scan the directory and return the
	// first parseable profile whose metadata.name equals `name`. We re-read
	// the matching file so the caller gets a fresh node tree (List discards
	// nodes by design).
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, fmt.Errorf("profile %q not found in %s: %w", name, dir, errdefs.ErrProfileNotFound)
		}
		return nil, nil, fmt.Errorf("read profiles dir %q: %w", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		filename := e.Name()
		if !strings.HasSuffix(filename, ".yaml") && !strings.HasSuffix(filename, ".yml") {
			continue
		}
		path := filepath.Join(dir, filename)
		profile, node, loadErr := loadFile(path)
		if loadErr != nil {
			continue
		}
		if profile.Metadata.Name == name {
			return profile, node, nil
		}
	}
	return nil, nil, fmt.Errorf("profile %q not found in %s: %w", name, dir, errdefs.ErrProfileNotFound)
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
		profile, _, loadErr := loadFile(filepath.Join(dir, name))
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

// loadFile parses a single profile YAML and returns both the typed profile
// and the underlying yaml.Node tree. The node is needed by LoadResolved to
// substitute `${KEY}` references in scalar values without round-tripping
// through marshal/unmarshal. Validation runs here so any caller (Load,
// LoadResolved, the List fallback) gets the same shape rejections.
func loadFile(path string) (*v1beta1.CellProfileDoc, *yaml.Node, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read profile %q: %w", path, err)
	}
	var node yaml.Node
	if unmarshalErr := yaml.Unmarshal(raw, &node); unmarshalErr != nil {
		return nil, nil, fmt.Errorf("parse profile %q: %w: %w", path, errdefs.ErrProfileInvalid, unmarshalErr)
	}
	var profile v1beta1.CellProfileDoc
	if decodeErr := node.Decode(&profile); decodeErr != nil {
		return nil, nil, fmt.Errorf("parse profile %q: %w: %w", path, errdefs.ErrProfileInvalid, decodeErr)
	}
	if profile.Kind != v1beta1.KindCellProfile {
		return nil, nil, fmt.Errorf(
			"profile %q has kind %q, want %q: %w",
			path, profile.Kind, v1beta1.KindCellProfile, errdefs.ErrProfileInvalid,
		)
	}
	if strings.TrimSpace(profile.Metadata.Name) == "" {
		return nil, nil, fmt.Errorf("profile %q: metadata.name is required: %w", path, errdefs.ErrProfileInvalid)
	}
	if strings.TrimSpace(profile.Spec.Cell.ID) != "" {
		// Every materialized cell gets a generated `<prefix>-<6hex>` name; a
		// hardcoded spec.cell.id would silently produce N cells sharing one ID.
		// Reject loudly so the operator removes the stray field instead.
		return nil, nil, fmt.Errorf(
			"profile %q: spec.cell.id must not be set on a CellProfile: %w",
			path, errdefs.ErrProfileInvalid,
		)
	}
	if validateErr := validateParameters(&profile, &node, path); validateErr != nil {
		return nil, nil, validateErr
	}
	return &profile, &node, nil
}

func matchesName(profile *v1beta1.CellProfileDoc, name string) bool {
	return profile != nil && profile.Metadata.Name == name
}

// Materialize converts a CellProfile into a CellDoc suitable for the same
// path `kuke run -f` drives. CellProfile is always a template: every call
// returns a cell named `<prefix>-<6hex>`, where prefix is spec.prefix when set
// and metadata.name otherwise. Singleton workloads belong on the Cell kind.
// The realm/space/stack triple is taken from the profile spec verbatim —
// callers layer --realm/--space/--stack flag overrides separately, mirroring
// the existing -f resolution. Every produced cell also carries the
// kukeon.io/profile=<metadata.name> label so the set of cells materialized
// from a profile is queryable via `kuke get cells -l`.
func Materialize(profile *v1beta1.CellProfileDoc) (v1beta1.CellDoc, error) {
	return MaterializeWithName(profile, "")
}

// MaterializeWithName is Materialize with an explicit cell-name override —
// used by `kuke run -p ... --name <override>`. When nameOverride is non-empty
// (after trim) it replaces the generated `<prefix>-<6hex>` name verbatim, so
// callers can give the cell a deterministic identity (e.g., the orchestrator
// dispatching `crew-dev-354` so it can later attach by name). When empty,
// behavior matches Materialize.
func MaterializeWithName(profile *v1beta1.CellProfileDoc, nameOverride string) (v1beta1.CellDoc, error) {
	cellName, err := resolveCellName(profile, nameOverride)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	spec := profile.Spec.Cell
	spec.RealmID = strings.TrimSpace(profile.Spec.Realm)
	spec.SpaceID = strings.TrimSpace(profile.Spec.Space)
	spec.StackID = strings.TrimSpace(profile.Spec.Stack)
	spec.ID = cellName

	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   cellName,
			Labels: mergeLabels(profile.Metadata.Labels, LabelProfile, profile.Metadata.Name),
		},
		Spec: spec,
	}, nil
}

func resolveCellName(profile *v1beta1.CellProfileDoc, nameOverride string) (string, error) {
	if override := strings.TrimSpace(nameOverride); override != "" {
		return override, nil
	}
	prefix := strings.TrimSpace(profile.Spec.Prefix)
	if prefix == "" {
		prefix = profile.Metadata.Name
	}
	suffix, err := naming.RandomHexSuffix(naming.DefaultCellNameSuffixBytes)
	if err != nil {
		return "", fmt.Errorf("generate name suffix for profile %q: %w", profile.Metadata.Name, err)
	}
	return prefix + "-" + suffix, nil
}

func mergeLabels(in map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for lk, lv := range in {
		out[lk] = lv
	}
	out[k] = v
	return out
}
