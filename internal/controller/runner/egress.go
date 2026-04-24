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
// firewall. It is a no-op when the space has no egress policy or when the
// policy effectively matches current behavior (default=allow with no
// allowlist entries).
func (r *Exec) applySpaceEgressPolicy(space intmodel.Space) error {
	if space.Spec.Network == nil || space.Spec.Network.Egress == nil {
		return nil
	}
	networkName, err := naming.BuildSpaceNetworkName(space.Spec.RealmName, space.Metadata.Name)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	bridge := cni.SafeBridgeName(networkName)

	policy, err := netpolicy.FromInternal(
		space.Spec.RealmName, space.Metadata.Name, bridge, space.Spec.Network.Egress,
	)
	if err != nil {
		return fmt.Errorf("%w: %w", errdefs.ErrEgressApply, err)
	}
	if policy == nil {
		return nil
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
