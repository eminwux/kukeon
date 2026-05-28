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
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	cell "github.com/eminwux/kukeon/cmd/kuke/restart/cell"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

func TestRestartCell(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name       string
		args       []string
		setup      func()
		fake       *fakeClient
		wantErr    string
		wantOutput string
		wantStderr string
		// validate is run after a successful command for additional
		// post-conditions the assertion knobs above don't cover.
		validate func(t *testing.T, f *fakeClient)
	}{
		{
			name: "synced ready cell: stop then start with same spec",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: doc.Metadata.Name},
							Spec: v1beta1.CellSpec{
								ID:      doc.Metadata.Name,
								RealmID: "r1",
								SpaceID: "s1",
								StackID: "st1",
							},
							Status: v1beta1.CellStatus{State: v1beta1.CellStateReady, OutOfSync: false},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Restarted cell "c1" from stack "st1"`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.stopCalls != 1 || f.startCalls != 1 {
					t.Fatalf("want exactly 1 StopCell + 1 StartCell, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
				if f.applyCalls != 0 {
					t.Fatalf("Synced restart must not call ApplyDocuments, got %d calls", f.applyCalls)
				}
			},
		},
		{
			name: "outofsync ready cell: reconciles via ApplyDocuments then explicitly bounces",
			args: []string{"prod"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "prod",
								Labels: map[string]string{cellconfig.LabelConfig: "prod"},
							},
							Spec: v1beta1.CellSpec{ID: "prod", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:           v1beta1.CellStateReady,
								OutOfSync:       true,
								OutOfSyncReason: "spec differs",
							},
						},
					}, nil
				},
				getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					if doc.Metadata.Name != "prod" || doc.Metadata.Realm != "r1" {
						t.Errorf("GetConfig saw unexpected lookup: name=%q realm=%q",
							doc.Metadata.Name, doc.Metadata.Realm)
					}
					return kukeonv1.GetConfigResult{
						MetadataExists: true,
						Config: v1beta1.CellConfigDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellConfig,
							Metadata: v1beta1.CellConfigMetadata{
								Name:  "prod",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellConfigSpec{
								Blueprint: v1beta1.CellConfigBlueprintRef{
									Name:  "web",
									Realm: "r1",
									Space: "s1",
									Stack: "st1",
								},
							},
						},
					}, nil
				},
				getBlueprintFn: func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					if doc.Metadata.Name != "web" {
						t.Errorf("GetBlueprint saw unexpected name=%q", doc.Metadata.Name)
					}
					return kukeonv1.GetBlueprintResult{
						MetadataExists: true,
						Blueprint: v1beta1.CellBlueprintDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellBlueprint,
							Metadata: v1beta1.CellBlueprintMetadata{
								Name:  "web",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellBlueprintSpec{
								Cell: v1beta1.BlueprintCellSpec{
									Containers: []v1beta1.BlueprintContainer{
										{ID: "main", Image: "nginx:latest"},
									},
								},
							},
						},
					}, nil
				},
				applyDocsFn: func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
					var sent v1beta1.CellDoc
					if err := yaml.Unmarshal(rawYAML, &sent); err != nil {
						t.Errorf("ApplyDocuments received un-unmarshalable YAML: %v", err)
					}
					if sent.Metadata.Name != "prod" {
						t.Errorf("ApplyDocuments cell name=%q want %q", sent.Metadata.Name, "prod")
					}
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Cell", Name: "prod", Action: "updated"},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc}, nil
				},
			},
			wantOutput: `Restarted cell "prod" from stack "st1" (reconciled from config "prod")`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.applyCalls != 1 {
					t.Fatalf("want exactly 1 ApplyDocuments call, got %d", f.applyCalls)
				}
				// Apply alone does not guarantee a bounce — the daemon's
				// reconcile only stops+starts containers on image/cmd/args
				// changes. The CLI explicitly StopCell+StartCells afterwards
				// to honor the "restart" verb's contract.
				if f.stopCalls != 1 || f.startCalls != 1 {
					t.Fatalf("OutOfSync restart must follow Apply with StopCell+StartCell, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
			},
		},
		{
			// Reviewer-requested coverage of the reconcile-no-bounce gap: a
			// pure env-var divergence routes through UpdateCell's metadata
			// path (containerSpecChanged returns false for non-image/cmd/args
			// fields), so the daemon does NOT bounce containers. The CLI's
			// follow-up StopCell+StartCell is what makes "kuke restart" on
			// this divergence class actually bounce the running containers.
			name: "outofsync env-var-only divergence: reconciles then explicitly bounces",
			args: []string{"prod"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "prod",
								Labels: map[string]string{cellconfig.LabelConfig: "prod"},
							},
							Spec: v1beta1.CellSpec{ID: "prod", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:           v1beta1.CellStateReady,
								OutOfSync:       true,
								OutOfSyncReason: "env vars differ",
							},
						},
					}, nil
				},
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{
						MetadataExists: true,
						Config: v1beta1.CellConfigDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellConfig,
							Metadata: v1beta1.CellConfigMetadata{
								Name:  "prod",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellConfigSpec{
								Blueprint: v1beta1.CellConfigBlueprintRef{
									Name:  "web",
									Realm: "r1",
									Space: "s1",
									Stack: "st1",
								},
							},
						},
					}, nil
				},
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{
						MetadataExists: true,
						Blueprint: v1beta1.CellBlueprintDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellBlueprint,
							Metadata: v1beta1.CellBlueprintMetadata{
								Name:  "web",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellBlueprintSpec{
								Cell: v1beta1.BlueprintCellSpec{
									Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "nginx:latest"}},
								},
							},
						},
					}, nil
				},
				applyDocsFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					// Mirrors what reconcile.go emits when only env vars
					// changed: the container is "updated" with field list,
					// but no "image|command|args" entries are present, so
					// UpdateCell patched in place without bouncing.
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{
								Kind:    "Cell",
								Name:    "prod",
								Action:  "updated",
								Changes: []string{`container "main" updated: env`},
							},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc}, nil
				},
			},
			wantOutput: `Restarted cell "prod" from stack "st1" (reconciled from config "prod")`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.applyCalls != 1 {
					t.Fatalf("want exactly 1 ApplyDocuments call, got %d", f.applyCalls)
				}
				if f.stopCalls != 1 || f.startCalls != 1 {
					t.Fatalf(
						"env-var-only OutOfSync restart MUST follow Apply with StopCell+StartCell, got stop=%d start=%d",
						f.stopCalls,
						f.startCalls,
					)
				}
			},
		},
		{
			// The other half of the env-var-only test's contract: when the
			// daemon's reconcile already bounced every container (full root
			// recreate), the CLI must NOT add a redundant StopCell+StartCell.
			// reconcileBouncedAll's signal "root container recreated" gates
			// this branch.
			name: "outofsync ready cell with root-container recreate: skips redundant bounce",
			args: []string{"prod"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "prod",
								Labels: map[string]string{cellconfig.LabelConfig: "prod"},
							},
							Spec: v1beta1.CellSpec{ID: "prod", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:           v1beta1.CellStateReady,
								OutOfSync:       true,
								OutOfSyncReason: "root image bumped",
							},
						},
					}, nil
				},
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{
						MetadataExists: true,
						Config: v1beta1.CellConfigDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellConfig,
							Metadata: v1beta1.CellConfigMetadata{
								Name:  "prod",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellConfigSpec{
								Blueprint: v1beta1.CellConfigBlueprintRef{
									Name:  "web",
									Realm: "r1",
									Space: "s1",
									Stack: "st1",
								},
							},
						},
					}, nil
				},
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{
						MetadataExists: true,
						Blueprint: v1beta1.CellBlueprintDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellBlueprint,
							Metadata: v1beta1.CellBlueprintMetadata{
								Name:  "web",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellBlueprintSpec{
								Cell: v1beta1.BlueprintCellSpec{
									Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "nginx:latest"}},
								},
							},
						},
					}, nil
				},
				applyDocsFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{
								Kind:    "Cell",
								Name:    "prod",
								Action:  "updated",
								Changes: []string{"root container recreated"},
							},
						},
					}, nil
				},
			},
			wantOutput: `Restarted cell "prod" from stack "st1" (reconciled from config "prod")`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.applyCalls != 1 {
					t.Fatalf("want exactly 1 ApplyDocuments call, got %d", f.applyCalls)
				}
				if f.stopCalls != 0 || f.startCalls != 0 {
					t.Fatalf(
						"root-container recreate already bounced everything; CLI must NOT add a redundant StopCell+StartCell, got stop=%d start=%d",
						f.stopCalls,
						f.startCalls,
					)
				}
			},
		},
		{
			name: "stopped cell: equivalent to kuke start cell",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "c1"},
							Spec:     v1beta1.CellSpec{ID: "c1", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateStopped},
						},
					}, nil
				},
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc, Started: true}, nil
				},
			},
			wantOutput: `Started cell "c1" from stack "st1"`,
			validate: func(t *testing.T, f *fakeClient) {
				if f.stopCalls != 0 || f.startCalls != 1 {
					t.Fatalf("Stopped restart must call StartCell only, got stop=%d start=%d",
						f.stopCalls, f.startCalls)
				}
			},
		},
		{
			name: "failed cell: refused with kuke delete pointer",
			args: []string{"broken"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "broken"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateFailed},
						},
					}, nil
				},
			},
			wantErr: "kuke delete cell broken",
		},
		{
			name: "pending cell: refused with kuke delete pointer",
			args: []string{"halfborn"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "halfborn"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStatePending},
						},
					}, nil
				},
			},
			wantErr: "kuke delete cell halfborn",
		},
		{
			name: "missing cell: returns ErrCellNotFound",
			args: []string{"nope"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{MetadataExists: false}, nil
				},
			},
			wantErr: "cell not found",
		},
		{
			name:    "missing realm",
			args:    []string{"c1"},
			wantErr: "realm name is required",
		},
		{
			name: "outofsync cell with no lineage label: vanilla restart with notice",
			args: []string{"orphan"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "orphan", Labels: map[string]string{}},
							Spec:     v1beta1.CellSpec{ID: "orphan", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady, OutOfSync: true},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc}, nil
				},
			},
			wantOutput: `Restarted cell "orphan" from stack "st1"`,
			wantStderr: `notice: cell "orphan" is OutOfSync but carries no kukeon.io/config label`,
		},
		{
			name: "outofsync ready cell with OutOfSyncError: vanilla restart with notice",
			args: []string{"degraded"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "degraded",
								Labels: map[string]string{cellconfig.LabelConfig: "degraded"},
							},
							Spec: v1beta1.CellSpec{ID: "degraded", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:          v1beta1.CellStateReady,
								OutOfSyncError: "referenced blueprint missing",
							},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc}, nil
				},
			},
			wantOutput: `Restarted cell "degraded" from stack "st1"`,
			wantStderr: `OutOfSync detection failed (referenced blueprint missing)`,
		},
		{
			name: "outofsync cell whose lineage Config was deleted: falls back to vanilla restart",
			args: []string{"orphan"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "orphan",
								Labels: map[string]string{cellconfig.LabelConfig: "deleted-cfg"},
							},
							Spec: v1beta1.CellSpec{ID: "orphan", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{
								State:           v1beta1.CellStateReady,
								OutOfSync:       true,
								OutOfSyncReason: "lineage Config deleted",
							},
						},
					}, nil
				},
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{MetadataExists: false}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
				startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
					return kukeonv1.StartCellResult{Cell: doc}, nil
				},
			},
			wantOutput: `Restarted cell "orphan" from stack "st1"`,
			wantStderr: `reconcile materialisation failed`,
		},
		{
			name: "ApplyDocuments returns a failed row: surfaces as error",
			args: []string{"prod"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{
								Name:   "prod",
								Labels: map[string]string{cellconfig.LabelConfig: "prod"},
							},
							Spec:   v1beta1.CellSpec{ID: "prod", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status: v1beta1.CellStatus{State: v1beta1.CellStateReady, OutOfSync: true},
						},
					}, nil
				},
				getConfigFn: func(_ v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
					return kukeonv1.GetConfigResult{
						MetadataExists: true,
						Config: v1beta1.CellConfigDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellConfig,
							Metadata: v1beta1.CellConfigMetadata{
								Name:  "prod",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellConfigSpec{
								Blueprint: v1beta1.CellConfigBlueprintRef{
									Name:  "web",
									Realm: "r1",
									Space: "s1",
									Stack: "st1",
								},
							},
						},
					}, nil
				},
				getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
					return kukeonv1.GetBlueprintResult{
						MetadataExists: true,
						Blueprint: v1beta1.CellBlueprintDoc{
							APIVersion: v1beta1.APIVersionV1Beta1,
							Kind:       v1beta1.KindCellBlueprint,
							Metadata: v1beta1.CellBlueprintMetadata{
								Name:  "web",
								Realm: "r1",
								Space: "s1",
								Stack: "st1",
							},
							Spec: v1beta1.CellBlueprintSpec{
								Cell: v1beta1.BlueprintCellSpec{
									Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "nginx:latest"}},
								},
							},
						},
					}, nil
				},
				applyDocsFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
					return kukeonv1.ApplyDocumentsResult{
						Resources: []kukeonv1.ApplyResourceResult{
							{Kind: "Cell", Name: "prod", Action: "failed", Error: "containerd update rejected"},
						},
					}, nil
				},
			},
			wantErr: "reconcile failed for Cell \"prod\"",
		},
		{
			name: "stop step errors: surfaces and skips start",
			args: []string{"c1"},
			setup: func() {
				viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "r1")
				viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "s1")
				viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "st1")
			},
			fake: &fakeClient{
				getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
					return kukeonv1.GetCellResult{
						MetadataExists: true,
						Cell: v1beta1.CellDoc{
							Metadata: v1beta1.CellMetadata{Name: "c1"},
							Spec:     v1beta1.CellSpec{ID: "c1", RealmID: "r1", SpaceID: "s1", StackID: "st1"},
							Status:   v1beta1.CellStatus{State: v1beta1.CellStateReady},
						},
					}, nil
				},
				stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
					return kukeonv1.StopCellResult{}, errors.New("boom")
				},
			},
			wantErr: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			viper.Reset()
			if tt.setup != nil {
				tt.setup()
			}

			cmd := cell.NewCellCmd()
			outBuf := &bytes.Buffer{}
			errBuf := &bytes.Buffer{}
			cmd.SetOut(outBuf)
			cmd.SetErr(errBuf)
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
			if tt.fake != nil {
				ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(tt.fake))
			}
			cmd.SetContext(ctx)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want err containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantOutput != "" && !strings.Contains(outBuf.String(), tt.wantOutput) {
				t.Errorf("stdout missing %q\nGot:\n%s", tt.wantOutput, outBuf.String())
			}
			if tt.wantStderr != "" && !strings.Contains(errBuf.String(), tt.wantStderr) {
				t.Errorf("stderr missing %q\nGot:\n%s", tt.wantStderr, errBuf.String())
			}
			if tt.validate != nil {
				tt.validate(t, tt.fake)
			}
		})
	}
}

// TestRestartCell_CmdSurface pins the user-facing CLI shape: positional arg,
// scope flags, completion. Catches accidental flag renames or arg-count drift.
func TestRestartCell_CmdSurface(t *testing.T) {
	cmd := cell.NewCellCmd()

	if !cmd.HasAlias("ce") {
		t.Errorf("expected alias %q", "ce")
	}
	for _, f := range []string{"realm", "space", "stack"} {
		if cmd.Flag(f) == nil {
			t.Errorf("expected flag --%s to be registered", f)
		}
	}
	if cmd.ValidArgsFunction == nil {
		t.Error("expected ValidArgsFunction to be set for cell-name completion")
	}
	// The help text must surface the dual-mode behaviour (vanilla restart +
	// implicit reconcile) so operators see the contract without reading code.
	if !strings.Contains(cmd.Long, "reconcile") || !strings.Contains(cmd.Long, "OutOfSync") {
		t.Errorf("expected Long help to describe dual mode (reconcile + OutOfSync), got: %s", cmd.Long)
	}
}

// TestRestartCell_RealmScopedConfigFoundForStackPlacedCell pins the issue
// #921 fix on the CLI side: an OutOfSync cell at realm/space/stack whose
// `kukeon.io/config` lineage label names a Config bound only at realm
// scope must still drive a reconcile via ApplyDocuments. Without the
// scope-narrowing walk, materialiseFromConfig calls GetConfig once at the
// cell's full scope, sees MetadataExists==false, and falls through to a
// vanilla restart with the "reconcile materialisation failed (config not
// found …)" notice — never reconciling from the realm-scoped Config.
func TestRestartCell_RealmScopedConfigFoundForStackPlacedCell(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	viper.Set(config.KUKE_RESTART_CELL_REALM.ViperKey, "default")
	viper.Set(config.KUKE_RESTART_CELL_SPACE.ViperKey, "default")
	viper.Set(config.KUKE_RESTART_CELL_STACK.ViperKey, "default")

	type probeScope struct{ name, realm, space, stack string }
	var probes []probeScope
	fake := &fakeClient{
		getCellFn: func(_ v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
			return kukeonv1.GetCellResult{
				MetadataExists: true,
				Cell: v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{
						Name:   "kukeon-dev-root-0",
						Labels: map[string]string{cellconfig.LabelConfig: "kukeon-dev-root-0"},
					},
					Spec: v1beta1.CellSpec{
						ID:      "kukeon-dev-root-0",
						RealmID: "default",
						SpaceID: "default",
						StackID: "default",
					},
					Status: v1beta1.CellStatus{
						State:           v1beta1.CellStateReady,
						OutOfSync:       true,
						OutOfSyncReason: "lineage Config deleted",
					},
				},
			}, nil
		},
		getConfigFn: func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			probes = append(probes, probeScope{
				name:  doc.Metadata.Name,
				realm: doc.Metadata.Realm,
				space: doc.Metadata.Space,
				stack: doc.Metadata.Stack,
			})
			if doc.Metadata.Space == "" && doc.Metadata.Stack == "" {
				return kukeonv1.GetConfigResult{
					MetadataExists: true,
					Config: v1beta1.CellConfigDoc{
						APIVersion: v1beta1.APIVersionV1Beta1,
						Kind:       v1beta1.KindCellConfig,
						Metadata: v1beta1.CellConfigMetadata{
							Name:  "kukeon-dev-root-0",
							Realm: "default",
						},
						Spec: v1beta1.CellConfigSpec{
							Blueprint: v1beta1.CellConfigBlueprintRef{Name: "dev", Realm: "default"},
						},
					},
				}, nil
			}
			return kukeonv1.GetConfigResult{MetadataExists: false}, nil
		},
		getBlueprintFn: func(_ v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{
				MetadataExists: true,
				Blueprint: v1beta1.CellBlueprintDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindCellBlueprint,
					Metadata:   v1beta1.CellBlueprintMetadata{Name: "dev", Realm: "default"},
					Spec: v1beta1.CellBlueprintSpec{
						Cell: v1beta1.BlueprintCellSpec{
							Containers: []v1beta1.BlueprintContainer{{ID: "main", Image: "nginx:latest"}},
						},
					},
				},
			}, nil
		},
		applyDocsFn: func(_ []byte) (kukeonv1.ApplyDocumentsResult, error) {
			return kukeonv1.ApplyDocumentsResult{
				Resources: []kukeonv1.ApplyResourceResult{
					{Kind: "Cell", Name: "kukeon-dev-root-0", Action: "updated"},
				},
			}, nil
		},
		stopCellFn: func(_ v1beta1.CellDoc) (kukeonv1.StopCellResult, error) { return kukeonv1.StopCellResult{}, nil },
		startCellFn: func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
			return kukeonv1.StartCellResult{Cell: doc}, nil
		},
	}

	cmd := cell.NewCellCmd()
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}
	cmd.SetOut(outBuf)
	cmd.SetErr(errBuf)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.WithValue(context.Background(), types.CtxLogger, logger)
	ctx = context.WithValue(ctx, cell.MockControllerKey{}, kukeonv1.Client(fake))
	cmd.SetContext(ctx)
	cmd.SetArgs([]string{"kukeon-dev-root-0"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantProbes := []probeScope{
		{name: "kukeon-dev-root-0", realm: "default", space: "default", stack: "default"},
		{name: "kukeon-dev-root-0", realm: "default", space: "default", stack: ""},
		{name: "kukeon-dev-root-0", realm: "default", space: "", stack: ""},
	}
	if len(probes) != len(wantProbes) {
		t.Fatalf("GetConfig probe count: got %d want %d (probes=%+v)", len(probes), len(wantProbes), probes)
	}
	for i, p := range probes {
		if p != wantProbes[i] {
			t.Errorf("probe[%d] = %+v, want %+v", i, p, wantProbes[i])
		}
	}
	if fake.applyCalls != 1 {
		t.Errorf(
			"ApplyDocuments calls: got %d, want 1 (reconcile must run once the realm-scoped Config is found)",
			fake.applyCalls,
		)
	}
	if strings.Contains(errBuf.String(), "reconcile materialisation failed") {
		t.Errorf(
			"stderr surfaced 'reconcile materialisation failed' despite the realm-scoped Config being reachable: %s",
			errBuf.String(),
		)
	}
	if !strings.Contains(outBuf.String(), `reconciled from config "kukeon-dev-root-0"`) {
		t.Errorf("stdout did not confirm reconcile path: %s", outBuf.String())
	}
}

// fakeClient is a per-test stub kukeonv1.Client. Each RPC is dispatched
// through an optional Fn field; nil means "fail the test if called". Call
// counts let tests assert "Synced restart did not invoke ApplyDocuments" and
// the equivalent path-specific assertions.
type fakeClient struct {
	kukeonv1.FakeClient

	getCellFn      func(doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error)
	getConfigFn    func(doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error)
	getBlueprintFn func(doc v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	stopCellFn     func(doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error)
	startCellFn    func(doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error)
	applyDocsFn    func(rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error)

	getCellCalls int
	stopCalls    int
	startCalls   int
	getConfigCnt int
	getBpCalls   int
	applyCalls   int
}

func (f *fakeClient) GetCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.GetCellResult, error) {
	f.getCellCalls++
	if f.getCellFn == nil {
		return kukeonv1.GetCellResult{}, errors.New("unexpected GetCell call")
	}
	return f.getCellFn(doc)
}

func (f *fakeClient) GetConfig(_ context.Context, doc v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
	f.getConfigCnt++
	if f.getConfigFn == nil {
		return kukeonv1.GetConfigResult{}, errors.New("unexpected GetConfig call")
	}
	return f.getConfigFn(doc)
}

func (f *fakeClient) GetBlueprint(
	_ context.Context,
	doc v1beta1.CellBlueprintDoc,
) (kukeonv1.GetBlueprintResult, error) {
	f.getBpCalls++
	if f.getBlueprintFn == nil {
		return kukeonv1.GetBlueprintResult{}, errors.New("unexpected GetBlueprint call")
	}
	return f.getBlueprintFn(doc)
}

func (f *fakeClient) StopCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StopCellResult, error) {
	f.stopCalls++
	if f.stopCellFn == nil {
		return kukeonv1.StopCellResult{}, errors.New("unexpected StopCell call")
	}
	return f.stopCellFn(doc)
}

func (f *fakeClient) StartCell(_ context.Context, doc v1beta1.CellDoc) (kukeonv1.StartCellResult, error) {
	f.startCalls++
	if f.startCellFn == nil {
		return kukeonv1.StartCellResult{}, errors.New("unexpected StartCell call")
	}
	return f.startCellFn(doc)
}

func (f *fakeClient) ApplyDocuments(_ context.Context, rawYAML []byte) (kukeonv1.ApplyDocumentsResult, error) {
	f.applyCalls++
	if f.applyDocsFn == nil {
		return kukeonv1.ApplyDocumentsResult{}, errors.New("unexpected ApplyDocuments call")
	}
	return f.applyDocsFn(rawYAML)
}

// Sanity guard that the sentinel error in errdefs is still the one we
// reference — if it gets renamed, this fails at compile time rather than
// silently slipping into a string-match assertion.
var _ = errdefs.ErrCellNotFound
