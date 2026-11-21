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

// Tests for the per-cell connector method (issue #1551): a cell created from
// a Config may use another method of a connector the Config requires.

package controller_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func agentTokenFill(name string) map[string]intmodel.SecretFill {
	return map[string]intmodel.SecretFill{
		"agent-token": {SecretRef: &intmodel.ContainerSecretRef{Name: name, Realm: "kuke-system"}},
	}
}

func renderedComponent(values []ext.RenderedValue, name string) (ext.RenderedValue, bool) {
	for _, v := range values {
		if v.Kind == ext.RenderKindComponent && v.Key == name {
			return v, true
		}
	}
	return ext.RenderedValue{}, false
}

// TestPreviewCellFromConfig_ConnectorMethodSwapsComponent: a Config on the
// login method renders a cell on the token method when the cell picks it; the
// token slot resolves from a scope default, the login component is off, the
// choice is attributed to the cell and recorded in provenance, and the cell
// re-renders identically (no OutOfSync) from that provenance.
func TestPreviewCellFromConfig_ConnectorMethodSwapsComponent(t *testing.T) {
	fx := &connectorFixture{
		scope:   renderScope{realmDefaults: &intmodel.ScopeDefaults{Secrets: agentTokenFill("tok-default")}},
		secrets: map[string]intmodel.SecretSpec{"kuke-system/tok-default": {Connector: "agent"}},
	}
	mock := newConnectorRunner(t, fx, connectorConfig("agent-login"))
	req := previewRequest()
	req.ConnectorMethods = map[string]string{"agent": "token"}
	preview, err := setupTestController(t, mock).PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := secretNames(preview.Cell.Spec.Containers[0]); !reflect.DeepEqual(got, []string{"AGENT_TOKEN"}) {
		t.Fatalf("secrets = %v, want AGENT_TOKEN from the picked method", got)
	}
	status := connectorStatus(t, preview.Connectors, "agent")
	if status.Method != "token" || status.Component != "agent-token" || status.State != ext.ConnectorResolved ||
		status.Identity == nil || status.Identity.Name != "tok-default" {
		t.Fatalf("agent status = %+v, want the token method resolved to tok-default", status)
	}
	if _, on := renderedComponent(preview.Values, "agent-login"); on {
		t.Errorf("agent-login still enabled: %+v", preview.Values)
	}
	if v, on := renderedComponent(preview.Values, "agent-token"); !on || v.Origin != ext.RenderOriginCell {
		t.Errorf("agent-token component = %+v (enabled %v), want origin cell", v, on)
	}
	want := map[string]string{"agent": "token"}
	if got := preview.Cell.Spec.Provenance.ConnectorMethods; !reflect.DeepEqual(got, want) {
		t.Fatalf("provenance.connectorMethods = %+v, want %+v", got, want)
	}

	live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		t.Fatalf("convert live cell: %v", err)
	}
	if got := live.Spec.Provenance.ConnectorMethods; !reflect.DeepEqual(got, want) {
		t.Fatalf("internal provenance.connectorMethods = %+v, want %+v", got, want)
	}
	mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
	}
	mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
		return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
	}
	mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
		return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
	}
	mock.ReconcileCellFn = passthroughReconcile
	res, captured := runOutOfSyncHarness(t, live, mock)
	if captured != nil {
		t.Errorf("unexpected OutOfSync write for a cell with a connector method: reason=%q err=%q",
			captured.Status.OutOfSyncReason, captured.Status.OutOfSyncError)
	}
	if res.CellsErrored != 0 {
		t.Errorf("reconcile errors: %v", res.Errors)
	}
}

// TestPreviewCellFromConfig_ConnectorMethodWithIdentity: a method pick and an
// identity pick for the same connector combine — the identity fills the
// picked method's slot, and the Config's fill of its own method's slot is
// dropped as inactive.
func TestPreviewCellFromConfig_ConnectorMethodWithIdentity(t *testing.T) {
	fx := &connectorFixture{
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/tok": {Connector: "agent"}, "kuke-system/key": {Connector: "agent"},
		},
	}
	cfg := connectorConfig("agent-token")
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"agent-token": {SecretRef: contractSecretRef("tok")}}
	req := previewRequest()
	req.ConnectorMethods = map[string]string{"agent": "key"}
	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"agent": {Name: "key"}}
	preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := secretNames(preview.Cell.Spec.Containers[0]); !reflect.DeepEqual(got, []string{"AGENT_KEY"}) {
		t.Fatalf("secrets = %v, want only AGENT_KEY", got)
	}
	status := connectorStatus(t, preview.Connectors, "agent")
	if status.Method != "key" || status.Identity == nil || status.Identity.Name != "key" ||
		status.Origin != ext.RenderOriginCell {
		t.Fatalf("agent status = %+v, want the key method filled by the cell's pick", status)
	}
}

// TestPreviewCellFromConfig_ConnectorMethodUnresolved: a picked method whose
// slot nothing fills fails the preview naming the connector.
func TestPreviewCellFromConfig_ConnectorMethodUnresolved(t *testing.T) {
	req := previewRequest()
	req.ConnectorMethods = map[string]string{"agent": "token"}
	ctrl := setupTestController(t, newConnectorRunner(t, &connectorFixture{}, connectorConfig("agent-login")))
	_, err := ctrl.PreviewCellFromConfig(req)
	if !errors.Is(err, errdefs.ErrConnectorUnresolved) {
		t.Fatalf("error = %v, want ErrConnectorUnresolved", err)
	}
	if !strings.Contains(err.Error(), `"agent"`) {
		t.Errorf("error %q does not name the agent connector", err)
	}
}

// TestPreviewCellFromConfig_ConnectorMethodRejected: a method pick must name
// a connector the blueprint declares methods for, one of its methods, and a
// connector the Config requires — it never adds one the Config leaves out.
func TestPreviewCellFromConfig_ConnectorMethodRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		methods map[string]string
		mention string
	}{
		"unknown connector": {map[string]string{"cloud": "token"}, "agent, forge"},
		"unknown method":    {map[string]string{"agent": "oauth"}, "login, token, key"},
		"no methods":        {map[string]string{"git": "ssh"}, `no connector "git"`},
		"empty method":      {map[string]string{"agent": " "}, "names no method"},
		"not required by the Config": {
			map[string]string{"forge": "token"}, `connector "forge" is not required by config`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := previewRequest()
			req.ConnectorMethods = tc.methods
			ctrl := setupTestController(t, newConnectorRunner(t, &connectorFixture{}, connectorConfig("agent-login")))
			_, err := ctrl.PreviewCellFromConfig(req)
			if !errors.Is(err, errdefs.ErrConnectorMethod) {
				t.Fatalf("error = %v, want ErrConnectorMethod", err)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}
