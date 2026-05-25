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
	"context"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewCellCmd builds the `kuke restart cell <name>` leaf. The verb is dual mode:
// a Ready Synced cell is stop+start with the same spec; a Ready OutOfSync cell
// is stop + spec re-materialised from its lineage Config + start (implicit
// reconcile); a Stopped cell is equivalent to `kuke start cell <name>`; an
// error/partial-state cell is refused with a `kuke delete cell` pointer.
//
// The reconcile branch composes at CLI level over GetConfig + GetBlueprint +
// ApplyDocuments rather than introducing a new daemon-side RPC — keeps this
// change scoped to one CLI verb and avoids a wire-format addition. The daemon's
// ApplyDocuments only bounces containers when image/command/args change (see
// internal/controller/runner/update_cell.go:containerSpecChanged) — pure
// env-var or metadata-only divergence is patched in place. To preserve the
// "restart" verb's contract ("all running containers bounce"), the CLI follows
// the ApplyDocuments call with an explicit StopCell + StartCell whenever the
// reconcile did not itself bounce everything (root-container recreate or
// from-Stopped rematerialize are the only paths that already bounce all
// containers — see reconcileBouncedAll). The AC's user-facing contract holds
// either way (per the issue's open-question note).
func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cell [name]",
		Aliases: []string{"ce"},
		Short:   "Restart a cell (bounces the process; on OutOfSync also reconciles from Config)",
		Long: "Restart a cell. By default bounces the cell's containers " +
			"(stop + start with the same spec). When the cell carries a " +
			"`kukeon.io/config=<name>` lineage label and the daemon has " +
			"marked it OutOfSync, the start step uses the freshly " +
			"materialised spec from the Config so the restart is also a " +
			"reconcile. Severs any active attach session as a side effect " +
			"of the stop step.",
		Args:          cobra.ExactArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRestartCell,
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

func runRestartCell(cmd *cobra.Command, args []string) error {
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
		return restartReady(cmd, client, doc, pre)
	case v1beta1.CellStateStopped:
		return restartStopped(cmd, client, doc, pre)
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

// restartReady handles the Ready cell path: vanilla stop+start for Synced
// cells, or a Config-driven reconcile (stop + re-materialise + start) driven
// by the daemon for OutOfSync cells. OutOfSyncError (divergence undecidable
// — referenced Blueprint missing) and the "lineage Config deleted" reason
// cannot drive a reconcile, so they fall through to vanilla restart with a
// one-line stderr notice. Active attach sessions are severed by the daemon's
// stop step in both branches.
func restartReady(
	cmd *cobra.Command, client kukeonv1.Client,
	doc v1beta1.CellDoc, pre kukeonv1.GetCellResult,
) error {
	if pre.Cell.Status.OutOfSyncError != "" {
		cmd.PrintErrf(
			"notice: cell %q OutOfSync detection failed (%s); bouncing without reconcile\n",
			doc.Metadata.Name, pre.Cell.Status.OutOfSyncError,
		)
		return restartInPlace(cmd, client, doc)
	}
	if !pre.Cell.Status.OutOfSync {
		return restartInPlace(cmd, client, doc)
	}

	configName := strings.TrimSpace(pre.Cell.Metadata.Labels[cellconfig.LabelConfig])
	if configName == "" {
		// OutOfSync was set on a cell without a Config lineage label —
		// nothing to reconcile against. Fall through to vanilla restart.
		cmd.PrintErrf(
			"notice: cell %q is OutOfSync but carries no %s label; bouncing without reconcile\n",
			doc.Metadata.Name, cellconfig.LabelConfig,
		)
		return restartInPlace(cmd, client, doc)
	}

	cellDoc, err := materialiseFromConfig(cmd.Context(), client, doc, configName)
	if err != nil {
		// "lineage Config deleted" lands here (ErrConfigNotFound) and so
		// does a missing Blueprint. Either way no reconcile is possible.
		// Surface the cause and fall through to a vanilla restart so the
		// cell's runtime is still bounced as the operator asked.
		cmd.PrintErrf(
			"notice: cell %q is OutOfSync but reconcile materialisation failed (%v); bouncing without reconcile\n",
			doc.Metadata.Name, err,
		)
		return restartInPlace(cmd, client, doc)
	}

	rawYAML, marshalErr := yaml.Marshal(&cellDoc)
	if marshalErr != nil {
		return fmt.Errorf("marshal materialised cell: %w", marshalErr)
	}
	result, applyErr := client.ApplyDocuments(cmd.Context(), rawYAML)
	if applyErr != nil {
		return applyErr
	}
	if failErr := reportApplyFailures(result); failErr != nil {
		return failErr
	}
	// Honor the restart contract: the daemon's reconcile only bounces
	// containers when image/command/args change (update_cell.go
	// containerSpecChanged), so pure env-var or metadata-only divergence
	// leaves running containers untouched. Add an explicit StopCell +
	// StartCell unless the reconcile already bounced every container (full
	// root-container recreate or from-Stopped rematerialize), so the user's
	// `kuke restart` invariant — running containers always bounce — holds
	// across every divergence class. The double-bounce in the rare
	// reconcile-already-bounced-everything case is acceptable: bouncing twice
	// is still a restart; under-bouncing silently breaks the contract.
	if !reconcileBouncedAll(result) {
		if _, stopErr := client.StopCell(cmd.Context(), doc); stopErr != nil {
			return stopErr
		}
		if _, startErr := client.StartCell(cmd.Context(), doc); startErr != nil {
			return startErr
		}
	}
	cmd.Printf("Restarted cell %q from stack %q (reconciled from config %q)\n",
		doc.Metadata.Name, doc.Spec.StackID, configName)
	return nil
}

// reconcileBouncedAll reports whether the daemon-side ApplyDocuments already
// bounced every running container in the cell. The signals are intentionally
// narrow: only full-cell paths qualify. A non-root container's image bump in
// internal/controller/runner/update_cell.go does stop+delete+create+start on
// that one container, but leaves the root and other containers running — that
// is NOT "bounced all". The conservative default (anything not on this list)
// triggers the follow-up StopCell + StartCell in restartReady so the restart
// contract is preserved.
func reconcileBouncedAll(result kukeonv1.ApplyDocumentsResult) bool {
	for _, r := range result.Resources {
		if r.Kind != string(v1beta1.KindCell) {
			continue
		}
		// Action "created" cannot fire in the restart path (the cell already
		// exists or we would have refused at the cell-not-found gate) but is
		// listed for defensive parity — a fresh create starts every container.
		if r.Action == "created" {
			return true
		}
		for _, change := range r.Changes {
			// "root container recreated" comes from reconcile.go's
			// RecreateCell branch (diff.RootContainerChanged). RecreateCell
			// tears down and rebuilds the entire cell, so every container is
			// freshly started.
			//
			// "runtime stopped: containers re-materialized" comes from
			// reconcile.go's rematerializeChanges, fired when the apply runs
			// against a Stopped cell with no spec diff — StartCell brings
			// every container up. This branch cannot fire in the restart
			// path either (we route Stopped through restartStopped), but
			// is listed for the same defensive parity.
			if change == "root container recreated" ||
				change == "runtime stopped: containers re-materialized" {
				return true
			}
		}
	}
	return false
}

// restartInPlace bounces the cell with the same stored spec: stop then start.
// The CellDoc only needs name + scope — the daemon resolves both verbs from
// the stored cell metadata, so spec.containers and friends do not need to be
// re-supplied (preserving "all spec fields" is automatic).
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
// `kuke start cell <name>`. AC pins this even for OutOfSync stopped cells —
// reconcile happens only on the Ready+OutOfSync path; an operator wanting to
// reconcile from Stopped starts the cell first and then re-runs
// `kuke restart cell <name>` to trip the Ready+OutOfSync reconcile.
func restartStopped(
	cmd *cobra.Command, client kukeonv1.Client,
	doc v1beta1.CellDoc, _ kukeonv1.GetCellResult,
) error {
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

// materialiseFromConfig re-runs the CellConfig materialisation pipeline
// against the cell's lineage Config: GetConfig + GetBlueprint +
// cellconfig.Materialize. The cell's stored realm/space/stack supply the
// lookup scope — a Config materialises a cell into the scope it lives in,
// matching the daemon's reconciler (internal/controller/reconcile_outofsync.go).
func materialiseFromConfig(
	ctx context.Context, client kukeonv1.Client,
	cellDoc v1beta1.CellDoc, configName string,
) (v1beta1.CellDoc, error) {
	cfgRes, err := client.GetConfig(ctx, v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  configName,
			Realm: cellDoc.Spec.RealmID,
			Space: cellDoc.Spec.SpaceID,
			Stack: cellDoc.Spec.StackID,
		},
	})
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !cfgRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (config %q in realm=%q space=%q stack=%q)",
			errdefs.ErrConfigNotFound, configName,
			cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID, cellDoc.Spec.StackID,
		)
	}

	bpRef := cfgRes.Config.Spec.Blueprint
	bpRes, err := client.GetBlueprint(ctx, v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  bpRef.Name,
			Realm: bpRef.Realm,
			Space: bpRef.Space,
			Stack: bpRef.Stack,
		},
	})
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !bpRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (blueprint %q referenced by config %q)",
			errdefs.ErrBlueprintNotFound, bpRef.Name, configName,
		)
	}

	return cellconfig.Materialize(cfgRes.Config, bpRes.Blueprint)
}

// reportApplyFailures inspects an ApplyDocumentsResult for "failed" rows and
// returns a non-nil error so the CLI exits non-zero on a daemon-side
// reconcile failure. Matches `kuke apply`'s behaviour
// (cmd/kuke/apply/apply.go:468-495 printApplyResult).
func reportApplyFailures(result kukeonv1.ApplyDocumentsResult) error {
	for _, r := range result.Resources {
		if r.Action == "failed" {
			return fmt.Errorf("%w: reconcile failed for %s %q: %s",
				errdefs.ErrConfig, r.Kind, r.Name, r.Error)
		}
	}
	return nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.ClientFromCmd(cmd)
}
