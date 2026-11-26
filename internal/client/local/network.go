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

package local

import (
	"context"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// defaultRootContainerID is the cell-spec ID the runner gives an
// auto-generated root container (ctr.DefaultRootContainerSpec).
const defaultRootContainerID = "root"

// GetNetworkDetail implements kukeonv1.Client (issue #1411). It combines the
// scoped resource's declared networking with the runtime facts the runner
// reads: the space's conflist and allocator state, and the cell root
// container's task state and cached CNI IPv4.
func (c *Client) GetNetworkDetail(
	ctx context.Context, scope kukeonv1.NetworkScope,
) (kukeonv1.GetNetworkDetailResult, error) {
	res := kukeonv1.GetNetworkDetailResult{Scope: scope}
	switch scope.Kind {
	case kukeonv1.NetworkKindSpace, kukeonv1.NetworkKindCell, kukeonv1.NetworkKindContainer:
	default:
		return res, fmt.Errorf("%w: %q", errdefs.ErrNetworkScopeKind, scope.Kind)
	}
	cgScope := kukeonv1.CgroupScope{Realm: scope.Realm, Space: scope.Space, Stack: scope.Stack, Cell: scope.Cell}
	space, err := c.cgroupSpace(ctx, cgScope)
	if err != nil {
		return res, err
	}
	internalSpace, _, err := apischeme.NormalizeSpace(space)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	info, err := c.ctrl.InspectSpaceNetwork(internalSpace)
	if err != nil {
		return res, err
	}
	networkName, err := naming.BuildSpaceNetworkName(scope.Realm, scope.Space)
	if err != nil {
		return res, err
	}
	res.Space = spaceNetworkDetail(networkName, space.Spec, info)
	if scope.Kind == kukeonv1.NetworkKindSpace {
		return res, nil
	}

	if scope.Kind == kukeonv1.NetworkKindContainer && strings.TrimSpace(scope.Container) == "" {
		return res, errdefs.ErrContainerNameRequired
	}
	cell, err := c.cgroupCell(ctx, cgScope)
	if err != nil {
		return res, err
	}
	var container *v1beta1.ContainerSpec
	if scope.Kind == kukeonv1.NetworkKindContainer {
		for i := range cell.Spec.Containers {
			if cell.Spec.Containers[i].ID == scope.Container {
				container = &cell.Spec.Containers[i]
				break
			}
		}
		if container == nil {
			return res, errdefs.ErrContainerNotFound
		}
	}
	internalCell, _, err := apischeme.NormalizeCell(cell)
	if err != nil {
		return res, fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
	}
	hostNetwork := cellHostNetwork(cell.Spec.Containers)
	running, ipv4, err := c.ctrl.InspectCellNetwork(internalCell, hostNetwork)
	if err != nil {
		return res, err
	}
	cellDetail := cellNetworkDetail(cell, hostNetwork, running, ipv4)
	res.Cell = &cellDetail
	if container != nil {
		detail := containerNetworkDetail(*container, cellDetail.RootContainer, res.Space.ConfigPath)
		res.Container = &detail
	}
	return res, nil
}

// wireSubnetSource maps the cni package's subnet source onto the published
// kukeonv1 values, so the wire contract does not depend on internal strings.
func wireSubnetSource(source string) string {
	switch source {
	case cni.SubnetSourceAllocator:
		return kukeonv1.NetworkSubnetAllocator
	case cni.SubnetSourceConflist:
		return kukeonv1.NetworkSubnetConflist
	default:
		return kukeonv1.NetworkSubnetUnallocated
	}
}

func spaceNetworkDetail(
	networkName string, spec v1beta1.SpaceSpec, info cni.SpaceNetworkInfo,
) kukeonv1.SpaceNetworkDetail {
	detail := kukeonv1.SpaceNetworkDetail{
		NetworkName:      networkName,
		BridgeName:       info.BridgeName,
		SubnetCIDR:       info.SubnetCIDR,
		SubnetSource:     wireSubnetSource(info.SubnetSource),
		ParentCIDR:       info.ParentCIDR,
		ConfigPath:       info.ConfigPath,
		ConfigPathSource: kukeonv1.NetworkSourceSpec,
		CNINetworkExists: info.Exists,
		Egress: kukeonv1.NetworkEgress{
			Default:       string(v1beta1.EgressDefaultAllow),
			DefaultSource: kukeonv1.NetworkSourceDefault,
		},
	}
	// The daemon stamps its default conflist path into spec.cniConfigPath, so
	// a spec value equal to the default is still the default.
	declared := strings.TrimSpace(spec.CNIConfigPath)
	if declared == "" || declared == info.DefaultConfigPath {
		detail.ConfigPathSource = kukeonv1.NetworkSourceDefault
	}
	if spec.Network == nil || spec.Network.Egress == nil {
		return detail
	}
	egress := spec.Network.Egress
	// An empty default is allow (netpolicy.FromInternal).
	if egress.Default != "" {
		detail.Egress.Default = string(egress.Default)
		detail.Egress.DefaultSource = kukeonv1.NetworkSourceSpec
	}
	for _, rule := range egress.Allow {
		detail.Egress.Allow = append(detail.Egress.Allow, kukeonv1.NetworkEgressRule{
			Host:  rule.Host,
			CIDR:  rule.CIDR,
			Ports: append([]int(nil), rule.Ports...),
		})
	}
	return detail
}

// cellHostNetwork mirrors the runner's cellWantsHostNetworkRoot: one
// container with hostNetwork puts the root container, and so the whole cell,
// on the host netns.
func cellHostNetwork(containers []v1beta1.ContainerSpec) bool {
	for _, c := range containers {
		if c.HostNetwork {
			return true
		}
	}
	return false
}

func cellNetworkDetail(
	cell v1beta1.CellDoc, hostNetwork, running bool, ipv4 string,
) kukeonv1.CellNetworkDetail {
	detail := kukeonv1.CellNetworkDetail{
		BridgeName:    cell.Status.Network.BridgeName,
		HostNetwork:   hostNetwork,
		RootContainer: rootContainerID(cell.Spec),
	}
	switch {
	case !running:
		detail.IPv4State = kukeonv1.NetworkIPv4NotRunning
	case hostNetwork:
		detail.IPv4State = kukeonv1.NetworkIPv4HostNetwork
	case ipv4 == "":
		detail.IPv4State = kukeonv1.NetworkIPv4NotAssigned
	default:
		detail.IPv4State = kukeonv1.NetworkIPv4Assigned
		detail.IPv4 = ipv4
	}
	for _, c := range cell.Spec.Containers {
		detail.Containers = append(detail.Containers, kukeonv1.ContainerNetworkSummary{
			ID:          c.ID,
			Root:        c.ID == detail.RootContainer,
			HostNetwork: c.HostNetwork,
			HostPID:     c.HostPID,
		})
	}
	return detail
}

func rootContainerID(spec v1beta1.CellSpec) string {
	for _, c := range spec.Containers {
		if c.Root {
			return c.ID
		}
	}
	if id := strings.TrimSpace(spec.RootContainerID); id != "" {
		return id
	}
	return defaultRootContainerID
}

func containerNetworkDetail(
	c v1beta1.ContainerSpec, rootContainer, spaceConfigPath string,
) kukeonv1.ContainerNetworkDetail {
	detail := kukeonv1.ContainerNetworkDetail{
		ID:                  c.ID,
		Root:                c.ID == rootContainer,
		HostNetwork:         c.HostNetwork,
		HostPID:             c.HostPID,
		CNIConfigPath:       spaceConfigPath,
		CNIConfigPathSource: kukeonv1.NetworkSourceSpace,
	}
	// The runner fills an empty container cniConfigPath with the space's, so
	// a value equal to the space path is still inherited.
	if declared := strings.TrimSpace(c.CNIConfigPath); declared != "" && declared != spaceConfigPath {
		detail.CNIConfigPath = declared
		detail.CNIConfigPathSource = kukeonv1.NetworkSourceContainer
	}
	return detail
}
