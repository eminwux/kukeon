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
	"os"
	"strings"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

// AnnotationSourceCell is the inert provenance annotation a clone carries,
// recording the cell it was forked from (epic:cell-identity #1073). Distinct
// from the load-bearing kukeon.io/config / kukeon.io/blueprint lineage labels:
// no reconcile or selector path keys off it, DiffCell does not compare it, and
// it pins no identity — it is debug/grooming metadata only. Stored under the
// `kukeon.io/` prefix (not the `.kukeon.io` controller-managed suffix) so it
// reads as cell-authored provenance.
const AnnotationSourceCell = "kukeon.io/source-cell"

// optionalNameArgOrDefault resolves an optional cell name from the positional
// argument or the viper fallback, returning "" when neither is set. Unlike
// shared.RequireNameArgOrDefault it does not error on an omitted name — the
// clone source kind auto-generates `<source-name>-<6hex>` for an empty name
// (AC#2 of #1073).
func optionalNameArgOrDefault(args []string, fallback string) string {
	if len(args) > 0 {
		if name := strings.TrimSpace(args[0]); name != "" {
			return name
		}
	}
	return strings.TrimSpace(fallback)
}

// runClone implements the third source kind, `kuke create cell --clone <src>`
// (epic:cell-identity #1073). It forks an existing cell's recipe into a new
// cell:
//
//  1. Resolve the source cell at the operator's --realm/--space/--stack scope
//     and read its Spec.Provenance — the persisted record (P1) of the binding
//     it was materialised from plus any per-cell --param/--env overrides. A
//     source with no provenance (a hand-built cell never materialised from a
//     binding) cannot be cloned.
//  2. Re-materialise a fresh spec from that same binding (the same Config /
//     Blueprint materialisation the --from-config / --from-blueprint paths
//     run), re-applying the source's recorded params/env overrides and stacking
//     any additional --param/--env from the clone command on top
//     (last-write-wins, AC#6). The per-path override symmetry from P3 holds:
//     --env stacks on a config-lineage clone (--param rejected); --param stacks
//     on a blueprint-lineage clone (--env rejected).
//  3. Copy the source's Spec.Provenance onto the clone so a plain clone (no
//     extra overrides) is byte-equal to the source's (AC#1); stacked overrides
//     are merged into that copy.
//  4. Inherit the lineage label (set by materialisation), stamp the
//     kukeon.io/source-cell annotation, target the source cell's scope, and
//     finalise the name (<source-name>-<6hex> when omitted; verbatim when
//     explicit) before the collision-checked persist.
func materializeClone(cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags) (v1beta1.CellDoc, error) {
	src, err := resolveSourceCell(cmd, client, flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	srcProv := src.Spec.Provenance
	if srcProv == nil {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"cannot clone cell %q: it carries no materialization provenance "+
				"(only cells created from a Blueprint or Config can be cloned)",
			src.Metadata.Name,
		)
	}

	var cellDoc v1beta1.CellDoc
	switch srcProv.BindingKind {
	case v1beta1.BindingKindConfig:
		// Settle the clone's name in the source's scope before the daemon
		// renders it, so per-cell (${CELL_NAME}) volumes are named after the
		// clone itself.
		var name string
		name, err = kukeshared.ResolveCellName(
			cmd.Context(), client, flags.Name, src.Metadata.Name,
			src.Spec.RealmID, src.Spec.SpaceID, src.Spec.StackID,
		)
		if err != nil {
			return v1beta1.CellDoc{}, err
		}
		cellDoc, err = cloneFromConfig(cmd, client, flags, src, name)
	case v1beta1.BindingKindBlueprint:
		cellDoc, err = cloneFromBlueprint(cmd, client, flags, srcProv)
	default:
		return v1beta1.CellDoc{}, fmt.Errorf(
			"cannot clone cell %q: unrecognized provenance bindingKind %q",
			src.Metadata.Name, srcProv.BindingKind,
		)
	}
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	// The clone targets the source cell's scope (realm/space/stack), not the
	// binding's — cross-scope cloning is out of scope for #1073.
	cellDoc.Spec.RealmID = src.Spec.RealmID
	cellDoc.Spec.SpaceID = src.Spec.SpaceID
	cellDoc.Spec.StackID = src.Spec.StackID

	setSourceCellAnnotation(&cellDoc, src.Metadata.Name)

	// Prefix derives from the source cell's metadata.name: a materialized cell
	// carries no Spec.Prefix field (only the Blueprint/Config bindings do), so
	// the proposal's `Spec.Prefix ?? metadata.name` collapses to metadata.name.
	explicitName := flags.Name
	if srcProv.BindingKind == v1beta1.BindingKindConfig {
		explicitName = cellDoc.Metadata.Name
	}
	if err = finalizeCellName(cmd, client, &cellDoc, explicitName, src.Metadata.Name); err != nil {
		return v1beta1.CellDoc{}, err
	}
	applyIgnoreDiskPressure(&cellDoc, flags)
	return cellDoc, nil
}

// resolveSourceCell fetches the cell named by --clone at the operator's
// --realm/--space/--stack scope. Like the from-blueprint / from-config binding
// lookups (which default an unset --space/--stack to "default" via
// ExplicitScope per issue #1156), a cell always lives at a full
// realm/space/stack, so the source lookup uses the defaulted flags scope
// (default/default/default when the operator passes no coordinate). A missing
// source cell is a clear ErrCellNotFound.
func resolveSourceCell(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags,
) (v1beta1.CellDoc, error) {
	lookup := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: flags.CloneSource},
		Spec: v1beta1.CellSpec{
			RealmID: flags.Realm,
			SpaceID: flags.Space,
			StackID: flags.Stack,
		},
	}
	res, err := client.GetCell(cmd.Context(), lookup)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return v1beta1.CellDoc{}, fmt.Errorf(
				"%w (source cell %q in scope realm=%q space=%q stack=%q)",
				errdefs.ErrCellNotFound, lookup.Metadata.Name,
				lookup.Spec.RealmID, lookup.Spec.SpaceID, lookup.Spec.StackID,
			)
		}
		return v1beta1.CellDoc{}, err
	}
	if !res.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (source cell %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrCellNotFound, lookup.Metadata.Name,
			lookup.Spec.RealmID, lookup.Spec.SpaceID, lookup.Spec.StackID,
		)
	}
	return res.Cell, nil
}

// cloneFromConfig re-materialises a config-lineage clone from the source's
// provenance binding ref. --param/--param-file are rejected (the Config carries
// its own spec.values, mirroring the --from-config path); --env stacks on top
// of the source's recorded env overrides (last-write-wins). The returned doc's
// Spec.Provenance is the source's copied verbatim, with the merged env
// overrides recorded — byte-equal to the source when no extra --env is given.
// The clone is rendered in the source cell's space and stack (#1603), which
// may lie below the Config's scope.
func cloneFromConfig(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, src v1beta1.CellDoc, name string,
) (v1beta1.CellDoc, error) {
	srcProv := src.Spec.Provenance
	// --param stacks on the source's recorded per-cell params; like on the
	// --from-config path the daemon accepts only the picked mode's parameters.
	params := map[string]string{}
	for k, v := range srcProv.ParamOverrides {
		params[k] = v
	}
	if len(flags.ParamArgs) > 0 {
		extra, parseErr := cellblueprint.ParseParamArgs(flags.ParamArgs)
		if parseErr != nil {
			return v1beta1.CellDoc{}, parseErr
		}
		for k, v := range extra {
			params[k] = v
		}
	}
	mode := srcProv.Mode
	if flags.Mode != "" {
		mode = flags.Mode
	}
	if flags.ParamFile != "" {
		return v1beta1.CellDoc{}, errors.New(
			"--param-file is not valid when cloning a Config-lineage cell; the lineage Config " +
				"carries its own spec.values (edit the Config instead)",
		)
	}

	// The daemon renders the clone from the lineage Config (#1418) — the
	// same defaults merge and slot fills as --from-config — with the source's
	// recorded env overrides plus any additional --env re-baked (AC#6).
	ref := srcProv.BindingRef
	mergedEnv := cellconfig.MergeEnv(srcProv.EnvOverrides, flags.EnvArgs)
	preview, err := client.PreviewCellFromConfig(cmd.Context(), kukeonv1.CellFromConfigRequest{
		Config: v1beta1.CellConfigMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
		Name:         name,
		EnvOverrides: mergedEnv,
		Mode:         mode,
		Params:       params,
		// The clone keeps the identities the source cell picked (#1548),
		// login identities — Volumes — included (#1685).
		ConnectorOverrides: cloneConnectorOverrides(srcProv),
		// ...and its harness state choice (#1685).
		State: srcProv.State,
		// ...and the methods it picked (#1551).
		ConnectorMethods: srcProv.ConnectorMethods,
		// ...and renders where the source cell lives (#1603).
		TargetSpace: src.Spec.SpaceID,
		TargetStack: src.Spec.StackID,
	})
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return v1beta1.CellDoc{}, fmt.Errorf(
				"%w (lineage config %q in scope realm=%q space=%q stack=%q referenced by clone source)",
				errdefs.ErrConfigNotFound, ref.Name, ref.Realm, ref.Space, ref.Stack,
			)
		}
		return v1beta1.CellDoc{}, err
	}
	cellDoc := preview.Cell

	// Copy the source's provenance verbatim (AC#1) with the merged env
	// overrides recorded — byte-equal to the source when no extra --env.
	rendered := cellDoc.Spec.Provenance
	cellDoc.Spec.Provenance = v1beta1.CloneCellProvenance(srcProv)
	if len(mergedEnv) > 0 {
		cellDoc.Spec.Provenance.EnvOverrides = mergedEnv
	}
	// The clone's mode and per-cell params are what the daemon just rendered
	// (the source's, unless --mode / --param overrode them).
	if rendered != nil {
		cellDoc.Spec.Provenance.Mode = rendered.Mode
		cellDoc.Spec.Provenance.ParamOverrides = rendered.ParamOverrides
		cellDoc.Spec.Provenance.ConnectorOverrides = rendered.ConnectorOverrides
		cellDoc.Spec.Provenance.ConnectorMethods = rendered.ConnectorMethods
		cellDoc.Spec.Provenance.ConnectorVolumes = rendered.ConnectorVolumes
		cellDoc.Spec.Provenance.State = rendered.State
	}
	return cellDoc, nil
}

// cloneConnectorOverrides rebuilds the `--connector` overrides a source cell
// was created with from its provenance: the Secrets and the login-identity
// Volumes. The daemon tells them apart again by the method each connector
// uses.
func cloneConnectorOverrides(prov *v1beta1.CellProvenance) map[string]v1beta1.ContainerSecretRef {
	if len(prov.ConnectorOverrides) == 0 && len(prov.ConnectorVolumes) == 0 {
		return nil
	}
	out := make(map[string]v1beta1.ContainerSecretRef, len(prov.ConnectorOverrides)+len(prov.ConnectorVolumes))
	for name, ref := range prov.ConnectorOverrides {
		out[name] = ref
	}
	for name, ref := range prov.ConnectorVolumes {
		out[name] = v1beta1.ContainerSecretRef{Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack}
	}
	return out
}

// cloneFromBlueprint re-materialises a blueprint-lineage clone from the
// source's provenance binding ref. --env is rejected (its symmetric
// counterpart, mirroring the --from-blueprint path); --param/--param-file stack
// on top of the source's recorded params (last-write-wins). The returned doc's
// Spec.Provenance is the source's copied verbatim, with the merged params
// recorded — byte-equal to the source when no extra --param is given.
func cloneFromBlueprint(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, srcProv *v1beta1.CellProvenance,
) (v1beta1.CellDoc, error) {
	if len(flags.EnvArgs) > 0 {
		return v1beta1.CellDoc{}, errors.New(
			"--env is not valid when cloning a Blueprint-lineage cell; clone a Config-lineage cell " +
				"(or edit the Blueprint) to layer env overrides",
		)
	}
	if flags.Mode != "" {
		return v1beta1.CellDoc{}, errors.New(
			"--mode is not valid when cloning a Blueprint-lineage cell; a harness mode is picked " +
				"from the modes a CellConfig enables",
		)
	}

	ref := srcProv.BindingRef
	bpRes, err := client.GetBlueprint(cmd.Context(), v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	})
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !bpRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (lineage blueprint %q in scope realm=%q space=%q stack=%q referenced by clone source)",
			errdefs.ErrBlueprintNotFound, ref.Name, ref.Realm, ref.Space, ref.Stack,
		)
	}

	// Stack additional --param/--param-file on top of the source's recorded
	// params (last-write-wins via MergeParams' CLI-wins contract).
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	mergedParams := cellblueprint.MergeParams(srcProv.Params, cliParams)

	resolved, err := cellblueprint.Resolve(bpRes.Blueprint, mergedParams, os.LookupEnv)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	cellDoc, err := cellblueprint.MaterializeWithName(resolved, flags.Name, mergedParams)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	// Copy the source's provenance verbatim (AC#1), then record the merged
	// params (AC#6) — byte-equal to the source when no additional --param.
	cellDoc.Spec.Provenance = v1beta1.CloneCellProvenance(srcProv)
	if len(mergedParams) > 0 {
		cellDoc.Spec.Provenance.Params = mergedParams
	}
	return cellDoc, nil
}

// setSourceCellAnnotation stamps the inert kukeon.io/source-cell provenance
// annotation on the clone, recording the cell it was forked from (#1073).
func setSourceCellAnnotation(doc *v1beta1.CellDoc, sourceName string) {
	if doc.Metadata.Annotations == nil {
		doc.Metadata.Annotations = map[string]string{}
	}
	doc.Metadata.Annotations[AnnotationSourceCell] = sourceName
}
