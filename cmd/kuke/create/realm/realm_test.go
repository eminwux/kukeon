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
				Name:                          "test-realm",
				Namespace:                     "test-ns",
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
				Name:                          "existing-realm",
				Namespace:                     "existing-ns",
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
				Name:                          "mixed-realm",
				Namespace:                     "mixed-ns",
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
				Name:                          "ns-created",
				Namespace:                     "ns-created-ns",
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
				Name:                          "cgroup-created",
				Namespace:                     "cgroup-created-ns",
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
				Name:                          "missing-metadata",
				Namespace:                     "missing-ns",
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
				Name:                          "missing-ns",
				Namespace:                     "missing-ns-name",
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
				Name:                          "missing-cgroup",
				Namespace:                     "missing-cgroup-ns",
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
				Name:                          "all-missing",
				Namespace:                     "all-missing-ns",
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
					createRealmFn: func(opts controller.CreateRealmOptions) (controller.CreateRealmResult, error) {
						namespace := opts.Namespace
						if namespace == "" {
							namespace = opts.Name
						}
						return controller.CreateRealmResult{
							Name:                          opts.Name,
							Namespace:                     namespace,
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
	createRealmFn func(opts controller.CreateRealmOptions) (controller.CreateRealmResult, error)
}

func (f *fakeRealmController) CreateRealm(opts controller.CreateRealmOptions) (controller.CreateRealmResult, error) {
	if f.createRealmFn == nil {
		return controller.CreateRealmResult{}, errdefs.ErrCreateRealm
	}
	return f.createRealmFn(opts)
}
