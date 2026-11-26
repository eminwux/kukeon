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
	// System reports the kuke team, which is reset with kuke team init
	// --force and is never deleted.
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
		Configs: nonNil(s.Configs), System: s.Name == config.SystemTeamName,
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
}

type teamOutcomeView struct {
	Plan   planView        `json:"plan"`
	Result *teamResultView `json:"result,omitempty"`
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
	return outcomeView(outcome)
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
		team, err := req.Form.apply(base, a.catalog)
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
		return outcomeView(outcome)
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
	team, err := req.Form.apply(base, a.catalog)
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
	return outcomeView(outcome)
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
	return outcomeView(outcome)
}

func outcomeView(outcome initialize.Outcome) (teamOutcomeView, error) {
	var view teamOutcomeView
	if outcome.Plan != nil {
		declaration, err := encodeYAML(outcome.Plan.Team)
		if err != nil {
			return view, err
		}
		view.Plan = planView{Plan: outcome.Plan, Declaration: declaration}
	}
	if res := outcome.Result; res != nil {
		view.Result = &teamResultView{
			Team: res.Team, Resources: res.Resources, Pruned: nonNil(res.Pruned),
			MissingSecrets: nonNil(res.MissingSecrets), Notes: nonNil(res.Notes),
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

// defaultsRequest is either kuke team defaults set (roles, harnesses) or a
// whole TeamDefaults document (kuke apply -f).
type defaultsRequest struct {
	Roles     []string `json:"roles"`
	Harnesses []string `json:"harnesses"`
	Document  string   `json:"document"`
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
		if req.Roles != nil || req.Harnesses != nil {
			return nil, fmt.Errorf("%w: give a document or roles and harnesses, not both", errdefs.ErrInvalidTeam)
		}
		change, err = initialize.ApplyDefaults(ctx, opts, []byte(req.Document))
	case req.Roles != nil || req.Harnesses != nil:
		change, err = initialize.SetDefaults(ctx, opts, req.Roles, req.Harnesses)
	default:
		return nil, fmt.Errorf("%w: give a document, roles or harnesses", errdefs.ErrInvalidTeam)
	}
	if err != nil {
		return nil, err
	}
	return changeView(change), nil
}

// errTeamOperation marks a failure of a team operation that carries no
// sentinel: a declaration that does not compose, a source that does not
// resolve, a team that already exists. Its message is the operation's own,
// which never carries git stderr, a URL's credentials or a secret value.
var errTeamOperation = errors.New("team operation failed")

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
