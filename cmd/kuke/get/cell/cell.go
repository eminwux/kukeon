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

package cell

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	// syncStateSynced / syncStateOutOfSync are the SYNC column verdicts for
	// Config-lineage cells. syncStateNone is the third state for cells that
	// carry no kukeon.io/config lineage label (the bulk of the host —
	// hand-built and `-b`-lineage cells are deliberately out of scope of the
	// reconciler's OutOfSync detection per #819's umbrella) and follows the
	// established `-` convention used by CgroupPath when empty.
	syncStateSynced    = "Synced"
	syncStateOutOfSync = "OutOfSync"
	syncStateNone      = "-"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cell [name]",
		Aliases: []string{"cells", "ce"},
		Short:   "Get or list cell information",
		Long: `Get or list cell information.

The default table includes a SYNC column showing the reconciler-detected
sync state of each cell relative to its lineage Config:

  Synced     - the live spec matches what the lineage Config materializes
  OutOfSync  - divergence detected (or the lineage Config was deleted)
  -          - the cell carries no kukeon.io/config lineage label, so the
               Config reconciler does not track sync state for it

Use ` + "`-o wide`" + ` to add a DIVERGENCE column with the short divergence
summary (or the error message when the reconciler could not compute
divergence). ` + "`-o yaml` / `-o json`" + ` surface the full
outOfSync / outOfSyncReason / outOfSyncError status fields.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := resolveClient(cmd)
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()

			wide, outputFormat, err := resolveOutput(cmd)
			if err != nil {
				return err
			}

			realm := shared.ExplicitFlag(cmd, "realm", config.KUKE_GET_CELL_REALM.ViperKey)
			space := shared.ExplicitFlag(cmd, "space", config.KUKE_GET_CELL_SPACE.ViperKey)
			stack := shared.ExplicitFlag(cmd, "stack", config.KUKE_GET_CELL_STACK.ViperKey)

			var name string
			if len(args) > 0 {
				name = strings.TrimSpace(args[0])
			} else {
				name = strings.TrimSpace(viper.GetString(config.KUKE_GET_CELL_NAME.ViperKey))
			}

			if name != "" {
				if realm == "" {
					realm = strings.TrimSpace(config.KUKE_GET_CELL_REALM.ValueOrDefault())
				}
				if space == "" {
					space = strings.TrimSpace(config.KUKE_GET_CELL_SPACE.ValueOrDefault())
				}
				if stack == "" {
					stack = strings.TrimSpace(config.KUKE_GET_CELL_STACK.ValueOrDefault())
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
				doc := v1beta1.CellDoc{
					Metadata: v1beta1.CellMetadata{Name: name},
					Spec: v1beta1.CellSpec{
						RealmID: realm,
						SpaceID: space,
						StackID: stack,
					},
				}
				result, err := client.GetCell(cmd.Context(), doc)
				if err != nil {
					if errors.Is(err, errdefs.ErrCellNotFound) {
						return fmt.Errorf("cell %q not found in stack %q/%q/%q", name, realm, space, stack)
					}
					return err
				}
				if !result.MetadataExists {
					return fmt.Errorf("cell %q not found in stack %q/%q/%q", name, realm, space, stack)
				}
				return printCell(&result.Cell, outputFormat)
			}

			cells, err := client.ListCells(cmd.Context(), realm, space, stack)
			if err != nil {
				return err
			}
			showControllers, _ := cmd.Flags().GetBool("show-controllers")
			return printCells(cmd, cells, outputFormat, wide, showControllers)
		},
	}

	cmd.Flags().String("realm", "", "Filter cells by realm name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Filter cells by space name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Filter cells by stack name")
	_ = viper.BindPFlag(config.KUKE_GET_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))
	cmd.Flags().
		StringP("output", "o", "", "Output format (yaml, json, table, wide). Default: table for list, yaml for single resource")
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))
	_ = viper.BindPFlag(config.KUKE_GET_OUTPUT.ViperKey, cmd.Flags().Lookup("o"))
	cmd.Flags().Bool(
		"show-controllers", false,
		"Append a CONTROLLERS column listing the cgroup-v2 controllers delegated on each cell's subtree (issue #328).",
	)

	cmd.ValidArgsFunction = config.CompleteCellNames
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

// resolveOutput sits between the cobra flag and ParseOutputFormat so the
// `wide` value is normalised to `table` plus a bool, leaving the shared
// yaml/json/table parser untouched. Mirrors the helper in cmd/kuke/get/image.
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

// cellSyncState renders the SYNC column for a cell. The three values are
// driven by the cell's lineage label and the reconciler-written OutOfSync
// status fields (issue #820, populated by #830's detection loop). Cells
// where OutOfSyncError is non-empty (divergence undecidable — Blueprint
// missing or materialization failure) render as OutOfSync so the operator
// gets one actionable "look at this cell" signal in the SYNC column; the
// distinct error message surfaces in the DIVERGENCE column under -o wide
// and in the outOfSyncError field under -o yaml/json.
func cellSyncState(c *v1beta1.CellDoc) string {
	if !hasConfigLineage(c) {
		return syncStateNone
	}
	if c.Status.OutOfSync || c.Status.OutOfSyncError != "" {
		return syncStateOutOfSync
	}
	return syncStateSynced
}

// cellDivergence renders the DIVERGENCE column under -o wide. Synced or
// no-lineage cells render `-` (matching the established CgroupPath empty
// convention) so the column stays width-aligned. Reason takes precedence
// over Error when both are set (the reconciler never sets both, but the
// order is defensive).
func cellDivergence(c *v1beta1.CellDoc) string {
	if !hasConfigLineage(c) {
		return syncStateNone
	}
	if c.Status.OutOfSyncReason != "" {
		return c.Status.OutOfSyncReason
	}
	if c.Status.OutOfSyncError != "" {
		return "error: " + c.Status.OutOfSyncError
	}
	return syncStateNone
}

// hasConfigLineage matches the configLineage helper in
// internal/controller/reconcile_outofsync.go: a cell is Config-lineage iff
// it carries a non-empty kukeon.io/config label. Replicated inline rather
// than imported to keep the CLI leaf free of an internal/controller
// dependency.
func hasConfigLineage(c *v1beta1.CellDoc) bool {
	return strings.TrimSpace(c.Metadata.Labels[cellconfig.LabelConfig]) != ""
}

func printCell(cell *v1beta1.CellDoc, format shared.OutputFormat) error {
	switch format {
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cell)
	default:
		return shared.PrintYAML(cell)
	}
}

func printCells(
	cmd *cobra.Command,
	cells []v1beta1.CellDoc,
	format shared.OutputFormat,
	wide bool,
	showControllers bool,
) error {
	switch format {
	case shared.OutputFormatYAML:
		return shared.PrintYAML(cells)
	case shared.OutputFormatJSON:
		return shared.PrintJSON(cells)
	case shared.OutputFormatTable:
		if len(cells) == 0 {
			cmd.Println("No cells found.")
			return nil
		}
		headers := []string{"NAME", "REALM", "SPACE", "STACK", "STATE", "SYNC", "CGROUP"}
		if wide {
			headers = append(headers, "DIVERGENCE")
		}
		if showControllers {
			headers = append(headers, "CONTROLLERS")
		}
		rows := make([][]string, 0, len(cells))
		for i := range cells {
			c := &cells[i]
			state := (&c.Status.State).String()
			cgroup := c.Status.CgroupPath
			if cgroup == "" {
				cgroup = "-"
			}
			row := []string{
				c.Metadata.Name,
				c.Spec.RealmID,
				c.Spec.SpaceID,
				c.Spec.StackID,
				state,
				cellSyncState(c),
				cgroup,
			}
			if wide {
				row = append(row, cellDivergence(c))
			}
			if showControllers {
				row = append(row, shared.FormatControllers(c.Status.SubtreeControllers))
			}
			rows = append(rows, row)
		}
		shared.PrintTable(cmd, headers, rows)
		return nil
	default:
		return shared.PrintYAML(cells)
	}
}
