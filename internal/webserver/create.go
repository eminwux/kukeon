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

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const maxCreateBody = 64 << 10

type createConfigRequest struct {
	Config             v1beta1.CellConfigMetadata `json:"config"`
	Name               string                     `json:"name"`
	ResolvedName       string                     `json:"resolvedName,omitempty"`
	Env                []string                   `json:"env"`
	IgnoreDiskPressure bool                       `json:"ignoreDiskPressure"`
	Preview            string                     `json:"preview"`
}

func registerCreateRoutes(mux *http.ServeMux, dial Dial) {
	registerManualCreateRoutes(mux, dial)
	for _, action := range []string{"preview", "create"} {
		mux.HandleFunc(
			"POST /api/cells/from-config/"+action,
			resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				// Mutations require a same-origin browser request and JSON, including preview.
				if r.Header.Get("Origin") != "http://"+r.Host || r.Header.Get("Content-Type") != "application/json" {
					return nil, fmt.Errorf("%w: same-origin JSON request required", errdefs.ErrConfig)
				}
				var input createConfigRequest
				decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCreateBody))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&input); err != nil {
					return nil, fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
				}
				if err := decoder.Decode(&struct{}{}); err != io.EOF {
					return nil, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
				}
				rendered, err := configCell(ctx, c, input)
				if err != nil {
					return nil, redactCreationError(err, envSecrets(input.Env))
				}
				// The preview token binds the daemon's render digest to the
				// disk-pressure choice the operator reviewed.
				digest := fmt.Sprintf("%x", sha256.Sum256(
					append([]byte(rendered.Digest), byteBool(input.IgnoreDiskPressure))))
				if strings.HasSuffix(r.URL.Path, "/preview") {
					return map[string]any{
						"document": redactCell(rendered.Doc),
						"preview":  digest,
						"digest":   rendered.Digest,
						"values":   redactRenderedValues(rendered.Values, envSecrets(input.Env)),
					}, nil
				}
				if input.Preview != digest {
					return nil, fmt.Errorf("%w: inputs or config changed; preview again", errdefs.ErrConfig)
				}
				request := rendered.Request
				request.IgnoreDiskPressure = input.IgnoreDiskPressure
				request.ExpectedDigest = rendered.Digest
				created, createErr := c.CreateCellFromConfig(ctx, request)
				if createErr != nil {
					if errors.Is(createErr, errdefs.ErrRenderDigestMismatch) {
						return nil, fmt.Errorf("%w: inputs or config changed; preview again", errdefs.ErrConfig)
					}
					return nil, redactCreationError(createErr, envSecrets(input.Env))
				}
				if !created.Created {
					return nil, errdefs.ErrCellAlreadyExists
				}
				return redactCell(created.Cell), nil
			}),
		)
	}
}

func configCell(ctx context.Context, c kukeonv1.Client, input createConfigRequest) (configRender, error) {
	var empty configRender
	requestedName, err := reviewedCellName(input)
	if err != nil {
		return empty, err
	}
	configMetadata, err := configLookup(input.Config)
	if err != nil {
		return empty, err
	}
	cfg, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: configMetadata})
	if err != nil {
		return empty, err
	}
	if !cfg.MetadataExists {
		return empty, errdefs.ErrConfigNotFound
	}
	if !safeBlueprintRef(cfg.Config.Spec.Blueprint) {
		return empty, fmt.Errorf("%w: unsafe blueprint reference", errdefs.ErrConfig)
	}
	normalized, err := normalizeEnv(input.Env)
	if err != nil {
		return empty, err
	}
	// kukeond renders the cell (#1418): defaults merge, slot fills, realm
	// images and — for an omitted name — the generated <prefix>-<6hex>.
	request := kukeonv1.CellFromConfigRequest{
		Config:       configMetadata,
		Name:         requestedName,
		EnvOverrides: normalized,
	}
	preview, err := c.PreviewCellFromConfig(ctx, request)
	if err != nil {
		return empty, renderError(err)
	}
	doc := preview.Cell
	if !safeCellCoordinates(doc) {
		return empty, fmt.Errorf("%w: unsafe materialized cell coordinate", errdefs.ErrConfig)
	}
	request.Name = doc.Metadata.Name
	if err = checkCreationTarget(ctx, c, doc); err != nil {
		return empty, err
	}
	return configRender{Doc: doc, Digest: preview.Digest, Values: preview.Values, Request: request}, nil
}

// reviewedCellName returns the cell name the request asks for — the typed
// name, else the name a previous preview resolved — after the path-safety and
// hierarchy-name checks. Empty means "let the daemon allocate one".
func reviewedCellName(input createConfigRequest) (string, error) {
	name := strings.TrimSpace(input.Name)
	resolvedName := strings.TrimSpace(input.ResolvedName)
	if name != "" && resolvedName != "" && name != resolvedName {
		return "", fmt.Errorf("%w: the reviewed cell name changed; preview again", errdefs.ErrConfig)
	}
	if name != "" && !safeSegment(name) {
		return "", fmt.Errorf("%w: unsafe cell name", errdefs.ErrConfig)
	}
	if resolvedName != "" && !safeSegment(resolvedName) {
		return "", fmt.Errorf("%w: unsafe resolved cell name", errdefs.ErrConfig)
	}
	requested := name
	if requested == "" {
		requested = resolvedName
	}
	if requested != "" {
		if err := naming.ValidateHierarchyName("cell", requested); err != nil {
			return "", fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
		}
	}
	return requested, nil
}

// configLookup validates the browser-selected Config coordinates before any
// daemon read.
func configLookup(md v1beta1.CellConfigMetadata) (v1beta1.CellConfigMetadata, error) {
	if md.Realm == "" || md.Name == "" || (md.Stack != "" && md.Space == "") {
		return v1beta1.CellConfigMetadata{}, fmt.Errorf("%w: select a scoped config", errdefs.ErrConfig)
	}
	for _, field := range []struct{ label, value string }{
		{"config space", md.Space},
		{"config stack", md.Stack},
		{"config name", md.Name},
		{"config realm", md.Realm},
	} {
		if field.value != "" && !safeSegment(field.value) {
			return v1beta1.CellConfigMetadata{}, fmt.Errorf("%w: unsafe %s", errdefs.ErrConfig, field.label)
		}
	}
	return v1beta1.CellConfigMetadata{Name: md.Name, Realm: md.Realm, Space: md.Space, Stack: md.Stack}, nil
}

// safeSegment reports whether value is a local, single path segment.
func safeSegment(value string) bool {
	return filepath.IsLocal(value) && safePathSegment(value)
}

// safeBlueprintRef rejects a persisted blueprint reference that would escape
// the daemon's metadata tree.
func safeBlueprintRef(ref v1beta1.CellConfigBlueprintRef) bool {
	return safeSegment(ref.Name) && safeSegment(ref.Realm) &&
		(ref.Space == "" || safeSegment(ref.Space)) &&
		(ref.Stack == "" || safeSegment(ref.Stack))
}

// safeCellCoordinates rejects a rendered cell whose name or scope would escape
// the daemon's metadata tree.
func safeCellCoordinates(doc v1beta1.CellDoc) bool {
	return safeSegment(doc.Metadata.Name) && safeSegment(doc.Spec.RealmID) &&
		(doc.Spec.SpaceID == "" || safeSegment(doc.Spec.SpaceID)) &&
		(doc.Spec.StackID == "" || safeSegment(doc.Spec.StackID))
}

// normalizeEnv validates KEY=VALUE entries, collapsing exact duplicates and
// rejecting conflicting values for the same key.
func normalizeEnv(entries []string) ([]string, error) {
	seen := map[string]string{}
	normalized := []string{}
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%w: environment entries require KEY=VALUE", errdefs.ErrConfig)
		}
		if old, exists := seen[key]; exists {
			if old != value {
				return nil, fmt.Errorf("%w: conflicting environment values for %s", errdefs.ErrConfig, key)
			}
			continue
		}
		seen[key] = value
		normalized = append(normalized, key+"="+value)
	}
	return normalized, nil
}

// checkCreationTarget requires the rendered cell's stack to exist and its name
// to be free.
func checkCreationTarget(ctx context.Context, c kukeonv1.Client, doc v1beta1.CellDoc) error {
	stack, err := c.GetStack(
		ctx,
		v1beta1.StackDoc{
			Metadata: v1beta1.StackMetadata{Name: doc.Spec.StackID},
			Spec:     v1beta1.StackSpec{RealmID: doc.Spec.RealmID, SpaceID: doc.Spec.SpaceID},
		},
	)
	if err != nil {
		return err
	}
	if !stack.MetadataExists {
		return errdefs.ErrStackNotFound
	}
	existing, err := c.GetCell(ctx, doc)
	if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
		return err
	}
	if err == nil && existing.MetadataExists {
		return fmt.Errorf(
			"%w: cell %q already exists in %s / %s / %s; preview again or choose another name",
			errdefs.ErrCellAlreadyExists, doc.Metadata.Name, doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID,
		)
	}
	return nil
}

// configRender is a daemon-side preview plus the request that reproduces it.
type configRender struct {
	Doc     v1beta1.CellDoc
	Digest  string
	Values  []v1beta1.RenderedValue
	Request kukeonv1.CellFromConfigRequest
}

// renderError keeps the daemon's lookup / collision sentinels and classifies
// every other render failure (slot fills, parameters) as a config error.
func renderError(err error) error {
	for _, sentinel := range []error{
		errdefs.ErrConfigNotFound, errdefs.ErrBlueprintNotFound, errdefs.ErrCellAlreadyExists,
		errdefs.ErrStackNotFound, errdefs.ErrSpaceNotFound, errdefs.ErrRealmNotFound, errdefs.ErrConfig,
	} {
		if errors.Is(err, sentinel) {
			return err
		}
	}
	return fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
}

func byteBool(value bool) byte {
	if value {
		return 1
	}
	return 0
}

// redactRenderedValues prepares the merge explanation for the browser. Every
// key keeps its origin, but scalar values are withheld — the same policy
// redactCell applies to container env and provenance params — and any --env
// secret that leaks into a repo/secret/image description is masked.
func redactRenderedValues(values []v1beta1.RenderedValue, secrets []string) []v1beta1.RenderedValue {
	out := make([]v1beta1.RenderedValue, 0, len(values))
	for _, v := range values {
		if v.Kind == v1beta1.RenderKindValue {
			v.Value = ""
		}
		for _, secret := range secrets {
			if secret != "" {
				v.Value = strings.ReplaceAll(v.Value, secret, "[redacted]")
			}
		}
		out = append(out, v)
	}
	return out
}
