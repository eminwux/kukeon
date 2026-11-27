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
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// The Sources API (#1644, AC1 and AC6): a stored Source belongs to no team,
// so it has its own routes. Each calls the internal/team/initialize function
// the matching kuke command calls (AC8): kuke get sources with the teams
// that use each Source (SourceUsers), kuke create source (CreateSource),
// kuke source show (ShowSource) and kuke delete source (DeleteSource), plus
// InspectSource, which reads a Source before it is stored. A fetch uses the
// Source's git identity, named by its name only: no route returns secret
// data.

func (a *teamAPI) registerSources(mux *http.ServeMux, read, op routeWrap) {
	mux.HandleFunc("GET /api/sources", read(a.listSources))
	mux.HandleFunc("POST /api/sources", op(a.createSource))
	mux.HandleFunc("POST /api/sources/inspect", op(a.inspectAddress))
	mux.HandleFunc("POST /api/sources/{name}/inspect", emptyJSONRequest(op(a.inspectStored)))
	mux.HandleFunc("POST /api/sources/{name}/delete", emptyJSONRequest(op(a.deleteSource)))
}

// routeWrap turns a team handler into a route.
type routeWrap func(func(context.Context, kukeonv1.Client, *http.Request) (any, error)) http.HandlerFunc

// sourceView is a Source: its address, ref and git identity by name.
type sourceView struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Branch   string `json:"branch,omitempty"`
	Tag      string `json:"tag,omitempty"`
	Commit   string `json:"commit,omitempty"`
	Identity string `json:"identity,omitempty"`
}

func toSourceView(s config.Source) sourceView {
	return sourceView{
		Name: s.Name, URL: s.URL, Branch: s.Branch, Tag: s.Tag, Commit: s.Commit, Identity: identityName(s.Git),
	}
}

// selectionView is a default role or harness.
type selectionView struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}

// sourceUseView is one use of a Source: a team's project, sources entry,
// role or harness, or a default role or harness (Team empty).
type sourceUseView struct {
	Team string `json:"team,omitempty"`
	As   string `json:"as"`
	Name string `json:"name"`
}

// storedSourceView is one stored Source on the Sources page.
type storedSourceView struct {
	sourceView

	UsedBy           []sourceUseView `json:"usedBy"`
	DefaultRoles     []selectionView `json:"defaultRoles"`
	DefaultHarnesses []selectionView `json:"defaultHarnesses"`
}

// sourcesListView lists the stored Sources and the whole default lists,
// which a default toggle rewrites.
type sourcesListView struct {
	Sources          []storedSourceView `json:"sources"`
	DefaultRoles     []selectionView    `json:"defaultRoles"`
	DefaultHarnesses []selectionView    `json:"defaultHarnesses"`
	// DefaultForm is the default roles and connector choices in the team
	// form's shape (#1746): what the wizard starts from when a team stops
	// inheriting them.
	DefaultForm defaultsForm `json:"defaultForm"`
}

func usesView(uses []initialize.SourceUse) []sourceUseView {
	out := make([]sourceUseView, 0, len(uses))
	for _, use := range uses {
		out = append(out, sourceUseView{Team: use.Team, As: use.How, Name: use.Name})
	}
	return out
}

func (a *teamAPI) listSources(ctx context.Context, client kukeonv1.Client, _ *http.Request) (any, error) {
	stored, err := initialize.Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	defaults, err := initialize.Defaults(ctx, client)
	if err != nil {
		return nil, err
	}
	users, err := initialize.SourceUsers(ctx, client)
	if err != nil {
		return nil, err
	}
	d := defaults.Spec.Defaults
	view := sourcesListView{
		Sources:          make([]storedSourceView, 0, len(stored)),
		DefaultRoles:     make([]selectionView, 0, len(d.Roles)),
		DefaultHarnesses: make([]selectionView, 0, len(d.Harnesses)),
		DefaultForm:      defaultsFormOf(d, a.catalog),
	}
	for _, role := range d.Roles {
		view.DefaultRoles = append(
			view.DefaultRoles,
			selectionView{Name: role.Name, Source: role.Source, Path: role.Path},
		)
	}
	for _, h := range d.Harnesses {
		view.DefaultHarnesses = append(
			view.DefaultHarnesses,
			selectionView{Name: h.Name, Source: h.Source, Path: h.Path},
		)
	}
	for _, name := range slices.Sorted(maps.Keys(stored)) {
		spec := stored[name]
		item := storedSourceView{
			sourceView: sourceView{
				Name: name, URL: spec.URL, Branch: spec.Branch, Tag: spec.Tag, Commit: spec.Commit,
				Identity: identityName(spec.Git),
			},
			UsedBy: usesView(users[name]), DefaultRoles: []selectionView{}, DefaultHarnesses: []selectionView{},
		}
		for _, role := range view.DefaultRoles {
			if role.Source == name {
				item.DefaultRoles = append(item.DefaultRoles, role)
			}
		}
		for _, h := range view.DefaultHarnesses {
			if h.Source == name {
				item.DefaultHarnesses = append(item.DefaultHarnesses, h)
			}
		}
		view.Sources = append(view.Sources, item)
	}
	return view, nil
}

// sourceRequest is kuke create source's flags, or the Source an inspect
// reads.
type sourceRequest struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	Tag      string `json:"tag"`
	Commit   string `json:"commit"`
	Identity string `json:"identity"`
	Force    bool   `json:"force"`
}

func (r sourceRequest) identity() *config.SecretRef {
	if name := strings.TrimSpace(r.Identity); name != "" {
		return &config.SecretRef{Name: name, Realm: config.DefaultRealm}
	}
	return nil
}

// contentsView is what a source offers.
type contentsView struct {
	*initialize.SourceContents

	Source sourceView `json:"source"`
}

func toContentsView(contents *initialize.SourceContents) *contentsView {
	if contents == nil {
		return nil
	}
	return &contentsView{Source: toSourceView(contents.Source), SourceContents: contents}
}

// sourceChangeView reports a stored Source: created or updated, the teams
// and defaults that use a replaced one, and what it offers.
type sourceChangeView struct {
	Action     string          `json:"action"`
	Source     sourceView      `json:"source"`
	UsedBy     []sourceUseView `json:"usedBy"`
	Discovered *contentsView   `json:"discovered,omitempty"`
}

func (a *teamAPI) createSource(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	var req sourceRequest
	if err := decodeJSONBody(r, maxConnectorBody, &req); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	change, err := initialize.CreateSource(ctx, initialize.SourceOptions{
		Options: a.options(client, false), Name: name, URL: strings.TrimSpace(req.URL),
		Branch: req.Branch, Tag: req.Tag, Commit: req.Commit, Identity: req.identity(), Force: req.Force,
	})
	if err != nil {
		return nil, err
	}
	source := toSourceView(config.Source{
		Name: name, URL: change.Source.URL, Branch: change.Source.Branch, Tag: change.Source.Tag,
		Commit: change.Source.Commit, Git: change.Source.Git,
	})
	return sourceChangeView{
		Action: change.Action, Source: source, UsedBy: usesView(change.Users),
		Discovered: toContentsView(change.Discovered),
	}, nil
}

// inspectAddress reads a Source that is not stored: an address, a ref and an
// identity, as the wizard's sources step offers them.
func (a *teamAPI) inspectAddress(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	var req sourceRequest
	if err := decodeJSONBody(r, maxConnectorBody, &req); err != nil {
		return nil, err
	}
	spec := config.SourceSpec{URL: strings.TrimSpace(req.URL), Branch: req.Branch, Tag: req.Tag, Commit: req.Commit}
	if ref := req.identity(); ref != nil {
		spec.Git = &config.SourceGit{IdentityRef: ref}
	}
	contents, err := initialize.InspectSource(ctx, a.options(client, false), spec)
	if err != nil {
		return nil, err
	}
	return toContentsView(contents), nil
}

func (a *teamAPI) inspectStored(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	contents, err := initialize.ShowSource(ctx, a.options(client, false), r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	return toContentsView(contents), nil
}

// deleteSource deletes a stored Source; kukeond refuses one a team or the
// defaults use (ErrSourceInUse, 409 in_use) and names them.
func (a *teamAPI) deleteSource(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	if err := initialize.DeleteSource(ctx, a.options(client, false), r.PathValue("name")); err != nil {
		return nil, err
	}
	return map[string]bool{"deleted": true}, nil
}
