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

package ctr_test

import (
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestConvertRealmCredentials(t *testing.T) {
	tests := []struct {
		name  string
		input []intmodel.RegistryCredentials
		want  []ctr.RegistryCredentials
	}{
		{
			name:  "empty slice",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty slice literal",
			input: []intmodel.RegistryCredentials{},
			want:  nil,
		},
		{
			name: "single credential",
			input: []intmodel.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "docker.io",
				},
			},
			want: []ctr.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "docker.io",
				},
			},
		},
		{
			name: "multiple credentials",
			input: []intmodel.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "docker.io",
				},
				{
					Username:      "user2",
					Password:      "pass2",
					ServerAddress: "registry.example.com",
				},
			},
			want: []ctr.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "docker.io",
				},
				{
					Username:      "user2",
					Password:      "pass2",
					ServerAddress: "registry.example.com",
				},
			},
		},
		{
			name: "credential with empty server address",
			input: []intmodel.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "",
				},
			},
			want: []ctr.RegistryCredentials{
				{
					Username:      "user1",
					Password:      "pass1",
					ServerAddress: "",
				},
			},
		},
		{
			name: "credentials with empty username/password",
			input: []intmodel.RegistryCredentials{
				{
					Username:      "",
					Password:      "",
					ServerAddress: "registry.example.com",
				},
			},
			want: []ctr.RegistryCredentials{
				{
					Username:      "",
					Password:      "",
					ServerAddress: "registry.example.com",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ctr.ConvertRealmCredentials(tt.input)

			if tt.want == nil {
				if got != nil {
					t.Errorf("ConvertRealmCredentials() = %v, want nil", got)
				}
				return
			}

			if got == nil {
				t.Fatal("ConvertRealmCredentials() = nil, want non-nil")
			}

			if len(got) != len(tt.want) {
				t.Fatalf("ConvertRealmCredentials() length = %d, want %d", len(got), len(tt.want))
			}

			for i := range got {
				if got[i].Username != tt.want[i].Username {
					t.Errorf(
						"ConvertRealmCredentials()[%d].Username = %q, want %q",
						i,
						got[i].Username,
						tt.want[i].Username,
					)
				}
				if got[i].Password != tt.want[i].Password {
					t.Errorf(
						"ConvertRealmCredentials()[%d].Password = %q, want %q",
						i,
						got[i].Password,
						tt.want[i].Password,
					)
				}
				if got[i].ServerAddress != tt.want[i].ServerAddress {
					t.Errorf(
						"ConvertRealmCredentials()[%d].ServerAddress = %q, want %q",
						i,
						got[i].ServerAddress,
						tt.want[i].ServerAddress,
					)
				}
			}
		})
	}
}
