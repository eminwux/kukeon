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

package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// SpecHashLabelKey is the containerd container label that pins the OCI spec
// to the CellSpec it was created from. StartCell's existing-container branch
// reads the label, recomputes the hash from the on-disk CellSpec, and refuses
// to resume a record whose hash diverges — the operator is told to reconcile
// via `kuke apply -f`. The supported `kuke apply -f` flow re-stamps the label
// inside the same RecreateCell / UpdateCell transaction that rewrites
// containerd state, so steady-state restarts always match. Issue #867.
const SpecHashLabelKey = "kukeon.io/spec-hash"

// containerSpecHashPayload is the deterministic projection of an
// intmodel.ContainerSpec that ComputeContainerSpecHash hashes. The field set
// must match exactly what `apply.DiffCell` classifies as "requires containerd
// recreate" — today that is image/command/args (the domain
// `rootContainerSpecChanged` and `containerSpecChanged` both branch on). The
// `TestSpecHashDomainPinsToDiffCellBreakingFields` test pins the two
// definitions together so they cannot silently drift apart. Issue #867
// AC #5 / #6.
//
// Args is normalized to a non-nil empty slice on the in-payload side so a
// nil-vs-empty distinction in the source spec does not change the hash —
// containerd treats both as "no args".
type containerSpecHashPayload struct {
	Image   string   `json:"image"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// ComputeContainerSpecHash returns a hex-encoded SHA-256 over the
// containerSpecHashPayload projection of spec. Pure function; no
// containerd access. Same hash for root and non-root containers — the
// domain is identical (see containerSpecHashPayload).
func ComputeContainerSpecHash(spec intmodel.ContainerSpec) string {
	args := spec.Args
	if args == nil {
		args = []string{}
	}
	payload := containerSpecHashPayload{
		Image:   spec.Image,
		Command: spec.Command,
		Args:    args,
	}
	// json.Marshal on a struct with a fixed field order is deterministic.
	// Errors are not possible here (payload is plain strings + []string).
	buf, _ := json.Marshal(payload)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// stampSpecHashOnLabels writes the SpecHashLabelKey into labels (allocating
// the map if nil) and returns it. Used by every root-container create path
// where the runner builds the labels map up front and passes it through
// ctr.BuildRootContainerSpec.
func stampSpecHashOnLabels(labels map[string]string, spec intmodel.ContainerSpec) map[string]string {
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	labels[SpecHashLabelKey] = ComputeContainerSpecHash(spec)
	return labels
}

// reuseOrRefuseExistingChildContainer is the child-container counterpart of
// the spec-hash guard StartCell applies inline for the root container. It
// looks up the existing containerd record for ctrContainerID and:
//
//   - returns (false, nil) when the record is absent (caller takes the
//     fresh-create path);
//   - returns an error wrapping ErrCellSpecHashDrift when the record carries
//     a kukeon.io/spec-hash label that disagrees with the desired spec hash;
//   - returns (true, nil) on match or legacy-no-label, after dropping a
//     stale Created/Stopped task so the caller's StartContainer can create
//     a fresh task.
//
// Issue #867.
func (r *Exec) reuseOrRefuseExistingChildContainer(
	namespace, ctrContainerID, cellName string,
	spec intmodel.ContainerSpec,
) (bool, error) {
	container, err := r.ctrClient.GetContainer(namespace, ctrContainerID)
	if err != nil {
		if errors.Is(err, errdefs.ErrContainerNotFound) {
			return false, nil
		}
		// Surface other errors to the caller so a transient lookup
		// failure doesn't silently widen into a destructive recreate.
		return false, fmt.Errorf("failed to check existing container %s: %w", ctrContainerID, err)
	}
	desiredHash := ComputeContainerSpecHash(spec)
	nsCtx := namespaces.WithNamespace(r.ctx, namespace)
	existingLabels, labelErr := container.Labels(nsCtx)
	if labelErr != nil {
		r.logger.WarnContext(r.ctx,
			"failed to read existing container labels, treating as match for reuse",
			"id", ctrContainerID, "cell", cellName, "err", labelErr.Error())
	}
	onDiskHash := existingLabels[SpecHashLabelKey]
	if onDiskHash != "" && onDiskHash != desiredHash {
		return false, fmt.Errorf(
			"%w: cell %q: container %q carries spec-hash %q but cell spec hashes to %q — "+
				"run `kuke apply -f` to reconcile",
			errdefs.ErrCellSpecHashDrift, cellName, ctrContainerID, onDiskHash, desiredHash,
		)
	}
	// Drop any stale task so StartContainer can create a fresh one. The
	// container's snapshot is preserved across the call — only the task
	// (a transient runtime entity) goes away.
	if task, taskErr := container.Task(nsCtx, nil); taskErr == nil {
		if _, deleteTaskErr := task.Delete(nsCtx, containerd.WithProcessKill); deleteTaskErr != nil {
			r.logger.WarnContext(r.ctx,
				"failed to delete stale task on existing container, continuing",
				"id", ctrContainerID, "cell", cellName, "err", deleteTaskErr.Error())
		}
	}
	r.logger.InfoContext(r.ctx,
		"reusing existing container (spec-hash matched)",
		"id", ctrContainerID, "cell", cellName, "spec-hash", desiredHash)
	return true, nil
}
