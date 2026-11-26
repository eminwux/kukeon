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

// Tests for connector resolution in the daemon-side CellConfig render (issue
// #1548): which identity fills each connector a Config requires, the per-cell
// override, the status a Config reports, and the git identity expansion.

package controller_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// connectorBlueprint is a harness blueprint as `kuke team` renders one: the
// harness itself, the ways it can authenticate as one exclusive group, an API
// token connector, and the git connector.
func connectorBlueprint() ext.CellBlueprintDoc {
	agent := func(method, delivery string) *ext.BlueprintConnector {
		return &ext.BlueprintConnector{
			Name: "agent", Title: "Agent", Method: method, Delivery: delivery,
			SecretTypes: []string{ext.SecretTypeOpaque},
		}
	}
	return ext.CellBlueprintDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellBlueprint,
		Metadata:   ext.CellBlueprintMetadata{Name: "dev", Realm: "kuke-system"},
		Spec: ext.CellBlueprintSpec{
			Parameters: []ext.CellBlueprintParameter{
				{Name: "IMAGE"},
				{Name: "STATE_VOLUME"},
				{Name: "STATE_VOLUME_REALM"},
				{Name: "STATE_VOLUME_SPACE"},
				{Name: "STATE_VOLUME_STACK"},
			},
			Components: []ext.BlueprintComponent{
				{Kind: ext.ComponentKindHarness, Name: "harness"},
				{
					Kind: ext.ComponentKindConnector, Name: "agent-login", Optional: true, Group: "agent-auth",
					Connector: agent("login", ext.ConnectorDeliveryStateVolume),
				},
				{
					Kind: ext.ComponentKindConnector, Name: "agent-token", Optional: true, Group: "agent-auth",
					Connector: agent("token", ext.ConnectorDeliveryEnv),
					Secrets:   []ext.BlueprintSecretSlot{{Name: "agent-token", EnvName: "AGENT_TOKEN", Required: true}},
				},
				{
					Kind: ext.ComponentKindConnector, Name: "agent-key", Optional: true, Group: "agent-auth",
					Connector: agent("key", ext.ConnectorDeliveryEnv),
					Secrets:   []ext.BlueprintSecretSlot{{Name: "agent-key", EnvName: "AGENT_KEY", Required: true}},
				},
				{
					Kind: ext.ComponentKindConnector, Name: "forge", Optional: true,
					Connector: &ext.BlueprintConnector{
						Name: "forge", Method: "token", Delivery: ext.ConnectorDeliveryEnv,
						SecretTypes: []string{ext.SecretTypeOpaque},
					},
					Secrets: []ext.BlueprintSecretSlot{{Name: "forge-token", EnvName: "FORGE_TOKEN", Required: true}},
				},
				{
					Kind: ext.ComponentKindConnector, Name: "git", Optional: true,
					Connector: &ext.BlueprintConnector{
						Name: "git", Delivery: ext.ConnectorDeliveryGit, Optional: true,
						SecretTypes: []string{ext.SecretTypeSSHKey, ext.SecretTypeBasicAuth, ext.SecretTypeOpaque},
						Fields: []ext.ConnectorField{
							{Name: "author", Type: ext.ConnectorFieldPerson},
							{Name: "committer", Type: ext.ConnectorFieldPerson},
							{
								Name: "hosts", Type: ext.ConnectorFieldList, Required: true,
								SecretTypes: []string{ext.SecretTypeSSHKey, ext.SecretTypeBasicAuth},
							},
							{
								Name: "sign", Type: ext.ConnectorFieldList, Enum: []string{"commits", "tags"},
								SecretTypes: []string{ext.SecretTypeSSHKey},
							},
							{
								Name: "allowedSignersRef", Type: ext.ConnectorFieldSecret,
								SecretTypes: []string{ext.SecretTypeSSHKey},
							},
						},
					},
				},
			},
			Cell: ext.BlueprintCellSpec{Containers: []ext.BlueprintContainer{{
				ID:         "main",
				Image:      "${IMAGE}",
				Attachable: true,
				Repos:      []ext.ContainerRepo{{Name: "project", Target: "/work", Required: true}},
			}}},
		},
	}
}

// connectorFixture is the mutable state a connector render test serves: the
// scope defaults and the Secrets that exist, by "realm/name".
type connectorFixture struct {
	scope   renderScope
	secrets map[string]intmodel.SecretSpec
	// volumes are the Volumes that exist, by "realm/space/stack/name"; the
	// other Configs of the realm are in configs.
	volumes map[string]intmodel.Volume
	configs []ext.CellConfigDoc
	// cells are the cells of the realm.
	cells []intmodel.Cell
}

func volumeKey(md intmodel.VolumeMetadata) string {
	return md.Realm + "/" + md.Space + "/" + md.Stack + "/" + md.Name
}

func connectorConfig(components ...string) ext.CellConfigDoc {
	cfg := renderConfig()
	cfg.Spec.Values["STATE_VOLUME"] = "kukeon-dev-state"
	cfg.Spec.Components = components
	return cfg
}

func newConnectorRunner(t *testing.T, fx *connectorFixture, cfg ext.CellConfigDoc) *fakeRunner {
	t.Helper()
	mock := newRenderRunner(t, &fx.scope, cfg)
	mock.GetBlueprintFn = func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		return blueprintCarrier(t, connectorBlueprint()), nil
	}
	mock.GetSecretFn = func(secret intmodel.Secret) (intmodel.Secret, error) {
		spec, ok := fx.secrets[secret.Metadata.Realm+"/"+secret.Metadata.Name]
		if !ok {
			return intmodel.Secret{}, errdefs.ErrSecretNotFound
		}
		secret.Spec = spec
		return secret, nil
	}
	mock.GetVolumeFn = func(volume intmodel.Volume) (intmodel.Volume, error) {
		got, ok := fx.volumes[volumeKey(volume.Metadata)]
		if !ok {
			return intmodel.Volume{}, errdefs.ErrVolumeNotFound
		}
		got.Metadata = volume.Metadata
		return got, nil
	}
	mock.ListConfigsFn = func(string, string, string) ([]intmodel.CellConfig, error) {
		out := make([]intmodel.CellConfig, 0, len(fx.configs))
		for _, doc := range fx.configs {
			out = append(out, intmodel.CellConfig{Metadata: configCarrier(t, doc).Metadata})
		}
		return out, nil
	}
	mock.ListCellsFn = func(string, string, string) ([]intmodel.Cell, error) { return fx.cells, nil }
	getConfig := mock.GetConfigFn
	mock.GetConfigFn = func(in intmodel.CellConfig) (intmodel.CellConfig, error) {
		for _, doc := range fx.configs {
			if doc.Metadata.Name == in.Metadata.Name {
				return configCarrier(t, doc), nil
			}
		}
		return getConfig(in)
	}
	return mock
}

func secretFillOf(name string) map[string]intmodel.SecretFill {
	return map[string]intmodel.SecretFill{
		"forge-token": {SecretRef: &intmodel.ContainerSecretRef{Name: name, Realm: "kuke-system"}},
	}
}

func connectorStatus(t *testing.T, statuses []ext.ConnectorStatus, name string) ext.ConnectorStatus {
	t.Helper()
	for _, status := range statuses {
		if status.Connector == name {
			return status
		}
	}
	t.Fatalf("no status for connector %q in %+v", name, statuses)
	return ext.ConnectorStatus{}
}

// forgeSecret is the Secret a container reads the forge connector's token from.
func forgeSecret(c ext.ContainerSpec) string {
	for _, s := range c.Secrets {
		if s.Name == "FORGE_TOKEN" && s.SecretRef != nil {
			return s.SecretRef.Name
		}
	}
	return ""
}

// TestPreviewCellFromConfig_UnboundConnectorNamesIt: a required connector that
// nothing fills fails the preview with an error naming the connector, not just
// the slot.
func TestPreviewCellFromConfig_UnboundConnectorNamesIt(t *testing.T) {
	fx := &connectorFixture{}
	ctrl := setupTestController(t, newConnectorRunner(t, fx, connectorConfig("forge")))

	_, err := ctrl.PreviewCellFromConfig(previewRequest())
	if !errors.Is(err, errdefs.ErrConnectorUnresolved) {
		t.Fatalf("PreviewCellFromConfig error = %v, want ErrConnectorUnresolved", err)
	}
	for _, want := range []string{`connector "forge"`, `"forge-token"`, "--connector forge=<secret>"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
	if _, err = ctrl.CreateCellFromConfig(previewRequest()); !errors.Is(err, errdefs.ErrConnectorUnresolved) {
		t.Errorf("CreateCellFromConfig error = %v, want ErrConnectorUnresolved", err)
	}
}

// TestPreviewCellFromConfig_RealmDefaultIsLive: a Config that fills nothing
// inherits the Realm default, and changing that default changes the identity
// of the next cell with no change to the Config.
func TestPreviewCellFromConfig_RealmDefaultIsLive(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{Secrets: secretFillOf("forge-a")}},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/forge-a": {Connector: "forge"},
			"kuke-system/forge-b": {}, // untagged: created before connectors, still usable
		},
	}
	ctrl := setupTestController(t, newConnectorRunner(t, fx, connectorConfig("forge")))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := forgeSecret(preview.Cell.Spec.Containers[0]); got != "forge-a" {
		t.Errorf("FORGE_TOKEN secret = %q, want the realm default forge-a", got)
	}
	status := connectorStatus(t, preview.Connectors, "forge")
	if status.State != ext.ConnectorResolved || status.Origin != ext.RenderOriginRealm ||
		status.Identity == nil || status.Identity.Name != "forge-a" || !status.Required {
		t.Errorf("status = %+v, want resolved from the realm default forge-a", status)
	}

	fx.scope.realmDefaults = &intmodel.ScopeDefaults{Secrets: secretFillOf("forge-b")}
	preview, err = ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig after the default changed: %v", err)
	}
	if got := forgeSecret(preview.Cell.Spec.Containers[0]); got != "forge-b" {
		t.Errorf("FORGE_TOKEN secret = %q, want the new realm default forge-b", got)
	}
}

// TestPreviewCellFromConfig_ConnectorPrecedence: cell override, then Config
// fill, then Stack, Space and Realm defaults.
func TestPreviewCellFromConfig_ConnectorPrecedence(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{
			realmDefaults: &intmodel.ScopeDefaults{Secrets: secretFillOf("from-realm")},
			spaceDefaults: &intmodel.SpaceDefaults{Secrets: secretFillOf("from-space")},
			stackDefaults: &intmodel.ScopeDefaults{Secrets: secretFillOf("from-stack")},
		},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/from-realm": {}, "kuke-system/from-space": {}, "kuke-system/from-stack": {},
			"kuke-system/from-config": {}, "kuke-system/from-cell": {Connector: "forge"},
		},
	}
	cfg := connectorConfig("forge")
	run := func(req controller.CellFromConfigRequest) ext.ConnectorStatus {
		t.Helper()
		preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(req)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		status := connectorStatus(t, preview.Connectors, "forge")
		if got := forgeSecret(preview.Cell.Spec.Containers[0]); got != status.Identity.Name {
			t.Errorf("FORGE_TOKEN secret %q differs from the reported identity %q", got, status.Identity.Name)
		}
		return status
	}

	if got := run(previewRequest()); got.Identity.Name != "from-stack" || got.Origin != ext.RenderOriginStack {
		t.Errorf("no fill: %+v, want from-stack at the stack level", got)
	}
	fx.scope.stackDefaults = nil
	if got := run(previewRequest()); got.Identity.Name != "from-space" || got.Origin != ext.RenderOriginSpace {
		t.Errorf("no stack default: %+v, want from-space at the space level", got)
	}
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"forge-token": {SecretRef: contractSecretRef("from-config")}}
	if got := run(previewRequest()); got.Identity.Name != "from-config" || got.Origin != ext.RenderOriginConfig {
		t.Errorf("config fill: %+v, want from-config at the config level", got)
	}
	req := previewRequest()
	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"forge": {Name: "from-cell"}}
	if got := run(req); got.Identity.Name != "from-cell" || got.Origin != ext.RenderOriginCell ||
		got.Identity.Realm != "kuke-system" {
		t.Errorf("cell override: %+v, want from-cell in the cell's realm at the cell level", got)
	}
}

// TestPreviewCellFromConfig_ConnectorOverrideRecorded: the override is
// recorded in the cell's provenance, and the reconciler re-renders the live
// cell with it — the cell is not flagged OutOfSync and keeps its identity.
func TestPreviewCellFromConfig_ConnectorOverrideRecorded(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{Secrets: secretFillOf("forge-a")}},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/forge-a": {Connector: "forge"}, "kuke-system/forge-other": {Connector: "forge"},
		},
	}
	mock := newConnectorRunner(t, fx, connectorConfig("forge"))
	req := previewRequest()
	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"forge": {Name: "forge-other", Realm: "kuke-system"}}
	preview, err := setupTestController(t, mock).PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := forgeSecret(preview.Cell.Spec.Containers[0]); got != "forge-other" {
		t.Fatalf("FORGE_TOKEN secret = %q, want the override forge-other", got)
	}
	want := map[string]ext.ContainerSecretRef{"forge": {Name: "forge-other", Realm: "kuke-system"}}
	if got := preview.Cell.Spec.Provenance.ConnectorOverrides; !reflect.DeepEqual(got, want) {
		t.Fatalf("provenance.connectorOverrides = %+v, want %+v", got, want)
	}

	live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		t.Fatalf("convert live cell: %v", err)
	}
	if got := live.Spec.Provenance.ConnectorOverrides["forge"].Name; got != "forge-other" {
		t.Fatalf("internal provenance override = %q, want forge-other", got)
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
		t.Errorf("unexpected OutOfSync write for a cell with a connector override: reason=%q err=%q",
			captured.Status.OutOfSyncReason, captured.Status.OutOfSyncError)
	}
	if res.CellsErrored != 0 {
		t.Errorf("reconcile errors: %v", res.Errors)
	}
}

// TestPreviewCellFromConfig_ConnectorOverrideRejected: an override must name a
// connector the Config requires, one that takes an identity, and a Secret that
// is not another connector's.
func TestPreviewCellFromConfig_ConnectorOverrideRejected(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{Secrets: secretFillOf("forge-a")}},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/forge-a":   {Connector: "forge"},
			"kuke-system/agent-tok": {Connector: "agent"},
			"kuke-system/ssh":       {Type: ext.SecretTypeSSHKey},
		},
	}
	for name, tc := range map[string]struct {
		overrides map[string]ext.ContainerSecretRef
		want      error
		mention   string
	}{
		"unknown connector": {
			map[string]ext.ContainerSecretRef{"cloud": {Name: "forge-a"}}, errdefs.ErrConnectorUnknown, "agent, forge",
		},
		"wrong tag": {
			map[string]ext.ContainerSecretRef{"forge": {Name: "agent-tok"}}, errdefs.ErrConnectorIdentity, `"agent"`,
		},
		"wrong type": {
			map[string]ext.ContainerSecretRef{"forge": {Name: "ssh"}}, errdefs.ErrConnectorIdentity, "ssh-key",
		},
		"missing secret": {
			map[string]ext.ContainerSecretRef{"forge": {Name: "nope"}}, errdefs.ErrConnectorIdentity, "does not exist",
		},
		"login names a volume that does not exist": {
			map[string]ext.ContainerSecretRef{"agent": {Name: "agent-tok"}}, errdefs.ErrVolumeIdentity, "does not exist",
		},
	} {
		t.Run(name, func(t *testing.T) {
			req := previewRequest()
			req.ConnectorOverrides = tc.overrides
			ctrl := setupTestController(t, newConnectorRunner(t, fx, connectorConfig("forge", "agent-login")))
			_, err := ctrl.PreviewCellFromConfig(req)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}

// TestPreviewCellFromConfig_AuthGroupIsExclusive: two methods of one
// connector cannot both be enabled; one, or none, can.
func TestPreviewCellFromConfig_AuthGroupIsExclusive(t *testing.T) {
	fx := &connectorFixture{
		secrets: map[string]intmodel.SecretSpec{"kuke-system/tok": {}, "kuke-system/key": {}},
	}
	cfg := connectorConfig("agent-token", "agent-key")
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{
		"agent-token": {SecretRef: contractSecretRef("tok")},
		"agent-key":   {SecretRef: contractSecretRef("key")},
	}
	_, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if !errors.Is(err, errdefs.ErrBlueprintComponentGroup) {
		t.Fatalf("two methods of one group: error = %v, want ErrBlueprintComponentGroup", err)
	}
	for _, want := range []string{"agent-token", "agent-key", "agent-auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}

	cfg.Spec.Components = []string{"agent-token"}
	preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("one method: %v", err)
	}
	if got := secretNames(preview.Cell.Spec.Containers[0]); !reflect.DeepEqual(got, []string{"AGENT_TOKEN"}) {
		t.Errorf("secrets = %v, want only AGENT_TOKEN (the key fill belongs to a method that is off)", got)
	}
}

// TestGetConfig_ReportsConnectors: a Config get reports every connector the
// Config requires — the login's state volume, a resolved identity with the
// level it came from, and an unresolved one with the reason.
func TestGetConfig_ReportsConnectors(t *testing.T) {
	fx := &connectorFixture{secrets: map[string]intmodel.SecretSpec{"kuke-system/forge-a": {Connector: "forge"}}}
	cfg := connectorConfig("agent-login", "forge", "git")
	mock := newConnectorRunner(t, fx, cfg)
	ctrl := setupTestController(t, mock)

	get := func() []ext.ConnectorStatus {
		t.Helper()
		res, err := ctrl.GetConfig(configCarrier(t, cfg))
		if err != nil {
			t.Fatalf("GetConfig: %v", err)
		}
		doc, err := apischeme.ConvertCellConfigToExternal(res.Config)
		if err != nil {
			t.Fatalf("convert config: %v", err)
		}
		if doc.Status == nil {
			t.Fatal("config carries no status")
		}
		return doc.Status.Connectors
	}

	statuses := get()
	if len(statuses) != 3 {
		t.Fatalf("statuses = %+v, want one per required connector", statuses)
	}
	login := connectorStatus(t, statuses, "agent")
	if login.State != ext.ConnectorResolved || login.Volume != "kukeon-dev-state" || login.Required ||
		login.Method != "login" || login.Component != "agent-login" {
		t.Errorf("login status = %+v, want resolved with the state volume", login)
	}
	// A Config that names no state volume scope gets no reference to link.
	if login.VolumeRef != nil {
		t.Errorf("login volumeRef = %+v, want none without STATE_VOLUME_REALM", login.VolumeRef)
	}
	forge := connectorStatus(t, statuses, "forge")
	if forge.State != ext.ConnectorUnresolved || !forge.Required || forge.Message == "" || forge.Slot != "forge-token" {
		t.Errorf("forge status = %+v, want unresolved and required", forge)
	}
	git := connectorStatus(t, statuses, "git")
	if git.State != ext.ConnectorUnresolved || git.Required {
		t.Errorf("git status = %+v, want unresolved and not required", git)
	}

	// Setting the Realm default resolves it, with no change to the Config.
	fx.scope.realmDefaults = &intmodel.ScopeDefaults{Secrets: secretFillOf("forge-a")}
	forge = connectorStatus(t, get(), "forge")
	if forge.State != ext.ConnectorResolved || forge.Origin != ext.RenderOriginRealm ||
		forge.Identity.Name != "forge-a" {
		t.Errorf("forge status after the default = %+v, want resolved from the realm", forge)
	}

	// A list carries the same status.
	mock.ListConfigsFn = func(string, string, string) ([]intmodel.CellConfig, error) {
		carrier := configCarrier(t, cfg)
		carrier.Document = nil
		return []intmodel.CellConfig{carrier}, nil
	}
	listed, err := ctrl.ListConfigs("kuke-system", "", "")
	if err != nil {
		t.Fatalf("ListConfigs: %v", err)
	}
	if len(listed) != 1 || len(listed[0].Connectors) != 3 {
		t.Fatalf("listed = %+v, want one config with three connector statuses", listed)
	}
}

func gitIdentitySecret(identity map[string]string) intmodel.SecretSpec {
	return intmodel.SecretSpec{Type: ext.SecretTypeSSHKey, Connector: "git", Identity: identity}
}

// TestPreviewCellFromConfig_GitIdentityRef: a Config's git.identityRef expands
// into the identity's author and committer, a credential for each host and
// signing with the same key; fields stated next to the reference win.
func TestPreviewCellFromConfig_GitIdentityRef(t *testing.T) {
	fx := &connectorFixture{secrets: map[string]intmodel.SecretSpec{
		"kuke-system/git-ssh": gitIdentitySecret(map[string]string{
			"author":            "Ada Lovelace <ada@example.com>",
			"committer":         "Ada Bot <bot@example.com>",
			"hosts":             "github.com, gitlab.com/group",
			"sign":              "commits,tags",
			"allowedSignersRef": "signers",
		}),
	}}
	cfg := connectorConfig("git")
	cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef("git-ssh")}
	preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	git := preview.Cell.Spec.Containers[0].Git
	if git == nil {
		t.Fatal("container carries no git block")
	}
	if git.IdentityRef != nil {
		t.Errorf("rendered cell still carries git.identityRef %+v", git.IdentityRef)
	}
	if git.Author == nil || git.Author.Name != "Ada Lovelace" || git.Author.Email != "ada@example.com" {
		t.Errorf("author = %+v", git.Author)
	}
	if git.Committer == nil || git.Committer.Email != "bot@example.com" {
		t.Errorf("committer = %+v", git.Committer)
	}
	var hosts []string
	for _, cred := range git.Credentials {
		hosts = append(hosts, cred.Host)
		if cred.SecretRef == nil || cred.SecretRef.Name != "git-ssh" {
			t.Errorf("credential %q secret = %+v, want git-ssh", cred.Host, cred.SecretRef)
		}
	}
	if want := []string{"github.com", "gitlab.com/group"}; !reflect.DeepEqual(hosts, want) {
		t.Errorf("credential hosts = %v, want %v", hosts, want)
	}
	if git.Signing == nil || git.Signing.SecretRef.Name != "git-ssh" ||
		git.Signing.AllowedSignersRef == nil || git.Signing.AllowedSignersRef.Name != "signers" ||
		git.Signing.AllowedSignersRef.Realm != "kuke-system" {
		t.Errorf("signing = %+v, want the identity's key and its allowed-signers secret", git.Signing)
	}
	if want := []string{"commits", "tags"}; !reflect.DeepEqual(git.Sign, want) {
		t.Errorf("sign = %v, want %v", git.Sign, want)
	}
	status := connectorStatus(t, preview.Connectors, "git")
	if status.State != ext.ConnectorResolved || status.Origin != ext.RenderOriginConfig ||
		status.Identity == nil || status.Identity.Name != "git-ssh" {
		t.Errorf("git status = %+v, want resolved by git-ssh at the config level", status)
	}

	// Explicit fields next to the reference override the expanded ones.
	cfg.Spec.Git.Author = &ext.GitIdentity{Name: "Grace Hopper", Email: "grace@example.com"}
	cfg.Spec.Git.Sign = []string{"tags"}
	cfg.Spec.Git.Credentials = []ext.GitCredential{{Host: "github.com", SecretRef: contractSecretRef("other-key")}}
	fx.secrets["kuke-system/other-key"] = intmodel.SecretSpec{Type: ext.SecretTypeSSHKey}
	preview, err = setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig with explicit fields: %v", err)
	}
	git = preview.Cell.Spec.Containers[0].Git
	if git.Author.Name != "Grace Hopper" || git.Committer.Email != "bot@example.com" {
		t.Errorf(
			"author = %+v committer = %+v, want the explicit author over the identity's",
			git.Author,
			git.Committer,
		)
	}
	if !reflect.DeepEqual(git.Sign, []string{"tags"}) {
		t.Errorf("sign = %v, want the explicit [tags]", git.Sign)
	}
	for _, cred := range git.Credentials {
		want := "git-ssh"
		if cred.Host == "github.com" {
			want = "other-key"
		}
		if cred.SecretRef.Name != want {
			t.Errorf("credential %q secret = %q, want %q", cred.Host, cred.SecretRef.Name, want)
		}
	}
}

// TestPreviewCellFromConfig_GitIdentityLevels: a Realm default identity
// reaches a Config that names none; a cell override replaces it; a repo fill
// names its own.
func TestPreviewCellFromConfig_GitIdentityLevels(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{
			Git: &intmodel.ContainerGit{
				IdentityRef: &intmodel.ContainerSecretRef{Name: "team-git", Realm: "kuke-system"},
			},
		}},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/team-git": gitIdentitySecret(map[string]string{
				"author": "Team <team@example.com>", "hosts": "github.com", "sign": "commits",
			}),
			"kuke-system/my-git": gitIdentitySecret(map[string]string{
				"author": "Me <me@example.com>", "hosts": "github.com",
			}),
			"kuke-system/https-git": {
				Type: ext.SecretTypeBasicAuth, Connector: "git",
				Identity: map[string]string{"author": "Bot <bot@example.com>", "hosts": "github.com"},
			},
		},
	}
	cfg := connectorConfig("git")
	ctrl := setupTestController(t, newConnectorRunner(t, fx, cfg))

	preview, err := ctrl.PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	git := preview.Cell.Spec.Containers[0].Git
	if git == nil || git.Author.Email != "team@example.com" || git.Signing.SecretRef.Name != "team-git" {
		t.Fatalf("git = %+v, want the realm default identity", git)
	}
	if status := connectorStatus(t, preview.Connectors, "git"); status.Origin != ext.RenderOriginRealm ||
		status.Identity.Name != "team-git" {
		t.Errorf("git status = %+v, want team-git from the realm", status)
	}

	// The cell override names another identity: its author, its key for
	// github.com and for signing; the realm's sign list still applies.
	req := previewRequest()
	req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"git": {Name: "my-git"}}
	preview, err = ctrl.PreviewCellFromConfig(req)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig with a git override: %v", err)
	}
	git = preview.Cell.Spec.Containers[0].Git
	if git.Author.Email != "me@example.com" || git.Signing.SecretRef.Name != "my-git" ||
		len(git.Credentials) != 1 || git.Credentials[0].SecretRef.Name != "my-git" ||
		!reflect.DeepEqual(git.Sign, []string{"commits"}) {
		t.Errorf("git = %+v (credentials %+v), want the overriding identity", git, git.Credentials)
	}
	if status := connectorStatus(t, preview.Connectors, "git"); status.Origin != ext.RenderOriginCell ||
		status.Identity.Name != "my-git" {
		t.Errorf("git status = %+v, want my-git from the cell", status)
	}

	// A repo fill names its own identity: a basic-auth one carries no signing.
	cfg.Spec.Repos["project"] = ext.CellConfigRepoFill{
		URL: "https://github.com/eminwux/kukeon.git",
		Git: &ext.RepoGit{IdentityRef: contractSecretRef("https-git")},
	}
	preview, err = setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig with a repo identity: %v", err)
	}
	repoGit := preview.Cell.Spec.Containers[0].Repos[0].Git
	if repoGit == nil || repoGit.IdentityRef != nil || repoGit.Author.Email != "bot@example.com" ||
		repoGit.Credential == nil || repoGit.Credential.SecretRef.Name != "https-git" || repoGit.Signing != nil {
		t.Errorf("repo git = %+v, want the repo's own basic-auth identity and no signing", repoGit)
	}
}

// TestPreviewCellFromConfig_GitHubTokenGitIdentity: the git identity a GitHub
// token is stored as (issue #1693) — basic-auth, hosts github.com, and the
// reserved kukeon.io/token-from key the blueprint's git descriptor does not
// declare — resolves from a Realm default into one github.com credential and
// no signing.
func TestPreviewCellFromConfig_GitHubTokenGitIdentity(t *testing.T) {
	fx := &connectorFixture{
		scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{
			Git: &intmodel.ContainerGit{
				IdentityRef: &intmodel.ContainerSecretRef{Name: "github-git", Realm: "kuke-system"},
			},
		}},
		secrets: map[string]intmodel.SecretSpec{
			"kuke-system/github-git": {
				Type: ext.SecretTypeBasicAuth, Connector: "git",
				Identity: map[string]string{
					"hosts": "github.com", "kukeon.io/token-from": "github-token@kuke-system",
				},
			},
		},
	}
	cfg := connectorConfig("git")
	preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	git := preview.Cell.Spec.Containers[0].Git
	if git == nil || len(git.Credentials) != 1 || git.Credentials[0].Host != "github.com" ||
		git.Credentials[0].SecretRef == nil || git.Credentials[0].SecretRef.Name != "github-git" ||
		git.Signing != nil || len(git.Sign) != 0 {
		t.Fatalf("git = %+v, want one github.com credential and no signing", git)
	}
	if status := connectorStatus(t, preview.Connectors, "git"); status.State != ext.ConnectorResolved ||
		status.Origin != ext.RenderOriginRealm || status.Identity.Name != "github-git" {
		t.Errorf("git status = %+v, want github-git resolved from the realm", status)
	}
}

// TestPreviewCellFromConfig_AuthorOnlyGitIdentity: an opaque git identity
// (issue #1591) gives a cell its author and committer and nothing else — no
// credential, no signing — whether a Realm default names it, with or without a
// git connector on the blueprint, or one repo fill does.
func TestPreviewCellFromConfig_AuthorOnlyGitIdentity(t *testing.T) {
	authorOnly := intmodel.SecretSpec{
		Type: ext.SecretTypeOpaque, Connector: "git",
		Identity: map[string]string{"author": "Me <me@example.com>", "committer": "Me Bot <bot@example.com>"},
	}
	newFixture := func() *connectorFixture {
		return &connectorFixture{
			scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{
				Git: &intmodel.ContainerGit{
					IdentityRef: &intmodel.ContainerSecretRef{Name: "git-author", Realm: "kuke-system"},
				},
			}},
			secrets: map[string]intmodel.SecretSpec{"kuke-system/git-author": authorOnly},
		}
	}
	// "git" carries the connector descriptor; "forge" does not, so the
	// identity is checked against the fallback.
	for _, components := range [][]string{{"git"}, {"forge"}} {
		t.Run("realm default/"+components[0], func(t *testing.T) {
			fx := newFixture()
			fx.secrets["kuke-system/forge-a"] = intmodel.SecretSpec{Connector: "forge"}
			fx.scope.realmDefaults.Secrets = secretFillOf("forge-a")
			cfg := connectorConfig(components...)
			preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).
				PreviewCellFromConfig(previewRequest())
			if err != nil {
				t.Fatalf("PreviewCellFromConfig: %v", err)
			}
			git := preview.Cell.Spec.Containers[0].Git
			if git == nil || git.Author == nil || git.Author.Email != "me@example.com" ||
				git.Committer == nil || git.Committer.Email != "bot@example.com" {
				t.Fatalf("git = %+v, want the author-only identity's author and committer", git)
			}
			if len(git.Credentials) != 0 || git.Signing != nil || len(git.Sign) != 0 || git.IdentityRef != nil {
				t.Errorf("git = %+v (credentials %+v), want no credential, no signing and no reference",
					git, git.Credentials)
			}
		})
	}

	t.Run("repo", func(t *testing.T) {
		fx := newFixture()
		fx.scope.realmDefaults = nil
		cfg := connectorConfig("git")
		cfg.Spec.Repos["project"] = ext.CellConfigRepoFill{
			URL: "https://github.com/eminwux/kukeon.git",
			Git: &ext.RepoGit{IdentityRef: contractSecretRef("git-author")},
		}
		preview, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		repoGit := preview.Cell.Spec.Containers[0].Repos[0].Git
		if repoGit == nil || repoGit.IdentityRef != nil || repoGit.Author == nil ||
			repoGit.Author.Email != "me@example.com" || repoGit.Committer.Email != "bot@example.com" {
			t.Fatalf("repo git = %+v, want the author-only identity's author and committer", repoGit)
		}
		if repoGit.Credential != nil || repoGit.Signing != nil || len(repoGit.Sign) != 0 {
			t.Errorf("repo git = %+v, want no credential and no signing", repoGit)
		}
	})
}

// TestPreviewCellFromConfig_GitIdentityRefRejected: a reference that cannot be
// expanded fails the render — the cell would otherwise start with no identity.
func TestPreviewCellFromConfig_GitIdentityRefRejected(t *testing.T) {
	fx := &connectorFixture{secrets: map[string]intmodel.SecretSpec{
		"kuke-system/untagged": {Type: ext.SecretTypeSSHKey},
		"kuke-system/forge-a":  {Connector: "forge"},
		// An author-only (opaque) identity authenticates nothing: hosts on it
		// are refused (issue #1591).
		"kuke-system/opaque":   {Connector: "git", Identity: map[string]string{"hosts": "github.com"}},
		"kuke-system/no-hosts": gitIdentitySecret(map[string]string{"author": "A <a@example.com>"}),
	}}
	for name, mention := range map[string]string{
		"missing":  "does not exist",
		"untagged": "not a git identity",
		"forge-a":  `"forge"`,
		"opaque":   "ssh-key or basic-auth",
		"no-hosts": `"hosts"`,
	} {
		t.Run(name, func(t *testing.T) {
			cfg := connectorConfig("git")
			cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef(name)}
			_, err := setupTestController(t, newConnectorRunner(t, fx, cfg)).PreviewCellFromConfig(previewRequest())
			if !errors.Is(err, errdefs.ErrConnectorIdentity) {
				t.Fatalf("error = %v, want ErrConnectorIdentity", err)
			}
			if !strings.Contains(err.Error(), mention) {
				t.Errorf("error %q does not mention %q", err, mention)
			}
		})
	}
}

// TestPreviewCellFromConfig_ShadowedGitIdentity: a git identity that cannot be
// read fails the render only when it is the one that reaches the cell. One a
// deeper level overrides — the Config's own, a cell override, a repo fill the
// Config states — is shadowed, and a Config with no git connector is not failed
// by a broken Realm default it never asked for.
func TestPreviewCellFromConfig_ShadowedGitIdentity(t *testing.T) {
	broken := &intmodel.ContainerSecretRef{Name: "missing-git", Realm: "kuke-system"}
	newFixture := func() *connectorFixture {
		return &connectorFixture{
			scope: renderScope{realmDefaults: &intmodel.ScopeDefaults{
				Git: &intmodel.ContainerGit{IdentityRef: broken},
				Repos: map[string]intmodel.RepoFill{"project": {
					URL: "https://github.com/eminwux/kukeon.git",
					Git: &intmodel.RepoGit{IdentityRef: broken},
				}},
			}},
			secrets: map[string]intmodel.SecretSpec{
				"kuke-system/my-git": gitIdentitySecret(map[string]string{
					"author": "Me <me@example.com>", "hosts": "github.com",
				}),
			},
		}
	}
	// The Config states its own repo fill, so the Realm's broken repo identity
	// is shadowed in every case below.
	withRepo := func(cfg ext.CellConfigDoc) ext.CellConfigDoc {
		cfg.Spec.Repos = map[string]ext.CellConfigRepoFill{"project": {URL: "https://github.com/eminwux/kukeon.git"}}
		return cfg
	}

	t.Run("the realm default is the winning identity", func(t *testing.T) {
		cfg := withRepo(connectorConfig("git"))
		_, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).
			PreviewCellFromConfig(previewRequest())
		if !errors.Is(err, errdefs.ErrConnectorIdentity) || !strings.Contains(err.Error(), "missing-git") {
			t.Fatalf("error = %v, want ErrConnectorIdentity naming missing-git", err)
		}
	})

	t.Run("the config names its own identity", func(t *testing.T) {
		cfg := withRepo(connectorConfig("git"))
		cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef("my-git")}
		preview, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).
			PreviewCellFromConfig(previewRequest())
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if status := connectorStatus(t, preview.Connectors, "git"); status.State != ext.ConnectorResolved ||
			status.Origin != ext.RenderOriginConfig || status.Identity == nil || status.Identity.Name != "my-git" {
			t.Errorf("git status = %+v, want my-git resolved at the config level", status)
		}
	})

	t.Run("a cell override names another identity", func(t *testing.T) {
		cfg := withRepo(connectorConfig("git"))
		cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef("also-missing")}
		req := previewRequest()
		req.ConnectorOverrides = map[string]ext.ContainerSecretRef{"git": {Name: "my-git"}}
		preview, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).PreviewCellFromConfig(req)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if git := preview.Cell.Spec.Containers[0].Git; git == nil || git.Author.Email != "me@example.com" {
			t.Errorf("git = %+v, want the overriding identity", git)
		}
		if status := connectorStatus(t, preview.Connectors, "git"); status.Origin != ext.RenderOriginCell ||
			status.Identity == nil || status.Identity.Name != "my-git" {
			t.Errorf("git status = %+v, want my-git from the cell", status)
		}
	})

	t.Run("a config with no git connector", func(t *testing.T) {
		cfg := withRepo(connectorConfig())
		if _, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).
			PreviewCellFromConfig(previewRequest()); err != nil {
			t.Fatalf("PreviewCellFromConfig: %v, want the broken realm default ignored", err)
		}
		// An identity the Config names itself still fails it.
		cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef("also-missing")}
		_, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).
			PreviewCellFromConfig(previewRequest())
		if !errors.Is(err, errdefs.ErrConnectorIdentity) || !strings.Contains(err.Error(), "also-missing") {
			t.Fatalf("error = %v, want ErrConnectorIdentity naming also-missing", err)
		}
	})

	t.Run("the realm repo fill wins its repo", func(t *testing.T) {
		cfg := connectorConfig("git")
		cfg.Spec.Repos = nil
		cfg.Spec.Git = &ext.ContainerGit{IdentityRef: contractSecretRef("my-git")}
		_, err := setupTestController(t, newConnectorRunner(t, newFixture(), cfg)).
			PreviewCellFromConfig(previewRequest())
		if !errors.Is(err, errdefs.ErrConnectorIdentity) || !strings.Contains(err.Error(), "repo project") {
			t.Fatalf("error = %v, want ErrConnectorIdentity naming repo project", err)
		}
	})
}

// TestConfigConnectorStatusStateVolumeRef: a Config naming its state volume's
// scope reports the volume as a reference the web UI links.
func TestConfigConnectorStatusStateVolumeRef(t *testing.T) {
	fx := &connectorFixture{}
	cfg := connectorConfig("agent-login")
	cfg.Spec.Values["STATE_VOLUME_REALM"] = "default"
	cfg.Spec.Values["STATE_VOLUME_SPACE"] = "teams"
	cfg.Spec.Values["STATE_VOLUME_STACK"] = "kukeon"
	ctrl := setupTestController(t, newConnectorRunner(t, fx, cfg))
	res, err := ctrl.GetConfig(configCarrier(t, cfg))
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}
	doc, err := apischeme.ConvertCellConfigToExternal(res.Config)
	if err != nil {
		t.Fatalf("convert config: %v", err)
	}
	if doc.Status == nil {
		t.Fatal("config carries no status")
	}
	login := connectorStatus(t, doc.Status.Connectors, "agent")
	want := ext.VolumeRef{Name: "kukeon-dev-state", Realm: "default", Space: "teams", Stack: "kukeon"}
	if login.VolumeRef == nil || *login.VolumeRef != want {
		t.Fatalf("login volumeRef = %+v, want %+v", login.VolumeRef, want)
	}
}
