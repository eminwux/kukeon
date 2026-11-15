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

package config

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"
)

func blank(s string) bool { return strings.TrimSpace(s) == "" }
func validUTF8(field, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s: must be valid UTF-8", field)
	}
	return nil
}

func required(field, value string) error {
	if err := validUTF8(field, value); err != nil {
		return err
	}
	if blank(value) {
		return fmt.Errorf("%s: must be nonblank", field)
	}
	return nil
}

// Validate checks structural validity and global references without mutating cfg.
// It does not require signing keys or source paths to exist on this host.
func Validate(cfg *TeamsConfig) error {
	if cfg == nil {
		return errors.New("config: must not be nil")
	}
	if cfg.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion: expected %q", APIVersion)
	}
	if cfg.Kind != Kind {
		return fmt.Errorf("kind: expected %q", Kind)
	}
	if err := validateGit(cfg.Spec.Git); err != nil {
		return err
	}
	if err := validateBindings(&cfg.Spec); err != nil {
		return err
	}
	return validateDefaults(&cfg.Spec.Defaults)
}

func validateGit(g *Git) error {
	if g == nil {
		return nil
	}
	for _, optional := range []struct{ field, value string }{
		{"spec.git.signingKey", g.SigningKey},
		{"spec.git.allowedSigners", g.AllowedSigners},
	} {
		if err := validUTF8(optional.field, optional.value); err != nil {
			return err
		}
	}
	for _, entry := range []struct {
		name     string
		identity *Identity
	}{
		{"author", g.Author}, {"committer", g.Committer},
	} {
		if entry.identity == nil {
			continue
		}
		if err := required("spec.git."+entry.name+".name", entry.identity.Name); err != nil {
			return err
		}
		if err := required("spec.git."+entry.name+".email", entry.identity.Email); err != nil {
			return err
		}
	}
	for i, sign := range g.Sign {
		if sign != "commits" && sign != "tags" {
			return fmt.Errorf("spec.git.sign[%d]: expected commits or tags", i)
		}
	}
	if len(g.Sign) > 0 {
		if err := required("spec.git.signingKey", g.SigningKey); err != nil {
			return err
		}
	}
	return nil
}

func validateSources(list []Source, sources map[string]bool) error {
	targets := map[string]bool{}
	for i, source := range list {
		field := fmt.Sprintf("spec.defaults.sources[%d]", i)
		if err := unique(field+".name", source.Name, sources); err != nil {
			return err
		}
		if err := required(field+".url", source.URL); err != nil {
			return err
		}
		if source.ClonePath != "" {
			if !ValidName(source.Name) || !SafePath(source.ClonePath) {
				return fmt.Errorf("%s.clonePath: require a safe source name and absolute in-cell path", field)
			}
			if targets[source.ClonePath] {
				return fmt.Errorf("%s.clonePath: duplicate clone destination", field)
			}
			targets[source.ClonePath] = true
		}
		count := 0
		for _, selector := range []struct{ name, value string }{
			{"branch", source.Branch}, {"tag", source.Tag}, {"commit", source.Commit},
		} {
			if selector.value == "" {
				continue
			}
			if err := required(field+"."+selector.name, selector.value); err != nil {
				return err
			}
			count++
		}
		if count != 1 {
			return fmt.Errorf("%s: require exactly one nonblank revision selector (branch, tag, commit)", field)
		}
	}
	return nil
}

func validateDefaults(d *Defaults) error {
	return validateSelections(d)
}

func validateSelections(d *Defaults) error {
	sources, roles, harnesses := map[string]bool{}, map[string]bool{}, map[string]bool{}
	if err := validateSources(d.Sources, sources); err != nil {
		return err
	}
	for i, h := range d.Harnesses {
		field := fmt.Sprintf("spec.defaults.harnesses[%d]", i)
		if err := unique(field+".name", h.Name, harnesses); err != nil {
			return err
		}
		if err := selection(field, h.Source, h.Path, sources); err != nil {
			return err
		}
	}
	for i, role := range d.Roles {
		field := fmt.Sprintf("spec.defaults.roles[%d]", i)
		if err := unique(field+".name", role.Name, roles); err != nil {
			return err
		}
		if err := selection(field, role.Source, role.Path, sources); err != nil {
			return err
		}
		if err := validateInitialPath(field+".initialPath", role.InitialPath); err != nil {
			return err
		}
		if role.Harness != "" && !harnesses[role.Harness] {
			return fmt.Errorf("%s.harness: unknown harness %q", field, role.Harness)
		}
		if role.Needs != nil {
			for j, image := range role.Needs.Image {
				if err := required(fmt.Sprintf("%s.needs.image[%d]", field, j), image); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func unique(field, name string, names map[string]bool) error {
	if err := required(field, name); err != nil {
		return err
	}
	if names[name] {
		return fmt.Errorf("%s: duplicate name %q", field, name)
	}
	names[name] = true
	return nil
}

func selection(field, source, definition string, sources map[string]bool) error {
	if err := required(field+".source", source); err != nil {
		return err
	}
	if !sources[source] {
		return fmt.Errorf("%s.source: unknown source %q", field, source)
	}
	if err := required(field+".path", definition); err != nil {
		return err
	}
	// Paths use repository (slash) syntax regardless of the operator's OS.
	// Reject backslashes and drive/URI prefixes to avoid platform-dependent escapes.
	clean := path.Clean(definition)
	if path.IsAbs(definition) || clean == ".." || strings.HasPrefix(clean, "../") ||
		strings.ContainsAny(definition, "\\:\x00") {
		return fmt.Errorf("%s.path: must be a nonempty repository-relative path within the source root", field)
	}
	return nil
}

// validateInitialPath allows only literal clean absolute paths and a leading
// $HOME token. No general shell expansion is performed.
func validateInitialPath(field, value string) error {
	if err := validUTF8(field, value); err != nil {
		return err
	}
	if value == "" || value == "$HOME" {
		return nil
	}
	suffix, home := strings.CutPrefix(value, "$HOME/")
	if home {
		value = "/" + suffix
	}
	if value == "/" && !home || SafePath(value) {
		return nil
	}
	return fmt.Errorf(
		"%s: use a clean absolute Unix path, $HOME, or $HOME/subdirectory (letters, digits, _, ., / and - only)",
		field,
	)
}
