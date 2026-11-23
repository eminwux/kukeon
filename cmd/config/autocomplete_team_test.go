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

package config_test

import (
	"context"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/cobra"
)

type teamCompletionClient struct {
	kukeonv1.FakeClient

	gotRealm string
}

func (f *teamCompletionClient) ListTeams(_ context.Context, realm string) ([]kukeonv1.GetTeamResult, error) {
	f.gotRealm = realm
	return []kukeonv1.GetTeamResult{
		{Summary: kukeonv1.TeamSummary{Name: "alpha", Realm: "default"}},
		{Summary: kukeonv1.TeamSummary{Name: "kuke", Realm: "default"}},
		{Summary: kukeonv1.TeamSummary{Name: "alpha", Realm: "other"}},
	}, nil
}

// TestCompleteTeamNames completes the stored teams by prefix, each name once,
// filtered by --realm when the command has one.
func TestCompleteTeamNames(t *testing.T) {
	fake := &teamCompletionClient{}
	cmd := daemonCmd(t, fake)
	cmd.Flags().String("realm", "", "")

	names, directive := config.CompleteTeamNames(cmd, []string{}, "")
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v", directive)
	}
	assertNames(t, names, []string{"alpha", "kuke"})

	assertNames(t, mustComplete(t, cmd, "k"), []string{"kuke"})

	_ = cmd.Flags().Set("realm", "other")
	_ = mustComplete(t, cmd, "")
	if fake.gotRealm != "other" {
		t.Errorf("realm passed = %q, want other", fake.gotRealm)
	}

	if names, _ = config.CompleteTeamNames(cmd, []string{"alpha"}, ""); len(names) != 0 {
		t.Errorf("a second argument completed %v", names)
	}
}

func mustComplete(t *testing.T, cmd *cobra.Command, prefix string) []string {
	t.Helper()
	names, _ := config.CompleteTeamNames(cmd, []string{}, prefix)
	return names
}
