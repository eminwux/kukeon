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

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/config"
)

type fakeGit struct {
	values map[string]string
	err    error
	calls  int
}

func (g *fakeGit) Lookup(_ context.Context, key string, _ bool) (string, error) {
	g.calls++
	if g.err != nil {
		return "", g.err
	}
	if value, ok := g.values[key]; ok {
		return value, nil
	}
	return "", ErrMissing
}

func TestFirstRun(t *testing.T) {
	root := filepath.Join(t.TempDir(), "operator", ".kuke")
	git := &fakeGit{values: map[string]string{
		"user.name": " Operator ", "user.email": " operator@example.test\n",
		"user.signingkey": " ~/.ssh/operator.pub ", "commit.gpgsign": "true", "tag.gpgsign": "true",
		"gpg.ssh.allowedSignersFile": " ~/allowed_signers ",
	}}
	result, err := Run(t.Context(), Options{Root: root, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Path != filepath.Join(root, "kuketeams.yaml") || len(result.Diagnostics) != 0 {
		t.Fatalf("result = %+v", result)
	}
	want := &config.TeamsConfig{
		APIVersion: "kuketeams.io/v1", Kind: "TeamsConfig",
		Spec: config.Spec{
			Git: &config.Git{
				Author:     &config.Identity{Name: "Operator", Email: "operator@example.test"},
				Committer:  &config.Identity{Name: "Operator", Email: "operator@example.test"},
				SigningKey: "~/.ssh/operator.pub", Sign: []string{"commits", "tags"}, AllowedSigners: "~/allowed_signers",
			},
		},
	}
	if !reflect.DeepEqual(result.Config, want) {
		t.Fatalf("config = %#v, want %#v", result.Config, want)
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := config.Decode(bytes.NewReader(data))
	if err != nil || !reflect.DeepEqual(decoded, want) {
		t.Fatalf("persisted config = %#v, error = %v", decoded, err)
	}
	if result.Config.Spec.Git.Author == result.Config.Spec.Git.Committer {
		t.Fatal("identity pointers are shared")
	}
	result.Config.Spec.Git.Author.Name = "Changed"
	if result.Config.Spec.Git.Committer.Name != "Operator" {
		t.Fatal("committer changed with author")
	}
	checkMode(t, root, 0o700)
	checkMode(t, filepath.Dir(root), 0o700)
	checkMode(t, result.Path, 0o600)
	checkEntries(t, root, "kuketeams.yaml")
}

func TestGitDiscoveryCases(t *testing.T) {
	for _, tc := range []struct {
		name       string
		values     map[string]string
		err        error
		want       *config.Git
		diagnostic string
	}{
		{name: "no settings", diagnostic: "No usable global Git settings"},
		{name: "unavailable", err: ErrUnavailable, diagnostic: "discovery skipped"},
		{name: "partial name", values: map[string]string{"user.name": "Operator"}, diagnostic: "incomplete"},
		{name: "partial email", values: map[string]string{"user.email": "op@example.test", "user.name": " \n"}, diagnostic: "incomplete"},
		{name: "key only", values: map[string]string{"user.signingkey": "ssh-key"}, want: &config.Git{SigningKey: "ssh-key"}},
		{name: "allowed signers only", values: map[string]string{"gpg.ssh.allowedSignersFile": "~/signers"}, want: &config.Git{AllowedSigners: "~/signers"}},
		{name: "signing disabled", values: map[string]string{"user.signingkey": "ssh-key", "commit.gpgsign": "false", "tag.gpgsign": "false"}, want: &config.Git{SigningKey: "ssh-key"}},
		{name: "commits only", values: map[string]string{"user.signingkey": "ssh-key", "commit.gpgsign": "true"}, want: &config.Git{SigningKey: "ssh-key", Sign: []string{"commits"}}},
		{name: "tags only", values: map[string]string{"user.signingkey": "ssh-key", "tag.gpgsign": "true"}, want: &config.Git{SigningKey: "ssh-key", Sign: []string{"tags"}}},
		{name: "missing signing key", values: map[string]string{"commit.gpgsign": "true", "tag.gpgsign": "true"}, diagnostic: "without user.signingkey"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), ".kuke")
			result, err := Run(t.Context(), Options{Root: root, Git: &fakeGit{values: tc.values, err: tc.err}})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Config.Spec.Git, tc.want) {
				t.Fatalf("git = %#v, want %#v", result.Config.Spec.Git, tc.want)
			}
			if !strings.Contains(strings.Join(result.Diagnostics, "\n"), tc.diagnostic) {
				t.Fatalf("diagnostics = %v", result.Diagnostics)
			}
			data, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = config.Decode(bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			if tc.want == nil && bytes.Contains(data, []byte("git:")) {
				t.Fatal("empty Git block persisted")
			}
		})
	}
}

const authored = "# operator authored\napiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  git:\n    author: {name: Author, email: author@example.test}\n    committer: {name: Committer, email: committer@example.test}\n  defaults: {}\n"

func TestExistingPreserved(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "kuketeams.yaml")
	writeFile(t, path, authored)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	git := &fakeGit{err: errors.New("must not be called")}
	result, err := Run(t.Context(), Options{Root: root, Git: git})
	if err != nil {
		t.Fatal(err)
	}
	if result.Created || result.Path != path || git.calls != 0 {
		t.Fatalf("result=%+v calls=%d", result, git.calls)
	}
	if result.Config.Spec.Git.Author.Name != "Author" || result.Config.Spec.Git.Committer.Name != "Committer" {
		t.Fatal("independent existing identities lost")
	}
	checkContents(t, path, authored)
	checkMode(t, path, 0o640)
	checkMode(t, root, before.Mode().Perm())
	checkEntries(t, root, "kuketeams.yaml")
}

func TestExistingFailures(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"malformed", "spec: ["},
		{"empty", ""},
		{"wrong version", "apiVersion: other/v1\nkind: TeamsConfig\nspec: {}\n"},
		{"wrong kind", "apiVersion: kuketeams.io/v1\nkind: Other\nspec: {}\n"},
		{"directory", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "kuketeams.yaml")
			if tc.name == "directory" {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, tc.content)
			}
			git := &fakeGit{}
			_, err := Run(t.Context(), Options{Root: root, Git: git})
			if err == nil || !strings.Contains(err.Error(), path) || git.calls != 0 {
				t.Fatalf("error=%v Git calls=%d", err, git.calls)
			}
			if tc.name != "directory" {
				checkContents(t, path, tc.content)
			}
		})
	}
}

func TestPartialLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(t.Context(), Options{Root: root, Git: &fakeGit{}}); err != nil {
		t.Fatal(err)
	}
	checkMode(t, root, 0o755)
	checkMode(t, filepath.Join(root, "kuketeams.yaml"), 0o600)
}

func TestDiscoveryFailureDoesNotPublish(t *testing.T) {
	for _, tc := range []struct {
		name     string
		git      GitReader
		canceled bool
	}{
		{name: "command error", git: &fakeGit{err: errors.New("failed discovery")}},
		{name: "invalid boolean", git: &fakeGit{values: map[string]string{"commit.gpgsign": "invalid"}}},
		{name: "invalid generated config", git: &fakeGit{values: map[string]string{"user.signingkey": "\xff"}}},
		{name: "canceled", git: &fakeGit{}, canceled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), ".kuke")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.canceled {
				cancel()
			}
			_, err := Run(ctx, Options{Root: root, Git: tc.git})
			if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "kuketeams.yaml")) {
				t.Fatalf("error = %v", err)
			}
			if tc.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("root created: %v", statErr)
			}
		})
	}
}

// fileFailure delegates the real close even when injecting its reported failure.
type fileFailure struct {
	temporaryFile

	phase  string
	err    error
	cancel context.CancelFunc
}

func (f fileFailure) Write(p []byte) (int, error) {
	if f.phase == "write" {
		return 0, f.err
	}
	if f.phase == "short write" {
		return 0, nil
	}
	return f.temporaryFile.Write(p)
}

func (f fileFailure) Sync() error {
	if f.cancel != nil {
		f.cancel()
	}
	if f.phase == "sync" {
		return f.err
	}
	return f.temporaryFile.Sync()
}

func (f fileFailure) Close() error {
	err := f.temporaryFile.Close()
	if f.phase == "close" {
		return f.err
	}
	return err
}

func TestFilesystemFailures(t *testing.T) {
	for _, phase := range []string{"read", "mkdir", "create", "write", "short write", "sync", "close", "link", "cancel before publish"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "kuketeams.yaml")
			files := localFiles()
			sentinel := errors.New("injected filesystem failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch phase {
			case "read":
				writeFile(t, path, authored)
				files.readFile = func(string) ([]byte, error) { return nil, sentinel }
			case "mkdir":
				files.mkdirAll = func(string, os.FileMode) error { return sentinel }
			case "create":
				files.createTemp = func(string, string) (temporaryFile, error) { return nil, sentinel }
			case "link":
				files.link = func(string, string) error { return sentinel }
			default:
				create := files.createTemp
				files.createTemp = func(dir, pattern string) (temporaryFile, error) {
					file, err := create(dir, pattern)
					if err != nil {
						return nil, err
					}
					failure := fileFailure{temporaryFile: file, phase: phase, err: sentinel}
					if phase == "cancel before publish" {
						failure.cancel = cancel
					}
					return failure, nil
				}
			}
			_, err := run(ctx, Options{Root: root, Git: &fakeGit{}}, files)
			wantErr := sentinel
			if phase == "short write" {
				wantErr = io.ErrShortWrite
			}
			if phase == "cancel before publish" {
				wantErr = context.Canceled
			}
			if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			if phase == "read" {
				checkContents(t, path, authored)
				checkEntries(t, root, "kuketeams.yaml")
			} else {
				checkEntries(t, root)
			}
		})
	}
}

func TestCompetingCreator(t *testing.T) {
	for _, content := range []string{authored, "kind: Invalid\n"} {
		t.Run(content, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "kuketeams.yaml")
			files := localFiles()
			files.link = func(old, newPath string) error {
				// The winner appears after discovery, encoding and complete-file writing,
				// immediately before the actual no-overwrite primitive runs.
				writeFile(t, path, content)
				return os.Link(old, newPath)
			}
			result, err := run(t.Context(), Options{Root: root, Git: &fakeGit{}}, files)
			if content == authored {
				if err != nil || result.Created || result.Path != path {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				if result.Config.Spec.Git.Author.Name != "Author" {
					t.Fatal("returned losing candidate")
				}
			} else if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("error = %v", err)
			}
			checkContents(t, path, content)
			checkEntries(t, root, "kuketeams.yaml")
		})
	}
}

func TestRelativeRootRejected(t *testing.T) {
	if _, err := Run(t.Context(), Options{Root: ".kuke", Git: &fakeGit{}}); err == nil {
		t.Fatal("relative root accepted")
	}
}

func writeFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s: mode %o, want %o", path, info.Mode().Perm(), want)
	}
}

func checkContents(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s: contents changed", path)
	}
}

func checkEntries(t *testing.T, root string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
		t.Fatalf("entries = %v, want %v", got, want)
	}
}

// cancelingGit simulates cancellation while a discovery dependency is running.
type cancelingGit struct{ cancel context.CancelFunc }

func (g cancelingGit) Lookup(context.Context, string, bool) (string, error) {
	g.cancel()
	return "", ErrUnavailable
}

func TestCancellationDuringDiscovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), ".kuke")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_, err := Run(ctx, Options{Root: root, Git: cancelingGit{cancel: cancel}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("published after cancellation: %v", statErr)
	}
}

func TestFreshConfigWithoutGitIsMinimal(t *testing.T) {
	result, err := Run(t.Context(), Options{Root: t.TempDir(), Git: &fakeGit{}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec:\n  defaults: {}\n"
	if string(raw) != want {
		t.Fatalf("fresh configuration:\n%s", raw)
	}
}
