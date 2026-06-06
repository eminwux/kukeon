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

package run

import (
	"encoding/json"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// TestDivergedFields_MaterializePersistMaterializeIsIdempotent guards the
// invariant that `kuke restart` writes a spec to disk that — when
// re-materialised by the next `kuke run --from-config <cfg>` — compares clean against
// the persisted version (issue #984). The flow being asserted:
//
//	Materialize(cfg, bp) → ApplyDocuments(persist) → GetCell → Materialize(cfg, bp)
//	→ divergedFields(persisted.Spec, freshly-materialised.Spec) == nil
//
// The daemon-side ApplyDocuments + GetCell pair is simulated here via the
// apischeme + JSON pipeline plus the mutations UpdateCell applies (parent
// refs, ContainerdID, Space-defaults merge). A real daemon test would need a
// containerd socket and a temp filesystem; the simulation captures the
// normalisations divergedContainerFields is sensitive to (the Space-defaults
// fields are deliberately excluded from that comparator, but the simulation
// applies them anyway to verify the exclusion stays correct under realistic
// inputs).
//
// If this test trips, divergedFields and the persistence pipeline have
// drifted apart. Tighten either side per the issue's "Suggested fix" — make
// Materialize deterministic, or grow divergedFields' exclusion set to match
// a new normalisation introduced on the persist side.
func TestDivergedFields_MaterializePersistMaterializeIsIdempotent(t *testing.T) {
	cases := []struct {
		name string
		bp   v1beta1.CellBlueprintDoc
		cfg  v1beta1.CellConfigDoc
	}{
		{
			name: "minimal one-container",
			bp:   minimalBlueprintForDivergence(),
			cfg:  minimalConfigForDivergence(),
		},
		{
			name: "rich container fields (env, ports, volumes, networks, git, repos, secrets)",
			bp:   richBlueprintForDivergence(),
			cfg:  richConfigForDivergence(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Step 1: Materialize what `kuke restart` would push through
			// ApplyDocuments.
			cell1, err := cellconfig.MaterializeWithName(tc.cfg, tc.bp, cellconfig.Prefix(tc.cfg))
			if err != nil {
				t.Fatalf("Materialize (restart side): %v", err)
			}

			// Step 2a: Mirror restart's `yaml.Marshal(&cellDoc)` + the
			// daemon-side `yaml.Unmarshal(raw, &cellDoc)` parser step. A
			// nil/empty slice round-trips through YAML differently than
			// through direct in-memory copy, and the bug hypothesis cares
			// about the disk-side normalisation specifically.
			yamlBytes, err := yaml.Marshal(&cell1)
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}
			var parsed v1beta1.CellDoc
			if unErr := yaml.Unmarshal(yamlBytes, &parsed); unErr != nil {
				t.Fatalf("yaml.Unmarshal: %v", unErr)
			}

			// Step 2b: Simulate the daemon's ApplyDocuments → UpdateCell →
			// UpdateCellMetadata write path:
			//   external CellDoc → intmodel.Cell → daemon mutations →
			//   JSON marshal (what UpdateCellMetadata writes to disk).
			int1, err := apischeme.ConvertCellDocToInternal(parsed)
			if err != nil {
				t.Fatalf("ConvertCellDocToInternal: %v", err)
			}
			applyDaemonSideMutations(&int1)
			ext1, err := apischeme.BuildCellExternalFromInternal(int1, apischeme.VersionV1Beta1)
			if err != nil {
				t.Fatalf("BuildCellExternalFromInternal: %v", err)
			}
			diskBytes, err := json.Marshal(ext1)
			if err != nil {
				t.Fatalf("json.Marshal (disk write): %v", err)
			}

			// Step 3: Simulate GetCell → readCellInternal → BuildExternal on
			// the way back out to the RPC caller.
			var diskDoc v1beta1.CellDoc
			if unErr := json.Unmarshal(diskBytes, &diskDoc); unErr != nil {
				t.Fatalf("json.Unmarshal (disk read): %v", unErr)
			}
			int2, err := apischeme.ConvertCellDocToInternal(diskDoc)
			if err != nil {
				t.Fatalf("ConvertCellDocToInternal (readback): %v", err)
			}
			persisted, err := apischeme.BuildCellExternalFromInternal(int2, apischeme.VersionV1Beta1)
			if err != nil {
				t.Fatalf("BuildCellExternalFromInternal (readback): %v", err)
			}

			// Step 4: Re-Materialize as the next `kuke run --from-config <cfg>` would.
			cell2, err := cellconfig.MaterializeWithName(tc.cfg, tc.bp, cellconfig.Prefix(tc.cfg))
			if err != nil {
				t.Fatalf("Materialize (run side): %v", err)
			}

			// Step 5: Assert.
			if diffs := divergedFields(persisted.Spec, cell2.Spec); len(diffs) > 0 {
				t.Errorf(
					"divergedFields after restart→run round-trip = %v want empty; "+
						"the persistence pipeline normalised a field divergedFields still compares "+
						"(see issue #984 — either tighten Materialize determinism or extend the "+
						"divergedContainerFields exclusion list to match the new normalisation)",
					diffs,
				)
			}
		})
	}
}

// applyDaemonSideMutations mirrors the in-place mutations UpdateCell +
// ensureCellContainers apply to a freshly-normalised internal Cell before it
// hits UpdateCellMetadata: per-container parent refs, ContainerdID, and the
// Space-defaults merge. Space defaults here use a sentinel non-zero User so
// the test exercises the divergedContainerFields exclusion rather than a
// no-op merge.
func applyDaemonSideMutations(cell *intmodel.Cell) {
	for i := range cell.Spec.Containers {
		c := &cell.Spec.Containers[i]
		if c.RealmName == "" {
			c.RealmName = cell.Spec.RealmName
		}
		if c.SpaceName == "" {
			c.SpaceName = cell.Spec.SpaceName
		}
		if c.StackName == "" {
			c.StackName = cell.Spec.StackName
		}
		if c.CellName == "" {
			c.CellName = cell.Spec.ID
			if c.CellName == "" {
				c.CellName = cell.Metadata.Name
			}
		}
		// ContainerdID is filled by ensureCellContainers when the container
		// is created. A real daemon writes a stack-cell-id-derived value; the
		// exact shape is irrelevant — the assertion is that divergedFields
		// ignores it.
		if c.ContainerdID == "" {
			c.ContainerdID = "ctr-" + c.ID
		}
		// Simulate the Space-defaults merge with a sentinel non-zero User so
		// the test fails loudly if divergedContainerFields ever starts
		// comparing the Space-defaults envelope.
		intmodel.ApplySpaceDefaultsToContainer(
			intmodel.Space{
				Spec: intmodel.SpaceSpec{
					Defaults: &intmodel.SpaceDefaults{
						Container: &intmodel.SpaceContainerDefaults{
							User: "spaceuser",
						},
					},
				},
			},
			c,
		)
	}
}

func minimalBlueprintForDivergence() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "web", Realm: "bp-realm"},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "root", Root: true, Image: "registry.example.com/root:latest"},
					{ID: "main", Image: "registry.example.com/web:${TAG}", Attachable: true},
				},
			},
		},
	}
}

func minimalConfigForDivergence() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
		},
	}
}

func richBlueprintForDivergence() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "web", Realm: "bp-realm"},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellBlueprintParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "root", Root: true, Image: "registry.example.com/root:latest"},
					{
						ID:              "main",
						Image:           "registry.example.com/web:${TAG}",
						Attachable:      true,
						Args:            []string{"--port", "8080"},
						Env:             []string{"FOO=bar", "BAZ=qux"},
						Ports:           []string{"8080/tcp"},
						Volumes:         []v1beta1.VolumeMount{{Source: "/host/data", Target: "/data", ReadOnly: true, Mode: 0o644}},
						Networks:        []string{"default"},
						NetworksAliases: []string{"web"},
						RestartPolicy:   "on-failure",
						Repos:           []v1beta1.ContainerRepo{{Name: "src", Target: "/srv", Required: true}},
						Secrets: []v1beta1.BlueprintSecretSlot{
							{Name: "token", Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "TOKEN", Required: true},
						},
						Git: &v1beta1.ContainerGit{
							Author: &v1beta1.GitIdentity{Name: "kuke", Email: "kuke@example.com"},
						},
						Tty: &v1beta1.ContainerTty{Prompt: "kuke> "},
					},
				},
			},
		},
	}
}

func richConfigForDivergence() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata:   v1beta1.CellConfigMetadata{Name: "prod", Realm: "cfg-realm"},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
			Repos: map[string]v1beta1.CellConfigRepoFill{
				"src": {URL: "https://example.com/src.git"},
			},
			Secrets: map[string]v1beta1.CellConfigSecretFill{
				"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "api-token", Realm: "cfg-realm"}},
			},
		},
	}
}
