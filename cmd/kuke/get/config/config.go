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

package config

import (
	"errors"
	"fmt"
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

// NewConfigCmd builds `kuke get config(s)` (issue #644). With a name it shows
// one Config's full document (blueprint ref, values, repo/secret slot fills);
// without one it lists every Config bound to the filter scope or any scope
// nested within it (subtree-filter semantics, matching `kuke get blueprints`).
// A Config is never cell-scoped, so there is no --cell flag — the scope bottoms
// out at stack.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "config [name]",
		Aliases:       []string{"configs", "cfg"},
		Short:         "Get or list kind: CellConfig",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			outputFormat, err := shared.ParseOutputFormat(cmd)
			if err != nil {
				return err
			}

			// List filters: an unset flag means "no filter" (ExplicitFlag),
			// so `kuke get configs` with no flags lists the whole subtree.
			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_CONFIG_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_CONFIG_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_CONFIG_STACK.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CONFIG_NAME.ViperKey))
			}

			if name != "" {
				// A single config is identified by its name plus its exact
				// binding scope. Default every coordinate to the operator's
				// full default scope (realm/space/stack = "default") so a
				// no-flag `kuke get config <name>` finds resources stored at
				// the full default coordinate — how `kuke create config` and
				// the team renderer actually store them. Mirrors `get cell`
				// (issue #1156). A realm-scoped Config (space/stack unset) is
				// reachable by passing an explicit `--space "" --stack ""`.
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_CONFIG_REALM.ValueOrDefault())
				}
				if space == "" {
					space = strings.TrimSpace(config.KUKE_GET_CONFIG_SPACE.ValueOrDefault())
				}
				if stack == "" {
					stack = strings.TrimSpace(config.KUKE_GET_CONFIG_STACK.ValueOrDefault())
				}
				if realm == "" {
					return fmt.Errorf("%w (--realm)", errdefs.ErrRealmNameRequired)
				}
				lookup := v1beta1.CellConfigDoc{
					APIVersion: v1beta1.APIVersionV1Beta1,
					Kind:       v1beta1.KindCellConfig,
					Metadata: v1beta1.CellConfigMetadata{
						Name:  name,
						Realm: realm,
						Space: space,
						Stack: stack,
					},
				}
				result, getErr := client.GetConfig(cmd.Context(), lookup)
				if getErr != nil {
					if errors.Is(getErr, errdefs.ErrConfigNotFound) {
						return configNotFoundErr(cmd, client, name, realm, space, stack)
					}
					return getErr
				}
				if !result.MetadataExists {
					return configNotFoundErr(cmd, client, name, realm, space, stack)
				}
				return printConfig(cmd, &result.Config, outputFormat)
			}

			configs, err := client.ListConfigs(cmd.Context(), realm, space, stack)
			if err != nil {
				return err
			}
			return printConfigs(cmd, configs, outputFormat)
		},
	}

	cmd.Flags().String("realm", "", "Filter configs by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter configs by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter configs by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CONFIG_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, table for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))

	cmd.ValidArgsFunction = config.CompleteConfigNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("output", config.CompleteOutputFormat)
	_ = cmd.RegisterFlagCompletionFunc("o", config.CompleteOutputFormat)

	return cmd
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}

// configNotFoundErr builds the single-get miss error. It surfaces the exact
// scope searched and — best-effort — hints the coordinate where a Config of the
// same name does live (probed realm-wide), so an operator who stored it at a
// non-default space/stack sees where to look instead of a bare "not found"
// (issue #1156). The realm-wide list is advisory: a list error is swallowed and
// the base error returned unadorned.
func configNotFoundErr(
	cmd *cobra.Command, client kukeonv1.Client, name, realm, space, stack string,
) error {
	base := fmt.Sprintf(
		"config %q not found (searched realm=%q space=%q stack=%q)", name, realm, space, stack,
	)
	configs, err := client.ListConfigs(cmd.Context(), realm, "", "")
	if err != nil {
		return errors.New(base)
	}
	for i := range configs {
		m := &configs[i].Metadata
		if m.Name == name && (m.Realm != realm || m.Space != space || m.Stack != stack) {
			return fmt.Errorf(
				"%s; a config %q exists at realm=%q space=%q stack=%q",
				base, name, m.Realm, m.Space, m.Stack,
			)
		}
	}
	return errors.New(base)
}

func printConfig(cmd *cobra.Command, cfg *v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cmd, cfg)
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, cfg)
	default:
		// table / wide: render the single found element as a one-row table
		// with the same columns as the list view (kubectl parity).
		return printConfigs(cmd, []v1beta1.CellConfigDoc{*cfg}, format)
	}
}

func printConfigs(cmd *cobra.Command, configs []v1beta1.CellConfigDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cmd, configs)
	case shared.OutputFormatJSON:
		// A nil slice encodes as JSON null; normalize so an empty list
		// emits [] like cells/containers and the YAML path already do.
		if configs == nil {
			configs = []v1beta1.CellConfigDoc{}
		}
		return shared.PrintJSON(cmd, configs)
	case shared.OutputFormatTable, shared.OutputFormatWide:
		if len(configs) == 0 {
			cmd.Println("No configs found.")
			return nil
		}
		// -o wide adds how each connector the Config requires resolves
		// (issue #1548); the structured formats carry it as status.connectors.
		// It also adds the Config's display name (issue #1692).
		wide := format == shared.OutputFormatWide
		headers := []string{"NAME", "REALM", "SPACE", "STACK", "SCHEDULE", "NEXT"}
		if wide {
			headers = append(headers, "CONNECTORS", "DISPLAY NAME")
		}
		rows := make([][]string, 0, len(configs))
		for i := range configs {
			c := &configs[i]
			row := []string{
				c.Metadata.Name,
				dashIfEmpty(c.Metadata.Realm),
				dashIfEmpty(c.Metadata.Space),
				dashIfEmpty(c.Metadata.Stack),
				scheduleColumn(c),
				nextColumn(c),
			}
			if wide {
				row = append(row, dashIfEmpty(connectorsColumn(c)),
					dashIfEmpty(strings.TrimSpace(c.Metadata.Annotations[v1beta1.AnnotationDisplayName])))
			}
			rows = append(rows, row)
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cmd, configs)
	}
}

// scheduleColumn renders a Config's spec.schedule for the SCHEDULE column: the
// cron expression, followed by its time zone when it is not UTC. Empty for an
// unscheduled Config (issue #1503).
func scheduleColumn(c *v1beta1.CellConfigDoc) string {
	schedule := c.Spec.Schedule
	if schedule == nil {
		return ""
	}
	cron := strings.TrimSpace(schedule.Cron)
	if tz := strings.TrimSpace(schedule.Timezone); tz != "" {
		return cron + " (" + tz + ")"
	}
	return cron
}

// nextColumn renders the NEXT column: the schedule's next fire time in UTC,
// or "suspended" while spec.schedule.suspend is set. Empty for an unscheduled
// Config, and for a schedule kukeond has not picked up yet — the scheduler
// fills status.schedule on its next pass, within a minute of the apply.
func nextColumn(c *v1beta1.CellConfigDoc) string {
	schedule := c.Spec.Schedule
	if schedule == nil {
		return ""
	}
	if schedule.Suspend {
		return "suspended"
	}
	if c.Status == nil || c.Status.Schedule == nil || c.Status.Schedule.NextScheduleTime == nil {
		return ""
	}
	return c.Status.Schedule.NextScheduleTime.UTC().Format(time.RFC3339)
}

// connectorsColumn renders the CONNECTORS column: one `<connector>=<what fills
// it>` entry per connector the Config requires. What fills it is the identity
// and the level it comes from (`gh-token (realm)`), the method for a connector
// that keeps a login in the state volume (`manual`), `MISSING` for a required
// connector nothing resolves — a cell cannot be created until one does — and
// `none` for one the cell starts without. A connector that authenticates a
// harness adds, in brackets, where the harness keeps its state: the login
// identity and its login state (`login [kukeon-dev-state, logged in]`), or
// the state volume of a token method (`tok (realm) [state kukeon-dev-state]`,
// `[state ephemeral]`).
func connectorsColumn(c *v1beta1.CellConfigDoc) string {
	if c.Status == nil {
		return ""
	}
	parts := make([]string, 0, len(c.Status.Connectors))
	for _, status := range c.Status.Connectors {
		part := status.Connector + "=" + status.Fill()
		if state := status.StateText(); state != "" {
			part += " [" + state + "]"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, ", ")
}

// dashIfEmpty renders an unset scope coordinate as "-" so the table column
// never collapses to an unaligned blank cell.
func dashIfEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}
