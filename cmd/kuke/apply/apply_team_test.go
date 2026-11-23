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

package apply_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	apply "github.com/eminwux/kukeon/cmd/kuke/apply"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

// teamFake adds the reads a Team apply makes before composing: nothing is
// stored, so the Team composes from its own declaration.
type teamFake struct {
	fakeClient

	events []string
}

func (f *teamFake) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	f.events = append(f.events, "GetTeam "+ref.Name)
	return kukeonv1.GetTeamResult{}, nil
}

func (f *teamFake) GetTeamDefaults(context.Context, string) (kukeonv1.GetTeamDefaultsResult, error) {
	f.events = append(f.events, "GetTeamDefaults")
	return kukeonv1.GetTeamDefaultsResult{}, nil
}

func (f *teamFake) ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error) {
	f.events = append(f.events, "ListTeams")
	return nil, nil
}

// TestApplyFileSendsTeamsThroughComposition pins D7 and AC5: the file's
// other documents are applied first, in one ApplyDocuments without the Team,
// and the Team is then composed as kuke team apply -f composes it. A Team
// that selects nothing fails composition with AC11's guidance before any
// fetch, which is what shows it reached composition.
func TestApplyFileSendsTeamsThroughComposition(t *testing.T) {
	const manifest = `apiVersion: v1beta1
kind: Team
metadata:
  name: alpha
spec:
  sources: []
  roles: []
  harnesses: []
---
apiVersion: v1beta1
kind: Realm
metadata:
  name: r1
spec:
  namespace: r1
`
	fake := &teamFake{}
	var sent []byte
	fake.applyFn = func(raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
		fake.events = append(fake.events, "ApplyDocuments")
		sent = raw
		return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
			{Kind: "Realm", Name: "r1", Action: "created"},
		}}, nil
	}

	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	ctx := context.WithValue(context.Background(), types.CtxLogger, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cmd.SetContext(context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs([]string{"-f", writeTempYAML(t, manifest)})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--role") {
		t.Fatalf("err = %v, want the empty-selection guidance naming --role", err)
	}
	if fake.applyCalls != 1 || strings.Contains(string(sent), "kind: Team") ||
		!strings.Contains(string(sent), "kind: Realm") {
		t.Fatalf("ApplyDocuments calls = %d, sent:\n%s", fake.applyCalls, sent)
	}
	if len(fake.events) < 2 || fake.events[0] != "ApplyDocuments" || fake.events[1] != "GetTeam alpha" {
		t.Fatalf("events = %v, want the other documents applied before the Team is composed", fake.events)
	}
	if !strings.Contains(buf.String(), `Realm "r1": created`) {
		t.Errorf("output missing the Realm result:\n%s", buf.String())
	}
}

// TestApplyFileOnlyTeamsSkipsPlainApply: a file of Teams only sends nothing
// through the plain ApplyDocuments.
func TestApplyFileOnlyTeamsSkipsPlainApply(t *testing.T) {
	const manifest = "apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\nspec:\n  sources: []\n  roles: []\n  harnesses: []\n"
	fake := &teamFake{}
	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), apply.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs([]string{"-f", writeTempYAML(t, manifest)})
	if err := cmd.Execute(); err == nil {
		t.Fatal("an empty selection composed")
	}
	if fake.applyCalls != 0 {
		t.Fatalf("ApplyDocuments called %d times for a Team-only file", fake.applyCalls)
	}
}

// TestApplyFileTeamDefaultsPrintsInheritingTeams pins AC14 for kuke apply -f:
// a changed TeamDefaults names the teams that inherit it.
func TestApplyFileTeamDefaultsPrintsInheritingTeams(t *testing.T) {
	const manifest = "apiVersion: v1beta1\nkind: TeamDefaults\nmetadata:\n  name: default\n  realm: default\n"
	fake := &inheritingFake{}
	fake.applyFn = func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
		return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
			{Kind: "TeamDefaults", Name: "default", Action: "updated"},
		}}, nil
	}
	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), apply.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs([]string{"-f", writeTempYAML(t, manifest)})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `TeamDefaults "default": updated`) ||
		!strings.Contains(out, "Teams inheriting the defaults (not recomposed): alpha") ||
		!strings.Contains(out, "kuke team apply alpha") || strings.Contains(out, "kuke team apply beta") {
		t.Fatalf("output:\n%s", out)
	}
}

// inheritingFake stores alpha, which inherits its roles, and beta, which
// declares every list.
type inheritingFake struct {
	fakeClient
}

func (f *inheritingFake) ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error) {
	return []kukeonv1.GetTeamResult{
		{
			Exists:   true,
			Document: []byte("apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\n  realm: default\n"),
		},
		{Exists: true, Document: []byte("apiVersion: v1beta1\nkind: Team\nmetadata:\n  name: beta\n  realm: default\n" +
			"spec:\n  sources: []\n  roles: []\n  harnesses: []\n  connectors: {}\n")},
	}, nil
}

// TestApplyFileSkipsTeamsAfterAFailedDocument: a Team composes against what
// the rest of the file applied, so a failed document skips the Teams instead
// of composing them against defaults the file meant to change.
func TestApplyFileSkipsTeamsAfterAFailedDocument(t *testing.T) {
	const manifest = "apiVersion: v1beta1\nkind: TeamDefaults\nmetadata:\n  name: default\n  realm: default\n" +
		"---\napiVersion: v1beta1\nkind: Team\nmetadata:\n  name: alpha\nspec:\n  roles: []\n"
	for _, output := range []string{"", "json"} {
		t.Run("output="+output, func(t *testing.T) {
			fake := &teamFake{}
			fake.applyFn = func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
				return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "TeamDefaults", Name: "default", Action: "failed", Error: "realm missing"},
				}}, nil
			}
			cmd := apply.NewApplyCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)
			cmd.SetContext(context.WithValue(context.Background(), apply.MockControllerKey{}, kukeonv1.Client(fake)))
			args := []string{"-f", writeTempYAML(t, manifest)}
			if output != "" {
				args = append(args, "-o", output)
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if output == "" && err == nil {
				t.Fatal("a failed document did not fail the apply")
			}
			for _, event := range fake.events {
				if strings.HasPrefix(event, "GetTeam ") {
					t.Fatalf("the Team was composed after a failed document: %v", fake.events)
				}
			}
			want := `Team "alpha": skipped`
			if output != "" {
				want = "not applied: another document in the file failed"
			}
			if !strings.Contains(buf.String(), want) {
				t.Fatalf("output missing %q:\n%s", want, buf.String())
			}
		})
	}
}
