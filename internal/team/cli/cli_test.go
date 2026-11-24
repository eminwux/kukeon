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
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/cli"
	"github.com/spf13/cobra"
)

func newTeam(out, stderr *bytes.Buffer) *cobra.Command {
	cmd := cli.NewTeamCommand()
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	cmd.SilenceErrors = true
	return cmd
}

// newKuke is `kuke` with this package's verbs: team, create team, create
// source and source.
func newKuke(out, stderr *bytes.Buffer) *cobra.Command {
	root := &cobra.Command{Use: "kuke", SilenceUsage: true}
	create := &cobra.Command{Use: "create"}
	create.AddCommand(cli.NewCreateTeamCommand(), cli.NewCreateSourceCommand())
	root.AddCommand(newTeam(out, stderr), create, cli.NewSourceCommand())
	root.SetOut(out)
	root.SetErr(stderr)
	root.SilenceErrors = true
	return root
}

func TestCommands(t *testing.T) {
	for _, tc := range []struct {
		name          string
		args          []string
		want, wantErr string
	}{
		{"root", nil, "Usage:", ""},
		{"help flag", []string{"--help"}, "Usage:", ""},
		{"short help", []string{"-h"}, "Usage:", ""},
		{"unknown flag", []string{"--unknown"}, "", "unknown flag"},
		{"root argument", []string{"extra"}, "", "unknown command"},
		{"init help", []string{"init", "--help"}, "No cells are created or started", ""},
		{"init build unsupported", []string{"init", "--build"}, "", "unknown flag"},
		{"init argument", []string{"init", "extra"}, "", "unknown command"},
		{"no add", []string{"add", "x"}, "", "unknown command"},
		{"no source", []string{"source", "list"}, "", "unknown command"},
		{"create team help", []string{"create", "team", "--help"}, "team NAME --source ADDRESS|SOURCE | DIRECTORY", ""},
		{"create team requires argument", []string{"create", "team"}, "", "accepts 1 arg(s)"},
		{"create team exclusive revisions", []string{"create", "team", "x", "--source", "s", "--tag", "t", "--commit", "c"}, "", "none of the others can be"},
		{"apply help", []string{"apply", "--help"}, "apply NAME | -f FILE", ""},
		{"export help", []string{"export", "--help"}, "kuketeam.yaml", ""},
		{"defaults help", []string{"defaults", "--help"}, "kuke apply -f", ""},
		{"create source help", []string{"create", "source", "--help"}, "kind: Source", ""},
		{"create source requires url", []string{"create", "source", "x"}, "", `"url" not set`},
		{"create source has no clone path", []string{"create", "source", "x", "--url", "u", "--clone-path", "/p"}, "", "unknown flag"},
		{"source show requires argument", []string{"source", "show"}, "", "accepts 1 arg(s)"},
		{"no placeholder update", []string{"update"}, "", "unknown command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			cmd := newKuke(&out, &stderr)
			args := tc.args
			if len(args) == 0 || (args[0] != "create" && args[0] != "source") {
				args = append([]string{"team"}, args...)
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want %q", err, tc.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.want) {
				t.Fatalf("stdout = %q, want %q", out.String(), tc.want)
			}
			if tc.wantErr != "" && out.Len() != 0 {
				t.Fatalf("unexpected stdout: %q", out.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("CLI must leave error reporting to entrypoint: %q", stderr.String())
			}
		})
	}
}

func TestHelpIsDeterministic(t *testing.T) {
	var first, second bytes.Buffer
	for _, out := range []*bytes.Buffer{&first, &second} {
		cmd := newTeam(out, out)
		cmd.SetArgs([]string{"--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if first.String() != second.String() {
		t.Fatal("help changed between invocations")
	}
}

// The kuke root's PersistentPreRunE loads kuke's runtime configuration; the
// team subtree must not inherit it.
func TestTeamOverridesRootPreRun(t *testing.T) {
	ran := false
	root := &cobra.Command{
		Use:               "kuke",
		PersistentPreRunE: func(*cobra.Command, []string) error { ran = true; return errors.New("root pre-run") },
	}
	var out, stderr bytes.Buffer
	team := newTeam(&out, &stderr)
	root.AddCommand(team)
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SilenceErrors = true
	root.SetArgs([]string{"team", "init", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	root.SetArgs([]string{"team"})
	if err := root.Execute(); err != nil || ran {
		t.Fatalf("error = %v, root pre-run ran = %v", err, ran)
	}
}
