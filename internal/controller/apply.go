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
	"errors"
	"fmt"
	"sort"

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

// ApplyDocuments applies a set of resource documents in dependency order.
// Documents are sorted: Realm → Space → Stack → Cell → Container.
// Returns a summary of actions taken for each resource.
func (b *Exec) ApplyDocuments(docs []parser.Document) (ApplyResult, error) {
	defer b.runner.Close()

	result := ApplyResult{
		Resources: make([]ResourceResult, 0, len(docs)),
	}

	// Sort documents by dependency order
	sortedDocs := sortDocumentsByDependency(docs)

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

// sortDocumentsByDependency sorts documents by dependency order:
// Realm → Space → Stack → Cell → Container.
func sortDocumentsByDependency(docs []parser.Document) []parser.Document {
	// Create a copy to avoid modifying the original
	sorted := make([]parser.Document, len(docs))
	copy(sorted, docs)

	// Define dependency order
	kindOrder := map[v1beta1.Kind]int{
		v1beta1.KindRealm:     1,
		v1beta1.KindSpace:     2,
		v1beta1.KindStack:     3,
		v1beta1.KindCell:      4,
		v1beta1.KindContainer: 5,
	}

	// Sort by kind order, then by original index
	sort.Slice(sorted, func(i, j int) bool {
		orderI := kindOrder[sorted[i].Kind]
		orderJ := kindOrder[sorted[j].Kind]
		if orderI != orderJ {
			return orderI < orderJ
		}
		return sorted[i].Index < sorted[j].Index
	})

	return sorted
}
