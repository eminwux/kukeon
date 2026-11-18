// Copyright 2025 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"fmt"
	"io"
	"net/http"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	// majordomoTeam and majordomoRole identify majordomo's configs by label
	// (issue #1509); nothing here matches on a config name.
	majordomoTeam = "kukeon"
	majordomoRole = "majordomo"
	sweepTask     = "health-sweep"
	maxSweepBody  = 4 << 10
)

// sweepRequest names the health-sweep task config to run once.
type sweepRequest struct {
	Config v1beta1.CellConfigMetadata `json:"config"`
}

// registerHealthRoutes serves the Health page: the reports kukeond stores and
// the one-shot sweep. Every route goes through a daemon RPC; the server reads
// no kukeon file itself.
func registerHealthRoutes(mux *http.ServeMux, dial Dial) {
	mux.HandleFunc("GET /api/health/reports", resourceRoute(dial,
		func(ctx context.Context, c kukeonv1.Client, _ *http.Request) (any, error) {
			reports, err := c.ListHealthReports(ctx)
			if err != nil {
				return nil, err
			}
			if reports == nil {
				reports = []kukeonv1.HealthReportSummary{}
			}
			return reports, nil
		}))
	mux.HandleFunc("GET /api/health/reports/{id}", resourceRoute(dial,
		func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			return c.ReadHealthReport(ctx, r.PathValue("id"))
		}))
	// "Run sweep now" is `kuke run -d <sweep config>`: one create-and-start
	// of a cell from the task config, which deletes itself when it ends.
	mux.HandleFunc("POST /api/health/sweep", resourceRoute(dial,
		func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			if r.Header.Get("Content-Type") != jsonContentType {
				return nil, fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
			}
			var input sweepRequest
			decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxSweepBody))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&input); err != nil {
				return nil, fmt.Errorf("%w: invalid request: %w", errdefs.ErrConfig, err)
			}
			if err := decoder.Decode(&struct{}{}); err != io.EOF {
				return nil, fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
			}
			// The same coordinate checks as creating a cell from a config: no
			// request value reaches a daemon path lookup unvalidated.
			lookup, err := configLookup(input.Config)
			if err != nil {
				return nil, err
			}
			stored, err := c.GetConfig(ctx, v1beta1.CellConfigDoc{
				APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig, Metadata: lookup,
			})
			if err != nil {
				return nil, err
			}
			if !stored.MetadataExists {
				return nil, errdefs.ErrConfigNotFound
			}
			// The endpoint runs the sweep and nothing else: any other config
			// goes through the create-from-config review flow.
			labels := stored.Config.Metadata.Labels
			if labels[v1beta1.LabelTeam] != majordomoTeam || labels[v1beta1.LabelRole] != majordomoRole ||
				labels[v1beta1.LabelTask] != sweepTask {
				return nil, fmt.Errorf("%w: %s is not a majordomo health-sweep config", errdefs.ErrConfig, input.Config.Name)
			}
			created, err := c.CreateCellFromConfig(ctx, kukeonv1.CellFromConfigRequest{
				Config: lookup, Start: true,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"cell": redactCell(created.Cell)}, nil
		}))
}
