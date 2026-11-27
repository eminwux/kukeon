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

package cellblueprint_test

import (
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// hostMountContainer carries the three team host-mount selectors as the
// team blueprint renderer writes them, plus the in-cell KUKEON_HOST.
func hostMountContainer() v1beta1.ContainerSpec {
	return v1beta1.ContainerSpec{
		ID: "work",
		Volumes: []v1beta1.VolumeMount{
			{Source: "${KUKEOND_SOCKET_DIR}/kukeond", Target: "/run/kukeon/kukeond", ReadOnly: true},
			{Source: "${KUKEON_RUN_PATH}/bin/client", Target: "/.kukeon/client", ReadOnly: true},
			{Source: "${KUKEON_RUN_PATH}/bin/kuke-shim", Target: "/usr/local/bin/kuke", ReadOnly: true},
			{Source: "${KUKEON_RUN_PATH}", Target: "/opt/kukeon", ReadOnly: true},
			{Kind: v1beta1.VolumeKindVolume, Source: "state-${KUKEON_RUN_PATH}", Target: "/state"},
		},
		Env: []string{"KUKEON_HOST=unix:///run/kukeon/kukeond/${KUKEOND_SOCKET_NAME}", "A=b"},
	}
}

func sources(c v1beta1.ContainerSpec) []string {
	out := make([]string, 0, len(c.Volumes))
	for _, v := range c.Volumes {
		out = append(out, v.Source)
	}
	return out
}

// TestExpandHostPaths pins #1710: the host-mount sources and KUKEON_HOST
// follow the rendering daemon's run path and socket, the targets stay the
// standard layout, and the standard layout renders the literal paths team
// blueprints carried before. Only bind sources are rewritten.
func TestExpandHostPaths(t *testing.T) {
	tests := []struct {
		name        string
		paths       cellblueprint.HostPaths
		wantSources []string
		wantHost    string
	}{
		{
			name:  "standard layout",
			paths: cellblueprint.HostPaths{},
			wantSources: []string{
				"/run/kukeon/kukeond", "/opt/kukeon/bin/client", "/opt/kukeon/bin/kuke-shim", "/opt/kukeon",
				"state-${KUKEON_RUN_PATH}",
			},
			wantHost: "KUKEON_HOST=unix:///run/kukeon/kukeond/kukeond.sock",
		},
		{
			name:  "standard paths spelled out",
			paths: cellblueprint.HostPaths{RunPath: "/opt/kukeon", Socket: "/run/kukeon/kukeond.sock"},
			wantSources: []string{
				"/run/kukeon/kukeond", "/opt/kukeon/bin/client", "/opt/kukeon/bin/kuke-shim", "/opt/kukeon",
				"state-${KUKEON_RUN_PATH}",
			},
			wantHost: "KUKEON_HOST=unix:///run/kukeon/kukeond/kukeond.sock",
		},
		{
			name:  "kuke init --run-path X",
			paths: cellblueprint.HostPaths{RunPath: "/tmp/ke-1/", Socket: "/tmp/ke-1/kukeond.sock"},
			wantSources: []string{
				"/tmp/ke-1/kukeond", "/tmp/ke-1/bin/client", "/tmp/ke-1/bin/kuke-shim", "/tmp/ke-1",
				"state-${KUKEON_RUN_PATH}",
			},
			wantHost: "KUKEON_HOST=unix:///run/kukeon/kukeond/kukeond.sock",
		},
		{
			name:  "nested dev daemon socket",
			paths: cellblueprint.HostPaths{RunPath: "/opt/kukeon", Socket: "/run/kukeon-dev/dev.sock"},
			wantSources: []string{
				"/run/kukeon-dev/kukeond", "/opt/kukeon/bin/client", "/opt/kukeon/bin/kuke-shim", "/opt/kukeon",
				"state-${KUKEON_RUN_PATH}",
			},
			wantHost: "KUKEON_HOST=unix:///run/kukeon/kukeond/dev.sock",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			template := hostMountContainer()
			containers := []v1beta1.ContainerSpec{template}
			cellblueprint.ExpandHostPaths(tc.paths, containers)
			got := containers[0]
			if !slices.Equal(sources(got), tc.wantSources) {
				t.Errorf("sources = %q, want %q", sources(got), tc.wantSources)
			}
			for i, v := range got.Volumes {
				if v.Target != template.Volumes[i].Target {
					t.Errorf("target %q changed to %q", template.Volumes[i].Target, v.Target)
				}
			}
			if !slices.Equal(got.Env, []string{tc.wantHost, "A=b"}) {
				t.Errorf("env = %q, want %q", got.Env, tc.wantHost)
			}
			// The template's slices are not aliased.
			if template.Volumes[1].Source != "${KUKEON_RUN_PATH}/bin/client" ||
				template.Env[0] != "KUKEON_HOST=unix:///run/kukeon/kukeond/${KUKEOND_SOCKET_NAME}" {
				t.Errorf("template mutated: %+v", template)
			}
		})
	}
}

// TestMaterializeWithName_InlineBlueprintStandardLayout: `kuke run -b` is
// materialized by the client, which does not know the daemon's paths, so it
// keeps the standard layout instead of a relative, silently skipped source.
func TestMaterializeWithName_InlineBlueprintStandardLayout(t *testing.T) {
	doc := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "bp"},
		Spec: v1beta1.CellBlueprintSpec{
			Prefix: "bp",
			Cell: v1beta1.BlueprintCellSpec{Containers: []v1beta1.BlueprintContainer{{
				ID: "work", Image: "busybox",
				Volumes: []v1beta1.VolumeMount{{Source: "${KUKEON_RUN_PATH}/bin/client", Target: "/.kukeon/client"}},
			}}},
		},
	}
	cell, err := cellblueprint.MaterializeWithName(doc, "c1", nil)
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got := cell.Spec.Containers[0].Volumes[0].Source; got != "/opt/kukeon/bin/client" {
		t.Errorf("source = %q, want /opt/kukeon/bin/client", got)
	}
}
