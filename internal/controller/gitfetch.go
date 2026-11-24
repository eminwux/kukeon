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

package controller

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/internal/team/gitremote"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

const (
	// gitFetchRootMode lets every user reach their own staging by its
	// unguessable name, and none list the others.
	gitFetchRootMode os.FileMode = 0o711
	// gitFetchMaxAge bounds a staging kuke never released (a killed kuke):
	// every stage removes the ones older than this.
	gitFetchMaxAge  = 10 * time.Minute
	gitFetchIDBytes = 8
	// gitFetchRepo is the name of the one repo a fetch bundle plans.
	gitFetchRepo = gitplan.HostFetchRepo
)

var gitFetchID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// GitFetchStage is a staged fetch credential (issue #1643, D10).
type GitFetchStage struct {
	Staged  bool
	ID, Dir string
}

// gitFetchRoot is the directory, beside kukeond's socket, that holds the
// stagings. kuke reaches the socket directory to dial, so it reaches this one.
func (b *Exec) gitFetchRoot() string {
	if b.opts.GitFetchDir != "" {
		return b.opts.GitFetchDir
	}
	runDir := filepath.Dir(ctr.DefaultSecretsStagingDir)
	if b.opts.KukeondSocket != "" {
		runDir = filepath.Dir(b.opts.KukeondSocket)
	}
	return filepath.Join(runDir, consts.KukeonGitFetchSubdir)
}

// StageGitFetch stages the stored credential a fetch kuke makes on the host
// uses for address (issue #1643, AC9), owned by uid: the identity identityRef
// names when set, else the realm's default git identity when it has a
// credential for the address's host. With neither the result is not Staged and
// kuke fetches with the caller's own git configuration. The bundle is the one
// a cell gets (gitcred.BuildAt), rooted at its staging directory; only the
// credential is planned — a fetch needs no author or signing key.
func (b *Exec) StageGitFetch(
	realm, address string, identityRef *v1beta1.ContainerSecretRef, uid int,
) (GitFetchStage, error) {
	realm = teamRealm(realm)
	if !config.ValidName(realm) {
		return GitFetchStage{}, fmt.Errorf("%w: invalid realm name %q", errdefs.ErrGitFetchStaging, realm)
	}
	if uid < 0 {
		return GitFetchStage{}, fmt.Errorf("%w: invalid caller uid", errdefs.ErrGitFetchStaging)
	}
	if _, err := gitremote.URL(address); err != nil {
		return GitFetchStage{}, fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	url := gitcred.CloneURL(address)
	spec, ok, err := b.gitFetchSpec(realm, url, identityRef)
	if err != nil || !ok {
		return GitFetchStage{}, err
	}

	root := b.gitFetchRoot()
	if err = os.MkdirAll(root, gitFetchRootMode); err != nil {
		return GitFetchStage{}, fmt.Errorf("%w: create %s: %w", errdefs.ErrGitFetchStaging, root, err)
	}
	if err = os.Chmod(root, gitFetchRootMode); err != nil {
		return GitFetchStage{}, fmt.Errorf("%w: chmod %s: %w", errdefs.ErrGitFetchStaging, root, err)
	}
	b.pruneGitFetches(root, time.Now())
	id, err := newGitFetchID()
	if err != nil {
		return GitFetchStage{}, fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	dir := filepath.Join(root, id)
	bundle, err := gitcred.BuildAt(spec, func(ref intmodel.ContainerSecretRef) (intmodel.SecretSpec, error) {
		return secretstore.Read(b.opts.RunPath, ref)
	}, dir)
	if err != nil {
		return GitFetchStage{}, fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	if err = gitcred.Stage(dir, bundle); err != nil {
		_ = os.RemoveAll(dir)
		return GitFetchStage{}, fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	// Owned by the caller and by no group: ssh refuses a key others can read.
	if err = gitcred.Chown(dir, uid); err != nil {
		_ = os.RemoveAll(dir)
		return GitFetchStage{}, fmt.Errorf("%w: chown: %w", errdefs.ErrGitFetchStaging, err)
	}
	return GitFetchStage{Staged: true, ID: id, Dir: dir}, nil
}

// gitFetchSpec is the one-repo container a fetch bundle renders, and whether
// any credential applies to url.
func (b *Exec) gitFetchSpec(
	realm, url string, identityRef *v1beta1.ContainerSecretRef,
) (intmodel.ContainerSpec, bool, error) {
	expander := &gitIdentityExpander{r: b.runner}
	repo := intmodel.ContainerRepo{Name: gitFetchRepo, URL: url}
	spec := intmodel.ContainerSpec{}
	if identityRef != nil {
		ref := *identityRef
		if ref.Realm == "" {
			ref.Realm = realm
		}
		expanded := expander.repo(v1beta1.RenderOriginConfig, gitFetchRepo, &v1beta1.RepoGit{IdentityRef: &ref})
		if err := expander.repoErrs[v1beta1.RenderOriginConfig][gitFetchRepo]; err != nil {
			return spec, false, fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
		}
		if expanded == nil || expanded.Credential == nil {
			return spec, false, nil // An author-only identity authenticates nothing.
		}
		repo.Git = apischeme.RepoGitToInternal(&v1beta1.RepoGit{Credential: expanded.Credential})
		spec.Repos = []intmodel.ContainerRepo{repo}
		return spec, true, nil
	}
	got, err := b.runner.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: realm}})
	if errors.Is(err, errdefs.ErrRealmNotFound) {
		return spec, false, nil
	}
	if err != nil {
		return spec, false, fmt.Errorf("%w: read realm %q: %w", errdefs.ErrGitFetchStaging, realm, err)
	}
	doc, err := apischeme.BuildRealmExternalFromInternal(got, apischeme.VersionV1Beta1)
	if err != nil {
		return spec, false, fmt.Errorf("%w: convert realm %q: %w", errdefs.ErrGitFetchStaging, realm, err)
	}
	if doc.Spec.Defaults == nil || doc.Spec.Defaults.Git == nil {
		return spec, false, nil
	}
	// A realm default that cannot be read is no credential: the fetch runs as
	// it would without one, as a cell's does when its git connector is
	// optional.
	expanded := expander.container(v1beta1.RenderOriginRealm, doc.Spec.Defaults.Git)
	git := apischeme.GitToInternal(&v1beta1.ContainerGit{Credentials: expanded.Credentials})
	if !gitcred.HasCredential(git, url) {
		return spec, false, nil
	}
	spec.Git = git
	spec.Repos = []intmodel.ContainerRepo{repo}
	return spec, true, nil
}

// ReleaseGitFetch removes one staging by its id (issue #1643, D10). The id is
// checked as kukeond's own hex name and never joined as a path; a staging
// owned by another user is refused unless the caller is root.
func (b *Exec) ReleaseGitFetch(id string, uid int) error {
	if !gitFetchID.MatchString(id) {
		return fmt.Errorf("%w: invalid staging id", errdefs.ErrGitFetchStaging)
	}
	dir := filepath.Join(b.gitFetchRoot(), id)
	info, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && uid != 0 && int(st.Uid) != uid {
		return fmt.Errorf("%w: staging %s belongs to another user", errdefs.ErrGitFetchStaging, id)
	}
	if err = os.RemoveAll(dir); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrGitFetchStaging, err)
	}
	return nil
}

// pruneGitFetches removes the stagings older than gitFetchMaxAge.
func (b *Exec) pruneGitFetches(root string, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !gitFetchID.MatchString(entry.Name()) {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && now.Sub(info.ModTime()) > gitFetchMaxAge {
			_ = os.RemoveAll(filepath.Join(root, entry.Name()))
		}
	}
}

func newGitFetchID() (string, error) {
	raw := make([]byte, gitFetchIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
