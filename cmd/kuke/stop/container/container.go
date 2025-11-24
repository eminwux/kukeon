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
	"github.com/eminwux/kukeon/cmd/kuke/stop/shared"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type containerController interface {
	StopContainer(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error)
}

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"co"},
		Short:         "Stop a container",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			realm := strings.TrimSpace(viper.GetString(config.KUKE_STOP_CONTAINER_REALM.ViperKey))
			space := strings.TrimSpace(viper.GetString(config.KUKE_STOP_CONTAINER_SPACE.ViperKey))
			stack := strings.TrimSpace(viper.GetString(config.KUKE_STOP_CONTAINER_STACK.ViperKey))
			cell := strings.TrimSpace(viper.GetString(config.KUKE_STOP_CONTAINER_CELL.ViperKey))

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

			containerDoc := newContainerDoc(name, realm, space, stack, cell)

			result, err := ctrl.StopContainer(containerDoc)
			if err != nil {
				return err
			}

			printDoc := containerDoc
			stopped := true
			if result != (controller.StopContainerResult{}) {
				if result.ContainerDoc != nil {
					printDoc = result.ContainerDoc
				}
				stopped = result.Stopped
			}

			containerName := containerNameFromDoc(printDoc, name)
			cellName := cellNameFromDoc(printDoc, cell)

			if stopped {
				cmd.Printf("Stopped container %q from cell %q\n", containerName, cellName)
			} else {
				cmd.Printf("Container %q was already stopped in cell %q\n", containerName, cellName)
			}
			return nil
		},
	}

	cmd.Flags().String("realm", "", "Realm that owns the container")
	_ = viper.BindPFlag(config.KUKE_STOP_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the container")
	_ = viper.BindPFlag(config.KUKE_STOP_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the container")
	_ = viper.BindPFlag(config.KUKE_STOP_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Cell that owns the container")
	_ = viper.BindPFlag(config.KUKE_STOP_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	// Register autocomplete functions for flags and positional argument
	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) StopContainer(doc *v1beta1.ContainerDoc) (controller.StopContainerResult, error) {
	if doc == nil {
		return controller.StopContainerResult{}, errdefs.ErrContainerNameRequired
	}

	fillDocDefaults(
		doc,
		strings.TrimSpace(doc.Metadata.Name),
		strings.TrimSpace(doc.Spec.RealmID),
		strings.TrimSpace(doc.Spec.SpaceID),
		strings.TrimSpace(doc.Spec.StackID),
		strings.TrimSpace(doc.Spec.CellID),
	)

	name := strings.TrimSpace(doc.Metadata.Name)
	if name == "" {
		return controller.StopContainerResult{}, errdefs.ErrContainerNameRequired
	}
	realm := strings.TrimSpace(doc.Spec.RealmID)
	if realm == "" {
		return controller.StopContainerResult{}, errdefs.ErrRealmNameRequired
	}
	space := strings.TrimSpace(doc.Spec.SpaceID)
	if space == "" {
		return controller.StopContainerResult{}, errdefs.ErrSpaceNameRequired
	}
	stack := strings.TrimSpace(doc.Spec.StackID)
	if stack == "" {
		return controller.StopContainerResult{}, errdefs.ErrStackNameRequired
	}
	cell := strings.TrimSpace(doc.Spec.CellID)
	if cell == "" {
		return controller.StopContainerResult{}, errdefs.ErrCellNameRequired
	}

	res, err := w.ctrl.StopContainer(doc)
	if err != nil {
		return controller.StopContainerResult{}, err
	}

	if res.ContainerDoc == nil {
		res.ContainerDoc = newContainerDoc(name, realm, space, stack, cell)
	} else {
		fillDocDefaults(res.ContainerDoc, name, realm, space, stack, cell)
	}

	return res, nil
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

func fillDocDefaults(doc *v1beta1.ContainerDoc, name, realm, space, stack, cell string) {
	if doc == nil {
		return
	}
	if doc.APIVersion == "" {
		doc.APIVersion = v1beta1.APIVersionV1Beta1
	}
	if doc.Kind == "" {
		doc.Kind = v1beta1.KindContainer
	}
	if strings.TrimSpace(doc.Metadata.Name) == "" {
		doc.Metadata.Name = strings.TrimSpace(name)
	}
	if strings.TrimSpace(doc.Spec.ID) == "" {
		doc.Spec.ID = strings.TrimSpace(name)
	}
	if strings.TrimSpace(doc.Spec.RealmID) == "" {
		doc.Spec.RealmID = strings.TrimSpace(realm)
	}
	if strings.TrimSpace(doc.Spec.SpaceID) == "" {
		doc.Spec.SpaceID = strings.TrimSpace(space)
	}
	if strings.TrimSpace(doc.Spec.StackID) == "" {
		doc.Spec.StackID = strings.TrimSpace(stack)
	}
	if strings.TrimSpace(doc.Spec.CellID) == "" {
		doc.Spec.CellID = strings.TrimSpace(cell)
	}
	if doc.Metadata.Labels == nil {
		doc.Metadata.Labels = make(map[string]string)
	}
}

func containerNameFromDoc(doc *v1beta1.ContainerDoc, defaultName string) string {
	if doc != nil {
		if trimmed := strings.TrimSpace(doc.Metadata.Name); trimmed != "" {
			return trimmed
		}
		if trimmed := strings.TrimSpace(doc.Spec.ID); trimmed != "" {
			return trimmed
		}
	}
	return defaultName
}

func cellNameFromDoc(doc *v1beta1.ContainerDoc, defaultCell string) string {
	if doc != nil {
		if trimmed := strings.TrimSpace(doc.Spec.CellID); trimmed != "" {
			return trimmed
		}
	}
	return defaultCell
}
