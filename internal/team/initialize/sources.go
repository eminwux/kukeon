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
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/team/gitremote"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const (
	sha1CommitLength   = 40
	sha256ObjectFormat = "sha256"
	sha256CommitLength = 64
)

// checkSourceURL validates a source's remote before any network operation;
// the diagnostic never repeats the URL.
func checkSourceURL(source config.Source) error {
	if _, err := gitremote.URL(source.URL); err != nil {
		return fmt.Errorf("source %q: %w", source.Name, err)
	}
	return nil
}

// fetched is the definition trees of the sources a composition reads, and
// the commit each was fetched at. Temporary checkouts do not become runtime
// clone paths and are removed by cleanup.
type fetched struct {
	trees   map[string]fs.FS
	commits map[string]string
	cleanup func()
	// injected marks trees a test or embedder supplied: nothing is fetched.
	injected bool
}

// neededSources names the sources a composition reads definitions from: every
// selected role's source and the sources of the harnesses it pairs with, plus
// the project source when project is set.
func neededSources(d config.Defaults, project bool) map[string]bool {
	needed := map[string]bool{}
	for _, source := range d.Sources {
		if project && source.Name == config.ProjectSourceName {
			needed[source.Name] = true
		}
	}
	for _, role := range d.Roles {
		needed[role.Source] = true
		for _, harness := range d.Harnesses {
			if role.Selects(harness) {
				needed[harness.Source] = true
			}
		}
	}
	return needed
}

// identity prepares the context one source is fetched under (issue #1643,
// D10): the git identity kukeond staged for it, or ctx as it is. release
// undoes it.
type identity func(ctx context.Context, source config.Source) (context.Context, func(), error)

// noIdentity fetches with the caller's own git transport.
func noIdentity(ctx context.Context, _ config.Source) (context.Context, func(), error) {
	return ctx, func() {}, nil
}

// withIdentity asks kukeond to stage the git identity a fetch of source
// needs: the one the source names, else the realm's git default for its
// host. Staged, the returned context fetches with it alone; not staged (no
// identity applies), it is ctx and the fetch uses the caller's transport.
// release stops the private agent and releases the staging.
func withIdentity(
	ctx context.Context, client Client, realm string, source config.Source,
) (context.Context, func(), error) {
	req := kukeonv1.GitFetchRequest{Realm: realm, Address: source.URL}
	if source.Git != nil && source.Git.IdentityRef != nil {
		ref := source.Git.IdentityRef
		req.IdentityRef = &api.ContainerSecretRef{
			Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
		}
	}
	staged, err := client.StageGitFetch(ctx, req)
	if err != nil {
		return nil, nil, fmt.Errorf("source %q: stage its git identity: %w", source.Name, err)
	}
	if !staged.Staged {
		return ctx, func() {}, nil
	}
	releaseStaging := func() {
		_ = client.ReleaseGitFetch(context.WithoutCancel(ctx), staged.ID)
	}
	out, stop, err := gitremote.WithStagedIdentity(ctx, staged.Dir)
	if err != nil {
		releaseStaging()
		return nil, nil, fmt.Errorf("source %q: %w", source.Name, err)
	}
	return out, func() {
		stop()
		releaseStaging()
	}, nil
}

// identityOf is withIdentity bound to a client and realm.
func identityOf(client Client, realm string) identity {
	if client == nil {
		return noIdentity
	}
	if realm == "" {
		realm = config.DefaultRealm
	}
	return func(ctx context.Context, source config.Source) (context.Context, func(), error) {
		return withIdentity(ctx, client, realm, source)
	}
}

// fetchSources fetches the needed sources of d at their declared revision,
// each under the identity stage prepares. A source already in have is
// reused, not fetched again.
func fetchSources(
	ctx context.Context, stage identity, d config.Defaults, needed map[string]bool, have *fetched,
) (*fetched, error) {
	root, err := os.MkdirTemp("", "kuke-source-trees-*")
	if err != nil {
		return nil, err
	}
	out := &fetched{trees: map[string]fs.FS{}, commits: map[string]string{}}
	out.cleanup = func() { _ = os.RemoveAll(root) }
	for i, source := range d.Sources {
		if !needed[source.Name] {
			continue
		}
		if have != nil && have.trees[source.Name] != nil {
			out.trees[source.Name] = have.trees[source.Name]
			out.commits[source.Name] = have.commits[source.Name]
			continue
		}
		dir := filepath.Join(root, strconv.Itoa(i))
		commit, fetchErr := fetchStaged(ctx, stage, source, dir)
		if fetchErr != nil {
			out.cleanup()
			return nil, fetchErr
		}
		out.trees[source.Name] = os.DirFS(dir)
		out.commits[source.Name] = commit
	}
	return out, nil
}

// defaultBranch discovers a source's default branch under the identity stage
// prepares.
func defaultBranch(ctx context.Context, stage identity, source config.Source) (string, error) {
	if _, err := gitremote.URL(source.URL); err != nil {
		return "", err
	}
	ctx, release, err := stage(ctx, source)
	if err != nil {
		return "", err
	}
	defer release()
	return gitremote.DefaultBranch(ctx, source.URL)
}

// fetchStaged fetches one source under the identity stage prepares.
func fetchStaged(ctx context.Context, stage identity, source config.Source, dir string) (string, error) {
	if stage == nil {
		stage = noIdentity
	}
	ctx, release, err := stage(ctx, source)
	if err != nil {
		return "", err
	}
	defer release()
	return fetchSource(ctx, source, dir)
}

// fetchSource shallow-fetches one source into dir and returns the commit it
// checked out. The remote is the one a staged identity rewrote the address
// to, else the address expanded.
func fetchSource(ctx context.Context, source config.Source, dir string) (string, error) {
	url := gitremote.Remote(ctx, source.URL)
	ref := source.Commit
	if source.Branch != "" {
		ref = "refs/heads/" + source.Branch
	}
	if source.Tag != "" {
		ref = "refs/tags/" + source.Tag
	}
	objectFormat, err := sourceObjectFormat(ctx, url, ref, source.Commit)
	if err != nil {
		return "", fmt.Errorf("materialize source %q: %w", source.Name, err)
	}
	commands := [][]string{
		{"init", "--quiet", "--object-format=" + objectFormat, dir},
		{"-C", dir, "remote", "add", "--", "origin", url},
		{"-C", dir, "fetch", "--quiet", "--depth=1", "--", "origin", ref},
		{"-C", dir, "checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	for _, args := range commands {
		if _, runErr := gitremote.Run(ctx, args...); runErr != nil {
			// Git stderr and URLs may contain credentials; never forward them.
			return "", fmt.Errorf("materialize source %q: %w", source.Name, runErr)
		}
	}
	head, err := gitremote.Run(ctx, "-C", dir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("materialize source %q: %w", source.Name, err)
	}
	return strings.TrimSpace(string(head)), nil
}

// sourceObjectFormat pins initialization independently of Git's configured default.
// Commit IDs carry their format; named refs require the remote's advertised ID.
func sourceObjectFormat(ctx context.Context, remote, ref, commit string) (string, error) {
	oid := commit
	if oid == "" {
		if ref == "" {
			ref = "HEAD"
		}
		output, err := gitremote.Run(ctx, "ls-remote", "--exit-code", "--", remote, ref)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(output), "\n") {
			hash, name, ok := strings.Cut(line, "\t")
			if ok && name == ref {
				oid = hash
				break
			}
		}
	}
	if _, err := hex.DecodeString(oid); err == nil {
		switch len(oid) {
		case sha1CommitLength:
			return "sha1", nil
		case sha256CommitLength:
			return sha256ObjectFormat, nil
		}
	}
	return "", errors.New("source: cannot determine revision object format (Git output withheld)")
}
