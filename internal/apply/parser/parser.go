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
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// Document represents a parsed YAML document with its type information.
type Document struct {
	Index        int
	Raw          []byte
	APIVersion   v1beta1.Version
	Kind         v1beta1.Kind
	RealmDoc     *v1beta1.RealmDoc
	SpaceDoc     *v1beta1.SpaceDoc
	StackDoc     *v1beta1.StackDoc
	CellDoc      *v1beta1.CellDoc
	ContainerDoc *v1beta1.ContainerDoc
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
// Documents are separated by `---`.
func ParseDocuments(r io.Reader) ([][]byte, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read input: %w", err)
	}

	// Split on document separator
	docs := strings.Split(string(data), "---")
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

	default:
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
	case v1beta1.KindRealm, v1beta1.KindSpace, v1beta1.KindStack, v1beta1.KindCell, v1beta1.KindContainer:
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
	}

	return nil
}
