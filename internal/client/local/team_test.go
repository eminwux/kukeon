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

package local

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const teamApplyYAML = `apiVersion: v1beta1
kind: TeamDefaults
metadata:
  name: default
  realm: default
spec:
  defaults:
    sources: []
---
apiVersion: v1beta1
kind: Team
metadata:
  name: alpha
  realm: default
spec:
  projectSource:
    name: project
    url: https://example.test/alpha
    tag: v1
  roles: []
`

func newTeamTestClient(t *testing.T, runPath string) *Client {
	t.Helper()
	client := New(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), controller.Options{
		RunPath:          runPath,
		ContainerdSocket: filepath.Join(runPath, "no-containerd.sock"),
		KukeondSocket:    filepath.Join(runPath, "run", "kukeond.sock"),
	})
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func seedRealm(t *testing.T, runPath, name string) {
	t.Helper()
	err := metadata.WriteMetadata(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)),
		v1beta1.RealmDoc{
			APIVersion: v1beta1.APIVersionV1Beta1, Kind: v1beta1.KindRealm,
			Metadata: v1beta1.RealmMetadata{Name: name},
			Spec:     v1beta1.RealmSpec{Namespace: name + ".kukeon.io"},
		}, fs.RealmMetadataPath(runPath, name))
	if err != nil {
		t.Fatal(err)
	}
}

// TestTeamKindsThroughTheInProcessClient drives what kukeond runs behind the
// socket over a real run path (issue #1618, AC3 to AC6): a team apply stores
// the TeamDefaults and the Team, both read back with their lists as
// authored, survive a restart, and a delete removes the Team.
func TestTeamKindsThroughTheInProcessClient(t *testing.T) {
	ctx := context.Background()
	runPath := t.TempDir()
	seedRealm(t, runPath, "default")
	client := newTeamTestClient(t, runPath)

	// A Team on its own is refused.
	_, err := client.ApplyDocuments(ctx, []byte(teamApplyYAML))
	if !errors.Is(err, errdefs.ErrTeamApplyOnly) {
		t.Fatalf("plain apply of a Team err = %v, want ErrTeamApplyOnly", err)
	}
	if got, getErr := client.GetTeam(ctx, kukeonv1.TeamRef{Name: "alpha"}); getErr != nil || got.Exists {
		t.Fatalf("Team stored by a refused apply: %+v, %v", got, getErr)
	}

	result, err := client.ApplyDocumentsForTeam(ctx, []byte(teamApplyYAML), "alpha")
	if err != nil {
		t.Fatalf("team apply error = %v", err)
	}
	var actions []string
	for _, r := range result.Resources {
		if r.Error != "" {
			t.Fatalf("resource %s/%s failed: %s", r.Kind, r.Name, r.Error)
		}
		actions = append(actions, r.Kind+"/"+r.Name+":"+r.Action)
	}
	if strings.Join(actions, " ") != "TeamDefaults/default:created Team/alpha:created" {
		t.Fatalf("actions = %v", actions)
	}

	check := func(c *Client) {
		t.Helper()
		got, getErr := c.GetTeam(ctx, kukeonv1.TeamRef{Name: "alpha", Realm: "default"})
		if getErr != nil || !got.Exists {
			t.Fatalf("GetTeam = %+v, %v", got, getErr)
		}
		doc := string(got.Document)
		if !strings.Contains(doc, "roles: []") || strings.Contains(doc, "harnesses") {
			t.Fatalf("stored Team lost list presence:\n%s", doc)
		}
		s := got.Summary
		if s.ProjectURL != "https://example.test/alpha" || s.ProjectRef != "v1" ||
			s.RolesInherited || !s.HarnessesInherited {
			t.Fatalf("summary = %+v", s)
		}
		defaults, defErr := c.GetTeamDefaults(ctx, "")
		if defErr != nil || !defaults.Exists || defaults.Realm != "default" ||
			!strings.Contains(string(defaults.Document), "sources: []") ||
			strings.Contains(string(defaults.Document), "roles") {
			t.Fatalf("GetTeamDefaults = %+v (%s), %v", defaults, defaults.Document, defErr)
		}
	}
	check(client)
	// A kukeond restart: a new client over the same run path.
	restarted := newTeamTestClient(t, runPath)
	check(restarted)

	listed, err := restarted.ListTeams(ctx, "")
	if err != nil || len(listed) != 1 || listed[0].Summary.Name != "alpha" {
		t.Fatalf("ListTeams = %+v, %v", listed, err)
	}

	if _, err = restarted.DeleteTeam(ctx, kukeonv1.TeamRef{Name: "kuke"}); !errors.Is(err, errdefs.ErrTeamReserved) {
		t.Fatalf("DeleteTeam(kuke) err = %v, want ErrTeamReserved", err)
	}
	deleted, err := restarted.DeleteTeam(ctx, kukeonv1.TeamRef{Name: "alpha"})
	if err != nil || !deleted.Deleted {
		t.Fatalf("DeleteTeam = %+v, %v", deleted, err)
	}
	if got, getErr := restarted.GetTeam(ctx, kukeonv1.TeamRef{Name: "alpha"}); getErr != nil || got.Exists {
		t.Fatalf("Team survived delete: %+v, %v", got, getErr)
	}
	if _, err = restarted.DeleteTeam(ctx, kukeonv1.TeamRef{Name: "alpha"}); !errors.Is(err, errdefs.ErrTeamNotFound) {
		t.Fatalf("second DeleteTeam err = %v, want ErrTeamNotFound", err)
	}
}

// TestTeamApplyRejectsInvalidTeam: kukeond rejects an invalid Team with the
// decoder's message and writes nothing.
func TestTeamApplyRejectsInvalidTeam(t *testing.T) {
	ctx := context.Background()
	runPath := t.TempDir()
	seedRealm(t, runPath, "default")
	client := newTeamTestClient(t, runPath)

	raw := strings.Replace(teamApplyYAML, "  roles: []\n", "  rolez: []\n", 1)
	_, err := client.ApplyDocumentsForTeam(ctx, []byte(raw), "alpha")
	if err == nil || !strings.Contains(err.Error(), "rolez") {
		t.Fatalf("err = %v, want the decoder's message naming rolez", err)
	}
	if got, _ := client.GetTeam(ctx, kukeonv1.TeamRef{Name: "alpha"}); got.Exists {
		t.Fatal("invalid Team stored")
	}
	if got, _ := client.GetTeamDefaults(ctx, "default"); got.Exists {
		t.Fatal("TeamDefaults of a rejected apply stored")
	}
}
