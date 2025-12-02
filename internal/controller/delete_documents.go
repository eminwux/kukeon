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

package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	actionDeleted  = "deleted"
	actionNotFound = "not found"
	// actionFailed is defined in apply.go.
)

// DeleteResult represents the result of deleting a set of resources.
type DeleteResult struct {
	Resources []ResourceDeleteResult
}

// ResourceDeleteResult represents the result of deleting a single resource.
type ResourceDeleteResult struct {
	Index    int
	Kind     string
	Name     string
	Action   string // "deleted", "not found", "failed"
	Error    error
	Cascaded []string // Child resources deleted (if cascade=true)
	Details  map[string]string
}

// resourceDeleteResultJSON is a helper type for JSON/YAML serialization.
type resourceDeleteResultJSON struct {
	Index    int               `json:"index"              yaml:"index"`
	Kind     string            `json:"kind"               yaml:"kind"`
	Name     string            `json:"name"               yaml:"name"`
	Action   string            `json:"action"             yaml:"action"`
	Error    *string           `json:"error,omitempty"    yaml:"error,omitempty"`
	Cascaded []string          `json:"cascaded,omitempty" yaml:"cascaded,omitempty"`
	Details  map[string]string `json:"details,omitempty"  yaml:"details,omitempty"`
}

// MarshalJSON implements json.Marshaler for ResourceDeleteResult.
func (r ResourceDeleteResult) MarshalJSON() ([]byte, error) {
	result := resourceDeleteResultJSON{
		Index:    r.Index,
		Kind:     r.Kind,
		Name:     r.Name,
		Action:   r.Action,
		Cascaded: r.Cascaded,
		Details:  r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return json.Marshal(result)
}

// MarshalYAML implements yaml.Marshaler for ResourceDeleteResult.
func (r ResourceDeleteResult) MarshalYAML() (interface{}, error) {
	result := resourceDeleteResultJSON{
		Index:    r.Index,
		Kind:     r.Kind,
		Name:     r.Name,
		Action:   r.Action,
		Cascaded: r.Cascaded,
		Details:  r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return result, nil
}

// DeleteDocuments deletes a set of resource documents in reverse dependency order.
// Documents are sorted: Container → Cell → Stack → Space → Realm.
// Returns a summary of actions taken for each resource.
func (b *Exec) DeleteDocuments(docs []parser.Document, cascade, force bool) (DeleteResult, error) {
	defer b.runner.Close()

	result := DeleteResult{
		Resources: make([]ResourceDeleteResult, 0, len(docs)),
	}

	// Sort documents by reverse dependency order
	sortedDocs := SortDocumentsByKind(docs, true)

	// Delete each document in order
	for _, doc := range sortedDocs {
		resourceResult := ResourceDeleteResult{
			Index:    doc.Index,
			Kind:     string(doc.Kind),
			Details:  make(map[string]string),
			Cascaded: []string{},
		}

		// Convert to internal model and delete
		switch doc.Kind {
		case v1beta1.KindRealm:
			if doc.RealmDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("realm document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			realm, _, err := apischeme.NormalizeRealm(*doc.RealmDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = realm.Metadata.Name
			deleteResult, deleteErr := b.DeleteRealm(realm, force, cascade)
			if deleteErr != nil {
				if isNotFoundError(deleteErr) {
					resourceResult.Action = actionNotFound
				} else {
					resourceResult.Action = actionFailed
					resourceResult.Error = deleteErr
				}
			} else {
				resourceResult.Action = actionDeleted
				extractCascadedResources(&resourceResult, deleteResult.Deleted, "space")
			}

		case v1beta1.KindSpace:
			if doc.SpaceDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("space document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			space, _, err := apischeme.NormalizeSpace(*doc.SpaceDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = space.Metadata.Name
			deleteResult, deleteErr := b.DeleteSpace(space, force, cascade)
			if deleteErr != nil {
				if isNotFoundError(deleteErr) {
					resourceResult.Action = actionNotFound
				} else {
					resourceResult.Action = actionFailed
					resourceResult.Error = deleteErr
				}
			} else {
				resourceResult.Action = actionDeleted
				extractCascadedResources(&resourceResult, deleteResult.Deleted, "stack")
			}

		case v1beta1.KindStack:
			if doc.StackDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("stack document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			stack, _, err := apischeme.NormalizeStack(*doc.StackDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = stack.Metadata.Name
			deleteResult, deleteErr := b.DeleteStack(stack, force, cascade)
			if deleteErr != nil {
				if isNotFoundError(deleteErr) {
					resourceResult.Action = actionNotFound
				} else {
					resourceResult.Action = actionFailed
					resourceResult.Error = deleteErr
				}
			} else {
				resourceResult.Action = actionDeleted
				extractCascadedResources(&resourceResult, deleteResult.Deleted, "cell")
			}

		case v1beta1.KindCell:
			if doc.CellDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("cell document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			cell, _, err := apischeme.NormalizeCell(*doc.CellDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = cell.Metadata.Name
			deleteResult, deleteErr := b.DeleteCell(cell)
			if deleteErr != nil {
				if isNotFoundError(deleteErr) {
					resourceResult.Action = actionNotFound
				} else {
					resourceResult.Action = actionFailed
					resourceResult.Error = deleteErr
				}
			} else {
				resourceResult.Action = actionDeleted
				if deleteResult.ContainersDeleted {
					// Count containers that were deleted
					containerCount := len(cell.Spec.Containers)
					if containerCount > 0 {
						resourceResult.Details["containers"] = fmt.Sprintf("%d deleted", containerCount)
					}
				}
			}

		case v1beta1.KindContainer:
			if doc.ContainerDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("container document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			container, _, err := apischeme.NormalizeContainer(*doc.ContainerDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = container.Metadata.Name
			_, deleteErr := b.DeleteContainer(container)
			if deleteErr != nil {
				if isNotFoundError(deleteErr) {
					resourceResult.Action = actionNotFound
				} else {
					resourceResult.Action = actionFailed
					resourceResult.Error = deleteErr
				}
			} else {
				resourceResult.Action = actionDeleted
				resourceResult.Details["containers"] = "1 deleted"
			}

		default:
			resourceResult.Action = actionFailed
			resourceResult.Error = fmt.Errorf("%w: %s", errdefs.ErrUnknownKind, doc.Kind)
			result.Resources = append(result.Resources, resourceResult)
			continue
		}

		result.Resources = append(result.Resources, resourceResult)
	}

	return result, nil
}

// isNotFoundError checks if an error indicates a resource was not found.
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific not found errors
	if errors.Is(err, errdefs.ErrRealmNotFound) ||
		errors.Is(err, errdefs.ErrSpaceNotFound) ||
		errors.Is(err, errdefs.ErrStackNotFound) ||
		errors.Is(err, errdefs.ErrCellNotFound) {
		return true
	}

	// Check error message for "not found" pattern
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not found")
}

// extractCascadedResources extracts cascaded resource names from the Deleted slice.
// It looks for entries starting with "prefix:" and adds them to the cascaded list.
func extractCascadedResources(result *ResourceDeleteResult, deleted []string, prefix string) {
	for _, item := range deleted {
		if strings.HasPrefix(item, prefix+":") {
			// Extract name after "prefix:"
			name := strings.TrimPrefix(item, prefix+":")
			result.Cascaded = append(result.Cascaded, name)
		}
	}
}
