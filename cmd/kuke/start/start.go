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

package start

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	getshared "github.com/eminwux/kukeon/cmd/kuke/get/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewStartCmd builds the `kuke start [name]` leaf command. Cell is the only
// resource this verb targets, so the noun is implied by the verb. A bare name
// starts one cell; `-l <selector>` fans the start out across every cell whose
// labels match (mutually exclusive with a positional name), reconciling each
// matched cell individually — unmatched cells are untouched.
func NewStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "start [name]",
		Aliases:       []string{"sta"},
		Short:         "Start a cell (or a fleet via -l <selector>)",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runStart,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_START_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_START_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_START_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	getshared.RegisterLabelSelectorFlag(cmd)

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runStart(cmd *cobra.Command, args []string) error {
	selector, err := getshared.ParseLabelSelectorFlag(cmd)
	if err != nil {
		return err
	}

	var name string
	if len(args) > 0 {
		name = strings.TrimSpace(args[0])
	}

	if name != "" && !selector.Empty() {
		return errdefs.ErrSelectorWithName
	}
	if name == "" && selector.Empty() {
		return errdefs.ErrCellNameRequired
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if !selector.Empty() {
		return startBySelector(cmd, client, selector)
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_START_CELL_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_START_CELL_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_START_CELL_STACK.ViperKey))

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
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata:   v1beta1.CellMetadata{Name: name, Labels: map[string]string{}},
		Spec: v1beta1.CellSpec{
			ID:      name,
			RealmID: realm,
			SpaceID: space,
			StackID: stack,
		},
	}

	return startOne(cmd, client, doc)
}

// startOne starts a single cell described by doc and prints the per-cell
// confirmation line. Shared by the positional-name path and the per-match
// loop in startBySelector so both paths render identical output.
func startOne(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error {
	result, err := client.StartCell(cmd.Context(), doc)
	if err != nil {
		return err
	}

	cellName := result.Cell.Metadata.Name
	if cellName == "" {
		cellName = doc.Metadata.Name
	}
	stackName := result.Cell.Spec.StackID
	if stackName == "" {
		stackName = doc.Spec.StackID
	}
	cmd.Printf("Started cell %q from stack %q\n", cellName, stackName)
	return nil
}

// startBySelector lists cells in the (optionally realm/space/stack-scoped)
// fleet, keeps those whose labels match selector, and starts each one
// individually by looping the per-cell verb. Realm/space/stack flags act as
// list filters here (unset = no filter), mirroring `kuke get cell`; the scope
// of each StartCell call comes from the matched cell's own spec. A per-cell
// failure is collected and the loop continues so one bad cell does not abort
// the rest of the fleet rollout.
func startBySelector(cmd *cobra.Command, client kukeonv1.Client, selector *getshared.LabelSelector) error {
	realm := getshared.ExplicitFlag(cmd, "realm", config.KUKE_START_CELL_REALM.ViperKey)
	space := getshared.ExplicitFlag(cmd, "space", config.KUKE_START_CELL_SPACE.ViperKey)
	stack := getshared.ExplicitFlag(cmd, "stack", config.KUKE_START_CELL_STACK.ViperKey)

	cells, err := client.ListCells(cmd.Context(), realm, space, stack)
	if err != nil {
		return err
	}
	matched := filterCellsBySelector(cells, selector)
	if len(matched) == 0 {
		cmd.Println("No cells matched the selector.")
		return nil
	}

	var errs []error
	for i := range matched {
		c := &matched[i]
		doc := v1beta1.CellDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindCell,
			Metadata:   v1beta1.CellMetadata{Name: c.Metadata.Name, Labels: map[string]string{}},
			Spec: v1beta1.CellSpec{
				ID:      c.Metadata.Name,
				RealmID: c.Spec.RealmID,
				SpaceID: c.Spec.SpaceID,
				StackID: c.Spec.StackID,
			},
		}
		if err := startOne(cmd, client, doc); err != nil {
			errs = append(errs, fmt.Errorf("start cell %q: %w", c.Metadata.Name, err))
		}
	}
	return errors.Join(errs...)
}

// filterCellsBySelector returns the subset of cells whose Metadata.Labels
// satisfy selector. A nil or empty selector returns the input slice
// unmodified. Replicated inline (mirroring `kuke get cell`) rather than
// importing the get/cell leaf, keeping the lifecycle verb off that package.
func filterCellsBySelector(cells []v1beta1.CellDoc, selector *getshared.LabelSelector) []v1beta1.CellDoc {
	if selector.Empty() {
		return cells
	}
	out := make([]v1beta1.CellDoc, 0, len(cells))
	for i := range cells {
		if selector.Matches(cells[i].Metadata.Labels) {
			out = append(out, cells[i])
		}
	}
	return out
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
