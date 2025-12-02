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
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// GetContainerResult is an alias for controller.GetContainerResult
type GetContainerResult = controller.GetContainerResult

type ContainerController interface {
	GetContainer(container intmodel.Container) (GetContainerResult, error)
	ListContainers(realmName, spaceName, stackName, cellName string) ([]intmodel.ContainerSpec, error)
}

type containerController = ContainerController // internal alias for backward compatibility

// MockControllerKey is used to inject mock controllers in tests via context.
type MockControllerKey struct{}

type (
	printObjectFunc  func(interface{}) error
	tablePrinterFunc func(*cobra.Command, []string, [][]string)
)

var (
	// YAMLPrinter is exported for testing.
	YAMLPrinter printObjectFunc = shared.PrintYAML
	// JSONPrinter is exported for testing.
	JSONPrinter printObjectFunc = shared.PrintJSON
	// TablePrinter is exported for testing.
	TablePrinter tablePrinterFunc = shared.PrintTable
)

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"containers", "co"},
		Short:         "Get or list container information",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runContainerCmd,
	}

	cmd.Flags().String("realm", "", "Filter containers by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Filter containers by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Filter containers by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("cell", "", "Filter containers by cell name")
	_ = viper.BindPFlag(config.KUKE_GET_CONTAINER_CELL.ViperKey, cmd.Flags().Lookup("cell"))

	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	// Register autocomplete for positional argument
	cmd.ValidArgsFunction = config.CompleteContainerNames

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func runContainerCmd(cmd *cobra.Command, args []string) error {
	return runContainerCmdWithDeps(
		cmd,
		args,
		YAMLPrinter,
		JSONPrinter,
		TablePrinter,
	)
}

func runContainerCmdWithDeps(
	cmd *cobra.Command,
	args []string,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
	printTable tablePrinterFunc,
) error {
	var ctrl containerController
	if mockCtrl, ok := cmd.Context().Value(MockControllerKey{}).(ContainerController); ok {
		ctrl = mockCtrl
	} else {
		realCtrl, err := shared.ControllerFromCmd(cmd)
		if err != nil {
			return err
		}
		ctrl = &controllerWrapper{ctrl: realCtrl}
	}

	outputFormat, err := shared.ParseOutputFormat(cmd)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_REALM.ViperKey))
	if realm == "" {
		realm, _ = cmd.Flags().GetString("realm")
		realm = strings.TrimSpace(realm)
	}
	if realm == "" {
		realm = config.KUKE_GET_CONTAINER_REALM.ValueOrDefault()
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_SPACE.ViperKey))
	if space == "" {
		space, _ = cmd.Flags().GetString("space")
		space = strings.TrimSpace(space)
	}
	if space == "" {
		space = config.KUKE_GET_CONTAINER_SPACE.ValueOrDefault()
	}

	stack := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_STACK.ViperKey))
	if stack == "" {
		stack, _ = cmd.Flags().GetString("stack")
		stack = strings.TrimSpace(stack)
	}
	if stack == "" {
		stack = config.KUKE_GET_CONTAINER_STACK.ValueOrDefault()
	}

	cell := strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_CELL.ViperKey))
	if cell == "" {
		cell, _ = cmd.Flags().GetString("cell")
		cell = strings.TrimSpace(cell)
	}

	var name string
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	} else {
		name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_NAME.ViperKey))
	}

	if name != "" {
		// Get single container (requires realm, space, stack, and cell)
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

		// Construct ContainerDoc from command args/flags
		containerDoc := &v1beta1.ContainerDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindContainer,
			Metadata: v1beta1.ContainerMetadata{
				Name:   name,
				Labels: make(map[string]string),
			},
			Spec: v1beta1.ContainerSpec{
				ID:      name,
				RealmID: realm,
				SpaceID: space,
				StackID: stack,
				CellID:  cell,
			},
		}

		// Convert at boundary before calling controller
		var containerInternal intmodel.Container
		containerInternal, _, err = apischeme.NormalizeContainer(*containerDoc)
		if err != nil {
			return fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
		}

		var result controller.GetContainerResult
		result, err = ctrl.GetContainer(containerInternal)
		if err != nil {
			return err
		}

		if !result.ContainerExists {
			return fmt.Errorf("container %q not found", name)
		}

		// Convert result back to external for printing
		containerDoc, err = fs.ConvertContainerToExternal(result.Container)
		if err != nil {
			return err
		}

		return printContainer(cmd, containerDoc, outputFormat, printYAML, printJSON)
	}

	// List containers (optionally filtered by realm, space, stack, and/or cell)
	internalContainers, err := ctrl.ListContainers(realm, space, stack, cell)
	if err != nil {
		return err
	}

	// Query state for each container by calling GetContainer
	// This ensures we get the actual state from containerd
	// We'll store containers with their state in a map for lookup during printing
	containerStates := make(map[string]string) // key: container ID, value: state string
	containersWithState := make([]*v1beta1.ContainerSpec, 0, len(internalContainers))

	for _, containerSpec := range internalContainers {
		// Build a Container from the spec to query state
		// Ensure all required fields are set (they should already be in the spec from ListContainers)
		containerInternal := intmodel.Container{
			Metadata: intmodel.ContainerMetadata{
				Name: containerSpec.ID,
			},
			Spec: containerSpec,
		}

		// Ensure required fields are set from command flags if not in spec
		if containerInternal.Spec.RealmName == "" {
			containerInternal.Spec.RealmName = realm
		}
		if containerInternal.Spec.SpaceName == "" {
			containerInternal.Spec.SpaceName = space
		}
		if containerInternal.Spec.StackName == "" {
			containerInternal.Spec.StackName = stack
		}
		if containerInternal.Spec.CellName == "" {
			containerInternal.Spec.CellName = cell
		}

		// Get full container with state
		var result controller.GetContainerResult
		result, err = ctrl.GetContainer(containerInternal)
		if err != nil {
			// If we can't get state, use Unknown and continue
			// Log the error for debugging (this should be visible in verbose mode)
			cmd.PrintErrln("Warning: failed to get container state for", containerSpec.ID, ":", err)
			containerStates[containerSpec.ID] = "Unknown"
		} else {
			// Convert state to string for display
			// Convert internal state to external state first
			externalState := convertInternalStateToExternal(result.Container.Status.State)
			stateStr := containerStateToString(externalState)
			containerStates[containerSpec.ID] = stateStr
		}

		// Convert spec to external (state will be added during printing)
		// Use the conversion function from apischeme
		externalSpec := apischeme.BuildContainerSpecExternalFromInternal(containerSpec)
		containersWithState = append(containersWithState, &externalSpec)
	}

	return printContainersWithState(
		cmd,
		containersWithState,
		containerStates,
		outputFormat,
		printYAML,
		printJSON,
		printTable,
	)
}

func printContainer(
	_ *cobra.Command,
	container interface{},
	format shared.OutputFormat,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
) error {
	switch format {
	case shared.OutputFormatYAML:
		return printYAML(container)
	case shared.OutputFormatJSON:
		return printJSON(container)
	case shared.OutputFormatTable:
		// For single resource, show full YAML by default
		return printYAML(container)
	default:
		return printYAML(container)
	}
}

func printContainers(
	cmd *cobra.Command,
	containers []*v1beta1.ContainerSpec,
	format shared.OutputFormat,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
	printTable tablePrinterFunc,
) error {
	return printContainersWithState(cmd, containers, nil, format, printYAML, printJSON, printTable)
}

func printContainersWithState(
	cmd *cobra.Command,
	containers []*v1beta1.ContainerSpec,
	containerStates map[string]string,
	format shared.OutputFormat,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
	printTable tablePrinterFunc,
) error {
	switch format {
	case shared.OutputFormatYAML:
		return printYAML(containers)
	case shared.OutputFormatJSON:
		return printJSON(containers)
	case shared.OutputFormatTable:
		if len(containers) == 0 {
			cmd.Println("No containers found.")
			return nil
		}

		headers := []string{"NAME", "REALM", "SPACE", "STACK", "CELL", "ROOT", "IMAGE", "STATE"}
		rows := make([][]string, 0, len(containers))

		for _, c := range containers {
			containerName := containerDisplayName(c)

			// Get state from map if available, otherwise use Unknown
			state := "Unknown"
			if containerStates != nil {
				if s, ok := containerStates[c.ID]; ok {
					state = s
				}
			}

			rows = append(rows, []string{
				containerName,
				c.RealmID,
				c.SpaceID,
				c.StackID,
				c.CellID,
				strconv.FormatBool(c.Root),
				c.Image,
				state,
			})
		}

		printTable(cmd, headers, rows)
		return nil
	default:
		return printYAML(containers)
	}
}

func convertInternalStateToExternal(state intmodel.ContainerState) v1beta1.ContainerState {
	switch state {
	case intmodel.ContainerStatePending:
		return v1beta1.ContainerStatePending
	case intmodel.ContainerStateReady:
		return v1beta1.ContainerStateReady
	case intmodel.ContainerStateStopped:
		return v1beta1.ContainerStateStopped
	case intmodel.ContainerStatePaused:
		return v1beta1.ContainerStatePaused
	case intmodel.ContainerStatePausing:
		return v1beta1.ContainerStatePausing
	case intmodel.ContainerStateFailed:
		return v1beta1.ContainerStateFailed
	case intmodel.ContainerStateUnknown:
		return v1beta1.ContainerStateUnknown
	default:
		return v1beta1.ContainerStateUnknown
	}
}

func containerStateToString(state v1beta1.ContainerState) string {
	switch state {
	case v1beta1.ContainerStatePending:
		return "Pending"
	case v1beta1.ContainerStateReady:
		return "Ready"
	case v1beta1.ContainerStateStopped:
		return "Stopped"
	case v1beta1.ContainerStatePaused:
		return "Paused"
	case v1beta1.ContainerStatePausing:
		return "Pausing"
	case v1beta1.ContainerStateFailed:
		return "Failed"
	case v1beta1.ContainerStateUnknown:
		return "Unknown"
	default:
		return "Unknown"
	}
}

func containerDisplayName(c *v1beta1.ContainerSpec) string {
	if c == nil {
		return ""
	}
	if c.Root {
		return "root"
	}
	id := strings.TrimSpace(c.ID)
	if id == "" {
		return "-"
	}
	return id
}

type controllerWrapper struct {
	ctrl *controller.Exec
}

func (w *controllerWrapper) GetContainer(
	container intmodel.Container,
) (GetContainerResult, error) {
	return w.ctrl.GetContainer(container)
}

func (w *controllerWrapper) ListContainers(
	realmName, spaceName, stackName, cellName string,
) ([]intmodel.ContainerSpec, error) {
	return w.ctrl.ListContainers(realmName, spaceName, stackName, cellName)
}
