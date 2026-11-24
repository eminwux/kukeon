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
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

func setupDirectoryAdd(t *testing.T) (AddOptions, string) {
	t.Helper()
	opts, commit := setupAdd(t)
	opts.Directory = os.Getenv("ADD_FIXTURE_REPO")
	fixtureGit(t, opts.Directory, "remote", "add", "origin", opts.Source)
	opts.Source, opts.Name = "", ""
	return opts, commit
}

func TestDirectoryAddDiscoveryAndRepeat(t *testing.T) {
	for _, mode := range []string{"branch", "detached", "worktree"} {
		t.Run(mode, func(t *testing.T) {
			opts, commit := setupDirectoryAdd(t)
			if mode == "detached" {
				fixtureGit(t, opts.Directory, "checkout", "--detach", commit)
			}
			if mode == "worktree" {
				dir := filepath.Join(t.TempDir(), "worktree")
				fixtureGit(t, opts.Directory, "worktree", "add", "--detach", dir, commit)
				opts.Directory = dir // .git is an indirection file.
			}
			expectedName := filepath.Base(opts.Directory)
			before := fixtureGit(t, opts.Directory, "status", "--porcelain")
			configBefore, configErr := os.ReadFile(filepath.Join(opts.Directory, ".git", "config"))
			if mode != "worktree" && configErr != nil {
				t.Fatal(configErr)
			}
			for attempt := range 2 {
				opts.Force = attempt == 1
				out, err := Add(t.Context(), opts)
				if err != nil {
					t.Fatal(err)
				}
				// One blueprint, one config, the Team and, the first time,
				// the project's Source (#1643).
				if out.Result.Resources != 4-attempt || out.Result.Team != expectedName {
					t.Fatalf("result=%+v", out.Result)
				}
				assertDirectoryEntry(t, opts, expectedName, mode, commit)
			}
			if after := fixtureGit(t, opts.Directory, "status", "--porcelain"); after != before {
				t.Fatal("checkout changed")
			}
			if mode != "worktree" {
				after, err := os.ReadFile(filepath.Join(opts.Directory, ".git", "config"))
				if err != nil || !bytes.Equal(configBefore, after) {
					t.Fatal("Git config changed")
				}
			}
			if opts.Client.(*fakeClient).applies != 2 {
				t.Fatal("repeat registration did not converge")
			}
		})
	}
}

func assertDirectoryEntry(t *testing.T, opts AddOptions, expectedName, mode, commit string) {
	t.Helper()
	accepted := opts
	accepted.Name = expectedName
	raw, team := readAdded(t, accepted)
	if team.Spec.Sources != nil || team.Spec.Roles != nil || team.Spec.Harnesses != nil {
		t.Fatal("inheritance frozen")
	}
	source := team.Spec.ProjectSource
	f := opts.Client.(*fakeClient)
	if projectURL(t, f, team) != "git@work-alias:owner/project.git" || source.URL != "" ||
		source.ClonePath != "/home/user/"+expectedName {
		t.Fatalf("source=%+v", source)
	}
	if mode == "branch" && (source.Branch != "trunk" || source.Commit != "") {
		t.Fatal("local branch lost")
	}
	if mode != "branch" && (source.Commit != commit || source.Branch != "") {
		t.Fatal("detached commit lost")
	}
	// A stored team is self-contained; Resolve never consults the directory.
	effective, err := resolveStored(t, f, team)
	if err != nil ||
		!slices.ContainsFunc(effective.Sources, func(s config.Source) bool { return s.Name == "kukeon" }) ||
		!slices.ContainsFunc(effective.Sources, func(s config.Source) bool { return s.Name == "project" }) {
		t.Fatalf("inherited definitions lost: %v", err)
	}
	if bytes.Contains(raw, []byte(opts.Directory)) {
		t.Fatal("stored team depends on the input path")
	}
}

func TestDirectoryDeclarationPrecedence(t *testing.T) {
	for _, mode := range []string{"local-name", "flag-name", "basename", "local-source", "source-override", "revision-override"} {
		t.Run(mode, func(t *testing.T) {
			opts, commit := setupDirectoryAdd(t)
			team := &config.Team{
				APIVersion: config.APIVersion,
				Kind:       config.KindTeam,
				Metadata:   config.Metadata{Name: "authored"},
			}
			expectedName := "authored"
			switch mode {
			case "flag-name":
				opts.Name, expectedName = "chosen", "chosen"
			case "basename":
				team.Metadata.Name, expectedName = "", filepath.Base(opts.Directory)
			case "local-source":
				team.Spec.ProjectSource = &config.Source{
					Name:      "project",
					URL:       "git@local-alias:owner/repo",
					Tag:       "v1",
					ClonePath: "/workspace/code",
				}
				fixtureGit(t, opts.Directory, "remote", "remove", "origin")
			case "source-override":
				team.Spec.ProjectSource = &config.Source{Name: "project", URL: "https://invalid.test/repo", Tag: "v1"}
				opts.Source = "git@override-alias:owner/repo"
			case "revision-override":
				team.Spec.ProjectSource = &config.Source{Name: "project", Tag: "missing-tag"}
				opts.Commit = commit
			}
			raw, err := yaml.Marshal(team)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(opts.Directory, "kuketeam.yaml")
			writeFixture(t, path, string(raw))
			out, err := Add(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if out.Result.Team != expectedName || out.Result.Resources != 4 {
				t.Fatalf("result = %+v", out.Result)
			}
			if out.Plan.Origin.Kind != OriginDirectory || !strings.HasSuffix(out.Plan.Origin.Detail, "kuketeam.yaml") {
				t.Fatalf("origin = %+v", out.Plan.Origin)
			}
			accepted := opts
			accepted.Name = expectedName
			_, entry := readAdded(t, accepted)
			project := entry.Spec.ProjectSource
			url := projectURL(t, opts.Client.(*fakeClient), entry)
			if mode == "local-source" &&
				(url != team.Spec.ProjectSource.URL || project.Tag != "v1" || project.ClonePath != "/workspace/code") {
				t.Fatal("authored source overwritten")
			}
			if mode == "source-override" && (url != opts.Source || project.Tag != "v1") {
				t.Fatal("source override lost local revision")
			}
			if mode == "revision-override" && (project.Commit != commit || project.Tag != "") {
				t.Fatal("revision override did not replace selector")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("authored file modified")
			}
		})
	}
}

func TestDirectoryInvalidInputsBeforeApplication(t *testing.T) {
	for _, mode := range []string{"missing-origin", "no-git", "unborn", "invalid-name", "reserved-name", "malformed", "unknown-field", "wrong-kind", "multiple-docs", "empty-file", "unreadable", "dangling-file", "collision", "bad-reference", "two-revisions", "bad-origin"} {
		t.Run(mode, func(t *testing.T) {
			opts, _ := setupDirectoryAdd(t)
			opts.Name = "chosen"
			// A stored team must survive all subsequent input/preflight errors.
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			before, _ := readAdded(t, opts)
			fake := opts.Client.(*fakeClient)
			writes := len(fake.writes)
			opts.Force = true
			path := filepath.Join(opts.Directory, "kuketeam.yaml")
			prefix := "apiVersion: v1beta1\nkind: Team\n"
			switch mode {
			case "missing-origin":
				fixtureGit(t, opts.Directory, "remote", "remove", "origin")
			case "bad-origin":
				fixtureGit(t, opts.Directory, "remote", "set-url", "origin", "https://unsafe-canary@host/repo")
			case "no-git":
				opts.Directory = t.TempDir()
			case "unborn":
				opts.Directory = t.TempDir()
				fixtureGit(t, opts.Directory, "init", "--initial-branch=trunk")
				fixtureGit(t, opts.Directory, "remote", "add", "origin", "git@work-alias:owner/project.git")
			case "invalid-name":
				opts.Name = "Invalid_Name"
			case "reserved-name":
				opts.Name = "root"
			case "malformed":
				writeFixture(t, path, "[bad yaml")
			case "unknown-field":
				writeFixture(t, path, prefix+"unknown: true\n")
			case "wrong-kind":
				writeFixture(t, path, "apiVersion: v1beta1\nkind: ProjectTeam\n")
			case "multiple-docs":
				writeFixture(t, path, prefix+"---\n"+prefix)
			case "empty-file":
				writeFixture(t, path, "")
			case "dangling-file":
				if err := os.Symlink("missing-input", path); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "collision":
				writeFixture(
					t,
					path,
					prefix+"spec:\n  sources:\n    - {name: project, url: 'https://example.test/repo', branch: main}\n",
				)
			case "bad-reference":
				writeFixture(
					t,
					path,
					prefix+"spec:\n  roles:\n    - {name: missing, source: nonexistent, path: roles/missing}\n",
				)
			case "two-revisions":
				writeFixture(t, path, prefix+"spec:\n  projectSource: {name: project, branch: trunk, tag: v1}\n")
			}
			_, err := Add(t.Context(), opts)
			if err == nil || strings.Contains(err.Error(), "unsafe-canary") {
				t.Fatalf("invalid input accepted/leaked: %v", err)
			}
			accepted := opts
			accepted.Name = "chosen"
			accepted.Client = fake
			after, _ := readAdded(t, accepted)
			if !bytes.Equal(before, after) || len(fake.writes) != writes {
				t.Fatal("invalid input mutated accepted state")
			}
		})
	}
}

func TestDirectoryExplicitInputsWithoutGit(t *testing.T) {
	opts, _ := setupAdd(t)
	opts.Directory, opts.Branch = t.TempDir(), "trunk"
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	opts.Force = true
	opts.Source = "" // Revision alone must not invent a remote.
	if _, err := Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Fatalf("error=%v", err)
	}
}

func TestDirectoryPartialApplyPreservesTheStoredTeam(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	opts.Name = "chosen"
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	before, _ := readAdded(t, opts)
	fake := opts.Client.(*fakeClient)
	configureAddFailure(t, &opts, fake, "partial")
	opts.Force = true
	_, err := Add(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "rerun kuke create team <directory> --name chosen") ||
		!strings.Contains(err.Error(), "team not stored") {
		t.Fatalf("error=%v", err)
	}
	after, _ := readAdded(t, opts)
	if !bytes.Equal(before, after) {
		t.Fatal("partial application replaced the stored team")
	}
}

// TestDirectoryExplicitEmptyLists: an authored [] survives into the plan and
// selects nothing, which AC11 explains.
func TestDirectoryExplicitEmptyLists(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	writeFixture(
		t,
		filepath.Join(opts.Directory, "kuketeam.yaml"),
		"apiVersion: v1beta1\nkind: Team\nspec: {sources: [], roles: [], harnesses: []}\n",
	)
	_, err := Add(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "selects no role × harness pair") ||
		!strings.Contains(err.Error(), "--role") {
		t.Fatalf("empty selection ignored: %v", err)
	}
	if f := opts.Client.(*fakeClient); len(f.writes) != 0 {
		t.Fatalf("writes = %v", f.writes)
	}
}

func TestDirectoryPersistsAuthoredSelectionsWithoutInput(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	opts.Name = "chosen"
	input := "apiVersion: v1beta1\nkind: Team\nspec:\n  roles:\n    - {name: worker, source: kukeon, path: team/roles/majordomo, harness: codex}\n"
	path := filepath.Join(opts.Directory, "kuketeam.yaml")
	writeFixture(t, path, input)
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	_, team := readAdded(t, opts)
	if len(team.Spec.Roles) != 1 || team.Spec.Roles[0].Name != "worker" || team.Spec.Sources != nil ||
		team.Spec.Harnesses != nil {
		t.Fatal("authored list replacement or omissions lost")
	}
	// Recomposition uses only the stored Team and the current defaults.
	if err := os.RemoveAll(opts.Directory); err != nil {
		t.Fatal(err)
	}
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) { cfg.Spec.Defaults.Roles[0].Name = "new-global-role" })
	out, err := ApplyStored(t.Context(), opts.Options, "chosen", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.Plan.Origin.Kind != OriginStored || out.Plan.Pairs[0].Role != "worker" {
		t.Fatalf("plan = %+v", out.Plan)
	}
	_, saved := readAdded(t, opts)
	if saved.Spec.Roles[0].Name != "worker" || saved.Spec.Harnesses != nil {
		t.Fatal("recomposition flattened inheritance or replaced authored list")
	}
}

func TestDirectoryNamePrecedence(t *testing.T) {
	for _, tc := range []struct{ directory, local, flag, want string }{
		{"project", "", "", "project"},
		{"Invalid_Name", "", "", ""},
		{"Invalid_Name", "authored", "", "authored"},
		{"project", "Invalid_Local", "chosen", "chosen"},
		{"project", "Invalid_Local", "", ""},
		{"project", "authored", "root", ""},
	} {
		t.Run(tc.directory+"/"+tc.local+"/"+tc.flag, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), tc.directory)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if tc.local != "" {
				writeFixture(
					t,
					filepath.Join(dir, "kuketeam.yaml"),
					"apiVersion: v1beta1\nkind: Team\nmetadata: {name: "+tc.local+"}\n",
				)
			}
			fake := newFake()
			storeDefaults(t, fake, addDefaults())
			// An injected project tree: nothing is fetched.
			sources := sourceFS()
			sources["project"] = fstest.MapFS{}
			out, err := Add(t.Context(), AddOptions{
				Options:   Options{Client: fake, Sources: sources, DryRun: true},
				Directory: dir, Name: tc.flag, Source: "git@host:owner/project", Branch: "main",
			})
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "--name") {
					t.Fatalf("invalid name needs guidance: %v", err)
				}
				return
			}
			if err != nil || out.Plan.Team.Metadata.Name != tc.want {
				t.Fatalf("name precedence: plan=%+v err=%v", out.Plan, err)
			}
		})
	}
}

func TestDirectoryLocalSSHConfiguration(t *testing.T) {
	for _, mode := range []string{"ssh-command", "url-rewrite", "linked-worktree"} {
		t.Run(mode, func(t *testing.T) {
			opts, _ := setupDirectoryAdd(t)
			opts.Name = "private-project"
			fixtureGit(t, opts.Directory, "config", "--local", "core.sshCommand", os.Getenv("GIT_SSH_COMMAND"))
			t.Setenv("GIT_SSH_COMMAND", "")
			t.Setenv("GIT_SSH", "/nonexistent/ssh")
			if mode == "url-rewrite" {
				fixtureGit(
					t,
					opts.Directory,
					"config",
					"--local",
					"url.git@work-alias:.insteadOf",
					"https://private.invalid/",
				)
				fixtureGit(
					t,
					opts.Directory,
					"remote",
					"set-url",
					"origin",
					"https://private.invalid/owner/project.git",
				)
			}
			if mode == "linked-worktree" {
				dir := filepath.Join(t.TempDir(), "worktree")
				fixtureGit(t, opts.Directory, "worktree", "add", "--detach", dir)
				fixtureGit(t, opts.Directory, "config", "extensions.worktreeConfig", "true")
				command := fixtureGit(t, opts.Directory, "config", "--get", "core.sshCommand")
				fixtureGit(t, opts.Directory, "config", "--unset", "core.sshCommand")
				fixtureGit(t, dir, "config", "--worktree", "core.sshCommand", command)
				opts.Directory = dir
			}
			// Discover the origin through Git and then perform real object transfer.
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			if opts.Client.(*fakeClient).applies != 1 {
				t.Fatal("private source did not reach composition and application")
			}
		})
	}
}

// TestAddRejectsReservedNames: root and kuke (the init team) are refused as a
// positional name, as --name, and as an authored metadata.name, before any
// daemon call.
func TestAddRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"root", "kuke"} {
		for _, form := range []string{"positional", "flag", "metadata"} {
			t.Run(name+"/"+form, func(t *testing.T) {
				opts, _ := setupDirectoryAdd(t)
				fake := opts.Client.(*fakeClient)
				switch form {
				case "positional":
					opts.Directory, opts.Name, opts.Source = "", name, "git@work-alias:owner/project.git"
				case "flag":
					opts.Name = name
				case "metadata":
					writeFixture(t, filepath.Join(opts.Directory, "kuketeam.yaml"),
						"apiVersion: v1beta1\nkind: Team\nmetadata: {name: "+name+"}\n")
				}
				_, err := Add(t.Context(), opts)
				if err == nil || !strings.Contains(err.Error(), `"`+name+`" is reserved`) {
					t.Fatalf("reserved name %s accepted or unexplained: %v", name, err)
				}
				if len(fake.writes) != 0 || len(fake.teams) != 0 {
					t.Fatal("reserved name reached the daemon")
				}
			})
		}
	}
}

// TestAddKukeonProjectKeepsTheInitTeam is the #1527 collision: with the init
// team applied, adding kukeon's own checkout (metadata.name: kukeon) stores
// a separate team and leaves the kuke Team and its configs alone.
func TestAddKukeonProjectKeepsTheInitTeam(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	fake := opts.Client.(*fakeClient)
	fake.prune = true
	if _, err := Init(t.Context(), InitOptions{Options: opts.Options}); err != nil {
		t.Fatal(err)
	}
	kukeBefore := slices.Clone(fake.teams["kuke"])
	majordomo := map[string]bool{}
	for _, cfg := range fake.configs {
		if cfg.Metadata.Labels[api.LabelTeam] == "kuke" {
			majordomo[cfg.Metadata.Name] = true
		}
	}
	if !majordomo["kuke-majordomo-codex"] || !majordomo["kuke-majordomo-claude"] {
		t.Fatalf("init did not apply the majordomo configs: %v", majordomo)
	}

	// The majordomo role reads the kukeon Source, which is the project of
	// team kukeon: the team clones it beside the project.
	writeFixture(t, filepath.Join(opts.Directory, "kuketeam.yaml"),
		"apiVersion: v1beta1\nkind: Team\nmetadata: {name: kukeon}\n"+
			"spec:\n  sources: [{name: kukeon, clonePath: /home/user/kukeon-src}]\n")
	// The checkout is kukeon's: its project is the kukeon Source init stored
	// (#1643), at the checkout's revision.
	opts.Source = "git@github.com:eminwux/kukeon.git"
	sources := maps.Clone(fake.sources)
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.team != "kukeon" || out.Result.Team != "kukeon" || fake.teams["kukeon"] == nil {
		t.Fatalf("team=%q result=%+v", fake.team, out.Result)
	}
	if !bytes.Equal(kukeBefore, fake.teams["kuke"]) {
		t.Fatal("the kuke Team changed by add")
	}
	if !maps.EqualFunc(sources, fake.sources, bytes.Equal) {
		t.Fatal("the add of kukeon's checkout changed the stored Sources")
	}
	kept := map[string]bool{}
	for _, cfg := range fake.configs {
		if cfg.Metadata.Labels[api.LabelTeam] == "kuke" {
			kept[cfg.Metadata.Name] = true
		}
	}
	for name := range majordomo {
		if !kept[name] {
			t.Fatalf("add pruned init config %s", name)
		}
	}
	if len(fake.deleted) != 0 {
		t.Fatalf("add deleted %v", fake.deleted)
	}
}
