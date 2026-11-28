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
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

// loginClient stands in for kukeond behind the login-identity routes: the
// Volumes, Configs and cells it serves, and the cells it was asked to create.
type loginClient struct {
	connectorClient

	volumes map[string]v1beta1.VolumeDoc // "realm/space/stack/name"
	cells   map[string]v1beta1.CellDoc   // "realm/space/stack/name"
	started []v1beta1.CellDoc
}

func volumeDocKey(md v1beta1.VolumeMetadata) string {
	return md.Realm + "/" + md.Space + "/" + md.Stack + "/" + md.Name
}

func cellDocKey(doc v1beta1.CellDoc) string {
	return doc.Spec.RealmID + "/" + doc.Spec.SpaceID + "/" + doc.Spec.StackID + "/" + doc.Metadata.Name
}

func (c *loginClient) ListVolumes(context.Context, string, string, string) ([]v1beta1.VolumeDoc, error) {
	out := make([]v1beta1.VolumeDoc, 0, len(c.volumes))
	for _, volume := range c.volumes {
		out = append(out, volume)
	}
	return out, nil
}

func (c *loginClient) GetVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.GetVolumeResult, error) {
	volume, ok := c.volumes[volumeDocKey(doc.Metadata)]
	return kukeonv1.GetVolumeResult{Volume: volume, MetadataExists: ok}, nil
}

func (c *loginClient) CreateVolume(_ context.Context, doc v1beta1.VolumeDoc) (kukeonv1.CreateVolumeResult, error) {
	if c.volumes == nil {
		c.volumes = map[string]v1beta1.VolumeDoc{}
	}
	if doc.Spec.LoginMarker != "" {
		doc.Status.Login = &v1beta1.LoginStatus{State: v1beta1.LoginStateEmpty}
	}
	c.volumes[volumeDocKey(doc.Metadata)] = doc
	return kukeonv1.CreateVolumeResult{Volume: doc, Created: true}, nil
}

func (c *loginClient) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	return []v1beta1.RealmDoc{c.realm}, nil
}

func (c *loginClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	cell, ok := c.cells[cellDocKey(doc)]
	if !ok {
		return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
	}
	return kukeonv1.GetCellResult{Cell: cell, MetadataExists: true, RootContainerTaskRunning: true}, nil
}

func (c *loginClient) CreateCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
	if c.cells == nil {
		c.cells = map[string]v1beta1.CellDoc{}
	}
	c.cells[cellDocKey(doc)] = doc
	c.started = append(c.started, doc)
	return kukeonv1.CreateCellResult{Cell: doc}, nil
}

// loginConnectorName is a connector of the catalog with a login method, so no
// test names one.
func loginConnectorName(t *testing.T) string {
	t.Helper()
	for _, conn := range connectortest.Catalog(t).Connectors {
		if _, ok := conn.LoginMethod(""); ok {
			return conn.Metadata.Name
		}
	}
	t.Fatal("the catalog has no connector with a login method")
	return ""
}

func loginHandler(t *testing.T, c *loginClient) http.Handler {
	t.Helper()
	policy, err := parseAccessPolicy("http://localhost:5129")
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newHandlerWithCatalog(
		policy, "test", func(context.Context) (kukeonv1.Client, error) { return c, nil }, connectortest.Catalog(t))
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// TestLoginIdentitiesList: the list carries every login identity — a tagged
// Volume, and the untagged state volume a Config logs in through — with its
// login state and the Configs using it; a plain Volume is not one. The catalog
// marks the methods the browser can log in to.
func TestLoginIdentitiesList(t *testing.T) {
	name := loginConnectorName(t)
	team := v1beta1.VolumeRef{Name: "kukeon-dev-state", Realm: "dev", Space: "teams", Stack: "kukeon"}
	c := &loginClient{connectorClient: *devScopes()}
	c.volumes = map[string]v1beta1.VolumeDoc{
		"dev///personal": {
			Metadata: v1beta1.VolumeMetadata{Name: "personal", Realm: "dev"},
			Spec:     v1beta1.VolumeSpec{Connector: name, Method: "login", LoginMarker: "marker"},
			Status:   v1beta1.VolumeStatus{Login: &v1beta1.LoginStatus{State: v1beta1.LoginStateEmpty}},
		},
		"dev/teams/kukeon/kukeon-dev-state": {
			Metadata: v1beta1.VolumeMetadata{Name: team.Name, Realm: "dev", Space: "teams", Stack: "kukeon"},
		},
		"dev///scratch": {Metadata: v1beta1.VolumeMetadata{Name: "scratch", Realm: "dev"}},
	}
	c.configs = []v1beta1.CellConfigDoc{{
		Metadata: v1beta1.CellConfigMetadata{Name: "kukeon-dev", Realm: "dev", Space: "teams", Stack: "kukeon"},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
			Connector: name, Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
			State: v1beta1.ConnectorResolved, Volume: team.Name, VolumeRef: &team,
			Login: &v1beta1.LoginStatus{State: v1beta1.LoginStateLoggedIn},
		}}},
	}}
	handler := loginHandler(t, c)

	w := connectorRequest(t, handler, http.MethodGet, "/api/connectors/logins", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	got := decodeBody[[]loginIdentityView](t, w)
	if len(got) != 2 {
		t.Fatalf("identities = %+v, want the tagged volume and the team state volume", got)
	}
	byName := map[string]loginIdentityView{}
	for _, id := range got {
		byName[id.Volume.Name] = id
	}
	personal, teamID := byName["personal"], byName[team.Name]
	if personal.Connector != name || !personal.Tagged || personal.Login == nil ||
		personal.Login.State != v1beta1.LoginStateEmpty || len(personal.UsedBy) != 0 {
		t.Errorf("personal = %+v, want a tagged, empty identity nothing uses", personal)
	}
	if teamID.Connector != name || teamID.Tagged || !teamID.Exists || teamID.Login == nil ||
		teamID.Login.State != v1beta1.LoginStateLoggedIn ||
		len(teamID.UsedBy) != 1 || teamID.UsedBy[0].Name != "kukeon-dev" {
		t.Errorf("team volume = %+v, want an untagged, logged-in identity used by kukeon-dev", teamID)
	}
	if personal.Session != nil || teamID.Session != nil {
		t.Errorf("no login runs, but a session is reported: %+v / %+v", personal.Session, teamID.Session)
	}

	catalog := decodeBody[[]connectorView](t, connectorRequest(t, handler, http.MethodGet, "/api/connectors", ""))
	logins := 0
	for _, conn := range catalog {
		for _, method := range conn.Methods {
			if method.Login {
				logins++
				if method.Delivery != v1beta1.ConnectorDeliveryStateVolume {
					t.Errorf("%s/%s is marked a login without stateVolume delivery", conn.Name, method.Name)
				}
			}
		}
	}
	if logins == 0 {
		t.Error("the catalog marks no method as a login")
	}
}

// TestLoginIdentityRolesInUsedBy: each Config in usedBy names the team role
// it runs (issue #1790), `<team>/<role>` from its labels, or from the
// kukeon.io/agent-team annotation of a New agent copy, so the Identities page
// needs no second read per row. A Volume two roles share names both; a Config
// outside a team names none.
func TestLoginIdentityRolesInUsedBy(t *testing.T) {
	name := loginConnectorName(t)
	shared := v1beta1.VolumeRef{Name: "work-max", Realm: "dev"}
	uses := func(cfg string, labels, annotations map[string]string) v1beta1.CellConfigDoc {
		return v1beta1.CellConfigDoc{
			Metadata: v1beta1.CellConfigMetadata{
				Name: cfg, Realm: "dev", Labels: labels, Annotations: annotations,
			},
			Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
				Connector: name, Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
				State: v1beta1.ConnectorResolved, Volume: shared.Name, VolumeRef: &shared,
			}}},
		}
	}
	c := &loginClient{connectorClient: *devScopes()}
	c.volumes = map[string]v1beta1.VolumeDoc{
		"dev///work-max": {
			Metadata: v1beta1.VolumeMetadata{Name: shared.Name, Realm: "dev"},
			Spec:     v1beta1.VolumeSpec{Connector: name, Method: "login", LoginMarker: "marker"},
		},
	}
	c.configs = []v1beta1.CellConfigDoc{
		uses("kukeon-dev", map[string]string{v1beta1.LabelTeam: "kukeon", v1beta1.LabelRole: "dev"}, nil),
		uses("kuke-majordomo", map[string]string{v1beta1.LabelTeam: "kuke", v1beta1.LabelRole: "majordomo"}, nil),
		uses("my-agent", map[string]string{v1beta1.LabelRole: "agent"},
			map[string]string{v1beta1.AnnotationAgentTeam: "default"}),
		uses("plain", nil, nil),
	}
	w := connectorRequest(t, loginHandler(t, c), http.MethodGet, "/api/connectors/logins", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body)
	}
	got := decodeBody[[]loginIdentityView](t, w)
	if len(got) != 1 {
		t.Fatalf("identities = %+v, want the one shared Volume", got)
	}
	roles := map[string]string{}
	for _, user := range got[0].UsedBy {
		roles[user.Name] = user.Role
	}
	want := map[string]string{
		"kukeon-dev": "kukeon/dev", "kuke-majordomo": "kuke/majordomo", "my-agent": "default/agent", "plain": "",
	}
	if !reflect.DeepEqual(roles, want) {
		t.Errorf("usedBy roles = %v, want %v", roles, want)
	}
	if body := w.Body.String(); strings.Count(body, `"role":`) != 3 {
		t.Errorf("a Config outside a team carries a role in %s", body)
	}
}

// TestLoginIdentityCreateAndLogin: "New login identity" creates the tagged,
// retained Volume and no cell; "Log in" starts the identity cell — mounting
// only that Volume — and returns its coordinates; a second "Log in" while the
// session runs returns the same cell instead of starting another.
//
//nolint:gocyclo,cyclop // one identity followed through create, login and a second login.
func TestLoginIdentityCreateAndLogin(t *testing.T) {
	name := loginConnectorName(t)
	c := &loginClient{connectorClient: *devScopes()}
	handler := loginHandler(t, c)
	body := `{"name":"personal-max","scope":{"realm":"dev"}}`

	w := connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+name+"/logins", body)
	if w.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body)
	}
	created := decodeBody[loginResult](t, w)
	volume, ok := c.volumes["dev///personal-max"]
	if !ok || !created.Created || created.Identity.Session != nil || len(c.started) != 0 {
		t.Fatalf("create = %+v, volumes = %+v, started = %d; want the volume and no cell",
			created, c.volumes, len(c.started))
	}
	if volume.Spec.Connector != name || volume.Spec.LoginMarker == "" ||
		volume.Spec.ReclaimPolicy != v1beta1.ReclaimRetain {
		t.Errorf("volume spec = %+v, want the tag, the marker and Retain", volume.Spec)
	}

	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+name+"/login", body)
	if w.Code != http.StatusOK {
		t.Fatalf("login status=%d body=%s", w.Code, w.Body)
	}
	first := decodeBody[loginResult](t, w)
	if first.Created || first.Attached || first.Identity.Session == nil || len(c.started) != 1 {
		t.Fatalf("login = %+v, started = %d; want one new session on the existing volume", first, len(c.started))
	}
	cell := c.started[0]
	session := first.Identity.Session
	if session.Cell != cell.Metadata.Name || session.Realm != "dev" || session.Container == "" {
		t.Errorf("session = %+v, want the coordinates of cell %q", session, cell.Metadata.Name)
	}
	container := cell.Spec.Containers[0]
	want := v1beta1.VolumeRef{Name: "personal-max", Realm: "dev"}
	if !cell.Spec.AutoDelete || len(cell.Spec.Containers) != 1 || len(container.Volumes) != 1 ||
		container.Volumes[0].VolumeRef == nil || *container.Volumes[0].VolumeRef != want ||
		len(container.Secrets) != 0 || len(container.Repos) != 0 || cell.Spec.Provenance != nil {
		t.Errorf("identity cell = %+v, want a self-deleting cell with only the identity volume", cell.Spec)
	}

	w = connectorRequest(t, handler, http.MethodPost, "/api/connectors/"+name+"/login", body)
	second := decodeBody[loginResult](t, w)
	if w.Code != http.StatusOK || !second.Attached || len(c.started) != 1 ||
		second.Identity.Session == nil || second.Identity.Session.Cell != session.Cell {
		t.Fatalf(
			"second login = %+v (status %d), started = %d; want the running session",
			second,
			w.Code,
			len(c.started),
		)
	}

	listed := decodeBody[[]loginIdentityView](t,
		connectorRequest(t, handler, http.MethodGet, "/api/connectors/logins", ""))
	if len(listed) != 1 || listed[0].Session == nil || listed[0].Session.Cell != session.Cell {
		t.Errorf("list = %+v, want the identity with its running session", listed)
	}
}

// TestLoginIdentityRejections: a connector without a login method, an unknown
// connector, an unsafe name or scope, and a Volume that is another connector's
// are refused, and no cell is started.
func TestLoginIdentityRejections(t *testing.T) {
	name := loginConnectorName(t)
	c := &loginClient{connectorClient: *devScopes()}
	c.volumes = map[string]v1beta1.VolumeDoc{"dev///taken": {
		Metadata: v1beta1.VolumeMetadata{Name: "taken", Realm: "dev"},
		Spec:     v1beta1.VolumeSpec{Connector: "something-else", Method: "login"},
	}}
	handler := loginHandler(t, c)
	for label, tc := range map[string]struct{ connector, body, want string }{
		"no login method":   {connectortest.Name, `{"name":"x","scope":{"realm":"dev"}}`, "no method that keeps a login"},
		"unknown connector": {"nope", `{"name":"x","scope":{"realm":"dev"}}`, "unknown connector"},
		"unsafe name":       {name, `{"name":"../x","scope":{"realm":"dev"}}`, "safe name"},
		"no name":           {name, `{"name":"","scope":{"realm":"dev"}}`, "safe name"},
		"unsafe scope":      {name, `{"name":"x","scope":{"realm":"../dev"}}`, ""},
		"another connector": {name, `{"name":"taken","scope":{"realm":"dev"}}`, "something-else"},
	} {
		t.Run(label, func(t *testing.T) {
			for _, action := range []string{"logins", "login"} {
				w := connectorRequest(t, handler, http.MethodPost,
					"/api/connectors/"+tc.connector+"/"+action, tc.body)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("%s: status=%d body=%s, want 400", action, w.Code, w.Body)
				}
				if !strings.Contains(w.Body.String(), tc.want) {
					t.Errorf("%s: body %s does not mention %q", action, w.Body, tc.want)
				}
			}
			if len(c.started) != 0 {
				t.Errorf("a cell was started: %+v", c.started)
			}
		})
	}
}

// TestPickLoginIdentityForConfig: picking a login identity for a Config
// writes it as the Config's state volume, scope included, and leaves the
// other values alone; a Volume of another connector, one that does not exist,
// and clearing the pick are refused.
func TestPickLoginIdentityForConfig(t *testing.T) {
	name := loginConnectorName(t)
	c := &loginClient{connectorClient: *devScopes()}
	own := v1beta1.VolumeRef{Name: "mine-state", Realm: "dev", Space: "default", Stack: "default"}
	c.configs = []v1beta1.CellConfigDoc{{
		APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{Name: "mine", Realm: "dev"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "bp", Realm: "dev"},
			Values: map[string]string{
				"KEEP": "me", "STATE_VOLUME": own.Name, "STATE_VOLUME_REALM": own.Realm,
				"STATE_VOLUME_SPACE": own.Space, "STATE_VOLUME_STACK": own.Stack,
			},
		},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
			Connector: name, Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
			State: v1beta1.ConnectorResolved, Volume: own.Name, VolumeRef: &own,
		}}},
	}}
	c.volumes = map[string]v1beta1.VolumeDoc{
		"dev///personal-max": {
			Metadata: v1beta1.VolumeMetadata{Name: "personal-max", Realm: "dev"},
			Spec:     v1beta1.VolumeSpec{Connector: name, Method: "login"},
		},
		"dev///taken": {
			Metadata: v1beta1.VolumeMetadata{Name: "taken", Realm: "dev"},
			Spec:     v1beta1.VolumeSpec{Connector: "something-else", Method: "login"},
		},
	}
	handler := loginHandler(t, c)
	pick := func(identity string) int {
		w := connectorRequest(t, handler, http.MethodPost, "/api/configs/connector",
			`{"config":{"name":"mine","realm":"dev"},"connector":"`+name+`","identity":`+identity+`}`)
		return w.Code
	}

	// The daemon stores a pick another role uses, with a warning (#1789); the
	// warning comes back beside the saved Config for the web UI to show.
	const warning = `config "mine": volume "personal-max" is also the login identity of role dev`
	c.applyWarnings = map[string][]string{string(v1beta1.KindCellConfig): {warning}}
	w := connectorRequest(
		t,
		handler,
		http.MethodPost,
		"/api/configs/connector",
		`{"config":{"name":"mine","realm":"dev"},"connector":"`+name+`","identity":{"name":"personal-max","realm":"dev"}}`,
	)
	if w.Code != http.StatusOK {
		t.Fatalf("pick status=%d %s", w.Code, w.Body)
	}
	var saved struct {
		v1beta1.CellConfigDoc

		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Metadata.Name != "mine" || len(saved.Warnings) != 1 || saved.Warnings[0] != warning {
		t.Fatalf("pick response = %s, want the Config and [%q]", w.Body, warning)
	}
	c.applyWarnings = nil
	values := c.configs[0].Spec.Values
	want := map[string]string{
		"KEEP": "me", "STATE_VOLUME": "personal-max", "STATE_VOLUME_REALM": "dev",
		"STATE_VOLUME_SPACE": "", "STATE_VOLUME_STACK": "",
	}
	for key, value := range want {
		if values[key] != value {
			t.Errorf("values[%s] = %q, want %q (values %+v)", key, values[key], value, values)
		}
	}
	applied := len(c.applied)
	for label, identity := range map[string]string{
		"another connector": `{"name":"taken","realm":"dev"}`,
		"missing volume":    `{"name":"nope","realm":"dev"}`,
		"clearing":          `null`,
	} {
		if code := pick(identity); code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", label, code)
		}
	}
	if len(c.applied) != applied {
		t.Errorf("a refused pick was applied")
	}
}

// TestCreateFromConfigSendsState: the harness state picked for one cell
// reaches the daemon's render request; anything but persistent or ephemeral is
// refused.
func TestCreateFromConfigSendsState(t *testing.T) {
	c := devScopes()
	c.configs = []v1beta1.CellConfigDoc{exampleConfig("mine", "", v1beta1.ConnectorResolved)}
	handler := connectorHandler(t, c)
	preview := func(extra string) int {
		w := connectorRequest(t, handler, http.MethodPost, "/api/cells/from-config/preview",
			`{"config":{"name":"mine","realm":"dev"},"name":"cell-1","env":[]`+extra+`}`)
		return w.Code
	}
	if code := preview(""); code != http.StatusOK || c.previews[0].State != "" {
		t.Fatalf("status=%d state=%q, want no state sent", code, c.previews[0].State)
	}
	if code := preview(`,"state":"ephemeral"`); code != http.StatusOK ||
		c.previews[1].State != v1beta1.HarnessStateEphemeral {
		t.Fatalf("status=%d state=%q, want ephemeral", code, c.previews[1].State)
	}
	if code := preview(`,"state":"sometimes"`); code != http.StatusBadRequest {
		t.Errorf("unknown state: status=%d, want 400", code)
	}
}
