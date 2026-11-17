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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

type healthClient struct {
	kukeonv1.FakeClient

	reports []kukeonv1.HealthReport
	configs map[string]v1beta1.CellConfigDoc
	created []kukeonv1.CellFromConfigRequest
}

func (c *healthClient) Close() error { return nil }

func (c *healthClient) ListHealthReports(context.Context) ([]kukeonv1.HealthReportSummary, error) {
	var out []kukeonv1.HealthReportSummary
	for _, report := range c.reports {
		out = append(out, kukeonv1.HealthReportSummary{ID: report.ID, Summary: report.Summary, Findings: len(report.Findings)})
	}
	return out, nil
}

func (c *healthClient) ReadHealthReport(_ context.Context, id string) (kukeonv1.HealthReport, error) {
	for _, report := range c.reports {
		if report.ID == id || id == kukeonv1.HealthReportLatest {
			return report, nil
		}
	}
	return kukeonv1.HealthReport{}, errdefs.ErrHealthReportNotFound
}

func (c *healthClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	stored, ok := c.configs[doc.Metadata.Name]
	return kukeonv1.GetConfigResult{Config: stored, MetadataExists: ok}, nil
}

func (c *healthClient) CreateCellFromConfig(
	_ context.Context, req kukeonv1.CellFromConfigRequest,
) (kukeonv1.CreateCellFromConfigResult, error) {
	c.created = append(c.created, req)
	var result kukeonv1.CreateCellFromConfigResult
	result.Cell.Metadata.Name = "sweep-1a2b3c"
	result.Created = true
	return result, nil
}

func sweepConfig(name string, labels map[string]string) v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{Metadata: v1beta1.CellConfigMetadata{
		Name: name, Realm: "default", Space: "default", Stack: "default", Labels: labels,
	}}
}

func serveHealth(t *testing.T, c *healthClient, method, url, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler("http://localhost:5129", "unix:///run/kukeon/kukeond.sock",
		func(context.Context) (kukeonv1.Client, error) { return c, nil })
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(method, "http://localhost:5129"+url, strings.NewReader(body))
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	return w
}

func TestHealthReportRoutes(t *testing.T) {
	empty := serveHealth(t, &healthClient{}, http.MethodGet, "/api/health/reports", "", "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != "[]" {
		t.Fatalf("empty list: %d %s", empty.Code, empty.Body)
	}
	if w := serveHealth(t, &healthClient{}, http.MethodGet, "/api/health/reports/latest", "", ""); w.Code != http.StatusNotFound ||
		!strings.Contains(w.Body.String(), "not_found") {
		t.Fatalf("no latest report: %d %s", w.Code, w.Body)
	}

	client := &healthClient{reports: []kukeonv1.HealthReport{{
		ID: "r1", Summary: "one defect",
		Findings: []kukeonv1.HealthFinding{{
			Signature: "check host/cni-plugins", Classification: kukeonv1.HealthClassKukeon, Recurring: true,
			Severity: kukeonv1.HealthSeverityError, Evidence: []string{"missing: bridge"},
			IssueURL: "https://github.com/acme/ops/issues/3",
		}},
	}}}
	list := serveHealth(t, client, http.MethodGet, "/api/health/reports", "", "")
	var summaries []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &summaries); err != nil || len(summaries) != 1 ||
		summaries[0]["id"] != "r1" || summaries[0]["findings"] != float64(1) {
		t.Fatalf("list: %s (%v)", list.Body, err)
	}
	read := serveHealth(t, client, http.MethodGet, "/api/health/reports/latest", "", "")
	var report map[string]any
	if err := json.Unmarshal(read.Body.Bytes(), &report); err != nil || report["id"] != "r1" {
		t.Fatalf("read: %s (%v)", read.Body, err)
	}
	finding := report["findings"].([]any)[0].(map[string]any)
	if finding["classification"] != "kukeon" || finding["recurring"] != true ||
		finding["issueUrl"] != "https://github.com/acme/ops/issues/3" {
		t.Fatalf("finding = %v", finding)
	}
}

func TestRunSweepNow(t *testing.T) {
	sweepLabels := map[string]string{
		v1beta1.LabelTeam: "kukeon", v1beta1.LabelRole: "majordomo", v1beta1.LabelTask: "health-sweep",
	}
	client := &healthClient{configs: map[string]v1beta1.CellConfigDoc{
		"kukeon-majordomo-claude-health-sweep": sweepConfig("kukeon-majordomo-claude-health-sweep", sweepLabels),
		"kukeon-majordomo-claude": sweepConfig("kukeon-majordomo-claude", map[string]string{
			v1beta1.LabelTeam: "kukeon", v1beta1.LabelRole: "majordomo",
		}),
		"other-dev-claude-health-sweep": sweepConfig("other-dev-claude-health-sweep", map[string]string{
			v1beta1.LabelTeam: "other", v1beta1.LabelRole: "dev", v1beta1.LabelTask: "health-sweep",
		}),
	}}
	body := func(name string) string {
		return `{"config":{"name":"` + name + `","realm":"default","space":"default","stack":"default"}}`
	}

	w := serveHealth(t, client, http.MethodPost, "/api/health/sweep", "application/json", body("kukeon-majordomo-claude-health-sweep"))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "sweep-1a2b3c") {
		t.Fatalf("run sweep: %d %s", w.Code, w.Body)
	}
	if len(client.created) != 1 || !client.created[0].Start || client.created[0].Name != "" ||
		client.created[0].Config.Name != "kukeon-majordomo-claude-health-sweep" {
		t.Fatalf("create request = %+v", client.created)
	}

	// Only a majordomo health-sweep config runs here, whatever it is named.
	for _, name := range []string{"kukeon-majordomo-claude", "other-dev-claude-health-sweep"} {
		if w = serveHealth(t, client, http.MethodPost, "/api/health/sweep", "application/json", body(name)); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w = serveHealth(t, client, http.MethodPost, "/api/health/sweep", "application/json", body("missing")); w.Code != http.StatusNotFound {
		t.Errorf("missing config: %d %s", w.Code, w.Body)
	}
	if w = serveHealth(t, client, http.MethodPost, "/api/health/sweep", "text/plain", body("x")); w.Code != http.StatusBadRequest {
		t.Errorf("non-JSON request: %d", w.Code)
	}
	if w = serveHealth(t, client, http.MethodPost, "/api/health/sweep", "application/json", `{"config":{"name":"x"},"extra":1}`); w.Code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", w.Code)
	}
	if len(client.created) != 1 {
		t.Fatalf("a refused request created a cell: %+v", client.created)
	}
}
