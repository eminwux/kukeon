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
	"maps"
	"path"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/eminwux/kukeon/pkg/api/version"
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
func Validate(cfg *TeamDefaults) error {
	if cfg == nil {
		return errors.New("config: must not be nil")
	}
	if version.Check(cfg.APIVersion) != nil {
		return fmt.Errorf("apiVersion: expected %q", APIVersion)
	}
	if cfg.Kind != KindTeamDefaults {
		return fmt.Errorf("kind: expected %q", KindTeamDefaults)
	}
	if cfg.Metadata.Name != "" && cfg.Metadata.Name != DefaultsName {
		return fmt.Errorf("metadata.name: a realm has one TeamDefaults, named %q", DefaultsName)
	}
	if cfg.Metadata.Realm != "" && !ValidName(cfg.Metadata.Realm) {
		return errors.New("metadata.realm: require a valid realm name")
	}
	if err := validateBindings(&cfg.Spec); err != nil {
		return err
	}
	return validateDefaults(&cfg.Spec.Defaults)
}

// errDefaultsSources is the message a TeamDefaults that lists sources is
// rejected with: sources are stored Source documents since #1643.
const errDefaultsSources = "spec.defaults.sources: a TeamDefaults lists no sources; " +
	"store each as a Source with kuke create source NAME --url ADDRESS"

// ValidateStoredDefaults checks a TeamDefaults as kukeond stores it (#1643,
// AC2): the rules of Validate, a spec.defaults.sources list rejected, and the
// sources its default roles and harnesses name left unchecked — they name
// stored Sources, which are checked when kuke composes (D8).
func ValidateStoredDefaults(cfg *TeamDefaults) error {
	if cfg == nil {
		return errors.New("config: must not be nil")
	}
	if cfg.Spec.Defaults.Sources != nil {
		return errors.New(errDefaultsSources)
	}
	if version.Check(cfg.APIVersion) != nil {
		return fmt.Errorf("apiVersion: expected %q", APIVersion)
	}
	if cfg.Kind != KindTeamDefaults {
		return fmt.Errorf("kind: expected %q", KindTeamDefaults)
	}
	if cfg.Metadata.Name != "" && cfg.Metadata.Name != DefaultsName {
		return fmt.Errorf("metadata.name: a realm has one TeamDefaults, named %q", DefaultsName)
	}
	if cfg.Metadata.Realm != "" && !ValidName(cfg.Metadata.Realm) {
		return errors.New("metadata.realm: require a valid realm name")
	}
	if err := validateBindings(&cfg.Spec); err != nil {
		return err
	}
	return checkSelections(&cfg.Spec.Defaults, nil)
}

// ValidateSource checks a stored Source (#1643, AC1): a valid name other than
// project, a nonblank address, exactly one of branch, tag and commit, and a
// well-formed git block. Whether the address is one git can fetch is the
// caller's check (kukeon's gitremote.URL), as it is for a team's sources.
func ValidateSource(name string, spec SourceSpec) error {
	if !ValidName(name) || name == ProjectSourceName {
		return fmt.Errorf("metadata.name: invalid source name %q "+
			"(lowercase letters, digits, dots, hyphens; project is reserved)", name)
	}
	if err := required("spec.url", spec.URL); err != nil {
		return err
	}
	if err := revisionSelectors("spec", spec.Branch, spec.Tag, spec.Commit, true); err != nil {
		return err
	}
	return validateSourceGit("spec.git", spec.Git)
}

// revisionSelectors checks that at most one (exactly one when exact) of
// branch, tag and commit is set, each nonblank.
func revisionSelectors(field, branch, tag, commit string, exact bool) error {
	count := 0
	for _, selector := range []struct{ name, value string }{
		{"branch", branch}, {"tag", tag}, {"commit", commit},
	} {
		if selector.value == "" {
			continue
		}
		if err := required(field+"."+selector.name, selector.value); err != nil {
			return err
		}
		count++
	}
	switch {
	case exact && count != 1:
		return fmt.Errorf("%s: require exactly one nonblank revision selector (branch, tag, commit)", field)
	case count > 1:
		return fmt.Errorf("%s: set at most one revision selector (branch, tag, commit)", field)
	}
	return nil
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
	if g.IdentityRef != nil {
		if err := validateSecretRef(field+".identityRef", *g.IdentityRef); err != nil {
			return err
		}
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
		if err := revisionSelectors(field, source.Branch, source.Tag, source.Commit, true); err != nil {
			return err
		}
		if err := validateSourceGit(field+".git", source.Git); err != nil {
			return err
		}
	}
	return nil
}

// validateTeamSources checks a team's own sources and its projectSource as a
// kuketeam.yaml or a stored Team states them (#1643, AC3): each entry has a
// unique valid alias, an optional valid stored-Source name, at most one ref
// and a safe, unique clone path. An entry with a url is a definition; its url
// is checked where it is fetched.
func validateTeamSources(spec *TeamSpec) error {
	aliases, targets := map[string]bool{}, map[string]bool{}
	check := func(field string, source Source) error {
		if source.Stored != "" && (!ValidName(source.Stored) || source.Stored == ProjectSourceName) {
			return fmt.Errorf("%s.source: invalid source name %q", field, source.Stored)
		}
		if source.URL != "" {
			if err := required(field+".url", source.URL); err != nil {
				return err
			}
		}
		if err := revisionSelectors(field, source.Branch, source.Tag, source.Commit, false); err != nil {
			return err
		}
		if source.ClonePath != "" {
			if !SafePath(source.ClonePath) {
				return fmt.Errorf("%s.clonePath: require an absolute in-cell path", field)
			}
			if targets[source.ClonePath] {
				return fmt.Errorf("%s.clonePath: duplicate clone destination", field)
			}
			targets[source.ClonePath] = true
		}
		return validateSourceGit(field+".git", source.Git)
	}
	for i, source := range spec.Sources {
		field := fmt.Sprintf("spec.sources[%d]", i)
		if err := unique(field+".name", source.Name, aliases); err != nil {
			return err
		}
		if !ValidName(source.Name) || source.Name == ProjectSourceName {
			return fmt.Errorf("%s.name: invalid source name %q (project names spec.projectSource)", field, source.Name)
		}
		if err := check(field, source); err != nil {
			return err
		}
	}
	if project := spec.ProjectSource; project != nil {
		if project.Name != "" && project.Name != ProjectSourceName {
			return errors.New("spec.projectSource: name must be project")
		}
		return check("spec.projectSource", *project)
	}
	return nil
}

func validateDefaults(d *Defaults) error {
	return validateSelections(d)
}

func validateSelections(d *Defaults) error {
	sources := map[string]bool{}
	if err := validateSources(d.Sources, sources); err != nil {
		return err
	}
	return checkSelections(d, sources)
}

// checkSelections checks the connectors, roles and harnesses of d. With
// sources nil the source each names is not checked against a list: kukeond
// stores a document whose sources are stored Sources it does not resolve
// (#1643, D8).
func checkSelections(d *Defaults, sources map[string]bool) error {
	roles, harnesses := map[string]bool{}, map[string]bool{}
	if err := validateConnectors("spec.connectors", d.Connectors); err != nil {
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
		if err := validateConnectors(field+".connectors", role.Connectors); err != nil {
			return err
		}
		if role.Tasks != nil {
			tasks := map[string]bool{}
			for j, task := range *role.Tasks {
				taskField := fmt.Sprintf("%s.tasks[%d]", field, j)
				if err := unique(taskField, task.Name, tasks); err != nil {
					return err
				}
				if !ValidName(task.Name) {
					return fmt.Errorf("%s: invalid task name %q", taskField, task.Name)
				}
			}
		}
		if role.Needs != nil {
			for j, image := range role.Needs.Image {
				if err := required(fmt.Sprintf("%s.needs.image[%d]", field, j), image); err != nil {
					return err
				}
			}
			for j, secret := range role.Needs.Secrets {
				if !ValidName(secret) {
					return fmt.Errorf("%s.needs.secrets[%d]: invalid secret name %q", field, j, secret)
				}
			}
		}
	}
	return nil
}

// validateConnectors checks the shape of a connector choice map. Whether a
// connector or a method exists is the connector catalog's to say, so that is
// checked at composition.
func validateConnectors(field string, choices map[string]ConnectorChoice) error {
	for _, name := range slices.Sorted(maps.Keys(choices)) {
		choice := choices[name]
		if !ValidName(name) {
			return fmt.Errorf("%s: invalid connector name %q", field, name)
		}
		if choice.Method != "" && !ValidName(choice.Method) {
			return fmt.Errorf("%s.%s.method: invalid method name %q", field, name, choice.Method)
		}
		if choice.Identity == "" {
			continue
		}
		if _, err := ParseIdentity(choice.Identity, "default"); err != nil {
			return fmt.Errorf("%s.%s.identity: %w", field, name, err)
		}
	}
	return nil
}

// ParseIdentity reads a connector identity written
// `name[@realm[/space[/stack]]]`; without a scope it is a Secret of realm.
func ParseIdentity(value, realm string) (SecretRef, error) {
	name, scope, scoped := strings.Cut(strings.TrimSpace(value), "@")
	ref := SecretRef{Name: name, Realm: realm}
	if scoped {
		parts := strings.Split(scope, "/")
		const maxScope = 3 // realm/space/stack
		if len(parts) > maxScope {
			return SecretRef{}, fmt.Errorf("want name[@realm[/space[/stack]]], got %q", value)
		}
		for len(parts) < maxScope {
			parts = append(parts, "")
		}
		ref.Realm, ref.Space, ref.Stack = parts[0], parts[1], parts[2]
	}
	if !ValidName(ref.Name) || !ValidName(ref.Realm) || (ref.Space != "" && !ValidName(ref.Space)) ||
		(ref.Stack != "" && (!ValidName(ref.Stack) || ref.Space == "")) {
		return SecretRef{}, fmt.Errorf("want name[@realm[/space[/stack]]], got %q", value)
	}
	return ref, nil
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
	if sources != nil && !sources[source] {
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
