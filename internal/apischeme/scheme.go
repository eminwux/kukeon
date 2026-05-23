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

	"github.com/eminwux/kukeon/internal/errdefs"
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: intmodel.RealmSpec{
				Namespace:           in.Spec.Namespace,
				RegistryCredentials: registryCreds,
			},
			Status: intmodel.RealmStatus{
				State:                    intmodel.RealmState(in.Status.State),
				CgroupPath:               in.Status.CgroupPath,
				SubtreeControllers:       cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:                in.Status.CreatedAt,
				UpdatedAt:                in.Status.UpdatedAt,
				ReadyAt:                  in.Status.ReadyAt,
				Reason:                   in.Status.Reason,
				Message:                  in.Status.Message,
				CgroupReady:              in.Status.CgroupReady,
				ContainerdNamespaceReady: in.Status.ContainerdNamespaceReady,
				ObservedGeneration:       in.Status.ObservedGeneration,
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: ext.RealmSpec{
				Namespace:           in.Spec.Namespace,
				RegistryCredentials: registryCreds,
			},
			Status: ext.RealmStatus{
				State:                    ext.RealmState(in.Status.State),
				CgroupPath:               in.Status.CgroupPath,
				SubtreeControllers:       cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:                in.Status.CreatedAt,
				UpdatedAt:                in.Status.UpdatedAt,
				ReadyAt:                  in.Status.ReadyAt,
				Reason:                   in.Status.Reason,
				Message:                  in.Status.Message,
				CgroupReady:              in.Status.CgroupReady,
				ContainerdNamespaceReady: in.Status.ContainerdNamespaceReady,
				ObservedGeneration:       in.Status.ObservedGeneration,
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

// ConvertSecretDocToInternal converts an external SecretDoc to the internal
// hub type (issue #619). A Secret has no status to map, so this is a flat
// field copy of the scope coordinates and the material.
func ConvertSecretDocToInternal(in ext.SecretDoc) (intmodel.Secret, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		return intmodel.Secret{
			Metadata: intmodel.SecretMetadata{
				Name:  in.Metadata.Name,
				Realm: in.Metadata.Realm,
				Space: in.Metadata.Space,
				Stack: in.Metadata.Stack,
				Cell:  in.Metadata.Cell,
			},
			Spec: intmodel.SecretSpec{
				Data: in.Spec.Data,
			},
		}, nil
	default:
		return intmodel.Secret{}, fmt.Errorf("unsupported apiVersion for Secret: %s", in.APIVersion)
	}
}

// NormalizeSecret takes an external SecretDoc request and returns an internal
// object and chosen apiVersion.
func NormalizeSecret(req ext.SecretDoc) (intmodel.Secret, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertSecretDocToInternal(req)
	if err != nil {
		return intmodel.Secret{}, "", err
	}
	return internal, version, nil
}

// ConvertSecretToExternal builds a metadata-only external SecretDoc from the
// internal hub type (issue #622). Spec.Data is deliberately left zero: the
// get/list verbs never echo the secret material, preserving the
// never-round-tripped contract from #619.
func ConvertSecretToExternal(in intmodel.Secret) ext.SecretDoc {
	return ext.SecretDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindSecret,
		Metadata: ext.SecretMetadata{
			Name:  in.Metadata.Name,
			Realm: in.Metadata.Realm,
			Space: in.Metadata.Space,
			Stack: in.Metadata.Stack,
			Cell:  in.Metadata.Cell,
		},
	}
}

// ConvertSecretListToExternal maps a slice of internal Secrets to metadata-only
// external SecretDocs (issue #622).
func ConvertSecretListToExternal(in []intmodel.Secret) []ext.SecretDoc {
	if in == nil {
		return nil
	}
	out := make([]ext.SecretDoc, len(in))
	for i := range in {
		out[i] = ConvertSecretToExternal(in[i])
	}
	return out
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: intmodel.SpaceSpec{
				RealmName:     in.Spec.RealmID,
				CNIConfigPath: in.Spec.CNIConfigPath,
				Network:       convertSpaceNetworkToInternal(in.Spec.Network),
				Defaults:      convertSpaceDefaultsToInternal(in.Spec.Defaults),
			},
			Status: intmodel.SpaceStatus{
				State:              intState,
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: ext.SpaceSpec{
				RealmID:       in.Spec.RealmName,
				CNIConfigPath: in.Spec.CNIConfigPath,
				Network:       buildSpaceNetworkExternalFromInternal(in.Spec.Network),
				Defaults:      buildSpaceDefaultsExternalFromInternal(in.Spec.Defaults),
			},
			Status: ext.SpaceStatus{
				State:              extState,
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: intmodel.StackSpec{
				ID:        in.Spec.ID,
				RealmName: in.Spec.RealmID,
				SpaceName: in.Spec.SpaceID,
			},
			Status: intmodel.StackStatus{
				State:              intmodel.StackState(in.Status.State),
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: ext.StackSpec{
				ID:      in.Spec.ID,
				RealmID: in.Spec.RealmName,
				SpaceID: in.Spec.SpaceName,
			},
			Status: ext.StackStatus{
				State:              ext.StackState(in.Status.State),
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
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
		if err := validateContainerGit(in.Spec); err != nil {
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
				HostCgroup:             in.Spec.HostCgroup,
				User:                   in.Spec.User,
				ReadOnlyRootFilesystem: in.Spec.ReadOnlyRootFilesystem,
				Capabilities:           convertCapabilitiesToInternal(in.Spec.Capabilities),
				SecurityOpts:           in.Spec.SecurityOpts,
				Tmpfs:                  convertTmpfsMountsToInternal(in.Spec.Tmpfs),
				Resources:              convertResourcesToInternal(in.Spec.Resources),
				Secrets:                convertSecretsToInternal(in.Spec.Secrets),
				Repos:                  reposToInternal(in.Spec.Repos),
				Git:                    gitToInternal(in.Spec.Git),
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
				Repos:        repoStatusesToInternal(in.Status.Repos),
				Stages:       stageStatusesToInternal(in.Status.Stages),
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
				HostCgroup:             in.Spec.HostCgroup,
				User:                   in.Spec.User,
				ReadOnlyRootFilesystem: in.Spec.ReadOnlyRootFilesystem,
				Capabilities:           buildCapabilitiesExternalFromInternal(in.Spec.Capabilities),
				SecurityOpts:           in.Spec.SecurityOpts,
				Tmpfs:                  buildTmpfsMountsExternalFromInternal(in.Spec.Tmpfs),
				Resources:              buildResourcesExternalFromInternal(in.Spec.Resources),
				Secrets:                buildSecretsExternalFromInternal(in.Spec.Secrets),
				Repos:                  reposToExternal(in.Spec.Repos),
				Git:                    gitToExternal(in.Spec.Git),
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
				Repos:        repoStatusesToExternal(in.Status.Repos),
				Stages:       stageStatusesToExternal(in.Status.Stages),
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
		HostCgroup:             in.HostCgroup,
		User:                   in.User,
		ReadOnlyRootFilesystem: in.ReadOnlyRootFilesystem,
		Capabilities:           convertCapabilitiesToInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  convertTmpfsMountsToInternal(in.Tmpfs),
		Resources:              convertResourcesToInternal(in.Resources),
		Secrets:                convertSecretsToInternal(in.Secrets),
		Repos:                  reposToInternal(in.Repos),
		Git:                    gitToInternal(in.Git),
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
		HostCgroup:             in.HostCgroup,
		User:                   in.User,
		ReadOnlyRootFilesystem: in.ReadOnlyRootFilesystem,
		Capabilities:           buildCapabilitiesExternalFromInternal(in.Capabilities),
		SecurityOpts:           in.SecurityOpts,
		Tmpfs:                  buildTmpfsMountsExternalFromInternal(in.Tmpfs),
		Resources:              buildResourcesExternalFromInternal(in.Resources),
		Secrets:                buildSecretsExternalFromInternal(in.Secrets),
		Repos:                  reposToExternal(in.Repos),
		Git:                    gitToExternal(in.Git),
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
	out := &intmodel.ContainerTty{
		Prompt:   in.Prompt,
		LogFile:  in.LogFile,
		LogLevel: in.LogLevel,
	}
	if len(in.OnInit) > 0 {
		out.OnInit = make([]intmodel.TtyStage, len(in.OnInit))
		for i, s := range in.OnInit {
			out.OnInit[i] = intmodel.TtyStage{Script: s.Script, RunOn: s.RunOn}
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
	out := &ext.ContainerTty{
		Prompt:   in.Prompt,
		LogFile:  in.LogFile,
		LogLevel: in.LogLevel,
	}
	if len(in.OnInit) > 0 {
		out.OnInit = make([]ext.TtyStage, len(in.OnInit))
		for i, s := range in.OnInit {
			out.OnInit[i] = ext.TtyStage{Script: s.Script, RunOn: s.RunOn}
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
//
// It also enforces the LogLevel enum (issue #599): empty or one of
// debug/info/warn/error. Unknown values are rejected at apply time
// rather than silently coerced to "info" downstream, since the
// kuketty wrapper's debug log is the operator's primary diagnostic
// when an attach session misbehaves.
func validateContainerTty(spec ext.ContainerSpec) error {
	if !spec.Attachable {
		if !spec.Tty.IsEmpty() {
			return fmt.Errorf("container %q: tty fields require attachable: true", spec.ID)
		}
		return nil
	}
	if spec.Tty == nil {
		return nil
	}
	if err := validateTtyLogLevel(spec.Tty.LogLevel); err != nil {
		return fmt.Errorf("container %q: %w", spec.ID, err)
	}
	for i, s := range spec.Tty.OnInit {
		if err := validateTtyStageRunOn(s.RunOn); err != nil {
			return fmt.Errorf("container %q: onInit[%d]: %w", spec.ID, i, err)
		}
	}
	if err := validateContainerCreateStagePersistence(spec); err != nil {
		return err
	}
	return nil
}

// validateContainerCreateStagePersistence enforces that a container declaring
// runOn: create stages has at least one persistent writable mount. Without one,
// the side effects of create stages (npm ci, DB seed, bootstrap) evaporate when
// the container's writable layer is dropped on the next recreate while
// ContainerStatus.Stages still reports State == "done" — the run-once gate
// (phase C2, #737) would then silently skip a stage whose effect no longer
// exists. TtyStage has no per-stage target field, so the reasoning is
// container-scoped: any runOn: create stage on a container with no persistent
// writable mount is at risk. Issue #738.
//
// A mount is treated as a persistent writable target when it is a VolumeMount
// whose Kind is anything other than tmpfs (so VolumeKindBind, including the
// empty back-compat default, plus any future persistent kind like a future
// pvc:) and is not declared ReadOnly. ReadOnlyRootFilesystem alone does not
// affect the decision — the test is solely the presence of at least one
// persistent writable VolumeMount. ContainerSpec.Tmpfs entries are explicitly
// ephemeral and never count.
func validateContainerCreateStagePersistence(spec ext.ContainerSpec) error {
	if spec.Tty == nil {
		return nil
	}
	hasCreate := false
	for _, s := range spec.Tty.OnInit {
		if s.RunOn == ext.RunOnCreate {
			hasCreate = true
			break
		}
	}
	if !hasCreate {
		return nil
	}
	for _, v := range spec.Volumes {
		if v.Kind == ext.VolumeKindTmpfs {
			continue
		}
		if v.ReadOnly {
			continue
		}
		return nil
	}
	return fmt.Errorf(
		"container %q: tty.onInit has runOn: %q stages but the container has no persistent writable mount; "+
			"declare a volumes: entry of kind bind covering the stage's write target so its side effects "+
			"survive container recreate, or move the work into a runOn: %q stage",
		spec.ID, ext.RunOnCreate, ext.RunOnStart,
	)
}

// validateTtyLogLevel accepts the empty string (the daemon defaults to
// "info" downstream) or one of the four sbsh log levels. Issue #599.
func validateTtyLogLevel(level string) error {
	switch level {
	case "", "debug", "info", "warn", "error":
		return nil
	}
	return fmt.Errorf("tty.logLevel %q: must be one of debug, info, warn, error", level)
}

// validateTtyStageRunOn accepts the empty string (treated as "start"
// downstream) or one of the two known runOn values. Unknown values are
// rejected at apply time rather than silently routed to the default lane, so a
// typo'd `runOn: craete` surfaces as an error instead of running the stage on
// every boot. Issue #635.
func validateTtyStageRunOn(runOn string) error {
	switch runOn {
	case "", ext.RunOnStart, ext.RunOnCreate:
		return nil
	}
	return fmt.Errorf("tty stage runOn %q: must be one of %q, %q (or empty)", runOn, ext.RunOnStart, ext.RunOnCreate)
}

// resolveCellRootContainer enforces the rules around CellSpec.RootContainerID
// and ContainerSpec.Root and returns the resolved root container ID:
//   - At most one container may carry root: true. Issue #349: a second
//     root-flagged container would silently lose its Root intent, so it
//     surfaces as a hard error here.
//   - If RootContainerID is set, the named container must exist in the
//     array, and any container marked root: true must be that same one.
//   - If RootContainerID is empty but exactly one container has
//     root: true, that container's ID is the resolved root. Callers
//     building a normalized internal cell should overwrite
//     spec.RootContainerID with the returned value so downstream code
//     (runner.ensureCellRootContainerSpec, helpers.findRootContainer,
//     etc.) sees a self-consistent cell.
//   - If neither is set, returns "" — the runner builds a default root.
func resolveCellRootContainer(spec intmodel.CellSpec) (string, error) {
	var rootMarked []string
	for _, c := range spec.Containers {
		if c.Root {
			rootMarked = append(rootMarked, c.ID)
		}
	}
	if len(rootMarked) > 1 {
		return "", fmt.Errorf("%w: %v", errdefs.ErrMultipleRootContainers, rootMarked)
	}

	if spec.RootContainerID != "" {
		found := false
		for _, c := range spec.Containers {
			if c.ID == spec.RootContainerID {
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf(
				"rootContainerId %q not found in containers array",
				spec.RootContainerID,
			)
		}
		if len(rootMarked) == 1 && rootMarked[0] != spec.RootContainerID {
			return "", fmt.Errorf(
				"%w: rootContainerId is %q but container %q has root: true",
				errdefs.ErrRootContainerMismatch,
				spec.RootContainerID,
				rootMarked[0],
			)
		}
		return spec.RootContainerID, nil
	}

	if len(rootMarked) == 1 {
		return rootMarked[0], nil
	}
	return "", nil
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
			SecretRef: secretRefToInternal(s.SecretRef),
			MountPath: s.MountPath,
		}
	}
	return out
}

// secretRefToInternal copies an external ContainerSecretRef into the internal
// model, preserving nil. Issue #623.
func secretRefToInternal(in *ext.ContainerSecretRef) *intmodel.ContainerSecretRef {
	if in == nil {
		return nil
	}
	return &intmodel.ContainerSecretRef{
		Name:  in.Name,
		Realm: in.Realm,
		Space: in.Space,
		Stack: in.Stack,
		Cell:  in.Cell,
	}
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
			SecretRef: secretRefToExternal(s.SecretRef),
			MountPath: s.MountPath,
		}
	}
	return out
}

// secretRefToExternal is the inverse of secretRefToInternal. Issue #623.
func secretRefToExternal(in *intmodel.ContainerSecretRef) *ext.ContainerSecretRef {
	if in == nil {
		return nil
	}
	return &ext.ContainerSecretRef{
		Name:  in.Name,
		Realm: in.Realm,
		Space: in.Space,
		Stack: in.Stack,
		Cell:  in.Cell,
	}
}

// reposToInternal copies external repo declarations into the internal model.
// Issue #617.
func reposToInternal(in []ext.ContainerRepo) []intmodel.ContainerRepo {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.ContainerRepo, len(in))
	for i, r := range in {
		out[i] = intmodel.ContainerRepo{
			Name:     r.Name,
			Target:   r.Target,
			Branch:   r.Branch,
			URL:      r.URL,
			Required: r.Required,
		}
	}
	return out
}

// reposToExternal is the inverse of reposToInternal.
func reposToExternal(in []intmodel.ContainerRepo) []ext.ContainerRepo {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.ContainerRepo, len(in))
	for i, r := range in {
		out[i] = ext.ContainerRepo{
			Name:     r.Name,
			Target:   r.Target,
			Branch:   r.Branch,
			URL:      r.URL,
			Required: r.Required,
		}
	}
	return out
}

// repoStatusesToInternal copies external per-repo status into the internal
// model. Issue #617.
func repoStatusesToInternal(in []ext.RepoStatus) []intmodel.RepoStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.RepoStatus, len(in))
	for i, s := range in {
		out[i] = intmodel.RepoStatus{
			Name:   s.Name,
			Target: s.Target,
			State:  s.State,
			Commit: s.Commit,
			Error:  s.Error,
		}
	}
	return out
}

// repoStatusesToExternal is the inverse of repoStatusesToInternal.
func repoStatusesToExternal(in []intmodel.RepoStatus) []ext.RepoStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.RepoStatus, len(in))
	for i, s := range in {
		out[i] = ext.RepoStatus{
			Name:   s.Name,
			Target: s.Target,
			State:  s.State,
			Commit: s.Commit,
			Error:  s.Error,
		}
	}
	return out
}

// stageStatusesToInternal copies external per-stage status into the internal
// model. Schema only this phase; populated in phase B (#689). Issue #635.
func stageStatusesToInternal(in []ext.StageStatus) []intmodel.StageStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]intmodel.StageStatus, len(in))
	for i, s := range in {
		out[i] = intmodel.StageStatus{
			Index: s.Index,
			State: s.State,
			Error: s.Error,
		}
	}
	return out
}

// stageStatusesToExternal is the inverse of stageStatusesToInternal.
func stageStatusesToExternal(in []intmodel.StageStatus) []ext.StageStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.StageStatus, len(in))
	for i, s := range in {
		out[i] = ext.StageStatus{
			Index: s.Index,
			State: s.State,
			Error: s.Error,
		}
	}
	return out
}

// gitToInternal copies the external git sugar block into the internal model,
// deep-copying the Author/Committer pointers and Sign slice. Issue #618.
func gitToInternal(in *ext.ContainerGit) *intmodel.ContainerGit {
	if in == nil {
		return nil
	}
	out := &intmodel.ContainerGit{
		SigningKey:     in.SigningKey,
		Sign:           cloneStringSlice(in.Sign),
		AllowedSigners: in.AllowedSigners,
	}
	if in.Author != nil {
		out.Author = &intmodel.GitIdentity{Name: in.Author.Name, Email: in.Author.Email}
	}
	if in.Committer != nil {
		out.Committer = &intmodel.GitIdentity{Name: in.Committer.Name, Email: in.Committer.Email}
	}
	return out
}

// gitToExternal is the inverse of gitToInternal.
func gitToExternal(in *intmodel.ContainerGit) *ext.ContainerGit {
	if in == nil {
		return nil
	}
	out := &ext.ContainerGit{
		SigningKey:     in.SigningKey,
		Sign:           cloneStringSlice(in.Sign),
		AllowedSigners: in.AllowedSigners,
	}
	if in.Author != nil {
		out.Author = &ext.GitIdentity{Name: in.Author.Name, Email: in.Author.Email}
	}
	if in.Committer != nil {
		out.Committer = &ext.GitIdentity{Name: in.Committer.Name, Email: in.Committer.Email}
	}
	return out
}

// validateContainerGit enforces the ContainerGit invariants (issue #618):
//   - author and committer each require both name and email when present, so
//     a half-specified identity never renders a GIT_AUTHOR_EMAIL= empty entry.
//   - sign requires signingKey, since commit.gpgsign/tag.gpgsign with no
//     user.signingkey makes git fail the commit at runtime rather than at
//     apply time.
//   - each sign entry must be a known target (commits or tags), rejected at
//     apply time rather than silently dropped during expansion.
func validateContainerGit(spec ext.ContainerSpec) error {
	git := spec.Git
	if git == nil {
		return nil
	}
	if err := validateGitIdentity("author", git.Author); err != nil {
		return fmt.Errorf("container %q: %w", spec.ID, err)
	}
	if err := validateGitIdentity("committer", git.Committer); err != nil {
		return fmt.Errorf("container %q: %w", spec.ID, err)
	}
	for _, s := range git.Sign {
		if s != ext.GitSignCommits && s != ext.GitSignTags {
			return fmt.Errorf(
				"container %q: git.sign %q: must be one of %q, %q",
				spec.ID, s, ext.GitSignCommits, ext.GitSignTags,
			)
		}
	}
	if len(git.Sign) > 0 && git.SigningKey == "" {
		return fmt.Errorf("container %q: git.sign requires git.signingKey to be set", spec.ID)
	}
	return nil
}

// validateGitIdentity rejects a git identity that sets only one of name/email
// — both are required so the expanded GIT_*_NAME / GIT_*_EMAIL pair is never
// half-empty.
func validateGitIdentity(role string, id *ext.GitIdentity) error {
	if id == nil {
		return nil
	}
	if id.Name == "" || id.Email == "" {
		return fmt.Errorf("git.%s requires both name and email", role)
	}
	return nil
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

// cloneStringSlice returns a deep copy of in. Used by Status conversions so
// the external and internal models do not alias the same backing array.
// Returns nil for nil/empty input so the resulting Status field stays nil
// and `omitempty`-tagged JSON/YAML emit nothing (issue #328).
func cloneStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
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
			Kind:      intmodel.VolumeKind(v.Kind),
			Source:    v.Source,
			Target:    v.Target,
			ReadOnly:  v.ReadOnly,
			SizeBytes: v.SizeBytes,
			Mode:      v.Mode,
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
			Kind:      ext.VolumeKind(v.Kind),
			Source:    v.Source,
			Target:    v.Target,
			ReadOnly:  v.ReadOnly,
			SizeBytes: v.SizeBytes,
			Mode:      v.Mode,
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
			if err := validateContainerGit(c); err != nil {
				return intmodel.Cell{}, err
			}
		}
		if err := validateCellTty(in.Spec); err != nil {
			return intmodel.Cell{}, err
		}
		cell := intmodel.Cell{
			Metadata: intmodel.CellMetadata{
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: intmodel.CellSpec{
				ID:                  in.Spec.ID,
				RealmName:           in.Spec.RealmID,
				SpaceName:           in.Spec.SpaceID,
				StackName:           in.Spec.StackID,
				RootContainerID:     in.Spec.RootContainerID,
				Tty:                 convertCellTtyToInternal(in.Spec.Tty),
				AutoDelete:          in.Spec.AutoDelete,
				NestedCgroupRuntime: in.Spec.NestedCgroupRuntime,
			},
			Status: intmodel.CellStatus{
				State:              intmodel.CellState(in.Status.State),
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				Network: intmodel.CellNetworkStatus{
					BridgeName: in.Status.Network.BridgeName,
				},
				Containers:         convertContainerStatusesToInternal(in.Status.Containers),
				ReadyObserved:      in.Status.ReadyObserved,
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
			},
		}

		// Convert nested Containers slice
		cell.Spec.Containers = make([]intmodel.ContainerSpec, len(in.Spec.Containers))
		for i, extContainer := range in.Spec.Containers {
			cell.Spec.Containers[i] = convertContainerSpecToInternal(extContainer)
		}

		// Resolve RootContainerID. Auto-populates the field when a
		// container in the array carries root: true with no explicit
		// rootContainerId set, so the runner's explicit-root branch
		// (and every downstream consumer of cell.Spec.RootContainerID)
		// sees the user-supplied root spec instead of a default-built
		// one (issue #349).
		resolvedRoot, err := resolveCellRootContainer(cell.Spec)
		if err != nil {
			return intmodel.Cell{}, err
		}
		cell.Spec.RootContainerID = resolvedRoot

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
				Name:       in.Metadata.Name,
				Labels:     in.Metadata.Labels,
				Generation: in.Metadata.Generation,
			},
			Spec: ext.CellSpec{
				ID:                  in.Spec.ID,
				RealmID:             in.Spec.RealmName,
				SpaceID:             in.Spec.SpaceName,
				StackID:             in.Spec.StackName,
				RootContainerID:     in.Spec.RootContainerID,
				Tty:                 buildCellTtyExternalFromInternal(in.Spec.Tty),
				AutoDelete:          in.Spec.AutoDelete,
				NestedCgroupRuntime: in.Spec.NestedCgroupRuntime,
			},
			Status: ext.CellStatus{
				State:              ext.CellState(in.Status.State),
				CgroupPath:         in.Status.CgroupPath,
				SubtreeControllers: cloneStringSlice(in.Status.SubtreeControllers),
				Network: ext.CellNetworkStatus{
					BridgeName: in.Status.Network.BridgeName,
				},
				Containers:         buildContainerStatusesExternalFromInternal(in.Status.Containers),
				ReadyObserved:      in.Status.ReadyObserved,
				CreatedAt:          in.Status.CreatedAt,
				UpdatedAt:          in.Status.UpdatedAt,
				ReadyAt:            in.Status.ReadyAt,
				Reason:             in.Status.Reason,
				Message:            in.Status.Message,
				CgroupReady:        in.Status.CgroupReady,
				ObservedGeneration: in.Status.ObservedGeneration,
			},
		}

		// Convert nested Containers slice
		cell.Spec.Containers = make([]ext.ContainerSpec, len(in.Spec.Containers))
		for i, intContainer := range in.Spec.Containers {
			cell.Spec.Containers[i] = BuildContainerSpecExternalFromInternal(intContainer)
		}

		// Defensive validation on the way out: a malformed internal
		// cell (e.g. constructed without going through ConvertCellDoc-
		// ToInternal) should not silently round-trip. Reuses the same
		// rules the inbound path applies; the resolved ID is ignored
		// because the persisted RootContainerID is the source of truth
		// for serialization.
		if _, err := resolveCellRootContainer(in.Spec); err != nil {
			return ext.CellDoc{}, err
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
