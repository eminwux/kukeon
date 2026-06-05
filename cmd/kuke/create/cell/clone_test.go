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

package cell_test

import (
	"encoding/json"
	"strings"
	"testing"

	cell "github.com/eminwux/kukeon/cmd/kuke/create/cell"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// sourceConfigCell is a Config-lineage source cell the fake daemon hands back
// for `--clone`: provenance points at the configDoc()/blueprintDoc() binding,
// carries the kukeon.io/config lineage label, and records a per-cell env
// override (P3) so byte-equality and env-stacking can be exercised.
func sourceConfigCell() v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   "prod-a1b2c3",
			Labels: map[string]string{cellconfig.LabelConfig: "prod"},
		},
		Spec: v1beta1.CellSpec{
			ID:      "prod-a1b2c3",
			RealmID: "cfg-realm",
			SpaceID: "cfg-space",
			StackID: "cfg-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "main", Image: "registry.example.com/web:stable", Env: []string{"FOO=stable"}, Attachable: true},
			},
			Provenance: &v1beta1.CellProvenance{
				BindingKind: v1beta1.BindingKindConfig,
				BindingRef: v1beta1.CellBindingRef{
					Name: "prod", Realm: "cfg-realm", Space: "cfg-space", Stack: "cfg-stack",
				},
				Params:       map[string]string{"TAG": "stable"},
				EnvOverrides: []string{"DEBUG=1"},
			},
		},
	}
}

// sourceBlueprintCell is a Blueprint-lineage source cell: provenance points at
// blueprintDoc(), carries the kukeon.io/blueprint lineage label, and records a
// resolved --param (P1) so param-stacking can be exercised.
func sourceBlueprintCell() v1beta1.CellDoc {
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   "web-x9y8z7",
			Labels: map[string]string{cellblueprint.LabelBlueprint: "web"},
		},
		Spec: v1beta1.CellSpec{
			ID:      "web-x9y8z7",
			RealmID: "bp-realm",
			SpaceID: "bp-space",
			StackID: "bp-stack",
			Containers: []v1beta1.ContainerSpec{
				{ID: "main", Image: "registry.example.com/web:custom", Attachable: true},
			},
			Provenance: &v1beta1.CellProvenance{
				BindingKind: v1beta1.BindingKindBlueprint,
				BindingRef: v1beta1.CellBindingRef{
					Name: "web", Realm: "bp-realm", Space: "bp-space", Stack: "bp-stack",
				},
				Params: map[string]string{"TAG": "custom"},
			},
		},
	}
}

// cloneFakeClient wires a fake daemon for the clone path: GetCell returns the
// source cell by name (and not-found for the would-be clone name, so the
// pre-persist collision check and any auto-name probe see a clean slate),
// GetConfig/GetBlueprint hand back the lineage binding, and MaterializeCell
// captures the final doc.
func cloneFakeClient(t *testing.T, src v1beta1.CellDoc, captured *v1beta1.CellDoc) *fakeClient {
	t.Helper()
	return &fakeClient{
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			if doc.Metadata.Name == src.Metadata.Name {
				return kukeonv1.GetCellResult{Cell: src, MetadataExists: true}, nil
			}
			return kukeonv1.GetCellResult{MetadataExists: false}, errdefs.ErrCellNotFound
		},
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		materializeCellFn: func(doc v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			*captured = doc
			return successResultFromDoc(doc), nil
		},
	}
}

func setSourceScope(t *testing.T, cmd *cobra.Command, src v1beta1.CellDoc) {
	t.Helper()
	setFlag(t, cmd, "realm", src.Spec.RealmID)
	setFlag(t, cmd, "space", src.Spec.SpaceID)
	setFlag(t, cmd, "stack", src.Spec.StackID)
}

// TestClone_FromConfigLineage_ExplicitName covers AC#1 (provenance byte-equal),
// AC#2 (explicit name verbatim), AC#3 (inherits kukeon.io/config lineage
// label), and AC#4 (kukeon.io/source-cell annotation).
func TestClone_FromConfigLineage_ExplicitName(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs([]string{"myclone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Metadata.Name != "myclone" {
		t.Errorf("clone name = %q, want %q (explicit verbatim)", got.Metadata.Name, "myclone")
	}
	if got.Spec.ID != "myclone" {
		t.Errorf("clone Spec.ID = %q, want %q", got.Spec.ID, "myclone")
	}
	// AC#1: provenance byte-equal to source's (the persisted/serialized form).
	assertProvenanceByteEqual(t, got.Spec.Provenance, src.Spec.Provenance)
	// AC#3: lineage label inherited (re-stamped by materialisation).
	if got.Metadata.Labels[cellconfig.LabelConfig] != "prod" {
		t.Errorf("clone missing kukeon.io/config=prod lineage label; labels=%v", got.Metadata.Labels)
	}
	// AC#4: source-cell annotation.
	if got.Metadata.Annotations[cell.AnnotationSourceCell] != src.Metadata.Name {
		t.Errorf("clone source-cell annotation = %q, want %q",
			got.Metadata.Annotations[cell.AnnotationSourceCell], src.Metadata.Name)
	}
	// Clone targets the source cell's scope.
	if got.Spec.RealmID != src.Spec.RealmID || got.Spec.SpaceID != src.Spec.SpaceID ||
		got.Spec.StackID != src.Spec.StackID {
		t.Errorf("clone scope = %s/%s/%s, want source scope %s/%s/%s",
			got.Spec.RealmID, got.Spec.SpaceID, got.Spec.StackID,
			src.Spec.RealmID, src.Spec.SpaceID, src.Spec.StackID)
	}
	// The source's env override is re-baked into the attachable container.
	if !containerEnvHas(got, "main", "DEBUG=1") {
		t.Errorf("clone container env missing source override DEBUG=1; got %v", got.Spec.Containers)
	}
}

// TestClone_AutoName covers AC#2: an omitted name yields <source-name>-<6hex>.
func TestClone_AutoName(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	prefix := src.Metadata.Name + "-"
	if !strings.HasPrefix(got.Metadata.Name, prefix) {
		t.Fatalf("auto-name %q does not start with %q", got.Metadata.Name, prefix)
	}
	suffix := strings.TrimPrefix(got.Metadata.Name, prefix)
	if len(suffix) != 6 {
		t.Errorf("auto-name suffix %q is %d chars, want 6 hex", suffix, len(suffix))
	}
	if got.Spec.ID != got.Metadata.Name {
		t.Errorf("Spec.ID %q != metadata.name %q", got.Spec.ID, got.Metadata.Name)
	}
}

// TestClone_FromBlueprintLineage covers the blueprint source kind: provenance
// byte-equal (AC#1), kukeon.io/blueprint lineage inherited (AC#3), source-cell
// annotation (AC#4).
func TestClone_FromBlueprintLineage(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceBlueprintCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs([]string{"bpclone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertProvenanceByteEqual(t, got.Spec.Provenance, src.Spec.Provenance)
	if got.Metadata.Labels[cellblueprint.LabelBlueprint] != "web" {
		t.Errorf("clone missing kukeon.io/blueprint=web lineage label; labels=%v", got.Metadata.Labels)
	}
	if got.Metadata.Annotations[cell.AnnotationSourceCell] != src.Metadata.Name {
		t.Errorf("clone source-cell annotation = %q, want %q",
			got.Metadata.Annotations[cell.AnnotationSourceCell], src.Metadata.Name)
	}
}

// TestClone_EnvStacking covers AC#6 on the config path: additional --env stacks
// on top of the source's recorded overrides (last-write-wins).
func TestClone_EnvStacking(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "env", "DEBUG=2") // overrides source's DEBUG=1
	setFlag(t, cmd, "env", "EXTRA=x") // new key stacks on top
	cmd.SetArgs([]string{"stacked"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Spec.Provenance == nil {
		t.Fatal("clone provenance is nil")
	}
	if !slicesContain(got.Spec.Provenance.EnvOverrides, "DEBUG=2") ||
		!slicesContain(got.Spec.Provenance.EnvOverrides, "EXTRA=x") {
		t.Errorf("provenance EnvOverrides = %v, want DEBUG=2 + EXTRA=x", got.Spec.Provenance.EnvOverrides)
	}
	if slicesContain(got.Spec.Provenance.EnvOverrides, "DEBUG=1") {
		t.Errorf("provenance EnvOverrides still carries overridden DEBUG=1: %v", got.Spec.Provenance.EnvOverrides)
	}
	if !containerEnvHas(got, "main", "DEBUG=2") {
		t.Errorf("clone container env missing stacked DEBUG=2; got %v", got.Spec.Containers)
	}
}

// TestClone_ParamStacking covers AC#6 on the blueprint path: additional --param
// stacks on top of the source's recorded params (last-write-wins).
func TestClone_ParamStacking(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceBlueprintCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "param", "TAG=override")
	cmd.SetArgs([]string{"pclone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Spec.Provenance == nil || got.Spec.Provenance.Params["TAG"] != "override" {
		t.Errorf("provenance Params = %v, want TAG=override", got.Spec.Provenance.Params)
	}
	// The stacked param flows into the resolved image.
	if !containerImageHas(got, "main", "override") {
		t.Errorf("clone container image did not pick up TAG=override; got %v", got.Spec.Containers)
	}
}

func TestClone_RejectsParamOnConfigLineage(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "param", "TAG=x")
	cmd.SetArgs([]string{"badclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--param is not valid when cloning a Config-lineage cell") {
		t.Fatalf("err = %v, want --param rejection on config-lineage clone", err)
	}
}

func TestClone_RejectsEnvOnBlueprintLineage(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceBlueprintCell()
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "env", "DEBUG=1")
	cmd.SetArgs([]string{"badclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--env is not valid when cloning a Blueprint-lineage cell") {
		t.Fatalf("err = %v, want --env rejection on blueprint-lineage clone", err)
	}
}

func TestClone_SourceNotFound(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{MetadataExists: false}, errdefs.ErrCellNotFound
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "clone", "ghost")
	cmd.SetArgs([]string{"newclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "source cell") {
		t.Fatalf("err = %v, want source-cell-not-found", err)
	}
}

func TestClone_SourceNoProvenance(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	src.Spec.Provenance = nil // hand-built cell, never materialised from a binding
	var got v1beta1.CellDoc
	fc := cloneFakeClient(t, src, &got)
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs([]string{"newclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "no materialization provenance") {
		t.Fatalf("err = %v, want no-provenance rejection", err)
	}
}

// TestClone_ExplicitNameCollisionErrors covers AC#2: an explicit name that
// already exists in scope errors (not an idempotent attach).
func TestClone_ExplicitNameCollisionErrors(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceConfigCell()
	fc := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			// Both the source lookup and the clone-name collision probe hit.
			return kukeonv1.GetCellResult{Cell: src, MetadataExists: true}, nil
		},
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		materializeCellFn: func(v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			t.Fatal("MaterializeCell must not be called on a name collision")
			return kukeonv1.CreateCellResult{}, nil
		},
	}
	cmd, _ := newTestExecCmd(t, fc)
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs([]string{"taken"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already-exists collision error", err)
	}
}

// TestClone_MutuallyExclusive covers AC#5: --clone with another source flag
// errors at parse time (cobra mutex).
func TestClone_MutuallyExclusive(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := &fakeClient{}
	cmd, _ := newTestExecCmd(t, fc)
	setFlag(t, cmd, "clone", "src")
	setFlag(t, cmd, "from-config", "prod")
	cmd.SetArgs([]string{"newclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "if any flags in the group") {
		t.Fatalf("err = %v, want cobra mutual-exclusivity error", err)
	}
}

// assertProvenanceByteEqual compares the serialized (persisted) form of two
// provenance blocks — the sense in which AC#1 means "byte-equal". JSON marshal
// folds the cosmetic nil-vs-empty-slice difference CloneCellProvenance
// introduces (both omitempty away), which reflect.DeepEqual would flag despite
// identical on-disk metadata.json.
func assertProvenanceByteEqual(t *testing.T, got, want *v1beta1.CellProvenance) {
	t.Helper()
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal clone provenance: %v", err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal source provenance: %v", err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("clone provenance not byte-equal to source's:\n got=%s\nwant=%s", gotJSON, wantJSON)
	}
}

func containerEnvHas(doc v1beta1.CellDoc, containerID, entry string) bool {
	for _, c := range doc.Spec.Containers {
		if c.ID == containerID {
			return slicesContain(c.Env, entry)
		}
	}
	return false
}

func containerImageHas(doc v1beta1.CellDoc, containerID, substr string) bool {
	for _, c := range doc.Spec.Containers {
		if c.ID == containerID {
			return strings.Contains(c.Image, substr)
		}
	}
	return false
}

func slicesContain(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
