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

package controller

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/apply"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"gopkg.in/yaml.v3"
)

// overlayBaseCell is a stored cell with the runner stamps DiffCell ignores
// (managed labels, containerdId, a synthesized root), so the tests can check
// they survive the overlay.
func overlayBaseCell() intmodel.Cell {
	return intmodel.Cell{
		Metadata: intmodel.CellMetadata{
			Name: "dev",
			Labels: map[string]string{
				"cell.kukeon.io":       "dev",
				cellconfig.LabelConfig: "dev-config",
				"team":                 "a",
			},
			Annotations: map[string]string{
				cellconfig.AnnotationRenderDigest: "sha256:live",
			},
		},
		Spec: intmodel.CellSpec{
			RealmName: "default",
			SpaceName: "default",
			StackName: "default",
			Containers: []intmodel.ContainerSpec{
				{ID: "root", Root: true, Image: "pause", ContainerdID: "default_root"},
				{ID: "work", Image: "img:1", Env: []string{"A=1"}, ContainerdID: "default_work"},
			},
		},
	}
}

// cloneOverlayCell deep-copies a cell through YAML, enough for the tests to
// compare a cell before and after an overlay.
func cloneOverlayCell(t *testing.T, cell intmodel.Cell) intmodel.Cell {
	t.Helper()
	out := cell
	out.Metadata.Labels = make(map[string]string)
	for k, v := range cell.Metadata.Labels {
		out.Metadata.Labels[k] = v
	}
	out.Metadata.Annotations = make(map[string]string)
	for k, v := range cell.Metadata.Annotations {
		out.Metadata.Annotations[k] = v
	}
	out.Spec.Containers = nil
	for _, c := range cell.Spec.Containers {
		cp := reflect.New(reflect.TypeFor[intmodel.ContainerSpec]()).Elem()
		deepCopyValue(cp, reflect.ValueOf(c))
		out.Spec.Containers = append(out.Spec.Containers, cp.Interface().(intmodel.ContainerSpec))
	}
	return out
}

// deepCopyValue copies src into dst with fresh slices, maps and pointers.
func deepCopyValue(dst, src reflect.Value) {
	switch src.Kind() { //nolint:exhaustive // every other kind is copied by value
	case reflect.Pointer:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.New(src.Elem().Type()))
		deepCopyValue(dst.Elem(), src.Elem())
	case reflect.Slice:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.MakeSlice(src.Type(), src.Len(), src.Len()))
		for i := range src.Len() {
			deepCopyValue(dst.Index(i), src.Index(i))
		}
	case reflect.Map:
		if src.IsNil() {
			return
		}
		dst.Set(reflect.MakeMap(src.Type()))
		for _, k := range src.MapKeys() {
			v := reflect.New(src.Type().Elem()).Elem()
			deepCopyValue(v, src.MapIndex(k))
			dst.SetMapIndex(k, v)
		}
	case reflect.Struct:
		for i := range src.NumField() {
			if dst.Field(i).CanSet() {
				deepCopyValue(dst.Field(i), src.Field(i))
			}
		}
	default:
		dst.Set(src)
	}
}

// nonZeroValue builds a non-zero value of type t: every string, number and
// bool set, every slice and map with one element, every pointer allocated.
func nonZeroValue(t reflect.Type, depth int) reflect.Value {
	v := reflect.New(t).Elem()
	if depth > 6 {
		return v
	}
	switch t.Kind() { //nolint:exhaustive // ContainerSpec holds no other kind
	case reflect.String:
		v.SetString("x-" + strings.ToLower(t.Name()))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(7)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(7)
	case reflect.Pointer:
		v.Set(reflect.New(t.Elem()))
		v.Elem().Set(nonZeroValue(t.Elem(), depth+1))
	case reflect.Slice:
		v.Set(reflect.Append(reflect.MakeSlice(t, 0, 1), nonZeroValue(t.Elem(), depth+1)))
	case reflect.Map:
		v.Set(reflect.MakeMap(t))
		v.SetMapIndex(nonZeroValue(t.Key(), depth+1), nonZeroValue(t.Elem(), depth+1))
	case reflect.Struct:
		for i := range t.NumField() {
			if t.Field(i).IsExported() {
				v.Field(i).Set(nonZeroValue(t.Field(i).Type, depth+1))
			}
		}
	default:
	}
	return v
}

// assertOverlayCloses checks the overlay's contract on one desired/live pair:
// every name DiffCell reports is one the overlay knows, live is not modified,
// and DiffCell finds nothing left between the render and the overlaid cell.
// It returns the overlaid cell and the reported names.
func assertOverlayCloses(t *testing.T, live, desired intmodel.Cell) (intmodel.Cell, []string) {
	t.Helper()
	before := cloneOverlayCell(t, live)
	diff := apply.DiffCell(desired, live)
	var names []string
	for _, field := range slices.Concat(diff.BreakingChanges, diff.ChangedFields) {
		names = append(names, field)
		probe := cloneOverlayCell(t, live)
		if !overlayCellField(&probe, &desired, field) {
			t.Errorf("DiffCell reports cell field %q, which overlayDrift does not handle", field)
		}
		if name, ok := strings.CutPrefix(field, rootContainerFieldPrefix); ok {
			if _, known := containerFieldOverlay[name]; !known {
				t.Errorf("DiffCell reports root container field %q with no containerFieldOverlay entry", name)
			}
		}
	}
	for _, cd := range diff.Containers {
		for _, field := range slices.Concat(cd.BreakingChanges, cd.ChangedFields) {
			names = append(names, field)
			if _, known := containerFieldOverlay[field]; !known {
				t.Errorf("DiffCell reports container field %q with no containerFieldOverlay entry", field)
			}
		}
	}

	shown := overlayDrift(live, desired, diff, nil)
	if !reflect.DeepEqual(before, cloneOverlayCell(t, live)) {
		t.Errorf("overlayDrift modified the live cell")
	}
	if rest := apply.DiffCell(desired, shown); rest.HasChanges {
		t.Errorf("after the overlay DiffCell still reports %v %v %+v",
			rest.BreakingChanges, rest.ChangedFields, rest.Containers)
	}
	return shown, names
}

func overlayYAML(t *testing.T, cell intmodel.Cell) string {
	t.Helper()
	doc, err := driftDoc(cell)
	if err != nil {
		t.Fatalf("driftDoc: %v", err)
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	return string(raw)
}

// TestOverlayDrift_CoversEveryContainerField is the AC3 guard of #1768: for
// every ContainerSpec field, on a child and on the root container, a render
// that differs from the stored cell in that one field yields DiffCell names
// the overlay handles, and the overlaid cell then matches the render. A field
// DiffCell starts comparing without an overlay entry fails here.
func TestOverlayDrift_CoversEveryContainerField(t *testing.T) {
	specType := reflect.TypeFor[intmodel.ContainerSpec]()
	full := nonZeroValue(specType, 0).Interface().(intmodel.ContainerSpec)
	reported := map[string]bool{}

	for _, rootContainer := range []bool{false, true} {
		for i := range specType.NumField() {
			field := specType.Field(i)
			if field.Name == "ID" || field.Name == "Root" {
				continue // identity: pairs the containers, not a compared field
			}
			live := overlayBaseCell()
			idx := 1
			if rootContainer {
				idx = 0
			}
			live.Spec.Containers[idx] = intmodel.ContainerSpec{
				ID: live.Spec.Containers[idx].ID, Root: rootContainer,
			}
			desired := cloneOverlayCell(t, live)
			reflect.ValueOf(&desired.Spec.Containers[idx]).Elem().FieldByName(field.Name).
				Set(reflect.ValueOf(full).FieldByName(field.Name))

			shown, names := assertOverlayCloses(t, live, desired)
			for _, name := range names {
				reported[strings.TrimPrefix(name, rootContainerFieldPrefix)] = true
			}
			if len(names) > 0 && overlayYAML(t, shown) == overlayYAML(t, live) {
				t.Errorf(
					"%s (root=%v): DiffCell reports %v but the YAML does not change",
					field.Name,
					rootContainer,
					names,
				)
			}
		}
	}

	for name := range containerFieldOverlay {
		if !reported[name] {
			t.Errorf("containerFieldOverlay has %q, which DiffCell never reported", name)
		}
	}
}

// TestOverlayDrift_CellLevelFields covers every cell-level name DiffCell
// reports, a container added and a container removed (AC3 of #1768), and that
// the runner stamps DiffCell ignores keep their live value.
func TestOverlayDrift_CellLevelFields(t *testing.T) {
	cases := []struct {
		name   string
		want   string
		mutate func(desired *intmodel.Cell)
	}{
		{"name", "metadata.name", func(d *intmodel.Cell) { d.Metadata.Name = "other" }},
		{"realm", "spec.realmName", func(d *intmodel.Cell) { d.Spec.RealmName = "other" }},
		{"space", "spec.spaceName", func(d *intmodel.Cell) { d.Spec.SpaceName = "other" }},
		{"stack", "spec.stackName", func(d *intmodel.Cell) { d.Spec.StackName = "other" }},
		{"labels", "metadata.labels", func(d *intmodel.Cell) {
			delete(d.Metadata.Labels, "cell.kukeon.io")
			d.Metadata.Labels["team"] = "b"
		}},
		{"tty", "spec.tty", func(d *intmodel.Cell) { d.Spec.Tty = &intmodel.CellTty{Default: "work"} }},
		{"autoDelete", "spec.autoDelete", func(d *intmodel.Cell) { d.Spec.AutoDelete = true }},
		{
			"nestedCgroupRuntime",
			"spec.nestedCgroupRuntime",
			func(d *intmodel.Cell) { d.Spec.NestedCgroupRuntime = true },
		},
		{"container added", "", func(d *intmodel.Cell) {
			d.Spec.Containers = append(d.Spec.Containers, intmodel.ContainerSpec{ID: "side", Image: "side:1"})
		}},
		{"container removed", "", func(d *intmodel.Cell) { d.Spec.Containers = d.Spec.Containers[:1] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := overlayBaseCell()
			desired := cloneOverlayCell(t, live)
			tc.mutate(&desired)

			diff := apply.DiffCell(desired, live)
			if !diff.HasChanges {
				t.Fatal("fixture does not differ")
			}
			if tc.want != "" && !slices.Contains(slices.Concat(diff.BreakingChanges, diff.ChangedFields), tc.want) {
				t.Fatalf("DiffCell reports %v %v, want %q", diff.BreakingChanges, diff.ChangedFields, tc.want)
			}
			shown, _ := assertOverlayCloses(t, live, desired)
			if overlayYAML(t, shown) == overlayYAML(t, live) {
				t.Error("the overlaid YAML equals the live YAML")
			}
			if shown.Metadata.Labels["cell.kukeon.io"] != "dev" {
				t.Errorf("managed label lost: %v", shown.Metadata.Labels)
			}
			if work := childContainerSpec(&shown, "work"); work != nil && work.ContainerdID != "default_work" {
				t.Errorf("containerdId = %q, want the live value", work.ContainerdID)
			}
		})
	}
}

// TestOverlayDrift_RootContainerAdded covers DiffCell's spec.rootContainer: a
// render with a root container the stored cell lacks.
func TestOverlayDrift_RootContainerAdded(t *testing.T) {
	live := overlayBaseCell()
	live.Spec.Containers = live.Spec.Containers[1:]
	desired := cloneOverlayCell(t, overlayBaseCell())

	shown, names := assertOverlayCloses(t, live, desired)
	if !slices.Contains(names, "spec.rootContainer") {
		t.Fatalf("names = %v, want spec.rootContainer", names)
	}
	if root := rootContainerSpec(&shown); root == nil || root.ID != "root" {
		t.Errorf("root container not inserted: %+v", shown.Spec.Containers)
	}
}

// TestOverlayDrift_ProvenanceAnnotations covers every reason
// renderProvenanceDriftKeys gives (AC3 of #1768): each copies its annotation
// from the render, after which nothing drifts.
func TestOverlayDrift_ProvenanceAnnotations(t *testing.T) {
	cases := []struct {
		key, live, desired, reason string
	}{
		{
			cellconfig.AnnotationRenderBlueprintRevision,
			"claude-host@aaa",
			"claude-host@bbb",
			"blueprint claude-host changed",
		},
		{cellconfig.AnnotationRenderConfigRevision, "sha256:old", "sha256:new", "config changed"},
		{cellconfig.AnnotationRenderValuesHash, "sha256:old", "sha256:new", "values changed"},
		{
			cellconfig.AnnotationRenderRealmDefaults,
			"generation=1 hash=a",
			"generation=2 hash=b",
			"realm defaults changed",
		},
		{
			cellconfig.AnnotationRenderSpaceDefaults,
			"generation=1 hash=a",
			"generation=2 hash=b",
			"space defaults changed",
		},
		{
			cellconfig.AnnotationRenderStackDefaults,
			"generation=1 hash=a",
			"generation=2 hash=b",
			"stack defaults changed",
		},
		{
			cellconfig.AnnotationRenderImageDigests,
			"base=sha256:a,tool=sha256:c",
			"base=sha256:b,tool=sha256:c",
			"image base digest changed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.reason, func(t *testing.T) {
			live := overlayBaseCell()
			live.Metadata.Annotations[tc.key] = tc.live
			desired := cloneOverlayCell(t, live)
			desired.Metadata.Annotations[tc.key] = tc.desired
			desired.Metadata.Annotations[cellconfig.AnnotationRenderDigest] = "sha256:desired"

			drift := renderProvenanceDriftKeys(desired, live, "")
			if len(drift) != 1 || drift[0].Key != tc.key || drift[0].Reason != tc.reason {
				t.Fatalf("drift = %+v, want one %q for %s", drift, tc.reason, tc.key)
			}
			if got := renderProvenanceDrift(desired, live, ""); !slices.Equal(got, []string{tc.reason}) {
				t.Errorf("renderProvenanceDrift = %v, want the keyed reasons", got)
			}

			shown := overlayDrift(live, desired, apply.DiffCell(desired, live), drift)
			if got := shown.Metadata.Annotations[tc.key]; got != tc.desired {
				t.Errorf("annotation %s = %q, want %q", tc.key, got, tc.desired)
			}
			if live.Metadata.Annotations[tc.key] != tc.live {
				t.Error("overlayDrift modified the live annotations")
			}
			if rest := renderProvenanceDriftKeys(desired, shown, ""); len(rest) != 0 {
				t.Errorf("drift left after the overlay: %+v", rest)
			}
			if got := shown.Metadata.Annotations[cellconfig.AnnotationRenderDigest]; got != "sha256:live" {
				t.Errorf("an annotation that did not drift changed: render-digest = %q", got)
			}
		})
	}
}

// TestOverlayDrift_NoChangeIsLive is AC2 of #1768 at the overlay level: with
// no spec diff and no drift, the overlaid cell is the live cell, even when the
// render differs in fields DiffCell ignores.
func TestOverlayDrift_NoChangeIsLive(t *testing.T) {
	live := overlayBaseCell()
	desired := cloneOverlayCell(t, live)
	delete(desired.Metadata.Labels, "cell.kukeon.io")
	desired.Spec.Containers = desired.Spec.Containers[1:]
	desired.Spec.Containers[0].ContainerdID = ""
	desired.Metadata.Annotations[cellconfig.AnnotationRenderDigest] = "sha256:other"

	diff := apply.DiffCell(desired, live)
	if diff.HasChanges {
		t.Fatalf("fixture differs: %v %v", diff.BreakingChanges, diff.ChangedFields)
	}
	shown := overlayDrift(live, desired, diff, nil)
	if overlayYAML(t, shown) != overlayYAML(t, live) {
		t.Errorf("in-sync overlay differs:\n%s\n---\n%s", overlayYAML(t, shown), overlayYAML(t, live))
	}
}
