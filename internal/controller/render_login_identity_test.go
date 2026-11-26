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
// Tests for login identities in the daemon-side CellConfig render (issue
// #1685): the Volume a stateVolume connector keeps its login in, the per-cell
// `--connector <connector>=<volume>` override, the one-role guard, and the
// persistent or ephemeral harness state of a token method.

package controller_test

import (
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// loginBlueprint is connectorBlueprint with what a harness blueprint carries
// for login identities: the agent connector authenticates a harness, its login
// method declares a marker, and the container mounts the state volume.
func loginBlueprint() ext.CellBlueprintDoc {
	bp := connectorBlueprint()
	for i := range bp.Spec.Components {
		desc := bp.Spec.Components[i].Connector
		if desc == nil || desc.Name != "agent" {
			continue
		}
		desc.Harness = "agent"
		if desc.Delivery == ext.ConnectorDeliveryStateVolume {
			desc.Login = &ext.ConnectorLogin{Marker: "auth.json", Command: []string{"agent", "login"}}
		}
	}
	bp.Spec.Cell.Containers[0].Volumes = []ext.VolumeMount{{
		Kind: ext.VolumeKindVolume, Target: "/home/user/.agent", Ensure: true,
		VolumeRef: &ext.VolumeRef{
			Name: "${STATE_VOLUME}", Realm: "${STATE_VOLUME_REALM}",
			Space: "${STATE_VOLUME_SPACE}", Stack: "${STATE_VOLUME_STACK}",
		},
	}}
	return bp
}

// loginConfig is a Config of loginBlueprint naming its state volume and scope.
func loginConfig(components ...string) ext.CellConfigDoc {
	cfg := connectorConfig(components...)
	cfg.Spec.Values["STATE_VOLUME_REALM"] = "kuke-system"
	cfg.Spec.Values["STATE_VOLUME_SPACE"] = "teams"
	cfg.Spec.Values["STATE_VOLUME_STACK"] = "kukeon"
	cfg.Spec.Repos = map[string]ext.CellConfigRepoFill{"project": {URL: "https://example.com/p.git"}}
	return cfg
}

func newLoginController(t *testing.T, fx *connectorFixture, cfg ext.CellConfigDoc) *controller.Exec {
	t.Helper()
	mock := newConnectorRunner(t, fx, cfg)
	mock.GetBlueprintFn = func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		return blueprintCarrier(t, loginBlueprint()), nil
	}
	return setupTestController(t, mock)
}

const stateVolumeKey = "kuke-system/teams/kukeon/kukeon-dev-state"

// stateMount returns the volume reference the cell's container mounts as its
// harness state, or nil when it mounts none.
func stateMount(cell ext.CellDoc) *ext.VolumeRef {
	for _, c := range cell.Spec.Containers {
		for _, m := range c.Volumes {
			if m.Kind == ext.VolumeKindVolume && m.VolumeRef != nil {
				return m.VolumeRef
			}
		}
	}
	return nil
}

// TestPreview_LoginStatus: the status of a login method names the state Volume
// and says whether it holds a login — empty before the Volume exists, and
// whatever the Volume reports once it does.
func TestPreview_LoginStatus(t *testing.T) {
	fx := &connectorFixture{}
	ctrl := newLoginController(t, fx, loginConfig("agent-login"))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	status := connectorStatus(t, preview.Connectors, "agent")
	if status.State != ext.ConnectorResolved || status.Login == nil || status.Login.State != ext.LoginStateEmpty {
		t.Fatalf("status before the volume exists = %+v (login %+v), want resolved and empty", status, status.Login)
	}
	if status.HarnessState != ext.HarnessStatePersistent {
		t.Errorf("harnessState = %q, want persistent", status.HarnessState)
	}

	fx.volumes = map[string]intmodel.Volume{stateVolumeKey: {
		Spec: intmodel.VolumeSpec{Connector: "agent", Method: "login", LoginMarker: "auth.json"},
		Status: intmodel.VolumeStatus{
			Login: &intmodel.LoginStatus{State: ext.LoginStateLoggedIn, Since: "2026-11-25T10:00:00Z"},
		},
	}}
	preview, err = ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig with a logged-in volume: %v", err)
	}
	status = connectorStatus(t, preview.Connectors, "agent")
	if status.Login == nil || status.Login.State != ext.LoginStateLoggedIn || status.Login.Since == "" {
		t.Errorf("login = %+v, want logged in with a time", status.Login)
	}
}

// TestPreview_UnsafeStateVolumeNotProbed: a state volume named by Config
// values that would leave the volumes tree is reported, and never read.
func TestPreview_UnsafeStateVolumeNotProbed(t *testing.T) {
	cfg := loginConfig("agent-login")
	cfg.Spec.Values["STATE_VOLUME"] = "../../../../root"
	mock := newConnectorRunner(t, &connectorFixture{}, cfg)
	mock.GetBlueprintFn = func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		return blueprintCarrier(t, loginBlueprint()), nil
	}
	mock.GetVolumeFn = func(v intmodel.Volume) (intmodel.Volume, error) {
		t.Errorf("GetVolume called with %+v", v.Metadata)
		return intmodel.Volume{}, errdefs.ErrVolumeNotFound
	}
	_, err := setupTestController(t, mock).PreviewCellFromConfig(previewRequest())
	if !errors.Is(err, errdefs.ErrVolumeIdentity) || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("PreviewCellFromConfig: %v, want the volume refused as unsafe", err)
	}
}

// TestPreview_VolumeOfAnotherConnectorRejected: a Volume tagged for one
// connector is refused as the state volume of another, before a cell exists.
func TestPreview_VolumeOfAnotherConnectorRejected(t *testing.T) {
	fx := &connectorFixture{volumes: map[string]intmodel.Volume{
		stateVolumeKey: {Spec: intmodel.VolumeSpec{Connector: "other", Method: "login"}},
	}}
	ctrl := newLoginController(t, fx, loginConfig("agent-login"))

	_, err := ctrl.PreviewCellFromConfig(previewRequest())
	if !errors.Is(err, errdefs.ErrVolumeIdentity) {
		t.Fatalf("error = %v, want ErrVolumeIdentity", err)
	}
	for _, want := range []string{`"other"`, `"agent"`, "kukeon-dev-state"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

// TestPreview_LoginIdentityOverride: `--connector <connector>=<volume>` on a
// login method mounts that Volume as the cell's state volume and records it in
// provenance as a Volume; one that does not exist is rejected.
func TestPreview_LoginIdentityOverride(t *testing.T) {
	fx := &connectorFixture{volumes: map[string]intmodel.Volume{
		"kuke-system///personal-max": {Spec: intmodel.VolumeSpec{Connector: "agent", Method: "login"}},
	}}
	ctrl := newLoginController(t, fx, loginConfig("agent-login"))

	req := previewRequest()
	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"agent": {Name: "personal-max"}}
	preview, err := ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	want := ext.VolumeRef{Name: "personal-max", Realm: "kuke-system"}
	if got := stateMount(preview.Cell); got == nil || *got != want {
		t.Fatalf("state mount = %+v, want %+v", got, want)
	}
	prov := preview.Cell.Spec.Provenance
	if prov == nil || prov.ConnectorVolumes["agent"] != want {
		t.Fatalf("provenance = %+v, want connectorVolumes[agent] = %+v", prov, want)
	}
	if len(prov.ConnectorOverrides) != 0 {
		t.Errorf("connectorOverrides = %+v, want none: the override names a Volume", prov.ConnectorOverrides)
	}
	status := connectorStatus(t, preview.Connectors, "agent")
	if status.VolumeRef == nil || *status.VolumeRef != want || status.Origin != ext.RenderOriginCell {
		t.Errorf("status = %+v (volumeRef %+v), want the override at the cell level", status, status.VolumeRef)
	}

	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"agent": {Name: "nope"}}
	_, err = ctrl.PreviewCellFromConfig(req)
	if !errors.Is(err, errdefs.ErrVolumeIdentity) || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing volume: error = %v, want ErrVolumeIdentity saying it does not exist", err)
	}
}

// TestPreview_OneRolePerLoginIdentity: a login identity Configs of another
// role already use is refused, and the error names both roles — when a Config
// binds it (checked where the Config is stored, so the role already using it
// keeps working) and when one cell picks it. Configs of the same role, and
// Configs without a role, may share.
func TestPreview_OneRolePerLoginIdentity(t *testing.T) {
	other := func(name, role string) ext.CellConfigDoc {
		cfg := loginConfig("agent-login")
		cfg.Metadata.Name = name
		if role != "" {
			cfg.Metadata.Labels = map[string]string{ext.LabelTeam: "kukeon", ext.LabelRole: role}
		}
		return cfg
	}
	own := loginConfig("agent-login")
	own.Metadata.Labels = map[string]string{ext.LabelTeam: "kukeon", ext.LabelRole: "dev"}
	refused := func(t *testing.T, err error) {
		t.Helper()
		if !errors.Is(err, errdefs.ErrVolumeIdentity) {
			t.Fatalf("error = %v, want ErrVolumeIdentity", err)
		}
		for _, want := range []string{"kukeon/pm", "kukeon/dev"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not name role %s", err, want)
			}
		}
	}

	// Binding: a Config being saved names the volume another role uses.
	for name, tc := range map[string]struct {
		configs []ext.CellConfigDoc
		refused bool
	}{
		"another role":    {[]ext.CellConfigDoc{other("kukeon-pm", "pm")}, true},
		"the same role":   {[]ext.CellConfigDoc{other("kukeon-dev-task", "dev")}, false},
		"no role":         {[]ext.CellConfigDoc{other("scratch", "")}, false},
		"no other config": {nil, false},
	} {
		t.Run("a config binding it: "+name, func(t *testing.T) {
			ctrl := newLoginController(t, &connectorFixture{configs: tc.configs}, own)
			req := previewRequest()
			draft := own
			req.Draft = &draft
			_, err := ctrl.PreviewCellFromConfig(req)
			if tc.refused {
				refused(t, err)
			} else if err != nil {
				t.Fatalf("PreviewCellFromConfig: %v", err)
			}
		})
	}

	// The role that already uses the identity is not locked out by another
	// role's stored Config: its own cells still render.
	t.Run("the first role keeps rendering", func(t *testing.T) {
		ctrl := newLoginController(t, &connectorFixture{configs: []ext.CellConfigDoc{other("kukeon-pm", "pm")}}, own)
		if _, err := ctrl.PreviewCellFromConfig(previewRequest()); err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
	})

	// Picking: one cell of role dev picks the identity role pm uses.
	t.Run("a cell picking it", func(t *testing.T) {
		pm := other("kukeon-pm", "pm")
		pm.Spec.Values["STATE_VOLUME"] = "pm-login"
		pm.Spec.Values["STATE_VOLUME_SPACE"], pm.Spec.Values["STATE_VOLUME_STACK"] = "", ""
		fx := &connectorFixture{
			configs: []ext.CellConfigDoc{pm},
			volumes: map[string]intmodel.Volume{
				"kuke-system///pm-login": {Spec: intmodel.VolumeSpec{Connector: "agent", Method: "login"}},
			},
		}
		req := previewRequest()
		req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"agent": {Name: "pm-login"}}
		_, err := newLoginController(t, fx, own).PreviewCellFromConfig(req)
		refused(t, err)
	})

	// A cell's pick counts too: a cell of role pm picked a Volume no Config
	// names.
	shared := intmodel.VolumeRef{Name: "shared-login", Realm: "kuke-system"}
	pmCell := func(scope, name string) intmodel.Cell {
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: name},
			Spec: intmodel.CellSpec{
				RealmName: "kuke-system", SpaceName: scope, StackName: scope,
				Provenance: &intmodel.CellProvenance{
					BindingKind:      ext.BindingKindConfig,
					BindingRef:       intmodel.CellBindingRef{Name: "kukeon-pm", Realm: "kuke-system"},
					ConnectorVolumes: map[string]intmodel.VolumeRef{"agent": shared},
				},
			},
		}
	}
	pickFixture := func(cells ...intmodel.Cell) *connectorFixture {
		pm := other("kukeon-pm", "pm")
		pm.Spec.Values["STATE_VOLUME"] = "pm-state"
		return &connectorFixture{
			configs: []ext.CellConfigDoc{pm},
			cells:   cells,
			volumes: map[string]intmodel.Volume{
				"kuke-system///shared-login": {Spec: intmodel.VolumeSpec{Connector: "agent", Method: "login"}},
			},
		}
	}
	pickShared := func() controller.CellFromConfigRequest {
		req := previewRequest()
		req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"agent": {Name: "shared-login"}}
		return req
	}
	t.Run("a cell picking what a cell of another role picked", func(t *testing.T) {
		_, err := newLoginController(t, pickFixture(pmCell("teams", "pm-1")), own).PreviewCellFromConfig(pickShared())
		refused(t, err)
	})
	t.Run("a config binding what a cell of another role picked", func(t *testing.T) {
		shared.Space, shared.Stack = "teams", "kukeon"
		defer func() { shared.Space, shared.Stack = "", "" }()
		draft := own
		draft.Spec.Values = maps.Clone(own.Spec.Values)
		draft.Spec.Values["STATE_VOLUME"] = "shared-login"
		req := previewRequest()
		req.Draft = &draft
		_, err := newLoginController(t, pickFixture(pmCell("teams", "pm-1")), own).PreviewCellFromConfig(req)
		refused(t, err)
	})
	// The cell being rendered does not conflict with its own pick.
	t.Run("the cell's own pick", func(t *testing.T) {
		self := pmCell("default", previewRequest().Name)
		if _, err := newLoginController(t, pickFixture(self), own).PreviewCellFromConfig(pickShared()); err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
	})
}

// TestPreview_HarnessState: a token method keeps persistent harness state by
// default and reports the state volume; `ephemeral`, on the Config or for one
// cell, mounts no state volume; a login method refuses it.
func TestPreview_HarnessState(t *testing.T) {
	fx := &connectorFixture{secrets: map[string]intmodel.SecretSpec{"kuke-system/agent-tok": {Connector: "agent"}}}
	tokenConfig := func(state string) ext.CellConfigDoc {
		cfg := loginConfig("agent-token")
		cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{
			"agent-token": {SecretRef: &ext.ContainerSecretRef{Name: "agent-tok", Realm: "kuke-system"}},
		}
		cfg.Spec.State = state
		return cfg
	}
	volume := ext.VolumeRef{Name: "kukeon-dev-state", Realm: "kuke-system", Space: "teams", Stack: "kukeon"}

	t.Run("persistent by default", func(t *testing.T) {
		preview, err := newLoginController(t, fx, tokenConfig("")).PreviewCellFromConfig(previewRequest())
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if got := stateMount(preview.Cell); got == nil || *got != volume {
			t.Fatalf("state mount = %+v, want %+v", got, volume)
		}
		status := connectorStatus(t, preview.Connectors, "agent")
		if status.HarnessState != ext.HarnessStatePersistent || status.VolumeRef == nil ||
			*status.VolumeRef != volume || status.Identity == nil || status.Identity.Name != "agent-tok" {
			t.Errorf("status = %+v, want the Secret identity and the persistent state volume", status)
		}
		if preview.Cell.Spec.Provenance.State != "" {
			t.Errorf("provenance state = %q, want none", preview.Cell.Spec.Provenance.State)
		}
	})

	t.Run("ephemeral on the config", func(t *testing.T) {
		preview, err := newLoginController(t, fx, tokenConfig(ext.HarnessStateEphemeral)).
			PreviewCellFromConfig(previewRequest())
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if got := stateMount(preview.Cell); got != nil {
			t.Fatalf("state mount = %+v, want none", got)
		}
		status := connectorStatus(t, preview.Connectors, "agent")
		if status.HarnessState != ext.HarnessStateEphemeral || status.Volume != "" || status.VolumeRef != nil {
			t.Errorf("status = %+v, want ephemeral state and no volume", status)
		}
	})

	t.Run("ephemeral for one cell", func(t *testing.T) {
		req := previewRequest()
		req.State = ext.HarnessStateEphemeral
		preview, err := newLoginController(t, fx, tokenConfig("")).PreviewCellFromConfig(req)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if got := stateMount(preview.Cell); got != nil {
			t.Fatalf("state mount = %+v, want none", got)
		}
		if got := preview.Cell.Spec.Provenance.State; got != ext.HarnessStateEphemeral {
			t.Errorf("provenance state = %q, want ephemeral", got)
		}
	})

	t.Run("persistent for one cell of an ephemeral config", func(t *testing.T) {
		req := previewRequest()
		req.State = ext.HarnessStatePersistent
		preview, err := newLoginController(t, fx, tokenConfig(ext.HarnessStateEphemeral)).PreviewCellFromConfig(req)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if got := stateMount(preview.Cell); got == nil || *got != volume {
			t.Fatalf("state mount = %+v, want %+v", got, volume)
		}
	})

	t.Run("ephemeral with a login method", func(t *testing.T) {
		req := previewRequest()
		req.State = ext.HarnessStateEphemeral
		_, err := newLoginController(t, fx, loginConfig("agent-login")).PreviewCellFromConfig(req)
		if !errors.Is(err, errdefs.ErrConfig) {
			t.Fatalf("error = %v, want ErrConfig", err)
		}
		for _, want := range []string{"ephemeral", `"login"`, "would not survive"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not mention %s", err, want)
			}
		}
	})

	t.Run("unknown state", func(t *testing.T) {
		req := previewRequest()
		req.State = "sometimes"
		_, err := newLoginController(t, fx, tokenConfig("")).PreviewCellFromConfig(req)
		if !errors.Is(err, errdefs.ErrConfig) || !strings.Contains(err.Error(), "sometimes") {
			t.Errorf("error = %v, want ErrConfig naming the value", err)
		}
	})
}
