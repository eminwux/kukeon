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

package team_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	team "github.com/eminwux/kukeon/cmd/kuke/delete/team"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

type fakeClient struct {
	kukeonv1.FakeClient

	result kukeonv1.DeleteTeamResult
	err    error
	calls  []kukeonv1.TeamRef
}

func (f *fakeClient) DeleteTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.DeleteTeamResult, error) {
	f.calls = append(f.calls, ref)
	return f.result, f.err
}

func run(t *testing.T, fake *fakeClient, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := team.NewTeamCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), team.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestDeleteTeam pins AC6's output: what was removed, the cells still
// running from the removed Configs, and the state volumes kept.
func TestDeleteTeam(t *testing.T) {
	fake := &fakeClient{result: kukeonv1.DeleteTeamResult{
		Deleted: true,
		Removed: []kukeonv1.ApplyResourceResult{
			{Kind: "CellConfig", Name: "alpha-dev", Action: "pruned"},
			{Kind: "CellBlueprint", Name: "alpha-bp", Action: "pruned"},
		},
		LiveCells:   []string{"default/default/default/alpha-dev-1"},
		KeptVolumes: []string{"default/default/default/alpha-dev-state"},
	}}
	out, err := run(t, fake, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 || fake.calls[0] != (kukeonv1.TeamRef{Name: "alpha", Realm: "default"}) {
		t.Fatalf("calls = %+v", fake.calls)
	}
	for _, want := range []string{
		"Deleted CellConfig alpha-dev", "Deleted CellBlueprint alpha-bp", "Deleted team alpha",
		"cell default/default/default/alpha-dev-1", "kuke delete cell",
		"Kept state volume default/default/default/alpha-dev-state", "kuke delete volume",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDeleteTeamRefusesKuke(t *testing.T) {
	fake := &fakeClient{}
	_, err := run(t, fake, "kuke")
	if !errors.Is(err, errdefs.ErrTeamReserved) || !strings.Contains(err.Error(), "kuke team init --force") {
		t.Fatalf("err = %v, want ErrTeamReserved", err)
	}
	if len(fake.calls) != 0 {
		t.Fatal("kukeond was asked to delete the kuke team")
	}
}

func TestDeleteTeamFailurePrintsPartialResults(t *testing.T) {
	fake := &fakeClient{
		result: kukeonv1.DeleteTeamResult{Removed: []kukeonv1.ApplyResourceResult{
			{Kind: "CellConfig", Name: "alpha-dev", Action: "pruned"},
			{Kind: "CellConfig", Name: "alpha-ops", Action: "failed", Error: "i/o error"},
		}},
		err: fmt.Errorf("%w: team kept", errdefs.ErrDeleteTeam),
	}
	out, err := run(t, fake, "alpha", "--realm", "other")
	if !errors.Is(err, errdefs.ErrDeleteTeam) {
		t.Fatalf("err = %v", err)
	}
	if fake.calls[0].Realm != "other" {
		t.Errorf("realm = %q, want other", fake.calls[0].Realm)
	}
	if !strings.Contains(out, "Failed to delete CellConfig alpha-ops: i/o error") ||
		strings.Contains(out, "Deleted team") {
		t.Fatalf("output = %q", out)
	}
}

func TestDeleteTeamNotFound(t *testing.T) {
	_, err := run(t, &fakeClient{err: errdefs.ErrTeamNotFound}, "gamma")
	if !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("err = %v", err)
	}
}
