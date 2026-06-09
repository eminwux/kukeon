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

// SpecHashVersionLabelKey pins the *domain version* the SpecHashLabelKey value
// was computed under — the field set in containerSpecHashPayload. A bare hash
// cannot distinguish "the spec drifted" (genuine out-of-band tampering, which
// must refuse) from "the hashing algorithm changed" (a daemon upgrade widened
// the payload, which must NOT refuse). Without this label, any change to the
// hash domain made every pre-existing cell look tampered and stranded the
// whole fleet on the next start with no migration path. The guard now keys its
// refuse-vs-re-stamp decision on whether this version matches the running
// daemon's SpecHashDomainVersion. Issue #1171.
const SpecHashVersionLabelKey = "kukeon.io/spec-hash-version"

// SpecHashDomainVersion is the current hash-domain version. Bump it whenever
// containerSpecHashPayload's field set changes — the
// TestSpecHashDomainVersionPinsToPayload regression guard forces the bump in
// the same edit by pinning the version to the payload's reflected field set.
// History: "1" (issue #867, original domain) → "2" (#1001, widened the
// root-container diff to all spec fields) → "3" (#1155, added Volumes,
// Secrets, WorkingDir, SecurityOpts). A cell stamped under an older version
// is re-stamped from its authoritative on-disk spec on the next start rather
// than refused. Issue #1171.
const SpecHashDomainVersion = "3"

// containerSpecHashPayload is the deterministic projection of an
// intmodel.ContainerSpec that ComputeContainerSpecHash hashes. The field set
// must match exactly what `apply.DiffCell` classifies as "requires containerd
// recreate" — the Breaking-on-root domain of `diffContainerSpec`. The
// `TestSpecHashDomainPinsToDiffCellBreakingFields` test pins the two
// definitions together so they cannot silently drift apart. Issues #867,
// #990.
//
// Args (and SecurityOpts) is normalized to a non-nil empty slice on the
// in-payload side so a nil-vs-empty distinction in the source spec does not
// change the hash — containerd treats both as "no args". The Capabilities,
// Tmpfs, Resources, Volumes, and Secrets projections normalize nil pointers /
// slices to zero values so "field unset" and "field set to its zero value"
// hash identically (matches the equality semantics in
// `internal/controller/apply/diff.go`'s `capabilitiesEqual`, `tmpfsEqual`,
// `resourcesEqual`, `volumeMountsEqual`, and `secretsEqual`).
//
// WorkingDir, SecurityOpts, Volumes, and Secrets were added by issue #1154
// when their diff.go classification widened to Breaking-on-root.
type containerSpecHashPayload struct {
	Image                  string                  `json:"image"`
	Command                string                  `json:"command"`
	Args                   []string                `json:"args"`
	WorkingDir             string                  `json:"workingDir"`
	Privileged             bool                    `json:"privileged"`
	User                   string                  `json:"user"`
	ReadOnlyRootFilesystem bool                    `json:"readOnlyRootFilesystem"`
	Capabilities           capabilitiesHashPayload `json:"capabilities"`
	SecurityOpts           []string                `json:"securityOpts"`
	Tmpfs                  []tmpfsHashPayload      `json:"tmpfs"`
	Resources              resourcesHashPayload    `json:"resources"`
	Volumes                []volumeHashPayload     `json:"volumes"`
	Secrets                []secretHashPayload     `json:"secrets"`
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

type volumeHashPayload struct {
	Kind      string `json:"kind"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"readOnly"`
	SizeBytes int64  `json:"sizeBytes"`
	Mode      uint32 `json:"mode"`
}

// secretHashPayload flattens the intmodel.ContainerSecret reference set the
// runner bakes into the OCI spec at create — the env-injected Process.Env
// adds and the file-form Mounts. Only the reference is hashed (never the
// resolved value), matching the equality semantics in
// `internal/controller/apply/diff.go`'s `secretsEqual`. Issue #1154.
type secretHashPayload struct {
	Name      string                `json:"name"`
	FromFile  string                `json:"fromFile"`
	FromEnv   string                `json:"fromEnv"`
	MountPath string                `json:"mountPath"`
	SecretRef *secretRefHashPayload `json:"secretRef"`
}

type secretRefHashPayload struct {
	Name  string `json:"name"`
	Realm string `json:"realm"`
	Space string `json:"space"`
	Stack string `json:"stack"`
	Cell  string `json:"cell"`
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
		WorkingDir:             spec.WorkingDir,
		Privileged:             spec.Privileged,
		User:                   spec.User,
		ReadOnlyRootFilesystem: spec.ReadOnlyRootFilesystem,
		Capabilities:           projectCapabilities(spec.Capabilities),
		SecurityOpts:           normalizeStrings(spec.SecurityOpts),
		Tmpfs:                  projectTmpfs(spec.Tmpfs),
		Resources:              projectResources(spec.Resources),
		Volumes:                projectVolumes(spec.Volumes),
		Secrets:                projectSecrets(spec.Secrets),
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

// projectVolumes flattens a VolumeMount slice in declaration order (the
// equality semantics in diff.go's `volumeMountsEqual` are order-sensitive).
// An empty Kind is left as-is so it hashes identically to the diff layer's
// bare struct comparison.
func projectVolumes(v []intmodel.VolumeMount) []volumeHashPayload {
	out := make([]volumeHashPayload, len(v))
	for i := range v {
		out[i] = volumeHashPayload{
			Kind:      string(v[i].Kind),
			Source:    v[i].Source,
			Target:    v[i].Target,
			ReadOnly:  v[i].ReadOnly,
			SizeBytes: v[i].SizeBytes,
			Mode:      v[i].Mode,
		}
	}
	return out
}

// projectSecrets flattens a ContainerSecret slice in declaration order,
// mirroring `secretsEqual`'s order-sensitive, reference-only comparison.
func projectSecrets(s []intmodel.ContainerSecret) []secretHashPayload {
	out := make([]secretHashPayload, len(s))
	for i := range s {
		out[i] = secretHashPayload{
			Name:      s[i].Name,
			FromFile:  s[i].FromFile,
			FromEnv:   s[i].FromEnv,
			MountPath: s[i].MountPath,
			SecretRef: projectSecretRef(s[i].SecretRef),
		}
	}
	return out
}

func projectSecretRef(r *intmodel.ContainerSecretRef) *secretRefHashPayload {
	if r == nil {
		return nil
	}
	return &secretRefHashPayload{
		Name:  r.Name,
		Realm: r.Realm,
		Space: r.Space,
		Stack: r.Stack,
		Cell:  r.Cell,
	}
}

// specHashLabels is the single source of truth for the (hash, version) label
// pair every create and re-stamp path writes. Both labels move together so a
// record can never carry a hash from one domain and a version from another.
// Issue #1171.
func specHashLabels(spec intmodel.ContainerSpec) map[string]string {
	return map[string]string{
		SpecHashLabelKey:        ComputeContainerSpecHash(spec),
		SpecHashVersionLabelKey: SpecHashDomainVersion,
	}
}

// stampSpecHashOnLabels writes the SpecHashLabelKey + SpecHashVersionLabelKey
// pair into labels (allocating the map if nil) and returns it. Used by every
// root-container create path where the runner builds the labels map up front
// and passes it through ctr.BuildRootContainerSpec.
func stampSpecHashOnLabels(labels map[string]string, spec intmodel.ContainerSpec) map[string]string {
	if labels == nil {
		// specHashLabels already returns a fresh map carrying exactly the
		// (hash, version) pair — reuse it rather than allocate-then-copy.
		return specHashLabels(spec)
	}
	for k, v := range specHashLabels(spec) {
		labels[k] = v
	}
	return labels
}

// specHashReuseAction is the verdict of the versioned spec-hash guard for an
// existing containerd record on the reuse (stop/start) path. Issue #1171.
type specHashReuseAction int

const (
	// specHashReuseAsIs: the stamped version matches the running daemon's
	// domain and the stamped hash matches (or is absent) — reuse the snapshot
	// without touching the labels.
	specHashReuseAsIs specHashReuseAction = iota
	// specHashRestamp: the stamped version is legacy-absent or from a prior
	// hash domain. The on-disk spec (which `kuke apply -f` writes back) is
	// authoritative, so there is nothing to recover — re-stamp the fresh
	// (hash, version) pair and reuse. Tamper detection is suspended for this
	// one start across the upgrade boundary; an upgrade is already a trust
	// boundary, so that is acceptable. This arm turns a domain bump from a
	// fleet-wide outage into a one-time silent re-stamp, and auto-heals cells
	// stamped with a bare pre-#1171 hash.
	specHashRestamp
	// specHashRefuse: the stamped version matches the current domain but the
	// hash diverges — genuine out-of-band tampering. The caller refuses with
	// ErrCellSpecHashDrift.
	specHashRefuse
)

// classifySpecHashReuse turns an existing record's labels and the freshly
// computed desiredHash into the three-way reuse verdict. The version gate is
// what distinguishes a changed hashing algorithm (re-stamp) from a changed
// spec (refuse): a hash mismatch only refuses when the record was stamped
// under the running daemon's own domain version. Issue #1171.
func classifySpecHashReuse(labels map[string]string, desiredHash string) specHashReuseAction {
	if labels[SpecHashVersionLabelKey] != SpecHashDomainVersion {
		// Legacy-absent or prior-domain stamp — the on-disk spec is the
		// source of truth, so re-stamp rather than refuse.
		return specHashRestamp
	}
	stampedHash := labels[SpecHashLabelKey]
	if stampedHash != "" && stampedHash != desiredHash {
		return specHashRefuse
	}
	return specHashReuseAsIs
}

// restampSpecHashLabels writes the current (hash, version) label pair onto an
// existing containerd record on the reuse path. Best-effort by contract: the
// labels are a tripwire, not a source of truth, so a write failure is logged
// and the start proceeds (the next start re-attempts the re-stamp). Issue
// #1171.
func (r *Exec) restampSpecHashLabels(
	container containerd.Container,
	namespace, ctrContainerID, cellName string,
	spec intmodel.ContainerSpec,
) {
	nsCtx := namespaces.WithNamespace(r.ctx, namespace)
	if _, err := container.SetLabels(nsCtx, specHashLabels(spec)); err != nil {
		r.logger.WarnContext(
			r.ctx,
			"failed to re-stamp spec-hash labels on reuse path, continuing (labels are a tripwire, not source of truth)",
			"id",
			ctrContainerID,
			"cell",
			cellName,
			"err",
			err.Error(),
		)
		return
	}
	r.logger.InfoContext(r.ctx,
		"re-stamped spec-hash to current domain version on reuse path",
		"id", ctrContainerID, "cell", cellName,
		"version", SpecHashDomainVersion, "spec-hash", ComputeContainerSpecHash(spec))
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
	switch classifySpecHashReuse(existingLabels, desiredHash) {
	case specHashRefuse:
		onDiskHash := existingLabels[SpecHashLabelKey]
		return false, fmt.Errorf(
			"%w: cell %q: container %q carries spec-hash %q but cell spec hashes to %q — "+
				"run `kuke apply -f` to reconcile",
			errdefs.ErrCellSpecHashDrift, cellName, ctrContainerID, onDiskHash, desiredHash,
		)
	case specHashRestamp:
		r.restampSpecHashLabels(container, namespace, ctrContainerID, cellName, spec)
	case specHashReuseAsIs:
		// Stamped (hash, version) match the running domain — reuse untouched.
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
