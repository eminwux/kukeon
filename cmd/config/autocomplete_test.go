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

package config_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestCompleteRealmNames(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, runPath string)
		toComplete string
		wantNames  []string
		wantErr    bool
		noLogger   bool
	}{
		{
			name: "success with multiple realms",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "alpha", "alpha-ns")
				createRealmMetadata(t, runPath, "bravo", "bravo-ns")
				createRealmMetadata(t, runPath, "charlie", "charlie-ns")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "success with prefix filter",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "alpha", "alpha-ns")
				createRealmMetadata(t, runPath, "bravo", "bravo-ns")
				createRealmMetadata(t, runPath, "charlie", "charlie-ns")
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with empty list",
			setup: func(_ *testing.T, _ string) {
				// No realms created
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "error when logger not in context",
			setup: func(_ *testing.T, _ string) {
				// No setup needed
			},
			toComplete: "",
			wantNames:  []string{},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			names, directive := config.CompleteRealmNames(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("CompleteRealmNames() directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteRealmNames() returned %d names, want %d: got %v, want %v",
					len(names), len(tt.wantNames), names, tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteRealmNames() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func setupTestCommand(t *testing.T, runPath string, noLogger bool) *cobra.Command {
	t.Helper()

	t.Cleanup(viper.Reset)
	viper.Reset()

	viper.Set(config.KUKEON_ROOT_RUN_PATH.ViperKey, runPath)
	viper.Set(config.KUKEON_ROOT_CONTAINERD_SOCKET.ViperKey, filepath.Join(runPath, "containerd.sock"))

	cmd := &cobra.Command{Use: "test"}

	var ctx context.Context
	if noLogger {
		ctx = context.Background()
	} else {
		logger := slog.New(slog.NewTextHandler(io.Discard, nil))
		ctx = context.WithValue(context.Background(), types.CtxLogger, logger)
	}
	cmd.SetContext(ctx)

	return cmd
}

func createRealmMetadata(t *testing.T, runPath, name, namespace string) {
	t.Helper()

	realmDir := fs.RealmMetadataDir(runPath, name)
	if err := os.MkdirAll(realmDir, 0o755); err != nil {
		t.Fatalf("Failed to create realm directory: %v", err)
	}

	doc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata: v1beta1.RealmMetadata{
			Name: name,
		},
		Spec: v1beta1.RealmSpec{
			Namespace: namespace,
		},
		Status: v1beta1.RealmStatus{
			State: v1beta1.RealmStateReady,
		},
	}

	metadataPath := filepath.Join(realmDir, consts.KukeonMetadataFile)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, &doc, metadataPath); err != nil {
		t.Fatalf("Failed to write realm metadata: %v", err)
	}
}

func TestCompleteSpaceNames(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, runPath string)
		flags      map[string]string
		toComplete string
		wantNames  []string
		wantErr    bool
		noLogger   bool
	}{
		{
			name: "success with multiple spaces using default realm",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "alpha")
				createSpaceMetadata(t, runPath, "default", "bravo")
				createSpaceMetadata(t, runPath, "default", "charlie")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "success with spaces across multiple realms (default realm only)",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createRealmMetadata(t, runPath, "realm2", "realm2-ns")
				createSpaceMetadata(t, runPath, "default", "alpha")
				createSpaceMetadata(t, runPath, "default", "bravo")
				createSpaceMetadata(t, runPath, "realm2", "charlie")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo"},
		},
		{
			name: "success with realm filter",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "realm1", "realm1-ns")
				createRealmMetadata(t, runPath, "realm2", "realm2-ns")
				createSpaceMetadata(t, runPath, "realm1", "alpha")
				createSpaceMetadata(t, runPath, "realm1", "bravo")
				createSpaceMetadata(t, runPath, "realm2", "charlie")
			},
			flags: map[string]string{
				"realm": "realm1",
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo"},
		},
		{
			name: "success with realm filter and prefix filter",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "realm1", "realm1-ns")
				createRealmMetadata(t, runPath, "realm2", "realm2-ns")
				createSpaceMetadata(t, runPath, "realm1", "alpha")
				createSpaceMetadata(t, runPath, "realm1", "bravo")
				createSpaceMetadata(t, runPath, "realm2", "charlie")
			},
			flags: map[string]string{
				"realm": "realm1",
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with prefix filter using default realm",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "alpha")
				createSpaceMetadata(t, runPath, "default", "bravo")
				createSpaceMetadata(t, runPath, "default", "charlie")
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with empty list",
			setup: func(_ *testing.T, _ string) {
				// No spaces created
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "error when logger not in context",
			setup: func(_ *testing.T, _ string) {
				// No setup needed
			},
			toComplete: "",
			wantNames:  []string{},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			// Set flags if provided
			for flagName, flagValue := range tt.flags {
				cmd.Flags().String(flagName, "", "")
				_ = cmd.Flags().Set(flagName, flagValue)
			}

			names, directive := config.CompleteSpaceNames(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("CompleteSpaceNames() directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteSpaceNames() returned %d names, want %d: got %v, want %v",
					len(names), len(tt.wantNames), names, tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteSpaceNames() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func createSpaceMetadata(t *testing.T, runPath, realmName, spaceName string) {
	t.Helper()

	// Ensure realm exists first
	realmDir := fs.RealmMetadataDir(runPath, realmName)
	if _, err := os.Stat(realmDir); os.IsNotExist(err) {
		// Create a minimal realm if it doesn't exist
		createRealmMetadata(t, runPath, realmName, realmName+"-ns")
	}

	spaceDir := fs.SpaceMetadataDir(runPath, realmName, spaceName)
	if err := os.MkdirAll(spaceDir, 0o755); err != nil {
		t.Fatalf("Failed to create space directory: %v", err)
	}

	doc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata: v1beta1.SpaceMetadata{
			Name: spaceName,
		},
		Spec: v1beta1.SpaceSpec{
			RealmID: realmName,
		},
		Status: v1beta1.SpaceStatus{
			State: v1beta1.SpaceStateReady,
		},
	}

	metadataPath := filepath.Join(spaceDir, consts.KukeonMetadataFile)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, &doc, metadataPath); err != nil {
		t.Fatalf("Failed to write space metadata: %v", err)
	}
}

func TestCompleteStackNames(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, runPath string)
		flags      map[string]string
		toComplete string
		wantNames  []string
		noLogger   bool
	}{
		{
			name: "success with multiple stacks using default realm and space",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "default")
				createStackMetadata(t, runPath, "default", "default", "alpha")
				createStackMetadata(t, runPath, "default", "default", "bravo")
				createStackMetadata(t, runPath, "default", "default", "charlie")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "success with stacks across multiple realms/spaces (default realm/space only)",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createRealmMetadata(t, runPath, "realm2", "realm2-ns")
				createSpaceMetadata(t, runPath, "default", "default")
				createSpaceMetadata(t, runPath, "realm2", "space2")
				createStackMetadata(t, runPath, "default", "default", "alpha")
				createStackMetadata(t, runPath, "default", "default", "bravo")
				createStackMetadata(t, runPath, "realm2", "space2", "charlie")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo"},
		},
		{
			name: "success with prefix filter using default realm and space",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "default")
				createStackMetadata(t, runPath, "default", "default", "alpha")
				createStackMetadata(t, runPath, "default", "default", "bravo")
				createStackMetadata(t, runPath, "default", "default", "charlie")
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with realm and space filter",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "realm1", "realm1-ns")
				createRealmMetadata(t, runPath, "realm2", "realm2-ns")
				createSpaceMetadata(t, runPath, "realm1", "space1")
				createSpaceMetadata(t, runPath, "realm2", "space1")
				createStackMetadata(t, runPath, "realm1", "space1", "alpha")
				createStackMetadata(t, runPath, "realm2", "space1", "bravo")
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
			},
			toComplete: "",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with empty list",
			setup: func(_ *testing.T, _ string) {
				// No stacks created
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "error when logger not in context",
			setup: func(_ *testing.T, _ string) {
				// No setup needed
			},
			toComplete: "",
			wantNames:  []string{},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			// Set flags if provided
			for flagName, flagValue := range tt.flags {
				cmd.Flags().String(flagName, "", "")
				_ = cmd.Flags().Set(flagName, flagValue)
			}

			names, directive := config.CompleteStackNames(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("CompleteStackNames() directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteStackNames() returned %d names, want %d: got %v, want %v",
					len(names), len(tt.wantNames), names, tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteStackNames() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func createStackMetadata(t *testing.T, runPath, realmName, spaceName, stackName string) {
	t.Helper()

	// Ensure realm and space exist first
	realmDir := fs.RealmMetadataDir(runPath, realmName)
	if _, err := os.Stat(realmDir); os.IsNotExist(err) {
		// Create a minimal realm if it doesn't exist
		createRealmMetadata(t, runPath, realmName, realmName+"-ns")
	}

	spaceDir := fs.SpaceMetadataDir(runPath, realmName, spaceName)
	if _, err := os.Stat(spaceDir); os.IsNotExist(err) {
		// Create a minimal space if it doesn't exist
		createSpaceMetadata(t, runPath, realmName, spaceName)
	}

	stackDir := fs.StackMetadataDir(runPath, realmName, spaceName, stackName)
	if err := os.MkdirAll(stackDir, 0o755); err != nil {
		t.Fatalf("Failed to create stack directory: %v", err)
	}

	doc := v1beta1.StackDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindStack,
		Metadata: v1beta1.StackMetadata{
			Name: stackName,
		},
		Spec: v1beta1.StackSpec{
			RealmID: realmName,
			SpaceID: spaceName,
		},
		Status: v1beta1.StackStatus{
			State: v1beta1.StackStateReady,
		},
	}

	metadataPath := filepath.Join(stackDir, consts.KukeonMetadataFile)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, &doc, metadataPath); err != nil {
		t.Fatalf("Failed to write stack metadata: %v", err)
	}
}

func createCellMetadata(t *testing.T, runPath, realmName, spaceName, stackName, cellName string) {
	t.Helper()

	// Ensure realm, space, and stack exist first
	realmDir := fs.RealmMetadataDir(runPath, realmName)
	if _, err := os.Stat(realmDir); os.IsNotExist(err) {
		createRealmMetadata(t, runPath, realmName, realmName+"-ns")
	}

	spaceDir := fs.SpaceMetadataDir(runPath, realmName, spaceName)
	if _, err := os.Stat(spaceDir); os.IsNotExist(err) {
		createSpaceMetadata(t, runPath, realmName, spaceName)
	}

	stackDir := fs.StackMetadataDir(runPath, realmName, spaceName, stackName)
	if _, err := os.Stat(stackDir); os.IsNotExist(err) {
		createStackMetadata(t, runPath, realmName, spaceName, stackName)
	}

	cellDir := fs.CellMetadataDir(runPath, realmName, spaceName, stackName, cellName)
	if err := os.MkdirAll(cellDir, 0o755); err != nil {
		t.Fatalf("Failed to create cell directory: %v", err)
	}

	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			ID:      cellName,
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
		},
		Status: v1beta1.CellStatus{
			State: v1beta1.CellStateReady,
		},
	}

	metadataPath := filepath.Join(cellDir, consts.KukeonMetadataFile)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, &doc, metadataPath); err != nil {
		t.Fatalf("Failed to write cell metadata: %v", err)
	}
}

func TestCompleteCellNames(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, runPath string)
		flags      map[string]string
		toComplete string
		wantNames  []string
		noLogger   bool
	}{
		{
			name: "success with multiple cells using default realm, space, and stack",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "default")
				createStackMetadata(t, runPath, "default", "default", "default")
				createCellMetadata(t, runPath, "default", "default", "default", "alpha")
				createCellMetadata(t, runPath, "default", "default", "default", "bravo")
				createCellMetadata(t, runPath, "default", "default", "default", "charlie")
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "success with prefix filter using default realm, space, and stack",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "default", "default-ns")
				createSpaceMetadata(t, runPath, "default", "default")
				createStackMetadata(t, runPath, "default", "default", "default")
				createCellMetadata(t, runPath, "default", "default", "default", "alpha")
				createCellMetadata(t, runPath, "default", "default", "default", "bravo")
				createCellMetadata(t, runPath, "default", "default", "default", "charlie")
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with realm, space, and stack filter",
			setup: func(t *testing.T, runPath string) {
				createRealmMetadata(t, runPath, "realm1", "realm1-ns")
				createSpaceMetadata(t, runPath, "realm1", "space1")
				createStackMetadata(t, runPath, "realm1", "space1", "stack1")
				createStackMetadata(t, runPath, "realm1", "space1", "stack2")
				createCellMetadata(t, runPath, "realm1", "space1", "stack1", "alpha")
				createCellMetadata(t, runPath, "realm1", "space1", "stack2", "bravo")
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
			},
			toComplete: "",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with empty list",
			setup: func(_ *testing.T, _ string) {
				// No cells created
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "error when logger not in context",
			setup: func(_ *testing.T, _ string) {
				// No setup needed
			},
			toComplete: "",
			wantNames:  []string{},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			// Set flags if provided
			for flagName, flagValue := range tt.flags {
				cmd.Flags().String(flagName, "", "")
				_ = cmd.Flags().Set(flagName, flagValue)
			}

			names, directive := config.CompleteCellNames(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("CompleteCellNames() directive = %v, want %v", directive, cobra.ShellCompDirectiveNoFileComp)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteCellNames() returned %d names, want %d: got %v, want %v",
					len(names),
					len(tt.wantNames),
					names,
					tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteCellNames() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func createCellWithContainers(
	t *testing.T,
	runPath, realmName, spaceName, stackName, cellName string,
	containerNames []string,
) {
	t.Helper()

	// Ensure realm, space, and stack exist first
	realmDir := fs.RealmMetadataDir(runPath, realmName)
	if _, err := os.Stat(realmDir); os.IsNotExist(err) {
		createRealmMetadata(t, runPath, realmName, realmName+"-ns")
	}

	spaceDir := fs.SpaceMetadataDir(runPath, realmName, spaceName)
	if _, err := os.Stat(spaceDir); os.IsNotExist(err) {
		createSpaceMetadata(t, runPath, realmName, spaceName)
	}

	stackDir := fs.StackMetadataDir(runPath, realmName, spaceName, stackName)
	if _, err := os.Stat(stackDir); os.IsNotExist(err) {
		createStackMetadata(t, runPath, realmName, spaceName, stackName)
	}

	cellDir := fs.CellMetadataDir(runPath, realmName, spaceName, stackName, cellName)
	if err := os.MkdirAll(cellDir, 0o755); err != nil {
		t.Fatalf("Failed to create cell directory: %v", err)
	}

	containers := make([]v1beta1.ContainerSpec, 0, len(containerNames))
	for _, containerName := range containerNames {
		containers = append(containers, v1beta1.ContainerSpec{
			ID:      containerName,
			RealmID: realmName,
			SpaceID: spaceName,
			StackID: stackName,
			CellID:  cellName,
			Image:   "docker.io/library/debian:latest",
		})
	}

	doc := v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name: cellName,
		},
		Spec: v1beta1.CellSpec{
			ID:         cellName,
			RealmID:    realmName,
			SpaceID:    spaceName,
			StackID:    stackName,
			Containers: containers,
		},
		Status: v1beta1.CellStatus{
			State: v1beta1.CellStateReady,
		},
	}

	metadataPath := filepath.Join(cellDir, consts.KukeonMetadataFile)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, &doc, metadataPath); err != nil {
		t.Fatalf("Failed to write cell metadata with containers: %v", err)
	}
}

func TestCompleteContainerNames(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, runPath string)
		flags      map[string]string
		toComplete string
		wantNames  []string
		noLogger   bool
	}{
		{
			name: "success with multiple containers",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(
					t,
					runPath,
					"realm1",
					"space1",
					"stack1",
					"cell1",
					[]string{"alpha", "bravo", "charlie"},
				)
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{"alpha", "bravo", "charlie"},
		},
		{
			name: "success with prefix filter",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(
					t,
					runPath,
					"realm1",
					"space1",
					"stack1",
					"cell1",
					[]string{"alpha", "bravo", "charlie"},
				)
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "a",
			wantNames:  []string{"alpha"},
		},
		{
			name: "success with empty list",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(t, runPath, "realm1", "space1", "stack1", "cell1", []string{})
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "returns empty when realm flag missing",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(t, runPath, "realm1", "space1", "stack1", "cell1", []string{"alpha"})
			},
			flags: map[string]string{
				"space": "space1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "returns empty when space flag missing",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(t, runPath, "realm1", "space1", "stack1", "cell1", []string{"alpha"})
			},
			flags: map[string]string{
				"realm": "realm1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "returns empty when stack flag missing",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(t, runPath, "realm1", "space1", "stack1", "cell1", []string{"alpha"})
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "returns empty when cell flag missing",
			setup: func(t *testing.T, runPath string) {
				createCellWithContainers(t, runPath, "realm1", "space1", "stack1", "cell1", []string{"alpha"})
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
			},
			toComplete: "",
			wantNames:  []string{},
		},
		{
			name: "error when logger not in context",
			setup: func(_ *testing.T, _ string) {
				// No setup needed
			},
			flags: map[string]string{
				"realm": "realm1",
				"space": "space1",
				"stack": "stack1",
				"cell":  "cell1",
			},
			toComplete: "",
			wantNames:  []string{},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			if tt.setup != nil {
				tt.setup(t, runPath)
			}

			// Set flags if provided
			for flagName, flagValue := range tt.flags {
				cmd.Flags().String(flagName, "", "")
				_ = cmd.Flags().Set(flagName, flagValue)
			}

			names, directive := config.CompleteContainerNames(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf(
					"CompleteContainerNames() directive = %v, want %v",
					directive,
					cobra.ShellCompDirectiveNoFileComp,
				)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteContainerNames() returned %d names, want %d: got %v, want %v",
					len(names),
					len(tt.wantNames),
					names,
					tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteContainerNames() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}

func TestCompleteOutputFormat(t *testing.T) {
	tests := []struct {
		name       string
		toComplete string
		wantNames  []string
		noLogger   bool
	}{
		{
			name:       "success with all formats",
			toComplete: "",
			wantNames:  []string{"yaml", "json", "table"},
		},
		{
			name:       "success with prefix filter 'y'",
			toComplete: "y",
			wantNames:  []string{"yaml"},
		},
		{
			name:       "success with prefix filter 'j'",
			toComplete: "j",
			wantNames:  []string{"json"},
		},
		{
			name:       "success with prefix filter 't'",
			toComplete: "t",
			wantNames:  []string{"table"},
		},
		{
			name:       "success with prefix filter 'ya'",
			toComplete: "ya",
			wantNames:  []string{"yaml"},
		},
		{
			name:       "success with prefix filter 'tab'",
			toComplete: "tab",
			wantNames:  []string{"table"},
		},
		{
			name:       "success with no matches",
			toComplete: "x",
			wantNames:  []string{},
		},
		{
			name:       "works without logger in context",
			toComplete: "",
			wantNames:  []string{"yaml", "json", "table"},
			noLogger:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runPath := t.TempDir()
			cmd := setupTestCommand(t, runPath, tt.noLogger)

			names, directive := config.CompleteOutputFormat(cmd, []string{}, tt.toComplete)

			if directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf(
					"CompleteOutputFormat() directive = %v, want %v",
					directive,
					cobra.ShellCompDirectiveNoFileComp,
				)
			}

			sort.Strings(names)
			sort.Strings(tt.wantNames)

			if len(names) != len(tt.wantNames) {
				t.Errorf(
					"CompleteOutputFormat() returned %d names, want %d: got %v, want %v",
					len(names), len(tt.wantNames), names, tt.wantNames,
				)
				return
			}

			for i, name := range names {
				if name != tt.wantNames[i] {
					t.Errorf("CompleteOutputFormat() names[%d] = %q, want %q", i, name, tt.wantNames[i])
				}
			}
		})
	}
}
