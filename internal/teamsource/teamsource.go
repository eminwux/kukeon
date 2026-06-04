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

// Package teamsource resolves a kuketeams.io/v1 ProjectTeam's pinned `source`
// reference to a local clone of the agents repo and loads the Role, Harness,
// and ImageCatalog documents the project's roster references (epic #792, step
// 2 #1041). Materialization is cache-keyed by the pinned
// `<owner>/<repo>@vX.Y.Z` reference: an existing cache directory at the pinned
// version is reused without re-cloning, and a missing cache is cloned via
// `git clone --depth=1 --branch <version> --no-tags` into the pinned ref
// exactly. Document loading delegates to the existing kuketeams parser, so
// parse errors carry the offending file path verbatim. No daemon is required —
// the package is unit-testable against a local fixture remote.
package teamsource

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/kuketeams"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// sourcePattern matches a pinned-exact `<owner>/<repo>@vX.Y.Z` reference,
// mirroring internal/kuketeams's parser pattern. Floating refs (`@main`) and
// bare tags (`@v1`) are rejected.
var sourcePattern = regexp.MustCompile(
	`^([A-Za-z0-9._-]+/[A-Za-z0-9._-]+)@(v\d+\.\d+\.\d+([-+][0-9A-Za-z.-]+)?)$`,
)

// cacheDirPerm is the cache root and per-source clone mode — operator-only,
// matching teamhost's drop-in directory perm. Secret material never lives in
// the cache (the agents source is the operator's own public repo at a pinned
// tag), but keeping the perm consistent avoids surprise for an operator
// inspecting ~/.kuke.
const cacheDirPerm = 0o700

// Source is a parsed pinned-exact agents reference.
type Source struct {
	// Raw is the original `<owner>/<repo>@vX.Y.Z` string, trimmed.
	Raw string
	// OwnerRepo is the `<owner>/<repo>` half — the lookup key against
	// TeamsConfig.spec.sources.
	OwnerRepo string
	// Version is the `vX.Y.Z[<+|->suffix]` half — the ref the cache directory
	// pins to and the value passed to `git clone --branch`.
	Version string
}

// ParseSource parses raw into a Source. The input is trimmed and matched
// against the pinned-exact `<owner>/<repo>@vX.Y.Z` pattern. Floating refs and
// bare tags surface errdefs.ErrTeamSourceInvalid so callers reuse the same
// sentinel the parser uses.
func ParseSource(raw string) (Source, error) {
	trimmed := strings.TrimSpace(raw)
	m := sourcePattern.FindStringSubmatch(trimmed)
	if m == nil {
		return Source{}, fmt.Errorf("%w (got %q)", errdefs.ErrTeamSourceInvalid, raw)
	}
	return Source{Raw: trimmed, OwnerRepo: m[1], Version: m[2]}, nil
}

// CloneURL returns the clone URL for src by looking up its `<owner>/<repo>`
// key in tc.spec.sources. An unmapped or blank-value key surfaces
// errdefs.ErrTeamSourceURLNotMapped with the missing key named in the wrapper
// — the AC's "hard-error naming the missing key" contract.
func CloneURL(tc *model.TeamsConfig, src Source) (string, error) {
	if tc == nil {
		return "", fmt.Errorf("%w: %q", errdefs.ErrTeamSourceURLNotMapped, src.OwnerRepo)
	}
	url, ok := tc.Spec.Sources[src.OwnerRepo]
	if !ok || strings.TrimSpace(url) == "" {
		return "", fmt.Errorf("%w: %q", errdefs.ErrTeamSourceURLNotMapped, src.OwnerRepo)
	}
	return strings.TrimSpace(url), nil
}

// Cache is the on-disk cache root for materialized agents sources. Each pinned
// `<owner>/<repo>@vX.Y.Z` reference lives at <Base>/<owner>/<repo>@<version>,
// so the version is encoded in the leaf directory name and the existence of
// that directory is sufficient to decide reuse — the cache key is the pinned
// reference itself.
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
	return filepath.Join(c.Base, src.OwnerRepo+"@"+src.Version)
}

// Materialize ensures src is present at its cache path and returns that path.
// An existing cache directory at the pinned reference is reused as-is; a
// missing one is cloned via `git clone --depth=1 --branch <version> --no-tags
// <cloneURL> <tmp>` into a sibling temp directory and renamed into place
// atomically, so an interrupted clone never leaves a half-materialized cache
// dir behind. The non-interactive git env (GIT_TERMINAL_PROMPT=0,
// StrictHostKeyChecking=accept-new BatchMode=yes SSH) mirrors kuketty's
// processRepos so a first-time clone of an unseen host never hangs.
func (c Cache) Materialize(ctx context.Context, src Source, cloneURL string) (string, error) {
	dst := c.Path(src)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat cache dir %q: %w", dst, err)
	}

	parent := filepath.Dir(dst)
	if err := os.MkdirAll(parent, cacheDirPerm); err != nil {
		return "", fmt.Errorf("create cache parent %q: %w", parent, err)
	}

	// Reserve a unique temp name as a sibling of dst (so the rename stays on
	// the same filesystem), then remove it so `git clone` creates the
	// directory itself — `git clone <url> <dir>` requires the target to not
	// exist or to be empty, and an empty dir works on every platform.
	tmp, err := os.MkdirTemp(parent, ".clone-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp clone dir in %q: %w", parent, err)
	}
	if rmErr := os.Remove(tmp); rmErr != nil {
		return "", fmt.Errorf("clear temp clone dir %q: %w", tmp, rmErr)
	}

	if cloneErr := runGit(ctx, "", "clone",
		"--depth=1", "--no-tags",
		"--branch", src.Version,
		cloneURL, tmp,
	); cloneErr != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("clone %s@%s: %w", src.OwnerRepo, src.Version, cloneErr)
	}

	if renameErr := os.Rename(tmp, dst); renameErr != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("rename temp clone %q to %q: %w", tmp, dst, renameErr)
	}
	return dst, nil
}

// Bundle is the materialized agents source plus the Role / Harness /
// ImageCatalog documents the project's roster references.
type Bundle struct {
	// Source is the parsed pinned reference (raw, owner/repo, version).
	Source Source
	// CacheDir is the on-disk clone root — passed to step 3's render pipeline
	// when it needs to read blueprint templates or other repo-relative paths.
	CacheDir string
	// Roles maps the role ref (ProjectTeam.spec.roles[].ref) to its loaded Role.
	Roles map[string]*model.Role
	// Harnesses maps the harness name (ProjectTeam.spec.defaults.harnesses[])
	// to its loaded Harness.
	Harnesses map[string]*model.Harness
	// ImageCatalog is the single harnesses/images.yaml in the agents source.
	ImageCatalog *model.ImageCatalog
}

// Resolve parses pt.Spec.Source, looks up the clone URL in tc.spec.sources,
// materializes the agents source under cache, and loads every Role referenced
// by pt.Spec.Roles, every Harness referenced by pt.Spec.Defaults.Harnesses,
// and the ImageCatalog. Parse errors surface with the offending file path —
// the AC's "surface parse errors with the offending file path" contract.
func Resolve(
	ctx context.Context,
	cache Cache,
	tc *model.TeamsConfig,
	pt *model.ProjectTeam,
) (*Bundle, error) {
	src, err := ParseSource(pt.Spec.Source)
	if err != nil {
		return nil, err
	}
	url, err := CloneURL(tc, src)
	if err != nil {
		return nil, err
	}
	dir, err := cache.Materialize(ctx, src, url)
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

// RolePath is the on-disk role.yaml location for ref under cacheDir.
func RolePath(cacheDir, ref string) string {
	return filepath.Join(cacheDir, "roles", ref, "role.yaml")
}

// HarnessPath is the on-disk harness.yaml location for name under cacheDir.
func HarnessPath(cacheDir, name string) string {
	return filepath.Join(cacheDir, "harnesses", name, "harness.yaml")
}

// ImageCatalogPath is the on-disk harnesses/images.yaml location under cacheDir.
func ImageCatalogPath(cacheDir string) string {
	return filepath.Join(cacheDir, "harnesses", "images.yaml")
}

// LoadRole reads <cacheDir>/roles/<ref>/role.yaml, parses it via the
// kuketeams parser, and returns the typed Role. A document of the wrong kind
// surfaces ErrTeamRoleFileKind; parse errors carry the file path verbatim.
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
// yes/no prompt. dir is the working directory ("" for the process cwd).
// Combined output is folded into the error for actionable diagnostics.
func runGit(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = gitEnv()
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
// pre-seeds known_hosts and exports its own GIT_SSH_COMMAND wins.
func gitEnv() []string {
	env := os.Environ()
	if _, ok := os.LookupEnv("GIT_TERMINAL_PROMPT"); !ok {
		env = append(env, "GIT_TERMINAL_PROMPT=0")
	}
	if _, ok := os.LookupEnv("GIT_SSH_COMMAND"); !ok {
		env = append(env, "GIT_SSH_COMMAND=ssh -o StrictHostKeyChecking=accept-new -o BatchMode=yes")
	}
	return env
}
