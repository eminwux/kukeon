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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	actionFailed = "failed"
)

// ApplyResult represents the result of applying a set of resources.
type ApplyResult struct {
	Resources []ResourceResult
}

// ResourceResult represents the result of applying a single resource.
type ResourceResult struct {
	Index   int
	Kind    string
	Name    string
	Action  string // "created", "updated", "unchanged", "failed"
	Error   error
	Changes []string
	Details map[string]string
}

// resourceResultJSON is a helper type for JSON/YAML serialization.
type resourceResultJSON struct {
	Index   int               `json:"index"             yaml:"index"`
	Kind    string            `json:"kind"              yaml:"kind"`
	Name    string            `json:"name"              yaml:"name"`
	Action  string            `json:"action"            yaml:"action"`
	Error   *string           `json:"error,omitempty"   yaml:"error,omitempty"`
	Changes []string          `json:"changes,omitempty" yaml:"changes,omitempty"`
	Details map[string]string `json:"details,omitempty" yaml:"details,omitempty"`
}

// MarshalJSON implements json.Marshaler for ResourceResult.
func (r ResourceResult) MarshalJSON() ([]byte, error) {
	result := resourceResultJSON{
		Index:   r.Index,
		Kind:    r.Kind,
		Name:    r.Name,
		Action:  r.Action,
		Changes: r.Changes,
		Details: r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return json.Marshal(result)
}

// MarshalYAML implements yaml.Marshaler for ResourceResult.
func (r ResourceResult) MarshalYAML() (interface{}, error) {
	result := resourceResultJSON{
		Index:   r.Index,
		Kind:    r.Kind,
		Name:    r.Name,
		Action:  r.Action,
		Changes: r.Changes,
		Details: r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return result, nil
}

// ApplyDocuments applies a set of resource documents in dependency order.
// Documents are sorted: Realm → Space → Stack → Cell → Container.
// Returns a summary of actions taken for each resource.
func (b *Exec) ApplyDocuments(docs []parser.Document) (ApplyResult, error) {
	defer b.runner.Close()

	result := ApplyResult{
		Resources: make([]ResourceResult, 0, len(docs)),
	}

	// Sort documents by dependency order
	sortedDocs := SortDocumentsByKind(docs, false)

	// Apply each document in order
	for _, doc := range sortedDocs {
		resourceResult := ResourceResult{
			Index:   doc.Index,
			Kind:    string(doc.Kind),
			Details: make(map[string]string),
		}

		// Convert to internal model and reconcile
		var reconcileResult applypkg.ReconcileResult
		var reconcileErr error

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
			reconcileResult, reconcileErr = applypkg.ReconcileRealm(b.runner, realm)

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
			reconcileResult, reconcileErr = applypkg.ReconcileSpace(b.runner, space)

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
			reconcileResult, reconcileErr = applypkg.ReconcileStack(b.runner, stack)

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
			reconcileResult, reconcileErr = applypkg.ReconcileCell(b.runner, cell)

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
			reconcileResult, reconcileErr = applypkg.ReconcileContainer(b.runner, container)

		default:
			resourceResult.Action = actionFailed
			resourceResult.Error = fmt.Errorf("%w: %s", errdefs.ErrUnknownKind, doc.Kind)
			result.Resources = append(result.Resources, resourceResult)
			continue
		}

		if reconcileErr != nil {
			resourceResult.Action = actionFailed
			resourceResult.Error = reconcileErr
		} else {
			resourceResult.Action = reconcileResult.Action
			resourceResult.Changes = reconcileResult.Changes
			resourceResult.Details = reconcileResult.Details
		}

		result.Resources = append(result.Resources, resourceResult)
	}

	return result, nil
}
