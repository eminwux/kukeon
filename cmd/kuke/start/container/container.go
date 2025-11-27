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
	"github.com/eminwux/kukeon/cmd/kuke/start/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// StartContainerResult exposes the started container doc plus controller booleans.
type StartContainerResult struct {
	Container intmodel.Container
	Started   bool
}

type containerController interface {
	StartContainer(container intmodel.Container) (controller.StartContainerResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"co"},
		Short:         "Start a container",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Check for mock controller in context (for testing)
			var ctrl containerController
			if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(containerController); ok {
				ctrl = mockCtrl
			} else {
				realCtrl, err := shared.ControllerFromCmd(cmd)
				if err != nil {
					return err
				}
				ctrl = &controllerWrapper{ctrl: realCtrl}
			}

			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_START_CONTAINER_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_START_CONTAINER_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_START_CONTAINER_STACK.ViperKey))
			cell := strings.TrimSpace(viper.GetString(config.KUKE_START_CONTAINER_CELL.ViperKey))

			if realm == "" {
				return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
			}
			if space == "" {
				return fmt.Errorf("%w (--space)", errdefs.ErrSpaceNameRequired)
			}
			if stack == "" {
				return fmt.Errorf("%w (--stack)", errdefs.ErrStackNameRequired)
			}
			if cell == "" {
				return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
			}

			containerDoc := newContainerDoc(name, realm, space, stack, cell)

			// Convert at boundary before calling controller
			containerInternal, _, err := apischeme.NormalizeContainer(*containerDoc)
			if err != nil {
				return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
			}

			result, err := ctrl.StartContainer(containerInternal)
			if err != nil {
				return err
			}

			// Use container from result for output
			containerName := result.Container.Metadata.Name
			if containerName == "" {
				containerName = result.Container.Spec.ID
			}
			if containerName == "" {
				containerName = name
			}
			cellName := result.Container.Spec.CellName
			if cellName == "" {
				cellName = cell
			}

			cmd.Printf(
				"Started container %q from cell %q\n",
				containerName,
				cellName,
			)
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the container")
	_ = viper.BindPFlag(config.KUKE_START_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the container")
	_ = viper.BindPFlag(config.KUKE_START_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the container")
	_ = viper.BindPFlag(config.KUKE_START_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Cell that owns the container")
	_ = viper.BindPFlag(config.KUKE_START_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	// Register autocomplete functions for flags and positional argument
	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

func newContainerDoc(name, realm, space, stack, cell string) *v1beta1.ContainerDoc {
	return &v1beta1.ContainerDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindContainer,
		Metadata: v1beta1.ContainerMetadata{
			Name:   strings.TrimSpace(name),
			Labels: make(map[string]string),
		},
		Spec: v1beta1.ContainerSpec{
			ID:      strings.TrimSpace(name),
			RealmID: strings.TrimSpace(realm),
			SpaceID: strings.TrimSpace(space),
			StackID: strings.TrimSpace(stack),
			CellID:  strings.TrimSpace(cell),
		},
	}
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) StartContainer(container intmodel.Container) (controller.StartContainerResult, error) {
	return w.ctrl.StartContainer(container)
}
