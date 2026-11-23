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

package daemon_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// teamCore stubs the team methods of the in-process client behind kukeond;
// err, when set, is what every one returns.
type teamCore struct {
	kukeonv1.FakeClient

	gotRef   kukeonv1.TeamRef
	gotRealm string
	err      error
}

func alphaResult() kukeonv1.GetTeamResult {
	return kukeonv1.GetTeamResult{
		Exists:   true,
		Document: []byte("apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\nspec:\n  roles: []\n"),
		Summary: kukeonv1.TeamSummary{
			Name: "alpha", Realm: "default", ProjectURL: "https://example.test/alpha", ProjectRef: "main",
			Roles: []string{}, HarnessesInherited: true, Configs: []string{"alpha-dev"},
		},
	}
}

func (c *teamCore) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	c.gotRef = ref
	if c.err != nil {
		return kukeonv1.GetTeamResult{}, c.err
	}
	return alphaResult(), nil
}

func (c *teamCore) ListTeams(_ context.Context, realm string) ([]kukeonv1.GetTeamResult, error) {
	c.gotRealm = realm
	if c.err != nil {
		return nil, c.err
	}
	return []kukeonv1.GetTeamResult{alphaResult()}, nil
}

func (c *teamCore) DeleteTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.DeleteTeamResult, error) {
	c.gotRef = ref
	if c.err != nil {
		return kukeonv1.DeleteTeamResult{}, c.err
	}
	return kukeonv1.DeleteTeamResult{
		Deleted:     true,
		Removed:     []kukeonv1.ApplyResourceResult{{Kind: "CellConfig", Name: "alpha-dev", Action: "pruned"}},
		LiveCells:   []string{"default/default/default/alpha-dev-1"},
		KeptVolumes: []string{"default/default/default/alpha-dev-state"},
	}, nil
}

func (c *teamCore) GetTeamDefaults(_ context.Context, realm string) (kukeonv1.GetTeamDefaultsResult, error) {
	c.gotRealm = realm
	if c.err != nil {
		return kukeonv1.GetTeamDefaultsResult{}, c.err
	}
	return kukeonv1.GetTeamDefaultsResult{Exists: true, Realm: "default", Document: []byte("d")}, nil
}

// TestTeamRPCRoundTrip drives the four team methods over a real kukeond
// socket and the client kuke dials (issue #1618).
func TestTeamRPCRoundTrip(t *testing.T) {
	core := &teamCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, shortTempDir(t), core))
	t.Cleanup(func() { _ = client.Close() })
	ctx := testCtx(t)

	got, err := client.GetTeam(ctx, kukeonv1.TeamRef{Name: "alpha", Realm: "default"})
	if err != nil {
		t.Fatal(err)
	}
	if core.gotRef.Name != "alpha" || core.gotRef.Realm != "default" {
		t.Fatalf("ref passed = %+v", core.gotRef)
	}
	if !got.Exists || string(got.Document) != string(alphaResult().Document) ||
		got.Summary.ProjectRef != "main" || !got.Summary.HarnessesInherited ||
		!slices.Equal(got.Summary.Configs, []string{"alpha-dev"}) {
		t.Fatalf("GetTeam over the wire = %+v", got)
	}

	listed, err := client.ListTeams(ctx, "other")
	if err != nil || core.gotRealm != "other" || len(listed) != 1 || listed[0].Summary.Name != "alpha" {
		t.Fatalf("ListTeams = %+v, %v (realm passed %q)", listed, err, core.gotRealm)
	}

	deleted, err := client.DeleteTeam(ctx, kukeonv1.TeamRef{Name: "alpha"})
	if err != nil || !deleted.Deleted || len(deleted.Removed) != 1 || deleted.Removed[0].Name != "alpha-dev" ||
		len(deleted.LiveCells) != 1 || len(deleted.KeptVolumes) != 1 {
		t.Fatalf("DeleteTeam = %+v, %v", deleted, err)
	}

	defaults, err := client.GetTeamDefaults(ctx, "default")
	if err != nil || !defaults.Exists || string(defaults.Document) != "d" {
		t.Fatalf("GetTeamDefaults = %+v, %v", defaults, err)
	}
}

// TestTeamRPCErrorKinds: every team sentinel survives the wire so kuke and
// kuke serve can tell a missing team from a refused one.
func TestTeamRPCErrorKinds(t *testing.T) {
	core := &teamCore{}
	client := kukeonv1.NewUnixClient(startKukeond(t, shortTempDir(t), core))
	t.Cleanup(func() { _ = client.Close() })

	for _, sentinel := range []error{
		errdefs.ErrTeamNotFound, errdefs.ErrTeamDefaultsNotFound, errdefs.ErrTeamApplyOnly,
		errdefs.ErrTeamNameInUse, errdefs.ErrTeamReserved, errdefs.ErrInvalidTeam,
	} {
		core.err = fmt.Errorf("%w: alpha", sentinel)
		calls := map[string]error{}
		_, calls["GetTeam"] = client.GetTeam(testCtx(t), kukeonv1.TeamRef{Name: "alpha"})
		_, calls["ListTeams"] = client.ListTeams(testCtx(t), "")
		_, calls["DeleteTeam"] = client.DeleteTeam(testCtx(t), kukeonv1.TeamRef{Name: "alpha"})
		_, calls["GetTeamDefaults"] = client.GetTeamDefaults(testCtx(t), "")
		for method, err := range calls {
			if !errors.Is(err, sentinel) {
				t.Errorf("%s: err = %v, want %v across the wire", method, err, sentinel)
			}
		}
	}
}
