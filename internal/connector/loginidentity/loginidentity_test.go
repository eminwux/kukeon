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

package loginidentity_test

import (
	"errors"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/eminwux/kukeon/internal/connector/loginidentity"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/team/harnesses"
)

// loginCatalog is a catalog of two test-only connectors: one that
// authenticates a harness with a login and a token method, and one that keeps
// no login.
func loginCatalog(t *testing.T) *connector.Catalog {
	t.Helper()
	catalog, err := connector.Load(fstest.MapFS{
		"agent/connector.yaml": {Data: []byte(`apiVersion: v1beta1
kind: Connector
metadata:
  name: agent
spec:
  title: Agent
  harness: agent
  identity:
    secretTypes: [opaque]
  methods:
    - name: token
      default: true
      delivery: env
      secret: {name: agent-token, envName: AGENT_TOKEN}
    - name: login
      delivery: stateVolume
      login:
        marker: auth.json
        command: [agent, login, --device]
`)},
		"forge/connector.yaml": {Data: []byte(`apiVersion: v1beta1
kind: Connector
metadata:
  name: forge
spec:
  title: Forge
  identity:
    secretTypes: [opaque]
  methods:
    - name: token
      default: true
      delivery: env
      secret: {name: forge-token, envName: FORGE_TOKEN}
`)},
	})
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return catalog
}

// harnessFS is the harness the agent connector authenticates.
func harnessFS() fstest.MapFS {
	return fstest.MapFS{
		"agent/harness.yaml": {Data: []byte(`apiVersion: v1beta1
kind: Harness
metadata:
  name: agent
spec:
  renderer: kukeon/bundled
  shell: /bin/bash
  statePath: $HOME/.agent
  skillPath: $HOME/.agent/skills
  onInit:
    - script: exec agent
  images:
    - name: agent
      build: {context: harnesses/agent/context, dockerfile: Dockerfile}
      capabilities: [agent]
`)},
		"agent/images.yaml": {Data: []byte(`apiVersion: v1beta1
kind: HarnessImageCatalog
metadata:
  name: agent
spec:
  images:
    - image: example.com/harness/agent:v1
      capabilities: [agent]
`)},
	}
}

func loginStatus(name string, ref v1beta1.VolumeRef, state string) v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
			Connector: "agent", Method: "login", Delivery: v1beta1.ConnectorDeliveryStateVolume,
			State: v1beta1.ConnectorResolved, Volume: ref.Name, VolumeRef: &ref,
			Login: &v1beta1.LoginStatus{State: state},
		}}},
	}
}

// TestCollect: the login identities are the tagged Volumes plus the state
// volumes Configs log in through — existing untagged, or not created yet —
// each with the Configs using it. A plain Volume, and the state volume of a
// token method nothing tagged, are not identities.
func TestCollect(t *testing.T) {
	team := v1beta1.VolumeRef{Name: "team-dev-state", Realm: "default", Space: "teams", Stack: "core"}
	future := v1beta1.VolumeRef{Name: "team-pm-state", Realm: "default", Space: "teams", Stack: "core"}
	tokenState := v1beta1.VolumeRef{Name: "team-qa-state", Realm: "default", Space: "teams", Stack: "core"}
	volume := func(ref v1beta1.VolumeRef, spec v1beta1.VolumeSpec, state string) v1beta1.VolumeDoc {
		doc := v1beta1.VolumeDoc{
			Metadata: v1beta1.VolumeMetadata(ref),
			Spec:     spec,
		}
		if state != "" {
			doc.Status.Login = &v1beta1.LoginStatus{State: state}
		}
		return doc
	}
	tag := v1beta1.VolumeSpec{Connector: "agent", Method: "login", LoginMarker: "auth.json"}
	volumes := []v1beta1.VolumeDoc{
		volume(v1beta1.VolumeRef{Name: "personal", Realm: "default"}, tag, v1beta1.LoginStateEmpty),
		volume(team, v1beta1.VolumeSpec{}, ""),
		volume(tokenState, v1beta1.VolumeSpec{}, ""),
		volume(v1beta1.VolumeRef{Name: "scratch", Realm: "default"}, v1beta1.VolumeSpec{}, ""),
	}
	token := v1beta1.CellConfigDoc{
		Metadata: v1beta1.CellConfigMetadata{Name: "team-qa", Realm: "default"},
		Status: &v1beta1.CellConfigStatus{Connectors: []v1beta1.ConnectorStatus{{
			Connector: "agent", Method: "token", Delivery: v1beta1.ConnectorDeliveryEnv,
			Volume: tokenState.Name, VolumeRef: &tokenState, HarnessState: v1beta1.HarnessStatePersistent,
		}}},
	}
	configs := []v1beta1.CellConfigDoc{
		loginStatus("team-dev-task", team, v1beta1.LoginStateLoggedIn),
		loginStatus("team-dev", team, v1beta1.LoginStateLoggedIn),
		loginStatus("team-pm", future, v1beta1.LoginStateEmpty),
		token,
		{Metadata: v1beta1.CellConfigMetadata{Name: "no-status", Realm: "default"}},
	}

	got := loginidentity.Collect(volumes, configs)
	type row struct {
		name           string
		exists, tagged bool
		status, usedBy string
	}
	var rows []row
	for _, id := range got {
		if id.Connector != "agent" || id.Method != "login" {
			t.Errorf("identity %s = %s/%s, want agent/login", id.Volume.Name, id.Connector, id.Method)
		}
		rows = append(rows, row{id.Volume.Name, id.Exists, id.Tagged, id.Status(), loginidentity.UsedBy(id.UsedBy)})
	}
	want := []row{
		{"personal", true, true, v1beta1.LoginStateEmpty, "-"},
		{"team-dev-state", true, false, v1beta1.LoginStateLoggedIn, "team-dev (+1)"},
		{"team-pm-state", false, false, v1beta1.LoginStateEmpty, "team-pm"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("identities = %+v, want %+v", rows, want)
	}
	if scope := loginidentity.ScopePath(team); scope != "default/teams/core" {
		t.Errorf("ScopePath = %q", scope)
	}
	// A tagged Volume lists the Configs that keep token-method state in it too.
	usedBy := ""
	for _, id := range loginidentity.Collect(
		[]v1beta1.VolumeDoc{volume(tokenState, tag, v1beta1.LoginStateEmpty)}, configs,
	) {
		if id.Volume == tokenState {
			usedBy = loginidentity.UsedBy(id.UsedBy)
		}
	}
	if usedBy != "team-qa" {
		t.Errorf("tagged state volume is used by %q, want team-qa", usedBy)
	}
}

// TestVolumeDocAndMethod: a new login identity is tagged with the connector
// and its login method, carries the marker and is retained; a connector with
// no login method has none.
func TestVolumeDocAndMethod(t *testing.T) {
	catalog := loginCatalog(t)
	agent, _ := catalog.Find("agent")
	forge, _ := catalog.Find("forge")

	// The default method is a token: the login method is still found.
	method, err := loginidentity.Method(agent, "")
	if err != nil || method.Name != "login" {
		t.Fatalf("Method(agent) = %+v, %v; want its login method", method, err)
	}
	doc := loginidentity.VolumeDoc(agent, method, v1beta1.VolumeRef{Name: "personal", Realm: "default", Space: "s"})
	wantSpec := v1beta1.VolumeSpec{
		ReclaimPolicy: v1beta1.ReclaimRetain, Connector: "agent", Method: "login", LoginMarker: "auth.json",
	}
	if doc.Spec != wantSpec || doc.Kind != v1beta1.KindVolume || doc.Metadata.Space != "s" {
		t.Errorf("volume doc = %+v, want spec %+v in space s", doc, wantSpec)
	}
	for name, tc := range map[string]struct {
		conn   *connector.Connector
		method string
	}{
		"no login method":     {forge, ""},
		"a token method":      {agent, "token"},
		"an unknown method":   {agent, "nope"},
		"named on a no-login": {forge, "token"},
	} {
		if _, methodErr := loginidentity.Method(tc.conn, tc.method); !errors.Is(methodErr, errdefs.ErrVolumeIdentity) {
			t.Errorf("%s: error = %v, want ErrVolumeIdentity", name, methodErr)
		}
	}
}

// TestCellDoc: the identity cell is one attachable, self-deleting container on
// the harness image that mounts only the identity's Volume at the harness
// state directory and runs the connector's login command.
//
//nolint:gocyclo,cyclop // a flat list of independent assertions on one cell document.
func TestCellDoc(t *testing.T) {
	catalog := loginCatalog(t)
	agent, _ := catalog.Find("agent")
	hn, err := loginidentity.LoadHarnessFrom(harnessFS(), agent)
	if err != nil {
		t.Fatalf("LoadHarnessFrom: %v", err)
	}
	if hn.StatePath != "/home/user/.agent" || hn.Image != "example.com/harness/agent:v1" {
		t.Fatalf("harness = %+v, want $HOME resolved and the catalog's first image", hn)
	}
	volume := v1beta1.VolumeRef{Name: "personal", Realm: "default"}
	doc, err := loginidentity.CellDoc(loginidentity.CellRequest{
		Connector: "agent", Method: "login", Command: []string{"agent", "login", "--device"},
		Volume: volume, StatePath: hn.StatePath, Image: hn.Image,
	})
	if err != nil {
		t.Fatalf("CellDoc: %v", err)
	}
	if doc.Metadata.Name != loginidentity.CellName(volume) || !doc.Spec.AutoDelete ||
		doc.Metadata.Labels[v1beta1.LabelIdentityCell] != "personal@default" ||
		doc.Metadata.Labels[v1beta1.LabelIdentityConnector] != "agent" {
		t.Errorf("cell = %+v, want a self-deleting cell labelled with the identity", doc.Metadata)
	}
	// A realm-scoped identity runs in the realm's default space and stack.
	if doc.Spec.RealmID != "default" || doc.Spec.SpaceID != "default" || doc.Spec.StackID != "default" {
		t.Errorf("scope = %s/%s/%s", doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID)
	}
	if len(doc.Spec.Containers) != 1 || doc.Spec.Provenance != nil {
		t.Fatalf("spec = %+v, want one container and no Config lineage", doc.Spec)
	}
	c := doc.Spec.Containers[0]
	if c.Command != "agent" || !reflect.DeepEqual(c.Args, []string{"login", "--device"}) || !c.Attachable ||
		c.Image != hn.Image {
		t.Errorf("container = %+v, want the login command on the harness image, attachable", c)
	}
	if len(c.Volumes) != 1 || c.Volumes[0].Target != "/home/user/.agent" ||
		c.Volumes[0].VolumeRef == nil || *c.Volumes[0].VolumeRef != volume || c.Volumes[0].Ensure {
		t.Errorf("mounts = %+v, want only the identity volume at the state directory", c.Volumes)
	}
	if len(c.Secrets) != 0 || len(c.Repos) != 0 || c.Git != nil || len(c.Env) != 0 || c.Privileged {
		t.Errorf("container carries more than the volume: %+v", c)
	}
	if doc.Spec.Tty == nil || doc.Spec.Tty.Default != c.ID {
		t.Errorf("tty = %+v, want the login container as the attach default", doc.Spec.Tty)
	}

	// A connector that declares no login command cannot be logged in to.
	_, err = loginidentity.CellDoc(loginidentity.CellRequest{
		Connector: "agent", Method: "login", Volume: volume, StatePath: hn.StatePath, Image: hn.Image,
	})
	if !errors.Is(err, errdefs.ErrVolumeIdentity) {
		t.Errorf("no command: error = %v, want ErrVolumeIdentity", err)
	}
	forge, _ := catalog.Find("forge")
	if _, err = loginidentity.LoadHarnessFrom(harnessFS(), forge); !errors.Is(err, errdefs.ErrVolumeIdentity) {
		t.Errorf("a connector without a harness: error = %v, want ErrVolumeIdentity", err)
	}
}

// TestCellNameScope: the identity cell of a Volume is told apart from the
// cell of a Volume of the same name at another scope that runs in the same
// stack — a realm- or space-scoped Volume's cell runs in the `default` stack
// below it.
func TestCellNameScope(t *testing.T) {
	refs := []v1beta1.VolumeRef{
		{Name: "claude", Realm: "r"},
		{Name: "claude", Realm: "r", Space: "default"},
		{Name: "claude", Realm: "r", Space: "default", Stack: "default"},
	}
	names, keys := map[string]bool{}, map[string]bool{}
	for _, ref := range refs {
		names[loginidentity.CellName(ref)] = true
		keys[loginidentity.IdentityKey(ref)] = true
		if err := naming.ValidateHierarchyName("cell", loginidentity.CellName(ref)); err != nil {
			t.Errorf("CellName(%+v) = %q: %v", ref, loginidentity.CellName(ref), err)
		}
	}
	if len(names) != len(refs) || len(keys) != len(refs) {
		t.Errorf("names %v, keys %v: want one of each per scope", names, keys)
	}
	if got := loginidentity.CellName(refs[2]); got != "login-claude" {
		t.Errorf("stack-scoped cell name = %q, want login-claude", got)
	}
	if got := loginidentity.IdentityKey(refs[0]); got != "claude@r" {
		t.Errorf("realm-scoped key = %q, want claude@r", got)
	}
	cell := v1beta1.CellDoc{Metadata: v1beta1.CellMetadata{
		Labels: map[string]string{v1beta1.LabelIdentityCell: loginidentity.IdentityKey(refs[2])},
	}}
	if loginidentity.IsIdentityCellOf(cell, refs[0]) || !loginidentity.IsIdentityCellOf(cell, refs[2]) {
		t.Errorf("IsIdentityCellOf compares only part of the scope")
	}
}

// TestShippedLoginConnectors: every connector kuke ships with a login method
// declares a marker and a login command, and its harness — from the catalog
// embedded in kuke — gives a state directory and an image, so `kuke connector
// login` works for each without a team.
func TestShippedLoginConnectors(t *testing.T) {
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		method, ok := conn.LoginMethod("")
		if !ok {
			continue
		}
		found++
		if method.Login == nil || method.Login.Marker == "" || len(method.Login.Command) == 0 {
			t.Errorf("connector %s: login method %s declares no marker or command: %+v",
				conn.Metadata.Name, method.Name, method.Login)
			continue
		}
		hn, harnessErr := loginidentity.LoadHarnessFrom(harnesses.FS, conn)
		if harnessErr != nil {
			t.Errorf("connector %s: %v", conn.Metadata.Name, harnessErr)
			continue
		}
		doc, cellErr := loginidentity.CellDoc(loginidentity.CellRequest{
			Connector: conn.Metadata.Name, Method: method.Name, Command: method.Login.Command,
			Volume: v1beta1.VolumeRef{Name: "x", Realm: "default"}, StatePath: hn.StatePath, Image: hn.Image,
		})
		if cellErr != nil || doc.Spec.Containers[0].Volumes[0].Target == "" {
			t.Errorf("connector %s: identity cell = %+v, %v", conn.Metadata.Name, doc, cellErr)
		}
	}
	if found == 0 {
		t.Fatal("no shipped connector has a login method")
	}
}
