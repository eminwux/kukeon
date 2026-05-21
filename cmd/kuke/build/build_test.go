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

package build

import (
	"errors"
	"io"
	"os/exec"
	"reflect"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// withStubs swaps the package-level lookPath / execHandoff indirections for
// the duration of a test and restores them after.
func withStubs(t *testing.T, lp func(string) (string, error), eh func(string, []string, []string) error) {
	t.Helper()
	origLook, origExec := lookPath, execHandoff
	lookPath, execHandoff = lp, eh
	t.Cleanup(func() { lookPath, execHandoff = origLook, origExec })
}

func TestRunBuildKukebuildNotFound(t *testing.T) {
	withStubs(t,
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string, []string, []string) error {
			t.Fatal("execHandoff must not run when kukebuild is absent")
			return nil
		},
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"-t", "demo:latest", "/ctx"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrKukebuildNotFound) {
		t.Fatalf("Execute error = %v, want wraps ErrKukebuildNotFound", err)
	}
}

func TestRunBuildTagRequired(t *testing.T) {
	withStubs(t,
		func(string) (string, error) { t.Fatal("lookPath must not run without a tag"); return "", nil },
		func(string, []string, []string) error { t.Fatal("execHandoff must not run without a tag"); return nil },
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"/ctx"})

	err := cmd.Execute()
	if !errors.Is(err, errdefs.ErrImageTagRequired) {
		t.Fatalf("Execute error = %v, want wraps ErrImageTagRequired", err)
	}
}

func TestRunBuildExecHandoff(t *testing.T) {
	var gotPath string
	var gotArgv []string
	withStubs(t,
		func(string) (string, error) { return "/usr/local/bin/kukebuild", nil },
		func(path string, argv []string, _ []string) error {
			gotPath = path
			gotArgv = argv
			return nil
		},
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		"-f", "/ctx/Dockerfile.alt",
		"-t", "reg/app:v1",
		"--realm", "kuke-system",
		"--build-arg", "FOO=bar",
		"--build-arg", "BAZ=qux",
		"/ctx",
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if gotPath != "/usr/local/bin/kukebuild" {
		t.Errorf("exec path = %q, want /usr/local/bin/kukebuild", gotPath)
	}
	want := []string{
		"kukebuild",
		"--tag", "reg/app:v1",
		"--realm", "kuke-system",
		"--file", "/ctx/Dockerfile.alt",
		"--build-arg", "FOO=bar",
		"--build-arg", "BAZ=qux",
		"/ctx",
	}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}
}

func TestRunBuildForwardsKukeondConfig(t *testing.T) {
	var gotArgv []string
	withStubs(t,
		func(string) (string, error) { return "/usr/local/bin/kukebuild", nil },
		func(_ string, argv []string, _ []string) error { gotArgv = argv; return nil },
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"-t", "x:1", "--kukeond-config", "/etc/kukeon/alt.yaml", "."})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	want := []string{
		"kukebuild",
		"--tag", "x:1",
		"--realm", consts.KukeonDefaultRealmName,
		"--kukeond-config", "/etc/kukeon/alt.yaml",
		".",
	}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}
}

func TestRunBuildDefaultRealm(t *testing.T) {
	var gotArgv []string
	withStubs(t,
		func(string) (string, error) { return "/usr/local/bin/kukebuild", nil },
		func(_ string, argv []string, _ []string) error { gotArgv = argv; return nil },
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"-t", "x:1", "."})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	// No --file given: the shim forwards no --file flag (kukebuild defaults it
	// to <context>/Dockerfile). The default realm is the user realm.
	want := []string{"kukebuild", "--tag", "x:1", "--realm", consts.KukeonDefaultRealmName, "."}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}
}
