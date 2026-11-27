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

package apply_test

import (
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func egressSpace(egress *intmodel.EgressPolicy) intmodel.Space {
	s := intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: "spb"},
		Spec:     intmodel.SpaceSpec{RealmName: "default"},
	}
	if egress != nil {
		s.Spec.Network = &intmodel.SpaceNetwork{Egress: egress}
	}
	return s
}

func denyWith(rules ...intmodel.EgressAllowRule) *intmodel.EgressPolicy {
	return &intmodel.EgressPolicy{Default: intmodel.EgressDefaultDeny, Allow: rules}
}

// TestDiffSpace_Egress pins #1703: DiffSpace compares spec.network.egress, so
// an apply that changes only the egress policy of an existing space is a
// compatible update instead of "unchanged". Documents that mean the same
// policy (reordered rules or ports, nil vs. empty vs. default=allow without
// rules) compare equal.
func TestDiffSpace_Egress(t *testing.T) {
	cidrA := intmodel.EgressAllowRule{CIDR: "10.89.2.0/24"}
	cidrB := intmodel.EgressAllowRule{CIDR: "10.89.3.0/24", Ports: []int{443, 80}}
	host := intmodel.EgressAllowRule{Host: "example.com", Ports: []int{443}}

	tests := []struct {
		name        string
		desired     *intmodel.EgressPolicy
		actual      *intmodel.EgressPolicy
		wantChanged bool
	}{
		{name: "added rule", desired: denyWith(cidrA), actual: denyWith(), wantChanged: true},
		{name: "removed rule", desired: denyWith(cidrA), actual: denyWith(cidrA, cidrB), wantChanged: true},
		{
			name:    "allow to deny",
			desired: denyWith(cidrA),
			actual: &intmodel.EgressPolicy{
				Default: intmodel.EgressDefaultAllow,
				Allow:   []intmodel.EgressAllowRule{cidrA},
			},
			wantChanged: true,
		},
		{
			name:        "changed ports on a rule",
			desired:     denyWith(intmodel.EgressAllowRule{Host: "example.com", Ports: []int{443, 8443}}),
			actual:      denyWith(host),
			wantChanged: true,
		},
		{
			name:        "cidr replaced by host",
			desired:     denyWith(intmodel.EgressAllowRule{Host: "10.89.2.0/24"}),
			actual:      denyWith(cidrA),
			wantChanged: true,
		},
		{name: "policy omitted on a restricted space", desired: nil, actual: denyWith(), wantChanged: true},
		{name: "policy added to an unconstrained space", desired: denyWith(), actual: nil, wantChanged: true},
		{name: "no change", desired: denyWith(cidrA, host), actual: denyWith(cidrA, host)},
		{name: "reordered allow", desired: denyWith(host, cidrB, cidrA), actual: denyWith(cidrA, host, cidrB)},
		{
			name:    "reordered ports",
			desired: denyWith(intmodel.EgressAllowRule{CIDR: "10.89.3.0/24", Ports: []int{80, 443}}),
			actual:  denyWith(cidrB),
		},
		{
			name:    "default case and whitespace",
			desired: &intmodel.EgressPolicy{Default: " Deny ", Allow: []intmodel.EgressAllowRule{cidrA}},
			actual:  denyWith(cidrA),
		},
		{
			name:    "rule whitespace",
			desired: denyWith(intmodel.EgressAllowRule{CIDR: " 10.89.2.0/24 "}),
			actual:  denyWith(cidrA),
		},
		{name: "nil vs empty default", desired: nil, actual: &intmodel.EgressPolicy{}},
		{
			name:    "nil vs default allow without rules",
			desired: &intmodel.EgressPolicy{Default: intmodel.EgressDefaultAllow},
			actual:  nil,
		},
		{
			name:    "empty default vs allow",
			desired: &intmodel.EgressPolicy{Allow: []intmodel.EgressAllowRule{cidrA}},
			actual: &intmodel.EgressPolicy{
				Default: intmodel.EgressDefaultAllow,
				Allow:   []intmodel.EgressAllowRule{cidrA},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diff := apply.DiffSpace(egressSpace(tc.desired), egressSpace(tc.actual))
			changed := slices.Contains(diff.ChangedFields, "spec.network.egress")
			if changed != tc.wantChanged || diff.HasChanges != tc.wantChanged {
				t.Fatalf("DiffSpace = %+v, want spec.network.egress changed=%v", diff, tc.wantChanged)
			}
			if tc.wantChanged && diff.ChangeType != apply.ChangeTypeCompatible {
				t.Errorf("ChangeType = %v, want compatible", diff.ChangeType)
			}
		})
	}
}

// TestDiffSpace_EgressNetworkWithoutEgress: a network block with no egress
// policy equals an absent network block.
func TestDiffSpace_EgressNetworkWithoutEgress(t *testing.T) {
	withBlock := egressSpace(nil)
	withBlock.Spec.Network = &intmodel.SpaceNetwork{}
	if d := apply.DiffSpace(withBlock, egressSpace(nil)); d.HasChanges {
		t.Errorf("DiffSpace = %+v, want no change", d)
	}
}

// TestDiffSpace_EgressDoesNotMutateInputs: normalizing for the comparison
// works on copies, so the caller's rule and port order survive.
func TestDiffSpace_EgressDoesNotMutateInputs(t *testing.T) {
	desired := denyWith(
		intmodel.EgressAllowRule{CIDR: "10.89.3.0/24", Ports: []int{443, 80}},
		intmodel.EgressAllowRule{CIDR: "10.89.2.0/24"},
	)
	apply.DiffSpace(egressSpace(desired), egressSpace(denyWith()))
	if desired.Allow[0].CIDR != "10.89.3.0/24" || !slices.Equal(desired.Allow[0].Ports, []int{443, 80}) {
		t.Errorf("desired policy was reordered: %+v", desired.Allow)
	}
}

// TestReconcileSpace_EgressOnlyChange: an apply whose only change is the
// egress policy reports updated and stores the new policy; the same document
// again reports unchanged and writes nothing.
func TestReconcileSpace_EgressOnlyChange(t *testing.T) {
	f := newScopeStoreRunner()
	f.space.Spec.Network = &intmodel.SpaceNetwork{Egress: denyWith()}

	desired := f.space
	desired.Spec.Network = &intmodel.SpaceNetwork{
		Egress: denyWith(intmodel.EgressAllowRule{CIDR: "10.89.2.0/24"}),
	}
	res, err := apply.ReconcileSpace(f, desired)
	wantAction(t, "egress change", res, err, "updated")
	if !slices.Contains(res.Changes, "spec.network.egress") {
		t.Errorf("changes = %v, want spec.network.egress", res.Changes)
	}
	if n := f.space.Spec.Network; n == nil || n.Egress == nil || len(n.Egress.Allow) != 1 ||
		n.Egress.Allow[0].CIDR != "10.89.2.0/24" {
		t.Fatalf("stored network = %+v, want the new allow rule", n)
	}

	res, err = apply.ReconcileSpace(f, desired)
	wantAction(t, "re-apply", res, err, "unchanged")
	if f.updates != 1 {
		t.Errorf("re-apply wrote again: updates = %d, want 1", f.updates)
	}
}
