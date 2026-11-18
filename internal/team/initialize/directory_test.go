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
	"os"
	"path/filepath"
	"strings"
	"testing"

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
			for range 2 {
				result, err := Add(t.Context(), opts)
				if err != nil {
					t.Fatal(err)
				}
				if result.Resources != 2 || filepath.Base(result.EntryPath) != expectedName+".yaml" {
					t.Fatalf("result=%+v", result)
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
	raw, entry := readAddEntry(t, accepted)
	if entry.Spec.Sources != nil || entry.Spec.Roles != nil || entry.Spec.Harnesses != nil {
		t.Fatal("inheritance frozen")
	}
	source := entry.ProjectSource
	if source.URL != "git@work-alias:owner/project.git" || source.ClonePath != "/home/user/"+expectedName {
		t.Fatalf("source=%+v", source)
	}
	if mode == "branch" && (source.Branch != "trunk" || source.Commit != "") {
		t.Fatal("local branch lost")
	}
	if mode != "branch" && (source.Commit != commit || source.Branch != "") {
		t.Fatal("detached commit lost")
	}
	// A saved entry is self-contained; Resolve never consults the directory.
	cfgFile, err := os.Open(filepath.Join(opts.Root, "kuketeams.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Decode(cfgFile)
	_ = cfgFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := config.Resolve(cfg, entry.Team())
	if err != nil || len(effective.Sources) != 2 {
		t.Fatalf("inherited definitions lost: %v", err)
	}
	if bytes.Contains(raw, []byte(opts.Directory)) {
		t.Fatal("entry depends on input path")
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
				team.ProjectSource = &config.Source{
					Name:      "project",
					URL:       "git@local-alias:owner/repo",
					Tag:       "v1",
					ClonePath: "/workspace/code",
				}
				fixtureGit(t, opts.Directory, "remote", "remove", "origin")
			case "source-override":
				team.ProjectSource = &config.Source{Name: "project", URL: "https://invalid.test/repo", Tag: "v1"}
				opts.Source = "git@override-alias:owner/repo"
			case "revision-override":
				team.ProjectSource = &config.Source{Name: "project", Tag: "missing-tag"}
				opts.Commit = commit
			}
			raw, err := yaml.Marshal(team)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(opts.Directory, "kuketeam.yaml")
			writeFixture(t, path, string(raw))
			declaration, err := directoryTeam(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if declaration.Metadata.Name != expectedName {
				t.Fatalf("name=%s", declaration.Metadata.Name)
			}
			result, err := Add(t.Context(), opts)
			if err != nil {
				t.Fatal(err)
			}
			accepted := opts
			accepted.Name = expectedName
			_, entry := readAddEntry(t, accepted)
			if result.Resources != 2 {
				t.Fatal("missing application")
			}
			if mode == "local-source" &&
				(entry.ProjectSource.URL != team.ProjectSource.URL || entry.ProjectSource.Tag != "v1" || entry.ProjectSource.ClonePath != "/workspace/code") {
				t.Fatal("authored source overwritten")
			}
			if mode == "source-override" &&
				(entry.ProjectSource.URL != opts.Source || entry.ProjectSource.Tag != "v1") {
				t.Fatal("source override lost local revision")
			}
			if mode == "revision-override" && (entry.ProjectSource.Commit != commit || entry.ProjectSource.Tag != "") {
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
			// A successful registration must survive all subsequent input/preflight errors.
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			before, _ := readAddEntry(t, opts)
			fake := newFake()
			opts.Client = fake
			path := filepath.Join(opts.Directory, "kuketeam.yaml")
			prefix := "apiVersion: kuketeams.io/v1\nkind: Team\n"
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
				writeFixture(t, path, "apiVersion: kuketeams.io/v1\nkind: ProjectTeam\n")
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
				writeFixture(t, path, prefix+"projectSource: {name: project, branch: trunk, tag: v1}\n")
			}
			_, err := Add(t.Context(), opts)
			if err == nil || strings.Contains(err.Error(), "unsafe-canary") {
				t.Fatalf("invalid input accepted/leaked: %v", err)
			}
			accepted := opts
			accepted.Name = "chosen"
			after, _ := readAddEntry(t, accepted)
			if !bytes.Equal(before, after) || fake.applies != 0 || fake.creates != 0 {
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
	opts.Source = "" // Revision alone must not invent a remote.
	if _, err := Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "--source") {
		t.Fatalf("error=%v", err)
	}
}

func TestDirectoryPartialApplyPreservesRegistration(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	opts.Name = "chosen"
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	before, _ := readAddEntry(t, opts)
	fake := newFake()
	configureAddFailure(t, &opts, fake, "partial")
	opts.Client = fake
	_, err := Add(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "rerun kuke team add <directory> --name chosen") {
		t.Fatalf("error=%v", err)
	}
	after, _ := readAddEntry(t, opts)
	if !bytes.Equal(before, after) {
		t.Fatal("partial application replaced entry")
	}
}

func TestDirectoryExplicitEmptyLists(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	writeFixture(
		t,
		filepath.Join(opts.Directory, "kuketeam.yaml"),
		"apiVersion: kuketeams.io/v1\nkind: Team\nspec: {sources: [], roles: [], harnesses: []}\n",
	)
	declaration, err := directoryTeam(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if declaration.Spec.Sources == nil || declaration.Spec.Roles == nil || declaration.Spec.Harnesses == nil {
		t.Fatal("explicit empty lists erased")
	}
	entry, err := config.Entry(declaration)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := yaml.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := config.DecodeEntry(bytes.NewReader(raw))
	if err != nil || saved.Spec.Sources == nil || saved.Spec.Roles == nil || saved.Spec.Harnesses == nil {
		t.Fatal("entry lost empty selections")
	}
	_, err = Add(t.Context(), opts)
	if err == nil || !strings.Contains(err.Error(), "no role/harness pairs") {
		t.Fatalf("empty selection ignored: %v", err)
	}
}

func TestDirectoryPersistsAuthoredSelectionsWithoutInput(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	opts.Name = "chosen"
	input := "apiVersion: kuketeams.io/v1\nkind: Team\nspec:\n  roles:\n    - {name: worker, source: kukeon, path: team/roles/majordomo, harness: codex}\n"
	path := filepath.Join(opts.Directory, "kuketeam.yaml")
	writeFixture(t, path, input)
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	_, entry := readAddEntry(t, opts)
	if len(entry.Spec.Roles) != 1 || entry.Spec.Roles[0].Name != "worker" || entry.Spec.Sources != nil ||
		entry.Spec.Harnesses != nil {
		t.Fatal("authored list replacement or omissions lost")
	}
	// Recomposition uses only the saved TeamEntry and the current global defaults.
	if err := os.RemoveAll(opts.Directory); err != nil {
		t.Fatal(err)
	}
	cfgBytes, err := os.ReadFile(filepath.Join(opts.Root, "kuketeams.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Decode(bytes.NewReader(cfgBytes))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Spec.Defaults.Roles[0].Name = "new-global-role"
	if _, err = runTeam(t.Context(), opts.Options, cfg, entry.Team(), entry.Secrets, Result{}, "saved declaration", nil); err != nil {
		t.Fatal(err)
	}
	_, saved := readAddEntry(t, opts)
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
					"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: "+tc.local+"}\n",
				)
			}
			team, err := directoryTeam(
				t.Context(),
				AddOptions{Directory: dir, Name: tc.flag, Source: "git@host:owner/project", Branch: "main"},
			)
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "--name") {
					t.Fatalf("invalid name needs guidance: %v", err)
				}
				return
			}
			if err != nil || team.Metadata.Name != tc.want {
				t.Fatalf("name precedence: team=%+v err=%v", team, err)
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
				fake := newFake()
				opts.Client = fake
				switch form {
				case "positional":
					opts.Directory, opts.Name, opts.Source = "", name, "git@work-alias:owner/project.git"
				case "flag":
					opts.Name = name
				case "metadata":
					writeFixture(t, filepath.Join(opts.Directory, "kuketeam.yaml"),
						"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: "+name+"}\n")
				}
				_, err := Add(t.Context(), opts)
				if err == nil || !strings.Contains(err.Error(), `"`+name+`" is reserved`) {
					t.Fatalf("reserved name %s accepted or unexplained: %v", name, err)
				}
				if fake.applies != 0 || fake.creates != 0 || len(fake.deleted) != 0 {
					t.Fatal("reserved name reached the daemon")
				}
				entry := filepath.Join(opts.Root, "kuketeam.d", name+".yaml")
				if _, statErr := os.Stat(entry); !os.IsNotExist(statErr) {
					t.Fatalf("reserved name registered: %v", statErr)
				}
			})
		}
	}
}

// TestAddKukeonProjectKeepsTheInitTeam is the #1527 collision: with the init
// team applied, adding kukeon's own checkout (metadata.name: kukeon) registers
// a separate team and leaves kuke.yaml and the kuke-majordomo-* configs alone.
func TestAddKukeonProjectKeepsTheInitTeam(t *testing.T) {
	opts, _ := setupDirectoryAdd(t)
	fake := opts.Client.(*fakeClient)
	fake.prune = true
	globalPath := filepath.Join(opts.Root, "kuketeams.yaml")
	global, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatal(err)
	}
	initOpts := opts.Options
	initOpts.Force = true
	if _, err = Run(t.Context(), initOpts); err != nil {
		t.Fatal(err)
	}
	// Restore the add fixture's defaults; init --force reset them.
	if err = os.WriteFile(globalPath, global, 0o600); err != nil {
		t.Fatal(err)
	}
	kukePath := filepath.Join(opts.Root, "kuketeam.d", "kuke.yaml")
	kukeBefore, err := os.ReadFile(kukePath)
	if err != nil {
		t.Fatal(err)
	}
	majordomo := map[string]bool{}
	for _, cfg := range fake.configs {
		if cfg.Metadata.Labels[api.LabelTeam] == "kuke" {
			majordomo[cfg.Metadata.Name] = true
		}
	}
	if !majordomo["kuke-majordomo-codex"] || !majordomo["kuke-majordomo-claude"] {
		t.Fatalf("init did not apply the majordomo configs: %v", majordomo)
	}

	writeFixture(t, filepath.Join(opts.Directory, "kuketeam.yaml"),
		"apiVersion: kuketeams.io/v1\nkind: Team\nmetadata: {name: kukeon}\n")
	result, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fake.team != "kukeon" || result.EntryPath != filepath.Join(opts.Root, "kuketeam.d", "kukeon.yaml") {
		t.Fatalf("team=%q entry=%s", fake.team, result.EntryPath)
	}
	kukeAfter, err := os.ReadFile(kukePath)
	if err != nil || !bytes.Equal(kukeBefore, kukeAfter) {
		t.Fatalf("init entry changed by add: %v", err)
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
