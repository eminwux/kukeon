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
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
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

	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func runContainerCmd(cmd *cobra.Command, args []string) error {
	return runContainerCmdWithDeps(cmd, args, YAMLPrinter, JSONPrinter, TablePrinter)
}

func runContainerCmdWithDeps(
	cmd *cobra.Command,
	args []string,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
	printTable tablePrinterFunc,
) error {
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	outputFormat, err := shared.ParseOutputFormat(cmd)
	if err != nil {
		return err
	}

	realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_CONTAINER_REALM.ViperKey)
	space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_CONTAINER_SPACE.ViperKey)
	stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_CONTAINER_STACK.ViperKey)
	cell := shared.ExplicitFlag(cmd, "cell", config.KUKE_GET_CONTAINER_CELL.ViperKey)

	var name string
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	} else {
		name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CONTAINER_NAME.ViperKey))
	}

	if name != "" {
		if realm == "" {
			realm = strings.TrimSpace(config.KUKE_GET_CONTAINER_REALM.ValueOrDefault())
		}
		if space == "" {
			space = strings.TrimSpace(config.KUKE_GET_CONTAINER_SPACE.ValueOrDefault())
		}
		if stack == "" {
			stack = strings.TrimSpace(config.KUKE_GET_CONTAINER_STACK.ValueOrDefault())
		}
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

		doc := v1beta1.ContainerDoc{
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

		result, err := client.GetContainer(cmd.Context(), doc)
		if err != nil {
			if errors.Is(err, errdefs.ErrContainerNotFound) {
				return fmt.Errorf("container %q not found", name)
			}
			return err
		}
		if !result.ContainerExists {
			return fmt.Errorf("container %q not found", name)
		}

		return printContainer(&result.Container, outputFormat, printYAML, printJSON)
	}

	// List path — query each container's state by calling GetContainer.
	specs, err := client.ListContainers(cmd.Context(), realm, space, stack, cell)
	if err != nil {
		return err
	}

	containerStates := make(map[string]string, len(specs))
	for i := range specs {
		spec := specs[i]
		if spec.RealmID == "" {
			spec.RealmID = realm
		}
		if spec.SpaceID == "" {
			spec.SpaceID = space
		}
		if spec.StackID == "" {
			spec.StackID = stack
		}
		if spec.CellID == "" {
			spec.CellID = cell
		}
		probe := v1beta1.ContainerDoc{
			Metadata: v1beta1.ContainerMetadata{Name: spec.ID},
			Spec:     spec,
		}
		probeResult, err := client.GetContainer(cmd.Context(), probe)
		if err != nil {
			cmd.PrintErrln("Warning: failed to get container state for", spec.ID, ":", err)
			containerStates[spec.ID] = "Unknown"
			continue
		}
		containerStates[spec.ID] = containerStateToString(probeResult.Container.Status.State)
	}

	return printContainersWithState(cmd, specs, containerStates, outputFormat, printYAML, printJSON, printTable)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printContainer(
	container interface{},
	format shared.OutputFormat,
	printYAML printObjectFunc,
	printJSON printObjectFunc,
) error {
	switch format {
	case shared.OutputFormatJSON:
		return printJSON(container)
	default:
		return printYAML(container)
	}
}

func printContainersWithState(
	cmd *cobra.Command,
	containers []v1beta1.ContainerSpec,
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
		for i := range containers {
			c := &containers[i]
			state := "Unknown"
			if containerStates != nil {
				if s, ok := containerStates[c.ID]; ok {
					state = s
				}
			}
			rows = append(rows, []string{
				containerDisplayName(c),
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
