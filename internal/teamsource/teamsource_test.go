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

package teamsource

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// gitRun runs git in dir with a hermetic identity so the test never depends on
// (or mutates) the host operator's global git config. Mirrors the pattern in
// cmd/kuketty/repos_test.go.
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@kukeon.invalid",
		"-c", "user.name=kukeon test",
		"-c", "init.defaultBranch=main",
		"-c", "commit.gpgsign=false",
		"-c", "tag.gpgsign=false",
		"-c", "tag.forceSignAnnotated=false",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %q: %v\n%s", args, dir, err, out)
	}
}

// gitOutput runs git in dir with the same hermetic identity as gitRun and
// returns its stdout — used to read a fixture commit's SHA.
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@kukeon.invalid",
		"-c", "user.name=kukeon test",
		"-c", "init.defaultBranch=main",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %q: %v", args, dir, err)
	}
	return string(out)
}

// agentsFile is one document the fixture remote should carry. Path is relative
// to the agents repo root.
type agentsFile struct {
	path string
	body string
}

// validRoleDevYAML is a minimal Role document the parser accepts.
const validRoleDevYAML = `apiVersion: kuketeams.io/v1
kind: Role
metadata: { name: dev }
spec:
  needs:
    image: [base]
`

// validHarnessClaudeYAML is a minimal Harness document the parser accepts.
const validHarnessClaudeYAML = `apiVersion: kuketeams.io/v1
kind: Harness
metadata: { name: claude }
spec:
  skillPath: /opt/claude/skills
  makeTarget: claude
  template: harnesses/claude/blueprint.tmpl.yaml
`

// validImageCatalogYAML is a minimal ImageCatalog the parser accepts.
const validImageCatalogYAML = `apiVersion: kuketeams.io/v1
kind: ImageCatalog
spec:
  images:
    - ref: claude-base
      harness: claude
      image: registry.example.com/claude:latest
      build:
        context: harnesses/claude
        dockerfile: Dockerfile
      capabilities: [base]
`

// defaultFixtureFiles is the file set every test in this file pins at the
// fixture-remote tag unless it overrides.
func defaultFixtureFiles() []agentsFile {
	return []agentsFile{
		{path: "dev/role.yaml", body: validRoleDevYAML},
		{path: "harnesses/claude/harness.yaml", body: validHarnessClaudeYAML},
		{path: "harnesses/images.yaml", body: validImageCatalogYAML},
	}
}

// makeFixtureRemote creates a non-bare git repo with the given files, commits,
// and tags it as version. Returns the file:// URL usable as a clone source.
func makeFixtureRemote(t *testing.T, version string, files []agentsFile) string {
	t.Helper()
	src := t.TempDir()
	gitRun(t, src, "init")
	for _, f := range files {
		full := filepath.Join(src, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(f.body), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "fixture")
	gitRun(t, src, "tag", version)
	return "file://" + src
}

// tagSrc is the resolved test source for the fixture remote's v1.4.0 tag.
func tagSrc(t *testing.T) Source {
	t.Helper()
	s, err := FromModel(model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"})
	if err != nil {
		t.Fatalf("FromModel: %v", err)
	}
	return s
}

func TestFromModel_Forms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name          string
		in            model.TeamSource
		wantOwnerRepo string
		wantHost      string
		wantRef       string
		wantKind      RefKind
	}{
		{
			"host tag",
			model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			"eminwux/agents",
			"github.com",
			"v1.4.0",
			RefTag,
		},
		{
			"bare defaults host",
			model.TeamSource{Repo: "eminwux/agents", Tag: "v1.4.0"},
			"eminwux/agents",
			"github.com",
			"v1.4.0",
			RefTag,
		},
		{
			"floating branch",
			model.TeamSource{Repo: "github.com/eminwux/agents", Branch: "main"},
			"eminwux/agents",
			"github.com",
			"main",
			RefBranch,
		},
		{
			"pinned commit",
			model.TeamSource{Repo: "github.com/eminwux/agents", Commit: "9ae9606"},
			"eminwux/agents",
			"github.com",
			"9ae9606",
			RefCommit,
		},
		{
			"nested host path",
			model.TeamSource{Repo: "gitlab.com/grp/sub/repo", Branch: "trunk"},
			"grp/sub/repo",
			"gitlab.com",
			"trunk",
			RefBranch,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, err := FromModel(tc.in)
			if err != nil {
				t.Fatalf("FromModel: %v", err)
			}
			if s.OwnerRepo != tc.wantOwnerRepo || s.Host != tc.wantHost || s.Ref != tc.wantRef ||
				s.Kind != tc.wantKind {
				t.Errorf("FromModel = %+v", s)
			}
			if s.Floating() != (tc.wantKind == RefBranch) {
				t.Errorf("Floating() = %v, kind = %v", s.Floating(), tc.wantKind)
			}
		})
	}
}

func TestFromModel_Rejects(t *testing.T) {
	t.Parallel()
	for _, in := range []model.TeamSource{
		{Repo: "github.com/eminwux/agents"},                            // no ref
		{Repo: "github.com/eminwux/agents", Tag: "v1", Branch: "main"}, // two refs
		{Repo: "agents", Tag: "v1.4.0"},                                // missing owner
		{Tag: "v1.4.0"},                                                // missing repo
	} {
		if _, err := FromModel(in); !errors.Is(err, errdefs.ErrTeamSourceInvalid) {
			t.Errorf("FromModel(%+v) err = %v, want ErrTeamSourceInvalid", in, err)
		}
	}
}

func TestCloneURL_SSHDefault(t *testing.T) {
	t.Parallel()
	src := tagSrc(t)
	if got := CloneURL(nil, src); got != "git@github.com:eminwux/agents.git" {
		t.Errorf("CloneURL(nil) = %q, want SSH default", got)
	}
	if got := CloneURL(&model.TeamsConfig{}, src); got != "git@github.com:eminwux/agents.git" {
		t.Errorf("CloneURL(empty) = %q, want SSH default", got)
	}
}

func TestCloneURL_OverrideByOwnerRepo(t *testing.T) {
	t.Parallel()
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Sources: map[string]string{"eminwux/agents": "  https://internal.mirror/eminwux/agents.git  "},
	}}
	if got := CloneURL(tc, tagSrc(t)); got != "https://internal.mirror/eminwux/agents.git" {
		t.Errorf("CloneURL = %q, want trimmed override", got)
	}
}

func TestCloneURL_OverrideByHostQualifiedRepo(t *testing.T) {
	t.Parallel()
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Sources: map[string]string{"github.com/eminwux/agents": "https://internal.mirror/agents.git"},
	}}
	if got := CloneURL(tc, tagSrc(t)); got != "https://internal.mirror/agents.git" {
		t.Errorf("CloneURL = %q, want host-qualified override", got)
	}
}

func TestCloneURL_BlankOverrideFallsBackToSSH(t *testing.T) {
	t.Parallel()
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{
		Sources: map[string]string{"eminwux/agents": "   "},
	}}
	if got := CloneURL(tc, tagSrc(t)); got != "git@github.com:eminwux/agents.git" {
		t.Errorf("CloneURL = %q, want SSH default on blank override", got)
	}
}

func TestCache_MaterializeClones(t *testing.T) {
	t.Parallel()
	url := makeFixtureRemote(t, "v1.4.0", defaultFixtureFiles())
	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	src := tagSrc(t)

	dir, err := cache.Materialize(context.Background(), src, url, "")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	want := filepath.Join(cache.Base, "github.com/eminwux/agents@v1.4.0")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "dev", "role.yaml")); err != nil {
		t.Errorf("role.yaml not present in cache: %v", err)
	}
}

func TestCache_MaterializeReusesPinned(t *testing.T) {
	t.Parallel()
	url := makeFixtureRemote(t, "v1.4.0", defaultFixtureFiles())
	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	src := tagSrc(t)

	dir, err := cache.Materialize(context.Background(), src, url, "")
	if err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	// Plant a sentinel inside the cache dir. A re-Materialize of a pinned tag
	// must not re-clone (which would wipe the sentinel) — the AC's "pinned
	// refs are reused as-is" contract.
	sentinel := filepath.Join(dir, ".reuse-sentinel")
	if err := os.WriteFile(sentinel, []byte("kept"), 0o600); err != nil {
		t.Fatalf("plant sentinel: %v", err)
	}
	// Point the second call at a bogus URL — if Materialize tried to clone, it
	// would fail loudly, proving reuse is broken.
	dir2, err := cache.Materialize(context.Background(), src, "file:///nonexistent", "")
	if err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	if dir2 != dir {
		t.Errorf("reuse path = %q, want %q", dir2, dir)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != "kept" {
		t.Errorf("sentinel lost on reuse: data=%q err=%v", got, err)
	}
}

func TestCache_MaterializeRefetchesFloatingBranch(t *testing.T) {
	t.Parallel()
	// A floating branch must refetch + reset to the branch tip on every
	// materialize — blind reuse would silently run a stale roster.
	remote := t.TempDir()
	gitRun(t, remote, "init")
	rolePath := filepath.Join(remote, "dev", "role.yaml")
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePath, []byte(validRoleDevYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "-m", "v1")
	url := "file://" + remote

	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	src, err := FromModel(model.TeamSource{Repo: "github.com/eminwux/agents", Branch: "main"})
	if err != nil {
		t.Fatalf("FromModel: %v", err)
	}

	dir, err := cache.Materialize(context.Background(), src, url, "")
	if err != nil {
		t.Fatalf("first Materialize: %v", err)
	}
	// Advance the branch tip with a new commit changing the role body.
	v2Role := strings.Replace(validRoleDevYAML, "image: [base]", "image: [base, go]", 1)
	if werr := os.WriteFile(rolePath, []byte(v2Role), 0o644); werr != nil {
		t.Fatal(werr)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "-m", "v2")

	dir2, err := cache.Materialize(context.Background(), src, url, "")
	if err != nil {
		t.Fatalf("second Materialize: %v", err)
	}
	if dir2 != dir {
		t.Errorf("floating refetch path = %q, want %q", dir2, dir)
	}
	got, err := os.ReadFile(filepath.Join(dir, "dev", "role.yaml"))
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}
	if !strings.Contains(string(got), "go") {
		t.Errorf("floating branch not refetched to tip; body=%q", got)
	}
}

func TestCache_MaterializePinsToVersion(t *testing.T) {
	t.Parallel()
	// Build a fixture remote with two distinct commits at two version tags so
	// HEAD on the wrong tag would surface a different role body. The pinned
	// ref check is then: the role at v1.4.0 carries the v1.4.0 body, not the
	// v1.5.0 body.
	src := t.TempDir()
	gitRun(t, src, "init")
	rolePath := filepath.Join(src, "dev", "role.yaml")
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// v1.4.0 commit: role needs [base].
	if err := os.WriteFile(rolePath, []byte(validRoleDevYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "v1.4.0")
	gitRun(t, src, "tag", "v1.4.0")
	// v1.5.0 commit: rewrite role needs to [base, go] so a clone landing here
	// would carry the second-image capability.
	v15Role := strings.Replace(validRoleDevYAML, "image: [base]", "image: [base, go]", 1)
	if err := os.WriteFile(rolePath, []byte(v15Role), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, src, "add", ".")
	gitRun(t, src, "commit", "-m", "v1.5.0")
	gitRun(t, src, "tag", "v1.5.0")
	url := "file://" + src

	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	srcRef := tagSrc(t)
	dir, err := cache.Materialize(context.Background(), srcRef, url, "")
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "dev", "role.yaml"))
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}
	if strings.Contains(string(got), "go") {
		t.Errorf("clone landed on v1.5.0, not v1.4.0; body=%q", got)
	}
}

func TestCache_MaterializePinsToCommit(t *testing.T) {
	t.Parallel()
	// Two commits on the default branch; pinning the first commit's SHA must
	// land that commit's body, not the branch tip's.
	remote := t.TempDir()
	gitRun(t, remote, "init")
	rolePath := filepath.Join(remote, "dev", "role.yaml")
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePath, []byte(validRoleDevYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "-m", "c1")
	sha := strings.TrimSpace(gitOutput(t, remote, "rev-parse", "HEAD"))
	// Advance the tip so the pinned SHA is no longer HEAD.
	v2Role := strings.Replace(validRoleDevYAML, "image: [base]", "image: [base, go]", 1)
	if werr := os.WriteFile(rolePath, []byte(v2Role), 0o644); werr != nil {
		t.Fatal(werr)
	}
	gitRun(t, remote, "add", ".")
	gitRun(t, remote, "commit", "-m", "c2")
	url := "file://" + remote

	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	src, err := FromModel(model.TeamSource{Repo: "github.com/eminwux/agents", Commit: sha})
	if err != nil {
		t.Fatalf("FromModel: %v", err)
	}
	dir, err := cache.Materialize(context.Background(), src, url, "")
	if err != nil {
		t.Fatalf("Materialize commit: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "dev", "role.yaml"))
	if err != nil {
		t.Fatalf("read role.yaml: %v", err)
	}
	if strings.Contains(string(got), "go") {
		t.Errorf("commit pin landed on tip, not c1; body=%q", got)
	}
}

func TestCache_MaterializeMissingTagSurfaces(t *testing.T) {
	t.Parallel()
	url := makeFixtureRemote(t, "v1.4.0", defaultFixtureFiles())
	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	src, err := FromModel(model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v9.9.9"})
	if err != nil {
		t.Fatalf("FromModel: %v", err)
	}

	dir := cache.Path(src)
	if _, merr := cache.Materialize(context.Background(), src, url, ""); merr == nil {
		t.Fatalf("Materialize: want clone error for missing tag, got nil")
	}
	// Atomic-rename guarantee: a failed clone leaves no half-materialized dir.
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("failed clone left %q on disk: stat err = %v", dir, err)
	}
}

func TestResolve_LoadsAllReferenced(t *testing.T) {
	t.Parallel()
	url := makeFixtureRemote(t, "v1.4.0", defaultFixtureFiles())
	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	// Default transport is SSH; an override points the resolver at the local
	// fixture remote without a live SSH host.
	tc := &model.TeamsConfig{
		Spec: model.TeamsConfigSpec{
			Sources: map[string]string{"eminwux/agents": url},
		},
	}
	pt := &model.ProjectTeam{
		Spec: model.ProjectTeamSpec{
			Source:   model.TeamSource{Repo: "github.com/eminwux/agents", Tag: "v1.4.0"},
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	b, err := Resolve(context.Background(), cache, tc, pt)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if b.Roles["dev"] == nil || b.Roles["dev"].Metadata.Name != "dev" {
		t.Errorf("role dev not loaded: %+v", b.Roles)
	}
	if b.Harnesses["claude"] == nil || b.Harnesses["claude"].Metadata.Name != "claude" {
		t.Errorf("harness claude not loaded: %+v", b.Harnesses)
	}
	if b.ImageCatalog == nil || len(b.ImageCatalog.Spec.Images) != 1 {
		t.Errorf("image catalog not loaded: %+v", b.ImageCatalog)
	}
}

func TestResolve_InvalidSourceErrors(t *testing.T) {
	t.Parallel()
	tc := &model.TeamsConfig{Spec: model.TeamsConfigSpec{}}
	pt := &model.ProjectTeam{
		// No ref set — exactly-one-of violated.
		Spec: model.ProjectTeamSpec{Source: model.TeamSource{Repo: "github.com/eminwux/agents"}},
	}
	cache := NewCache(filepath.Join(t.TempDir(), "cache"))
	_, err := Resolve(context.Background(), cache, tc, pt)
	if !errors.Is(err, errdefs.ErrTeamSourceInvalid) {
		t.Fatalf("Resolve err = %v, want ErrTeamSourceInvalid", err)
	}
}

func TestLoadRole_WrongKindNamesPath(t *testing.T) {
	t.Parallel()
	// A role.yaml that parses successfully as a different kind (a valid
	// Harness document here) triggers ErrTeamRoleFileKind at the loader layer
	// — the parser dispatched on `kind:`, validated cleanly, but the loader
	// expected a Role document.
	dir := t.TempDir()
	rolePath := filepath.Join(dir, "dev", "role.yaml")
	if err := os.MkdirAll(filepath.Dir(rolePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePath, []byte(validHarnessClaudeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadRole(dir, "dev")
	if !errors.Is(err, errdefs.ErrTeamRoleFileKind) {
		t.Fatalf("err = %v, want ErrTeamRoleFileKind", err)
	}
	if !strings.Contains(err.Error(), rolePath) {
		t.Errorf("err %q does not name role path %q", err, rolePath)
	}
}

func TestLoadHarness_ParseErrorNamesPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	hp := filepath.Join(dir, "harnesses", "claude", "harness.yaml")
	if err := os.MkdirAll(filepath.Dir(hp), 0o755); err != nil {
		t.Fatal(err)
	}
	// Invalid YAML: surfaces a parse error wrapped with the file path.
	if err := os.WriteFile(hp, []byte("apiVersion: kuketeams.io/v1\nkind: Harness\nspec: { skillPath: [bad}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadHarness(dir, "claude")
	if err == nil {
		t.Fatalf("want parse error, got nil")
	}
	if !strings.Contains(err.Error(), hp) {
		t.Errorf("err %q does not name harness path %q", err, hp)
	}
}

func TestLoadImageCatalog_WrongKindNamesPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ip := filepath.Join(dir, "harnesses", "images.yaml")
	if err := os.MkdirAll(filepath.Dir(ip), 0o755); err != nil {
		t.Fatal(err)
	}
	// Plant a valid Role document at the image-catalog path: the parser
	// validates it cleanly, the loader catches the kind mismatch and surfaces
	// ErrTeamImageCatalogFileKind with the path named.
	if err := os.WriteFile(ip, []byte(validRoleDevYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadImageCatalog(dir)
	if !errors.Is(err, errdefs.ErrTeamImageCatalogFileKind) {
		t.Fatalf("err = %v, want ErrTeamImageCatalogFileKind", err)
	}
	if !strings.Contains(err.Error(), ip) {
		t.Errorf("err %q does not name catalog path %q", err, ip)
	}
}

func TestScrubURLCredentials(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "basic-auth-token",
			in:   "https://x-token:ghp_xxx@github.com/owner/agents.git",
			want: "https://github.com/owner/agents.git",
		},
		{
			name: "user-only",
			in:   "https://bob@github.com/owner/agents.git",
			want: "https://github.com/owner/agents.git",
		},
		{
			name: "ssh-with-password",
			in:   "ssh://git:secret@github.com/owner/agents.git",
			want: "ssh://github.com/owner/agents.git",
		},
		{
			name: "https-no-userinfo-unchanged",
			in:   "https://github.com/owner/agents.git",
			want: "https://github.com/owner/agents.git",
		},
		{
			name: "scp-style-unchanged",
			in:   "git@github.com:owner/agents.git",
			want: "git@github.com:owner/agents.git",
		},
		{
			name: "flag-unchanged",
			in:   "--depth=1",
			want: "--depth=1",
		},
		{
			name: "branch-name-unchanged",
			in:   "v1.4.0",
			want: "v1.4.0",
		},
		{
			name: "file-url-unchanged",
			in:   "file:///tmp/agents-fixture",
			want: "file:///tmp/agents-fixture",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scrubURLCredentials(tc.in); got != tc.want {
				t.Errorf("scrubURLCredentials(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoadRole_MissingFileNamesPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := LoadRole(dir, "dev")
	if err == nil {
		t.Fatalf("want read error, got nil")
	}
	wantSub := filepath.Join(dir, "dev", "role.yaml")
	if !strings.Contains(err.Error(), wantSub) {
		t.Errorf("err %q does not name role path %q", err, wantSub)
	}
}
