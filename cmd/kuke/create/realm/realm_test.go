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

package realm_test

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

	"github.com/eminwux/kukeon/cmd/config"
	realm "github.com/eminwux/kukeon/cmd/kuke/create/realm"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestPrintRealmResult(t *testing.T) {
	tests := []struct {
		name        string
		result      controller.CreateRealmResult
		expectMatch []string
	}{
		{
			name: "all resources created",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "test-realm",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "test-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       true,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    true,
				CgroupExistsPost:              true,
				CgroupCreated:                 true,
			},
			expectMatch: []string{
				`Realm "test-realm" (namespace "test-ns")`,
				"  - metadata: created",
				"  - containerd namespace: created",
				"  - cgroup: created",
			},
		},
		{
			name: "all resources already existed",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "existing-realm",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "existing-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       false,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              true,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "existing-realm" (namespace "existing-ns")`,
				"  - metadata: already existed",
				"  - containerd namespace: already existed",
				"  - cgroup: already existed",
			},
		},
		{
			name: "metadata created, others existed",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "mixed-realm",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "mixed-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       true,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              true,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "mixed-realm" (namespace "mixed-ns")`,
				"  - metadata: created",
				"  - containerd namespace: already existed",
				"  - cgroup: already existed",
			},
		},
		{
			name: "namespace created, others existed",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "ns-created",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "ns-created-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       false,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    true,
				CgroupExistsPost:              true,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "ns-created" (namespace "ns-created-ns")`,
				"  - metadata: already existed",
				"  - containerd namespace: created",
				"  - cgroup: already existed",
			},
		},
		{
			name: "cgroup created, others existed",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "cgroup-created",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "cgroup-created-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       false,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              true,
				CgroupCreated:                 true,
			},
			expectMatch: []string{
				`Realm "cgroup-created" (namespace "cgroup-created-ns")`,
				"  - metadata: already existed",
				"  - containerd namespace: already existed",
				"  - cgroup: created",
			},
		},
		{
			name: "metadata missing",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "missing-metadata",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "missing-ns",
					},
				},
				MetadataExistsPost:            false,
				Created:                       false,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              true,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "missing-metadata" (namespace "missing-ns")`,
				"  - metadata: missing",
				"  - containerd namespace: already existed",
				"  - cgroup: already existed",
			},
		},
		{
			name: "namespace missing",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "missing-ns",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "missing-ns-name",
					},
				},
				MetadataExistsPost:            true,
				Created:                       false,
				ContainerdNamespaceExistsPost: false,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              true,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "missing-ns" (namespace "missing-ns-name")`,
				"  - metadata: already existed",
				"  - containerd namespace: missing",
				"  - cgroup: already existed",
			},
		},
		{
			name: "cgroup missing",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "missing-cgroup",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "missing-cgroup-ns",
					},
				},
				MetadataExistsPost:            true,
				Created:                       false,
				ContainerdNamespaceExistsPost: true,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              false,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "missing-cgroup" (namespace "missing-cgroup-ns")`,
				"  - metadata: already existed",
				"  - containerd namespace: already existed",
				"  - cgroup: missing",
			},
		},
		{
			name: "all missing",
			result: controller.CreateRealmResult{
				RealmDoc: &v1beta1.RealmDoc{
					Metadata: v1beta1.RealmMetadata{
						Name: "all-missing",
					},
					Spec: v1beta1.RealmSpec{
						Namespace: "all-missing-ns",
					},
				},
				MetadataExistsPost:            false,
				Created:                       false,
				ContainerdNamespaceExistsPost: false,
				ContainerdNamespaceCreated:    false,
				CgroupExistsPost:              false,
				CgroupCreated:                 false,
			},
			expectMatch: []string{
				`Realm "all-missing" (namespace "all-missing-ns")`,
				"  - metadata: missing",
				"  - containerd namespace: missing",
				"  - cgroup: missing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "realm"}
			buf := &bytes.Buffer{}
			cmd.SetOut(buf)
			cmd.SetErr(buf)

			realm.PrintRealmResult(cmd, tt.result)

			output := buf.String()
			for _, match := range tt.expectMatch {
				if !strings.Contains(output, match) {
					t.Errorf("output %q missing expected match %q", output, match)
				}
			}
		})
	}
}

func TestNewRealmCmd(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		setup              func(t *testing.T, runPath string)
		expectErr          bool
		expectErrText      string
		expectMatch        string
		useStdout          bool
		skipIfNoContainerd bool
	}{
		{
			name: "error missing name no arg no config",
			setup: func(_ *testing.T, _ string) {
				// No config set
			},
			expectErr:     true,
			expectErrText: "realm name is required",
		},
		{
			name: "error missing logger in context",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ string) {
				// Context without logger will be set in test
			},
			expectErr:     true,
			expectErrText: "logger not found in context",
		},
		{
			name: "error with empty name after trim",
			args: []string{"   "},
			setup: func(_ *testing.T, _ string) {
				// Empty name after trim should fail
			},
			expectErr:     true,
			expectErrText: "realm name is required",
		},
		{
			name: "success with name argument",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ string) {
				// Mock controller will be injected via context
			},
			expectMatch: `Realm "test-realm"`,
		},
		{
			name: "success with name from config",
			setup: func(_ *testing.T, _ string) {
				viper.Set(config.KUKE_CREATE_REALM_NAME.ViperKey, "config-realm")
			},
			expectMatch: `Realm "config-realm"`,
		},
		{
			name: "success with namespace flag",
			args: []string{"test-realm", "--namespace", "custom-ns"},
			setup: func(_ *testing.T, _ string) {
				// Mock controller will be injected via context
			},
			expectMatch: `namespace "custom-ns"`,
		},
		{
			name: "success with empty namespace defaults to name",
			args: []string{"default-ns-realm"},
			setup: func(_ *testing.T, _ string) {
				// Mock controller will be injected via context
			},
			expectMatch: `Realm "default-ns-realm" (namespace "default-ns-realm")`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd, buf := newRealmTestCommand(t, runPath, tt.name == "error missing logger in context")

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			// Inject mock controller via context for success cases
			if !tt.expectErr && tt.expectMatch != "" {
				ctx := cmd.Context()
				mockCtrl := &fakeRealmController{
					createRealmFn: func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
						namespace := doc.Spec.Namespace
						if namespace == "" {
							namespace = doc.Metadata.Name
						}
						return controller.CreateRealmResult{
							RealmDoc: &v1beta1.RealmDoc{
								Metadata: v1beta1.RealmMetadata{
									Name: doc.Metadata.Name,
								},
								Spec: v1beta1.RealmSpec{
									Namespace: namespace,
								},
							},
							Created:                       true,
							ContainerdNamespaceCreated:    true,
							CgroupCreated:                 true,
							MetadataExistsPost:            true,
							ContainerdNamespaceExistsPost: true,
							CgroupExistsPost:              true,
						}, nil
					},
				}
				ctx = context.WithValue(ctx, realm.MockControllerKey{}, mockCtrl)
				cmd.SetContext(ctx)
			}

			cmd.SetArgs(tt.args)

			var (
				out string
				err error
			)
			if tt.useStdout {
				out, err = captureStdout(cmd.Execute)
			} else {
				err = cmd.Execute()
				out = buf.String()
			}

			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.expectErrText != "" && !strings.Contains(err.Error(), tt.expectErrText) {
					t.Fatalf("expected error containing %q, got %v", tt.expectErrText, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.expectMatch != "" && !strings.Contains(out, tt.expectMatch) {
				t.Fatalf("output %q missing expected match %q", out, tt.expectMatch)
			}
		})
	}
}

func TestNewRealmCmd_CommandStructure(t *testing.T) {
	// Test that the command is created with correct structure
	cmd := realm.NewRealmCmd()

	if cmd.Use != "realm [name]" {
		t.Errorf("expected Use to be 'realm [name]', got %q", cmd.Use)
	}

	if cmd.Short != "Create or reconcile a realm" {
		t.Errorf("expected Short to be 'Create or reconcile a realm', got %q", cmd.Short)
	}

	if !cmd.SilenceUsage {
		t.Error("expected SilenceUsage to be true")
	}

	if cmd.SilenceErrors {
		t.Error("expected SilenceErrors to be false")
	}

	// Test that namespace flag exists
	namespaceFlag := cmd.Flags().Lookup("namespace")
	if namespaceFlag == nil {
		t.Fatal("expected 'namespace' flag to exist")
	}

	if namespaceFlag.Usage != "Containerd namespace for the realm (defaults to the realm name)" {
		t.Errorf("unexpected namespace flag usage: %q", namespaceFlag.Usage)
	}
}

func TestNewRealmCmdRunE(t *testing.T) {
	t.Cleanup(func() {
		viper.Reset()
	})

	tests := []struct {
		name           string
		args           []string
		setup          func(t *testing.T, cmd *cobra.Command)
		controllerFn   func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error)
		wantErr        string
		wantCallCreate bool
		wantDoc        *v1beta1.RealmDoc
		wantOutput     []string
	}{
		{
			name: "success: name from args",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No setup needed
			},
			controllerFn: func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
				namespace := doc.Spec.Namespace
				if namespace == "" {
					namespace = doc.Metadata.Name
				}
				return controller.CreateRealmResult{
					RealmDoc: &v1beta1.RealmDoc{
						Metadata: v1beta1.RealmMetadata{
							Name: doc.Metadata.Name,
						},
						Spec: v1beta1.RealmSpec{
							Namespace: namespace,
						},
					},
					Created:                       true,
					MetadataExistsPost:            true,
					ContainerdNamespaceCreated:    true,
					ContainerdNamespaceExistsPost: true,
					CgroupCreated:                 true,
					CgroupExistsPost:              true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc: &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "",
				},
			},
			wantOutput: []string{
				`Realm "test-realm"`,
			},
		},
		{
			name: "success: name from viper",
			setup: func(_ *testing.T, _ *cobra.Command) {
				viper.Set(config.KUKE_CREATE_REALM_NAME.ViperKey, "viper-realm")
			},
			controllerFn: func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
				namespace := doc.Spec.Namespace
				if namespace == "" {
					namespace = doc.Metadata.Name
				}
				return controller.CreateRealmResult{
					RealmDoc: &v1beta1.RealmDoc{
						Metadata: v1beta1.RealmMetadata{
							Name: doc.Metadata.Name,
						},
						Spec: v1beta1.RealmSpec{
							Namespace: namespace,
						},
					},
					Created:                       true,
					MetadataExistsPost:            true,
					ContainerdNamespaceCreated:    true,
					ContainerdNamespaceExistsPost: true,
					CgroupCreated:                 true,
					CgroupExistsPost:              true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc: &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: "viper-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "",
				},
			},
			wantOutput: []string{
				`Realm "viper-realm"`,
			},
		},
		{
			name: "success: with namespace flag",
			args: []string{"test-realm"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				setFlag(t, cmd, "namespace", "custom-ns")
			},
			controllerFn: func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
				return controller.CreateRealmResult{
					RealmDoc: &v1beta1.RealmDoc{
						Metadata: v1beta1.RealmMetadata{
							Name: doc.Metadata.Name,
						},
						Spec: v1beta1.RealmSpec{
							Namespace: doc.Spec.Namespace,
						},
					},
					Created:                       true,
					MetadataExistsPost:            true,
					ContainerdNamespaceCreated:    true,
					ContainerdNamespaceExistsPost: true,
					CgroupCreated:                 true,
					CgroupExistsPost:              true,
				}, nil
			},
			wantCallCreate: true,
			wantDoc: &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "custom-ns",
				},
			},
			wantOutput: []string{
				`namespace "custom-ns"`,
			},
		},
		{
			name: "error: missing name",
			setup: func(_ *testing.T, _ *cobra.Command) {
				// Don't set name in args or viper
			},
			wantErr:        "realm name is required",
			wantCallCreate: false,
		},
		{
			name: "error: logger not in context",
			args: []string{"test-realm"},
			setup: func(t *testing.T, cmd *cobra.Command) {
				cmd.SetContext(context.Background())
			},
			controllerFn: func(_ *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
				return controller.CreateRealmResult{}, errors.New("unexpected call")
			},
			wantErr:        "logger not found",
			wantCallCreate: false,
		},
		{
			name: "error: CreateRealm fails",
			args: []string{"test-realm"},
			setup: func(_ *testing.T, _ *cobra.Command) {
				// No setup needed
			},
			controllerFn: func(_ *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
				return controller.CreateRealmResult{}, errdefs.ErrCreateRealm
			},
			wantErr:        "failed to create realm",
			wantCallCreate: true,
			wantDoc: &v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{
					Name: "test-realm",
				},
				Spec: v1beta1.RealmSpec{
					Namespace: "",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Cleanup(viper.Reset)

			var createCalled bool
			var createDoc *v1beta1.RealmDoc

			cmd := realm.NewRealmCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})

			ctx := context.Background()

			// Inject mock controller via context if needed
			if tt.name != "error: logger not in context" {
				// Set up logger context
				logger := slog.New(slog.NewTextHandler(io.Discard, nil))
				ctx = context.WithValue(ctx, types.CtxLogger, logger)

				// If we need to mock the controller, inject it via context
				if tt.controllerFn != nil {
					fakeCtrl := &fakeRealmController{
						createRealmFn: func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
							createCalled = true
							createDoc = doc
							return tt.controllerFn(doc)
						},
					}
					// Inject mock controller into context using the exported key
					ctx = context.WithValue(ctx, realm.MockControllerKey{}, fakeCtrl)
				}
			}

			cmd.SetContext(ctx)

			if tt.setup != nil {
				tt.setup(t, cmd)
			}

			cmd.SetArgs(tt.args)

			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if createCalled != tt.wantCallCreate {
				t.Errorf("CreateRealm called=%v want=%v", createCalled, tt.wantCallCreate)
			}

			if tt.wantDoc != nil {
				if createDoc == nil {
					t.Fatalf("CreateRealm doc is nil, want %+v", tt.wantDoc)
				}
				if createDoc.Metadata.Name != tt.wantDoc.Metadata.Name {
					t.Errorf("CreateRealm Name=%q want=%q", createDoc.Metadata.Name, tt.wantDoc.Metadata.Name)
				}
				if createDoc.Spec.Namespace != tt.wantDoc.Spec.Namespace {
					t.Errorf("CreateRealm Namespace=%q want=%q", createDoc.Spec.Namespace, tt.wantDoc.Spec.Namespace)
				}
			}

			if tt.wantOutput != nil {
				output := cmd.OutOrStdout().(*bytes.Buffer).String()
				for _, expected := range tt.wantOutput {
					if !strings.Contains(output, expected) {
						t.Errorf("output missing expected string %q\nGot output:\n%s", expected, output)
					}
				}
			}
		})
	}
}

func TestNewRealmCmd_AutocompleteRegistration(t *testing.T) {
	cmd := realm.NewRealmCmd()

	// Test that ValidArgsFunction is NOT set (create realm is the only command without autocomplete)
	if cmd.ValidArgsFunction != nil {
		t.Fatal("expected ValidArgsFunction to be nil for create realm command")
	}

	// Test that namespace flag exists
	namespaceFlag := cmd.Flags().Lookup("namespace")
	if namespaceFlag == nil {
		t.Fatal("expected 'namespace' flag to exist")
	}

	// Verify flag structure (completion function registration is verified by Cobra)
	if namespaceFlag.Usage != "Containerd namespace for the realm (defaults to the realm name)" {
		t.Errorf("unexpected namespace flag usage: %q", namespaceFlag.Usage)
	}
}

func setFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("failed to set flag %s: %v", name, err)
	}
}

func captureStdout(fn func() error) (string, error) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}

	os.Stdout = w
	runErr := fn()
	_ = w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	_, copyErr := io.Copy(&buf, r)
	_ = r.Close()
	if copyErr != nil {
		return "", copyErr
	}
	return buf.String(), runErr
}

func newRealmTestCommand(t *testing.T, runPath string, noLogger bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()

	t.Cleanup(viper.Reset)
	viper.Reset()

	viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
	viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(runPath, "containerd.sock"))

	cmd := realm.NewRealmCmd()

	var ctx context.Context
	if noLogger {
		ctx = context.Background()
	} else {
		ctx = context.WithValue(context.Background(), types.CtxLogger, testLogger())
	}
	cmd.SetContext(ctx)

	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	return cmd, buf
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type fakeRealmController struct {
	createRealmFn func(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error)
}

func (f *fakeRealmController) CreateRealm(doc *v1beta1.RealmDoc) (controller.CreateRealmResult, error) {
	if f.createRealmFn == nil {
		return controller.CreateRealmResult{}, errdefs.ErrCreateRealm
	}
	return f.createRealmFn(doc)
}
