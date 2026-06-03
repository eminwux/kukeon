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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
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
// resource this verb targets. The verb is dual mode: a Ready cell is
// stop+start; a Stopped cell is equivalent to `kuke start <name>`; an
// error/partial-state cell is refused with a `kuke delete cell` pointer.
//
// For a Config-lineage cell whose live spec has diverged from the daemon-stored
// Config (Status.OutOfSync = true), the start step automatically re-materialises
// from the Config so the restart is also a reconcile. That logic lives in the
// daemon's controller.StartCell (internal/controller/start_cell.go) — restart
// is just stop+start on top, and `kuke stop` + `kuke start` produces the same
// end state as `kuke restart`. Issue #983.
func NewRestartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "restart <name>",
		Aliases: []string{"res"},
		Short:   "Restart a cell (bounces the process; on OutOfSync also reconciles from Config)",
		Long: "Restart a cell. Bounces the cell's containers (stop + start). " +
			"When the cell carries a `kukeon.io/config=<name>` lineage label " +
			"and the daemon has marked it OutOfSync, the start step uses the " +
			"freshly materialised spec from the Config so the restart is also " +
			"a reconcile — equivalent to `kuke stop` + `kuke start`. " +
			"Severs any active attach session as a side effect of the stop step.",
		Args:          cobra.ExactArgs(1),
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

	cmd.ValidArgsFunction = config.CompleteCellNames
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runRestart(cmd *cobra.Command, args []string) error {
	name := strings.TrimSpace(args[0])
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

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

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
		return restartInPlace(cmd, client, doc)
	case v1beta1.CellStateStopped:
		return restartStopped(cmd, client, doc)
	case v1beta1.CellStatePending, v1beta1.CellStateFailed, v1beta1.CellStateUnknown:
		// Same delete-then-rerun pointer as `kuke run` on a degraded cell
		// (cmd/kuke/run/run.go:505-516). Restart does not reconcile a
		// degraded cell in place; the operator deletes and re-runs.
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
