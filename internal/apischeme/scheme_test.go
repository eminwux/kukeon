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
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestRealmRoundTripV1Beta1(t *testing.T) {
	input := ext.RealmDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindRealm,
		Metadata: ext.RealmMetadata{
			Name:   "realm0",
			Labels: map[string]string{"a": "b"},
		},
		Spec: ext.RealmSpec{
			Namespace: "realm0",
		},
		Status: ext.RealmStatus{
			State: ext.RealmStateCreating,
		},
	}

	internal, version, err := apischeme.NormalizeRealm(input)
	if err != nil {
		t.Fatalf("NormalizeRealm failed: %v", err)
	}
	if version != ext.APIVersionV1Beta1 {
		t.Fatalf("unexpected version: %s", version)
	}
	if internal.Metadata.Name != "realm0" || internal.Spec.Namespace != "realm0" {
		t.Fatalf("unexpected internal realm: %+v", internal)
	}

	// mutate internal to simulate controller updates
	internal.Status.State = intmodel.RealmStateReady

	output, err := apischeme.BuildRealmExternalFromInternal(internal, version)
	if err != nil {
		t.Fatalf("BuildRealmExternalFromInternal failed: %v", err)
	}
	if output.APIVersion != ext.APIVersionV1Beta1 || output.Kind != ext.KindRealm {
		t.Fatalf("unexpected output GVK: %s %s", output.APIVersion, output.Kind)
	}
	if output.Status.State != ext.RealmStateReady {
		t.Fatalf("unexpected output status: %+v", output.Status)
	}
}
