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
// recreate" — the Breaking-on-root domain of `diffContainerSpec`. The
// `TestSpecHashDomainPinsToDiffCellBreakingFields` test pins the two
// definitions together so they cannot silently drift apart. Issues #867,
// #990.
//
// Args is normalized to a non-nil empty slice on the in-payload side so a
// nil-vs-empty distinction in the source spec does not change the hash —
// containerd treats both as "no args". The Capabilities, Tmpfs, and
// Resources projections normalize nil pointers / slices to zero values so
// "field unset" and "field set to its zero value" hash identically
// (matches the equality semantics in `internal/controller/apply/diff.go`'s
// `capabilitiesEqual`, `tmpfsEqual`, and `resourcesEqual`).
type containerSpecHashPayload struct {
	Image                  string                  `json:"image"`
	Command                string                  `json:"command"`
	Args                   []string                `json:"args"`
	Privileged             bool                    `json:"privileged"`
	User                   string                  `json:"user"`
	ReadOnlyRootFilesystem bool                    `json:"readOnlyRootFilesystem"`
	Capabilities           capabilitiesHashPayload `json:"capabilities"`
	Tmpfs                  []tmpfsHashPayload      `json:"tmpfs"`
	Resources              resourcesHashPayload    `json:"resources"`
}

type capabilitiesHashPayload struct {
	Add  []string `json:"add"`
	Drop []string `json:"drop"`
}

type tmpfsHashPayload struct {
	Path      string   `json:"path"`
	SizeBytes int64    `json:"sizeBytes"`
	Options   []string `json:"options"`
}

type resourcesHashPayload struct {
	MemoryLimitBytes int64 `json:"memoryLimitBytes"`
	CPUShares        int64 `json:"cpuShares"`
	PidsLimit        int64 `json:"pidsLimit"`
}

// ComputeContainerSpecHash returns a hex-encoded SHA-256 over the
// containerSpecHashPayload projection of spec. Pure function; no
// containerd access. Same hash for root and non-root containers — the
// domain is identical (see containerSpecHashPayload).
func ComputeContainerSpecHash(spec intmodel.ContainerSpec) string {
	payload := containerSpecHashPayload{
		Image:                  spec.Image,
		Command:                spec.Command,
		Args:                   normalizeStrings(spec.Args),
		Privileged:             spec.Privileged,
		User:                   spec.User,
		ReadOnlyRootFilesystem: spec.ReadOnlyRootFilesystem,
		Capabilities:           projectCapabilities(spec.Capabilities),
		Tmpfs:                  projectTmpfs(spec.Tmpfs),
		Resources:              projectResources(spec.Resources),
	}
	// json.Marshal on a struct with a fixed field order is deterministic.
	// Errors are not possible here (payload is plain comparable types).
	buf, _ := json.Marshal(payload)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

// normalizeStrings replaces a nil slice with a non-nil empty slice so the
// JSON projection produces `[]` rather than `null` regardless of source
// nilness.
func normalizeStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func projectCapabilities(c *intmodel.ContainerCapabilities) capabilitiesHashPayload {
	if c == nil {
		return capabilitiesHashPayload{Add: []string{}, Drop: []string{}}
	}
	return capabilitiesHashPayload{
		Add:  normalizeStrings(c.Add),
		Drop: normalizeStrings(c.Drop),
	}
}

func projectTmpfs(t []intmodel.ContainerTmpfsMount) []tmpfsHashPayload {
	out := make([]tmpfsHashPayload, len(t))
	for i := range t {
		out[i] = tmpfsHashPayload{
			Path:      t[i].Path,
			SizeBytes: t[i].SizeBytes,
			Options:   normalizeStrings(t[i].Options),
		}
	}
	return out
}

func projectResources(r *intmodel.ContainerResources) resourcesHashPayload {
	if r == nil {
		return resourcesHashPayload{}
	}
	return resourcesHashPayload{
		MemoryLimitBytes: derefInt64(r.MemoryLimitBytes),
		CPUShares:        derefInt64(r.CPUShares),
		PidsLimit:        derefInt64(r.PidsLimit),
	}
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
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
