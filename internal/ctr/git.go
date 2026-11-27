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

package ctr

import (
	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
)

// ContainerGitStagingDir returns the host directory a container's rendered git
// bundle is staged in. It sits under the secrets staging root — a tmpfs on a
// standard host — so key material never reaches persistent storage outside the
// daemon's own secrets tree. stagingRoot is that root (SecretsStagingDir);
// empty means DefaultSecretsStagingDir.
func ContainerGitStagingDir(spec intmodel.ContainerSpec, stagingRoot string) string {
	containerdID := spec.ContainerdID
	if containerdID == "" {
		containerdID = spec.ID
	}
	if stagingRoot == "" {
		stagingRoot = DefaultSecretsStagingDir
	}
	return gitcred.StagingDir(stagingRoot, containerdID)
}

// StageContainerGit renders the container's git block (identity, stored
// credentials, signing, per-source overrides) into its staging directory and
// returns the read-only bind mount that exposes it at gitplan.MountPath, or nil
// when the container declares no git config. Secrets are read from the
// daemon's secrets tree under runPath and staged under stagingRoot (see
// ContainerGitStagingDir). Staging is idempotent: the directory's contents are
// replaced on every call, which is what lets a start re-create them after a
// reboot wiped the tmpfs.
func StageContainerGit(spec intmodel.ContainerSpec, runPath, stagingRoot string) (*intmodel.VolumeMount, error) {
	if !gitcred.Needed(spec) {
		return nil, nil //nolint:nilnil // no git block, no mount: the documented no-op.
	}
	bundle, err := gitcred.Build(spec, func(ref intmodel.ContainerSecretRef) (intmodel.SecretSpec, error) {
		return secretstore.Read(runPath, ref)
	})
	if err != nil {
		return nil, err
	}
	dir := ContainerGitStagingDir(spec, stagingRoot)
	if err = gitcred.Stage(dir, bundle); err != nil {
		return nil, err
	}
	return &intmodel.VolumeMount{Source: dir, Target: gitplan.MountPath, ReadOnly: true}, nil
}
