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

package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/initialize"
)

func TestAddCLIForwardsOptions(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[failure], func(t *testing.T) {
			home := t.TempDir()
			sentinel := errors.New("fixture failure")
			cmd := newAddCommand(
				func() (string, error) { return home, nil },
				func(ctx context.Context, opts initialize.AddOptions) (initialize.Result, error) {
					if ctx != t.Context() || opts.Name != "kukeon" ||
						opts.Source != "git@github.com:eminwux/kukeon.git" ||
						opts.Tag != "v1" ||
						opts.Home != home ||
						opts.Root != filepath.Join(home, ".kuke") ||
						opts.Host != "unix:///chosen.sock" ||
						opts.Configuration != "/chosen/kuke.yaml" {
						t.Fatal("options lost")
					}
					if failure {
						return initialize.Result{}, sentinel
					}
					return initialize.Result{Resources: 2, EntryPath: "/fixture/kukeon.yaml"}, nil
				},
			)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs(
				[]string{
					"kukeon",
					"--source",
					"git@github.com:eminwux/kukeon.git",
					"--tag",
					"v1",
					"--host",
					"unix:///chosen.sock",
					"--configuration",
					"/chosen/kuke.yaml",
				},
			)
			err := cmd.ExecuteContext(t.Context())
			if failure {
				if !errors.Is(err, sentinel) || out.Len() != 0 {
					t.Fatal("bad failure output")
				}
				return
			}
			if err != nil || !strings.Contains(out.String(), "registered team kukeon") {
				t.Fatalf("output=%s error=%v", &out, err)
			}
		})
	}
}

func TestAddCLIRejectsInvalidArgumentsWithoutEffects(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"kukeon"}, {".", "--source", ""}, {".", "--name", ""}, {"/nonexistent/directory"}, {"kukeon", "--source", "https://example.test/repo", "--branch", ""}, {"kukeon", "extra", "--source", "https://example.test/repo"}, {"kukeon", "--source", "https://example.test/repo", "--branch", "trunk", "--tag", "v1"}, {"kukeon", "--source", "https://example.test/repo", "--build"}} {
		cmd := newAddCommand(
			func() (string, error) { t.Fatal("resolved home before validation"); return "", nil },
			func(context.Context, initialize.AddOptions) (initialize.Result, error) {
				t.Fatal("add ran")
				return initialize.Result{}, nil
			},
		)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		err := cmd.Execute()
		if args[0] == "--help" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted invalid args")
		}
	}
}

func TestAddCLIDirectoryOptionsAndOutput(t *testing.T) {
	for _, named := range []bool{false, true} {
		t.Run(map[bool]string{false: "inferred", true: "overridden"}[named], func(t *testing.T) {
			directory := t.TempDir()
			cmd := newAddCommand(func() (string, error) { return "/home/operator", nil },
				func(_ context.Context, opts initialize.AddOptions) (initialize.Result, error) {
					if opts.Directory != directory || opts.Source != "git@host:owner/project" || opts.Tag != "v1" {
						t.Fatalf("options=%+v", opts)
					}
					expected := ""
					if named {
						expected = "chosen"
					}
					if opts.Name != expected {
						t.Fatalf("name=%q", opts.Name)
					}
					return initialize.Result{
						Resources: 2,
						EntryPath: "/home/operator/.kuke/kuketeam.d/resolved.yaml",
					}, nil
				})
			args := []string{directory, "--source", "git@host:owner/project", "--tag", "v1"}
			if named {
				args = append(args, "--name", "chosen")
			}
			cmd.SetArgs(args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "registered team resolved:") {
				t.Fatalf("output=%s", out.String())
			}
		})
	}
}
