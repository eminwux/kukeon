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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/eminwux/kukeon/internal/agent"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// New agent and display names (issue #1692).
//
//	GET  /api/agents/templates                 each harness's agent template, and whether it exists
//	POST /api/agents                           copy a template into a Config, create and start its first cell
//	POST /api/configs/display-name             set or clear a Config's display name
//	POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/display-name
//	                                           set or clear a cell's display name
func registerAgentRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc("GET /api/agents/templates", resourceRoute(dial, listAgentTemplates))
	mux.HandleFunc("POST /api/agents", createAgentHandler(dial))
	mux.HandleFunc("POST /api/configs/display-name", resourceRoute(dial, setConfigDisplayName))
	mux.HandleFunc(
		"POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/display-name",
		resourceRoute(dial, setCellDisplayName),
	)
}

// agentRef is a Config's or a cell's coordinates in a response.
type agentRef struct {
	Name        string            `json:"name"`
	Realm       string            `json:"realm"`
	Space       string            `json:"space,omitempty"`
	Stack       string            `json:"stack,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

func configRef(md v1beta1.CellConfigMetadata) agentRef {
	return agentRef{
		Name: md.Name, Realm: md.Realm, Space: md.Space, Stack: md.Stack,
		Labels: md.Labels, Annotations: md.Annotations,
	}
}

func cellRef(doc v1beta1.CellDoc) agentRef {
	return agentRef{
		Name: doc.Metadata.Name, Realm: doc.Spec.RealmID, Space: doc.Spec.SpaceID, Stack: doc.Spec.StackID,
		Annotations: doc.Metadata.Annotations,
	}
}

type agentTemplateView struct {
	Harness string `json:"harness"`
	Name    string `json:"name"`
	Realm   string `json:"realm"`
	Space   string `json:"space"`
	Stack   string `json:"stack"`
	Exists  bool   `json:"exists"`
}

func listAgentTemplates(ctx context.Context, c kukeonv1.Client, _ *http.Request) (any, error) {
	templates, err := agent.Templates(ctx, c)
	if err != nil {
		return nil, err
	}
	out := make([]agentTemplateView, 0, len(templates))
	for _, t := range templates {
		out = append(out, agentTemplateView{
			Harness: t.Harness, Name: t.Config.Name, Realm: t.Config.Realm,
			Space: t.Config.Space, Stack: t.Config.Stack, Exists: t.Exists,
		})
	}
	return map[string]any{"templates": out}, nil
}

type createAgentRequest struct {
	Harness     string `json:"harness"`
	DisplayName string `json:"displayName,omitempty"`
	Scope       *struct {
		Realm string `json:"realm"`
		Space string `json:"space,omitempty"`
		Stack string `json:"stack,omitempty"`
	} `json:"scope,omitempty"`
}

// decodeJSON decodes exactly one strict JSON document of at most
// maxCreateBody bytes into out.
func decodeJSON(r *http.Request, out any) error {
	if r.Header.Get("Content-Type") != jsonContentType {
		return fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxCreateBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
	}
	return nil
}

// createAgentHandler is New agent: the Config and the first cell are made
// under creationTimeout, and the cell is then started under the lifecycle
// timeout, as the lifecycle start route does. A failure after the Config is
// stored keeps it and reports the step (agent.StepError).
func createAgentHandler(dial Dial) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input createAgentRequest
		if err := decodeJSON(r, &input); err != nil {
			writeAPIError(w, err)
			return
		}
		req := agent.Request{Harness: input.Harness, DisplayName: input.DisplayName}
		if input.Scope != nil {
			req.Realm, req.Space, req.Stack = input.Scope.Realm, input.Scope.Space, input.Scope.Stack
		}
		ctx, cancel := context.WithTimeout(r.Context(), lifecycleTimeout)
		defer cancel()
		client, err := dial(ctx)
		if err != nil {
			writeAPIError(w, err)
			return
		}
		defer func() { _ = client.Close() }()

		createCtx, cancelCreate := context.WithTimeout(ctx, creationTimeout)
		cfg, err := agent.StoreConfig(createCtx, client, req, agent.Options{})
		var cell v1beta1.CellDoc
		if err == nil {
			cell, err = agent.CreateCell(createCtx, client, cfg.Metadata)
		}
		cancelCreate()
		if err == nil {
			cell, err = agent.StartCell(ctx, client, cfg.Metadata, cell)
		}
		if err != nil {
			writeAPIError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"config": configRef(cfg.Metadata), "cell": cellRef(cell)})
	}
}

type displayNameRequest struct {
	Config      *v1beta1.CellConfigMetadata `json:"config,omitempty"`
	DisplayName string                      `json:"displayName"`
}

func setConfigDisplayName(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	var input displayNameRequest
	if err := decodeJSON(r, &input); err != nil {
		return nil, err
	}
	if input.Config == nil {
		return nil, fmt.Errorf("%w: select a config", errdefs.ErrConfig)
	}
	md, err := configLookup(*input.Config)
	if err != nil {
		return nil, err
	}
	res, err := c.SetDisplayName(ctx, kukeonv1.SetDisplayNameRequest{
		Target: kukeonv1.DisplayNameTarget{
			Kind: kukeonv1.DisplayNameTargetConfig, Name: md.Name, Realm: md.Realm, Space: md.Space, Stack: md.Stack,
		},
		DisplayName: input.DisplayName,
	})
	if err != nil {
		return nil, err
	}
	md.Annotations = res.Annotations
	return map[string]any{"config": configRef(md)}, nil
}

func setCellDisplayName(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	doc, _, err := lifecycleCellDoc(r)
	if err != nil {
		return nil, err
	}
	var input displayNameRequest
	if err = decodeJSON(r, &input); err != nil {
		return nil, err
	}
	if input.Config != nil {
		return nil, fmt.Errorf("%w: a cell rename takes displayName only", errdefs.ErrConfig)
	}
	res, err := c.SetDisplayName(ctx, kukeonv1.SetDisplayNameRequest{
		Target: kukeonv1.DisplayNameTarget{
			Kind: kukeonv1.DisplayNameTargetCell, Name: doc.Metadata.Name,
			Realm: doc.Spec.RealmID, Space: doc.Spec.SpaceID, Stack: doc.Spec.StackID,
		},
		DisplayName: input.DisplayName,
	})
	if err != nil {
		return nil, err
	}
	doc.Metadata.Annotations = res.Annotations
	return map[string]any{"cell": cellRef(doc)}, nil
}

// agentErrorStatus maps the New agent and display-name sentinels.
func agentErrorStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errdefs.ErrAgentTemplateNotFound):
		return http.StatusNotFound, "template_not_found", true
	case errors.Is(err, errdefs.ErrInvalidDisplayName):
		return http.StatusBadRequest, codeInvalidRequest, true
	}
	return 0, "", false
}

// agentStepFields adds, for a New agent that failed after its Config was
// stored, the step that failed and what exists.
func agentStepFields(err error, fields map[string]any) {
	var stepErr *agent.StepError
	if !errors.As(err, &stepErr) {
		return
	}
	fields["step"] = string(stepErr.Step)
	fields["config"] = configRef(stepErr.Config)
	if stepErr.Cell != nil {
		fields["cell"] = cellRef(*stepErr.Cell)
	}
}
