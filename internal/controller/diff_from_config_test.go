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

// Tests for DiffCellFromConfig, the on-demand drift diff behind the web UI's
// "Show diff" (issue #1768).

package controller_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// driftRunner is stubLineageRunner scoped to kuke-system/a/b, serving live
// from GetCell and failing any write: the diff must not persist anything.
func driftRunner(
	t *testing.T,
	live *intmodel.Cell,
	getConfig func(intmodel.CellConfig) (intmodel.CellConfig, error),
	getBlueprint func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error),
) *fakeRunner {
	t.Helper()
	mock := stubLineageRunner(getConfig, getBlueprint, func(intmodel.Cell) error {
		t.Error("DiffCellFromConfig wrote cell metadata")
		return nil
	})
	mock.GetCellFn = func(intmodel.Cell) (intmodel.Cell, error) {
		if live == nil {
			return intmodel.Cell{}, errdefs.ErrCellNotFound
		}
		return *live, nil
	}
	mock.PatchCellMetadataFn = func(intmodel.Cell, func(*intmodel.Cell) bool) error {
		t.Error("DiffCellFromConfig patched cell metadata")
		return nil
	}
	return mock
}

// driftRequest names a cell by its scope, as the web route does.
func driftRequest(live intmodel.Cell) intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{Name: live.Metadata.Name},
		Spec: intmodel.CellSpec{
			RealmName: live.Spec.RealmName, SpaceName: live.Spec.SpaceName, StackName: live.Spec.StackName,
		},
	}
}

func docYAML(t *testing.T, doc ext.CellDoc) string {
	t.Helper()
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	return string(raw)
}

func sampleConfigLookupFn(t *testing.T) func(intmodel.CellConfig) (intmodel.CellConfig, error) {
	return func(intmodel.CellConfig) (intmodel.CellConfig, error) { return configCarrier(t, sampleConfig()), nil }
}

func blueprintLookupFn(
	t *testing.T, doc ext.CellBlueprintDoc,
) func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
	return func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) { return blueprintCarrier(t, doc), nil }
}

// previewInScope renders the sample Config into kuke-system/a/b through the
// daemon render, as `kuke create cell --from-config` does, so the cell
// carries the render provenance annotations.
func previewInScope(t *testing.T, mock *fakeRunner) intmodel.Cell {
	t.Helper()
	preview, err := setupTestController(t, mock).PreviewCellFromConfig(controller.CellFromConfigRequest{
		Config:      intmodel.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Name:        "dev-ab",
		TargetSpace: "a",
		TargetStack: "b",
	})
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	cell, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	return cell
}

// inScope places a fixture cell in kuke-system/a/b: the diff route always
// names a full scope, and a re-render keeps the cell where it lives.
func inScope(cell intmodel.Cell) intmodel.Cell {
	cell.Spec.SpaceName, cell.Spec.StackName = "a", "b"
	return cell
}

// TestDiffCellFromConfig_InSyncDocumentsIdentical is AC1 and AC2 of #1768:
// over the in-sync fixtures of reconcile_outofsync_test.go, the call returns
// both documents, no reasons, and the two serialize to identical YAML.
func TestDiffCellFromConfig_InSyncDocumentsIdentical(t *testing.T) {
	const cniPath = "/opt/kukeon/data/default/default/network.conflist"
	cases := []struct {
		name  string
		build func(t *testing.T) (intmodel.Cell, ext.CellBlueprintDoc, func(*fakeRunner))
	}{
		{"matching cell", func(t *testing.T) (intmodel.Cell, ext.CellBlueprintDoc, func(*fakeRunner)) {
			return inScope(materializeSampleCell(t)), sampleReferencedBlueprint(), nil
		}},
		{"env override", func(t *testing.T) (intmodel.Cell, ext.CellBlueprintDoc, func(*fakeRunner)) {
			doc, err := cellconfig.MaterializeWithName(
				sampleConfig(),
				sampleAttachableBlueprint(),
				cellconfig.Prefix(sampleConfig()),
			)
			if err != nil {
				t.Fatalf("MaterializeWithName: %v", err)
			}
			live, err := apischeme.ConvertCellDocToInternal(doc)
			if err != nil {
				t.Fatalf("ConvertCellDocToInternal: %v", err)
			}
			for i := range live.Spec.Containers {
				if live.Spec.Containers[i].Attachable {
					live.Spec.Containers[i].Env = append(live.Spec.Containers[i].Env, "APP_ENV=prod")
				}
			}
			live.Spec.Provenance = &intmodel.CellProvenance{EnvOverrides: []string{"APP_ENV=prod"}}
			return inScope(live), sampleAttachableBlueprint(), nil
		}},
		{
			"runner-injected container fields",
			func(t *testing.T) (intmodel.Cell, ext.CellBlueprintDoc, func(*fakeRunner)) {
				live := inScope(materializeSampleCell(t))
				for i := range live.Spec.Containers {
					live.Spec.Containers[i].CNIConfigPath = cniPath
					live.Spec.Containers[i].User = "1000:1000"
					live.Spec.Containers[i].ContainerdID = "kuke-system_" + live.Spec.Containers[i].ID
				}
				live.Metadata.Labels["cell.kukeon.io"] = live.Metadata.Name
				return live, sampleReferencedBlueprint(), func(mock *fakeRunner) {
					mock.ResolveSpaceCNIConfigPathFn = func(string, string) (string, error) { return cniPath, nil }
					mock.GetSpaceFn = func(s intmodel.Space) (intmodel.Space, error) {
						s.Spec.Defaults = &intmodel.SpaceDefaults{
							Container: &intmodel.SpaceContainerDefaults{User: "1000:1000"},
						}
						return s, nil
					}
				}
			},
		},
		{"daemon render with provenance", func(t *testing.T) (intmodel.Cell, ext.CellBlueprintDoc, func(*fakeRunner)) {
			mock := driftRunner(t, nil, sampleConfigLookupFn(t), blueprintLookupFn(t, sampleReferencedBlueprint()))
			live := previewInScope(t, mock)
			live.Status = intmodel.CellStatus{State: intmodel.CellStateReady, OutOfSync: false}
			return live, sampleReferencedBlueprint(), nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live, bp, tweak := tc.build(t)
			mock := driftRunner(t, &live, sampleConfigLookupFn(t), blueprintLookupFn(t, bp))
			if tweak != nil {
				tweak(mock)
			}

			got, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
			if err != nil {
				t.Fatalf("DiffCellFromConfig: %v", err)
			}
			if !got.ConfigFound || got.Desired == nil {
				t.Fatalf("ConfigFound=%v Desired=%v, want both documents", got.ConfigFound, got.Desired)
			}
			if len(got.Reasons) != 0 || len(got.Paths) != 0 || got.ChangeType != controller.DriftChangeNone {
				t.Fatalf(
					"in-sync fixture reports reasons=%v paths=%v changeType=%q",
					got.Reasons,
					got.Paths,
					got.ChangeType,
				)
			}
			if liveYAML, desiredYAML := docYAML(t, got.Live), docYAML(t, *got.Desired); liveYAML != desiredYAML {
				t.Errorf("in-sync documents differ:\n%s\n---\n%s", liveYAML, desiredYAML)
			}
			if got.Live.Metadata.Name != live.Metadata.Name || len(got.Live.Spec.Containers) == 0 {
				t.Errorf("Live is not the stored cell: %+v", got.Live.Metadata)
			}
		})
	}
}

// blueprintWithWorkEnv is a blueprint "claude-host" with one container
// "work", whose env is the drifting field of the reported case.
func blueprintWithWorkEnv(env ...string) ext.CellBlueprintDoc {
	bp := sampleReferencedBlueprint()
	bp.Metadata.Name = "claude-host"
	bp.Spec.Cell.Containers[0].ID = "work"
	bp.Spec.Cell.Containers[0].Env = env
	return bp
}

func claudeHostConfig() ext.CellConfigDoc {
	cfg := sampleConfig()
	cfg.Spec.Blueprint.Name = "claude-host"
	return cfg
}

// TestDiffCellFromConfig_BlueprintChangeShowsAnnotationAndEnv is AC5 of
// #1768, the reported case: the blueprint's content changed in a way that
// alters containers[work].env. The diff names both reasons, and the two
// documents differ in the blueprint-revision annotation and the env line, and
// nowhere else that the drift check ignores.
func TestDiffCellFromConfig_BlueprintChangeShowsAnnotationAndEnv(t *testing.T) {
	configLookup := func(intmodel.CellConfig) (intmodel.CellConfig, error) {
		return configCarrier(t, claudeHostConfig()), nil
	}
	before := blueprintWithWorkEnv("MODE=one", "KEEP=same")
	after := blueprintWithWorkEnv("MODE=two", "KEEP=same")

	mock := driftRunner(t, nil, configLookup, blueprintLookupFn(t, before))
	preview, err := setupTestController(t, mock).PreviewCellFromConfig(controller.CellFromConfigRequest{
		Config: intmodel.CellConfigMetadata{Name: "kukeon-dev", Realm: "kuke-system"},
		Name:   "dev-ab", TargetSpace: "a", TargetStack: "b",
	})
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	liveRev := live.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
	if !strings.HasPrefix(liveRev, "claude-host@") {
		t.Fatalf("live blueprint revision = %q", liveRev)
	}
	live.Spec.Containers[0].ContainerdID = "kuke-system_work"

	mock = driftRunner(t, &live, configLookup, blueprintLookupFn(t, after))
	got, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
	if err != nil {
		t.Fatalf("DiffCellFromConfig: %v", err)
	}

	wantReasons := []string{"blueprint claude-host changed", "spec differs at containers[work].env"}
	if !slices.Equal(got.Reasons, wantReasons) {
		t.Errorf("Reasons = %q, want %q", got.Reasons, wantReasons)
	}
	if !slices.Equal(got.Paths, []string{"containers[work].env"}) {
		t.Errorf("Paths = %q", got.Paths)
	}
	if got.ChangeType != controller.DriftChangeCompatible {
		t.Errorf("ChangeType = %q, want compatible (env is applied in place)", got.ChangeType)
	}
	if got.Desired == nil {
		t.Fatal("Desired is nil")
	}
	desiredRev := got.Desired.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
	if got.Live.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision] != liveRev ||
		desiredRev == liveRev || !strings.HasPrefix(desiredRev, "claude-host@") {
		t.Errorf("blueprint revision live=%q desired=%q", liveRev, desiredRev)
	}
	if env := got.Live.Spec.Containers[0].Env; !slices.Contains(env, "MODE=one") {
		t.Errorf("live env = %v", env)
	}
	work := got.Desired.Spec.Containers[0]
	if !slices.Contains(work.Env, "MODE=two") || !slices.Contains(work.Env, "KEEP=same") {
		t.Errorf("desired env = %v", work.Env)
	}
	if work.ContainerdID != "kuke-system_work" {
		t.Errorf("desired containerdId = %q, want the live value", work.ContainerdID)
	}

	liveLines := strings.Split(docYAML(t, got.Live), "\n")
	desiredLines := strings.Split(docYAML(t, *got.Desired), "\n")
	var changed []string
	for _, line := range desiredLines {
		if !slices.Contains(liveLines, line) {
			changed = append(changed, strings.TrimSpace(line))
		}
	}
	if len(changed) != 2 ||
		!strings.HasPrefix(changed[0], cellconfig.AnnotationRenderBlueprintRevision+": claude-host@") ||
		changed[1] != "- MODE=two" {
		t.Errorf("changed lines = %q, want the blueprint revision and MODE=two", changed)
	}
}

// TestDiffCellFromConfig_EdgeStates is AC6 of #1768 at the daemon call: a
// cell not made from a Config, a deleted lineage Config, a render failure
// and a missing cell.
func TestDiffCellFromConfig_EdgeStates(t *testing.T) {
	t.Run("not from a config", func(t *testing.T) {
		live := buildTestCell("hand-built", "kuke-system", "a", "b")
		mock := driftRunner(t, &live, func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			t.Error("GetConfig called for a cell without a lineage label")
			return intmodel.CellConfig{}, nil
		}, nil)
		_, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
		if !errors.Is(err, errdefs.ErrCellNotFromConfig) {
			t.Errorf("err = %v, want ErrCellNotFromConfig", err)
		}
	})

	t.Run("config deleted", func(t *testing.T) {
		live := inScope(materializeSampleCell(t))
		mock := driftRunner(t, &live, func(intmodel.CellConfig) (intmodel.CellConfig, error) {
			return intmodel.CellConfig{}, errdefs.ErrConfigNotFound
		}, nil)
		got, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
		if err != nil {
			t.Fatalf("DiffCellFromConfig: %v", err)
		}
		if got.ConfigFound || got.Desired != nil || !slices.Equal(got.Reasons, []string{"lineage Config deleted"}) {
			t.Errorf("got ConfigFound=%v Desired=%v Reasons=%q", got.ConfigFound, got.Desired, got.Reasons)
		}
		if got.Live.Metadata.Name != live.Metadata.Name {
			t.Errorf("Live = %+v", got.Live.Metadata)
		}
	})

	t.Run("render failure", func(t *testing.T) {
		live := inScope(materializeSampleCell(t))
		mock := driftRunner(
			t,
			&live,
			sampleConfigLookupFn(t),
			func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
				return intmodel.CellBlueprint{}, errdefs.ErrBlueprintNotFound
			},
		)
		_, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
		if err == nil || !strings.Contains(err.Error(), "blueprint") {
			t.Errorf("err = %v, want the render error", err)
		}
	})

	t.Run("cell missing", func(t *testing.T) {
		mock := driftRunner(t, nil, sampleConfigLookupFn(t), nil)
		_, err := setupTestController(
			t,
			mock,
		).DiffCellFromConfig(driftRequest(buildTestCell("gone", "kuke-system", "a", "b")))
		if !errors.Is(err, errdefs.ErrCellNotFound) {
			t.Errorf("err = %v, want ErrCellNotFound", err)
		}
	})
}

// TestDiffCellFromConfig_StripsStatusAndInputs checks that the documents
// carry no status, runtime env or provenance, and that a breaking change is
// classified as a recreate.
func TestDiffCellFromConfig_StripsStatusAndInputs(t *testing.T) {
	live := inScope(materializeSampleCell(t))
	live.Spec.Containers[0].Image = "drifted-image:v2"
	live.Spec.RuntimeEnv = []string{"RUNTIME=secret"}
	live.Spec.Provenance = &intmodel.CellProvenance{Params: map[string]string{"p": "secret"}}
	live.Status = intmodel.CellStatus{State: intmodel.CellStateReady, OutOfSync: true, OutOfSyncReason: "spec differs"}
	live.Spec.NestedCgroupRuntime = true

	mock := driftRunner(t, &live, sampleConfigLookupFn(t), blueprintLookupFn(t, sampleReferencedBlueprint()))
	got, err := setupTestController(t, mock).DiffCellFromConfig(driftRequest(live))
	if err != nil {
		t.Fatalf("DiffCellFromConfig: %v", err)
	}
	for _, doc := range []ext.CellDoc{got.Live, *got.Desired} {
		if !reflect.DeepEqual(doc.Status, ext.CellStatus{}) || doc.Spec.RuntimeEnv != nil ||
			doc.Spec.Provenance != nil {
			t.Errorf("document keeps status or inputs: status=%+v runtimeEnv=%v provenance=%v",
				doc.Status, doc.Spec.RuntimeEnv, doc.Spec.Provenance)
		}
	}
	if got.ChangeType != controller.DriftChangeBreaking {
		t.Errorf("ChangeType = %q, want breaking for nestedCgroupRuntime", got.ChangeType)
	}
	if !slices.Contains(got.Paths, "spec.nestedCgroupRuntime") {
		t.Errorf("Paths = %q", got.Paths)
	}
	if got.Desired.Spec.Containers[0].Image != "img" || got.Live.Spec.Containers[0].Image != "drifted-image:v2" {
		t.Errorf("image live=%q desired=%q", got.Live.Spec.Containers[0].Image, got.Desired.Spec.Containers[0].Image)
	}
	if live.Status.OutOfSyncReason != "spec differs" || live.Spec.Provenance == nil {
		t.Error("DiffCellFromConfig modified the caller's cell")
	}
}
