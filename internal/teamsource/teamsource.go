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

// Package teamsource resolves a kuketeams.io/v1 ProjectTeam's structured
// `source` reference to a local clone of the agents repo and loads the Role,
// Harness, and ImageCatalog documents the project's roster references (epic
// #792). The source is host-explicit (`repo: <host>/<owner>/<repo>`) and
// carries exactly one ref intent: a pinned `tag`/`commit` (reproducible — the
// cache is cloned once and reused as-is) or a floating `branch` (refetched and
// reset to the branch tip on every init, so a stale roster is never silently
// reused). The default clone transport is SSH (`git@<host>:<owner>/<repo>.git`)
// using TeamsConfig.spec.git.sshKey as the clone identity; TeamsConfig.spec.
// sources is consulted only as an optional per-repo transport override (HTTPS,
// internal mirror, non-standard port). Document loading delegates to the
// existing kuketeams parser, so parse errors carry the offending file path
// verbatim. No daemon is required — the package is unit-testable against a
// local fixture remote.
package teamsource

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/kuketeams"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// cacheDirPerm is the cache root and per-source clone mode — operator-only,
// matching teamhost's drop-in directory perm. Secret material never lives in
// the cache (the agents source is the operator's own repo at a tag/branch),
// but keeping the perm consistent avoids surprise for an operator inspecting
// ~/.kuke.
const cacheDirPerm = 0o700

// RefKind classifies a source ref's reuse semantics.
type RefKind string

const (
	// RefTag is a pinned tag — cloned once, reused as-is.
	RefTag RefKind = "tag"
	// RefBranch is a floating branch — refetched + reset on every init.
	RefBranch RefKind = "branch"
	// RefCommit is a pinned commit — fetched once, reused as-is.
	RefCommit RefKind = "commit"
)

// Source is a resolved, host-explicit agents reference.
type Source struct {
	// Repo is the normalized host-qualified `<host>/<owner>/<repo>` — the
	// cache-directory key (with @ref appended) and override-lookup key.
	Repo string
	// Host is the git host (`github.com`, `git.example:22`).
	Host string
	// OwnerRepo is the `<owner>/<repo>` (or `<group>/<sub>/<repo>`) half — the
	// SSH path and the bare override-lookup key.
	OwnerRepo string
	// Ref is the single tag/branch/commit value.
	Ref string
	// Kind is the ref classification (tag/branch/commit) driving reuse.
	Kind RefKind
}

// Floating reports whether the source is a floating branch (refetched every
// init). Pinned tag/commit sources return false.
func (s Source) Floating() bool { return s.Kind == RefBranch }

// FromModel validates and normalizes a model.TeamSource into a resolved
// Source: the repo host is defaulted to github.com when bare, and the single
// tag/branch/commit ref is extracted. A malformed source surfaces
// errdefs.ErrTeamSourceInvalid via the shared parser validation.
func FromModel(m model.TeamSource) (Source, error) {
	if err := kuketeams.ValidateSource(m); err != nil {
		return Source{}, err
	}
	host, ownerRepo := m.Normalized()
	value, kind := m.Ref()
	return Source{
		Repo:      host + "/" + ownerRepo,
		Host:      host,
		OwnerRepo: ownerRepo,
		Ref:       value,
		Kind:      RefKind(kind),
	}, nil
}

// CloneURL returns the clone URL for src. The default transport is SSH —
// `git@<host>:<owner>/<repo>.git`, cloned under the sshKey identity (threaded
// separately into the git env). TeamsConfig.spec.sources is consulted only as
// an optional transport override: an entry keyed by the host-qualified repo or
// the bare `<owner>/<repo>` (in that order) with a non-blank value replaces the
// SSH default. The common case needs no sources entry.
func CloneURL(tc *model.TeamsConfig, src Source) string {
	if tc != nil {
		for _, key := range []string{src.Repo, src.OwnerRepo} {
			if u, ok := tc.Spec.Sources[key]; ok && strings.TrimSpace(u) != "" {
				return strings.TrimSpace(u)
			}
		}
	}
	return "git@" + src.Host + ":" + src.OwnerRepo + ".git"
}

// Cache is the on-disk cache root for materialized agents sources. Each
// reference lives at <Base>/<host>/<owner>/<repo>@<ref>, so the ref is encoded
// in the leaf directory name. For a pinned tag/commit the existence of that
// directory is sufficient to decide reuse; a floating branch refetches the
// branch tip in place on every materialize.
type Cache struct {
	Base string
}

// NewCache returns a Cache rooted at base. Pair with teamhost.Layout.CacheDir
// for the standard ~/.kuke/cache location.
func NewCache(base string) Cache {
	return Cache{Base: base}
}

// Path returns the on-disk directory for src under this cache.
func (c Cache) Path(src Source) string {
	return filepath.Join(c.Base, src.Repo+"@"+src.Ref)
}

// Materialize ensures src is present at its cache path and returns that path,
// honoring the ref kind:
//
//   - pinned (tag/commit): an existing cache directory is reused as-is; a
//     missing one is cloned into a sibling temp directory and renamed into
//     place atomically, so an interrupted clone never leaves a
//     half-materialized cache dir behind.
//   - floating (branch): an existing cache directory is refetched (`git fetch`)
//     and hard-reset to the branch tip on every call — blind reuse of a
//     floating branch would silently run stale agents. A missing one is cloned
//     like the pinned path.
//
// sshKey, when non-empty, is wired into GIT_SSH_COMMAND (`-i <key>
// -o IdentitiesOnly=yes`) so the SSH-default transport clones under the
// operator's declared identity. The non-interactive git env
// (GIT_TERMINAL_PROMPT=0, StrictHostKeyChecking=accept-new BatchMode=yes SSH)
// mirrors kuketty's processRepos so a first-time clone of an unseen host never
// hangs.
func (c Cache) Materialize(ctx context.Context, src Source, cloneURL, sshKey string) (string, error) {
	dst := c.Path(src)
	_, statErr := os.Stat(dst)
	exists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("stat cache dir %q: %w", dst, statErr)
	}

	if exists {
		if src.Floating() {
			if err := refreshFloating(ctx, dst, src, sshKey); err != nil {
				return "", err
			}
		}
		// Pinned refs reuse the existing cache dir as-is.
		return dst, nil
	}

	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, cacheDirPerm); err != nil {
		return "", fmt.Errorf("create cache parent %q: %w", parent, err)
	}

	// Reserve a unique temp name as a sibling of dst (so the rename stays on
	// the same filesystem), then remove it so the clone path creates the
	// directory itself.
	tmp, err := os.MkdirTemp(parent, ".clone-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp clone dir in %q: %w", parent, err)
	}
	if rmErr := os.Remove(tmp); rmErr != nil {
		return "", fmt.Errorf("clear temp clone dir %q: %w", tmp, rmErr)
	}

	if cloneErr := cloneInto(ctx, tmp, cloneURL, sshKey, src); cloneErr != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("clone %s@%s: %w", src.Repo, src.Ref, cloneErr)
	}

	if renameErr := os.Rename(tmp, dst); renameErr != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("rename temp clone %q to %q: %w", tmp, dst, renameErr)
	}
	return dst, nil
}

// cloneInto clones src into the empty dst directory. Tags and branches use a
// shallow `git clone --branch <ref>`; a commit cannot be `--branch`-cloned, so
// it is fetched by SHA into a fresh repo and checked out detached.
func cloneInto(ctx context.Context, dst, cloneURL, sshKey string, src Source) error {
	if src.Kind == RefCommit {
		if err := runGit(ctx, "", sshKey, "init", "-q", dst); err != nil {
			return err
		}
		if err := runGit(ctx, dst, sshKey, "remote", "add", "origin", cloneURL); err != nil {
			return err
		}
		if err := runGit(ctx, dst, sshKey, "fetch", "--depth=1", "origin", src.Ref); err != nil {
			return err
		}
		return runGit(ctx, dst, sshKey, "checkout", "-q", "--detach", "FETCH_HEAD")
	}
	return runGit(ctx, "", sshKey,
		"clone", "--depth=1", "--no-tags",
		"--branch", src.Ref,
		cloneURL, dst,
	)
}

// refreshFloating refetches a floating branch's tip into the existing cache
// dir and hard-resets the worktree to it, so a re-init always runs the current
// branch HEAD rather than a stale clone.
func refreshFloating(ctx context.Context, dir string, src Source, sshKey string) error {
	if err := runGit(ctx, dir, sshKey, "fetch", "--depth=1", "origin", src.Ref); err != nil {
		return fmt.Errorf("refetch floating %s@%s: %w", src.Repo, src.Ref, err)
	}
	if err := runGit(ctx, dir, sshKey, "reset", "--hard", "FETCH_HEAD"); err != nil {
		return fmt.Errorf("reset floating %s@%s: %w", src.Repo, src.Ref, err)
	}
	return nil
}

// Bundle is the materialized agents source plus the Role / Harness /
// ImageCatalog documents the project's roster references.
type Bundle struct {
	// Source is the resolved reference (repo, host, owner/repo, ref, kind).
	Source Source
	// CacheDir is the on-disk clone root — passed to the render pipeline when
	// it needs to read blueprint templates or other repo-relative paths.
	CacheDir string
	// Roles maps the role ref (ProjectTeam.spec.roles[].ref) to its loaded Role.
	Roles map[string]*model.Role
	// Harnesses maps the harness name (ProjectTeam.spec.defaults.harnesses[])
	// to its loaded Harness.
	Harnesses map[string]*model.Harness
	// ImageCatalog is the single harnesses/images.yaml in the agents source.
	ImageCatalog *model.ImageCatalog
}

// Resolve validates pt.Spec.Source, derives the clone URL (SSH default, with
// tc.spec.sources as an optional override), materializes the agents source
// under cache using tc.spec.git.sshKey as the clone identity, and loads every
// Role referenced by pt.Spec.Roles, every Harness referenced by
// pt.Spec.Defaults.Harnesses, and the ImageCatalog. Parse errors surface with
// the offending file path.
func Resolve(
	ctx context.Context,
	cache Cache,
	tc *model.TeamsConfig,
	pt *model.ProjectTeam,
) (*Bundle, error) {
	src, err := FromModel(pt.Spec.Source)
	if err != nil {
		return nil, err
	}
	cloneURL := CloneURL(tc, src)
	dir, err := cache.Materialize(ctx, src, cloneURL, sshKeyOf(tc))
	if err != nil {
		return nil, err
	}

	b := &Bundle{
		Source:    src,
		CacheDir:  dir,
		Roles:     make(map[string]*model.Role, len(pt.Spec.Roles)),
		Harnesses: make(map[string]*model.Harness, len(pt.Spec.Defaults.Harnesses)),
	}

	for _, r := range pt.Spec.Roles {
		role, loadErr := LoadRole(dir, r.Ref)
		if loadErr != nil {
			return nil, loadErr
		}
		b.Roles[r.Ref] = role
	}
	for _, h := range pt.Spec.Defaults.Harnesses {
		harness, loadErr := LoadHarness(dir, h)
		if loadErr != nil {
			return nil, loadErr
		}
		b.Harnesses[h] = harness
	}
	ic, loadErr := LoadImageCatalog(dir)
	if loadErr != nil {
		return nil, loadErr
	}
	b.ImageCatalog = ic
	return b, nil
}

// sshKeyOf returns the operator's SSH clone identity from the TeamsConfig git
// block, or "" when none is configured (the default SSH agent identity is then
// used by git).
func sshKeyOf(tc *model.TeamsConfig) string {
	if tc == nil || tc.Spec.Git == nil {
		return ""
	}
	return strings.TrimSpace(tc.Spec.Git.SSHKey)
}

// RolePath is the on-disk role.yaml location for ref under cacheDir. Roles
// live at the agents source's top level (<cacheDir>/<ref>/role.yaml) — the
// asymmetry vs the `harnesses/`-nested HarnessPath / ImageCatalogPath layout
// matches the canonical agents repo (github.com/eminwux/agents), which keeps
// each role under its own top-level directory and groups harnesses (plus the
// shared images.yaml) under a `harnesses/` umbrella.
func RolePath(cacheDir, ref string) string {
	return filepath.Join(cacheDir, ref, "role.yaml")
}

// HarnessPath is the on-disk harness.yaml location for name under cacheDir.
func HarnessPath(cacheDir, name string) string {
	return filepath.Join(HarnessDir(cacheDir, name), "harness.yaml")
}

// HarnessDir is the on-disk directory the named harness's harness.yaml lives
// in. teamrender resolves harness.Spec.Template relative to this directory
// and scans it for sibling *.tmpl.yaml partials, so the renderer and the
// loader agree on the layout without each open-coding the path.
func HarnessDir(cacheDir, name string) string {
	return filepath.Join(cacheDir, "harnesses", name)
}

// ImageCatalogPath is the on-disk harnesses/images.yaml location under cacheDir.
func ImageCatalogPath(cacheDir string) string {
	return filepath.Join(cacheDir, "harnesses", "images.yaml")
}

// LoadRole reads <cacheDir>/<ref>/role.yaml, parses it via the kuketeams
// parser, and returns the typed Role. A document of the wrong kind surfaces
// ErrTeamRoleFileKind; parse errors carry the file path verbatim.
func LoadRole(cacheDir, ref string) (*model.Role, error) {
	path := RolePath(cacheDir, ref)
	doc, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if doc.Role == nil {
		return nil, fmt.Errorf("%w (got kind %q in %q)", errdefs.ErrTeamRoleFileKind, doc.Kind, path)
	}
	return doc.Role, nil
}

// LoadHarness reads <cacheDir>/harnesses/<name>/harness.yaml and returns the
// typed Harness. A document of the wrong kind surfaces ErrTeamHarnessFileKind.
func LoadHarness(cacheDir, name string) (*model.Harness, error) {
	path := HarnessPath(cacheDir, name)
	doc, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if doc.Harness == nil {
		return nil, fmt.Errorf("%w (got kind %q in %q)", errdefs.ErrTeamHarnessFileKind, doc.Kind, path)
	}
	return doc.Harness, nil
}

// LoadImageCatalog reads <cacheDir>/harnesses/images.yaml and returns the
// typed ImageCatalog. A document of the wrong kind surfaces
// ErrTeamImageCatalogFileKind.
func LoadImageCatalog(cacheDir string) (*model.ImageCatalog, error) {
	path := ImageCatalogPath(cacheDir)
	doc, err := parseFile(path)
	if err != nil {
		return nil, err
	}
	if doc.ImageCatalog == nil {
		return nil, fmt.Errorf("%w (got kind %q in %q)", errdefs.ErrTeamImageCatalogFileKind, doc.Kind, path)
	}
	return doc.ImageCatalog, nil
}

// parseFile reads path and parses it via kuketeams.Parse. Read and parse
// errors are wrapped with the file path so the operator sees which document in
// the cloned tree is broken.
func parseFile(path string) (*kuketeams.Document, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	doc, err := kuketeams.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse %q: %w", path, err)
	}
	return doc, nil
}

// runGit invokes git with the operator's environment plus non-interactive
// guards (GIT_TERMINAL_PROMPT=0 and a StrictHostKeyChecking=accept-new
// BatchMode=yes GIT_SSH_COMMAND), mirroring kuketty's processRepos guards so
// a first-time clone of an unseen host never blocks on an interactive
// yes/no prompt. dir is the working directory ("" for the process cwd); sshKey,
// when non-empty, is added as the SSH clone identity. Combined output is folded
// into the error for actionable diagnostics.
func runGit(ctx context.Context, dir, sshKey string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv(sshKey)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		// Scrub URL userinfo from args before joining into the wrapper — an
		// operator may embed basic-auth credentials in a TeamsConfig source URL
		// (e.g. https://x-token:ghp_xxx@host/repo.git) and a raw join would
		// leak the token through any surface that prints this error.
		scrubbed := strings.Join(scrubArgs(args), " ")
		if trimmed != "" {
			return fmt.Errorf("git %s: %w: %s", scrubbed, err, trimmed)
		}
		return fmt.Errorf("git %s: %w", scrubbed, err)
	}
	return nil
}

// scrubArgs returns a copy of args with URL userinfo (basic-auth user:password
// and bare tokens) redacted from any arg that parses as an absolute URL.
// Non-URL args (flags, version refs, paths) pass through unchanged.
func scrubArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = scrubURLCredentials(a)
	}
	return out
}

// scrubURLCredentials strips the userinfo component from arg when it parses as
// an absolute URL carrying one. Args that aren't URL-shaped (no scheme) or
// carry no userinfo are returned verbatim — the goal is targeted redaction of
// the http(s)/ssh credential-bearing surface, not aggressive rewriting.
func scrubURLCredentials(arg string) string {
	u, err := url.Parse(arg)
	if err != nil || u.Scheme == "" || u.User == nil {
		return arg
	}
	u.User = nil
	return u.String()
}

// gitEnv returns the operator's environment augmented with non-interactive
// guards. Both guards yield to a value the operator already set: an env that
// pre-seeds known_hosts and exports its own GIT_SSH_COMMAND wins. When sshKey
// is non-empty and the operator has not exported GIT_SSH_COMMAND, the key is
// wired in as the clone identity with IdentitiesOnly so only it is offered.
func gitEnv(sshKey string) []string {
	env := os.Environ()
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if _, ok := os.LookupEnv("GIT_SSH_COMMAND"); !ok {
		ssh := "ssh -o StrictHostKeyChecking=accept-new -o BatchMode=yes"
		if sshKey != "" {
			ssh += fmt.Sprintf(" -i %q -o IdentitiesOnly=yes", sshKey)
		}
		env = append(env, "GIT_SSH_COMMAND="+ssh)
	}
	return env
}
