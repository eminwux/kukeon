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
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestKukeondIntegration requires an explicitly selected disposable kukeond with
// default realm/space/stack metadata. It applies uniquely named configs against
// the realm's shared blueprints, reads them back, repeats apply, and on cleanup
// removes the configs plus any shared blueprint this run created.
func TestKukeondIntegration(t *testing.T) {
	host := os.Getenv("KUKETEAMS_INTEGRATION_HOST")
	if host == "" {
		t.Skip("set KUKETEAMS_INTEGRATION_HOST to a disposable kukeond Unix endpoint")
	}
	client, err := kukeonv1.Dial(t.Context(), host)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	team := inheritingTeam(fmt.Sprintf("init-check-%d", time.Now().UnixNano()))
	result := compositionFor(t, "codex", nil, team)
	// Shared blueprints belong to the realm, not to this check: one that
	// existed before is not removed (applying replaces it in place when its
	// content differs, #1560), and one this run created is removed only when
	// no other config references it.
	created := map[string]bool{}
	for _, bp := range result.Blueprints {
		got, getErr := client.GetBlueprint(t.Context(), *bp)
		if getErr != nil {
			t.Fatal(getErr)
		}
		created[bp.Metadata.Name] = !got.MetadataExists
	}
	defer func() {
		// The configs go first, so deleting the Team afterwards prunes no
		// shared blueprint that existed before this run.
		defer func() {
			if _, deleteErr := client.DeleteTeam(t.Context(), kukeonv1.TeamRef{Name: team.Metadata.Name}); deleteErr != nil {
				t.Errorf("cleanup team: %v", deleteErr)
			}
		}()
		for _, pair := range result.Pairs {
			if _, deleteErr := client.DeleteConfig(t.Context(), *pair.Config); deleteErr != nil {
				t.Errorf("cleanup config: %v", deleteErr)
			}
		}
		for _, bp := range result.Blueprints {
			if !created[bp.Metadata.Name] {
				continue
			}
			refs := &configRefs{client: client}
			users, lookupErr := refs.users(t.Context(), bp.Metadata.Realm, bp.Metadata.Name, team.Metadata.Name)
			if lookupErr != nil || len(users) > 0 {
				continue
			}
			if _, deleteErr := client.DeleteBlueprint(t.Context(), *bp); deleteErr != nil {
				t.Errorf("cleanup blueprint: %v", deleteErr)
			}
		}
	}()
	before, err := client.ListCells(t.Context(), "default", "default", "default")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = applyMissing(t.Context(), client, result); err != nil {
			t.Fatal(err)
		}
		// The Team is stored with the documents composed from it (AC4).
		if got, getErr := client.GetTeam(t.Context(), kukeonv1.TeamRef{Name: team.Metadata.Name}); getErr != nil ||
			!got.Exists {
			t.Fatalf("team readback: exists=%v error=%v", got.Exists, getErr)
		}
		for _, pair := range result.Pairs {
			bp, readErr := client.GetBlueprint(t.Context(), *pair.Blueprint)
			// A shared blueprint carries the shared label and no team label.
			if readErr != nil || !bp.MetadataExists ||
				bp.Blueprint.Metadata.Labels[api.LabelShared] != "true" ||
				bp.Blueprint.Metadata.Labels[api.LabelTeam] != "" {
				t.Fatalf("blueprint readback: exists=%v labels=%v error=%v",
					bp.MetadataExists, bp.Blueprint.Metadata.Labels, readErr)
			}
			cc, readErr := client.GetConfig(t.Context(), *pair.Config)
			if readErr != nil || !cc.MetadataExists || cc.Config.Metadata.Labels[api.LabelTeam] != team.Metadata.Name ||
				cc.Config.Spec.Blueprint.Name != pair.Blueprint.Metadata.Name {
				t.Fatalf("config readback: exists=%v error=%v", cc.MetadataExists, readErr)
			}
		}
	}
	after, err := client.ListCells(t.Context(), "default", "default", "default")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("initialization changed runtime cells")
	}
}
