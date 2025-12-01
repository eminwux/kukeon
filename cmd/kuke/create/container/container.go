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

package container

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type containerController interface {
	CreateContainer(container intmodel.Container) (controller.CreateContainerResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"co"},
		Short:         "Create a new container inside a cell",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name, err := shared.RequireNameArgOrDefault(
				cmd,
				args,
				"container",
				viper.GetString(config.KUKE_CREATE_CONTAINER_NAME.ViperKey),
			)
			if err != nil {
				return err
			}

			realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_REALM.ViperKey))
			if realm == "" {
				realm = config.KUKE_CREATE_CONTAINER_REALM.ValueOrDefault()
			}

			space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_SPACE.ViperKey))
			if space == "" {
				space = config.KUKE_CREATE_CONTAINER_SPACE.ValueOrDefault()
			}

			stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_STACK.ViperKey))
			if stack == "" {
				stack = config.KUKE_CREATE_CONTAINER_STACK.ValueOrDefault()
			}

			cell := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_CELL.ViperKey))
			if cell == "" {
				return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
			}

			image := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_IMAGE.ViperKey))
			if image == "" {
				image = "docker.io/library/debian:latest"
			} else {
				// Normalize image reference to fully qualified format
				image = ctr.NormalizeImageReference(image)
			}

			command, err := cmd.Flags().GetString("command")
			if err != nil {
				return err
			}

			argsList, err := cmd.Flags().GetStringArray("args")
			if err != nil {
				return err
			}

			containerDoc := &v1beta1.ContainerDoc{
				Metadata: v1beta1.ContainerMetadata{
					Name: name,
				},
				Spec: v1beta1.ContainerSpec{
					ID:      name,
					RealmID: realm,
					SpaceID: space,
					StackID: stack,
					CellID:  cell,
					Image:   image,
					Command: command,
					Args:    argsList,
				},
			}

			// Ensure all nested structs are initialized
			containerDoc = v1beta1.NewContainerDoc(containerDoc)

			// Convert at boundary before calling controller
			containerInternal, version, err := apischeme.NormalizeContainer(*containerDoc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			// Check for mock controller in context (for testing)
			var ctrl containerController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(containerController); ok {
				ctrl = mockCtrl
			} else {
				var realCtrl *controller.Exec
				realCtrl, err = shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = realCtrl
			}

			// Call controller with internal type
			createResult, err := ctrl.CreateContainer(containerInternal)
			if err != nil {
				return err
			}

			printContainerResult(cmd, createResult, version)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Cell that owns the container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	cmd.Flags().String("image", "docker.io/library/debian:latest", "Container image to use")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_IMAGE.ViperKey, cmd.Flags().Lookup("image"))

	cmd.Flags().String("command", "", "Command to run in the container")
	cmd.Flags().StringArray("args", []string{}, "Arguments to pass to the command")

	// Register autocomplete functions for flags and positional argument
	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

func printContainerResult(cmd *cobra.Command, result controller.CreateContainerResult, version v1beta1.Version) {
	// Convert result back to external for output
	resultDoc, err := apischeme.BuildContainerExternalFromInternal(result.Container, version)
	if err != nil {
		// Fallback to internal type if conversion fails
		cmd.Printf(
			"Container %q (ID: %q) in cell %q (realm %q, space %q, stack %q)\n",
			result.Container.Metadata.Name,
			result.Container.Spec.ID,
			result.Container.Spec.CellName,
			result.Container.Spec.RealmName,
			result.Container.Spec.SpaceName,
			result.Container.Spec.StackName,
		)
		cmd.Printf("Warning: failed to convert result for output: %v\n", err)
	} else {
		cmd.Printf(
			"Container %q (ID: %q) in cell %q (realm %q, space %q, stack %q)\n",
			resultDoc.Metadata.Name,
			resultDoc.Spec.ID,
			resultDoc.Spec.CellID,
			resultDoc.Spec.RealmID,
			resultDoc.Spec.SpaceID,
			resultDoc.Spec.StackID,
		)
	}
	shared.PrintCreationOutcome(cmd, "container", result.ContainerExistsPost, result.ContainerCreated)
	if result.Started {
		cmd.Println("  - container: started")
	} else {
		cmd.Println("  - container: not started")
	}
}

// PrintContainerResult is exported for testing purposes.
func PrintContainerResult(cmd *cobra.Command, result controller.CreateContainerResult, version v1beta1.Version) {
	printContainerResult(cmd, result, version)
}
