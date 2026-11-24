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

package apply

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const actionUnchanged = "unchanged"

// ReconcileTeam stores a Team document (issue #1618). The caller has decided
// the Team may be stored: it arrived in the team apply that carried every
// document composed from it, and all of them applied. The realm must exist.
func ReconcileTeam(r runner.Runner, desired intmodel.Team) (ReconcileResult, error) {
	result := ReconcileResult{Action: actionUnchanged, Kind: "Team", Name: desired.Metadata.Name}
	if err := ensureTeamRealm(r, desired.Metadata.Realm); err != nil {
		return result, err
	}
	existing, err := r.GetTeam(desired)
	switch {
	case err == nil && bytes.Equal(existing.Document, desired.Document):
		return result, nil
	case err != nil && !errors.Is(err, errdefs.ErrTeamNotFound):
		return result, err
	}
	created, err := r.WriteTeam(desired)
	if err != nil {
		return result, err
	}
	result.Action = actionUpdated
	if created {
		result.Action = actionCreated
	}
	return result, nil
}

// ReconcileTeamDefaults stores a realm's TeamDefaults document. Changing it
// recomposes no team: kuke tells the operator which teams inherit it.
func ReconcileTeamDefaults(r runner.Runner, desired intmodel.TeamDefaults) (ReconcileResult, error) {
	result := ReconcileResult{Action: actionUnchanged, Kind: "TeamDefaults", Name: desired.Metadata.Name}
	if err := ensureTeamRealm(r, desired.Metadata.Realm); err != nil {
		return result, err
	}
	existing, err := r.GetTeamDefaults(desired)
	switch {
	case err == nil && bytes.Equal(existing.Document, desired.Document):
		return result, nil
	case err != nil && !errors.Is(err, errdefs.ErrTeamDefaultsNotFound):
		return result, err
	}
	created, err := r.WriteTeamDefaults(desired)
	if err != nil {
		return result, err
	}
	result.Action = actionUpdated
	if created {
		result.Action = actionCreated
	}
	return result, nil
}

// ReconcileSource stores a Source document (issue #1643): created, updated or
// unchanged. Nothing is fetched and no team is recomposed.
func ReconcileSource(r runner.Runner, desired intmodel.Source) (ReconcileResult, error) {
	result := ReconcileResult{Action: actionUnchanged, Kind: "Source", Name: desired.Metadata.Name}
	if err := ensureTeamRealm(r, desired.Metadata.Realm); err != nil {
		return result, err
	}
	existing, err := r.GetSource(desired)
	switch {
	case err == nil && bytes.Equal(existing.Document, desired.Document):
		return result, nil
	case err != nil && !errors.Is(err, errdefs.ErrSourceNotFound):
		return result, err
	}
	created, err := r.WriteSource(desired)
	if err != nil {
		return result, err
	}
	result.Action = actionUpdated
	if created {
		result.Action = actionCreated
	}
	return result, nil
}

func ensureTeamRealm(r runner.Runner, realm string) error {
	if _, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: realm}}); err != nil {
		if errors.Is(err, errdefs.ErrRealmNotFound) {
			return fmt.Errorf("%w: realm %q", errdefs.ErrTeamScopeNotFound, realm)
		}
		return fmt.Errorf("failed to verify team realm %q: %w", realm, err)
	}
	return nil
}
