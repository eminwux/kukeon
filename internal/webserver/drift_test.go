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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const driftPath = "/api/realms/r/spaces/s/stacks/t/cells/c/drift"

type driftClient struct {
	kukeonv1.FakeClient

	request v1beta1.CellDoc
	result  kukeonv1.DiffCellFromConfigResult
	err     error
}

func (c *driftClient) Close() error { return nil }

func (c *driftClient) DiffCellFromConfig(
	_ context.Context, doc v1beta1.CellDoc,
) (kukeonv1.DiffCellFromConfigResult, error) {
	c.request = doc
	return c.result, c.err
}

func serveDrift(t *testing.T, c *driftClient) *httptest.ResponseRecorder {
	t.Helper()
	handler, err := newHandler(
		"http://localhost:5129",
		"unix:///test",
		func(context.Context) (kukeonv1.Client, error) { return c, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost:5129"+driftPath, nil))
	return w
}

func driftCell(containers ...v1beta1.ContainerSpec) v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: "c"},
		Spec: v1beta1.CellSpec{
			Containers: containers,
			RuntimeEnv: []string{"RUNTIME=runtime-secret"},
			Provenance: &v1beta1.CellProvenance{
				Params:       map[string]string{"prompt": "param-secret"},
				EnvOverrides: []string{"OVERRIDE=override-secret"},
			},
		},
		Status: v1beta1.CellStatus{OutOfSyncReason: "status-secret"},
	}
}

func decodeDrift(t *testing.T, w *httptest.ResponseRecorder) cellDriftResponse {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	var body cellDriftResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func yamlLines(doc string) []string {
	var lines []string
	for line := range strings.SplitSeq(doc, "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	return lines
}

// TestCellDriftMasksEnvAsAPair is AC4 of #1768: no value of Env, RuntimeEnv,
// Provenance.Params or Provenance.EnvOverrides reaches the browser; an env
// key with equal values masks the same on both sides, one with different
// values masks differently, and an added or removed key is masked.
func TestCellDriftMasksEnvAsAPair(t *testing.T) {
	live := driftCell(
		v1beta1.ContainerSpec{ID: "work", Env: []string{
			"SAME=same-secret", "DIFF=live-secret", "GONE=removed-secret", "bare-secret",
		}},
		v1beta1.ContainerSpec{ID: "old", Env: []string{"OLD=orphan-secret"}},
	)
	desired := driftCell(
		v1beta1.ContainerSpec{ID: "work", Env: []string{
			"SAME=same-secret", "DIFF=desired-secret", "NEW=added-secret", "bare-secret",
		}},
		v1beta1.ContainerSpec{ID: "side", Env: []string{"SIDE=added-container-secret"}},
	)
	c := &driftClient{result: kukeonv1.DiffCellFromConfigResult{
		Live: live, Desired: &desired, ConfigFound: true,
		Reasons:    []string{"blueprint claude-host changed", "spec differs at containers[work].env"},
		Paths:      []string{"containers[work].env"},
		ChangeType: "compatible",
	}}

	w := serveDrift(t, c)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("a secret reached the browser: %s", w.Body)
	}
	body := decodeDrift(t, w)
	if c.request.Metadata.Name != "c" || c.request.Spec.RealmID != "r" ||
		c.request.Spec.SpaceID != "s" || c.request.Spec.StackID != "t" {
		t.Errorf("request = %+v", c.request)
	}
	if !body.ConfigFound || body.ChangeType != "compatible" ||
		!slices.Equal(body.Paths, []string{"containers[work].env"}) || len(body.Reasons) != 2 {
		t.Errorf("body = %+v", body)
	}

	liveLines, desiredLines := yamlLines(body.Live), yamlLines(body.Desired)
	for _, want := range []string{
		"- SAME=" + envMask, "- DIFF=" + envMask, "- GONE=" + envMask, "- " + envMask, "- OLD=" + envMask,
	} {
		if !slices.Contains(liveLines, want) {
			t.Errorf("live YAML lacks %q:\n%s", want, body.Live)
		}
	}
	for _, want := range []string{
		"- SAME=" + envMask, "- DIFF=" + envMaskChanged, "- NEW=" + envMask, "- " + envMask, "- SIDE=" + envMask,
	} {
		if !slices.Contains(desiredLines, want) {
			t.Errorf("desired YAML lacks %q:\n%s", want, body.Desired)
		}
	}
	for _, doc := range []string{body.Live, body.Desired} {
		if strings.Contains(doc, "status:") || strings.Contains(doc, "provenance") ||
			strings.Contains(doc, "runtimeEnv") {
			t.Errorf("manifest keeps status or inputs:\n%s", doc)
		}
	}

	if live.Spec.Containers[0].Env[1] != "DIFF=live-secret" ||
		desired.Spec.Containers[0].Env[1] != "DIFF=desired-secret" ||
		live.Spec.Provenance.Params["prompt"] != "param-secret" {
		t.Error("masking mutated client data")
	}
}

// TestCellDriftIdenticalWhenInSync: equal documents serialize, masked, to
// identical YAML (AC2 of #1768 through the route).
func TestCellDriftIdenticalWhenInSync(t *testing.T) {
	live := driftCell(v1beta1.ContainerSpec{ID: "work", Image: "img", Env: []string{"A=1", "B=2"}})
	desired := driftCell(v1beta1.ContainerSpec{ID: "work", Image: "img", Env: []string{"A=1", "B=2"}})
	body := decodeDrift(t, serveDrift(t, &driftClient{result: kukeonv1.DiffCellFromConfigResult{
		Live: live, Desired: &desired, ConfigFound: true, ChangeType: "none",
	}}))
	if body.Live == "" || body.Live != body.Desired {
		t.Errorf("in-sync YAML differs:\n%s\n---\n%s", body.Live, body.Desired)
	}
	if body.Reasons == nil || body.Paths == nil {
		t.Errorf("reasons/paths must be [] not null: %+v", body)
	}
}

// TestCellDriftConfigDeleted: a deleted lineage Config returns the live
// document, configFound false and no desired document (AC6 of #1768).
func TestCellDriftConfigDeleted(t *testing.T) {
	live := driftCell(v1beta1.ContainerSpec{ID: "work", Env: []string{"A=deleted-secret"}})
	w := serveDrift(t, &driftClient{result: kukeonv1.DiffCellFromConfigResult{
		Live: live, Reasons: []string{"lineage Config deleted"},
	}})
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("a secret reached the browser: %s", w.Body)
	}
	body := decodeDrift(t, w)
	if body.ConfigFound || body.Desired != "" || !slices.Equal(body.Reasons, []string{"lineage Config deleted"}) {
		t.Errorf("body = %+v", body)
	}
	if !strings.Contains(w.Body.String(), `"configFound":false`) {
		t.Errorf("configFound missing from %s", w.Body)
	}
}

// TestCellDriftErrors: a cell not made from a Config is a 409 after crossing
// the daemon wire, a missing cell a 404, and a render failure keeps its text.
func TestCellDriftErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("cell %q: %w", "c", errdefs.ErrCellNotFromConfig), http.StatusConflict, "not_from_config"},
		{fmt.Errorf("lookup: %w", errdefs.ErrCellNotFound), http.StatusNotFound, "not_found"},
		{fmt.Errorf("resolve blueprint %q: %w", "dev", errdefs.ErrBlueprintNotFound), http.StatusNotFound, "not_found"},
	} {
		wire := kukeonv1.FromAPIError(kukeonv1.ToAPIError(tc.err))
		w := serveDrift(t, &driftClient{err: wire})
		if w.Code != tc.status {
			t.Errorf("%v: status=%d body=%s", tc.err, w.Code, w.Body)
			continue
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != tc.code || body.Error.Message != tc.err.Error() {
			t.Errorf("%v: error = %+v", tc.err, body.Error)
		}
	}
}
