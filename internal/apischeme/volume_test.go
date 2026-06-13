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

package apischeme_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestVolumeReclaimPolicyRoundTrip confirms reclaimPolicy survives the external
// → internal → external conversion in both directions (#1237). The empty
// (omitted) policy stays empty so existing specs are unaffected.
func TestVolumeReclaimPolicyRoundTrip(t *testing.T) {
	for _, policy := range []ext.ReclaimPolicy{"", ext.ReclaimRetain, ext.ReclaimDelete} {
		in := ext.VolumeDoc{
			APIVersion: ext.APIVersionV1Beta1,
			Kind:       ext.KindVolume,
			Metadata:   ext.VolumeMetadata{Name: "data", Realm: "r1"},
			Spec:       ext.VolumeSpec{ReclaimPolicy: policy},
		}
		internal, err := apischeme.ConvertVolumeDocToInternal(in)
		if err != nil {
			t.Fatalf("ConvertVolumeDocToInternal(%q) error = %v", policy, err)
		}
		if got := internal.Spec.ReclaimPolicy; string(got) != string(policy) {
			t.Errorf("internal reclaimPolicy = %q, want %q", got, policy)
		}
		out := apischeme.ConvertVolumeToExternal(internal)
		if got := out.Spec.ReclaimPolicy; got != policy {
			t.Errorf("round-tripped reclaimPolicy = %q, want %q", got, policy)
		}
	}
}
