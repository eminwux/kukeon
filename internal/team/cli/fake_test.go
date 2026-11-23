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

package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"testing"

	"github.com/eminwux/kukeon/internal/team/cli"
	"github.com/eminwux/kukeon/internal/team/initialize"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// store stands in for kukeond: the stored Teams, TeamDefaults and Configs,
// the default scope, and a log of every write.
type store struct {
	kukeonv1.FakeClient

	teams    map[string][]byte
	defaults []byte
	configs  []api.CellConfigDoc
	// writes counts every call that changes daemon state.
	writes int
	// applied is the last ApplyDocuments or ApplyDocumentsForTeam body.
	applied []byte
	team    string
	closed  bool
}

func newStore() *store { return &store{teams: map[string][]byte{}} }

func (s *store) Close() error               { s.closed = true; return nil }
func (s *store) Ping(context.Context) error { return nil }

func (s *store) ListRealms(context.Context) ([]api.RealmDoc, error) {
	return []api.RealmDoc{
		{Metadata: api.RealmMetadata{Name: "default"}, Spec: api.RealmSpec{Namespace: "default.kukeon.io"}},
	}, nil
}

func (s *store) ListSpaces(context.Context, string) ([]api.SpaceDoc, error) {
	return []api.SpaceDoc{{Metadata: api.SpaceMetadata{Name: "default"}}}, nil
}

func (s *store) ListStacks(context.Context, string, string) ([]api.StackDoc, error) {
	return []api.StackDoc{{
		Metadata: api.StackMetadata{Name: "default"},
		Spec:     api.StackSpec{ID: "default", RealmID: "default", SpaceID: "default"},
	}}, nil
}

func (s *store) ListCells(context.Context, string, string, string) ([]api.CellDoc, error) {
	return nil, nil
}

func (s *store) GetSecret(context.Context, api.SecretDoc) (kukeonv1.GetSecretResult, error) {
	return kukeonv1.GetSecretResult{}, nil
}

func (s *store) CreateSecret(context.Context, api.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	s.writes++
	return kukeonv1.CreateSecretResult{Created: true}, nil
}

func (s *store) GetBlueprint(context.Context, api.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
	return kukeonv1.GetBlueprintResult{}, nil
}

func (s *store) ListConfigs(context.Context, string, string, string) ([]api.CellConfigDoc, error) {
	return s.configs, nil
}

func (s *store) GetConfig(context.Context, api.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	return kukeonv1.GetConfigResult{}, nil
}

func (s *store) GetTeam(_ context.Context, ref kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error) {
	raw, ok := s.teams[ref.Name]
	if !ok {
		return kukeonv1.GetTeamResult{}, nil
	}
	return kukeonv1.GetTeamResult{
		Exists: true, Document: raw, Summary: kukeonv1.TeamSummary{Name: ref.Name, Realm: "default"},
	}, nil
}

func (s *store) ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error) {
	var out []kukeonv1.GetTeamResult
	for name, raw := range s.teams {
		out = append(out, kukeonv1.GetTeamResult{
			Exists: true, Document: raw, Summary: kukeonv1.TeamSummary{Name: name, Realm: "default"},
		})
	}
	return out, nil
}

func (s *store) GetTeamDefaults(context.Context, string) (kukeonv1.GetTeamDefaultsResult, error) {
	if s.defaults == nil {
		return kukeonv1.GetTeamDefaultsResult{Realm: "default"}, nil
	}
	return kukeonv1.GetTeamDefaultsResult{Exists: true, Realm: "default", Document: s.defaults}, nil
}

func (s *store) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	return s.apply(raw, "")
}

func (s *store) ApplyDocumentsForTeam(
	_ context.Context,
	raw []byte,
	team string,
) (kukeonv1.ApplyDocumentsResult, error) {
	return s.apply(raw, team)
}

// apply stores each document of raw and reports it created.
func (s *store) apply(raw []byte, team string) (kukeonv1.ApplyDocumentsResult, error) {
	s.writes++
	s.applied, s.team = raw, team
	var result kukeonv1.ApplyDocumentsResult
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	for {
		var node yaml.Node
		if err := decoder.Decode(&node); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return result, err
		}
		var head struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if err := node.Decode(&head); err != nil {
			return result, err
		}
		doc, err := yaml.Marshal(&node)
		if err != nil {
			return result, err
		}
		switch head.Kind {
		case string(api.KindTeam):
			s.teams[head.Metadata.Name] = doc
		case string(api.KindTeamDefaults):
			s.defaults = doc
		case "CellConfig":
			var cfg api.CellConfigDoc
			if err = node.Decode(&cfg); err != nil {
				return result, err
			}
			s.configs = append(s.configs, cfg)
		}
		result.Resources = append(result.Resources, kukeonv1.ApplyResourceResult{
			Index: len(result.Resources), Kind: head.Kind, Name: head.Metadata.Name, Action: "created",
		})
	}
	return result, nil
}

// putTeam stores a Team document as kukeond would.
func (s *store) putTeam(t *testing.T, team *config.Team) {
	t.Helper()
	raw, err := yaml.Marshal(team)
	if err != nil {
		t.Fatal(err)
	}
	s.teams[team.Metadata.Name] = raw
}

// putDefaults stores a TeamDefaults document as kukeond would.
func (s *store) putDefaults(t *testing.T, defaults *config.TeamDefaults) {
	t.Helper()
	raw, err := yaml.Marshal(defaults)
	if err != nil {
		t.Fatal(err)
	}
	s.defaults = raw
}

// storedDefaults decodes the stored TeamDefaults.
func (s *store) storedDefaults(t *testing.T) *config.TeamDefaults {
	t.Helper()
	got, err := config.DecodeDefaults(bytes.NewReader(s.defaults))
	if err != nil {
		t.Fatalf("stored TeamDefaults: %v\n%s", err, s.defaults)
	}
	return got
}

// kukeonTree is this repository, the kukeon source the kuke team and the
// initial TeamDefaults read.
func kukeonTree() map[string]fs.FS { return map[string]fs.FS{"kukeon": os.DirFS("../../..")} }

// harness runs `kuke team` against a store, with home as the operator's
// home and connect recording the connection options.
type harness struct {
	home    string
	store   *store
	sources map[string]fs.FS
	connect []initialize.ConnectOptions
	homeErr error
}

func newHarness(t *testing.T) *harness {
	return &harness{home: t.TempDir(), store: newStore(), sources: kukeonTree()}
}

func (h *harness) command() *cobra.Command {
	return cli.NewTeamCommandWith(cli.Deps{
		HomeDir: func() (string, error) { return h.home, h.homeErr },
		Connect: func(_ context.Context, opts initialize.ConnectOptions) (initialize.Client, error) {
			h.connect = append(h.connect, opts)
			return h.store, nil
		},
		Sources: h.sources,
	})
}

// run executes `kuke team args...` and returns stdout and the error.
func (h *harness) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := h.command()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	if stderr.Len() != 0 {
		t.Fatalf("CLI must leave error reporting to the entrypoint: %q", stderr.String())
	}
	return out.String(), err
}

// writeFile writes content under the test's temp dir and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := t.TempDir() + "/" + name
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
