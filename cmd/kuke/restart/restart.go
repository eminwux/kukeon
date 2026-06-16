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

package restart

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

// NewRestartCmd builds the `kuke restart <name>` leaf. Cell is the only
// resource this verb targets. A Ready cell is stop+start; a terminal/degraded
// cell (Stopped/Exited/Error/Failed) is equivalent to `kuke start <name>` (the
// daemon recovers a Failed cell via a recreate-style path, #1268); a
// genuinely-unrecoverable Pending/Unknown cell is refused with a
// `kuke delete cell` pointer.
//
// For a Config-lineage cell whose live spec has diverged from the daemon-stored
// Config (Status.OutOfSync = true), the start step automatically re-materialises
// from the Config so the restart is also a reconcile. That logic lives in the
// daemon's controller.StartCell (internal/controller/start_cell.go) — restart
// is just stop+start on top, and `kuke stop` + `kuke start` produces the same
// end state as `kuke restart`. Issue #983.
func NewRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart [name]",
		Aliases: []string{"res"},
		Short:   "Restart a cell (or a fleet via -l <selector>; on OutOfSync also reconciles from Config)",
		Long: "Restart a cell. Bounces the cell's containers (stop + start). " +
			"When the cell carries a `kukeon.io/config=<name>` lineage label " +
			"and the daemon has marked it OutOfSync, the start step uses the " +
			"freshly materialised spec from the Config so the restart is also " +
			"a reconcile — equivalent to `kuke stop` + `kuke start`. " +
			"Severs any active attach session as a side effect of the stop step. " +
			"With `-l <selector>` (mutually exclusive with a positional name) the " +
			"restart fans out across every cell whose labels match, reconciling " +
			"each matched cell individually — unmatched cells are untouched.",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRestart,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_RESTART_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_RESTART_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_RESTART_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	getshared.RegisterLabelSelectorFlag(cmd)

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runRestart(cmd *cobra.Command, args []string) error {
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
		return restartBySelector(cmd, client, selector)
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_RESTART_CELL_REALM.ViperKey))
	space := strings.TrimSpace(viper.GetString(config.KUKE_RESTART_CELL_SPACE.ViperKey))
	stack := strings.TrimSpace(viper.GetString(config.KUKE_RESTART_CELL_STACK.ViperKey))

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

	return restartOne(cmd, client, doc)
}

// restartBySelector lists cells in the (optionally realm/space/stack-scoped)
// fleet, keeps those whose labels match selector, and restarts each one
// individually by looping the per-cell verb. Realm/space/stack flags act as
// list filters here (unset = no filter), mirroring `kuke get cell`; the scope
// of each restart comes from the matched cell's own spec. A per-cell failure
// is collected and the loop continues so one bad cell does not abort the rest
// of the fleet rollout.
func restartBySelector(cmd *cobra.Command, client kukeonv1.Client, selector *getshared.LabelSelector) error {
	realm := getshared.ExplicitFlag(cmd, "realm", config.KUKE_RESTART_CELL_REALM.ViperKey)
	space := getshared.ExplicitFlag(cmd, "space", config.KUKE_RESTART_CELL_SPACE.ViperKey)
	stack := getshared.ExplicitFlag(cmd, "stack", config.KUKE_RESTART_CELL_STACK.ViperKey)

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
		if err := restartOne(cmd, client, doc); err != nil {
			errs = append(errs, fmt.Errorf("restart cell %q: %w", c.Metadata.Name, err))
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

// restartOne runs the restart state machine for a single cell described by doc:
// a Ready cell is stop+start; a Stopped/Exited/Error/Failed/Degraded cell is
// start (the daemon recovers a Failed/Error/Degraded cell via a recreate-style
// path, #1268/#1318); a Pending/Unknown cell is refused with a delete pointer.
// Shared by the positional-name path and the per-match loop in
// restartBySelector.
func restartOne(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error {
	name := doc.Metadata.Name
	realm := doc.Spec.RealmID
	space := doc.Spec.SpaceID
	stack := doc.Spec.StackID

	pre, err := client.GetCell(cmd.Context(), doc)
	if err != nil {
		return err
	}
	if !pre.MetadataExists {
		return fmt.Errorf("%w (cell %q in realm=%q space=%q stack=%q)",
			errdefs.ErrCellNotFound, name, realm, space, stack)
	}

	switch pre.Cell.Status.State {
	case v1beta1.CellStateReady:
		// A Ready cell bounces in place: stop then start.
		return restartInPlace(cmd, client, doc)
	case v1beta1.CellStateStopped, v1beta1.CellStateExited, v1beta1.CellStateError,
		v1beta1.CellStateFailed, v1beta1.CellStateDegraded:
		// These states restart without a stop-first (#1268): Stopped (operator
		// stop/kill) and Exited (clean self-exit, #1267) bring the cell back up
		// from intact container records, while Error (workload crash whose sticky
		// root is still live) and Failed (kukeon bring-up fault) are recovered by
		// the daemon's StartCell via a recreate-style recovery (stop -> recreate
		// containers -> start, including the leftover root) (#1274). Degraded
		// (#1318) — a live cell whose non-root workload is down/restarting —
		// recovers the same way: the daemon's StartCell now routes Degraded
		// through that recreate path, but only if it observes the persisted
		// Degraded state. restartStopped calls StartCell directly (no stop-first)
		// so the daemon sees the persisted Error/Failed/Degraded state and picks
		// the recreate path; an in-place stop-first would flip the cell to a
		// transient Stopped/Error before StartCell re-reads it, skipping the
		// recovery and leaving it stuck at a sticky Error N/N.
		return restartStopped(cmd, client, doc)
	case v1beta1.CellStatePending, v1beta1.CellStateUnknown:
		// Same delete-then-rerun pointer as `kuke run` on a genuinely-
		// unrecoverable cell. Restart does not reconcile a Pending/Unknown cell
		// in place; the operator deletes and re-runs.
		return fmt.Errorf(
			"cell %q exists in %s state; delete it with `kuke delete cell %s` before restarting",
			name, pre.Cell.Status.State.String(), name,
		)
	}
	return fmt.Errorf("cell %q exists in unrecognized state %d", name, pre.Cell.Status.State)
}

// restartInPlace bounces the cell: stop then start. The CellDoc only needs name
// + scope — the daemon resolves both verbs from the stored cell metadata. When
// the cell is Config-lineage and Status.OutOfSync = true, the daemon's
// controller.StartCell re-materialises from the Config on the start step so
// the running cell ends up with the freshly-materialised spec.
func restartInPlace(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error {
	if _, err := client.StopCell(cmd.Context(), doc); err != nil {
		return err
	}
	startRes, err := client.StartCell(cmd.Context(), doc)
	if err != nil {
		return err
	}

	cellName := startRes.Cell.Metadata.Name
	if cellName == "" {
		cellName = doc.Metadata.Name
	}
	stackName := startRes.Cell.Spec.StackID
	if stackName == "" {
		stackName = doc.Spec.StackID
	}
	cmd.Printf("Restarted cell %q from stack %q\n", cellName, stackName)
	return nil
}

// restartStopped is the already-stopped path: equivalent to
// `kuke start <name>`. The daemon's controller.StartCell handles the
// OutOfSync reapply uniformly with restartInPlace's start step.
func restartStopped(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error {
	startRes, err := client.StartCell(cmd.Context(), doc)
	if err != nil {
		return err
	}

	cellName := startRes.Cell.Metadata.Name
	if cellName == "" {
		cellName = doc.Metadata.Name
	}
	stackName := startRes.Cell.Spec.StackID
	if stackName == "" {
		stackName = doc.Spec.StackID
	}
	cmd.Printf("Started cell %q from stack %q\n", cellName, stackName)
	return nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
