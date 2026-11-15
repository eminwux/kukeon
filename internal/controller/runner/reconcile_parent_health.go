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
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// The ReconcileXHealth methods are the daemon-tick counterparts of
// RefreshRealm/Space/Stack for the parent levels (#1381), extending the
// ReconcileCell pattern (#853) that keeps Cell cgroupReady current. They
// re-probe only the health observations — Realm cgroupReady +
// containerdNamespaceReady, Space/Stack cgroupReady — and persist when one
// flipped, in either direction. Unlike RefreshX they never re-derive State or
// rebuild the status from scratch, so every other status field (paths,
// SubtreeControllers, lifecycle timestamps, Reason/Message) is written back
// exactly as read.
//
// A probe that errors is not an observation: the field keeps its last
// persisted value and nothing is written for it, so a transient failure
// neither marks a resource healthy nor flaps it unhealthy. Persists ride the
// same stale-generation guard as the refresh path, so a concurrent spec
// writer wins and the next tick re-observes. Each method re-reads the
// document before probing: the listing the caller walked may be a full pass
// old, and persisting a stale copy could clobber a fresher status write or
// rewrite the metadata of a resource deleted since. A vanished resource
// surfaces as the level's NotFound error for the caller to skip.

// ReconcileRealmHealth re-probes the realm's cgroup and containerd namespace
// and persists cgroupReady / containerdNamespaceReady when either changed.
// Returns whether a status write landed.
func (r *Exec) ReconcileRealmHealth(realm intmodel.Realm) (bool, error) {
	realm, err := r.GetRealm(realm)
	if err != nil {
		return false, err
	}
	changed := false

	cgroupExists, err := r.ExistsCgroup(realm)
	switch {
	case err != nil:
		r.logger.DebugContext(r.ctx, "reconcile: realm cgroup probe failed",
			"realm", realm.Metadata.Name, "error", err)
	case cgroupExists != realm.Status.CgroupReady:
		realm.Status.CgroupReady = cgroupExists
		changed = true
	}

	namespace := realm.Spec.Namespace
	if namespace == "" {
		namespace = realm.Metadata.Name
	}
	namespaceExists, nsErr := r.ExistsRealmContainerdNamespace(namespace)
	switch {
	case nsErr != nil:
		r.logger.DebugContext(r.ctx, "reconcile: realm namespace probe failed",
			"realm", realm.Metadata.Name, "error", nsErr)
	case namespaceExists != realm.Status.ContainerdNamespaceReady:
		realm.Status.ContainerdNamespaceReady = namespaceExists
		changed = true
	}

	if !changed {
		return false, nil
	}
	persisted, updateErr := r.persistRealmStatusGuarded(realm)
	if updateErr != nil {
		return false, fmt.Errorf("failed to update realm metadata: %w", updateErr)
	}
	return persisted, nil
}

// ReconcileSpaceHealth re-probes the space's cgroup and persists cgroupReady
// when it changed. Runs under the space lock and re-reads the document there,
// matching EnsureSpace, so it cannot resurrect a concurrently deleted space.
// Returns whether a status write landed.
func (r *Exec) ReconcileSpaceHealth(space intmodel.Space) (bool, error) {
	release := r.lockSpace(space.Spec.RealmName, space.Metadata.Name)
	defer release()
	current, err := r.GetSpace(space)
	if err != nil {
		return false, err
	}

	cgroupExists, probeErr := r.ExistsCgroup(current)
	if probeErr != nil {
		r.logger.DebugContext(r.ctx, "reconcile: space cgroup probe failed",
			"space", current.Metadata.Name, "error", probeErr)
		return false, nil
	}
	if cgroupExists == current.Status.CgroupReady {
		return false, nil
	}
	current.Status.CgroupReady = cgroupExists
	persisted, updateErr := r.persistSpaceStatusGuarded(current)
	if updateErr != nil {
		return false, fmt.Errorf("failed to update space metadata: %w", updateErr)
	}
	return persisted, nil
}

// ReconcileStackHealth re-probes the stack's cgroup and persists cgroupReady
// when it changed. Returns whether a status write landed.
func (r *Exec) ReconcileStackHealth(stack intmodel.Stack) (bool, error) {
	stack, err := r.GetStack(stack)
	if err != nil {
		return false, err
	}
	cgroupExists, err := r.ExistsCgroup(stack)
	if err != nil {
		r.logger.DebugContext(r.ctx, "reconcile: stack cgroup probe failed",
			"stack", stack.Metadata.Name, "error", err)
		return false, nil
	}
	if cgroupExists == stack.Status.CgroupReady {
		return false, nil
	}
	stack.Status.CgroupReady = cgroupExists
	persisted, updateErr := r.persistStackStatusGuarded(stack)
	if updateErr != nil {
		return false, fmt.Errorf("failed to update stack metadata: %w", updateErr)
	}
	return persisted, nil
}
