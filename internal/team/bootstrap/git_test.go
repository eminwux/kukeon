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
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func isolateGit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Fatal("Git is required for subprocess integration tests:", err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "global.gitconfig"))
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	return filepath.Join(home, "global.gitconfig")
}

func TestCommandGitBooleanConversion(t *testing.T) {
	global := isolateGit(t)
	for _, tc := range []struct{ spelling, want string }{
		{"yes", "true"},
		{"on", "true"},
		{"1", "true"},
		{"TRUE", "true"},
		{"-2", "true"},
		{"no", "false"},
		{"off", "false"},
		{"0", "false"},
		{"false", "false"},
		{"", "false"},
	} {
		t.Run(tc.spelling, func(t *testing.T) {
			writeFile(t, global, "[commit]\n gpgsign = "+tc.spelling+"\n")
			value, err := (CommandGit{}).Lookup(t.Context(), "commit.gpgsign", true)
			if err != nil || value != tc.want {
				t.Fatalf("value=%q error=%v, want %s", value, err, tc.want)
			}
		})
	}
	writeFile(t, global, "[commit]\n gpgsign\n")
	value, err := (CommandGit{}).Lookup(t.Context(), "commit.gpgsign", true)
	if err != nil || value != "true" {
		t.Fatalf("implicit boolean: value=%q error=%v", value, err)
	}
}

func TestCommandGitErrorClassification(t *testing.T) {
	global := isolateGit(t)
	reader := CommandGit{}
	if _, err := reader.Lookup(t.Context(), "user.name", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("absent file: %v", err)
	}
	writeFile(t, global, "[user]\n name = Operator\n")
	if _, err := reader.Lookup(t.Context(), "user.email", false); !errors.Is(err, ErrMissing) {
		t.Fatalf("absent key: %v", err)
	}
	for _, content := range []string{"[invalid secret-content", "[commit]\n gpgsign = secret-content\n"} {
		writeFile(t, global, content)
		_, err := reader.Lookup(t.Context(), "commit.gpgsign", true)
		if err == nil || errors.Is(err, ErrMissing) || errors.Is(err, ErrUnavailable) ||
			strings.Contains(err.Error(), "secret-content") {
			t.Fatalf("error = %v", err)
		}
		root := filepath.Join(t.TempDir(), ".kuke")
		_, err = Run(t.Context(), Options{Root: root, Git: reader})
		if err == nil || !strings.Contains(err.Error(), "Git key") {
			t.Fatalf("bootstrap error = %v", err)
		}
		if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("published on Git failure: %v", statErr)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := reader.Lookup(ctx, "user.name", false); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %v", err)
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := reader.Lookup(t.Context(), "user.name", false); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable: %v", err)
	}
}

func TestCommandGitUsesGlobalOnly(t *testing.T) {
	global := isolateGit(t)
	writeFile(
		t,
		global,
		"[user]\n name = Global\n email = global@example.test\n[commit]\n gpgsign = on\n[tag]\n gpgsign = yes\n[user]\n signingkey = ~/key.pub\n",
	)
	repo := t.TempDir()
	command := exec.CommandContext(t.Context(), "git", "init", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	writeFile(t, filepath.Join(repo, ".git", "config"), "[user]\n name = Local\n email = local@example.test\n")
	t.Chdir(repo)
	result, err := Run(t.Context(), Options{Root: filepath.Join(t.TempDir(), ".kuke")})
	if err != nil {
		t.Fatal(err)
	}
	git := result.Config.Spec.Git
	if git.Author.Name != "Global" || git.Committer.Email != "global@example.test" ||
		strings.Join(git.Sign, ",") != "commits,tags" {
		t.Fatalf("global settings not used: %+v", git)
	}
}
