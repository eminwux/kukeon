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

package space_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	space "github.com/eminwux/kukeon/cmd/kuke/create/space"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestNewSpaceCmd(t *testing.T) {
	t.Cleanup(viper.Reset)

	cmd := space.NewSpaceCmd()

	if cmd.Use != "space [name]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "space [name]")
	}
	if cmd.Short != "Create or reconcile a space within a realm" {
		t.Errorf("Short = %q, want %q", cmd.Short, "Create or reconcile a space within a realm")
	}
	if !cmd.SilenceUsage {
		t.Error("SilenceUsage = false, want true")
	}
	if cmd.SilenceErrors {
		t.Error("SilenceErrors = true, want false")
	}

	// Check Args validator
	if err := cmd.ValidateArgs([]string{"test"}); err != nil {
		t.Errorf("ValidateArgs with 1 arg failed: %v", err)
	}
	if err := cmd.ValidateArgs([]string{"test", "extra"}); err == nil {
		t.Error("ValidateArgs with 2 args should fail")
	}

	// Check realm flag exists and is bound
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("realm flag not found")
	}
	if realmFlag.Usage != "Realm that will own the space" {
		t.Errorf("realm flag usage = %q, want %q", realmFlag.Usage, "Realm that will own the space")
	}

	// Check viper binding
	if err := cmd.Flags().Set("realm", "test-realm"); err != nil {
		t.Fatalf("failed to set realm flag: %v", err)
	}
	if viper.GetString(config.KUKE_CREATE_SPACE_REALM.ViperKey) != "test-realm" {
		t.Errorf(
			"viper binding failed: got %q, want %q",
			viper.GetString(config.KUKE_CREATE_SPACE_REALM.ViperKey),
			"test-realm",
		)
	}
}

func TestNewSpaceCmd_AutocompleteRegistration(t *testing.T) {
	// Test that autocomplete function is properly registered for --realm flag
	cmd := space.NewSpaceCmd()

	// Test that realm flag exists
	realmFlag := cmd.Flags().Lookup("realm")
	if realmFlag == nil {
		t.Fatal("expected 'realm' flag to exist")
	}

	// Verify that the flag has a completion function registered
	// We can't directly access the completion function, but we can verify
	// the flag exists and the command structure is correct
	if realmFlag.Usage != "Realm that will own the space" {
		t.Errorf("unexpected realm flag usage: %q", realmFlag.Usage)
	}
}

func TestNewSpaceCmdRunE(t *testing.T) {
	t.Cleanup(viper.Reset)

	tests := []struct {
		name           string
		args           []string
		realmFlag      string
		nameConfig     string
		controller     *fakeSpaceController
		controllerErr  error
		createSpaceErr error
		wantOutput     []string
		wantErr        string
	}{
		{
			name:      "success with name from arg",
			args:      []string{"my-space"},
			realmFlag: "my-realm",
			controller: &fakeSpaceController{
				createSpaceFn: func(space intmodel.Space) (controller.CreateSpaceResult, error) {
					if space.Metadata.Name != "my-space" || space.Spec.RealmName != "my-realm" {
						return controller.CreateSpaceResult{}, errors.New("unexpected options")
					}
					return controller.CreateSpaceResult{
						Space:                space,
						Created:              true,
						CNINetworkCreated:    true,
						CgroupCreated:        true,
						MetadataExistsPost:   true,
						CNINetworkExistsPost: true,
						CgroupExistsPost:     true,
					}, nil
				},
			},
			wantOutput: []string{
				`Space "my-space" (realm "my-realm", network "my-realm-my-space")`,
				"  - metadata: created",
				"  - network: created",
				"  - cgroup: created",
			},
		},
		{
			name:       "success with name from config",
			realmFlag:  "my-realm",
			nameConfig: "config-space",
			controller: &fakeSpaceController{
				createSpaceFn: func(space intmodel.Space) (controller.CreateSpaceResult, error) {
					if space.Metadata.Name != "config-space" || space.Spec.RealmName != "my-realm" {
						return controller.CreateSpaceResult{}, errors.New("unexpected options")
					}
					return controller.CreateSpaceResult{
						Space:                space,
						Created:              true,
						CNINetworkCreated:    true,
						CgroupCreated:        true,
						MetadataExistsPost:   true,
						CNINetworkExistsPost: true,
						CgroupExistsPost:     true,
					}, nil
				},
			},
			wantOutput: []string{
				`Space "config-space" (realm "my-realm", network "my-realm-config-space")`,
				"  - metadata: created",
				"  - network: created",
				"  - cgroup: created",
			},
		},
		{
			name:      "success with all resources already existed",
			args:      []string{"existing-space"},
			realmFlag: "my-realm",
			controller: &fakeSpaceController{
				createSpaceFn: func(space intmodel.Space) (controller.CreateSpaceResult, error) {
					return controller.CreateSpaceResult{
						Space:                space,
						Created:              false,
						CNINetworkCreated:    false,
						CgroupCreated:        false,
						MetadataExistsPost:   true,
						CNINetworkExistsPost: true,
						CgroupExistsPost:     true,
					}, nil
				},
			},
			wantOutput: []string{
				`Space "existing-space" (realm "my-realm", network "my-realm-existing-space")`,
				"  - metadata: already existed",
				"  - network: already existed",
				"  - cgroup: already existed",
			},
		},
		{
			name:      "success with mixed states",
			args:      []string{"mixed-space"},
			realmFlag: "my-realm",
			controller: &fakeSpaceController{
				createSpaceFn: func(space intmodel.Space) (controller.CreateSpaceResult, error) {
					return controller.CreateSpaceResult{
						Space:                space,
						Created:              true,
						CNINetworkCreated:    false,
						CgroupCreated:        true,
						MetadataExistsPost:   true,
						CNINetworkExistsPost: true,
						CgroupExistsPost:     true,
					}, nil
				},
			},
			wantOutput: []string{
				`Space "mixed-space" (realm "my-realm", network "my-realm-mixed-space")`,
				"  - metadata: created",
				"  - network: already existed",
				"  - cgroup: created",
			},
		},
		{
			name:    "error missing realm",
			args:    []string{"my-space"},
			wantErr: "realm name is required",
		},
		{
			name:      "error missing name",
			realmFlag: "my-realm",
			wantErr:   "space name is required",
		},
		{
			name:          "error controller creation fails",
			args:          []string{"my-space"},
			realmFlag:     "my-realm",
			controllerErr: errdefs.ErrLoggerNotFound,
			wantErr:       "logger not found in context",
		},
		{
			name:           "error CreateSpace fails",
			args:           []string{"my-space"},
			realmFlag:      "my-realm",
			controller:     &fakeSpaceController{},
			createSpaceErr: errdefs.ErrCreateSpace,
			wantErr:        "failed to create space",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			cmd := space.NewSpaceCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)

			// Set up context with logger for controller creation
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			ctx := context.WithValue(context.Background(), types.CtxLogger, logger)

			// Inject mock controller via context if needed
			if tt.controller != nil {
				if tt.createSpaceErr != nil {
					tt.controller.createSpaceFn = func(intmodel.Space) (controller.CreateSpaceResult, error) {
						return controller.CreateSpaceResult{}, tt.createSpaceErr
					}
				}
				ctx = context.WithValue(ctx, space.MockControllerKey{}, tt.controller)
			}

			cmd.SetContext(ctx)

			if tt.realmFlag != "" {
				if err := cmd.Flags().Set("realm", tt.realmFlag); err != nil {
					t.Fatalf("failed to set realm flag: %v", err)
				}
			}
			if tt.nameConfig != "" {
				viper.Set(config.KUKE_CREATE_SPACE_NAME.ViperKey, tt.nameConfig)
			}

			// For controllerErr cases, we need to test without logger to trigger the error
			if tt.controllerErr != nil {
				cmd.SetContext(context.Background())
			}

			cmd.SetArgs(tt.args)
			err := cmd.Execute()

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			output := out.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
		})
	}
}

func TestPrintSpaceResult(t *testing.T) {
	tests := []struct {
		name          string
		result        controller.CreateSpaceResult
		wantOutput    []string
		wantNotOutput []string
	}{
		{
			name: "all created",
			result: controller.CreateSpaceResult{
				Space: intmodel.Space{
					Metadata: intmodel.SpaceMetadata{
						Name: "test-space",
					},
					Spec: intmodel.SpaceSpec{
						RealmName: "test-realm",
					},
				},
				Created:              true,
				CNINetworkCreated:    true,
				CgroupCreated:        true,
				MetadataExistsPost:   true,
				CNINetworkExistsPost: true,
				CgroupExistsPost:     true,
			},
			wantOutput: []string{
				`Space "test-space" (realm "test-realm", network "test-realm-test-space")`,
				"  - metadata: created",
				"  - network: created",
				"  - cgroup: created",
			},
		},
		{
			name: "all already existed",
			result: controller.CreateSpaceResult{
				Space: intmodel.Space{
					Metadata: intmodel.SpaceMetadata{
						Name: "existing-space",
					},
					Spec: intmodel.SpaceSpec{
						RealmName: "existing-realm",
					},
				},
				Created:              false,
				CNINetworkCreated:    false,
				CgroupCreated:        false,
				MetadataExistsPost:   true,
				CNINetworkExistsPost: true,
				CgroupExistsPost:     true,
			},
			wantOutput: []string{
				`Space "existing-space" (realm "existing-realm", network "existing-realm-existing-space")`,
				"  - metadata: already existed",
				"  - network: already existed",
				"  - cgroup: already existed",
			},
		},
		{
			name: "mixed states",
			result: controller.CreateSpaceResult{
				Space: intmodel.Space{
					Metadata: intmodel.SpaceMetadata{
						Name: "mixed-space",
					},
					Spec: intmodel.SpaceSpec{
						RealmName: "mixed-realm",
					},
				},
				Created:              true,
				CNINetworkCreated:    false,
				CgroupCreated:        true,
				MetadataExistsPost:   true,
				CNINetworkExistsPost: true,
				CgroupExistsPost:     true,
			},
			wantOutput: []string{
				`Space "mixed-space" (realm "mixed-realm", network "mixed-realm-mixed-space")`,
				"  - metadata: created",
				"  - network: already existed",
				"  - cgroup: created",
			},
		},
		{
			name: "missing resources",
			result: controller.CreateSpaceResult{
				Space: intmodel.Space{
					Metadata: intmodel.SpaceMetadata{
						Name: "missing-space",
					},
					Spec: intmodel.SpaceSpec{
						RealmName: "missing-realm",
					},
				},
				Created:              false,
				CNINetworkCreated:    false,
				CgroupCreated:        false,
				MetadataExistsPost:   false,
				CNINetworkExistsPost: false,
				CgroupExistsPost:     false,
			},
			wantOutput: []string{
				`Space "missing-space" (realm "missing-realm", network "missing-realm-missing-space")`,
				"  - metadata: missing",
				"  - network: missing",
				"  - cgroup: missing",
			},
		},
		{
			name: "metadata created but network and cgroup missing",
			result: controller.CreateSpaceResult{
				Space: intmodel.Space{
					Metadata: intmodel.SpaceMetadata{
						Name: "partial-space",
					},
					Spec: intmodel.SpaceSpec{
						RealmName: "partial-realm",
					},
				},
				Created:              true,
				CNINetworkCreated:    false,
				CgroupCreated:        false,
				MetadataExistsPost:   true,
				CNINetworkExistsPost: false,
				CgroupExistsPost:     false,
			},
			wantOutput: []string{
				`Space "partial-space" (realm "partial-realm", network "partial-realm-partial-space")`,
				"  - metadata: created",
				"  - network: missing",
				"  - cgroup: missing",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{}
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)

			space.PrintSpaceResult(cmd, tt.result, v1beta1.APIVersionV1Beta1)

			output := out.String()
			for _, want := range tt.wantOutput {
				if !strings.Contains(output, want) {
					t.Errorf("output missing %q, got:\n%s", want, output)
				}
			}
			for _, notWant := range tt.wantNotOutput {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q, got:\n%s", notWant, output)
				}
			}
		})
	}
}

// fakeSpaceController implements spaceController for testing.
type fakeSpaceController struct {
	createSpaceFn func(space intmodel.Space) (controller.CreateSpaceResult, error)
}

func (f *fakeSpaceController) CreateSpace(space intmodel.Space) (controller.CreateSpaceResult, error) {
	if f.createSpaceFn == nil {
		return controller.CreateSpaceResult{}, errors.New("unexpected CreateSpace call")
	}
	return f.createSpaceFn(space)
}
