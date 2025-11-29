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

package apischeme

import (
	"fmt"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Supported versions.
const (
	VersionV1Beta1 = ext.APIVersionV1Beta1
)

// DefaultVersion returns the canonical version when none is supplied.
func DefaultVersion(version ext.Version) ext.Version {
	if version == "" {
		return VersionV1Beta1
	}
	return version
}

// ConvertRealmDocToInternal converts an external RealmDoc to the internal hub type.
func ConvertRealmDocToInternal(in ext.RealmDoc) (intmodel.Realm, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Realm{
			Metadata: intmodel.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.RealmSpec{
				Namespace: in.Spec.Namespace,
			},
			Status: intmodel.RealmStatus{
				State:      intmodel.RealmState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return intmodel.Realm{}, fmt.Errorf("unsupported apiVersion for Realm: %s", in.APIVersion)
	}
}

// BuildRealmExternalFromInternal emits an external RealmDoc for a given version from an internal hub object.
func BuildRealmExternalFromInternal(in intmodel.Realm, apiVersion ext.Version) (ext.RealmDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		return ext.RealmDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata: ext.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.RealmSpec{
				Namespace: in.Spec.Namespace,
			},
			Status: ext.RealmStatus{
				State:      ext.RealmState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return ext.RealmDoc{}, fmt.Errorf("unsupported output apiVersion for Realm: %s", apiVersion)
	}
}

// NormalizeRealm takes an external RealmDoc request and returns an internal object and chosen apiVersion.
// For now, defaulting is minimal; future versions can enrich defaults here.
func NormalizeRealm(req ext.RealmDoc) (intmodel.Realm, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertRealmDocToInternal(req)
	if err != nil {
		return intmodel.Realm{}, "", err
	}
	return internal, version, nil
}

// ConvertSpaceDocToInternal converts an external SpaceDoc to the internal hub type.
func ConvertSpaceDocToInternal(in ext.SpaceDoc) (intmodel.Space, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		// Map external state to internal state (indices differ due to Creating/Deleting states)
		var intState intmodel.SpaceState
		switch in.Status.State { //nolint:exhaustive // default branch covers remaining states
		case ext.SpaceStatePending:
			intState = intmodel.SpaceStatePending
		case ext.SpaceStateReady:
			intState = intmodel.SpaceStateReady
		case ext.SpaceStateFailed:
			intState = intmodel.SpaceStateFailed
		default:
			intState = intmodel.SpaceStateUnknown
		}
		return intmodel.Space{
			Metadata: intmodel.SpaceMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.SpaceSpec{
				RealmName:     in.Spec.RealmID,
				CNIConfigPath: in.Spec.CNIConfigPath,
			},
			Status: intmodel.SpaceStatus{
				State:      intState,
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return intmodel.Space{}, fmt.Errorf("unsupported apiVersion for Space: %s", in.APIVersion)
	}
}

// BuildSpaceExternalFromInternal emits an external SpaceDoc for a given version from an internal hub object.
func BuildSpaceExternalFromInternal(in intmodel.Space, apiVersion ext.Version) (ext.SpaceDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		// Map internal state to external state (indices differ due to Creating/Deleting states)
		var extState ext.SpaceState
		switch in.Status.State { //nolint:exhaustive // default branch covers remaining states
		case intmodel.SpaceStatePending:
			extState = ext.SpaceStatePending
		case intmodel.SpaceStateCreating, intmodel.SpaceStateReady:
			extState = ext.SpaceStateReady
		case intmodel.SpaceStateDeleting, intmodel.SpaceStateFailed:
			extState = ext.SpaceStateFailed
		default:
			extState = ext.SpaceStateUnknown
		}
		return ext.SpaceDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindSpace,
			Metadata: ext.SpaceMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.SpaceSpec{
				RealmID:       in.Spec.RealmName,
				CNIConfigPath: in.Spec.CNIConfigPath,
			},
			Status: ext.SpaceStatus{
				State:      extState,
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return ext.SpaceDoc{}, fmt.Errorf("unsupported output apiVersion for Space: %s", apiVersion)
	}
}

// NormalizeSpace takes an external SpaceDoc request and returns an internal object and chosen apiVersion.
func NormalizeSpace(req ext.SpaceDoc) (intmodel.Space, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertSpaceDocToInternal(req)
	if err != nil {
		return intmodel.Space{}, "", err
	}
	return internal, version, nil
}

// ConvertStackDocToInternal converts an external StackDoc to the internal hub type.
func ConvertStackDocToInternal(in ext.StackDoc) (intmodel.Stack, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Stack{
			Metadata: intmodel.StackMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.StackSpec{
				ID:        in.Spec.ID,
				RealmName: in.Spec.RealmID,
				SpaceName: in.Spec.SpaceID,
			},
			Status: intmodel.StackStatus{
				State:      intmodel.StackState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return intmodel.Stack{}, fmt.Errorf("unsupported apiVersion for Stack: %s", in.APIVersion)
	}
}

// BuildStackExternalFromInternal emits an external StackDoc for a given version from an internal hub object.
func BuildStackExternalFromInternal(in intmodel.Stack, apiVersion ext.Version) (ext.StackDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		return ext.StackDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindStack,
			Metadata: ext.StackMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.StackSpec{
				ID:      in.Spec.ID,
				RealmID: in.Spec.RealmName,
				SpaceID: in.Spec.SpaceName,
			},
			Status: ext.StackStatus{
				State:      ext.StackState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}, nil
	default:
		return ext.StackDoc{}, fmt.Errorf("unsupported output apiVersion for Stack: %s", apiVersion)
	}
}

// NormalizeStack takes an external StackDoc request and returns an internal object and chosen apiVersion.
func NormalizeStack(req ext.StackDoc) (intmodel.Stack, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertStackDocToInternal(req)
	if err != nil {
		return intmodel.Stack{}, "", err
	}
	return internal, version, nil
}

// ConvertContainerDocToInternal converts an external ContainerDoc to the internal hub type.
func ConvertContainerDocToInternal(in ext.ContainerDoc) (intmodel.Container, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.ContainerSpec{
				ID:              in.Spec.ID,
				RealmName:       in.Spec.RealmID,
				SpaceName:       in.Spec.SpaceID,
				StackName:       in.Spec.StackID,
				CellName:        in.Spec.CellID,
				Root:            in.Spec.Root,
				Image:           in.Spec.Image,
				Command:         in.Spec.Command,
				Args:            in.Spec.Args,
				Env:             in.Spec.Env,
				Ports:           in.Spec.Ports,
				Volumes:         in.Spec.Volumes,
				Networks:        in.Spec.Networks,
				NetworksAliases: in.Spec.NetworksAliases,
				Privileged:      in.Spec.Privileged,
				CNIConfigPath:   in.Spec.CNIConfigPath,
				RestartPolicy:   in.Spec.RestartPolicy,
			},
			Status: intmodel.ContainerStatus{
				State:        intmodel.ContainerState(in.Status.State),
				RestartCount: in.Status.RestartCount,
				RestartTime:  in.Status.RestartTime,
				StartTime:    in.Status.StartTime,
				FinishTime:   in.Status.FinishTime,
				ExitCode:     in.Status.ExitCode,
				ExitSignal:   in.Status.ExitSignal,
			},
		}, nil
	default:
		return intmodel.Container{}, fmt.Errorf("unsupported apiVersion for Container: %s", in.APIVersion)
	}
}

// BuildContainerExternalFromInternal emits an external ContainerDoc for a given version from an internal hub object.
func BuildContainerExternalFromInternal(in intmodel.Container, apiVersion ext.Version) (ext.ContainerDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		return ext.ContainerDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindContainer,
			Metadata: ext.ContainerMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.ContainerSpec{
				ID:              in.Spec.ID,
				RealmID:         in.Spec.RealmName,
				SpaceID:         in.Spec.SpaceName,
				StackID:         in.Spec.StackName,
				CellID:          in.Spec.CellName,
				Root:            in.Spec.Root,
				Image:           in.Spec.Image,
				Command:         in.Spec.Command,
				Args:            in.Spec.Args,
				Env:             in.Spec.Env,
				Ports:           in.Spec.Ports,
				Volumes:         in.Spec.Volumes,
				Networks:        in.Spec.Networks,
				NetworksAliases: in.Spec.NetworksAliases,
				Privileged:      in.Spec.Privileged,
				CNIConfigPath:   in.Spec.CNIConfigPath,
				RestartPolicy:   in.Spec.RestartPolicy,
			},
			Status: ext.ContainerStatus{
				State:        ext.ContainerState(in.Status.State),
				RestartCount: in.Status.RestartCount,
				RestartTime:  in.Status.RestartTime,
				StartTime:    in.Status.StartTime,
				FinishTime:   in.Status.FinishTime,
				ExitCode:     in.Status.ExitCode,
				ExitSignal:   in.Status.ExitSignal,
			},
		}, nil
	default:
		return ext.ContainerDoc{}, fmt.Errorf("unsupported output apiVersion for Container: %s", apiVersion)
	}
}

// NormalizeContainer takes an external ContainerDoc request and returns an internal object and chosen apiVersion.
func NormalizeContainer(req ext.ContainerDoc) (intmodel.Container, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertContainerDocToInternal(req)
	if err != nil {
		return intmodel.Container{}, "", err
	}
	return internal, version, nil
}

// convertContainerSpecToInternal converts an external ContainerSpec to the internal ContainerSpec type.
// This is used for nested ContainerSpecs in CellSpec.
func convertContainerSpecToInternal(in ext.ContainerSpec) intmodel.ContainerSpec {
	return intmodel.ContainerSpec{
		ID:              in.ID,
		ContainerdID:    in.ContainerdID,
		RealmName:       in.RealmID,
		SpaceName:       in.SpaceID,
		StackName:       in.StackID,
		CellName:        in.CellID,
		Root:            in.Root,
		Image:           in.Image,
		Command:         in.Command,
		Args:            in.Args,
		Env:             in.Env,
		Ports:           in.Ports,
		Volumes:         in.Volumes,
		Networks:        in.Networks,
		NetworksAliases: in.NetworksAliases,
		Privileged:      in.Privileged,
		CNIConfigPath:   in.CNIConfigPath,
		RestartPolicy:   in.RestartPolicy,
	}
}

// BuildContainerSpecExternalFromInternal converts an internal ContainerSpec to the external ContainerSpec type.
// This is used for nested ContainerSpecs in CellSpec.
func BuildContainerSpecExternalFromInternal(in intmodel.ContainerSpec) ext.ContainerSpec {
	return ext.ContainerSpec{
		ID:              in.ID,
		ContainerdID:    in.ContainerdID,
		RealmID:         in.RealmName,
		SpaceID:         in.SpaceName,
		StackID:         in.StackName,
		CellID:          in.CellName,
		Root:            in.Root,
		Image:           in.Image,
		Command:         in.Command,
		Args:            in.Args,
		Env:             in.Env,
		Ports:           in.Ports,
		Volumes:         in.Volumes,
		Networks:        in.Networks,
		NetworksAliases: in.NetworksAliases,
		Privileged:      in.Privileged,
		CNIConfigPath:   in.CNIConfigPath,
		RestartPolicy:   in.RestartPolicy,
	}
}

// ConvertCellDocToInternal converts an external CellDoc to the internal hub type.
func ConvertCellDocToInternal(in ext.CellDoc) (intmodel.Cell, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		cell := intmodel.Cell{
			Metadata: intmodel.CellMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.CellSpec{
				ID:              in.Spec.ID,
				RealmName:       in.Spec.RealmID,
				SpaceName:       in.Spec.SpaceID,
				StackName:       in.Spec.StackID,
				RootContainerID: in.Spec.RootContainerID,
			},
			Status: intmodel.CellStatus{
				State:      intmodel.CellState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}

		// Convert nested Containers slice
		cell.Spec.Containers = make([]intmodel.ContainerSpec, len(in.Spec.Containers))
		for i, extContainer := range in.Spec.Containers {
			cell.Spec.Containers[i] = convertContainerSpecToInternal(extContainer)
		}

		// Validate that if RootContainerID is set, the container exists in Containers array
		if cell.Spec.RootContainerID != "" {
			found := false
			for _, container := range cell.Spec.Containers {
				if container.ID == cell.Spec.RootContainerID {
					found = true
					break
				}
			}
			if !found {
				return intmodel.Cell{}, fmt.Errorf(
					"rootContainerId %q not found in containers array",
					cell.Spec.RootContainerID,
				)
			}
		}

		return cell, nil
	default:
		return intmodel.Cell{}, fmt.Errorf("unsupported apiVersion for Cell: %s", in.APIVersion)
	}
}

// BuildCellExternalFromInternal emits an external CellDoc for a given version from an internal hub object.
func BuildCellExternalFromInternal(in intmodel.Cell, apiVersion ext.Version) (ext.CellDoc, error) {
	switch apiVersion {
	case VersionV1Beta1, "": // default to v1beta1
		cell := ext.CellDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindCell,
			Metadata: ext.CellMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.CellSpec{
				ID:              in.Spec.ID,
				RealmID:         in.Spec.RealmName,
				SpaceID:         in.Spec.SpaceName,
				StackID:         in.Spec.StackName,
				RootContainerID: in.Spec.RootContainerID,
			},
			Status: ext.CellStatus{
				State:      ext.CellState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
			},
		}

		// Convert nested Containers slice
		cell.Spec.Containers = make([]ext.ContainerSpec, len(in.Spec.Containers))
		for i, intContainer := range in.Spec.Containers {
			cell.Spec.Containers[i] = BuildContainerSpecExternalFromInternal(intContainer)
		}

		// Validate that if RootContainerID is set, the container exists in Containers array
		if cell.Spec.RootContainerID != "" {
			found := false
			for _, container := range cell.Spec.Containers {
				if container.ID == cell.Spec.RootContainerID {
					found = true
					break
				}
			}
			if !found {
				return ext.CellDoc{}, fmt.Errorf(
					"rootContainerId %q not found in containers array",
					cell.Spec.RootContainerID,
				)
			}
		}

		return cell, nil
	default:
		return ext.CellDoc{}, fmt.Errorf("unsupported output apiVersion for Cell: %s", apiVersion)
	}
}

// NormalizeCell takes an external CellDoc request and returns an internal object and chosen apiVersion.
func NormalizeCell(req ext.CellDoc) (intmodel.Cell, ext.Version, error) {
	version := req.APIVersion
	if version == "" {
		version = VersionV1Beta1
	}
	internal, err := ConvertCellDocToInternal(req)
	if err != nil {
		return intmodel.Cell{}, "", err
	}
	return internal, version, nil
}
