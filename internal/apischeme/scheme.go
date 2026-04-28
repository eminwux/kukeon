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
		registryCreds := make([]intmodel.RegistryCredentials, len(in.Spec.RegistryCredentials))
		for i, cred := range in.Spec.RegistryCredentials {
			registryCreds[i] = intmodel.RegistryCredentials{
				Username:      cred.Username,
				Password:      cred.Password,
				ServerAddress: cred.ServerAddress,
			}
		}
		return intmodel.Realm{
			Metadata: intmodel.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.RealmSpec{
				Namespace:           in.Spec.Namespace,
				RegistryCredentials: registryCreds,
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
		registryCreds := make([]ext.RegistryCredentials, len(in.Spec.RegistryCredentials))
		for i, cred := range in.Spec.RegistryCredentials {
			registryCreds[i] = ext.RegistryCredentials{
				Username:      cred.Username,
				Password:      cred.Password,
				ServerAddress: cred.ServerAddress,
			}
		}
		return ext.RealmDoc{
			APIVersion: VersionV1Beta1,
			Kind:       ext.KindRealm,
			Metadata: ext.RealmMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: ext.RealmSpec{
				Namespace:           in.Spec.Namespace,
				RegistryCredentials: registryCreds,
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
				Network:       convertSpaceNetworkToInternal(in.Spec.Network),
				Defaults:      convertSpaceDefaultsToInternal(in.Spec.Defaults),
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
				Network:       buildSpaceNetworkExternalFromInternal(in.Spec.Network),
				Defaults:      buildSpaceDefaultsExternalFromInternal(in.Spec.Defaults),
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
		if err := validateContainerTty(in.Spec); err != nil {
			return intmodel.Container{}, err
		}
		return intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name:   in.Metadata.Name,
				Labels: in.Metadata.Labels,
			},
			Spec: intmodel.ContainerSpec{
				ID:                     in.Spec.ID,
				RealmName:              in.Spec.RealmID,
				SpaceName:              in.Spec.SpaceID,
				StackName:              in.Spec.StackID,
				CellName:               in.Spec.CellID,
				Root:                   in.Spec.Root,
				Image:                  in.Spec.Image,
				Command:                in.Spec.Command,
				Args:                   in.Spec.Args,
				WorkingDir:             in.Spec.WorkingDir,
				Env:                    in.Spec.Env,
				Ports:                  in.Spec.Ports,
				Volumes:                volumeMountsToInternal(in.Spec.Volumes),
				Networks:               in.Spec.Networks,
				NetworksAliases:        in.Spec.NetworksAliases,
				Privileged:             in.Spec.Privileged,
				HostNetwork:            in.Spec.HostNetwork,
				HostPID:                in.Spec.HostPID,
				User:                   in.Spec.User,
				ReadOnlyRootFilesystem: in.Spec.ReadOnlyRootFilesystem,
				Capabilities:           convertCapabilitiesToInternal(in.Spec.Capabilities),
				SecurityOpts:           in.Spec.SecurityOpts,
				Tmpfs:                  convertTmpfsMountsToInternal(in.Spec.Tmpfs),
				Resources:              convertResourcesToInternal(in.Spec.Resources),
				Secrets:                convertSecretsToInternal(in.Spec.Secrets),
				CNIConfigPath:          in.Spec.CNIConfigPath,
				RestartPolicy:          in.Spec.RestartPolicy,
				Attachable:             in.Spec.Attachable,
				Tty:                    convertContainerTtyToInternal(in.Spec.Tty),
			},
			Status: intmodel.ContainerStatus{
				Name:         in.Status.Name,
				ID:           in.Status.ID,
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
				ID:                     in.Spec.ID,
				RealmID:                in.Spec.RealmName,
				SpaceID:                in.Spec.SpaceName,
				StackID:                in.Spec.StackName,
				CellID:                 in.Spec.CellName,
				Root:                   in.Spec.Root,
				Image:                  in.Spec.Image,
				Command:                in.Spec.Command,
				Args:                   in.Spec.Args,
				WorkingDir:             in.Spec.WorkingDir,
				Env:                    in.Spec.Env,
				Ports:                  in.Spec.Ports,
				Volumes:                volumeMountsToExternal(in.Spec.Volumes),
				Networks:               in.Spec.Networks,
				NetworksAliases:        in.Spec.NetworksAliases,
				Privileged:             in.Spec.Privileged,
				HostNetwork:            in.Spec.HostNetwork,
				HostPID:                in.Spec.HostPID,
				User:                   in.Spec.User,
				ReadOnlyRootFilesystem: in.Spec.ReadOnlyRootFilesystem,
				Capabilities:           buildCapabilitiesExternalFromInternal(in.Spec.Capabilities),
				SecurityOpts:           in.Spec.SecurityOpts,
				Tmpfs:                  buildTmpfsMountsExternalFromInternal(in.Spec.Tmpfs),
				Resources:              buildResourcesExternalFromInternal(in.Spec.Resources),
				Secrets:                buildSecretsExternalFromInternal(in.Spec.Secrets),
				CNIConfigPath:          in.Spec.CNIConfigPath,
				RestartPolicy:          in.Spec.RestartPolicy,
				Attachable:             in.Spec.Attachable,
				Tty:                    buildContainerTtyExternalFromInternal(in.Spec.Tty),
			},
			Status: ext.ContainerStatus{
				Name:         in.Status.Name,
				ID:           in.Status.ID,
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
		ID:                     in.ID,
		ContainerdID:           in.ContainerdID,
		RealmName:              in.RealmID,
		SpaceName:              in.SpaceID,
		StackName:              in.StackID,
		CellName:               in.CellID,
		Root:                   in.Root,
		Image:                  in.Image,
		Command:                in.Command,
		Args:                   in.Args,
		WorkingDir:             in.WorkingDir,
		Env:                    in.Env,
		Ports:                  in.Ports,
		Volumes:                volumeMountsToInternal(in.Volumes),
		Networks:               in.Networks,
		NetworksAliases:        in.NetworksAliases,
		Privileged:             in.Privileged,
		HostNetwork:            in.HostNetwork,
		HostPID:                in.HostPID,
		User:                   in.User,
		ReadOnlyRootFilesystem: in.ReadOnlyRootFilesystem,
		Capabilities:           convertCapabilitiesToInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  convertTmpfsMountsToInternal(in.Tmpfs),
		Resources:              convertResourcesToInternal(in.Resources),
		Secrets:                convertSecretsToInternal(in.Secrets),
		CNIConfigPath:          in.CNIConfigPath,
		RestartPolicy:          in.RestartPolicy,
		Attachable:             in.Attachable,
		Tty:                    convertContainerTtyToInternal(in.Tty),
	}
}

// BuildContainerSpecExternalFromInternal converts an internal ContainerSpec to the external ContainerSpec type.
// This is used for nested ContainerSpecs in CellSpec.
func BuildContainerSpecExternalFromInternal(in intmodel.ContainerSpec) ext.ContainerSpec {
	return ext.ContainerSpec{
		ID:                     in.ID,
		ContainerdID:           in.ContainerdID,
		RealmID:                in.RealmName,
		SpaceID:                in.SpaceName,
		StackID:                in.StackName,
		CellID:                 in.CellName,
		Root:                   in.Root,
		Image:                  in.Image,
		Command:                in.Command,
		Args:                   in.Args,
		WorkingDir:             in.WorkingDir,
		Env:                    in.Env,
		Ports:                  in.Ports,
		Volumes:                volumeMountsToExternal(in.Volumes),
		Networks:               in.Networks,
		NetworksAliases:        in.NetworksAliases,
		Privileged:             in.Privileged,
		HostNetwork:            in.HostNetwork,
		HostPID:                in.HostPID,
		User:                   in.User,
		ReadOnlyRootFilesystem: in.ReadOnlyRootFilesystem,
		Capabilities:           buildCapabilitiesExternalFromInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  buildTmpfsMountsExternalFromInternal(in.Tmpfs),
		Resources:              buildResourcesExternalFromInternal(in.Resources),
		Secrets:                buildSecretsExternalFromInternal(in.Secrets),
		CNIConfigPath:          in.CNIConfigPath,
		RestartPolicy:          in.RestartPolicy,
		Attachable:             in.Attachable,
		Tty:                    buildContainerTtyExternalFromInternal(in.Tty),
	}
}

// convertContainerTtyToInternal converts an external ContainerTty payload to
// the internal modelhub mirror. Returns nil for a nil or zero-value input so
// downstream callers can use the ContainerTty.IsEmpty contract.
func convertContainerTtyToInternal(in *ext.ContainerTty) *intmodel.ContainerTty {
	if in.IsEmpty() {
		return nil
	}
	out := &intmodel.ContainerTty{Prompt: in.Prompt}
	if len(in.OnInit) > 0 {
		out.OnInit = make([]intmodel.TtyStage, len(in.OnInit))
		for i, s := range in.OnInit {
			out.OnInit[i] = intmodel.TtyStage{Script: s.Script}
		}
	}
	return out
}

// buildContainerTtyExternalFromInternal is the inverse of
// convertContainerTtyToInternal.
func buildContainerTtyExternalFromInternal(in *intmodel.ContainerTty) *ext.ContainerTty {
	if in.IsEmpty() {
		return nil
	}
	out := &ext.ContainerTty{Prompt: in.Prompt}
	if len(in.OnInit) > 0 {
		out.OnInit = make([]ext.TtyStage, len(in.OnInit))
		for i, s := range in.OnInit {
			out.OnInit[i] = ext.TtyStage{Script: s.Script}
		}
	}
	return out
}

// convertCellTtyToInternal converts an external CellTty payload to the
// internal modelhub mirror. Returns nil when the block is absent or only
// contains zero-value fields.
func convertCellTtyToInternal(in *ext.CellTty) *intmodel.CellTty {
	if in == nil || in.Default == "" {
		return nil
	}
	return &intmodel.CellTty{Default: in.Default}
}

// buildCellTtyExternalFromInternal is the inverse of convertCellTtyToInternal.
func buildCellTtyExternalFromInternal(in *intmodel.CellTty) *ext.CellTty {
	if in == nil || in.Default == "" {
		return nil
	}
	return &ext.CellTty{Default: in.Default}
}

// validateContainerTty enforces the AC that any tty field set on a
// container with Attachable=false is a validation error. The tty block
// is config that only takes effect when Attachable=true (the capability
// gate); silently dropping it on a non-attachable container would let a
// future apply silently ignore configured prompts/onInit scripts.
func validateContainerTty(spec ext.ContainerSpec) error {
	if spec.Attachable {
		return nil
	}
	if spec.Tty.IsEmpty() {
		return nil
	}
	return fmt.Errorf("container %q: tty fields require attachable: true", spec.ID)
}

// validateCellTty enforces the AC that CellTty.Default names an existing
// attachable container in the same cell (or is empty).
func validateCellTty(spec ext.CellSpec) error {
	if spec.Tty == nil || spec.Tty.Default == "" {
		return nil
	}
	for _, c := range spec.Containers {
		if c.ID == spec.Tty.Default {
			if !c.Attachable {
				return fmt.Errorf(
					"cell tty.default %q references container that is not attachable",
					spec.Tty.Default,
				)
			}
			return nil
		}
	}
	return fmt.Errorf(
		"cell tty.default %q does not match any container in this cell",
		spec.Tty.Default,
	)
}

func convertCapabilitiesToInternal(in *ext.ContainerCapabilities) *intmodel.ContainerCapabilities {
	if in == nil {
		return nil
	}
	return &intmodel.ContainerCapabilities{
		Drop: in.Drop,
		Add:  in.Add,
	}
}

func buildCapabilitiesExternalFromInternal(in *intmodel.ContainerCapabilities) *ext.ContainerCapabilities {
	if in == nil {
		return nil
	}
	return &ext.ContainerCapabilities{
		Drop: in.Drop,
		Add:  in.Add,
	}
}

func convertTmpfsMountsToInternal(in []ext.ContainerTmpfsMount) []intmodel.ContainerTmpfsMount {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.ContainerTmpfsMount, len(in))
	for i, m := range in {
		out[i] = intmodel.ContainerTmpfsMount{
			Path:      m.Path,
			SizeBytes: m.SizeBytes,
			Options:   m.Options,
		}
	}
	return out
}

func buildTmpfsMountsExternalFromInternal(in []intmodel.ContainerTmpfsMount) []ext.ContainerTmpfsMount {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.ContainerTmpfsMount, len(in))
	for i, m := range in {
		out[i] = ext.ContainerTmpfsMount{
			Path:      m.Path,
			SizeBytes: m.SizeBytes,
			Options:   m.Options,
		}
	}
	return out
}

func convertResourcesToInternal(in *ext.ContainerResources) *intmodel.ContainerResources {
	if in == nil {
		return nil
	}
	return &intmodel.ContainerResources{
		MemoryLimitBytes: in.MemoryLimitBytes,
		CPUShares:        in.CPUShares,
		PidsLimit:        in.PidsLimit,
	}
}

func buildResourcesExternalFromInternal(in *intmodel.ContainerResources) *ext.ContainerResources {
	if in == nil {
		return nil
	}
	return &ext.ContainerResources{
		MemoryLimitBytes: in.MemoryLimitBytes,
		CPUShares:        in.CPUShares,
		PidsLimit:        in.PidsLimit,
	}
}

// convertSecretsToInternal copies external secret references into the internal
// model. Only the reference metadata (name + source + optional mountPath) is
// carried; there is no value field on either side.
func convertSecretsToInternal(in []ext.ContainerSecret) []intmodel.ContainerSecret {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.ContainerSecret, len(in))
	for i, s := range in {
		out[i] = intmodel.ContainerSecret{
			Name:      s.Name,
			FromFile:  s.FromFile,
			FromEnv:   s.FromEnv,
			MountPath: s.MountPath,
		}
	}
	return out
}

// buildSecretsExternalFromInternal is the inverse of convertSecretsToInternal.
func buildSecretsExternalFromInternal(in []intmodel.ContainerSecret) []ext.ContainerSecret {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.ContainerSecret, len(in))
	for i, s := range in {
		out[i] = ext.ContainerSecret{
			Name:      s.Name,
			FromFile:  s.FromFile,
			FromEnv:   s.FromEnv,
			MountPath: s.MountPath,
		}
	}
	return out
}

func convertSpaceDefaultsToInternal(in *ext.SpaceDefaults) *intmodel.SpaceDefaults {
	if in == nil {
		return nil
	}
	return &intmodel.SpaceDefaults{
		Container: convertSpaceContainerDefaultsToInternal(in.Container),
	}
}

func buildSpaceDefaultsExternalFromInternal(in *intmodel.SpaceDefaults) *ext.SpaceDefaults {
	if in == nil {
		return nil
	}
	return &ext.SpaceDefaults{
		Container: buildSpaceContainerDefaultsExternalFromInternal(in.Container),
	}
}

func convertSpaceContainerDefaultsToInternal(in *ext.SpaceContainerDefaults) *intmodel.SpaceContainerDefaults {
	if in == nil {
		return nil
	}
	return &intmodel.SpaceContainerDefaults{
		User:                   in.User,
		ReadOnlyRootFilesystem: copyBoolPtr(in.ReadOnlyRootFilesystem),
		Capabilities:           convertCapabilitiesToInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  convertTmpfsMountsToInternal(in.Tmpfs),
		Resources:              convertResourcesToInternal(in.Resources),
	}
}

func buildSpaceContainerDefaultsExternalFromInternal(in *intmodel.SpaceContainerDefaults) *ext.SpaceContainerDefaults {
	if in == nil {
		return nil
	}
	return &ext.SpaceContainerDefaults{
		User:                   in.User,
		ReadOnlyRootFilesystem: copyBoolPtr(in.ReadOnlyRootFilesystem),
		Capabilities:           buildCapabilitiesExternalFromInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  buildTmpfsMountsExternalFromInternal(in.Tmpfs),
		Resources:              buildResourcesExternalFromInternal(in.Resources),
	}
}

func copyBoolPtr(in *bool) *bool {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func volumeMountsToInternal(in []ext.VolumeMount) []intmodel.VolumeMount {
	if in == nil {
		return nil
	}
	out := make([]intmodel.VolumeMount, len(in))
	for i, v := range in {
		out[i] = intmodel.VolumeMount{
			Source:   v.Source,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		}
	}
	return out
}

func volumeMountsToExternal(in []intmodel.VolumeMount) []ext.VolumeMount {
	if in == nil {
		return nil
	}
	out := make([]ext.VolumeMount, len(in))
	for i, v := range in {
		out[i] = ext.VolumeMount{
			Source:   v.Source,
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		}
	}
	return out
}

// convertContainerStatusesToInternal converts a slice of external ContainerStatus to internal ContainerStatus.
func convertContainerStatusesToInternal(in []ext.ContainerStatus) []intmodel.ContainerStatus {
	if in == nil {
		return []intmodel.ContainerStatus{}
	}
	result := make([]intmodel.ContainerStatus, len(in))
	for i, status := range in {
		result[i] = intmodel.ContainerStatus{
			Name:         status.Name,
			ID:           status.ID,
			State:        intmodel.ContainerState(status.State),
			RestartCount: status.RestartCount,
			RestartTime:  status.RestartTime,
			StartTime:    status.StartTime,
			FinishTime:   status.FinishTime,
			ExitCode:     status.ExitCode,
			ExitSignal:   status.ExitSignal,
		}
	}
	return result
}

// buildContainerStatusesExternalFromInternal converts a slice of internal ContainerStatus to external ContainerStatus.
func buildContainerStatusesExternalFromInternal(in []intmodel.ContainerStatus) []ext.ContainerStatus {
	if in == nil {
		return []ext.ContainerStatus{}
	}
	result := make([]ext.ContainerStatus, len(in))
	for i, status := range in {
		result[i] = ext.ContainerStatus{
			Name:         status.Name,
			ID:           status.ID,
			State:        ext.ContainerState(status.State),
			RestartCount: status.RestartCount,
			RestartTime:  status.RestartTime,
			StartTime:    status.StartTime,
			FinishTime:   status.FinishTime,
			ExitCode:     status.ExitCode,
			ExitSignal:   status.ExitSignal,
		}
	}
	return result
}

// ConvertCellDocToInternal converts an external CellDoc to the internal hub type.
func ConvertCellDocToInternal(in ext.CellDoc) (intmodel.Cell, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		for _, c := range in.Spec.Containers {
			if err := validateContainerTty(c); err != nil {
				return intmodel.Cell{}, err
			}
		}
		if err := validateCellTty(in.Spec); err != nil {
			return intmodel.Cell{}, err
		}
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
				Tty:             convertCellTtyToInternal(in.Spec.Tty),
			},
			Status: intmodel.CellStatus{
				State:      intmodel.CellState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
				Network: intmodel.CellNetworkStatus{
					BridgeName: in.Status.Network.BridgeName,
				},
				Containers: convertContainerStatusesToInternal(in.Status.Containers),
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
				Tty:             buildCellTtyExternalFromInternal(in.Spec.Tty),
			},
			Status: ext.CellStatus{
				State:      ext.CellState(in.Status.State),
				CgroupPath: in.Status.CgroupPath,
				Network: ext.CellNetworkStatus{
					BridgeName: in.Status.Network.BridgeName,
				},
				Containers: buildContainerStatusesExternalFromInternal(in.Status.Containers),
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

func convertSpaceNetworkToInternal(in *ext.SpaceNetwork) *intmodel.SpaceNetwork {
	if in == nil {
		return nil
	}
	out := &intmodel.SpaceNetwork{}
	if in.Egress != nil {
		allow := make([]intmodel.EgressAllowRule, len(in.Egress.Allow))
		for i, r := range in.Egress.Allow {
			ports := append([]int(nil), r.Ports...)
			allow[i] = intmodel.EgressAllowRule{
				Host:  r.Host,
				CIDR:  r.CIDR,
				Ports: ports,
			}
		}
		out.Egress = &intmodel.EgressPolicy{
			Default: intmodel.EgressDefault(in.Egress.Default),
			Allow:   allow,
		}
	}
	return out
}

func buildSpaceNetworkExternalFromInternal(in *intmodel.SpaceNetwork) *ext.SpaceNetwork {
	if in == nil {
		return nil
	}
	out := &ext.SpaceNetwork{}
	if in.Egress != nil {
		allow := make([]ext.EgressAllowRule, len(in.Egress.Allow))
		for i, r := range in.Egress.Allow {
			ports := append([]int(nil), r.Ports...)
			allow[i] = ext.EgressAllowRule{
				Host:  r.Host,
				CIDR:  r.CIDR,
				Ports: ports,
			}
		}
		out.Egress = &ext.EgressPolicy{
			Default: ext.EgressDefault(in.Egress.Default),
			Allow:   allow,
		}
	}
	return out
}
