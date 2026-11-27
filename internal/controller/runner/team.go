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

package runner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// teamFileMode and teamsDirMode follow the configs directory (issue #1618): a
// team document holds source URLs and secret references, never secret bytes.
const (
	teamsDirMode  os.FileMode = 0o755
	teamFileMode  os.FileMode = 0o644
	teamExtension             = ".yaml"
)

// teamPathSafe reports whether a team or realm name is a single local path
// segment. The controller validates names before calling the runner; each
// runner method repeats the check inline so the filepath.IsLocal guard
// dominates the path it builds, both for defense in depth and for static
// path-injection analysis (CodeQL go/path-injection).
func teamPathSafe(value string) bool {
	return value != "" && value != "." && value != ".." && filepath.IsLocal(value) &&
		!strings.ContainsAny(value, `/\\`) && !strings.ContainsRune(value, 0)
}

func unsafeTeamRef(md intmodel.TeamMetadata) error {
	return fmt.Errorf("%w: unsafe team reference %q in realm %q", errdefs.ErrInvalidTeam, md.Name, md.Realm)
}

// WriteTeam persists a Team's validated document to
// <RunPath>/data/<realm>/teams/<name>.yaml atomically (temp file + rename).
// It reports whether the document was newly created. The caller has checked
// the realm exists.
func (r *Exec) WriteTeam(team intmodel.Team) (bool, error) {
	md := team.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return false, unsafeTeamRef(md)
	}
	return r.writeTeamDocument(
		fs.TeamsDir(r.opts.RunPath, md.Realm), fs.TeamPath(r.opts.RunPath, md.Realm, md.Name),
		".team-*.tmp", team.Document, "team", md,
	)
}

// GetTeam reads one stored Team's document. Returns errdefs.ErrTeamNotFound
// when none is stored.
func (r *Exec) GetTeam(team intmodel.Team) (intmodel.Team, error) {
	md := team.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return intmodel.Team{}, unsafeTeamRef(md)
	}
	data, err := os.ReadFile(fs.TeamPath(r.opts.RunPath, md.Realm, md.Name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.Team{}, errdefs.ErrTeamNotFound
		}
		return intmodel.Team{}, fmt.Errorf("%w: %w", errdefs.ErrGetTeam, err)
	}
	return intmodel.Team{Metadata: md, Document: data}, nil
}

// ListTeams enumerates the stored Teams of realmName, or of every realm when
// it is empty, with their documents.
func (r *Exec) ListTeams(realmName string) ([]intmodel.Team, error) {
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListTeams, err)
	}
	var out []intmodel.Team
	for _, realmDir := range realmDirs {
		realm := filepath.Base(realmDir)
		dir := fs.TeamsDir(r.opts.RunPath, realm)
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("%w: read teams dir %q: %w", errdefs.ErrListTeams, dir, readErr)
		}
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), teamExtension)
			if entry.IsDir() || !ok || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			team, getErr := r.GetTeam(intmodel.Team{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}})
			if errors.Is(getErr, errdefs.ErrTeamNotFound) {
				continue // Deleted between the read and the get.
			}
			if getErr != nil {
				return nil, fmt.Errorf("%w: %w", errdefs.ErrListTeams, getErr)
			}
			out = append(out, team)
		}
	}
	return out, nil
}

// DeleteTeam removes one stored Team's document. Returns
// errdefs.ErrTeamNotFound when none is stored.
func (r *Exec) DeleteTeam(team intmodel.Team) error {
	md := team.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return unsafeTeamRef(md)
	}
	path := fs.TeamPath(r.opts.RunPath, md.Realm, md.Name)
	if err := r.guardKindRemove(fs.TeamsDir(r.opts.RunPath, md.Realm), fs.RealmDepth+1, path); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errdefs.ErrTeamNotFound
		}
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteTeam, err)
	}
	r.logger.InfoContext(r.ctx, "team deleted", "name", md.Name, "realm", md.Realm)
	return nil
}

// WriteTeamDefaults persists a realm's TeamDefaults document to
// <RunPath>/data/<realm>/team-defaults/<name>.yaml atomically.
func (r *Exec) WriteTeamDefaults(defaults intmodel.TeamDefaults) (bool, error) {
	md := defaults.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return false, unsafeTeamRef(md)
	}
	return r.writeTeamDocument(
		fs.TeamDefaultsDir(r.opts.RunPath, md.Realm), fs.TeamDefaultsPath(r.opts.RunPath, md.Realm, md.Name),
		".team-defaults-*.tmp", defaults.Document, "team defaults", md,
	)
}

// GetTeamDefaults reads a realm's stored TeamDefaults. Returns
// errdefs.ErrTeamDefaultsNotFound when the realm stores none.
func (r *Exec) GetTeamDefaults(defaults intmodel.TeamDefaults) (intmodel.TeamDefaults, error) {
	md := defaults.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return intmodel.TeamDefaults{}, unsafeTeamRef(md)
	}
	data, err := os.ReadFile(fs.TeamDefaultsPath(r.opts.RunPath, md.Realm, md.Name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.TeamDefaults{}, errdefs.ErrTeamDefaultsNotFound
		}
		return intmodel.TeamDefaults{}, fmt.Errorf("%w: %w", errdefs.ErrGetTeam, err)
	}
	return intmodel.TeamDefaults{Metadata: md, Document: data}, nil
}

func (r *Exec) writeTeamDocument(
	dir, path, pattern string, document []byte, what string, md intmodel.TeamMetadata,
) (bool, error) {
	if err := os.MkdirAll(dir, teamsDirMode); err != nil {
		return false, fmt.Errorf("%w: create %s dir: %w", errdefs.ErrWriteTeam, what, err)
	}
	if err := os.Chmod(dir, teamsDirMode); err != nil {
		return false, fmt.Errorf("%w: chmod %s dir: %w", errdefs.ErrWriteTeam, what, err)
	}
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)
	if statErr != nil && !created {
		return false, fmt.Errorf("%w: stat %s: %w", errdefs.ErrWriteTeam, what, statErr)
	}
	if err := atomicWriteFileMode(dir, path, pattern, document, teamFileMode); err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrWriteTeam, err)
	}
	action := writeAction(created)
	r.logger.InfoContext(r.ctx, what+" "+action, "name", md.Name, "realm", md.Realm)
	return created, nil
}

func unsafeSourceRef(md intmodel.TeamMetadata) error {
	return fmt.Errorf("%w: unsafe source reference %q in realm %q", errdefs.ErrInvalidSource, md.Name, md.Realm)
}

// WriteSource persists a Source's validated document to
// <RunPath>/data/<realm>/sources/<name>.yaml atomically (issue #1643). It
// reports whether the document was newly created. The caller has checked the
// realm exists.
func (r *Exec) WriteSource(source intmodel.Source) (bool, error) {
	md := source.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return false, unsafeSourceRef(md)
	}
	created, err := r.writeTeamDocument(
		fs.SourcesDir(r.opts.RunPath, md.Realm), fs.SourcePath(r.opts.RunPath, md.Realm, md.Name),
		".source-*.tmp", source.Document, "source", md,
	)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errdefs.ErrWriteSource, err)
	}
	return created, nil
}

// GetSource reads one stored Source's document. Returns
// errdefs.ErrSourceNotFound when none is stored.
func (r *Exec) GetSource(source intmodel.Source) (intmodel.Source, error) {
	md := source.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return intmodel.Source{}, unsafeSourceRef(md)
	}
	data, err := os.ReadFile(fs.SourcePath(r.opts.RunPath, md.Realm, md.Name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return intmodel.Source{}, errdefs.ErrSourceNotFound
		}
		return intmodel.Source{}, fmt.Errorf("%w: %w", errdefs.ErrGetSource, err)
	}
	return intmodel.Source{Metadata: md, Document: data}, nil
}

// ListSources enumerates the stored Sources of realmName, or of every realm
// when it is empty, with their documents.
func (r *Exec) ListSources(realmName string) ([]intmodel.Source, error) {
	realmDirs, err := r.resolveRealmDirs(realmName)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errdefs.ErrListSources, err)
	}
	var out []intmodel.Source
	for _, realmDir := range realmDirs {
		realm := filepath.Base(realmDir)
		dir := fs.SourcesDir(r.opts.RunPath, realm)
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, fmt.Errorf("%w: read sources dir %q: %w", errdefs.ErrListSources, dir, readErr)
		}
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), teamExtension)
			if entry.IsDir() || !ok || strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			source, getErr := r.GetSource(intmodel.Source{Metadata: intmodel.TeamMetadata{Name: name, Realm: realm}})
			if errors.Is(getErr, errdefs.ErrSourceNotFound) {
				continue // Deleted between the read and the get.
			}
			if getErr != nil {
				return nil, fmt.Errorf("%w: %w", errdefs.ErrListSources, getErr)
			}
			out = append(out, source)
		}
	}
	return out, nil
}

// DeleteSource removes one stored Source's document. Returns
// errdefs.ErrSourceNotFound when none is stored.
func (r *Exec) DeleteSource(source intmodel.Source) error {
	md := source.Metadata
	if !filepath.IsLocal(md.Realm) || !filepath.IsLocal(md.Name) || !teamPathSafe(md.Realm) || !teamPathSafe(md.Name) {
		return unsafeSourceRef(md)
	}
	path := fs.SourcePath(r.opts.RunPath, md.Realm, md.Name)
	if err := r.guardKindRemove(fs.SourcesDir(r.opts.RunPath, md.Realm), fs.RealmDepth+1, path); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteSource, err)
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errdefs.ErrSourceNotFound
		}
		return fmt.Errorf("%w: %w", errdefs.ErrDeleteSource, err)
	}
	r.logger.InfoContext(r.ctx, "source deleted", "name", md.Name, "realm", md.Realm)
	return nil
}
