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
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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

const noContainersFoundMsg = "No containers found."

func NewContainerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "container [name]",
		Aliases: []string{"containers", "co"},
		Short:   "Get or list container information",
		Long: `Get or list container information.

The default table is ` + "`NAME REALM SPACE STACK CELL STATE RESTARTS AGE`" + `.
` + "`-o wide`" + ` appends two container-only signals:

  IMAGE  spec.image (the resolved container image reference)
  EXIT   ` + "`<exitCode>/<exitSignal>`" + ` when either field is non-zero/
         non-empty; "-" otherwise — most meaningful on Stopped/Failed.

CGROUP, ROOT (as a column), and IMAGE (as a default column) no longer
appear in the default table — use ` + "`-o yaml` / `-o json`" + ` for the
full container spec.`,
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
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	shared.RegisterLabelSelectorFlag(cmd)

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
	wide, outputFormat, err := resolveOutput(cmd)
	if err != nil {
		return err
	}

	selector, err := shared.ParseLabelSelectorFlag(cmd)
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

	if name != "" && !selector.Empty() {
		return errdefs.ErrSelectorWithName
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

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
				return fmt.Errorf("container %q not found: %w", name, err)
			}
			return err
		}
		if !result.ContainerExists {
			return fmt.Errorf("container %q not found", name)
		}

		if outputFormat == shared.OutputFormatYAML || outputFormat == shared.OutputFormatJSON {
			return printContainer(cmd, &result.Container, outputFormat)
		}
		// table / wide: render the single found container as a one-row table
		// with the same columns as the list view (kubectl parity). Build the
		// single-element spec slice + probe map the list renderer expects,
		// backfilling scope coordinates the daemon may leave unset on the
		// returned spec (same defensive backfill the list path applies).
		spec := result.Container.Spec
		if spec.ID == "" {
			spec.ID = name
		}
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
		st := result.Container.Status
		probes := map[string]containerProbe{
			spec.ID: {
				state:        containerStateToString(st.State),
				restartCount: st.RestartCount,
				createdAt:    st.CreatedAt,
				exitCode:     st.ExitCode,
				exitSignal:   st.ExitSignal,
				labels:       result.Container.Metadata.Labels,
			},
		}
		return printContainersWithState(
			cmd,
			[]v1beta1.ContainerSpec{spec},
			probes,
			outputFormat,
			wide,
			"",
		)
	}

	// List path — query each container's state by calling GetContainer.
	specs, err := client.ListContainers(cmd.Context(), realm, space, stack, cell)
	if err != nil {
		return err
	}

	emptyMsg := noContainersFoundMsg
	if len(specs) == 0 && outputFormat == shared.OutputFormatTable {
		emptyMsg = buildEmptyResultMessage(realm, space, stack, cell)
		if hint := maybeBuildScopeHint(cmd.Context(), client, space, stack, cell); hint != "" {
			emptyMsg = emptyMsg + "\n" + hint
		}
	}

	// containerProbes carries per-container Metadata.Labels alongside state
	// for the post-loop selector filter: ListContainers returns
	// ContainerSpec (which carries no labels), so the filter has to wait
	// until each container's ContainerDoc is in hand. When the GetContainer
	// probe fails the labels stay nil — the selector then treats that
	// container as "no labels", which is the same conservative call
	// ContainerStateUnknown already makes for state.
	containerProbes := make(map[string]containerProbe, len(specs))
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
			containerProbes[spec.ID] = containerProbe{state: "Unknown"}
			continue
		}
		st := probeResult.Container.Status
		containerProbes[spec.ID] = containerProbe{
			state:        containerStateToString(st.State),
			restartCount: st.RestartCount,
			createdAt:    st.CreatedAt,
			exitCode:     st.ExitCode,
			exitSignal:   st.ExitSignal,
			labels:       probeResult.Container.Metadata.Labels,
		}
	}

	if !selector.Empty() {
		filtered := make([]v1beta1.ContainerSpec, 0, len(specs))
		for i := range specs {
			if selector.Matches(containerProbes[specs[i].ID].labels) {
				filtered = append(filtered, specs[i])
			}
		}
		specs = filtered
	}

	return printContainersWithState(
		cmd,
		specs,
		containerProbes,
		outputFormat,
		wide,
		emptyMsg,
	)
}

// containerProbe carries the per-container fields a list-path probe pulls
// from GetContainer for the table renderer. State is the human-readable
// label; the rest source the RESTARTS / AGE / EXIT columns. labels feeds
// the post-loop selector filter — see runContainerCmd's probe loop.
type containerProbe struct {
	state        string
	restartCount int
	createdAt    time.Time
	exitCode     int
	exitSignal   string
	labels       map[string]string
}

// buildEmptyResultMessage describes the queried filter set when zero rows
// match, so the operator sees what filter actually fired instead of a bare
// "No containers found." Empty filters are omitted.
func buildEmptyResultMessage(realm, space, stack, cell string) string {
	var parts []string
	if cell != "" {
		parts = append(parts, fmt.Sprintf("cell %q", cell))
	}
	if stack != "" {
		parts = append(parts, fmt.Sprintf("stack %q", stack))
	}
	if space != "" {
		parts = append(parts, fmt.Sprintf("space %q", space))
	}
	if realm != "" {
		parts = append(parts, fmt.Sprintf("realm %q", realm))
	}
	if len(parts) == 0 {
		return noContainersFoundMsg
	}
	return fmt.Sprintf("No containers found for %s.", strings.Join(parts, " "))
}

// maybeBuildScopeHint looks for the user-supplied --cell / --space / --stack
// name in scopes other than the queried one. Issue #472: when scope-bound
// filtering returns zero rows but the named entity exists elsewhere, the
// operator otherwise has no clue where to find it. Returns the empty string
// when there is nothing useful to surface (e.g. only --realm was set, or the
// named entity does not exist anywhere). Fails open: any cross-scope lookup
// error is treated as "no hint" rather than escalated.
func maybeBuildScopeHint(
	ctx context.Context,
	client kukeonv1.Client,
	space, stack, cell string,
) string {
	if cell == "" && stack == "" && space == "" {
		return ""
	}

	allSpecs, err := client.ListContainers(ctx, "", "", "", "")
	if err != nil {
		return ""
	}

	type scope struct{ realm, space, stack string }
	seen := map[scope]bool{}
	for i := range allSpecs {
		s := &allSpecs[i]
		if cell != "" && s.CellID != cell {
			continue
		}
		if stack != "" && s.StackID != stack {
			continue
		}
		if space != "" && s.SpaceID != space {
			continue
		}
		seen[scope{s.RealmID, s.SpaceID, s.StackID}] = true
	}

	if len(seen) == 0 {
		return ""
	}

	sorted := make([]scope, 0, len(seen))
	for sc := range seen {
		sorted = append(sorted, sc)
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].realm != sorted[j].realm {
			return sorted[i].realm < sorted[j].realm
		}
		if sorted[i].space != sorted[j].space {
			return sorted[i].space < sorted[j].space
		}
		return sorted[i].stack < sorted[j].stack
	})

	var entityDesc string
	switch {
	case cell != "":
		entityDesc = fmt.Sprintf("cell %q", cell)
	case stack != "":
		entityDesc = fmt.Sprintf("stack %q", stack)
	case space != "":
		entityDesc = fmt.Sprintf("space %q", space)
	}

	locs := make([]string, 0, len(sorted))
	for _, sc := range sorted {
		locs = append(locs, fmt.Sprintf("realm %q space %q stack %q", sc.realm, sc.space, sc.stack))
	}

	return fmt.Sprintf(
		"Hint: %s exists in %s — pass --realm/--space/--stack to filter there.",
		entityDesc, strings.Join(locs, "; "),
	)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

func printContainer(cmd *cobra.Command, container interface{}, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, container)
	default:
		return shared.PrintYAML(cmd, container)
	}
}

func printContainersWithState(
	cmd *cobra.Command,
	containers []v1beta1.ContainerSpec,
	probes map[string]containerProbe,
	format shared.OutputFormat,
	wide bool,
	emptyMsg string,
) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, containers)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, containers)
	case shared.OutputFormatTable:
		if len(containers) == 0 {
			if emptyMsg == "" {
				emptyMsg = noContainersFoundMsg
			}
			cmd.Println(emptyMsg)
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK", "CELL", "STATE", "RESTARTS", "AGE"}
		if wide {
			headers = append(headers, "IMAGE", "EXIT")
		}
		now := time.Now()
		rows := make([][]string, 0, len(containers))
		for i := range containers {
			c := &containers[i]
			p, ok := probes[c.ID]
			if !ok {
				p = containerProbe{state: "Unknown"}
			}
			row := []string{
				containerDisplayName(c),
				c.RealmID,
				c.SpaceID,
				c.StackID,
				c.CellID,
				p.state,
				strconv.Itoa(p.restartCount),
				shared.RenderAge(p.createdAt, now),
			}
			if wide {
				row = append(row, c.Image, renderExit(p.exitCode, p.exitSignal))
			}
			rows = append(rows, row)
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, containers)
	}
}

// resolveOutput sits between the cobra flag and ParseOutputFormat so the
// `wide` value is normalised to `table` plus a bool, leaving the shared
// yaml/json/table parser untouched. Mirrors the helper in cmd/kuke/get/cell.
func resolveOutput(cmd *cobra.Command) (bool, shared.OutputFormat, error) {
	raw := strings.TrimSpace(cmd.Flag("output").Value.String())
	if strings.EqualFold(raw, "wide") {
		_ = cmd.Flags().Set("output", "table")
		format, err := shared.ParseOutputFormat(cmd)
		return true, format, err
	}
	format, err := shared.ParseOutputFormat(cmd)
	return false, format, err
}

// renderExit returns the EXIT column value — `<code>/<signal>` when either
// field is non-zero/non-empty, "-" when both are at their zero values.
// Most meaningful on Stopped/Failed states. Issue #605.
func renderExit(code int, signal string) string {
	if code == 0 && signal == "" {
		return "-"
	}
	return fmt.Sprintf("%d/%s", code, signal)
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
	case v1beta1.ContainerStateNotCreated:
		return "NotCreated"
	case v1beta1.ContainerStateExited:
		return "Exited"
	case v1beta1.ContainerStateError:
		return "Error"
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
