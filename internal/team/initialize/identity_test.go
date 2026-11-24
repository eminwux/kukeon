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
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// TestWithIdentity is #1643 D10 on the client side: kuke asks kukeond to
// stage the identity the source names, fetches with the staged bundle, and
// releases it afterwards; with nothing staged it fetches as before.
func TestWithIdentity(t *testing.T) {
	source := config.Source{
		Name: "agents", URL: "https://github.com/acme/agents",
		Git: &config.SourceGit{IdentityRef: &config.SecretRef{Name: "bot-key", Realm: "default"}},
	}

	t.Run("staged", func(t *testing.T) {
		dir := t.TempDir()
		plan := `{"repos":{"` + gitplan.HostFetchRepo + `":{"url":"git@github.com:acme/agents"}}}`
		if err := os.WriteFile(filepath.Join(dir, gitplan.PlanFile), []byte(plan), 0o600); err != nil {
			t.Fatal(err)
		}
		client := newFake()
		client.stage = func(kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error) {
			return kukeonv1.StageGitFetchResult{Staged: true, ID: "0123456789abcdef", Dir: dir}, nil
		}
		ctx, release, err := withIdentity(t.Context(), client, "team-realm", source)
		if err != nil {
			t.Fatal(err)
		}
		if got := gitremote.Remote(ctx, source.URL); got != "git@github.com:acme/agents" {
			t.Errorf("Remote = %q, want the staged URL", got)
		}
		if len(client.staged) != 1 {
			t.Fatalf("StageGitFetch calls = %d, want 1", len(client.staged))
		}
		req := client.staged[0]
		if req.Realm != "team-realm" || req.Address != source.URL || req.IdentityRef == nil ||
			req.IdentityRef.Name != "bot-key" || req.IdentityRef.Realm != "default" {
			t.Errorf("StageGitFetch request = %+v", req)
		}
		if len(client.released) != 0 {
			t.Fatal("released before the fetch finished")
		}
		release()
		if !slices.Equal(client.released, []string{"0123456789abcdef"}) {
			t.Errorf("released = %q", client.released)
		}
	})

	t.Run("not staged", func(t *testing.T) {
		client := newFake()
		plain := config.Source{Name: "public", URL: "https://example.org/acme/public"}
		ctx, release, err := withIdentity(t.Context(), client, "default", plain)
		if err != nil {
			t.Fatal(err)
		}
		release()
		if got := gitremote.Remote(ctx, "example.org/acme/public"); got != "https://example.org/acme/public" {
			t.Errorf("Remote = %q, want the expanded address", got)
		}
		if len(client.staged) != 1 || client.staged[0].IdentityRef != nil || len(client.released) != 0 {
			t.Errorf("staged = %+v, released = %q", client.staged, client.released)
		}
	})

	t.Run("staging refused", func(t *testing.T) {
		client := newFake()
		client.stage = func(kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error) {
			return kukeonv1.StageGitFetchResult{}, errors.New("secret bot-key not found")
		}
		_, _, err := withIdentity(t.Context(), client, "default", source)
		if err == nil || !strings.Contains(err.Error(), `source "agents"`) {
			t.Fatalf("withIdentity = %v; want an error naming the source", err)
		}
	})

	t.Run("unusable staging is released", func(t *testing.T) {
		client := newFake()
		client.stage = func(kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error) {
			return kukeonv1.StageGitFetchResult{Staged: true, ID: "fedcba9876543210", Dir: t.TempDir()}, nil
		}
		if _, _, err := withIdentity(t.Context(), client, "default", source); err == nil {
			t.Fatal("an empty staging was accepted")
		}
		if !slices.Equal(client.released, []string{"fedcba9876543210"}) {
			t.Errorf("released = %q; the staging must not be left behind", client.released)
		}
	})
}
