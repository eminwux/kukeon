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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/rpc"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"gopkg.in/yaml.v3"
)

// The teams API (issue #1618, AC15 to AC17). Every route calls
// internal/team/initialize, the package `kuke team`, `kuke create team`,
// `kuke create source` and `kuke apply -f` call, so a preview, an apply, a
// stored Source and a defaults edit are the same code from the browser as
// from the CLI (#1643, AC15). Composition runs here, in kuke serve, as the
// account running it: a Source is fetched with its stored git identity when
// it names one, else with this account's git configuration, and a secret
// binding's source.env / source.file is read by this process (D1, G5).
//
// A declaration travels as a YAML string in both directions (G1): only plans,
// summaries and results are JSON. Every route that fetches from git is a
// POST, so it gets creationTimeout. Preview and apply are separate requests
// and each composes from scratch; the apply response is the authoritative
// result.

const (
	// maxTeamBody bounds a team request: a declaration is a few KiB.
	maxTeamBody = 256 << 10
	// maxTeamActionBody bounds an action request, whose body is only {}.
	maxTeamActionBody = 64
	// yamlIndent is how a declaration is indented, as kuke prints one.
	yamlIndent = 2
	// teamRealm is the realm teams are managed in.
	teamRealm = config.DefaultRealm
)

// teamAPI serves the teams routes. sources, when set, replaces fetching
// (tests); catalog is the connector catalog the form offers per-role
// connectors from (#1649).
type teamAPI struct {
	sources map[string]fs.FS
	catalog *connector.Catalog
}

func registerTeamRoutes(mux *http.ServeMux, dial Dial, catalog *connector.Catalog) {
	(&teamAPI{catalog: catalog}).register(mux, dial)
}

func (a *teamAPI) register(mux *http.ServeMux, dial Dial) {
	// read serves a route that only calls the daemon; op one that runs a
	// team operation, whose unclassified failures are the operation's own.
	read := func(fn func(context.Context, kukeonv1.Client, *http.Request) (any, error)) http.HandlerFunc {
		return resourceRoute(dial, fn)
	}
	op := func(fn func(context.Context, kukeonv1.Client, *http.Request) (any, error)) http.HandlerFunc {
		return resourceRoute(dial, func(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
			result, err := fn(ctx, client, r)
			if err != nil {
				return nil, teamError(err)
			}
			return result, nil
		})
	}
	mux.HandleFunc("GET /api/teams", read(a.listTeams))
	mux.HandleFunc("GET /api/teams/{name}", read(a.getTeam))
	mux.HandleFunc(
		"POST /api/teams/preview",
		op(func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
			return a.applyTeam(ctx, c, r, true)
		}),
	)
	mux.HandleFunc("POST /api/teams", op(func(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
		return a.applyTeam(ctx, c, r, false)
	}))
	mux.HandleFunc("POST /api/teams/init", emptyJSONRequest(timedRoute(dial, lifecycleTimeout, a.initTeams)))
	mux.HandleFunc("POST /api/teams/{name}/reapply", emptyJSONRequest(op(a.reapplyTeam)))
	mux.HandleFunc("POST /api/teams/{name}/delete", emptyJSONRequest(read(a.deleteTeam)))
	mux.HandleFunc("POST /api/teams/draft", op(a.draftTeam))
	mux.HandleFunc("GET /api/teams/{name}/export", op(a.exportTeam))
	mux.HandleFunc("GET /api/team-defaults", read(a.getDefaults))
	mux.HandleFunc("POST /api/team-defaults", op(a.setDefaults))
	a.registerSources(mux, read, op)
}

// emptyJSONRequest gates an action route whose request carries nothing but
// its path. Like decodeCatalogDeleteRequest it requires Content-Type:
// application/json, which a cross-site HTML form cannot send, so a plain
// <form method=post> cannot reapply or delete a team or a source. The body
// may be empty or {}. The check runs before kukeond is dialed.
func emptyJSONRequest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := decodeEmptyJSONRequest(r); err != nil {
			writeAPIError(w, err)
			return
		}
		next(w, r)
	}
}

func decodeEmptyJSONRequest(r *http.Request) error {
	if r.Header.Get("Content-Type") != jsonContentType {
		return fmt.Errorf("%w: JSON request required", errdefs.ErrConfig)
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxTeamActionBody))
	decoder.DisallowUnknownFields()
	var options struct{}
	if err := decoder.Decode(&options); err != nil && err != io.EOF {
		return fmt.Errorf("%w: this request accepts only an empty JSON object", errdefs.ErrConfig)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("%w: expected one JSON document", errdefs.ErrConfig)
	}
	return nil
}

func (a *teamAPI) options(client kukeonv1.Client, dryRun bool) initialize.Options {
	return initialize.Options{Client: client, Sources: a.sources, DryRun: dryRun}
}

// teamSummaryView is a stored team as the browser lists it.
type teamSummaryView struct {
	Name               string   `json:"name"`
	Realm              string   `json:"realm"`
	ProjectURL         string   `json:"projectUrl,omitempty"`
	ProjectRef         string   `json:"projectRef,omitempty"`
	Roles              []string `json:"roles"`
	RolesInherited     bool     `json:"rolesInherited"`
	Harnesses          []string `json:"harnesses"`
	HarnessesInherited bool     `json:"harnessesInherited"`
	Configs            []string `json:"configs"`
	// System reports a system team, kuke or default (issue #1692), which is
	// reset with kuke team init --force and is not deleted from the web UI.
	System bool `json:"system,omitempty"`
}

// teamView is one stored team: its summary, its declaration as YAML, its
// view (the hydrated team, AC3) and its form (the stored team, AC4).
type teamView struct {
	Summary  teamSummaryView `json:"summary"`
	Document string          `json:"document"`
	View     *teamDetailView `json:"view,omitempty"`
	Form     *teamForm       `json:"form,omitempty"`
	// Problem says why the view or the form could not be built: a stored
	// declaration that no longer decodes, or a failed read.
	Problem string `json:"problem,omitempty"`
}

func summaryView(s kukeonv1.TeamSummary) teamSummaryView {
	return teamSummaryView{
		Name: s.Name, Realm: s.Realm, ProjectURL: s.ProjectURL, ProjectRef: s.ProjectRef,
		Roles: nonNil(s.Roles), RolesInherited: s.RolesInherited,
		Harnesses: nonNil(s.Harnesses), HarnessesInherited: s.HarnessesInherited,
		Configs: nonNil(s.Configs), System: s.Name == config.SystemTeamName || s.Name == config.DefaultTeamName,
	}
}

func (a *teamAPI) listTeams(ctx context.Context, client kukeonv1.Client, _ *http.Request) (any, error) {
	listed, err := client.ListTeams(ctx, teamRealm)
	if err != nil {
		return nil, err
	}
	out := make([]teamSummaryView, 0, len(listed))
	for _, team := range listed {
		out = append(out, summaryView(team.Summary))
	}
	return map[string]any{"teams": out}, nil
}

func (a *teamAPI) getTeam(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	name := r.PathValue("name")
	got, err := client.GetTeam(ctx, kukeonv1.TeamRef{Name: name, Realm: teamRealm})
	if err != nil {
		return nil, err
	}
	if !got.Exists {
		return nil, fmt.Errorf("%w: %s", errdefs.ErrTeamNotFound, name)
	}
	out := teamView{Summary: summaryView(got.Summary), Document: string(got.Document)}
	resolved, err := initialize.ResolveTeam(ctx, client, name)
	if err != nil {
		out.Problem = err.Error()
		return out, nil //nolint:nilerr // the summary and document still show; Problem says what is missing
	}
	view := teamViewOf(resolved, a.catalog)
	form := formOf(resolved.Stored, a.catalog)
	out.View, out.Form = &view, &form
	return out, nil
}

// teamRequest is a create or an edit: the inputs of kuke create team, or of
// kuke team apply -f when it carries a declaration and no source.
type teamRequest struct {
	// Name is the team. With Source it is create team's NAME; with only a
	// Declaration it must match the declaration's metadata.name when set.
	Name string `json:"name"`
	// Source, Branch, Tag and Commit are create team's --source and ref
	// flags.
	Source string `json:"source"`
	Branch string `json:"branch"`
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
	// UseRepoFile reads the repository's kuketeam.yaml; false is
	// --no-repo-file. Omitted means true.
	UseRepoFile *bool `json:"useRepoFile"`
	// Roles and Harnesses are --role and --harness: null leaves the
	// declaration's list, an array (even empty) replaces it.
	Roles     []string `json:"roles"`
	Harnesses []string `json:"harnesses"`
	// Declaration is a Team document as YAML: add's -f, or the whole
	// declaration of an edit.
	Declaration string `json:"declaration"`
	// Force replaces a stored team of the same name on add (AC8a).
	Force bool `json:"force"`
	// Identity is create team's --identity: the git identity the project's
	// Source is stored with when the add creates it.
	Identity string `json:"identity"`
	// Form, when set, replaces Declaration as the edit (#1644, D3): the
	// declaration is form.apply(base), where base is the stored team for
	// an edit, and Declaration — the draft's, sent back unchanged — for a
	// new team.
	Form *teamForm `json:"form"`
}

func (r teamRequest) identity() *config.SecretRef {
	if name := strings.TrimSpace(r.Identity); name != "" {
		return &config.SecretRef{Name: name, Realm: config.DefaultRealm}
	}
	return nil
}

// planView is a plan as the browser sees it: the plan, and the declaration
// it composed as YAML.
type planView struct {
	*initialize.Plan

	Declaration string `json:"declaration"`
}

// teamResultView is what an apply committed.
type teamResultView struct {
	Team           string   `json:"team"`
	Resources      int      `json:"resources"`
	Pruned         []string `json:"pruned"`
	MissingSecrets []string `json:"missingSecrets"`
	Notes          []string `json:"notes"`
	// Warnings are the risks kukeond reported for resources it applied, such
	// as a login identity another role uses (issue #1789).
	Warnings []string `json:"warnings,omitempty"`
}

type teamOutcomeView struct {
	Plan   planView        `json:"plan"`
	Result *teamResultView `json:"result,omitempty"`
	// Connectors, on a dry run only, is what each composed Config would
	// resolve per connector (#1745): the team page's Connectors rows before
	// anything is stored. ConnectorNotes names the Configs that did not
	// render, with the reason.
	Connectors     *[]initialize.RoleConnector `json:"connectors,omitempty"`
	ConnectorNotes []string                    `json:"connectorNotes,omitempty"`
}

func (a *teamAPI) applyTeam(ctx context.Context, client kukeonv1.Client, r *http.Request, dryRun bool) (any, error) {
	var req teamRequest
	if err := decodeJSONBody(r, maxTeamBody, &req); err != nil {
		return nil, err
	}
	opts := a.options(client, dryRun)
	if req.Form != nil {
		return a.applyForm(ctx, opts, req)
	}
	var outcome initialize.Outcome
	var err error
	switch {
	case req.Source != "":
		add := initialize.AddOptions{
			Options: opts, Name: req.Name, Source: req.Source,
			Branch: req.Branch, Tag: req.Tag, Commit: req.Commit,
			Roles: req.Roles, Harnesses: req.Harnesses,
			NoRepoFile: req.UseRepoFile != nil && !*req.UseRepoFile,
			Force:      req.Force, Identity: req.identity(),
		}
		if strings.TrimSpace(req.Declaration) != "" {
			add.File, add.FileName = []byte(req.Declaration), "declaration"
		}
		outcome, err = initialize.Add(ctx, add)
	case strings.TrimSpace(req.Declaration) != "":
		if req.Roles != nil || req.Harnesses != nil {
			return nil, fmt.Errorf("%w: roles and harnesses are edited in the declaration", errdefs.ErrInvalidTeam)
		}
		if err = checkDeclarationName(req); err != nil {
			return nil, err
		}
		outcome, err = initialize.ApplyFile(ctx, opts, []byte(req.Declaration), "declaration")
	default:
		return nil, fmt.Errorf("%w: give a repository URL or a declaration", errdefs.ErrInvalidTeam)
	}
	if err != nil {
		return nil, err
	}
	return a.outcomeView(ctx, client, outcome)
}

// formFileName names a form's declaration in plans and messages.
const formFileName = "form"

// applyForm composes the declaration a form describes and runs it through
// the same operation as the CLI (AC8): a new team (a draft's declaration as
// the base) is kuke create team NAME --source ... -f FILE; an edit (the
// stored team as the base) is kuke team apply -f FILE.
func (a *teamAPI) applyForm(ctx context.Context, opts initialize.Options, req teamRequest) (any, error) {
	if req.Roles != nil || req.Harnesses != nil {
		return nil, fmt.Errorf("%w: roles and harnesses are edited in the form", errdefs.ErrInvalidTeam)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.TrimSpace(req.Form.Name)
	}
	if formName := strings.TrimSpace(req.Form.Name); formName != "" && formName != name {
		return nil, fmt.Errorf("%w: the form names team %s, not %s", errdefs.ErrInvalidTeam, formName, name)
	}
	if strings.TrimSpace(req.Declaration) != "" {
		base, err := config.DecodeTeamDeclaration(strings.NewReader(req.Declaration))
		if err != nil {
			return nil, fmt.Errorf("%w: declaration: %w", errdefs.ErrInvalidTeam, err)
		}
		inherited, err := inheritedRoles(ctx, opts.Client, base, req.Form)
		if err != nil {
			return nil, err
		}
		team, err := req.Form.apply(base, inherited, a.catalog)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = team.Metadata.Name
		}
		raw, err := encodeYAML(team)
		if err != nil {
			return nil, err
		}
		outcome, err := initialize.Add(ctx, initialize.AddOptions{
			Options: opts, Name: name, Source: strings.TrimSpace(req.Source),
			Branch: req.Branch, Tag: req.Tag, Commit: req.Commit,
			File: []byte(raw), FileName: formFileName, Force: req.Force, Identity: req.identity(),
		})
		if err != nil {
			return nil, err
		}
		return a.outcomeView(ctx, previewerOf(opts.Client), outcome)
	}
	if name == "" {
		return nil, fmt.Errorf("%w: name the team the form edits", errdefs.ErrInvalidTeam)
	}
	if req.Source != "" || req.Force || req.identity() != nil {
		return nil, fmt.Errorf("%w: an edit keeps its project; give the project ref in the form",
			errdefs.ErrInvalidTeam)
	}
	base, err := initialize.StoredTeam(ctx, opts.Client, name)
	if err != nil {
		return nil, err
	}
	if base == nil {
		return nil, fmt.Errorf("%w: %s", errdefs.ErrTeamNotFound, name)
	}
	inherited, err := inheritedRoles(ctx, opts.Client, base, req.Form)
	if err != nil {
		return nil, err
	}
	team, err := req.Form.apply(base, inherited, a.catalog)
	if err != nil {
		return nil, err
	}
	team.Metadata.Name = name
	raw, err := encodeYAML(team)
	if err != nil {
		return nil, err
	}
	outcome, err := initialize.ApplyFile(ctx, opts, []byte(raw), formFileName)
	if err != nil {
		return nil, err
	}
	return a.outcomeView(ctx, previewerOf(opts.Client), outcome)
}

// inheritedRoles are the TeamDefaults' roles when form declares the roles of
// a base that inherits them (#1746), so apply builds each over the default
// role it copies; nil otherwise, without reading the defaults.
func inheritedRoles(
	ctx context.Context, client initialize.Client, base *config.Team, form *teamForm,
) ([]config.Role, error) {
	if form.Roles.Inherit || base.Spec.Roles != nil {
		return nil, nil
	}
	defaults, err := initialize.Defaults(ctx, client)
	if err != nil {
		return nil, err
	}
	return defaults.Spec.Defaults.Roles, nil
}

// draftRequest is what the wizard's Project step reads: create team's
// inputs without composing (#1644 Step 2).
type draftRequest struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Branch      string `json:"branch"`
	Tag         string `json:"tag"`
	Commit      string `json:"commit"`
	Identity    string `json:"identity"`
	UseRepoFile *bool  `json:"useRepoFile"`
	// Declaration is a pasted kuketeam.yaml.
	Declaration string `json:"declaration"`
}

// draftSourceView is one source of a draft and its state (#1643 AC6).
type draftSourceView struct {
	Alias    string        `json:"alias"`
	Stored   string        `json:"stored"`
	URL      string        `json:"url"`
	Ref      string        `json:"ref,omitempty"`
	Identity string        `json:"identity,omitempty"`
	State    string        `json:"state"`
	Have     string        `json:"have,omitempty"`
	Contents *contentsView `json:"contents,omitempty"`
}

// draftView is a draft: the form the wizard edits and the declaration it
// sends back unchanged (D3), which the browser never parses.
type draftView struct {
	Origin      initialize.Origin `json:"origin"`
	Name        string            `json:"name"`
	Form        teamForm          `json:"form"`
	Declaration string            `json:"declaration"`
	Sources     []draftSourceView `json:"sources"`
	Notes       []string          `json:"notes"`
}

func (a *teamAPI) draftTeam(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	var req draftRequest
	if err := decodeJSONBody(r, maxTeamBody, &req); err != nil {
		return nil, err
	}
	opts := initialize.AddOptions{
		Options: a.options(client, true), Name: strings.TrimSpace(req.Name), Source: strings.TrimSpace(req.Source),
		Branch: req.Branch, Tag: req.Tag, Commit: req.Commit,
		NoRepoFile: req.UseRepoFile != nil && !*req.UseRepoFile,
		Identity:   teamRequest{Identity: req.Identity}.identity(),
	}
	if strings.TrimSpace(req.Declaration) != "" {
		opts.File, opts.FileName = []byte(req.Declaration), "declaration"
	}
	draft, err := initialize.DraftTeam(ctx, opts)
	if err != nil {
		return nil, err
	}
	declaration, err := encodeYAML(draft.Team)
	if err != nil {
		return nil, err
	}
	view := draftView{
		Origin: draft.Origin, Name: draft.Name, Form: formOf(draft.Team, a.catalog), Declaration: declaration,
		Sources: make([]draftSourceView, 0, len(draft.Sources)), Notes: nonNil(draft.Notes),
	}
	// A new team gives each of its roles every connector a team can give
	// (#1745): the switches start on, and the request body carries them.
	for i := range view.Form.Roles.Items {
		for j := range view.Form.Roles.Items[i].Connectors {
			view.Form.Roles.Items[i].Connectors[j].Enabled = true
		}
	}
	for _, source := range draft.Sources {
		view.Sources = append(view.Sources, draftSourceView{
			Alias: source.Alias, Stored: source.Stored, URL: source.URL, Ref: source.Ref,
			Identity: source.Identity, State: source.State, Have: source.Have,
			Contents: toContentsView(source.Contents),
		})
	}
	return view, nil
}

// exportTeam is kuke team export NAME (#1643 AC12): ?hostFields=1 is
// --host-fields, ?explicit=1 is --explicit-defaults.
func (a *teamAPI) exportTeam(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	flag := func(key string) bool {
		switch r.URL.Query().Get(key) {
		case "1", "true":
			return true
		}
		return false
	}
	raw, err := initialize.Export(ctx, client, r.PathValue("name"), initialize.ExportOptions{
		HostFields: flag("hostFields"), ExplicitDefaults: flag("explicit"),
	})
	if err != nil {
		return nil, err
	}
	return map[string]string{"document": string(raw)}, nil
}

// checkDeclarationName keeps an edit of team A from storing team B.
func checkDeclarationName(req teamRequest) error {
	if req.Name == "" {
		return nil
	}
	team, err := config.DecodeTeam(strings.NewReader(req.Declaration))
	if err != nil {
		return fmt.Errorf("%w: declaration: %w", errdefs.ErrInvalidTeam, err)
	}
	if team.Metadata.Name != req.Name {
		return fmt.Errorf("%w: the declaration names team %s, not %s; rename a team by adding the new one "+
			"and deleting the old", errdefs.ErrInvalidTeam, team.Metadata.Name, req.Name)
	}
	return nil
}

func (a *teamAPI) reapplyTeam(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	name := r.PathValue("name")
	outcome, err := initialize.ApplyStored(ctx, a.options(client, false), name, "")
	if err != nil {
		return nil, err
	}
	return a.outcomeView(ctx, client, outcome)
}

// initTeamView is one system team kuke team init applied: what was stored,
// by kind.
type initTeamView struct {
	Team       string   `json:"team"`
	Resources  int      `json:"resources"`
	Configs    []string `json:"configs"`
	Blueprints []string `json:"blueprints"`
	// MissingSecrets and Notes are the apply's own remarks.
	MissingSecrets []string `json:"missingSecrets"`
	Notes          []string `json:"notes"`
}

// initView is what POST /api/teams/init returns: the teams it applied, in
// order, and the remarks of a team it left alone.
type initView struct {
	Teams []initTeamView `json:"teams"`
	Notes []string       `json:"notes"`
}

// initTeams is kuke team init from the browser (issue #1694): it applies the
// kuke team and the system default team with this server's client, as the
// account running kuke serve, which fetches the kukeon source with git. The
// route runs under the lifecycle timeout because that fetch is network-bound.
// A stored kuke team is a 409 the page answers with the reapply route, and a
// host without git a 422 that says to install it. Nothing here resets a team:
// --force stays a CLI flag.
func (a *teamAPI) initTeams(ctx context.Context, client kukeonv1.Client, _ *http.Request) (any, error) {
	outcome, err := initialize.Init(ctx, initialize.InitOptions{Options: a.options(client, false)})
	switch {
	case err == nil:
	case errors.Is(err, errdefs.ErrTeamInitialized):
		return nil, fmt.Errorf("%w: the kuke team is stored; reapply it to recompose it from its stored declaration",
			errdefs.ErrTeamInitialized)
	case errors.Is(err, errdefs.ErrGitNotFound):
		return nil, fmt.Errorf("%w: install git on the host running kuke serve, then initialize again",
			errGitMissing)
	default:
		return nil, teamError(err)
	}
	view := initView{Teams: []initTeamView{}, Notes: []string{}}
	for _, applied := range append([]initialize.Outcome{outcome}, outcome.Also...) {
		if applied.Result == nil {
			view.Notes = append(view.Notes, applied.Notes...)
			continue
		}
		view.Teams = append(view.Teams, initTeamOf(applied))
	}
	return view, nil
}

func initTeamOf(applied initialize.Outcome) initTeamView {
	res := applied.Result
	view := initTeamView{
		Team: res.Team, Resources: res.Resources, Configs: []string{}, Blueprints: []string{},
		MissingSecrets: nonNil(res.MissingSecrets), Notes: nonNil(res.Notes),
	}
	if applied.Plan != nil {
		for _, pair := range applied.Plan.Pairs {
			view.Configs = append(view.Configs, pair.Config)
			for _, task := range pair.Tasks {
				view.Configs = append(view.Configs, task.Config)
			}
		}
		view.Blueprints = nonNil(applied.Plan.Blueprints())
	}
	return view
}

// previewerOf is the daemon client's draft render, when it has one: the
// web server's client always does.
func previewerOf(client initialize.Client) initialize.ConfigPreviewer {
	previewer, _ := client.(initialize.ConfigPreviewer)
	return previewer
}

// outcomeView is a team application as the browser sees it. A dry run (no
// result) also carries the connector rows of its composed Configs, rendered
// by kukeond as drafts.
func (a *teamAPI) outcomeView(
	ctx context.Context, client initialize.ConfigPreviewer, outcome initialize.Outcome,
) (teamOutcomeView, error) {
	var view teamOutcomeView
	if outcome.Plan != nil {
		declaration, err := encodeYAML(outcome.Plan.Team)
		if err != nil {
			return view, err
		}
		view.Plan = planView{Plan: outcome.Plan, Declaration: declaration}
		if outcome.Result == nil {
			rows, notes := outcome.Plan.RoleConnectors(ctx, client, a.catalog)
			view.Connectors, view.ConnectorNotes = &rows, notes
		}
	}
	if res := outcome.Result; res != nil {
		view.Result = &teamResultView{
			Team: res.Team, Resources: res.Resources, Pruned: nonNil(res.Pruned),
			MissingSecrets: nonNil(res.MissingSecrets), Notes: nonNil(res.Notes), Warnings: res.Warnings,
		}
	}
	return view, nil
}

func encodeYAML(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(yamlIndent)
	if err := encoder.Encode(value); err != nil {
		return "", fmt.Errorf("encode declaration: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return "", fmt.Errorf("encode declaration: %w", err)
	}
	return buf.String(), nil
}

type deleteTeamView struct {
	Deleted     bool                           `json:"deleted"`
	Removed     []kukeonv1.ApplyResourceResult `json:"removed"`
	LiveCells   []string                       `json:"liveCells"`
	KeptVolumes []string                       `json:"keptVolumes"`
}

func (a *teamAPI) deleteTeam(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	name := r.PathValue("name")
	got, err := client.DeleteTeam(ctx, kukeonv1.TeamRef{Name: name, Realm: teamRealm})
	if err != nil {
		return nil, err
	}
	removed := got.Removed
	if removed == nil {
		removed = []kukeonv1.ApplyResourceResult{}
	}
	return deleteTeamView{
		Deleted: got.Deleted, Removed: removed,
		LiveCells: nonNil(got.LiveCells), KeptVolumes: nonNil(got.KeptVolumes),
	}, nil
}

// defaultsChangeView reports an edit of the TeamDefaults: which teams
// inherit from the defaults and so need a reapply to see the change (AC14,
// G4).
type defaultsChangeView struct {
	Action     string   `json:"action"`
	Inheriting []string `json:"inheriting"`
}

func changeView(change initialize.DefaultsChange) defaultsChangeView {
	return defaultsChangeView{Action: change.Action, Inheriting: nonNil(change.Inheriting)}
}

type defaultsView struct {
	Exists   bool   `json:"exists"`
	Document string `json:"document"`
}

func (a *teamAPI) getDefaults(ctx context.Context, client kukeonv1.Client, _ *http.Request) (any, error) {
	got, err := client.GetTeamDefaults(ctx, teamRealm)
	if err != nil {
		return nil, err
	}
	return defaultsView{Exists: got.Exists, Document: string(got.Document)}, nil
}

// defaultsRequest is either kuke team defaults set (roles, harnesses), a
// change of the defaults' connector choices, or a whole TeamDefaults document
// (kuke apply -f). Connectors (issue #1790) maps a connector to its new choice;
// null removes the connector's entry, and connectors it does not name are
// kept.
type defaultsRequest struct {
	Roles      []string                  `json:"roles"`
	Harnesses  []string                  `json:"harnesses"`
	Connectors map[string]*formConnector `json:"connectors"`
	Document   string                    `json:"document"`
}

func (a *teamAPI) setDefaults(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	var req defaultsRequest
	if err := decodeJSONBody(r, maxTeamBody, &req); err != nil {
		return nil, err
	}
	opts := a.options(client, false)
	var change initialize.DefaultsChange
	var err error
	switch {
	case strings.TrimSpace(req.Document) != "":
		if req.Roles != nil || req.Harnesses != nil || req.Connectors != nil {
			return nil, fmt.Errorf("%w: give a document or roles, harnesses and connectors, not both",
				errdefs.ErrInvalidTeam)
		}
		change, err = initialize.ApplyDefaults(ctx, opts, []byte(req.Document))
	case req.Connectors != nil:
		if req.Roles != nil || req.Harnesses != nil {
			return nil, fmt.Errorf("%w: change the connector choices apart from roles and harnesses",
				errdefs.ErrInvalidTeam)
		}
		var changes map[string]*config.ConnectorChoice
		if changes, err = a.connectorChanges(req.Connectors); err != nil {
			return nil, err
		}
		change, err = initialize.SetDefaultConnectors(ctx, opts, changes)
	case req.Roles != nil || req.Harnesses != nil:
		change, err = initialize.SetDefaults(ctx, opts, req.Roles, req.Harnesses)
	default:
		return nil, fmt.Errorf("%w: give a document, roles, harnesses or connectors", errdefs.ErrInvalidTeam)
	}
	if err != nil {
		return nil, err
	}
	return changeView(change), nil
}

// connectorChanges checks a defaults request's connector choices against the
// catalog: a choice names a known connector, and a method one of its methods.
func (a *teamAPI) connectorChanges(in map[string]*formConnector) (map[string]*config.ConnectorChoice, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: name a connector to change", errdefs.ErrInvalidTeam)
	}
	out := make(map[string]*config.ConnectorChoice, len(in))
	for name, item := range in {
		conn, ok := a.catalog.Find(name)
		if !ok {
			return nil, fmt.Errorf("%w: unknown connector %q", errdefs.ErrInvalidTeam, name)
		}
		if item == nil {
			out[name] = nil
			continue
		}
		choice := config.ConnectorChoice{
			Method: strings.TrimSpace(item.Method), Identity: strings.TrimSpace(item.Identity),
			State: strings.TrimSpace(item.State),
		}
		if _, known := conn.Method(choice.Method); choice.Method != "" && !known {
			return nil, fmt.Errorf("%w: connector %q has no method %q", errdefs.ErrInvalidTeam, name, choice.Method)
		}
		out[name] = &choice
	}
	return out, nil
}

// errTeamOperation marks a failure of a team operation that carries no
// sentinel: a declaration that does not compose, a source that does not
// resolve, a team that already exists. Its message is the operation's own,
// which never carries git stderr, a URL's credentials or a secret value.
var errTeamOperation = errors.New("team operation failed")

// errGitMissing is a team operation that cannot fetch because the host
// running kuke serve has no git; its message says where to install it.
var errGitMissing = errors.New("git is not installed")

// teamError keeps the sentinels apiErrorStatus maps and classifies every
// other error as errTeamOperation, so it reaches the browser as a 422 with
// its message rather than as an unavailable daemon.
func teamError(err error) error {
	if status, _ := apiErrorStatus(err); status != http.StatusServiceUnavailable {
		return err
	}
	var network net.Error
	if errors.Is(err, errdefs.ErrDaemonTransport) || errors.As(err, &network) || errors.Is(err, rpc.ErrShutdown) ||
		errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &classifiedError{err: err, class: errTeamOperation}
}

// classifiedError adds a class to an error without changing its message.
type classifiedError struct {
	err   error
	class error
}

func (e *classifiedError) Error() string   { return e.err.Error() }
func (e *classifiedError) Unwrap() []error { return []error{e.err, e.class} }

// teamErrorStatus maps the team sentinels.
func teamErrorStatus(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errdefs.ErrTeamNotFound),
		errors.Is(err, errdefs.ErrTeamDefaultsNotFound),
		errors.Is(err, errdefs.ErrTeamScopeNotFound),
		errors.Is(err, errdefs.ErrSourceNotFound):
		return http.StatusNotFound, "not_found", true
	case errors.Is(err, errdefs.ErrTeamExists),
		errors.Is(err, errdefs.ErrSourceExists):
		return http.StatusConflict, "already_exists", true
	case errors.Is(err, errdefs.ErrTeamInitialized):
		return http.StatusConflict, "already_initialized", true
	case errors.Is(err, errGitMissing), errors.Is(err, errdefs.ErrGitNotFound):
		return http.StatusUnprocessableEntity, "git_missing", true
	case errors.Is(err, errdefs.ErrTeamNameInUse),
		errors.Is(err, errdefs.ErrSourceInUse):
		return http.StatusConflict, "in_use", true
	case errors.Is(err, errdefs.ErrTeamReserved):
		return http.StatusConflict, "reserved", true
	case errors.Is(err, errdefs.ErrInvalidTeam),
		errors.Is(err, errdefs.ErrInvalidSource),
		errors.Is(err, errdefs.ErrTeamApplyOnly):
		return http.StatusBadRequest, codeInvalidRequest, true
	case errors.Is(err, errTeamOperation):
		return http.StatusUnprocessableEntity, "team_failed", true
	}
	return 0, "", false
}
