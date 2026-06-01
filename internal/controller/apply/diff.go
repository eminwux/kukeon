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

package apply

import (
	"fmt"
	"strings"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

const (
	labelsChangedMsg = "labels changed"
)

// ChangeType classifies the type of change detected.
type ChangeType int

const (
	// ChangeTypeNone indicates no changes detected.
	ChangeTypeNone ChangeType = iota
	// ChangeTypeAdditive indicates new fields or resources added.
	ChangeTypeAdditive
	// ChangeTypeCompatible indicates backward-compatible changes (labels, env vars, etc.).
	ChangeTypeCompatible
	// ChangeTypeBreaking indicates breaking changes that require destructive operations.
	ChangeTypeBreaking
)

// DiffResult represents the result of comparing desired vs actual state.
type DiffResult struct {
	HasChanges      bool
	ChangeType      ChangeType
	ChangedFields   []string
	BreakingChanges []string
	Details         map[string]string // field -> description of change
}

// ContainerDiff represents changes to a container within a cell.
type ContainerDiff struct {
	Name            string
	Action          string // "add", "remove", "update"
	ChangedFields   []string
	BreakingChanges []string
	Details         map[string]string
}

// CellDiffResult extends DiffResult with container-level diffs.
type CellDiffResult struct {
	DiffResult

	RootContainerChanged bool
	RootContainerDetails map[string]string
	Containers           []ContainerDiff
	Orphans              []string // container IDs to be removed
}

// DiffRealm compares desired and actual realm states.
func DiffRealm(desired, actual intmodel.Realm) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	// Check for breaking changes: name or namespace change
	if desired.Metadata.Name != actual.Metadata.Name {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "metadata.name")
		result.Details["metadata.name"] = fmt.Sprintf(
			"name changed from %q to %q (breaking)",
			actual.Metadata.Name,
			desired.Metadata.Name,
		)
		return result
	}

	// Namespace change is breaking
	if desired.Spec.Namespace != "" && desired.Spec.Namespace != actual.Spec.Namespace {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.namespace")
		result.Details["spec.namespace"] = fmt.Sprintf(
			"namespace changed from %q to %q (breaking)",
			actual.Spec.Namespace,
			desired.Spec.Namespace,
		)
		return result
	}

	// Compatible changes: labels
	if !mapsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = labelsChangedMsg
	}

	// Registry credentials changes are compatible
	if !registryCredentialsEqual(desired.Spec.RegistryCredentials, actual.Spec.RegistryCredentials) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "spec.registryCredentials")
		result.Details["spec.registryCredentials"] = "registry credentials changed"
	}

	return result
}

// DiffSpace compares desired and actual space states.
func DiffSpace(desired, actual intmodel.Space) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	// Check for breaking changes: name or realm association change
	if desired.Metadata.Name != actual.Metadata.Name {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "metadata.name")
		result.Details["metadata.name"] = fmt.Sprintf(
			"name changed from %q to %q (breaking)",
			actual.Metadata.Name,
			desired.Metadata.Name,
		)
		return result
	}

	if desired.Spec.RealmName != actual.Spec.RealmName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.realmName")
		result.Details["spec.realmName"] = fmt.Sprintf(
			"realm changed from %q to %q (breaking)",
			actual.Spec.RealmName,
			desired.Spec.RealmName,
		)
		return result
	}

	// CNI config path change is breaking (requires network recreation)
	if desired.Spec.CNIConfigPath != "" && desired.Spec.CNIConfigPath != actual.Spec.CNIConfigPath {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.cniConfigPath")
		result.Details["spec.cniConfigPath"] = fmt.Sprintf(
			"CNI config path changed from %q to %q (breaking - requires network recreation)",
			actual.Spec.CNIConfigPath,
			desired.Spec.CNIConfigPath,
		)
		return result
	}

	// Compatible changes: labels
	if !mapsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = labelsChangedMsg
	}

	// Compatible changes: spec.defaults.container. Inheritance is computed
	// at container-create/update time, so changing defaults is non-breaking
	// for already-running containers — only new or updated containers pick
	// up the new envelope.
	if !spaceDefaultsEqual(desired.Spec.Defaults, actual.Spec.Defaults) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "spec.defaults.container")
		result.Details["spec.defaults.container"] = "container defaults changed"
	}

	return result
}

// DiffStack compares desired and actual stack states.
func DiffStack(desired, actual intmodel.Stack) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	// Check for breaking changes: name or parent association changes
	if desired.Metadata.Name != actual.Metadata.Name {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "metadata.name")
		result.Details["metadata.name"] = fmt.Sprintf(
			"name changed from %q to %q (breaking)",
			actual.Metadata.Name,
			desired.Metadata.Name,
		)
		return result
	}

	if desired.Spec.RealmName != actual.Spec.RealmName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.realmName")
		result.Details["spec.realmName"] = fmt.Sprintf(
			"realm changed from %q to %q (breaking)",
			actual.Spec.RealmName,
			desired.Spec.RealmName,
		)
		return result
	}

	if desired.Spec.SpaceName != actual.Spec.SpaceName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.spaceName")
		result.Details["spec.spaceName"] = fmt.Sprintf(
			"space changed from %q to %q (breaking)",
			actual.Spec.SpaceName,
			desired.Spec.SpaceName,
		)
		return result
	}

	// Compatible changes: labels, ID
	if !mapsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = "labels changed"
	}

	if desired.Spec.ID != "" && desired.Spec.ID != actual.Spec.ID {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "spec.id")
		result.Details["spec.id"] = fmt.Sprintf("ID changed from %q to %q", actual.Spec.ID, desired.Spec.ID)
	}

	return result
}

// DiffCell compares desired and actual cell states.
func DiffCell(desired, actual intmodel.Cell) CellDiffResult {
	result := CellDiffResult{
		DiffResult: DiffResult{
			Details: make(map[string]string),
		},
		RootContainerDetails: make(map[string]string),
		Containers:           []ContainerDiff{},
		Orphans:              []string{},
	}

	// Check for breaking changes: name or parent association changes
	if desired.Metadata.Name != actual.Metadata.Name {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "metadata.name")
		result.Details["metadata.name"] = fmt.Sprintf(
			"name changed from %q to %q (breaking)",
			actual.Metadata.Name,
			desired.Metadata.Name,
		)
		return result
	}

	if desired.Spec.RealmName != actual.Spec.RealmName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.realmName")
		result.Details["spec.realmName"] = fmt.Sprintf(
			"realm changed from %q to %q (breaking)",
			actual.Spec.RealmName,
			desired.Spec.RealmName,
		)
		return result
	}

	if desired.Spec.SpaceName != actual.Spec.SpaceName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.spaceName")
		result.Details["spec.spaceName"] = fmt.Sprintf(
			"space changed from %q to %q (breaking)",
			actual.Spec.SpaceName,
			desired.Spec.SpaceName,
		)
		return result
	}

	if desired.Spec.StackName != actual.Spec.StackName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.stackName")
		result.Details["spec.stackName"] = fmt.Sprintf(
			"stack changed from %q to %q (breaking)",
			actual.Spec.StackName,
			desired.Spec.StackName,
		)
		return result
	}

	// Compatible changes: labels. The runner injects canonical
	// `*.kukeon.io` keys (cell.kukeon.io, realm.kukeon.io, …) on
	// CreateCell — these are controller-managed, not user-authored, and
	// must not be compared against an empty `desired.Metadata.Labels`
	// (issue #437 AC #3). Without this filter, every `kuke apply -f` of a
	// label-free YAML against an existing cell reports
	// `metadata.labels changed`, falls through to UpdateCell, and
	// clobbers the canonical labels with the user's empty map.
	if !userLabelsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = labelsChangedMsg
	}

	// Compatible: cell-default TTY pointer. The runner re-resolves which
	// container's TTY `kuke attach` lands on lazily from cell.Spec.Tty.Default,
	// so an operator edit just needs to flow into UpdateCell rather than
	// trigger a recreate. Without this branch the edit is persisted to cell
	// metadata but apply reports "no changes" and never re-stamps. Issue #992.
	if !cellTtyEqual(desired.Spec.Tty, actual.Spec.Tty) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "spec.tty")
		result.Details["spec.tty"] = "tty default changed"
	}

	// Compatible: AutoDelete. The reaper consults cell.Spec.AutoDelete on the
	// next reconcile pass, so toggling the flag does not require a recreate —
	// but the apply layer must still surface the change so the operator does
	// not see a misleading "no changes" verdict on a YAML that flipped it.
	// Issue #992.
	if desired.Spec.AutoDelete != actual.Spec.AutoDelete {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "spec.autoDelete")
		result.Details["spec.autoDelete"] = fmt.Sprintf(
			"autoDelete changed from %v to %v",
			actual.Spec.AutoDelete,
			desired.Spec.AutoDelete,
		)
	}

	// Breaking: NestedCgroupRuntime. Flipping the flag re-runs the
	// EnableCellAllSubtreeControllers delegation (#318) and recomputes the
	// in-container /sys/fs/cgroup mount per BuildContainerSpec; the namespace
	// setup baked into the existing cell cannot be re-stamped in place, so
	// RecreateCell is the safe path. Issue #992.
	if desired.Spec.NestedCgroupRuntime != actual.Spec.NestedCgroupRuntime {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.nestedCgroupRuntime")
		result.Details["spec.nestedCgroupRuntime"] = fmt.Sprintf(
			"nestedCgroupRuntime changed from %v to %v (breaking)",
			actual.Spec.NestedCgroupRuntime,
			desired.Spec.NestedCgroupRuntime,
		)
	}

	// Find root container in desired and actual
	desiredRoot := findRootContainer(desired.Spec.Containers)
	actualRoot := findRootContainer(actual.Spec.Containers)

	// Check if root container spec changed (breaking)
	switch {
	case desiredRoot != nil && actualRoot != nil:
		if rootContainerSpecChanged(desiredRoot, actualRoot) {
			result.HasChanges = true
			result.ChangeType = ChangeTypeBreaking
			result.RootContainerChanged = true
			result.BreakingChanges = append(result.BreakingChanges, "spec.rootContainer")
			result.RootContainerDetails["rootContainer"] = "root container spec changed (image, command, or args)"
		}
	case desiredRoot != nil && actualRoot == nil:
		// Root container added
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.RootContainerChanged = true
		result.BreakingChanges = append(result.BreakingChanges, "spec.rootContainer")
		result.RootContainerDetails["rootContainer"] = "root container added"
	case desiredRoot == nil && actualRoot != nil:
		// The YAML omitted a root container and the runner synthesized one
		// during create (see `internal/controller/runner/provision.go`'s
		// `ensureCellRootContainerSpec` path — every cell ends up with a
		// `Root: true` entry whether or not the user authored one). Treating
		// the synthesized root as a "removal" caused `kuke apply -f` of an
		// unchanged file to report `Cell <name>: updated\n  - root
		// container recreated` and trip RecreateCell. The synthesized root
		// is an implementation detail of cell creation, not a user-authored
		// field the apply layer should drift-detect on; skip it.
	}

	// Diff child containers
	desiredContainers := make(map[string]*intmodel.ContainerSpec)
	actualContainers := make(map[string]*intmodel.ContainerSpec)

	for i := range desired.Spec.Containers {
		container := &desired.Spec.Containers[i]
		if !container.Root && container.ID != "" {
			desiredContainers[container.ID] = container
		}
	}

	for i := range actual.Spec.Containers {
		container := &actual.Spec.Containers[i]
		if !container.Root && container.ID != "" {
			actualContainers[container.ID] = container
		}
	}

	// Find containers to add, update, or remove
	for id, desiredContainer := range desiredContainers {
		actualContainer, exists := actualContainers[id]
		if !exists {
			// Container to add
			result.HasChanges = true
			if result.ChangeType == ChangeTypeNone {
				result.ChangeType = ChangeTypeAdditive
			}
			result.Containers = append(result.Containers, ContainerDiff{
				Name:   id,
				Action: "add",
			})
		} else {
			// Container to update. Non-root container — image/command/args
			// are classified as in-place updateable (Compatible) here so
			// UpdateCell stops, removes, recreates, and starts the affected
			// child container instead of refusing the diff. The root
			// container's image/command/args bypass diffContainerSpec via
			// rootContainerSpecChanged above and stay on the recreate path.
			containerDiff := diffContainerSpec(desiredContainer, actualContainer, false)
			if containerDiff.HasChanges {
				result.HasChanges = true
				if containerDiff.ChangeType == ChangeTypeBreaking {
					result.ChangeType = ChangeTypeBreaking
					// Qualify per-container breaking fields with the container ID
					// so the cell-level error message in reconcile.go names them
					// instead of printing an empty `[]`.
					for _, field := range containerDiff.BreakingChanges {
						result.BreakingChanges = append(
							result.BreakingChanges,
							fmt.Sprintf("containers[%s].%s", id, field),
						)
					}
				} else if result.ChangeType == ChangeTypeNone {
					result.ChangeType = containerDiff.ChangeType
				}
				result.Containers = append(result.Containers, ContainerDiff{
					Name:            id,
					Action:          "update",
					ChangedFields:   containerDiff.ChangedFields,
					BreakingChanges: containerDiff.BreakingChanges,
					Details:         containerDiff.Details,
				})
			}
		}
	}

	// Find orphans (containers in actual but not in desired)
	for id := range actualContainers {
		if _, exists := desiredContainers[id]; !exists {
			result.HasChanges = true
			if result.ChangeType == ChangeTypeNone {
				result.ChangeType = ChangeTypeAdditive
			}
			result.Orphans = append(result.Orphans, id)
		}
	}

	return result
}

// DiffContainer compares desired and actual container states.
func DiffContainer(desired, actual intmodel.Container) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	// Check for breaking changes: name or parent association changes
	if desired.Metadata.Name != actual.Metadata.Name {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "metadata.name")
		result.Details["metadata.name"] = fmt.Sprintf(
			"name changed from %q to %q (breaking)",
			actual.Metadata.Name,
			desired.Metadata.Name,
		)
		return result
	}

	if desired.Spec.RealmName != actual.Spec.RealmName ||
		desired.Spec.SpaceName != actual.Spec.SpaceName ||
		desired.Spec.StackName != actual.Spec.StackName ||
		desired.Spec.CellName != actual.Spec.CellName {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "spec.parent")
		result.Details["spec.parent"] = "parent resource changed (breaking)"
		return result
	}

	// Compatible changes: labels
	if !mapsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = labelsChangedMsg
	}

	// Diff container spec. Pass desired.Spec.Root so a root container under
	// the single-container reconcile path keeps the existing
	// breaking-change classification for image/command/args (cell-level
	// callers always pass false because only non-root children flow
	// through DiffCell's container loop).
	specDiff := diffContainerSpec(&desired.Spec, &actual.Spec, desired.Spec.Root)
	if specDiff.HasChanges {
		result.HasChanges = true
		if specDiff.ChangeType == ChangeTypeBreaking {
			result.ChangeType = ChangeTypeBreaking
		} else if result.ChangeType == ChangeTypeNone {
			result.ChangeType = specDiff.ChangeType
		}
		result.ChangedFields = append(result.ChangedFields, specDiff.ChangedFields...)
		result.BreakingChanges = append(result.BreakingChanges, specDiff.BreakingChanges...)
		for k, v := range specDiff.Details {
			result.Details[k] = v
		}
	}

	return result
}

// diffContainerSpec compares two container specs.
//
// `rootContainer` controls whether image/command/args changes are
// classified as breaking. Root containers cannot be updated in place — the
// runner's StartCell path bakes the root spec into namespace setup, so any
// change to image/command/args has to flow through RecreateCell. Non-root
// containers can be stopped, removed, recreated, and started under the
// existing cell namespaces; UpdateCell already implements that recreate
// dance (the apply layer just needs to stop refusing the diff). Issue
// #485.
//
// which needs a per-field diff branch. Splitting into smaller helpers per
// field group (env/ports/volumes, privileged/user/readonly,
// capabilities/securityOpts/tmpfs/resources) would obscure the spec-vs-spec
// shape readers reach for when adding a new field. The image/command/args
// trio already lives in recordImageCmdArgsChange because those three share
// the only non-trivial classification branch (root vs. non-root).
//
//nolint:funlen // The container spec is a flat list of ~12 fields, each of
func diffContainerSpec(desired, actual *intmodel.ContainerSpec, rootContainer bool) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	if desired.Image != actual.Image {
		recordImageCmdArgsChange(&result, rootContainer, "image",
			fmt.Sprintf("image changed from %q to %q", actual.Image, desired.Image))
	}

	if desired.Command != actual.Command {
		recordImageCmdArgsChange(&result, rootContainer, "command",
			fmt.Sprintf("command changed from %q to %q", actual.Command, desired.Command))
	}

	if !slicesEqual(desired.Args, actual.Args) {
		recordImageCmdArgsChange(&result, rootContainer, "args", "args changed")
	}

	// Compatible changes: env, ports, volumes, etc.
	if !slicesEqual(desired.Env, actual.Env) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "env")
		result.Details["env"] = "environment variables changed"
	}

	if !slicesEqual(desired.Ports, actual.Ports) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "ports")
		result.Details["ports"] = "ports changed"
	}

	if !volumeMountsEqual(desired.Volumes, actual.Volumes) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "volumes")
		result.Details["volumes"] = "volumes changed"
	}

	if desired.Privileged != actual.Privileged {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "privileged")
		result.Details["privileged"] = fmt.Sprintf(
			"privileged changed from %v to %v",
			actual.Privileged,
			desired.Privileged,
		)
	}

	if desired.User != actual.User {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "user")
		result.Details["user"] = fmt.Sprintf("user changed from %q to %q", actual.User, desired.User)
	}

	if desired.ReadOnlyRootFilesystem != actual.ReadOnlyRootFilesystem {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "readOnlyRootFilesystem")
		result.Details["readOnlyRootFilesystem"] = fmt.Sprintf(
			"readOnlyRootFilesystem changed from %v to %v",
			actual.ReadOnlyRootFilesystem,
			desired.ReadOnlyRootFilesystem,
		)
	}

	if !capabilitiesEqual(desired.Capabilities, actual.Capabilities) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "capabilities")
		result.Details["capabilities"] = "capabilities changed"
	}

	if !slicesEqual(desired.SecurityOpts, actual.SecurityOpts) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "securityOpts")
		result.Details["securityOpts"] = "securityOpts changed"
	}

	if !tmpfsEqual(desired.Tmpfs, actual.Tmpfs) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "tmpfs")
		result.Details["tmpfs"] = "tmpfs mounts changed"
	}

	if !resourcesEqual(desired.Resources, actual.Resources) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "resources")
		result.Details["resources"] = "resource limits changed"
	}

	if !secretsEqual(desired.Secrets, actual.Secrets) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "secrets")
		result.Details["secrets"] = "secrets changed"
	}

	if !reposEqual(desired.Repos, actual.Repos) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "repos")
		result.Details["repos"] = "repos changed"
	}

	if desired.WorkingDir != actual.WorkingDir {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "workingDir")
		result.Details["workingDir"] = fmt.Sprintf(
			"workingDir changed from %q to %q",
			actual.WorkingDir,
			desired.WorkingDir,
		)
	}

	if !slicesEqual(desired.Networks, actual.Networks) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "networks")
		result.Details["networks"] = "networks changed"
	}

	if !slicesEqual(desired.NetworksAliases, actual.NetworksAliases) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "networksAliases")
		result.Details["networksAliases"] = "networksAliases changed"
	}

	// Host-namespace toggles change the parent cell's OCI namespace shape —
	// the netns / pidns / cgroupns the root container sets up at cell-start
	// time is inherited by child containers via JoinContainerNamespaces, so
	// flipping any of these on an existing container cannot be applied
	// in-place. Route through ChangeTypeBreaking so the apply layer drives
	// RecreateCell instead of UpdateCell's child stop-remove-recreate-start
	// path (which only re-enters the existing namespaces).
	if desired.HostNetwork != actual.HostNetwork {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "hostNetwork")
		result.Details["hostNetwork"] = fmt.Sprintf(
			"hostNetwork changed from %v to %v (breaking)",
			actual.HostNetwork,
			desired.HostNetwork,
		)
	}

	if desired.HostPID != actual.HostPID {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "hostPID")
		result.Details["hostPID"] = fmt.Sprintf(
			"hostPID changed from %v to %v (breaking)",
			actual.HostPID,
			desired.HostPID,
		)
	}

	if desired.HostCgroup != actual.HostCgroup {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "hostCgroup")
		result.Details["hostCgroup"] = fmt.Sprintf(
			"hostCgroup changed from %v to %v (breaking)",
			actual.HostCgroup,
			desired.HostCgroup,
		)
	}

	if desired.CNIConfigPath != actual.CNIConfigPath {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "cniConfigPath")
		result.Details["cniConfigPath"] = fmt.Sprintf(
			"cniConfigPath changed from %q to %q",
			actual.CNIConfigPath,
			desired.CNIConfigPath,
		)
	}

	if desired.RestartPolicy != actual.RestartPolicy {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "restartPolicy")
		result.Details["restartPolicy"] = fmt.Sprintf(
			"restartPolicy changed from %q to %q",
			actual.RestartPolicy,
			desired.RestartPolicy,
		)
	}

	if desired.Attachable != actual.Attachable {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "attachable")
		result.Details["attachable"] = fmt.Sprintf(
			"attachable changed from %v to %v",
			actual.Attachable,
			desired.Attachable,
		)
	}

	if !ttyEqual(desired.Tty, actual.Tty) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "tty")
		result.Details["tty"] = "tty changed"
	}

	if !gitEqual(desired.Git, actual.Git) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "git")
		result.Details["git"] = "git identity changed"
	}

	return result
}

// recordImageCmdArgsChange routes an image/command/args diff into either
// BreakingChanges (root container) or ChangedFields (non-root, in-place
// updateable). Lives outside diffContainerSpec so the latter stays within
// funlen's statement budget. Issue #485.
func recordImageCmdArgsChange(result *DiffResult, rootContainer bool, field, detail string) {
	result.HasChanges = true
	if rootContainer {
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, field)
	} else {
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, field)
	}
	result.Details[field] = detail
}

// cellTtyEqual reports whether two *CellTty pointers describe the same
// default-TTY pointer. A nil pointer is treated as equal to a zero-value
// CellTty so adding or clearing an explicitly-empty `spec.tty` block does not
// register as drift on a same-file re-apply.
func cellTtyEqual(a, b *intmodel.CellTty) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil {
		return b.Default == ""
	}
	if b == nil {
		return a.Default == ""
	}
	return a.Default == b.Default
}

func capabilitiesEqual(a, b *intmodel.ContainerCapabilities) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return len(capabilitiesDrop(a))+len(capabilitiesAdd(a)) == 0 &&
			len(capabilitiesDrop(b))+len(capabilitiesAdd(b)) == 0
	}
	return slicesEqual(a.Drop, b.Drop) && slicesEqual(a.Add, b.Add)
}

func capabilitiesDrop(c *intmodel.ContainerCapabilities) []string {
	if c == nil {
		return nil
	}
	return c.Drop
}

func capabilitiesAdd(c *intmodel.ContainerCapabilities) []string {
	if c == nil {
		return nil
	}
	return c.Add
}

func tmpfsEqual(a, b []intmodel.ContainerTmpfsMount) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].SizeBytes != b[i].SizeBytes {
			return false
		}
		if !slicesEqual(a[i].Options, b[i].Options) {
			return false
		}
	}
	return true
}

func resourcesEqual(a, b *intmodel.ContainerResources) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		// Treat nil and zeroed-out as equal so setting to nil or clearing all
		// pointers collapses to the same result.
		return resourcesAreZero(a) && resourcesAreZero(b)
	}
	return int64PtrEqual(a.MemoryLimitBytes, b.MemoryLimitBytes) &&
		int64PtrEqual(a.CPUShares, b.CPUShares) &&
		int64PtrEqual(a.PidsLimit, b.PidsLimit)
}

func resourcesAreZero(r *intmodel.ContainerResources) bool {
	if r == nil {
		return true
	}
	return r.MemoryLimitBytes == nil && r.CPUShares == nil && r.PidsLimit == nil
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// ttyEqual reports whether two *ContainerTty describe the same kuketty
// pre-Serve config. Nil and a zero-valued block compare equal so allocating
// or clearing an explicitly-empty `tty:` does not register as drift — same
// treatment resourcesEqual gives an empty resources block.
func ttyEqual(a, b *intmodel.ContainerTty) bool {
	if a.IsEmpty() && b.IsEmpty() {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Prompt != b.Prompt || a.LogFile != b.LogFile || a.LogLevel != b.LogLevel {
		return false
	}
	if len(a.OnInit) != len(b.OnInit) {
		return false
	}
	for i := range a.OnInit {
		if a.OnInit[i] != b.OnInit[i] {
			return false
		}
	}
	return true
}

// gitEqual reports whether two *ContainerGit describe the same declarative
// git identity/signing block. Nil and a zero-valued block compare equal so
// allocating or clearing an explicitly-empty `git:` does not register as
// drift.
func gitEqual(a, b *intmodel.ContainerGit) bool {
	if gitIsZero(a) && gitIsZero(b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return gitIdentityEqual(a.Author, b.Author) &&
		gitIdentityEqual(a.Committer, b.Committer) &&
		a.SigningKey == b.SigningKey &&
		a.AllowedSigners == b.AllowedSigners &&
		slicesEqual(a.Sign, b.Sign)
}

func gitIsZero(g *intmodel.ContainerGit) bool {
	if g == nil {
		return true
	}
	return gitIdentityIsZero(g.Author) &&
		gitIdentityIsZero(g.Committer) &&
		g.SigningKey == "" &&
		g.AllowedSigners == "" &&
		len(g.Sign) == 0
}

func gitIdentityEqual(a, b *intmodel.GitIdentity) bool {
	if gitIdentityIsZero(a) && gitIdentityIsZero(b) {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func gitIdentityIsZero(id *intmodel.GitIdentity) bool {
	if id == nil {
		return true
	}
	return id.Name == "" && id.Email == ""
}

// spaceDefaultsEqual reports whether two *SpaceDefaults describe the same
// inherited envelope. A nil pointer is treated as equal to an empty defaults
// block so that adding or clearing an explicitly-empty `spec.defaults` does
// not register as a change.
func spaceDefaultsEqual(a, b *intmodel.SpaceDefaults) bool {
	ac := spaceDefaultsContainer(a)
	bc := spaceDefaultsContainer(b)
	if ac == nil && bc == nil {
		return true
	}
	if ac == nil || bc == nil {
		return spaceContainerDefaultsIsZero(ac) && spaceContainerDefaultsIsZero(bc)
	}
	return ac.User == bc.User &&
		boolPtrEqual(ac.ReadOnlyRootFilesystem, bc.ReadOnlyRootFilesystem) &&
		capabilitiesEqual(ac.Capabilities, bc.Capabilities) &&
		slicesEqual(ac.SecurityOpts, bc.SecurityOpts) &&
		tmpfsEqual(ac.Tmpfs, bc.Tmpfs) &&
		resourcesEqual(ac.Resources, bc.Resources)
}

func spaceDefaultsContainer(d *intmodel.SpaceDefaults) *intmodel.SpaceContainerDefaults {
	if d == nil {
		return nil
	}
	return d.Container
}

func spaceContainerDefaultsIsZero(c *intmodel.SpaceContainerDefaults) bool {
	if c == nil {
		return true
	}
	return c.User == "" &&
		c.ReadOnlyRootFilesystem == nil &&
		c.Capabilities == nil &&
		len(c.SecurityOpts) == 0 &&
		len(c.Tmpfs) == 0 &&
		c.Resources == nil
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// Helper functions

// userLabelsEqual compares the user-authored subset of two label maps,
// filtering out controller-managed keys (anything with the `.kukeon.io`
// suffix — `cell.kukeon.io`, `realm.kukeon.io`, …, injected during cell
// creation). Used by DiffCell so the runner-managed canonical labels do
// not register as drift on a same-file re-apply (issue #437).
func userLabelsEqual(desired, actual map[string]string) bool {
	return mapsEqual(filterManagedLabels(desired), filterManagedLabels(actual))
}

// filterManagedLabels returns a copy of m with controller-managed keys
// removed. A key is considered managed when its full name ends with
// `.kukeon.io` — the suffix every controller-injected label uses.
func filterManagedLabels(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if strings.HasSuffix(k, ".kukeon.io") {
			continue
		}
		out[k] = v
	}
	return out
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func volumeMountsEqual(a, b []intmodel.VolumeMount) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func reposEqual(a, b []intmodel.ContainerRepo) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		// ContainerRepo is a flat struct of comparable fields.
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func secretsEqual(a, b []intmodel.ContainerSecret) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].FromFile != b[i].FromFile ||
			a[i].FromEnv != b[i].FromEnv ||
			a[i].MountPath != b[i].MountPath ||
			!secretRefEqual(a[i].SecretRef, b[i].SecretRef) {
			return false
		}
	}
	return true
}

func secretRefEqual(a, b *intmodel.ContainerSecretRef) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func registryCredentialsEqual(a, b []intmodel.RegistryCredentials) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Username != b[i].Username ||
			a[i].Password != b[i].Password ||
			a[i].ServerAddress != b[i].ServerAddress {
			return false
		}
	}
	return true
}

func findRootContainer(containers []intmodel.ContainerSpec) *intmodel.ContainerSpec {
	for i := range containers {
		if containers[i].Root {
			return &containers[i]
		}
	}
	return nil
}

func rootContainerSpecChanged(desired, actual *intmodel.ContainerSpec) bool {
	return desired.Image != actual.Image ||
		desired.Command != actual.Command ||
		!slicesEqual(desired.Args, actual.Args)
}

// isBreakingChange returns true if the change type is breaking.
func isBreakingChange(changeType ChangeType) bool {
	return changeType == ChangeTypeBreaking
}
