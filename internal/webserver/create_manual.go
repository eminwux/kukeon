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

package webserver

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// manualCellRequest is deliberately narrower than CellDoc. Runtime status,
// daemon-stamped identity, provenance, and transport-only fields cannot cross
// this boundary from a browser request.
type manualCellRequest struct {
	Name        string            `json:"name"`
	Realm       string            `json:"realm"`
	Space       string            `json:"space"`
	Stack       string            `json:"stack"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
	Spec        manualCellSpec    `json:"spec"`
	Preview     string            `json:"preview,omitempty"`
}

type manualCellSpec struct {
	RootContainerID     string                  `json:"rootContainerId,omitempty"`
	Tty                 *v1beta1.CellTty        `json:"tty,omitempty"`
	Containers          []v1beta1.ContainerSpec `json:"containers,omitempty"`
	AutoDelete          bool                    `json:"autoDelete,omitempty"`
	NestedCgroupRuntime bool                    `json:"nestedCgroupRuntime,omitempty"`
	IgnoreDiskPressure  bool                    `json:"ignoreDiskPressure,omitempty"`
}

func registerManualCreateRoutes(mux *http.ServeMux, dial Dial) {
	for _, action := range []string{"preview", "create"} {
		mux.HandleFunc(
			"POST /api/cells/manual/"+action,
			resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Content-Type") != "application/json" {
					return nil, fmt.Errorf("%w: same-origin JSON request required", errdefs.ErrConfig)
				}
				var input manualCellRequest
				decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCreateBody))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&input); err != nil {
					return nil, fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
				}
				if err := decoder.Decode(&struct{}{}); err != io.EOF {
					return nil, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
				}
				doc, err := manualCell(ctx, c, input)
				if err != nil {
					return nil, redactCreationError(err, containerEnvSecrets(input.Spec.Containers))
				}
				data, err := json.Marshal(doc)
				if err != nil {
					return nil, err
				}
				digest := fmt.Sprintf("%x", sha256.Sum256(append(data, byteBool(input.Spec.IgnoreDiskPressure))))
				if strings.HasSuffix(r.URL.Path, "/preview") {
					return map[string]any{"document": redactCell(doc), "preview": digest}, nil
				}
				if input.Preview != digest {
					return nil, fmt.Errorf("%w: inputs or scope changed; review a new preview", errdefs.ErrConfig)
				}
				doc.Spec.IgnoreDiskPressure = input.Spec.IgnoreDiskPressure
				result, createErr := c.MaterializeCell(ctx, doc)
				if createErr != nil {
					return nil, redactCreationError(createErr, containerEnvSecrets(input.Spec.Containers))
				}
				if !result.Created {
					return nil, fmt.Errorf(
						"%w: cell %q already exists in %s / %s / %s; refresh inventory before retrying",
						errdefs.ErrCellAlreadyExists,
						doc.Metadata.Name,
						doc.Spec.RealmID,
						doc.Spec.SpaceID,
						doc.Spec.StackID,
					)
				}
				return redactCell(result.Cell), nil
			}),
		)
	}
}

func manualCell(ctx context.Context, c kukeonv1.Client, input manualCellRequest) (v1beta1.CellDoc, error) {
	if err := validateManualCellInput(input); err != nil {
		return v1beta1.CellDoc{}, err
	}
	doc := manualCellDocument(input)
	// Re-check the coordinates on the document itself: these are the values
	// that reach the daemon's metadata paths.
	if !filepath.IsLocal(doc.Metadata.Name) || !safePathSegment(doc.Metadata.Name) ||
		!filepath.IsLocal(doc.Spec.RealmID) || !safePathSegment(doc.Spec.RealmID) ||
		!filepath.IsLocal(doc.Spec.SpaceID) || !safePathSegment(doc.Spec.SpaceID) ||
		!filepath.IsLocal(doc.Spec.StackID) || !safePathSegment(doc.Spec.StackID) {
		return v1beta1.CellDoc{}, fmt.Errorf("%w: unsafe cell coordinate", errdefs.ErrConfig)
	}
	canonical, err := apischeme.ConvertCellDocToInternal(doc)
	if err != nil {
		return v1beta1.CellDoc{}, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	// The daemon applies the same normalization on its JSON-RPC boundary.
	// Reflect the explicit `root: true` shorthand in the preview without
	// generating Kukeon's default root container in the web server.
	doc.Spec.RootContainerID = canonical.Spec.RootContainerID

	stackResult, err := c.GetStack(ctx, v1beta1.StackDoc{
		Metadata: v1beta1.StackMetadata{Name: doc.Spec.StackID},
		Spec:     v1beta1.StackSpec{RealmID: doc.Spec.RealmID, SpaceID: doc.Spec.SpaceID},
	})
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !stackResult.MetadataExists {
		return v1beta1.CellDoc{}, errdefs.ErrStackNotFound
	}

	existing, err := c.GetCell(ctx, doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return v1beta1.CellDoc{}, err
	}
	if err == nil && existing.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w: cell %q already exists in %s / %s / %s",
			errdefs.ErrCellAlreadyExists,
			doc.Metadata.Name,
			doc.Spec.RealmID,
			doc.Spec.SpaceID,
			doc.Spec.StackID,
		)
	}
	return doc, nil
}

func validateManualCellInput(input manualCellRequest) error {
	if err := validateManualCoordinate("cell name", input.Name); err != nil {
		return err
	}
	if err := naming.ValidateHierarchyName("cell", strings.TrimSpace(input.Name)); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	for name, value := range map[string]string{"realm": input.Realm, "space": input.Space, "stack": input.Stack} {
		if err := validateManualCoordinate(name, value); err != nil {
			return err
		}
	}
	seenContainers := make(map[string]struct{}, len(input.Spec.Containers))
	for i, container := range input.Spec.Containers {
		if container.ContainerdID != "" || container.RealmID != "" || container.SpaceID != "" ||
			container.StackID != "" || container.CellID != "" || container.KukeonGroupGID != 0 {
			return fmt.Errorf("%w: container %d includes daemon-owned fields", errdefs.ErrConfig, i+1)
		}
		if err := validateManualCoordinate(fmt.Sprintf("container %d id", i+1), container.ID); err != nil {
			return err
		}
		containerID := strings.TrimSpace(container.ID)
		if err := naming.ValidateHierarchyName("container", containerID); err != nil {
			return fmt.Errorf("%w: container %d: %w", errdefs.ErrConfig, i+1, err)
		}
		if _, exists := seenContainers[containerID]; exists {
			return fmt.Errorf("%w: duplicate container id %q", errdefs.ErrConfig, containerID)
		}
		seenContainers[containerID] = struct{}{}
		if strings.TrimSpace(container.Image) == "" {
			return fmt.Errorf("%w: container %q image is required", errdefs.ErrConfig, container.ID)
		}
	}
	if input.Spec.RootContainerID != "" {
		if err := validateManualCoordinate("root container id", input.Spec.RootContainerID); err != nil {
			return err
		}
	}
	return nil
}

func manualCellDocument(input manualCellRequest) v1beta1.CellDoc {
	name := strings.TrimSpace(input.Name)
	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:        name,
			Labels:      input.Labels,
			Annotations: input.Annotations,
		},
		Spec: v1beta1.CellSpec{
			ID:                  name,
			RealmID:             strings.TrimSpace(input.Realm),
			SpaceID:             strings.TrimSpace(input.Space),
			StackID:             strings.TrimSpace(input.Stack),
			RootContainerID:     strings.TrimSpace(input.Spec.RootContainerID),
			Tty:                 input.Spec.Tty,
			Containers:          append([]v1beta1.ContainerSpec{}, input.Spec.Containers...),
			AutoDelete:          input.Spec.AutoDelete,
			NestedCgroupRuntime: input.Spec.NestedCgroupRuntime,
			IgnoreDiskPressure:  input.Spec.IgnoreDiskPressure,
		},
	}
	for i := range doc.Spec.Containers {
		doc.Spec.Containers[i].ID = strings.TrimSpace(doc.Spec.Containers[i].ID)
	}
	return doc
}

func validateManualCoordinate(label, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%w: %s is required", errdefs.ErrConfig, label)
	}
	if !filepath.IsLocal(value) || !safePathSegment(value) {
		return fmt.Errorf("%w: unsafe %s", errdefs.ErrConfig, label)
	}
	return nil
}

func containerEnvSecrets(containers []v1beta1.ContainerSpec) []string {
	var secrets []string
	for _, container := range containers {
		secrets = append(secrets, envSecrets(container.Env)...)
	}
	return secrets
}

type redactedCreationError struct {
	err     error
	message string
}

func (e *redactedCreationError) Error() string { return e.message }
func (e *redactedCreationError) Unwrap() error { return e.err }

func redactCreationError(err error, secrets []string) error {
	if err == nil || len(secrets) == 0 {
		return err
	}
	message := err.Error()
	for _, secret := range secrets {
		if secret != "" {
			message = strings.ReplaceAll(message, secret, "[redacted]")
		}
	}
	if message == err.Error() {
		return err
	}
	return &redactedCreationError{err: err, message: message}
}

func envSecrets(entries []string) []string {
	secrets := make([]string, 0, len(entries))
	for _, entry := range entries {
		_, value, ok := strings.Cut(entry, "=")
		if ok && value != "" {
			secrets = append(secrets, value)
		}
	}
	return secrets
}
