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

package controller

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// stateVolumeSuffix ends the name of the harness state volume a team
// Config's cells mount (`<config>-state`, pkg/team/compose).
const stateVolumeSuffix = "-state"

// TeamRecord is one stored Team with what kukeond reads out of it.
type TeamRecord struct {
	Team    intmodel.Team
	Summary TeamSummary
}

// TeamSummary mirrors kukeonv1.TeamSummary.
type TeamSummary struct {
	Name, Realm                        string
	ProjectURL, ProjectRef             string
	Roles, Harnesses                   []string
	RolesInherited, HarnessesInherited bool
	Configs                            []string
}

// DeleteTeamResult reports what DeleteTeam removed and what it left.
type DeleteTeamResult struct {
	Removed     []ResourceResult
	LiveCells   []string
	KeptVolumes []string
}

func teamRealm(realm string) string {
	if strings.TrimSpace(realm) == "" {
		return config.DefaultRealm
	}
	return strings.TrimSpace(realm)
}

func validateTeamRef(name, realm string) error {
	if !config.ValidName(name) {
		return fmt.Errorf("%w: invalid team name %q", errdefs.ErrInvalidTeam, name)
	}
	if !config.ValidName(realm) {
		return fmt.Errorf("%w: invalid realm name %q", errdefs.ErrInvalidTeam, realm)
	}
	return nil
}

// GetTeam reads one stored Team (issue #1618). found is false, with no error,
// when none of the name is stored in the realm.
func (b *Exec) GetTeam(name, realm string) (TeamRecord, bool, error) {
	realm = teamRealm(realm)
	if err := validateTeamRef(name, realm); err != nil {
		return TeamRecord{}, false, err
	}
	team, err := b.runner.GetTeam(intmodel.Team{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}})
	if errors.Is(err, errdefs.ErrTeamNotFound) {
		return TeamRecord{}, false, nil
	}
	if err != nil {
		return TeamRecord{}, false, err
	}
	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		return TeamRecord{}, false, fmt.Errorf("%w: %w", errdefs.ErrGetTeam, err)
	}
	sources, err := b.storedSourceSpecs(realm)
	if err != nil {
		return TeamRecord{}, false, fmt.Errorf("%w: %w", errdefs.ErrGetTeam, err)
	}
	summary, err := summarizeTeam(team, configs, sources[realm])
	if err != nil {
		return TeamRecord{}, false, err
	}
	return TeamRecord{Team: team, Summary: summary}, true, nil
}

// ListTeams lists the stored Teams of a realm, or of every realm when realm
// is empty, sorted by realm and name.
func (b *Exec) ListTeams(realm string) ([]TeamRecord, error) {
	realm = strings.TrimSpace(realm)
	if realm != "" && !config.ValidName(realm) {
		return nil, fmt.Errorf("%w: invalid realm name %q", errdefs.ErrInvalidTeam, realm)
	}
	teams, err := b.runner.ListTeams(realm)
	if err != nil {
		return nil, err
	}
	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListTeams, err)
	}
	sources, err := b.storedSourceSpecs(realm)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListTeams, err)
	}
	out := make([]TeamRecord, 0, len(teams))
	for _, team := range teams {
		summary, sumErr := summarizeTeam(team, configs, sources[team.Metadata.Realm])
		if sumErr != nil {
			return nil, sumErr
		}
		out = append(out, TeamRecord{Team: team, Summary: summary})
	}
	slices.SortFunc(out, func(a, b TeamRecord) int {
		if c := strings.Compare(a.Summary.Realm, b.Summary.Realm); c != 0 {
			return c
		}
		return strings.Compare(a.Summary.Name, b.Summary.Name)
	})
	return out, nil
}

// GetTeamDefaults reads a realm's stored TeamDefaults. found is false, with
// no error, when the realm stores none.
func (b *Exec) GetTeamDefaults(realm string) (intmodel.TeamDefaults, bool, error) {
	realm = teamRealm(realm)
	if !config.ValidName(realm) {
		return intmodel.TeamDefaults{}, false, fmt.Errorf("%w: invalid realm name %q", errdefs.ErrInvalidTeam, realm)
	}
	got, err := b.runner.GetTeamDefaults(intmodel.TeamDefaults{
		Metadata: intmodel.TeamMetadata{Name: config.DefaultsName, Realm: realm},
	})
	if errors.Is(err, errdefs.ErrTeamDefaultsNotFound) {
		return intmodel.TeamDefaults{}, false, nil
	}
	if err != nil {
		return intmodel.TeamDefaults{}, false, err
	}
	return got, true, nil
}

// DeleteTeam deletes a team (issue #1618, D11): its Configs, the shared
// blueprints no other Config references, and then the Team document. Cells
// and volumes stay; the result names the cells still running from a removed
// Config and the state volumes kept. The system teams (kuke, and the system
// default team) are refused.
func (b *Exec) DeleteTeam(name, realm string) (DeleteTeamResult, error) {
	var res DeleteTeamResult
	realm = teamRealm(realm)
	if err := validateTeamRef(name, realm); err != nil {
		return res, err
	}
	if name == config.SystemTeamName {
		return res, fmt.Errorf("team %s is a %w", name, errdefs.ErrTeamReserved)
	}
	stored := intmodel.Team{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}}
	got, err := b.runner.GetTeam(stored)
	if err != nil {
		if errors.Is(err, errdefs.ErrTeamNotFound) {
			return res, fmt.Errorf("%w: %q in realm %q", errdefs.ErrTeamNotFound, name, realm)
		}
		return res, err
	}
	// The system default team (issue #1692) is reserved too. A user team
	// stored as default before it became a system team is not: deleting
	// it is how it is renamed.
	if name == config.DefaultTeamName {
		team, decodeErr := config.DecodeTeam(bytes.NewReader(got.Document))
		if decodeErr != nil || config.IsSystemDefaultTeam(team) {
			return res, fmt.Errorf("team %s is a %w", name, errdefs.ErrTeamReserved)
		}
	}
	previousShared, err := b.teamBlueprintRefs(name)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	var owned []intmodel.CellConfig
	for _, cfg := range configs {
		if cfg.Metadata.Labels[v1beta1.LabelTeam] == name {
			owned = append(owned, cfg)
		}
	}
	if res.LiveCells, err = b.liveCellsOf(owned); err != nil {
		return res, fmt.Errorf("%w: check cells: %w", errdefs.ErrDeleteTeam, err)
	}
	if res.KeptVolumes, err = b.stateVolumesOf(owned); err != nil {
		return res, fmt.Errorf("%w: check volumes: %w", errdefs.ErrDeleteTeam, err)
	}
	removed, err := b.pruneTeamObjects(name, nil, nil, previousShared)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	res.Removed = removed
	for _, r := range removed {
		if r.Error != nil {
			return res, fmt.Errorf("%w: team %q kept: %w; rerun the delete", errdefs.ErrDeleteTeam, name, r.Error)
		}
	}
	if err = b.runner.DeleteTeam(stored); err != nil && !errors.Is(err, errdefs.ErrTeamNotFound) {
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	return res, nil
}

// liveCellsOf lists the scope paths of the cells still running from one of
// configs.
func (b *Exec) liveCellsOf(configs []intmodel.CellConfig) ([]string, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	cells, err := b.runner.ListCells("", "", "")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, cell := range cells {
		switch cell.Status.State { //nolint:exhaustive // Only the running states matter.
		case intmodel.CellStateReady, intmodel.CellStatePending, intmodel.CellStateDegraded:
		default:
			continue
		}
		if slices.ContainsFunc(configs, func(cfg intmodel.CellConfig) bool {
			return cellBoundToConfig(cell, cfg.Metadata)
		}) {
			out = append(out, fmt.Sprintf("%s/%s/%s/%s",
				cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName, cell.Metadata.Name))
		}
	}
	slices.Sort(out)
	return out, nil
}

// stateVolumesOf lists the scope paths of the state volumes of configs that
// exist.
func (b *Exec) stateVolumesOf(configs []intmodel.CellConfig) ([]string, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	volumes, err := b.runner.ListVolumes("", "", "")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, cfg := range configs {
		md := cfg.Metadata
		for _, vol := range volumes {
			v := vol.Metadata
			if v.Name == md.Name+stateVolumeSuffix && v.Realm == md.Realm && v.Space == md.Space &&
				v.Stack == md.Stack {
				out = append(out, strings.TrimRight(strings.Join([]string{v.Realm, v.Space, v.Stack}, "/"), "/")+
					"/"+v.Name)
			}
		}
	}
	slices.Sort(out)
	return out, nil
}

// storedSourceSpecs reads the stored Sources of realm (every realm when
// empty), by realm and name.
func (b *Exec) storedSourceSpecs(realm string) (map[string]map[string]config.SourceSpec, error) {
	stored, err := b.runner.ListSources(realm)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]config.SourceSpec{}
	for _, source := range stored {
		doc, convErr := apischeme.ConvertSourceToExternal(source)
		if convErr != nil {
			return nil, convErr
		}
		if out[source.Metadata.Realm] == nil {
			out[source.Metadata.Realm] = map[string]config.SourceSpec{}
		}
		out[source.Metadata.Realm][source.Metadata.Name] = doc.Spec
	}
	return out, nil
}

// summarizeTeam reads a stored Team for a list. The project's address comes
// from the stored Source it resolves to (#1643), or from a legacy entry's own
// url; its ref is the entry's, else the Source's.
func summarizeTeam(
	team intmodel.Team, configs []intmodel.CellConfig, sources map[string]config.SourceSpec,
) (TeamSummary, error) {
	doc, err := apischeme.ConvertTeamToExternal(team)
	if err != nil {
		return TeamSummary{}, fmt.Errorf("%w: %w", errdefs.ErrGetTeam, err)
	}
	summary := TeamSummary{
		Name:               team.Metadata.Name,
		Realm:              team.Metadata.Realm,
		RolesInherited:     doc.Spec.Roles == nil,
		HarnessesInherited: doc.Spec.Harnesses == nil,
	}
	if project := doc.Spec.ProjectSource; project != nil {
		summary.ProjectURL = project.URL
		summary.ProjectRef = firstNonEmpty(project.Branch, project.Tag, project.Commit)
		if project.URL == "" {
			stored := firstNonEmpty(project.Stored, team.Metadata.Name)
			if spec, ok := sources[stored]; ok {
				summary.ProjectURL = spec.URL
				if summary.ProjectRef == "" {
					summary.ProjectRef = firstNonEmpty(spec.Branch, spec.Tag, spec.Commit)
				}
			}
		}
	}
	for _, role := range doc.Spec.Roles {
		summary.Roles = append(summary.Roles, role.Name)
	}
	for _, h := range doc.Spec.Harnesses {
		summary.Harnesses = append(summary.Harnesses, h.Name)
	}
	for _, cfg := range configs {
		if cfg.Metadata.Labels[v1beta1.LabelTeam] == team.Metadata.Name {
			summary.Configs = append(summary.Configs, cfg.Metadata.Name)
		}
	}
	slices.Sort(summary.Configs)
	return summary, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
