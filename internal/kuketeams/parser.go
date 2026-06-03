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

// Package kuketeams parses and validates the team-distribution documents
// (kuketeams.io/v1: ProjectTeam, TeamsConfig, Role, Harness, ImageCatalog;
// issue #793, epic #792). It mirrors the internal/serverconfig +
// internal/apply/parser split — the Go types live in
// pkg/api/model/kuketeams; this package owns deserialization and the
// validation rules. No CLI verb consumes it yet (the verbs land in #796 and
// later), so it is deliberately a standalone parser rather than a registration
// into the v1beta1 apply/get dispatch, which is group-scoped to v1beta1.
package kuketeams

import (
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	"gopkg.in/yaml.v3"
)

// Document is a parsed team-distribution document. Exactly one of the typed
// pointers is non-nil, selected by Kind.
type Document struct {
	APIVersion   string
	Kind         string
	ProjectTeam  *model.ProjectTeam
	TeamsConfig  *model.TeamsConfig
	TeamEntry    *model.TeamEntry
	Role         *model.Role
	Harness      *model.Harness
	ImageCatalog *model.ImageCatalog
}

// sourceRefPattern matches a pinned-exact `<owner>/<repo>@vX.Y.Z` reference.
// The version must be a full vMAJOR.MINOR.PATCH, optionally with a
// prerelease/build suffix — floating refs (`@main`) and bare tags (`@v1`) are
// rejected. Owner/repo allow the GitHub-name character class.
var sourceRefPattern = regexp.MustCompile(
	`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+@v\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?$`,
)

// ownerRepoPattern matches a bare `<owner>/<repo>` key (no version).
var ownerRepoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// docSeparator splits a multi-document YAML stream on `---` lines.
var docSeparator = regexp.MustCompile(`(?m)^\s*---\s*$`)

// header is the GVK preamble read before dispatching to a typed parse.
type header struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
}

// ParseDocuments splits a multi-document YAML stream and parses+validates each
// document, returning them in order. An empty stream is an error.
func ParseDocuments(r io.Reader) ([]*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read input: %w", err)
	}
	parts := docSeparator.Split(string(data), -1)
	docs := make([]*Document, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		doc, parseErr := Parse([]byte(part))
		if parseErr != nil {
			return nil, fmt.Errorf("document %d: %w", len(docs), parseErr)
		}
		docs = append(docs, doc)
	}
	if len(docs) == 0 {
		return nil, errors.New("no documents found in input")
	}
	return docs, nil
}

// Parse deserializes and validates a single team-distribution document. An
// unknown or empty apiVersion/kind pair is a parse error, matching the kind
// guard the v1beta1 surface enforces.
func Parse(raw []byte) (*Document, error) {
	var h header
	if err := yaml.Unmarshal(raw, &h); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if h.APIVersion != model.APIVersionV1 {
		return nil, fmt.Errorf(
			"%w: %q (expected %s)",
			errdefs.ErrUnsupportedAPIVersion,
			h.APIVersion,
			model.APIVersionV1,
		)
	}

	doc := &Document{APIVersion: h.APIVersion, Kind: h.Kind}
	switch h.Kind {
	case model.KindProjectTeam:
		var v model.ProjectTeam
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse ProjectTeam: %w", err)
		}
		if err := validateProjectTeam(&v); err != nil {
			return nil, err
		}
		doc.ProjectTeam = &v
	case model.KindTeamsConfig:
		var v model.TeamsConfig
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse TeamsConfig: %w", err)
		}
		if err := validateTeamsConfig(&v); err != nil {
			return nil, err
		}
		doc.TeamsConfig = &v
	case model.KindTeamEntry:
		var v model.TeamEntry
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse TeamEntry: %w", err)
		}
		if err := validateTeamEntry(&v); err != nil {
			return nil, err
		}
		doc.TeamEntry = &v
	case model.KindRole:
		var v model.Role
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse Role: %w", err)
		}
		if err := validateRole(&v); err != nil {
			return nil, err
		}
		doc.Role = &v
	case model.KindHarness:
		var v model.Harness
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse Harness: %w", err)
		}
		if err := validateHarness(&v); err != nil {
			return nil, err
		}
		doc.Harness = &v
	case model.KindImageCatalog:
		var v model.ImageCatalog
		if err := yaml.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("parse ImageCatalog: %w", err)
		}
		if err := validateImageCatalog(&v); err != nil {
			return nil, err
		}
		doc.ImageCatalog = &v
	default:
		return nil, fmt.Errorf("%w: %q", errdefs.ErrUnknownKind, h.Kind)
	}
	return doc, nil
}

// validateProjectTeam enforces the ProjectTeam contract: metadata.name present;
// source pinned-exact; every roles[].ref non-empty; defaults/role harness names
// known; project role image needs are capability names.
func validateProjectTeam(pt *model.ProjectTeam) error {
	if strings.TrimSpace(pt.Metadata.Name) == "" {
		return errdefs.ErrTeamMetadataNameRequired
	}
	if !sourceRefPattern.MatchString(strings.TrimSpace(pt.Spec.Source)) {
		return fmt.Errorf("%w (got %q)", errdefs.ErrTeamSourceInvalid, pt.Spec.Source)
	}
	for _, h := range pt.Spec.Defaults.Harnesses {
		if !model.IsKnownHarness(h) {
			return fmt.Errorf("%w: %q (defaults.harnesses)", errdefs.ErrTeamHarnessUnknown, h)
		}
	}
	for i, role := range pt.Spec.Roles {
		if strings.TrimSpace(role.Ref) == "" {
			return fmt.Errorf("%w (roles[%d])", errdefs.ErrTeamRoleRefRequired, i)
		}
		if role.Needs != nil {
			if err := validateCapabilityNames(role.Needs.Image, fmt.Sprintf("roles[%d].needs.image", i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateTeamsConfig enforces the TeamsConfig contract: git identities
// complete; git.sign entries valid and key-backed; secrets declare a source;
// sources keys well-formed. Per-project composition is no longer carried here —
// it moved to the TeamEntry drop-in (validateTeamEntry).
func validateTeamsConfig(tc *model.TeamsConfig) error {
	if tc.Spec.Git != nil {
		if err := validateGit(tc.Spec.Git); err != nil {
			return err
		}
	}
	for name, secret := range tc.Spec.Secrets {
		switch secret.From {
		case model.SecretFromEnv, model.SecretFromFile:
		default:
			return fmt.Errorf("%w (secrets[%q])", errdefs.ErrTeamSecretSourceInvalid, name)
		}
		if strings.TrimSpace(secret.Key) == "" {
			return fmt.Errorf("%w (secrets[%q])", errdefs.ErrTeamSecretSourceInvalid, name)
		}
	}
	for key := range tc.Spec.Sources {
		if !ownerRepoPattern.MatchString(key) {
			return fmt.Errorf("%w (got %q)", errdefs.ErrTeamSourceKeyInvalid, key)
		}
	}
	return nil
}

// validateTeamEntry enforces the per-project drop-in contract: metadata.name
// present (it is the <project>.yaml filename key), and source — when set —
// pinned-exact to a `<owner>/<repo>@vX.Y.Z` agents reference.
func validateTeamEntry(te *model.TeamEntry) error {
	if strings.TrimSpace(te.Metadata.Name) == "" {
		return errdefs.ErrTeamEntryNameRequired
	}
	if strings.TrimSpace(te.Spec.Source) != "" &&
		!sourceRefPattern.MatchString(strings.TrimSpace(te.Spec.Source)) {
		return fmt.Errorf("%w (got %q)", errdefs.ErrTeamSourceInvalid, te.Spec.Source)
	}
	return nil
}

// validateGit enforces the git superset rules: each present identity carries
// both name and email; sign entries are commits/tags and require a signingKey.
func validateGit(git *model.TeamsConfigGit) error {
	if git.Author != nil {
		if strings.TrimSpace(git.Author.Name) == "" || strings.TrimSpace(git.Author.Email) == "" {
			return fmt.Errorf("%w (git.author)", errdefs.ErrTeamGitIdentityIncomplete)
		}
	}
	if git.Committer != nil {
		if strings.TrimSpace(git.Committer.Name) == "" || strings.TrimSpace(git.Committer.Email) == "" {
			return fmt.Errorf("%w (git.committer)", errdefs.ErrTeamGitIdentityIncomplete)
		}
	}
	if len(git.Sign) > 0 {
		if strings.TrimSpace(git.SigningKey) == "" {
			return errdefs.ErrTeamGitSignNeedsKey
		}
		for _, s := range git.Sign {
			switch s {
			case model.GitSignCommits, model.GitSignTags:
			default:
				return fmt.Errorf("%w (got %q)", errdefs.ErrTeamGitSignInvalid, s)
			}
		}
	}
	return nil
}

// validateRole enforces the Role contract: metadata.name present; harness keys
// known; needs.image entries are capability names.
func validateRole(r *model.Role) error {
	if strings.TrimSpace(r.Metadata.Name) == "" {
		return errdefs.ErrTeamMetadataNameRequired
	}
	for h := range r.Spec.Harnesses {
		if !model.IsKnownHarness(h) {
			return fmt.Errorf("%w: %q (spec.harnesses)", errdefs.ErrTeamHarnessUnknown, h)
		}
	}
	if err := validateCapabilityNames(r.Spec.Needs.Image, "spec.needs.image"); err != nil {
		return err
	}
	return nil
}

// validateHarness enforces the Harness contract: name known; skillPath,
// makeTarget, template non-empty.
func validateHarness(h *model.Harness) error {
	if !model.IsKnownHarness(h.Metadata.Name) {
		return fmt.Errorf("%w: %q (metadata.name)", errdefs.ErrTeamHarnessUnknown, h.Metadata.Name)
	}
	if strings.TrimSpace(h.Spec.SkillPath) == "" ||
		strings.TrimSpace(h.Spec.MakeTarget) == "" ||
		strings.TrimSpace(h.Spec.Template) == "" {
		return errdefs.ErrTeamHarnessFieldRequired
	}
	return nil
}

// validateImageCatalog enforces the ImageCatalog contract per entry: ref
// present; harness known; image registry-qualified; build complete;
// capabilities non-empty.
func validateImageCatalog(ic *model.ImageCatalog) error {
	for i, entry := range ic.Spec.Images {
		if strings.TrimSpace(entry.Ref) == "" {
			return fmt.Errorf("%w (images[%d])", errdefs.ErrTeamImageRefRequired, i)
		}
		if !model.IsKnownHarness(entry.Harness) {
			return fmt.Errorf("%w: %q (images[%d] %q)", errdefs.ErrTeamHarnessUnknown, entry.Harness, i, entry.Ref)
		}
		if !isRegistryQualified(entry.Image) {
			return fmt.Errorf(
				"%w (images[%d] %q image %q)",
				errdefs.ErrTeamImageImageRequired,
				i,
				entry.Ref,
				entry.Image,
			)
		}
		if strings.TrimSpace(entry.Build.Context) == "" || strings.TrimSpace(entry.Build.Dockerfile) == "" {
			return fmt.Errorf("%w (images[%d] %q)", errdefs.ErrTeamImageBuildRequired, i, entry.Ref)
		}
		if len(entry.Capabilities) == 0 {
			return fmt.Errorf("%w (images[%d] %q)", errdefs.ErrTeamImageCapabilitiesRequired, i, entry.Ref)
		}
		if err := validateCapabilityNames(entry.Capabilities, fmt.Sprintf("images[%d].capabilities", i)); err != nil {
			return err
		}
	}
	return nil
}

// validateCapabilityNames rejects capability entries that look like image tags
// or digests (a "/" path, a ":" tag, or an "@" digest) — capabilities are bare
// selector names, never image references.
func validateCapabilityNames(caps []string, field string) error {
	for _, c := range caps {
		if strings.TrimSpace(c) == "" ||
			strings.ContainsAny(c, "/:@") {
			return fmt.Errorf("%w: %q (%s)", errdefs.ErrTeamImageCapabilityInvalid, c, field)
		}
	}
	return nil
}

// isRegistryQualified reports whether ref names a registry host explicitly: it
// has at least one "/" and the first path component looks like a host (carries a
// "." or a ":" port). This rejects bare ("claude") and docker-library
// ("library/claude") shorthands the contract forbids.
func isRegistryQualified(ref string) bool {
	ref = strings.TrimSpace(ref)
	slash := strings.IndexByte(ref, '/')
	if slash <= 0 {
		return false
	}
	host := ref[:slash]
	return strings.ContainsAny(host, ".:")
}
