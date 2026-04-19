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
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "container [name]",
		Aliases:       []string{"co"},
		Short:         "Create a new container inside a cell",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateContainer,
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

	cmd.Flags().StringArray("env", []string{}, "Environment variable in KEY=VALUE form (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_ENV.ViperKey, cmd.Flags().Lookup("env"))

	cmd.Flags().StringArray("port", []string{}, "Port mapping (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_PORTS.ViperKey, cmd.Flags().Lookup("port"))

	cmd.Flags().StringArray("volume", []string{}, "Volume mount (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_VOLUMES.ViperKey, cmd.Flags().Lookup("volume"))

	cmd.Flags().StringArray("network", []string{}, "Network to attach (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_NETWORKS.ViperKey, cmd.Flags().Lookup("network"))

	cmd.Flags().StringArray("network-alias", []string{}, "Network alias (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_NETWORK_ALIASES.ViperKey, cmd.Flags().Lookup("network-alias"))

	cmd.Flags().Bool("privileged", false, "Run the container in privileged mode")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_PRIVILEGED.ViperKey, cmd.Flags().Lookup("privileged"))

	cmd.Flags().Bool("root", false, "Run the container as a root cgroup container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_ROOT.ViperKey, cmd.Flags().Lookup("root"))

	cmd.Flags().String("cni-config-path", "", "Path to the CNI configuration directory")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_CNI_CONFIG_PATH.ViperKey, cmd.Flags().Lookup("cni-config-path"))

	cmd.Flags().String("restart-policy", "", "Restart policy for the container")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_RESTART_POLICY.ViperKey, cmd.Flags().Lookup("restart-policy"))

	cmd.Flags().StringArray("label", []string{}, "Metadata label in KEY=VALUE form (repeatable)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_LABELS.ViperKey, cmd.Flags().Lookup("label"))

	cmd.ValidArgsFunction = config.CompleteContainerNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("cell", config.CompleteCellNames)

	return cmd
}

func runCreateContainer(cmd *cobra.Command, args []string) error {
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
		realm = strings.TrimSpace(config.KUKE_CREATE_CONTAINER_REALM.ValueOrDefault())
	}

	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_SPACE.ViperKey))
	if space == "" {
		space = strings.TrimSpace(config.KUKE_CREATE_CONTAINER_SPACE.ValueOrDefault())
	}

	stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_STACK.ViperKey))
	if stack == "" {
		stack = strings.TrimSpace(config.KUKE_CREATE_CONTAINER_STACK.ValueOrDefault())
	}

	cell := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_CELL.ViperKey))
	if cell == "" {
		return fmt.Errorf("%w (--cell)", errdefs.ErrCellNameRequired)
	}

	image := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_IMAGE.ViperKey))
	if image == "" {
		image = "docker.io/library/debian:latest"
	} else {
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
	envList, err := cmd.Flags().GetStringArray("env")
	if err != nil {
		return err
	}
	portsList, err := cmd.Flags().GetStringArray("port")
	if err != nil {
		return err
	}
	volumesList, err := cmd.Flags().GetStringArray("volume")
	if err != nil {
		return err
	}
	networksList, err := cmd.Flags().GetStringArray("network")
	if err != nil {
		return err
	}
	networkAliasesList, err := cmd.Flags().GetStringArray("network-alias")
	if err != nil {
		return err
	}
	labelsList, err := cmd.Flags().GetStringArray("label")
	if err != nil {
		return err
	}
	labels, err := parseLabels(labelsList)
	if err != nil {
		return err
	}

	privileged := viper.GetBool(config.KUKE_CREATE_CONTAINER_PRIVILEGED.ViperKey)
	root := viper.GetBool(config.KUKE_CREATE_CONTAINER_ROOT.ViperKey)
	cniConfigPath := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_CNI_CONFIG_PATH.ViperKey))
	restartPolicy := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_RESTART_POLICY.ViperKey))

	doc := v1beta1.NewContainerDoc(&v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.ContainerSpec{
			ID:              name,
			RealmID:         realm,
			SpaceID:         space,
			StackID:         stack,
			CellID:          cell,
			Root:            root,
			Image:           image,
			Command:         command,
			Args:            argsList,
			Env:             envList,
			Ports:           portsList,
			Volumes:         volumesList,
			Networks:        networksList,
			NetworksAliases: networkAliasesList,
			Privileged:      privileged,
			CNIConfigPath:   cniConfigPath,
			RestartPolicy:   restartPolicy,
		},
	})

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	result, err := client.CreateContainer(cmd.Context(), *doc)
	if err != nil {
		return err
	}

	printContainerResult(cmd, result)
	return nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printContainerResult(cmd *cobra.Command, result kukeonv1.CreateContainerResult) {
	cmd.Printf(
		"Container %q (ID: %q) in cell %q (realm %q, space %q, stack %q)\n",
		result.Container.Metadata.Name,
		result.Container.Spec.ID,
		result.Container.Spec.CellID,
		result.Container.Spec.RealmID,
		result.Container.Spec.SpaceID,
		result.Container.Spec.StackID,
	)
	shared.PrintCreationOutcome(cmd, "container", result.ContainerExistsPost, result.ContainerCreated)
	if result.Started {
		cmd.Println("  - container: started")
	} else {
		cmd.Println("  - container: not started")
	}
}

// PrintContainerResult is exported for testing purposes.
func PrintContainerResult(cmd *cobra.Command, result kukeonv1.CreateContainerResult) {
	printContainerResult(cmd, result)
}

func parseLabels(entries []string) (map[string]string, error) {
	labels := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("invalid label %q: expected KEY=VALUE", entry)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid label %q: key must not be empty", entry)
		}
		labels[key] = value
	}
	return labels, nil
}
