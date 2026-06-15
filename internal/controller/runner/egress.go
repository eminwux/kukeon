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

	"github.com/eminwux/kukeon/internal/cni"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/netpolicy"
	"github.com/eminwux/kukeon/internal/util/naming"
)

// applySpaceEgressPolicy realizes space.Spec.Network.Egress on the host
// firewall as the space's own admission+egress chain.
//
// Since #1076 admission is per-space: the host-global `-i k-+ ACCEPT` blanket
// is gone, so every space's bridge needs its own chain to ACCEPT egress on a
// FORWARD-DROP host. A space with no explicit policy (or a default=allow policy
// with an empty allowlist, which FromInternal collapses to nil) therefore still
// gets an admit-all chain rather than a no-op — admission and the per-space
// KUKEON-EGRESS chain are installed as one unit so they can't drift, and a
// Default=deny space whose chain is missing fails closed (no path to ACCEPT)
// instead of leaking egress through a blanket rule.
func (r *Exec) applySpaceEgressPolicy(space intmodel.Space) error {
	networkName, err := naming.BuildSpaceNetworkName(space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	bridge := cni.SafeBridgeName(networkName)

	var egress *intmodel.EgressPolicy
	if space.Spec.Network != nil {
		egress = space.Spec.Network.Egress
	}
	policy, err := netpolicy.FromInternal(
		space.Spec.RealmName, space.Metadata.Name, bridge, egress,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	if policy == nil {
		// No restriction requested: install a per-space admit-all chain so the
		// bridge keeps full egress now that the host-global blanket is gone.
		policy = netpolicy.NewAdmitAllPolicy(space.Spec.RealmName, space.Metadata.Name, bridge)
	}
	if err = policy.Resolve(r.ctx, nil); err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	return r.netPolicyEnforcer().Apply(r.ctx, policy)
}

// removeSpaceEgressPolicy tears down any policy previously installed for
// this space. Idempotent; safe to call on spaces that never had a policy.
func (r *Exec) removeSpaceEgressPolicy(space intmodel.Space) error {
	return r.netPolicyEnforcer().Remove(r.ctx, space.Spec.RealmName, space.Metadata.Name)
}
