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

package cgroupinspect_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/cgroupinspect"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func ptr(v int64) *int64 { return &v }

func sources(got []kukeonv1.CgroupDeclaredResource) map[string]string {
	out := map[string]string{}
	for _, d := range got {
		out[d.Field] = d.Source
	}
	return out
}

func TestDeclared(t *testing.T) {
	spaceDefault := &v1beta1.ContainerResources{MemoryLimitBytes: ptr(1 << 30)}
	cases := []struct {
		name      string
		container *v1beta1.ContainerResources
		def       *v1beta1.ContainerResources
		want      map[string]string
	}{
		{
			name: "nothing set",
			want: map[string]string{"memoryLimitBytes": "unset", "cpuShares": "unset", "pidsLimit": "unset"},
		},
		{
			name:      "inherited from space",
			container: &v1beta1.ContainerResources{MemoryLimitBytes: ptr(1 << 30)},
			def:       spaceDefault,
			want: map[string]string{
				"memoryLimitBytes": "space-default",
				"cpuShares":        "unset",
				"pidsLimit":        "unset",
			},
		},
		{
			name:      "container overrides space",
			container: &v1beta1.ContainerResources{PidsLimit: ptr(64)},
			def:       spaceDefault,
			want:      map[string]string{"memoryLimitBytes": "unset", "cpuShares": "unset", "pidsLimit": "container"},
		},
		{
			name:      "container without space default",
			container: &v1beta1.ContainerResources{CPUShares: ptr(512)},
			want:      map[string]string{"memoryLimitBytes": "unset", "cpuShares": "container", "pidsLimit": "unset"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sources(cgroupinspect.Declared(tc.container, tc.def))
			for field, want := range tc.want {
				if got[field] != want {
					t.Fatalf("%s: got %q want %q", field, got[field], want)
				}
			}
		})
	}
}
