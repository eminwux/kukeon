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

	cmd.Flags().String("user", "", `Run the container as UID[:GID] (e.g. "1000:1000")`)
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_USER.ViperKey, cmd.Flags().Lookup("user"))

	cmd.Flags().Bool("read-only", false, "Mount the container's root filesystem read-only")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_READ_ONLY.ViperKey, cmd.Flags().Lookup("read-only"))

	cmd.Flags().StringArray("cap-drop", []string{}, "Linux capability to drop (repeatable, e.g. ALL or NET_ADMIN)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_CAP_DROP.ViperKey, cmd.Flags().Lookup("cap-drop"))

	cmd.Flags().StringArray("cap-add", []string{}, "Linux capability to add (repeatable, e.g. NET_ADMIN)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_CAP_ADD.ViperKey, cmd.Flags().Lookup("cap-add"))

	cmd.Flags().StringArray(
		"security-opt",
		[]string{},
		`Security option (repeatable, e.g. "no-new-privileges" or "seccomp=unconfined")`,
	)
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_SECURITY_OPTS.ViperKey, cmd.Flags().Lookup("security-opt"))

	cmd.Flags().StringArray("tmpfs", []string{}, `Tmpfs mount "path[:opts]" (e.g. "/tmp:size=64m") (repeatable)`)
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_TMPFS.ViperKey, cmd.Flags().Lookup("tmpfs"))

	cmd.Flags().String("memory", "", `Hard memory limit (bytes, or with suffix k/m/g, e.g. "4g")`)
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_MEMORY.ViperKey, cmd.Flags().Lookup("memory"))

	cmd.Flags().Int64("cpu-shares", 0, "Relative CPU weight (cgroup cpu.shares)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_CPU_SHARES.ViperKey, cmd.Flags().Lookup("cpu-shares"))

	cmd.Flags().Int64("pids-limit", 0, "Maximum number of PIDs in the container (0 to leave unset)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONTAINER_PIDS_LIMIT.ViperKey, cmd.Flags().Lookup("pids-limit"))

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
	volumeMounts, err := parseVolumeFlags(volumesList)
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

	user := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_USER.ViperKey))
	readOnly := viper.GetBool(config.KUKE_CREATE_CONTAINER_READ_ONLY.ViperKey)

	capDropList, err := cmd.Flags().GetStringArray("cap-drop")
	if err != nil {
		return err
	}
	capAddList, err := cmd.Flags().GetStringArray("cap-add")
	if err != nil {
		return err
	}
	securityOptsList, err := cmd.Flags().GetStringArray("security-opt")
	if err != nil {
		return err
	}
	tmpfsList, err := cmd.Flags().GetStringArray("tmpfs")
	if err != nil {
		return err
	}
	tmpfsMounts, err := parseTmpfsFlags(tmpfsList)
	if err != nil {
		return err
	}

	memoryRaw := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONTAINER_MEMORY.ViperKey))
	memoryBytes, err := parseMemoryBytes(memoryRaw)
	if err != nil {
		return err
	}
	cpuShares := viper.GetInt64(config.KUKE_CREATE_CONTAINER_CPU_SHARES.ViperKey)
	pidsLimit := viper.GetInt64(config.KUKE_CREATE_CONTAINER_PIDS_LIMIT.ViperKey)

	capabilities := buildCapabilities(capDropList, capAddList)
	resources := buildResources(memoryBytes, cpuShares, pidsLimit)

	doc := v1beta1.NewContainerDoc(&v1beta1.ContainerDoc{
		Metadata: v1beta1.ContainerMetadata{
			Name:   name,
			Labels: labels,
		},
		Spec: v1beta1.ContainerSpec{
			ID:                     name,
			RealmID:                realm,
			SpaceID:                space,
			StackID:                stack,
			CellID:                 cell,
			Root:                   root,
			Image:                  image,
			Command:                command,
			Args:                   argsList,
			Env:                    envList,
			Ports:                  portsList,
			Volumes:                volumeMounts,
			Networks:               networksList,
			NetworksAliases:        networkAliasesList,
			Privileged:             privileged,
			User:                   user,
			ReadOnlyRootFilesystem: readOnly,
			Capabilities:           capabilities,
			SecurityOpts:           securityOptsList,
			Tmpfs:                  tmpfsMounts,
			Resources:              resources,
			CNIConfigPath:          cniConfigPath,
			RestartPolicy:          restartPolicy,
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

func buildCapabilities(drop, add []string) *v1beta1.ContainerCapabilities {
	drop = trimAndDropEmpty(drop)
	add = trimAndDropEmpty(add)
	if len(drop) == 0 && len(add) == 0 {
		return nil
	}
	return &v1beta1.ContainerCapabilities{Drop: drop, Add: add}
}

func buildResources(memoryBytes, cpuShares, pidsLimit int64) *v1beta1.ContainerResources {
	if memoryBytes <= 0 && cpuShares <= 0 && pidsLimit <= 0 {
		return nil
	}
	res := &v1beta1.ContainerResources{}
	if memoryBytes > 0 {
		m := memoryBytes
		res.MemoryLimitBytes = &m
	}
	if cpuShares > 0 {
		s := cpuShares
		res.CPUShares = &s
	}
	if pidsLimit > 0 {
		p := pidsLimit
		res.PidsLimit = &p
	}
	return res
}

func trimAndDropEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}

// parseVolumeFlags parses docker-style `--volume SRC:DST[:ro|rw]` flag values
// into structured VolumeMounts. Missing mode defaults to rw; any other mode
// value is rejected.
func parseVolumeFlags(entries []string) ([]v1beta1.VolumeMount, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make([]v1beta1.VolumeMount, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		parts := strings.Split(entry, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf(
				"invalid --volume %q: expected SRC:DST[:ro|rw]",
				raw,
			)
		}
		src := strings.TrimSpace(parts[0])
		dst := strings.TrimSpace(parts[1])
		if src == "" || dst == "" {
			return nil, fmt.Errorf(
				"invalid --volume %q: source and target are both required",
				raw,
			)
		}
		var readOnly bool
		if len(parts) == 3 {
			switch strings.TrimSpace(parts[2]) {
			case "ro":
				readOnly = true
			case "rw", "":
				readOnly = false
			default:
				return nil, fmt.Errorf(
					"invalid --volume %q: mode must be ro or rw",
					raw,
				)
			}
		}
		out = append(out, v1beta1.VolumeMount{
			Source:   src,
			Target:   dst,
			ReadOnly: readOnly,
		})
	}
	return out, nil
}

// parseTmpfsFlags parses docker-style "--tmpfs path[:opt1,opt2,...]" entries.
// The size option ("size=<N>[k|m|g]") is promoted to the structured sizeBytes
// field; everything else is preserved as a raw option string.
func parseTmpfsFlags(entries []string) ([]v1beta1.ContainerTmpfsMount, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	mounts := make([]v1beta1.ContainerTmpfsMount, 0, len(entries))
	for _, raw := range entries {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		path := entry
		var rawOpts string
		if idx := strings.Index(entry, ":"); idx >= 0 {
			path = strings.TrimSpace(entry[:idx])
			rawOpts = entry[idx+1:]
		}
		if path == "" {
			return nil, fmt.Errorf("invalid --tmpfs %q: path is required", raw)
		}
		mount := v1beta1.ContainerTmpfsMount{Path: path}
		if rawOpts != "" {
			for _, opt := range strings.Split(rawOpts, ",") {
				opt = strings.TrimSpace(opt)
				if opt == "" {
					continue
				}
				if after, ok := strings.CutPrefix(opt, "size="); ok {
					bytes, err := parseSizeBytes(after)
					if err != nil {
						return nil, fmt.Errorf("invalid --tmpfs %q: %w", raw, err)
					}
					mount.SizeBytes = bytes
					continue
				}
				mount.Options = append(mount.Options, opt)
			}
		}
		mounts = append(mounts, mount)
	}
	return mounts, nil
}

// parseMemoryBytes accepts a plain byte count or a value with a k/m/g (or
// ki/mi/gi) suffix, matching the docker convention. Empty input returns 0.
func parseMemoryBytes(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	return parseSizeBytes(raw)
}

func parseSizeBytes(raw string) (int64, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, fmt.Errorf("size is empty")
	}
	multiplier := int64(1)
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "gi"):
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-2]
	case strings.HasSuffix(lower, "mi"):
		multiplier = 1024 * 1024
		s = s[:len(s)-2]
	case strings.HasSuffix(lower, "ki"):
		multiplier = 1024
		s = s[:len(s)-2]
	case strings.HasSuffix(lower, "g"):
		multiplier = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(lower, "m"):
		multiplier = 1024 * 1024
		s = s[:len(s)-1]
	case strings.HasSuffix(lower, "k"):
		multiplier = 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid size %q: must be non-negative", raw)
	}
	return n * multiplier, nil
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
