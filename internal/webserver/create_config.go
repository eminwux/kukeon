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
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// createFromBlueprintRequest is the browser's answers to a blueprint's form:
// the CellConfig to save. Spec is the document's spec verbatim, so anything
// the blueprint contract can express — values, repo fills with their own git
// identity, secret fills, enabled components, modes, requires, a git override
// — travels without a second schema. Mode names the harness mode the preview
// renders a cell in (the Config itself serves every mode it enables).
type createFromBlueprintRequest struct {
	Config  v1beta1.CellConfigMetadata `json:"config"`
	Spec    v1beta1.CellConfigSpec     `json:"spec"`
	Mode    string                     `json:"mode,omitempty"`
	Preview string                     `json:"preview,omitempty"`
}

// registerConfigRoutes serves the "create a config from a blueprint form"
// flow (issue #1472, step 16). Preview renders a cell from the unsaved Config
// through the daemon's ordinary render — so the browser shows the same merge
// explanation `kuke` would, including the git identity and credential every
// repo ends up with — and create saves the Config through the daemon's
// existing create-config path once the reviewed preview still holds.
func registerConfigRoutes(mux *http.ServeMux, dial Dial) {
	for _, action := range []string{"preview", "create"} {
		mux.HandleFunc(
			"POST /api/configs/from-blueprint/"+action,
			resourceRoute(dial, func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
				// Mutations require JSON, including preview; the shared access policy guards Host/Origin.
				if r.Header.Get("Content-Type") != jsonContentType {
					return nil, fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
				}
				var input createFromBlueprintRequest
				decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCreateBody))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&input); err != nil {
					return nil, fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
				}
				if err := decoder.Decode(&struct{}{}); err != io.EOF {
					return nil, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
				}
				doc, err := draftConfig(input)
				if err != nil {
					return nil, err
				}
				existing, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{Metadata: doc.Metadata})
				if err != nil {
					return nil, err
				}
				if existing.MetadataExists {
					return nil, fmt.Errorf("%w: config %q already exists in this scope; choose another name",
						errdefs.ErrConfig, doc.Metadata.Name)
				}
				rendered, err := c.PreviewCellFromConfig(ctx, kukeonv1.CellFromConfigRequest{
					Config: doc.Metadata, Draft: &doc, Mode: strings.TrimSpace(input.Mode),
				})
				if err != nil {
					return nil, renderError(err)
				}
				// The token binds the saved document to the render the operator
				// reviewed: a changed form, blueprint or inherited default
				// invalidates it.
				encoded, err := json.Marshal(doc)
				if err != nil {
					return nil, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
				}
				token := fmt.Sprintf("%x", sha256.Sum256(append(encoded, []byte(rendered.Digest)...)))
				if strings.HasSuffix(r.URL.Path, "/preview") {
					return map[string]any{
						"config":   doc,
						"document": redactCell(rendered.Cell),
						"preview":  token,
						"digest":   rendered.Digest,
						"values":   redactRenderedValues(rendered.Values, nil),
					}, nil
				}
				if input.Preview != token {
					return nil, fmt.Errorf(
						"%w: the form, blueprint or defaults changed; preview again",
						errdefs.ErrConfig,
					)
				}
				created, err := c.CreateConfig(ctx, doc)
				if err != nil {
					return nil, renderError(err)
				}
				if !created.Created {
					return nil, fmt.Errorf(
						"%w: config %q already exists in this scope",
						errdefs.ErrConfig,
						doc.Metadata.Name,
					)
				}
				return created.Config, nil
			}),
		)
	}
}

// draftConfig builds the CellConfig document from the form and refuses any
// coordinate — the Config's own, its blueprint's, a secret's — that would
// escape the daemon's metadata tree.
func draftConfig(input createFromBlueprintRequest) (v1beta1.CellConfigDoc, error) {
	var empty v1beta1.CellConfigDoc
	md, err := configLookup(input.Config)
	if err != nil {
		return empty, err
	}
	if err = naming.ValidateHierarchyName("config", md.Name); err != nil {
		return empty, fmt.Errorf("%w: %w", errdefs.ErrConfig, err)
	}
	spec := input.Spec
	if !safeBlueprintRef(spec.Blueprint) {
		return empty, fmt.Errorf("%w: select a blueprint", errdefs.ErrConfig)
	}
	refs := []*v1beta1.ContainerSecretRef{}
	gitRefs := func(credential *v1beta1.GitCredential, signing *v1beta1.GitSigning) {
		if credential != nil {
			refs = append(refs, credential.SecretRef)
		}
		if signing != nil {
			refs = append(refs, signing.SecretRef, signing.AllowedSignersRef)
		}
	}
	for _, fill := range spec.Secrets {
		refs = append(refs, fill.SecretRef)
	}
	for _, fill := range spec.Repos {
		if fill.Git != nil {
			gitRefs(fill.Git.Credential, fill.Git.Signing)
		}
	}
	if spec.Git != nil {
		for i := range spec.Git.Credentials {
			gitRefs(&spec.Git.Credentials[i], nil)
		}
		gitRefs(nil, spec.Git.Signing)
	}
	for _, ref := range refs {
		if ref == nil {
			continue
		}
		for _, segment := range []string{ref.Name, ref.Realm, ref.Space, ref.Stack, ref.Cell} {
			if segment != "" && !safeSegment(segment) {
				return empty, fmt.Errorf("%w: unsafe secret reference", errdefs.ErrConfig)
			}
		}
	}
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig, Metadata: md, Spec: spec,
	}, nil
}
