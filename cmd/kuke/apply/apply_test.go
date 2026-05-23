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
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	apply "github.com/eminwux/kukeon/cmd/kuke/apply"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func TestApplyRunE(t *testing.T) {
	const validYAML = `apiVersion: v1beta1
kind: Realm
metadata:
  name: r1
spec:
  namespace: r1
`

	tests := []struct {
		name       string
		args       []string
		fake       *fakeClient
		wantErr    string
		wantOutput string
	}{
		{
			name:    "no source flag",
			args:    []string{},
			fake:    &fakeClient{},
			wantErr: "at least one of the flags in the group [file blueprint config] is required",
		},
		{
			name: "success",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Realm", Name: "r1", Action: "created"},
						},
					}, nil
				},
			},
			wantOutput: `Realm "r1": created`,
		},
		{
			name: "client returns error",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{}, errors.New("server exploded")
				},
			},
			wantErr: "server exploded",
		},
		{
			name: "failure recorded as failed action",
			args: []string{"-f", writeTempYAML(t, validYAML)},
			fake: &fakeClient{
				applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Realm", Name: "r1", Action: "failed", Error: "boom"},
						},
					}, nil
				},
			},
			wantErr: "some resources failed to apply",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := apply.NewApplyCmd()
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(tt.fake))
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantOutput != "" && !strings.Contains(buf.String(), tt.wantOutput) {
				t.Errorf("output missing %q\nGot:\n%s", tt.wantOutput, buf.String())
			}
		})
	}
}

// TestNewApplyCmd_RunE_JSONOutput locks the lowercase shape of
// `kuke apply -f -o json` so the keys can't drift back to Go's default
// uppercase marshaling. Mirrors TestNewDeleteCmd_RunE_JSONOutput on the
// sibling delete command.
func TestNewApplyCmd_RunE_JSONOutput(t *testing.T) {
	const validYAML = `apiVersion: v1beta1
kind: Realm
metadata:
  name: r1
spec:
  namespace: r1
`

	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

	fc := &fakeClient{
		applyFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Index: 0, Kind: "Realm", Name: "r1", Action: "created"},
				},
			}, nil
		},
	}
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)

	cmd.SetArgs([]string{"-f", writeTempYAML(t, validYAML), "--output", "json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	for _, want := range []string{`"index"`, `"kind"`, `"name"`, `"action"`, `"resources"`, "Realm", "r1", "created"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected JSON output to contain %q, got: %q", want, output)
		}
	}
}

// blueprintDoc builds a minimal Blueprint with one param + one user (non-root)
// container so apply -b has something to materialise. The fixture mirrors
// run_test.blueprintDoc's shape so the round-trip behaviour stays comparable.
func blueprintDoc() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "my-realm",
			Space: "my-space",
			Stack: "my-stack",
		},
		Spec: v1beta1.CellBlueprintSpec{
			Prefix:     "web",
			Parameters: []v1beta1.CellProfileParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "main", Image: "registry.example.com/web:${TAG}", Attachable: true},
				},
			},
		},
	}
}

// configDoc + configBlueprintDoc let -c tests round-trip through Materialize.
// Slot fills are minimal (no secrets/repos) so Materialize succeeds without
// auxiliary scope reads.
func configBlueprintDoc() v1beta1.CellBlueprintDoc {
	def := "latest"
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  "web",
			Realm: "bp-realm",
		},
		Spec: v1beta1.CellBlueprintSpec{
			Parameters: []v1beta1.CellProfileParameter{{Name: "TAG", Default: &def}},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "main", Image: "registry.example.com/web:${TAG}", Attachable: true},
				},
			},
		},
	}
}

func configDoc() v1beta1.CellConfigDoc {
	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  "prod",
			Realm: "cfg-realm",
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{Name: "web", Realm: "bp-realm"},
			Values:    map[string]string{"TAG": "v2"},
		},
	}
}

// runApplyRef is the test helper that builds a fake-backed apply.NewApplyCmd
// and executes it with the given args. It returns the captured stdout/stderr
// buffer plus the cobra Execute error.
func runApplyRef(t *testing.T, fc *fakeClient, args []string) (string, error) {
	t.Helper()
	cmd := apply.NewApplyCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestApply_FromBlueprint_MissingCell_CreatesViaApplyDocuments covers the
// "missing cell" leg of the -b reconciliation matrix: GetCell returns
// ErrCellNotFound so the lineage check skips; ApplyDocuments fires with the
// materialised YAML and the daemon reports "created".
func TestApply_FromBlueprint_MissingCell_CreatesViaApplyDocuments(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			if doc.Metadata.Name != "web" {
				t.Errorf("lookup name=%q want web", doc.Metadata.Name)
			}
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
		},
		applyFn: func(raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
			if !strings.Contains(string(raw), "kukeon.io/blueprint: web") {
				t.Errorf("materialised YAML missing lineage label; raw=\n%s", string(raw))
			}
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "Cell", Name: "myCell", Action: "created"},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-b", "web", "--name", "myCell", "--realm", "my-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `Cell "myCell": created`) {
		t.Errorf("missing created line in output:\n%s", out)
	}
	if fc.applyCalls != 1 {
		t.Errorf("ApplyDocuments calls=%d want 1", fc.applyCalls)
	}
}

// TestApply_FromBlueprint_LiveCellMatchingLineage_NoOpsViaUnchanged covers the
// "exists, matches" leg: GetCell echoes the same cell back with the matching
// blueprint label, ApplyDocuments delegates to the daemon's diff which returns
// "unchanged". The lineage check passes; no refusal.
func TestApply_FromBlueprint_LiveCellMatchingLineage_NoOpsViaUnchanged(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   doc.Metadata.Name,
						Labels: map[string]string{cellblueprint.LabelBlueprint: "web"},
					},
				},
				MetadataExists: true,
			}, nil
		},
		applyFn: func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "Cell", Name: "myCell", Action: "unchanged"},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-b", "web", "--name", "myCell", "--realm", "my-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `Cell "myCell": unchanged`) {
		t.Errorf("missing unchanged line in output:\n%s", out)
	}
}

// TestApply_FromBlueprint_LiveCellDivergentSpec_UpdatesViaApplyDocuments covers
// the "exists, differs" leg: the materialised YAML round-trips into the
// daemon-side reconciler, which reports "updated" with the changed fields. The
// CLI surfaces the per-field summary line.
func TestApply_FromBlueprint_LiveCellDivergentSpec_UpdatesViaApplyDocuments(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   doc.Metadata.Name,
						Labels: map[string]string{cellblueprint.LabelBlueprint: "web"},
					},
					Spec: v1beta1.CellSpec{
						Containers: []v1beta1.ContainerSpec{
							{ID: "main", Image: "registry.example.com/web:old"},
						},
					},
				},
				MetadataExists: true,
			}, nil
		},
		applyFn: func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{
						Kind:    "Cell",
						Name:    "myCell",
						Action:  "updated",
						Changes: []string{`container "main" updated: image`},
					},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-b", "web", "--name", "myCell", "--realm", "my-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, `Cell "myCell": updated`) {
		t.Errorf("missing updated header in output:\n%s", out)
	}
	if !strings.Contains(out, `container "main" updated: image`) {
		t.Errorf("missing per-field diff line in output:\n%s", out)
	}
}

// TestApply_FromBlueprint_LiveCellHandBuilt_RefusesLineageMismatch is the
// safety gate the AC names: a hand-built cell (no kukeon.io/blueprint label) is
// refused, never silently overwritten. The error names the existing cell's
// source ("no lineage label") and the source apply tried to claim.
func TestApply_FromBlueprint_LiveCellHandBuilt_RefusesLineageMismatch(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{Name: doc.Metadata.Name},
				},
				MetadataExists: true,
			}, nil
		},
	}

	_, err := runApplyRef(t, fc, []string{"-b", "web", "--name", "alice-handbuilt", "--realm", "my-realm"})
	if err == nil {
		t.Fatal("expected lineage-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "no lineage label") {
		t.Errorf("err=%q want mention of 'no lineage label'", err.Error())
	}
	if !strings.Contains(err.Error(), "kukeon.io/blueprint=web") {
		t.Errorf("err=%q want mention of attempted lineage", err.Error())
	}
	if fc.applyCalls != 0 {
		t.Errorf("ApplyDocuments fired %d times on lineage mismatch; want 0", fc.applyCalls)
	}
}

// TestApply_FromBlueprint_CrossLineage_RefusesAgainstConfigCell ensures the
// lineage check is strict in both directions: applying -b against a cell whose
// lineage is a sibling CellConfig also refuses.
func TestApply_FromBlueprint_CrossLineage_RefusesAgainstConfigCell(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   doc.Metadata.Name,
						Labels: map[string]string{cellconfig.LabelConfig: "prod"},
					},
				},
				MetadataExists: true,
			}, nil
		},
	}

	_, err := runApplyRef(t, fc, []string{"-b", "web", "--name", "prod", "--realm", "my-realm"})
	if err == nil {
		t.Fatal("expected lineage-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "kukeon.io/config=prod") {
		t.Errorf("err=%q want existing-lineage detail", err.Error())
	}
}

// TestApply_FromConfig_MissingCell_CreatesViaApplyDocuments covers the
// "missing cell" leg of the -c reconciliation matrix. The materialised cell's
// stable name comes from StableName(configName); the YAML carries the config
// back-reference label.
func TestApply_FromConfig_MissingCell_CreatesViaApplyDocuments(t *testing.T) {
	fc := &fakeClient{
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			if doc.Metadata.Name != "prod" {
				t.Errorf("config lookup name=%q want prod", doc.Metadata.Name)
			}
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			if doc.Metadata.Name != "web" || doc.Metadata.Realm != "bp-realm" {
				t.Errorf("blueprint ref=%q/%q want web/bp-realm", doc.Metadata.Name, doc.Metadata.Realm)
			}
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
		},
		applyFn: func(raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
			if !strings.Contains(string(raw), "kukeon.io/config: prod") {
				t.Errorf("materialised YAML missing config lineage label; raw=\n%s", string(raw))
			}
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "Cell", Name: cellconfig.StableName("prod"), Action: "created"},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-c", "prod", "--realm", "cfg-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "created") {
		t.Errorf("missing created line in output:\n%s", out)
	}
}

// TestApply_FromConfig_LiveCellMatchingLineage_NoOpsViaUnchanged covers the
// "exists, matches" leg for -c.
func TestApply_FromConfig_LiveCellMatchingLineage_NoOpsViaUnchanged(t *testing.T) {
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   doc.Metadata.Name,
						Labels: map[string]string{cellconfig.LabelConfig: "prod"},
					},
				},
				MetadataExists: true,
			}, nil
		},
		applyFn: func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "Cell", Name: cellconfig.StableName("prod"), Action: "unchanged"},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-c", "prod", "--realm", "cfg-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "unchanged") {
		t.Errorf("missing unchanged line in output:\n%s", out)
	}
}

// TestApply_FromConfig_LiveCellDivergentSpec_UpdatesViaApplyDocuments covers
// the "exists, differs" leg for -c. The daemon-side reconciler is mocked to
// return the per-field changes; the CLI surfaces them under the updated header.
func TestApply_FromConfig_LiveCellDivergentSpec_UpdatesViaApplyDocuments(t *testing.T) {
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   doc.Metadata.Name,
						Labels: map[string]string{cellconfig.LabelConfig: "prod"},
					},
					Spec: v1beta1.CellSpec{
						Containers: []v1beta1.ContainerSpec{
							{ID: "main", Image: "registry.example.com/web:old"},
						},
					},
				},
				MetadataExists: true,
			}, nil
		},
		applyFn: func([]byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{
						Kind:    "Cell",
						Name:    cellconfig.StableName("prod"),
						Action:  "updated",
						Changes: []string{`container "main" updated: image`},
					},
				},
			}, nil
		},
	}

	out, err := runApplyRef(t, fc, []string{"-c", "prod", "--realm", "cfg-realm"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "updated") {
		t.Errorf("missing updated header in output:\n%s", out)
	}
	if !strings.Contains(out, `container "main" updated: image`) {
		t.Errorf("missing per-field diff line in output:\n%s", out)
	}
}

// TestApply_FromConfig_LiveCellHandBuilt_RefusesLineageMismatch is the parallel
// safety gate for -c: a cell sitting at the Config's StableName with no
// lineage label is refused.
func TestApply_FromConfig_LiveCellHandBuilt_RefusesLineageMismatch(t *testing.T) {
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: configBlueprintDoc(), MetadataExists: true}, nil
		},
		getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				Cell:           v1beta1.CellDoc{Metadata: v1beta1.CellMetadata{Name: doc.Metadata.Name}},
				MetadataExists: true,
			}, nil
		},
	}

	_, err := runApplyRef(t, fc, []string{"-c", "prod", "--realm", "cfg-realm"})
	if err == nil {
		t.Fatal("expected lineage-mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "no lineage label") {
		t.Errorf("err=%q want mention of 'no lineage label'", err.Error())
	}
	if !strings.Contains(err.Error(), "kukeon.io/config=prod") {
		t.Errorf("err=%q want mention of attempted lineage", err.Error())
	}
}

// TestApply_FromConfig_RejectsParamFlags asserts the cobra-side guard: -c is
// incompatible with --param / --param-file because a CellConfig owns its own
// spec.values channel.
func TestApply_FromConfig_RejectsParamFlags(t *testing.T) {
	fc := &fakeClient{}
	_, err := runApplyRef(t, fc, []string{"-c", "prod", "--param", "TAG=v3", "--realm", "cfg-realm"})
	if err == nil || !strings.Contains(err.Error(), "--param is not valid with -c/--config") {
		t.Fatalf("err=%v want rejection of --param with -c", err)
	}
}

// TestApply_BlueprintNotFound_Errors covers the lookup-miss path: GetBlueprint
// reports MetadataExists=false and the CLI surfaces ErrBlueprintNotFound
// before any cell read or apply fires.
func TestApply_BlueprintNotFound_Errors(t *testing.T) {
	fc := &fakeClient{
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{MetadataExists: false}, nil
		},
	}
	_, err := runApplyRef(t, fc, []string{"-b", "ghost", "--realm", "my-realm"})
	if err == nil || !errors.Is(err, errdefs.ErrBlueprintNotFound) {
		t.Fatalf("err=%v want ErrBlueprintNotFound", err)
	}
	if fc.applyCalls != 0 {
		t.Errorf("ApplyDocuments fired on lookup miss; want 0, got %d", fc.applyCalls)
	}
}

// TestApply_ConfigNotFound_Errors is the -c counterpart: GetConfig reports
// MetadataExists=false.
func TestApply_ConfigNotFound_Errors(t *testing.T) {
	fc := &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
	}
	_, err := runApplyRef(t, fc, []string{"-c", "ghost", "--realm", "cfg-realm"})
	if err == nil || !errors.Is(err, errdefs.ErrConfigNotFound) {
		t.Fatalf("err=%v want ErrConfigNotFound", err)
	}
}

type fakeClient struct {
	kukeonv1.FakeClient

	applyFn        func(raw []byte) (kukeonv1.ApplyDocumentsResult, error)
	getCellFn      func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	getBlueprintFn func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	getConfigFn    func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)

	applyCalls int
}

func (f *fakeClient) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	f.applyCalls++
	if f.applyFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyFn(raw)
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errdefs.ErrCellNotFound
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context, doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) GetConfig(
	_ context.Context, doc v1beta1.CellConfigDoc,
) (kukeonv1.GetConfigResult, error) {
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}
