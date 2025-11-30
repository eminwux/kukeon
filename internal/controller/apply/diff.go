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

	// Compatible changes: labels
	if !mapsEqual(desired.Metadata.Labels, actual.Metadata.Labels) {
		result.HasChanges = true
		if result.ChangeType == ChangeTypeNone {
			result.ChangeType = ChangeTypeCompatible
		}
		result.ChangedFields = append(result.ChangedFields, "metadata.labels")
		result.Details["metadata.labels"] = labelsChangedMsg
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
		// Root container removed
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.RootContainerChanged = true
		result.BreakingChanges = append(result.BreakingChanges, "spec.rootContainer")
		result.RootContainerDetails["rootContainer"] = "root container removed"
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
			// Container to update
			containerDiff := diffContainerSpec(desiredContainer, actualContainer)
			if containerDiff.HasChanges {
				result.HasChanges = true
				if containerDiff.ChangeType == ChangeTypeBreaking {
					result.ChangeType = ChangeTypeBreaking
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

	// Diff container spec
	specDiff := diffContainerSpec(&desired.Spec, &actual.Spec)
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
func diffContainerSpec(desired, actual *intmodel.ContainerSpec) DiffResult {
	result := DiffResult{
		Details: make(map[string]string),
	}

	// Breaking changes: image, command, args
	if desired.Image != actual.Image {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "image")
		result.Details["image"] = fmt.Sprintf("image changed from %q to %q", actual.Image, desired.Image)
	}

	if desired.Command != actual.Command {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "command")
		result.Details["command"] = fmt.Sprintf("command changed from %q to %q", actual.Command, desired.Command)
	}

	if !slicesEqual(desired.Args, actual.Args) {
		result.HasChanges = true
		result.ChangeType = ChangeTypeBreaking
		result.BreakingChanges = append(result.BreakingChanges, "args")
		result.Details["args"] = "args changed"
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

	if !slicesEqual(desired.Volumes, actual.Volumes) {
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

	return result
}

// Helper functions

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
