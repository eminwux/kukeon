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
	"maps"
	"net"
	"net/http"
	"net/rpc"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
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
// (tests).
type teamAPI struct {
	sources map[string]fs.FS
}

func registerTeamRoutes(mux *http.ServeMux, dial Dial) {
	(&teamAPI{}).register(mux, dial)
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
	mux.HandleFunc("GET /api/team-sources", op(a.listSources))
	mux.HandleFunc("POST /api/team-sources", op(a.addSource))
	mux.HandleFunc("POST /api/team-sources/{name}/inspect", emptyJSONRequest(op(a.inspectSource)))
	mux.HandleFunc("POST /api/team-sources/{name}/delete", emptyJSONRequest(op(a.removeSource)))
	mux.HandleFunc("GET /api/team-defaults", read(a.getDefaults))
	mux.HandleFunc("POST /api/team-defaults", op(a.setDefaults))
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

// teamView is one stored team: its summary and its declaration as YAML.
type teamView struct {
	Summary  teamSummaryView `json:"summary"`
	Document string          `json:"document"`
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
	return teamView{Summary: summaryView(got.Summary), Document: string(got.Document)}, nil
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
	var outcome initialize.Outcome
	var err error
	switch {
	case req.Source != "":
		add := initialize.AddOptions{
			Options: opts, Name: req.Name, Source: req.Source,
			Branch: req.Branch, Tag: req.Tag, Commit: req.Commit,
			Roles: req.Roles, Harnesses: req.Harnesses,
			NoRepoFile: req.UseRepoFile != nil && !*req.UseRepoFile,
			Force:      req.Force,
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

// sourceView is a stored Source.
type sourceView struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Branch string `json:"branch,omitempty"`
	Tag    string `json:"tag,omitempty"`
	Commit string `json:"commit,omitempty"`
}

// selectionView is a default role or harness.
type selectionView struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Path   string `json:"path"`
}

type sourcesView struct {
	Sources          []sourceView    `json:"sources"`
	DefaultRoles     []selectionView `json:"defaultRoles"`
	DefaultHarnesses []selectionView `json:"defaultHarnesses"`
}

func toSourceView(s config.Source) sourceView {
	return sourceView{Name: s.Name, URL: s.URL, Branch: s.Branch, Tag: s.Tag, Commit: s.Commit}
}

// listSources lists every stored Source (#1643) and the default roles and
// harnesses.
func (a *teamAPI) listSources(ctx context.Context, client kukeonv1.Client, _ *http.Request) (any, error) {
	stored, err := initialize.Sources(ctx, client)
	if err != nil {
		return nil, err
	}
	defaults, err := initialize.Defaults(ctx, client)
	if err != nil {
		return nil, err
	}
	d := defaults.Spec.Defaults
	view := sourcesView{
		Sources:          make([]sourceView, 0, len(stored)),
		DefaultRoles:     make([]selectionView, 0, len(d.Roles)),
		DefaultHarnesses: make([]selectionView, 0, len(d.Harnesses)),
	}
	for _, name := range slices.Sorted(maps.Keys(stored)) {
		spec := stored[name]
		view.Sources = append(view.Sources, sourceView{
			Name: name, URL: spec.URL, Branch: spec.Branch, Tag: spec.Tag, Commit: spec.Commit,
		})
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
	return view, nil
}

// sourceRequest is kuke create source's flags.
type sourceRequest struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Branch string `json:"branch"`
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
}

// contentsView is what a source offers.
type contentsView struct {
	*initialize.SourceContents

	Source sourceView `json:"source"`
}

// defaultsChangeView reports an edit of the TeamDefaults or a stored Source:
// which teams inherit from the defaults and so need a reapply to see the
// change (AC14, G4), and what a stored Source offers.
type defaultsChangeView struct {
	Action     string        `json:"action"`
	Inheriting []string      `json:"inheriting"`
	Discovered *contentsView `json:"discovered,omitempty"`
}

func changeView(change initialize.DefaultsChange) defaultsChangeView {
	return defaultsChangeView{Action: change.Action, Inheriting: nonNil(change.Inheriting)}
}

func (a *teamAPI) addSource(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	var req sourceRequest
	if err := decodeJSONBody(r, maxConnectorBody, &req); err != nil {
		return nil, err
	}
	change, err := initialize.CreateSource(ctx, initialize.SourceOptions{
		Options: a.options(client, false), Name: req.Name, URL: req.URL,
		Branch: req.Branch, Tag: req.Tag, Commit: req.Commit,
	})
	if err != nil {
		return nil, err
	}
	view := defaultsChangeView{Action: change.Action, Inheriting: []string{}}
	if change.Discovered != nil {
		view.Discovered = &contentsView{
			Source:         toSourceView(change.Discovered.Source),
			SourceContents: change.Discovered,
		}
	}
	return view, nil
}

func (a *teamAPI) inspectSource(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	contents, err := initialize.ShowSource(ctx, a.options(client, false), r.PathValue("name"))
	if err != nil {
		return nil, err
	}
	return contentsView{Source: toSourceView(contents.Source), SourceContents: contents}, nil
}

// removeSource deletes a stored Source; kukeond refuses one a team or the
// defaults reference (ErrSourceInUse, 409).
func (a *teamAPI) removeSource(ctx context.Context, client kukeonv1.Client, r *http.Request) (any, error) {
	if err := initialize.DeleteSource(ctx, a.options(client, false), r.PathValue("name")); err != nil {
		return nil, err
	}
	return defaultsChangeView{Action: "removed", Inheriting: []string{}}, nil
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
		return http.StatusBadRequest, "invalid_request", true
	case errors.Is(err, errTeamOperation):
		return http.StatusUnprocessableEntity, "team_failed", true
	}
	return 0, "", false
}
