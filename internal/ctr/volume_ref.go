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
	"errors"
	"fmt"
	"os"

	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// VolumeScope is the realm/space/stack scope a container resolves a same-scope
// (bare-source) kind: volume mount against. The scope walk starts at the
// deepest set coordinate and falls back to shallower ones, so the most-specific
// Volume of a given name wins — mirroring secret-scope lookup. Step 4 (#1016).
type VolumeScope struct {
	Realm string
	Space string
	Stack string
}

// ResolvedVolume is the outcome of resolving a kind: volume VolumeMount: the
// host directory to bind-mount and the scope coordinates the reference resolved
// to. The coordinates let the `kuke delete volume` gate match a live mount
// against the Volume being deleted without re-walking the scope. Step 4 (#1016).
type ResolvedVolume struct {
	HostPath string
	Realm    string
	Space    string
	Stack    string
	Name     string
}

// ResolveVolumeMount resolves a kind: volume VolumeMount to the on-disk Volume
// directory under runPath. A bare Source names a Volume in the container's own
// scope, walked most-specific-first (stack → space → realm); a VolumeRef names
// one cross-scope by explicit coordinates. The referenced Volume must already
// exist — there is no auto-create (an unknown name is a hard error, not a
// silent provision), so a typo'd reference fails fast at container create. A
// non-volume Kind is not this resolver's concern and yields an error so callers
// never mis-route a bind/tmpfs mount here. Step 4 (#1016).
func ResolveVolumeMount(runPath string, scope VolumeScope, mount intmodel.VolumeMount) (ResolvedVolume, error) {
	if mount.Kind != intmodel.VolumeKindVolume {
		return ResolvedVolume{}, fmt.Errorf("%w: not a volume-kind mount", internalerrdefs.ErrVolumeKindUnknown)
	}
	if runPath == "" {
		return ResolvedVolume{}, errors.New("cannot resolve volume reference: daemon RunPath is unset")
	}

	if mount.VolumeRef != nil {
		ref := mount.VolumeRef
		path := fs.VolumePath(runPath, ref.Realm, ref.Space, ref.Stack, ref.Name)
		if !volumeDirExists(path) {
			return ResolvedVolume{}, fmt.Errorf(
				"%w: volumeRef %q (realm=%q space=%q stack=%q)",
				internalerrdefs.ErrVolumeNotFound, ref.Name, ref.Realm, ref.Space, ref.Stack,
			)
		}
		return ResolvedVolume{
			HostPath: path,
			Realm:    ref.Realm,
			Space:    ref.Space,
			Stack:    ref.Stack,
			Name:     ref.Name,
		}, nil
	}

	// Same-scope name: walk the container's scope most-specific-first.
	name := mount.Source
	for _, cand := range volumeScopeCandidates(scope) {
		path := fs.VolumePath(runPath, cand.Realm, cand.Space, cand.Stack, name)
		if volumeDirExists(path) {
			return ResolvedVolume{
				HostPath: path,
				Realm:    cand.Realm,
				Space:    cand.Space,
				Stack:    cand.Stack,
				Name:     name,
			}, nil
		}
	}
	return ResolvedVolume{}, fmt.Errorf(
		"%w: volume %q not found in scope realm=%q space=%q stack=%q (or any parent scope)",
		internalerrdefs.ErrVolumeNotFound, name, scope.Realm, scope.Space, scope.Stack,
	)
}

// volumeScopeCandidates returns the scope coordinates to probe for a same-scope
// volume name, deepest first: the container's full (realm, space, stack), then
// the space scope, then the realm scope. Levels whose required parent
// coordinate is empty are skipped (a stack coordinate with no space is not a
// valid scope), so a realm-scoped container probes only the realm.
func volumeScopeCandidates(scope VolumeScope) []VolumeScope {
	var out []VolumeScope
	if scope.Realm != "" && scope.Space != "" && scope.Stack != "" {
		out = append(out, VolumeScope{Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack})
	}
	if scope.Realm != "" && scope.Space != "" {
		out = append(out, VolumeScope{Realm: scope.Realm, Space: scope.Space})
	}
	if scope.Realm != "" {
		out = append(out, VolumeScope{Realm: scope.Realm})
	}
	return out
}

// volumeDirExists reports whether path is an existing directory — the on-disk
// shape WriteVolume provisions. A non-directory squatting on the path is not a
// volume, so it reads as absent.
func volumeDirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// resolveVolumeMounts returns a copy of mounts with every kind: volume entry's
// Source rewritten to its resolved on-disk Volume directory, so the downstream
// OCI bind-mount emitter needs no Volume awareness. Non-volume mounts pass
// through untouched. The input slice is never mutated. When no kind: volume
// entry is present the original slice is returned and runPath is never consulted
// — a spec that declares no volume reference pays no resolution cost and is
// unaffected by an unset RunPath. Step 4 (#1016).
func resolveVolumeMounts(runPath string, scope VolumeScope, mounts []intmodel.VolumeMount) ([]intmodel.VolumeMount, error) {
	hasVolumeKind := false
	for i := range mounts {
		if mounts[i].Kind == intmodel.VolumeKindVolume {
			hasVolumeKind = true
			break
		}
	}
	if !hasVolumeKind {
		return mounts, nil
	}

	out := make([]intmodel.VolumeMount, len(mounts))
	copy(out, mounts)
	for i := range out {
		if out[i].Kind != intmodel.VolumeKindVolume {
			continue
		}
		resolved, err := ResolveVolumeMount(runPath, scope, out[i])
		if err != nil {
			return nil, err
		}
		out[i].Source = resolved.HostPath
		// VolumeRef has served its purpose; clear it so the rewritten mount is a
		// plain resolved bind reference and never re-resolved downstream.
		out[i].VolumeRef = nil
	}
	return out, nil
}
