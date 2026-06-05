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

package teambuild

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// fixtureFile carries one on-disk artifact a fixture cache tree must
// materialize: a path relative to cacheDir + its contents.
type fixtureFile struct {
	path, body string
}

// writeFixture writes every entry under a fresh cache dir and returns it.
func writeFixture(t *testing.T, files ...fixtureFile) string {
	t.Helper()
	cacheDir := t.TempDir()
	for _, f := range files {
		abs := filepath.Join(cacheDir, f.path)
		if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(abs), err)
		}
		if err := os.WriteFile(abs, []byte(f.body), 0o600); err != nil {
			t.Fatalf("write %q: %v", abs, err)
		}
	}
	return cacheDir
}

// claudeChainFixture builds a four-level FROM chain mirroring the agents
// repo's harness layout: leaf claude → base-user → base → debian. The
// intermediate bases live under harnesses/<name>/Dockerfile; the leaf carries
// a build.context + build.dockerfile.
func claudeChainFixture(t *testing.T) (string, *model.ImageCatalogEntry) {
	t.Helper()
	cacheDir := writeFixture(t,
		fixtureFile{
			path: "harnesses/claude/Dockerfile",
			body: "ARG REGISTRY=docker.io\n" +
				"FROM ${REGISTRY}/base-user:latest\n" +
				"RUN echo claude\n",
		},
		fixtureFile{
			path: "harnesses/base-user/Dockerfile",
			body: "ARG REGISTRY=docker.io\n" +
				"FROM ${REGISTRY}/base:latest\n" +
				"RUN echo base-user\n",
		},
		fixtureFile{
			path: "harnesses/base/Dockerfile",
			body: "FROM debian:bookworm\nRUN echo base\n",
		},
	)
	entry := &model.ImageCatalogEntry{
		Ref:     "claude",
		Harness: "claude",
		Image:   "registry.local/claude:latest",
		Build: model.ImageCatalogBuild{
			Context:    "harnesses/claude",
			Dockerfile: "Dockerfile",
		},
		Capabilities: []string{"go", "git"},
	}
	return cacheDir, entry
}

func TestPlanBaseBeforeLeaves(t *testing.T) {
	t.Parallel()
	cacheDir, entry := claudeChainFixture(t)

	steps, err := Plan(cacheDir, "v1.4.0", []*model.ImageCatalogEntry{entry})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	wantOrder := []string{"base", "base-user", "claude"}
	gotOrder := make([]string, len(steps))
	for i, s := range steps {
		gotOrder[i] = s.Name
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("Plan order = %v, want %v (bases must appear before leaves)", gotOrder, wantOrder)
	}

	// IsLeaf flag mirrors topology.
	for _, s := range steps {
		want := s.Name == "claude"
		if s.IsLeaf != want {
			t.Errorf("step %q IsLeaf = %v, want %v", s.Name, s.IsLeaf, want)
		}
	}

	// Leaf gets the source ref as its tag suffix per AC 3; intermediates
	// inherit the literal tag carried in the leaves' FROM line (`latest`).
	tags := map[string]string{}
	for _, s := range steps {
		tags[s.Name] = s.Tag
	}
	if got, want := tags["claude"], "kukeon.internal/claude:v1.4.0"; got != want {
		t.Errorf("leaf tag = %q, want %q", got, want)
	}
	if got, want := tags["base-user"], "kukeon.internal/base-user:latest"; got != want {
		t.Errorf("base-user tag = %q, want %q", got, want)
	}
	if got, want := tags["base"], "kukeon.internal/base:latest"; got != want {
		t.Errorf("base tag = %q, want %q", got, want)
	}

	// Every step is seeded with REGISTRY=kukeon.internal so leaf FROMs of the
	// form `${REGISTRY}/base-user:latest` resolve to the in-realm base.
	for _, s := range steps {
		if got, want := s.BuildArgs["REGISTRY"], InternalRegistry; got != want {
			t.Errorf("step %q REGISTRY build-arg = %q, want %q", s.Name, got, want)
		}
	}
}

func TestPlanDedupesSharedBase(t *testing.T) {
	t.Parallel()
	// Two leaves both FROM the same in-repo base; the base must appear in
	// the output exactly once and must precede both leaves.
	cacheDir := writeFixture(t,
		fixtureFile{
			path: "harnesses/alpha/Dockerfile",
			body: "FROM ${REGISTRY}/base:latest\n",
		},
		fixtureFile{
			path: "harnesses/beta/Dockerfile",
			body: "FROM ${REGISTRY}/base:latest\n",
		},
		fixtureFile{
			path: "harnesses/base/Dockerfile",
			body: "FROM debian:bookworm\n",
		},
	)
	leaves := []*model.ImageCatalogEntry{
		{
			Ref: "alpha", Harness: "alpha", Image: "registry.local/alpha:latest",
			Build: model.ImageCatalogBuild{Context: "harnesses/alpha", Dockerfile: "Dockerfile"},
		},
		{
			Ref: "beta", Harness: "beta", Image: "registry.local/beta:latest",
			Build: model.ImageCatalogBuild{Context: "harnesses/beta", Dockerfile: "Dockerfile"},
		},
	}

	steps, err := Plan(cacheDir, "v2", leaves)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	gotOrder := make([]string, len(steps))
	for i, s := range steps {
		gotOrder[i] = s.Name
	}
	want := []string{"base", "alpha", "beta"}
	if !reflect.DeepEqual(gotOrder, want) {
		t.Fatalf("dedup order = %v, want %v (base before both leaves, lex tiebreak)", gotOrder, want)
	}
}

func TestPlanMissingBaseSurfacesErrdefs(t *testing.T) {
	t.Parallel()
	// Leaf's FROM points at an in-repo base whose Dockerfile is absent —
	// must surface ErrTeamBuildBaseMissing.
	cacheDir := writeFixture(t,
		fixtureFile{
			path: "harnesses/leaf/Dockerfile",
			body: "FROM ${REGISTRY}/base-missing:latest\n",
		},
	)
	leaves := []*model.ImageCatalogEntry{
		{
			Ref: "leaf", Harness: "leaf", Image: "registry.local/leaf:latest",
			Build: model.ImageCatalogBuild{Context: "harnesses/leaf", Dockerfile: "Dockerfile"},
		},
	}

	_, err := Plan(cacheDir, "v1", leaves)
	if !errors.Is(err, errdefs.ErrTeamBuildBaseMissing) {
		t.Fatalf("Plan err = %v, want ErrTeamBuildBaseMissing", err)
	}
}

func TestPlanMissingContextSurfacesErrdefs(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	leaves := []*model.ImageCatalogEntry{
		{
			Ref: "leaf", Harness: "leaf", Image: "registry.local/leaf:latest",
			Build: model.ImageCatalogBuild{
				Context: "harnesses/nonexistent", Dockerfile: "Dockerfile",
			},
		},
	}

	_, err := Plan(cacheDir, "v1", leaves)
	if !errors.Is(err, errdefs.ErrTeamBuildContextMissing) {
		t.Fatalf("Plan err = %v, want ErrTeamBuildContextMissing", err)
	}
}

func TestPlanExternalFROMNotBuilt(t *testing.T) {
	t.Parallel()
	cacheDir := writeFixture(t,
		fixtureFile{
			path: "harnesses/leaf/Dockerfile",
			body: "FROM debian:bookworm\nRUN echo leaf\n",
		},
	)
	leaves := []*model.ImageCatalogEntry{
		{
			Ref: "leaf", Harness: "leaf", Image: "registry.local/leaf:latest",
			Build: model.ImageCatalogBuild{Context: "harnesses/leaf", Dockerfile: "Dockerfile"},
		},
	}

	steps, err := Plan(cacheDir, "v1", leaves)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 1 || steps[0].Name != "leaf" {
		t.Fatalf("steps = %+v, want a single leaf step", steps)
	}
}

func TestPlanEmptyLeavesIsEmpty(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	steps, err := Plan(cacheDir, "v1", nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(steps) != 0 {
		t.Fatalf("steps = %v, want empty slice", steps)
	}
}

func TestBuildArgvShape(t *testing.T) {
	t.Parallel()
	step := Step{
		Name:       "claude",
		Version:    "v1.4.0",
		Tag:        "kukeon.internal/claude:v1.4.0",
		Context:    "/cache/harnesses/claude",
		Dockerfile: "/cache/harnesses/claude/Dockerfile",
		BuildArgs: map[string]string{
			"REGISTRY": "kukeon.internal",
			"VERSION":  "v1.4.0",
		},
	}

	argv := BuildArgv(step, "kuke-system")

	want := []string{
		"kukebuild",
		"--tag", "kukeon.internal/claude:v1.4.0",
		"--realm", "kuke-system",
		"--file", "/cache/harnesses/claude/Dockerfile",
		"--build-arg", "REGISTRY=kukeon.internal",
		"--build-arg", "VERSION=v1.4.0",
		"/cache/harnesses/claude",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("BuildArgv = %v\n want %v", argv, want)
	}
}

func TestBuildArgvBuildArgsSorted(t *testing.T) {
	t.Parallel()
	// Go map iteration order is randomized — verify the sorted emission
	// produces byte-stable output by running BuildArgv many times and
	// asserting the build-arg order is always alphabetical.
	step := Step{
		Name: "leaf", Version: "v1", Tag: "kukeon.internal/leaf:v1",
		Context:    "/cache/leaf",
		Dockerfile: "/cache/leaf/Dockerfile",
		BuildArgs: map[string]string{
			"ZETA": "z", "ALPHA": "a", "GAMMA": "g", "BETA": "b",
		},
	}
	for i := 0; i < 32; i++ {
		argv := BuildArgv(step, "default")
		// Pull the build-arg keys in argv order.
		var keys []string
		for j := 0; j < len(argv)-1; j++ {
			if argv[j] == "--build-arg" {
				keys = append(keys, strings.SplitN(argv[j+1], "=", 2)[0])
			}
		}
		want := []string{"ALPHA", "BETA", "GAMMA", "ZETA"}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("iter %d build-arg keys = %v, want %v", i, keys, want)
		}
	}
}

func TestRunSpawnsBinaryViaRunStep(t *testing.T) {
	// Mutates package-level lookPath/runStep seams; cannot run in parallel
	// with the other seam-mutating tests.
	// Override the test seams so Run never resolves a real binary or spawns a
	// process. The captured argv must mirror BuildArgv's shape.
	origLook, origRun := lookPath, runStep
	t.Cleanup(func() { lookPath, runStep = origLook, origRun })

	lookPath = func(name string) (string, error) {
		if name != kukebuildBinary {
			t.Fatalf("lookPath(%q): want %q", name, kukebuildBinary)
		}
		return "/fake/path/" + name, nil
	}

	var (
		mu      sync.Mutex
		gotBin  string
		gotArgv []string
	)
	runStep = func(_ context.Context, binPath string, argv []string, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		gotBin = binPath
		gotArgv = append([]string(nil), argv...)
		return nil
	}

	step := Step{
		Name: "leaf", Version: "v1", Tag: "kukeon.internal/leaf:v1",
		Context:    "/cache/leaf",
		Dockerfile: "/cache/leaf/Dockerfile",
		BuildArgs:  map[string]string{"REGISTRY": InternalRegistry},
	}
	if err := Run(context.Background(), step, "default", io.Discard, io.Discard); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if gotBin != "/fake/path/kukebuild" {
		t.Errorf("binPath = %q, want %q", gotBin, "/fake/path/kukebuild")
	}
	wantArgv := BuildArgv(step, "default")
	if !reflect.DeepEqual(gotArgv, wantArgv) {
		t.Errorf("argv = %v, want %v", gotArgv, wantArgv)
	}
}

func TestRunMissingBinarySurfacesErrdefs(t *testing.T) {
	// Mutates package-level lookPath/runStep seams; cannot run in parallel.
	origLook, origRun := lookPath, runStep
	t.Cleanup(func() { lookPath, runStep = origLook, origRun })

	lookPath = func(string) (string, error) { return "", errors.New("not found") }
	runStep = func(context.Context, string, []string, io.Writer, io.Writer) error {
		t.Fatalf("runStep must not be called when kukebuild is missing")
		return nil
	}

	step := Step{Name: "leaf", Tag: "kukeon.internal/leaf:v1", Context: "/cache/leaf"}
	err := Run(context.Background(), step, "default", io.Discard, io.Discard)
	if !errors.Is(err, errdefs.ErrKukebuildNotFound) {
		t.Fatalf("Run err = %v, want ErrKukebuildNotFound", err)
	}
}

func TestBuildAllInvokesPerStepInOrder(t *testing.T) {
	// Mutates package-level lookPath/runStep seams; cannot run in parallel.
	cacheDir, entry := claudeChainFixture(t)

	origLook, origRun := lookPath, runStep
	t.Cleanup(func() { lookPath, runStep = origLook, origRun })

	lookPath = func(string) (string, error) { return "/fake/kukebuild", nil }

	var (
		mu    sync.Mutex
		calls []string
	)
	runStep = func(_ context.Context, _ string, argv []string, _, _ io.Writer) error {
		mu.Lock()
		defer mu.Unlock()
		// argv carries `--tag <tag>` at positions 1/2.
		var tag string
		for i := 0; i < len(argv)-1; i++ {
			if argv[i] == "--tag" {
				tag = argv[i+1]
				break
			}
		}
		calls = append(calls, tag)
		return nil
	}

	var progress bytes.Buffer
	err := BuildAll(
		context.Background(), cacheDir, "v1.4.0", "default",
		[]*model.ImageCatalogEntry{entry},
		&progress, io.Discard, io.Discard,
	)
	if err != nil {
		t.Fatalf("BuildAll: %v", err)
	}

	wantTags := []string{
		"kukeon.internal/base:latest",
		"kukeon.internal/base-user:latest",
		"kukeon.internal/claude:v1.4.0",
	}
	if !reflect.DeepEqual(calls, wantTags) {
		t.Fatalf("kukebuild call order = %v, want %v", calls, wantTags)
	}

	// Progress writer must announce every step before kukebuild fires.
	for _, tag := range wantTags {
		if !strings.Contains(progress.String(), tag) {
			t.Errorf("progress output missing %q; got:\n%s", tag, progress.String())
		}
	}
}

func TestBuildAllEmptyLeavesIsNoop(t *testing.T) {
	// Mutates package-level lookPath/runStep seams; cannot run in parallel.
	origLook, origRun := lookPath, runStep
	t.Cleanup(func() { lookPath, runStep = origLook, origRun })

	lookPath = func(string) (string, error) {
		t.Fatalf("lookPath must not be called on empty leaves")
		return "", nil
	}
	runStep = func(context.Context, string, []string, io.Writer, io.Writer) error {
		t.Fatalf("runStep must not be called on empty leaves")
		return nil
	}

	if err := BuildAll(
		context.Background(), t.TempDir(), "v1", "default",
		nil, io.Discard, io.Discard, io.Discard,
	); err != nil {
		t.Fatalf("BuildAll on empty leaves: %v", err)
	}
}

func TestResolveInternalDep(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in           string
		wantName     string
		wantTag      string
		wantInternal bool
	}{
		{"${REGISTRY}/base-user:latest", "base-user", "latest", true},
		{"$REGISTRY/base:v1", "base", "v1", true},
		{"kukeon.internal/base-user:latest", "base-user", "latest", true},
		{"kukeon.internal/leaf", "leaf", "latest", true},
		{"debian:bookworm", "", "", false},
		{"docker.io/library/ubuntu:24.04", "", "", false},
		{"registry.local/leaf:latest", "", "", false},
	}
	for _, c := range cases {
		name, tag, internal := resolveInternalDep(c.in)
		if name != c.wantName || tag != c.wantTag || internal != c.wantInternal {
			t.Errorf(
				"resolveInternalDep(%q) = (%q,%q,%v), want (%q,%q,%v)",
				c.in, name, tag, internal, c.wantName, c.wantTag, c.wantInternal,
			)
		}
	}
}
