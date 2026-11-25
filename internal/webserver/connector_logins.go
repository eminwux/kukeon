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
	"fmt"
	"net/http"

	"github.com/eminwux/kukeon/internal/connector/loginidentity"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Login identities (issue #1685). A connector method that keeps its login in
// the harness state volume has identities too: Volumes tagged with the
// connector. These routes list them, create one, and start the identity cell
// that logs in to one — a short-lived cell that mounts only that Volume and
// runs the harness login, which the browser then opens in the cell terminal.
// Like the rest of the connectors routes they name no connector: everything
// comes from the catalog and from the Volume's own tag. A login file is never
// read — the daemon only reports whether its marker exists.

// loginIdentityView is one login identity as the browser sees it.
type loginIdentityView struct {
	Connector string            `json:"connector"`
	Method    string            `json:"method,omitempty"`
	Volume    v1beta1.VolumeRef `json:"volume"`
	// Exists reports the Volume is on disk; Tagged that it carries the
	// connector tag (a team state volume gets it at its next cell start).
	Exists bool `json:"exists"`
	Tagged bool `json:"tagged"`
	// Login is `logged in` or `empty`; absent when unknown.
	Login *v1beta1.LoginStatus `json:"login,omitempty"`
	// UsedBy are the Configs whose status names the Volume.
	UsedBy []loginidentity.ConfigRef `json:"usedBy"`
	// Session is the identity cell logging in to the Volume right now, if
	// one is running.
	Session *loginSessionCell `json:"session,omitempty"`
}

// loginSessionCell locates an identity cell and its attachable container.
type loginSessionCell struct {
	Realm     string `json:"realm"`
	Space     string `json:"space"`
	Stack     string `json:"stack"`
	Cell      string `json:"cell"`
	Container string `json:"container"`
}

// loginRequest names a login identity of the {connector} in the path.
type loginRequest struct {
	Name   string   `json:"name"`
	Scope  scopeRef `json:"scope"`
	Method string   `json:"method,omitempty"`
}

// loginResult is what creating an identity, or starting its login, returns.
type loginResult struct {
	Identity loginIdentityView `json:"identity"`
	// Created reports that the request created the Volume.
	Created bool `json:"created"`
	// Attached reports that a login session was already running and no
	// second one was started.
	Attached bool `json:"attached,omitempty"`
}

func registerLoginIdentityRoutes(mux *http.ServeMux, dial Dial, api connectorAPI) {
	mux.HandleFunc("GET /api/connectors/logins", resourceRoute(dial, api.loginIdentities))
	mux.HandleFunc("POST /api/connectors/{connector}/logins", resourceRoute(dial, api.createLoginIdentity))
	mux.HandleFunc("POST /api/connectors/{connector}/login", resourceRoute(dial, api.startLogin))
}

// loginIdentities lists every login identity: the tagged Volumes and the
// state volumes Configs log in through, with their login state, the Configs
// using them, and the login session running on each, if any.
func (a connectorAPI) loginIdentities(ctx context.Context, c kukeonv1.Client, _ *http.Request) (any, error) {
	volumes, err := c.ListVolumes(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	configs, err := c.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, err
	}
	identities := loginidentity.Collect(volumes, configs)
	out := make([]loginIdentityView, 0, len(identities))
	for _, id := range identities {
		view := viewLoginIdentity(id)
		view.Session = runningLoginSession(ctx, c, id.Volume)
		out = append(out, view)
	}
	return out, nil
}

func viewLoginIdentity(id loginidentity.Identity) loginIdentityView {
	users := id.UsedBy
	if users == nil {
		users = []loginidentity.ConfigRef{}
	}
	return loginIdentityView{
		Connector: id.Connector, Method: id.Method, Volume: id.Volume,
		Exists: id.Exists, Tagged: id.Tagged, Login: id.Login, UsedBy: users,
	}
}

// runningLoginSession returns the identity cell of a Volume when it exists and
// is live. A lookup failure reads as "no session": the list is still useful.
func runningLoginSession(ctx context.Context, c kukeonv1.Client, ref v1beta1.VolumeRef) *loginSessionCell {
	realm, space, stack := loginidentity.CellScope(ref)
	name := loginidentity.CellName(ref.Name)
	got, err := c.GetCell(ctx, v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: name},
		Spec:     v1beta1.CellSpec{ID: name, RealmID: realm, SpaceID: space, StackID: stack},
	})
	if err != nil || !got.MetadataExists ||
		got.Cell.Metadata.Labels[v1beta1.LabelIdentityCell] != ref.Name ||
		(!got.RootContainerTaskRunning && got.Cell.Status.State != v1beta1.CellStateReady) {
		return nil
	}
	return sessionCell(got.Cell)
}

func sessionCell(cell v1beta1.CellDoc) *loginSessionCell {
	out := &loginSessionCell{
		Realm: cell.Spec.RealmID, Space: cell.Spec.SpaceID, Stack: cell.Spec.StackID, Cell: cell.Metadata.Name,
	}
	if cell.Spec.Tty != nil {
		out.Container = cell.Spec.Tty.Default
	}
	if out.Container == "" {
		for _, container := range cell.Spec.Containers {
			if container.Attachable {
				out.Container = container.ID
				break
			}
		}
	}
	return out
}

// loginInput decodes and checks the body of a login-identity request.
func (a connectorAPI) loginInput(r *http.Request) (loginidentity.Request, error) {
	conn, err := a.find(r)
	if err != nil {
		return loginidentity.Request{}, err
	}
	var input loginRequest
	if err = decodeJSONBody(r, maxConnectorBody, &input); err != nil {
		return loginidentity.Request{}, err
	}
	scope, err := checkScope(input.Scope)
	if err != nil {
		return loginidentity.Request{}, err
	}
	if !safeSegment(input.Name) {
		return loginidentity.Request{}, fmt.Errorf("%w: a login identity needs a safe name", errdefs.ErrConfig)
	}
	if input.Method != "" && !safeSegment(input.Method) {
		return loginidentity.Request{}, fmt.Errorf("%w: unsafe method name", errdefs.ErrConfig)
	}
	return loginidentity.Request{
		Connector: conn.Metadata.Name, Method: input.Method,
		Volume: v1beta1.VolumeRef{Name: input.Name, Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack},
	}, nil
}

// createLoginIdentity creates a login identity — the tagged, retained Volume
// — without logging in ("New login identity").
func (a connectorAPI) createLoginIdentity(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	req, err := a.loginInput(r)
	if err != nil {
		return nil, err
	}
	volume, created, err := loginidentity.Ensure(ctx, c, a.catalog, req)
	if err != nil {
		return nil, loginError(err)
	}
	return loginResult{Identity: a.identityOf(ctx, c, volume), Created: created}, nil
}

// startLogin makes sure the identity exists and an identity cell is logging
// in to it, and returns the cell's coordinates for the browser to attach to
// ("Log in" / "Log in again"). A session already running is returned as it
// is, so a second click attaches instead of starting another.
func (a connectorAPI) startLogin(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	req, err := a.loginInput(r)
	if err != nil {
		return nil, err
	}
	session, err := loginidentity.Start(ctx, c, a.catalog, req)
	if err != nil {
		return nil, loginError(err)
	}
	identity := a.identityOf(ctx, c, session.Volume)
	identity.Session = sessionCell(session.Cell)
	return loginResult{Identity: identity, Created: session.VolumeCreated, Attached: session.Attached}, nil
}

// identityOf describes one Volume as a login identity, with the Configs using
// it. Listing the Configs is best-effort: the identity is what was asked for.
func (a connectorAPI) identityOf(ctx context.Context, c kukeonv1.Client, volume v1beta1.VolumeDoc) loginIdentityView {
	md := volume.Metadata
	ref := v1beta1.VolumeRef(md)
	configs, _ := c.ListConfigs(ctx, md.Realm, "", "")
	for _, id := range loginidentity.Collect([]v1beta1.VolumeDoc{volume}, configs) {
		if id.Volume == ref {
			return viewLoginIdentity(id)
		}
	}
	return loginIdentityView{
		Connector: volume.Spec.Connector, Method: volume.Spec.Method, Volume: ref,
		Exists: true, Tagged: volume.Spec.IsLoginIdentity(), Login: volume.Status.Login,
		UsedBy: []loginidentity.ConfigRef{},
	}
}

// loginError classifies a login-identity failure as a config error unless it
// is one the UI already knows how to show.
func loginError(err error) error { return renderError(err) }
