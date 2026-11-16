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
	if err := validateIdentities("spec.git", g.Author, g.Committer); err != nil {
		return err
	}
	hosts := map[string]bool{}
	for i, credential := range g.Credentials {
		field := fmt.Sprintf("spec.git.credentials[%d]", i)
		if err := unique(field+".host", credential.Host, hosts); err != nil {
			return err
		}
		if strings.ContainsAny(credential.Host, " \t\r\n@") || strings.Contains(credential.Host, "://") {
			return fmt.Errorf("%s.host: use a bare host or host/path prefix (no scheme or user)", field)
		}
		if err := validateCredential(field, credential.SecretRef, credential.Port, credential.HostKeyPolicy); err != nil {
			return err
		}
	}
	if len(g.Sign) > 0 && g.Signing == nil && blank(g.SigningKey) {
		return errors.New("spec.git.signing: sign requires a stored signing secret (spec.git.signing.secretRef)")
	}
	return validateSigning("spec.git", g.Signing, g.Sign)
}

func validateIdentities(field string, author, committer *Identity) error {
	for _, entry := range []struct {
		name     string
		identity *Identity
	}{
		{"author", author}, {"committer", committer},
	} {
		if entry.identity == nil {
			continue
		}
		if err := required(field+"."+entry.name+".name", entry.identity.Name); err != nil {
			return err
		}
		if err := required(field+"."+entry.name+".email", entry.identity.Email); err != nil {
			return err
		}
	}
	return nil
}

func validateCredential(field string, ref SecretRef, port int, policy string) error {
	if err := validateSecretRef(field+".secretRef", ref); err != nil {
		return err
	}
	if port < 0 || port > 65535 {
		return fmt.Errorf("%s.port: expected 1-65535", field)
	}
	if policy != "" && policy != "strict" && policy != "accept-new" {
		return fmt.Errorf("%s.hostKeyPolicy: expected strict or accept-new", field)
	}
	return nil
}

// validateSigning checks sign selections and the stored signing secret.
func validateSigning(field string, signing *GitSigning, sign []string) error {
	for i, selection := range sign {
		if selection != "commits" && selection != "tags" {
			return fmt.Errorf("%s.sign[%d]: expected commits or tags", field, i)
		}
	}
	if signing == nil {
		return nil
	}
	if err := validateSecretRef(field+".signing.secretRef", signing.SecretRef); err != nil {
		return err
	}
	if signing.AllowedSignersRef != nil {
		return validateSecretRef(field+".signing.allowedSignersRef", *signing.AllowedSignersRef)
	}
	return nil
}

func validateSecretRef(field string, ref SecretRef) error {
	if !ValidName(ref.Name) || !ValidName(ref.Realm) ||
		(ref.Space != "" && !ValidName(ref.Space)) || (ref.Stack != "" && !ValidName(ref.Stack)) ||
		(ref.Cell != "" && !ValidName(ref.Cell)) {
		return fmt.Errorf("%s: require a valid secret name and scope", field)
	}
	if (ref.Stack != "" && ref.Space == "") || (ref.Cell != "" && ref.Stack == "") {
		return fmt.Errorf("%s: deeper scope requires all parent coordinates", field)
	}
	return nil
}

func validateSourceGit(field string, g *SourceGit) error {
	if g == nil {
		return nil
	}
	if err := validateIdentities(field, g.Author, g.Committer); err != nil {
		return err
	}
	if c := g.Credential; c != nil {
		if err := validateCredential(field+".credential", c.SecretRef, c.Port, c.HostKeyPolicy); err != nil {
			return err
		}
	}
	return validateSigning(field, g.Signing, g.Sign)
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
		if err := validateSourceGit(field+".git", source.Git); err != nil {
			return err
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
		if role.Tasks != nil {
			tasks := map[string]bool{}
			for j, task := range *role.Tasks {
				taskField := fmt.Sprintf("%s.tasks[%d]", field, j)
				if err := unique(taskField, task, tasks); err != nil {
					return err
				}
				if !ValidName(task) {
					return fmt.Errorf("%s: invalid task name %q", taskField, task)
				}
			}
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
