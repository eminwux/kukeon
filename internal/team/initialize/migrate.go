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

package initialize

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// isLegacyRole reports whether init ever declared role under the kukeon name:
// the operator role (until #1510) and majordomo.
func isLegacyRole(role string) bool { return role == "majordomo" || role == "operator" }

// isLegacyEntry reports whether path holds the init team's declaration as it
// was registered before the rename to kuke (#1527): a kukeon TeamEntry with no
// project source whose roles are only init's. kuke team add always records a
// project source, so a project team registered as kukeon (kukeon's own
// kuketeam.yaml) never matches, nor does an unreadable file. Neither is
// migrated.
func isLegacyEntry(path string) (bool, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("init: read %s: %w", path, err)
	}
	entry, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil || entry.Metadata.Name != legacyName || entry.ProjectSource != nil || len(entry.Spec.Roles) == 0 {
		return false, nil //nolint:nilerr // Anything but the legacy declaration is left untouched.
	}
	for _, role := range entry.Spec.Roles {
		if !isLegacyRole(role.Name) {
			return false, nil
		}
	}
	return true, nil
}

// removeTeamConfigs deletes every daemon-stored CellConfig labelled with team,
// as the daemon's per-team prune would: the legacy team's configs go once its
// replacement is applied (apply has already pinged the daemon). Cells created
// from them are not touched, and the shared blueprints they referenced stay
// for the replacement to reuse.
func removeTeamConfigs(ctx context.Context, client Client, team string) error {
	configs, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return requestError("lookup", "configs", err)
	}
	for _, listed := range configs {
		if listed.Metadata.Labels[api.LabelTeam] != team {
			continue
		}
		if _, err = client.DeleteConfig(ctx, api.CellConfigDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig, Metadata: listed.Metadata,
		}); err != nil {
			return fmt.Errorf(
				"migrate team %s: delete config %s: %w; rerun kuke team init --force",
				team, listed.Metadata.Name, err,
			)
		}
	}
	return nil
}

func migrationNote(legacyPath string) string {
	return fmt.Sprintf(
		"migrated the init team from %s to %s: removed %s and the %s-majordomo-* configs. "+
			"Log in to each harness again: the state volume is named after the config, so %s-majordomo-* cells "+
			"start with a new one; delete the old %s-majordomo-* volumes with kuke delete volume when no longer needed.",
		legacyName, majordomoName, legacyPath, legacyName, majordomoName, legacyName,
	)
}
