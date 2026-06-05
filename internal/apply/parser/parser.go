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

package parser

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// Document represents a parsed YAML document with its type information.
type Document struct {
	Index            int
	Raw              []byte
	APIVersion       v1beta1.Version
	Kind             v1beta1.Kind
	RealmDoc         *v1beta1.RealmDoc
	SpaceDoc         *v1beta1.SpaceDoc
	StackDoc         *v1beta1.StackDoc
	CellDoc          *v1beta1.CellDoc
	ContainerDoc     *v1beta1.ContainerDoc
	SecretDoc        *v1beta1.SecretDoc
	CellBlueprintDoc *v1beta1.CellBlueprintDoc
	CellConfigDoc    *v1beta1.CellConfigDoc
}

// ValidationError represents a validation error for a specific document.
type ValidationError struct {
	Index int
	Kind  v1beta1.Kind
	Name  string
	Err   error
}

func (e *ValidationError) Error() string {
	if e.Name != "" {
		return fmt.Sprintf("document %d (%s %q): %v", e.Index, e.Kind, e.Name, e.Err)
	}
	return fmt.Sprintf("document %d (%s): %v", e.Index, e.Kind, e.Err)
}

// ParseDocuments reads YAML from the given reader and splits it into multiple documents.
// Documents are separated by `---` at the start of a line (optionally preceded by whitespace),
// following the YAML specification. The separator must appear on its own line.
func ParseDocuments(r io.Reader) ([][]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	// Regex pattern to match document separator: --- at start of line (with optional whitespace)
	// Pattern: (?m)^\s*---\s*$
	// - (?m) enables multiline mode (^ and $ match line boundaries)
	// - ^\s* matches start of line with optional leading whitespace
	// - --- matches literal three dashes
	// - \s*$ matches optional trailing whitespace and end of line
	separatorPattern := regexp.MustCompile(`(?m)^\s*---\s*$`)

	// Split on document separator
	docs := separatorPattern.Split(string(data), -1)
	result := make([][]byte, 0, len(docs))

	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue // Skip empty documents
		}
		result = append(result, []byte(doc))
	}

	if len(result) == 0 {
		return nil, errors.New("no documents found in input")
	}

	return result, nil
}

// DetectKind extracts the kind from raw YAML bytes.
func DetectKind(raw []byte) (v1beta1.Kind, error) {
	var header struct {
		Kind v1beta1.Kind `yaml:"kind"`
	}
	if err := yaml.Unmarshal(raw, &header); err != nil {
		return "", fmt.Errorf("failed to parse kind: %w", err)
	}
	return header.Kind, nil
}

// ParseDocument parses a single YAML document and returns a Document with the appropriate typed doc.
func ParseDocument(index int, raw []byte) (*Document, error) {
	doc := &Document{
		Index: index,
		Raw:   raw,
	}

	// First, detect kind
	kind, err := DetectKind(raw)
	if err != nil {
		return nil, fmt.Errorf("document %d: %w", index, err)
	}
	doc.Kind = kind

	// Parse based on kind
	switch kind {
	case v1beta1.KindRealm:
		var realmDoc v1beta1.RealmDoc
		if unmarshalErr := yaml.Unmarshal(raw, &realmDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Realm: %w", index, unmarshalErr)
		}
		doc.RealmDoc = &realmDoc
		doc.APIVersion = realmDoc.APIVersion

	case v1beta1.KindSpace:
		var spaceDoc v1beta1.SpaceDoc
		if unmarshalErr := yaml.Unmarshal(raw, &spaceDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Space: %w", index, unmarshalErr)
		}
		doc.SpaceDoc = &spaceDoc
		doc.APIVersion = spaceDoc.APIVersion

	case v1beta1.KindStack:
		var stackDoc v1beta1.StackDoc
		if unmarshalErr := yaml.Unmarshal(raw, &stackDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Stack: %w", index, unmarshalErr)
		}
		doc.StackDoc = &stackDoc
		doc.APIVersion = stackDoc.APIVersion

	case v1beta1.KindCell:
		var cellDoc v1beta1.CellDoc
		if unmarshalErr := yaml.Unmarshal(raw, &cellDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Cell: %w", index, unmarshalErr)
		}
		doc.CellDoc = &cellDoc
		doc.APIVersion = cellDoc.APIVersion

	case v1beta1.KindContainer:
		var containerDoc v1beta1.ContainerDoc
		if unmarshalErr := yaml.Unmarshal(raw, &containerDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Container: %w", index, unmarshalErr)
		}
		doc.ContainerDoc = &containerDoc
		doc.APIVersion = containerDoc.APIVersion

	case v1beta1.KindSecret:
		var secretDoc v1beta1.SecretDoc
		if unmarshalErr := yaml.Unmarshal(raw, &secretDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse Secret: %w", index, unmarshalErr)
		}
		doc.SecretDoc = &secretDoc
		doc.APIVersion = secretDoc.APIVersion

	case v1beta1.KindCellBlueprint:
		var blueprintDoc v1beta1.CellBlueprintDoc
		if unmarshalErr := yaml.Unmarshal(raw, &blueprintDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse CellBlueprint: %w", index, unmarshalErr)
		}
		doc.CellBlueprintDoc = &blueprintDoc
		doc.APIVersion = blueprintDoc.APIVersion

	case v1beta1.KindCellConfig:
		var configDoc v1beta1.CellConfigDoc
		if unmarshalErr := yaml.Unmarshal(raw, &configDoc); unmarshalErr != nil {
			return nil, fmt.Errorf("document %d: failed to parse CellConfig: %w", index, unmarshalErr)
		}
		doc.CellConfigDoc = &configDoc
		doc.APIVersion = configDoc.APIVersion

	default:
		// kind: CellProfile was removed in #626. Give the operator an
		// explicit migration pointer instead of the generic unknown-kind
		// error, since a YAML written for the old kind is a common stumble
		// during the cutover.
		if kind == "CellProfile" {
			return nil, fmt.Errorf(
				"document %d: %w: %s (the CellProfile kind was removed in #626 — "+
					"convert to kind: CellBlueprint and use `kuke run -b` / `kuke run <config>`; "+
					"see docs/site/guides/migrate-cellprofile-to-blueprint.md)",
				index, errdefs.ErrUnknownKind, kind,
			)
		}
		return nil, fmt.Errorf("document %d: %w: %s", index, errdefs.ErrUnknownKind, kind)
	}

	return doc, nil
}

// ValidateDocument validates a parsed document for required fields and constraints.
func ValidateDocument(doc *Document) *ValidationError {
	// Validate apiVersion
	apiVersion := apischeme.DefaultVersion(doc.APIVersion)
	if apiVersion != apischeme.VersionV1Beta1 {
		return &ValidationError{
			Index: doc.Index,
			Kind:  doc.Kind,
			Err: fmt.Errorf(
				"%w: %s (expected %s)",
				errdefs.ErrUnsupportedAPIVersion,
				doc.APIVersion,
				apischeme.VersionV1Beta1,
			),
		}
	}

	// Validate kind
	switch doc.Kind {
	case v1beta1.KindRealm, v1beta1.KindSpace, v1beta1.KindStack, v1beta1.KindCell,
		v1beta1.KindContainer, v1beta1.KindSecret, v1beta1.KindCellBlueprint,
		v1beta1.KindCellConfig:
		// Valid kind
	default:
		return &ValidationError{
			Index: doc.Index,
			Kind:  doc.Kind,
			Err:   fmt.Errorf("%w: %s", errdefs.ErrUnknownKind, doc.Kind),
		}
	}

	// Validate resource-specific fields
	switch doc.Kind {
	case v1beta1.KindRealm:
		if doc.RealmDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("realm document is nil"),
			}
		}
		if doc.RealmDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("metadata.name is required"),
			}
		}

	case v1beta1.KindSpace:
		if doc.SpaceDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("space document is nil"),
			}
		}
		if doc.SpaceDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.SpaceDoc.Metadata.Name,
				Err:   errors.New("metadata.name is required"),
			}
		}
		if doc.SpaceDoc.Spec.RealmID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.SpaceDoc.Metadata.Name,
				Err:   errors.New("spec.realmId is required"),
			}
		}

	case v1beta1.KindStack:
		if doc.StackDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("stack document is nil"),
			}
		}
		if doc.StackDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("metadata.name is required"),
			}
		}
		if doc.StackDoc.Spec.RealmID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.StackDoc.Metadata.Name,
				Err:   errors.New("spec.realmId is required"),
			}
		}
		if doc.StackDoc.Spec.SpaceID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.StackDoc.Metadata.Name,
				Err:   errors.New("spec.spaceId is required"),
			}
		}

	case v1beta1.KindCell:
		if doc.CellDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("cell document is nil"),
			}
		}
		if doc.CellDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("metadata.name is required"),
			}
		}
		if doc.CellDoc.Spec.RealmID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.CellDoc.Metadata.Name,
				Err:   errors.New("spec.realmId is required"),
			}
		}
		if doc.CellDoc.Spec.SpaceID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.CellDoc.Metadata.Name,
				Err:   errors.New("spec.spaceId is required"),
			}
		}
		if doc.CellDoc.Spec.StackID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.CellDoc.Metadata.Name,
				Err:   errors.New("spec.stackId is required"),
			}
		}
		if len(doc.CellDoc.Spec.Containers) == 0 {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.CellDoc.Metadata.Name,
				Err:   errors.New("spec.containers is required and cannot be empty"),
			}
		}

	case v1beta1.KindContainer:
		if doc.ContainerDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("container document is nil"),
			}
		}
		if doc.ContainerDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("metadata.name is required"),
			}
		}
		if doc.ContainerDoc.Spec.RealmID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   errors.New("spec.realmId is required"),
			}
		}
		if doc.ContainerDoc.Spec.SpaceID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   errors.New("spec.spaceId is required"),
			}
		}
		if doc.ContainerDoc.Spec.StackID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   errors.New("spec.stackId is required"),
			}
		}
		if doc.ContainerDoc.Spec.CellID == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   errors.New("spec.cellId is required"),
			}
		}
		if doc.ContainerDoc.Spec.Image == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   errors.New("spec.image is required"),
			}
		}
		if secretErr := validateSecrets(doc.ContainerDoc.Spec.Secrets); secretErr != nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   secretErr,
			}
		}
		if repoErr := validateRepos(doc.ContainerDoc.Spec.Repos); repoErr != nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.ContainerDoc.Metadata.Name,
				Err:   repoErr,
			}
		}

	case v1beta1.KindSecret:
		if doc.SecretDoc == nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("secret document is nil"),
			}
		}
		if doc.SecretDoc.Metadata.Name == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Err:   errors.New("metadata.name is required"),
			}
		}
		if secretErr := validateSecretScope(doc.SecretDoc.Metadata); secretErr != nil {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.SecretDoc.Metadata.Name,
				Err:   secretErr,
			}
		}
		if strings.TrimSpace(doc.SecretDoc.Spec.Data) == "" {
			return &ValidationError{
				Index: doc.Index,
				Kind:  doc.Kind,
				Name:  doc.SecretDoc.Metadata.Name,
				Err:   errdefs.ErrSecretDataRequired,
			}
		}

	case v1beta1.KindCellBlueprint:
		if validationErr := validateBlueprint(doc); validationErr != nil {
			return validationErr
		}

	case v1beta1.KindCellConfig:
		if validationErr := validateConfig(doc); validationErr != nil {
			return validationErr
		}
	}

	return nil
}

// validateSecretSegment rejects a single secret name or scope coordinate that
// would escape the secrets tree once fs.SecretPath filepath.Join's it into a
// host path: a "/" or "\" separator, a "." or ".." element, or a NUL byte.
// Empty values are the caller's concern (a missing coordinate is legal; an
// empty name is rejected separately), so this only guards non-empty segments.
// Issue #673.
func validateSecretSegment(value string) error {
	if value == "" {
		return nil
	}
	if value == "." || value == ".." {
		return errdefs.ErrSecretCoordUnsafe
	}
	if strings.ContainsRune(value, 0) ||
		strings.ContainsRune(value, '/') ||
		strings.ContainsRune(value, '\\') ||
		strings.ContainsRune(value, filepath.Separator) {
		return errdefs.ErrSecretCoordUnsafe
	}
	return nil
}

// validateBlueprint enforces the structural contract for a kind: CellBlueprint
// document (issue #620): name + scope coordinates, a non-empty cell template,
// and the repo/secret slot shapes. The scope reachability gate (the named
// realm/space/stack must exist) runs at reconcile time against the runner, the
// same split the Secret kind uses (only the daemon can read /opt/kukeon).
func validateBlueprint(doc *Document) *ValidationError {
	if doc.CellBlueprintDoc == nil {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Err: errors.New("blueprint document is nil")}
	}
	bp := doc.CellBlueprintDoc
	if strings.TrimSpace(bp.Metadata.Name) == "" {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Err: errdefs.ErrBlueprintNameRequired}
	}
	if scopeErr := validateBlueprintScope(bp.Metadata); scopeErr != nil {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: bp.Metadata.Name, Err: scopeErr}
	}
	if len(bp.Spec.Cell.Containers) == 0 {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: bp.Metadata.Name, Err: errdefs.ErrBlueprintCellRequired}
	}
	for _, c := range bp.Spec.Cell.Containers {
		if repoErr := validateBlueprintRepos(c.Repos); repoErr != nil {
			return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: bp.Metadata.Name, Err: repoErr}
		}
		if secretErr := validateBlueprintSecretSlots(c.Secrets); secretErr != nil {
			return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: bp.Metadata.Name, Err: secretErr}
		}
	}
	return nil
}

// validateBlueprintScope enforces the Blueprint scope-coordinate contract:
// metadata.realm is always required, a deeper coordinate may only be set when
// every shallower one is, and — unlike a Secret — a Blueprint may not be
// cell-scoped (a template scoped to one cell is nonsensical, #423).
func validateBlueprintScope(md v1beta1.CellBlueprintMetadata) error {
	realm := strings.TrimSpace(md.Realm)
	space := strings.TrimSpace(md.Space)
	stack := strings.TrimSpace(md.Stack)

	if realm == "" {
		return errdefs.ErrBlueprintRealmRequired
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrBlueprintScopeIncomplete)
	}
	return nil
}

// validateBlueprintRepos enforces the blueprint repo-slot shape: name required,
// target required and absolute. Unlike the Cell/Container apply path
// (validateRepos), url is NOT required: a repo with no url is a structural slot
// a CellConfig fills (#624). A url supplied inline (directly or via ${PARAM})
// is carried through to the materialized cell unchanged.
func validateBlueprintRepos(repos []v1beta1.ContainerRepo) error {
	for i, r := range repos {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("%w (repos[%d])", errdefs.ErrRepoNameRequired, i)
		}
		target := strings.TrimSpace(r.Target)
		if target == "" {
			return fmt.Errorf("%w (repos[%d] %q)", errdefs.ErrRepoTargetRequired, i, name)
		}
		if !filepath.IsAbs(target) {
			return fmt.Errorf("%w (repos[%d] %q target %q)", errdefs.ErrRepoTargetNotAbsolute, i, name, target)
		}
		if strings.TrimSpace(r.Branch) != "" && strings.TrimSpace(r.Ref) != "" {
			return fmt.Errorf("%w (repos[%d] %q)", errdefs.ErrRepoBranchRefMutex, i, name)
		}
	}
	return nil
}

// validateBlueprintSecretSlots enforces the blueprint secret-slot shape: name
// required; mode (default "env") one of env/file; env mode requires envName;
// file mode requires an absolute mountPath. The source side (which kind: Secret
// provides the bytes) is intentionally absent — a CellConfig fills it (#624).
func validateBlueprintSecretSlots(slots []v1beta1.BlueprintSecretSlot) error {
	for i, s := range slots {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return fmt.Errorf("%w (secrets[%d])", errdefs.ErrBlueprintSecretSlotNameRequired, i)
		}
		mode := strings.TrimSpace(s.Mode)
		switch mode {
		case "", v1beta1.BlueprintSecretModeEnv:
			if strings.TrimSpace(s.EnvName) == "" {
				return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrBlueprintSecretSlotEnvName, i, name)
			}
		case v1beta1.BlueprintSecretModeFile:
			mountPath := strings.TrimSpace(s.MountPath)
			if mountPath == "" || !filepath.IsAbs(mountPath) {
				return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrBlueprintSecretSlotMountPath, i, name)
			}
		default:
			return fmt.Errorf("%w (secrets[%d] %q mode %q)", errdefs.ErrBlueprintSecretSlotMode, i, name, mode)
		}
	}
	return nil
}

// validateConfig enforces the structural contract for a kind: CellConfig
// document (issue #624): name + scope coordinates, a present blueprint
// reference, and the shape of each repo/secret slot fill. The slot-fill match
// against the *referenced blueprint's declared slots* (unknown slot, required
// slot unfilled) runs at reconcile time, the same split the Blueprint/Secret
// scope-reachability gate uses — only the daemon can read the stored blueprint.
func validateConfig(doc *Document) *ValidationError {
	if doc.CellConfigDoc == nil {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Err: errors.New("config document is nil")}
	}
	cfg := doc.CellConfigDoc
	if strings.TrimSpace(cfg.Metadata.Name) == "" {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Err: errdefs.ErrConfigNameRequired}
	}
	if scopeErr := validateConfigScope(cfg.Metadata); scopeErr != nil {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: cfg.Metadata.Name, Err: scopeErr}
	}
	if refErr := validateConfigBlueprintRef(cfg.Spec.Blueprint); refErr != nil {
		return &ValidationError{Index: doc.Index, Kind: doc.Kind, Name: cfg.Metadata.Name, Err: refErr}
	}
	for name, fill := range cfg.Spec.Repos {
		if strings.TrimSpace(fill.URL) == "" {
			return &ValidationError{
				Index: doc.Index, Kind: doc.Kind, Name: cfg.Metadata.Name,
				Err: fmt.Errorf("%w (repos[%q])", errdefs.ErrConfigRepoFillURLRequired, name),
			}
		}
		if strings.TrimSpace(fill.Branch) != "" && strings.TrimSpace(fill.Ref) != "" {
			return &ValidationError{
				Index: doc.Index, Kind: doc.Kind, Name: cfg.Metadata.Name,
				Err: fmt.Errorf("%w (repos[%q])", errdefs.ErrRepoBranchRefMutex, name),
			}
		}
	}
	for name, fill := range cfg.Spec.Secrets {
		if fill.SecretRef == nil ||
			strings.TrimSpace(fill.SecretRef.Name) == "" ||
			strings.TrimSpace(fill.SecretRef.Realm) == "" {
			return &ValidationError{
				Index: doc.Index, Kind: doc.Kind, Name: cfg.Metadata.Name,
				Err: fmt.Errorf("%w (secrets[%q])", errdefs.ErrConfigSecretFillRefRequired, name),
			}
		}
	}
	return nil
}

// validateConfigScope enforces the CellConfig scope-coordinate contract:
// metadata.realm is always required, a deeper coordinate may only be set when
// every shallower one is, and — like a Blueprint — a Config may not be
// cell-scoped.
func validateConfigScope(md v1beta1.CellConfigMetadata) error {
	realm := strings.TrimSpace(md.Realm)
	space := strings.TrimSpace(md.Space)
	stack := strings.TrimSpace(md.Stack)

	if realm == "" {
		return errdefs.ErrConfigRealmRequired
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrConfigScopeIncomplete)
	}
	return nil
}

// validateConfigBlueprintRef enforces the referenced-blueprint shape: name and
// realm are required, and the optional space/stack coordinates follow the same
// deeper-requires-shallower contract. It does not check the blueprint exists on
// the host — that reachability gate runs at reconcile time against the runner.
func validateConfigBlueprintRef(ref v1beta1.CellConfigBlueprintRef) error {
	name := strings.TrimSpace(ref.Name)
	realm := strings.TrimSpace(ref.Realm)
	space := strings.TrimSpace(ref.Space)
	stack := strings.TrimSpace(ref.Stack)

	if name == "" || realm == "" {
		return errdefs.ErrConfigBlueprintRefRequired
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrConfigBlueprintRefScopeIncomplete)
	}
	return nil
}

// validateSecretScope enforces the `kind: Secret` scope-coordinate contract:
// metadata.realm is always required, and a deeper coordinate may only be set
// when every shallower one is also set (a cell-scoped secret must name its
// stack, space, and realm). It does not check that the scope exists on the
// host — that reachability gate runs at reconcile time against the runner,
// because only the daemon can read /opt/kukeon. Issue #619.
func validateSecretScope(md v1beta1.SecretMetadata) error {
	realm := strings.TrimSpace(md.Realm)
	space := strings.TrimSpace(md.Space)
	stack := strings.TrimSpace(md.Stack)
	cell := strings.TrimSpace(md.Cell)

	if realm == "" {
		return errdefs.ErrSecretRealmRequired
	}
	// A deeper coordinate requires all shallower ones. Walk outward: the
	// first non-empty coordinate that sits below an empty parent is a gap.
	if cell != "" && stack == "" {
		return fmt.Errorf("%w (cell set without stack)", errdefs.ErrSecretScopeIncomplete)
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrSecretScopeIncomplete)
	}
	// Reject any name or coordinate that would escape the secrets tree.
	for _, seg := range []struct{ field, value string }{
		{"metadata.name", strings.TrimSpace(md.Name)},
		{"metadata.realm", realm},
		{"metadata.space", space},
		{"metadata.stack", stack},
		{"metadata.cell", cell},
	} {
		if err := validateSecretSegment(seg.value); err != nil {
			return fmt.Errorf("%w (%s)", err, seg.field)
		}
	}
	return nil
}

// validateRepos enforces the shape of repo declarations: name required, target
// required and absolute, url required. Mirrors validateSecrets. kuketty reads
// repos[] straight from the mounted ContainerDoc.Spec and performs no
// validation of its own, so this apply-time gate is the single check before a
// malformed repos[] reaches the wrapper. Issue #617.
func validateRepos(repos []v1beta1.ContainerRepo) error {
	for i, r := range repos {
		name := strings.TrimSpace(r.Name)
		if name == "" {
			return fmt.Errorf("%w (repos[%d])", errdefs.ErrRepoNameRequired, i)
		}
		target := strings.TrimSpace(r.Target)
		if target == "" {
			return fmt.Errorf("%w (repos[%d] %q)", errdefs.ErrRepoTargetRequired, i, name)
		}
		if !filepath.IsAbs(target) {
			return fmt.Errorf("%w (repos[%d] %q target %q)", errdefs.ErrRepoTargetNotAbsolute, i, name, target)
		}
		if strings.TrimSpace(r.URL) == "" {
			return fmt.Errorf("%w (repos[%d] %q)", errdefs.ErrRepoURLRequired, i, name)
		}
		if strings.TrimSpace(r.Branch) != "" && strings.TrimSpace(r.Ref) != "" {
			return fmt.Errorf("%w (repos[%d] %q)", errdefs.ErrRepoBranchRefMutex, i, name)
		}
	}
	return nil
}

// validateSecrets enforces the shape of secret references: name required,
// exactly one of fromFile/fromEnv/secretRef, and mountPath (if set) absolute.
func validateSecrets(secrets []v1beta1.ContainerSecret) error {
	for i, s := range secrets {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return fmt.Errorf("%w (secrets[%d])", errdefs.ErrSecretNameRequired, i)
		}
		// Exactly one of fromFile / fromEnv / secretRef must be set.
		sources := 0
		if strings.TrimSpace(s.FromFile) != "" {
			sources++
		}
		if strings.TrimSpace(s.FromEnv) != "" {
			sources++
		}
		if s.SecretRef != nil {
			sources++
		}
		switch {
		case sources == 0:
			return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrSecretSourceRequired, i, name)
		case sources > 1:
			return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrSecretMultipleSources, i, name)
		}
		if s.SecretRef != nil {
			if refErr := validateSecretRef(*s.SecretRef, i, name); refErr != nil {
				return refErr
			}
		}
		mountPath := strings.TrimSpace(s.MountPath)
		if mountPath != "" && !filepath.IsAbs(mountPath) {
			return fmt.Errorf(
				"%w (secrets[%d] %q mountPath %q)",
				errdefs.ErrSecretMountPathNotAbsolute,
				i,
				name,
				mountPath,
			)
		}
	}
	return nil
}

// validateSecretRef enforces the secretRef shape: a referenced name plus a
// scope that follows the same coordinate contract as a kind: Secret — realm
// always required, a deeper coordinate only when every shallower one is set.
// It does not check that the Secret exists on the host; that reachability gate
// runs at container-start time against the runner, because only the daemon can
// read the scope's secrets tree. Issue #623.
func validateSecretRef(ref v1beta1.ContainerSecretRef, i int, name string) error {
	if strings.TrimSpace(ref.Name) == "" {
		return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrSecretRefNameRequired, i, name)
	}
	realm := strings.TrimSpace(ref.Realm)
	space := strings.TrimSpace(ref.Space)
	stack := strings.TrimSpace(ref.Stack)
	cell := strings.TrimSpace(ref.Cell)
	if realm == "" {
		return fmt.Errorf("%w (secrets[%d] %q)", errdefs.ErrSecretRefRealmRequired, i, name)
	}
	if cell != "" && stack == "" {
		return fmt.Errorf(
			"%w (secrets[%d] %q: cell set without stack)", errdefs.ErrSecretRefScopeIncomplete, i, name,
		)
	}
	if stack != "" && space == "" {
		return fmt.Errorf(
			"%w (secrets[%d] %q: stack set without space)", errdefs.ErrSecretRefScopeIncomplete, i, name,
		)
	}
	// Reject any referenced name or coordinate that would escape the secrets tree.
	for _, seg := range []struct{ field, value string }{
		{"secretRef.name", strings.TrimSpace(ref.Name)},
		{"secretRef.realm", realm},
		{"secretRef.space", space},
		{"secretRef.stack", stack},
		{"secretRef.cell", cell},
	} {
		if err := validateSecretSegment(seg.value); err != nil {
			return fmt.Errorf("%w (secrets[%d] %q %s)", err, i, name, seg.field)
		}
	}
	return nil
}
