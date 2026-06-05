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
func runClone(cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags) error {
	src, err := resolveSourceCell(cmd, client, flags)
	if err != nil {
		return err
	}
	srcProv := src.Spec.Provenance
	if srcProv == nil {
		return fmt.Errorf(
			"cannot clone cell %q: it carries no materialization provenance "+
				"(only cells created from a Blueprint or Config can be cloned)",
			src.Metadata.Name,
		)
	}

	var cellDoc v1beta1.CellDoc
	switch srcProv.BindingKind {
	case v1beta1.BindingKindConfig:
		cellDoc, err = cloneFromConfig(cmd, client, flags, srcProv)
	case v1beta1.BindingKindBlueprint:
		cellDoc, err = cloneFromBlueprint(cmd, client, flags, srcProv)
	default:
		return fmt.Errorf(
			"cannot clone cell %q: unrecognized provenance bindingKind %q",
			src.Metadata.Name, srcProv.BindingKind,
		)
	}
	if err != nil {
		return err
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
	if err = finalizeCellName(cmd, client, &cellDoc, flags.name, src.Metadata.Name); err != nil {
		return err
	}
	applyIgnoreDiskPressure(&cellDoc, flags)

	return materialiseAndPersist(cmd, client, cellDoc)
}

// resolveSourceCell fetches the cell named by --clone at the operator's
// --realm/--space/--stack scope. Unlike the from-blueprint / from-config
// binding lookups (which use ExplicitScope so a realm-scoped binding stays
// findable with empty space/stack), a cell always lives at a full
// realm/space/stack, so the source lookup uses the defaulted flags scope
// (default/default/default when the operator passes no coordinate). A missing
// source cell is a clear ErrCellNotFound.
func resolveSourceCell(
	cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags,
) (v1beta1.CellDoc, error) {
	lookup := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: flags.cloneSource},
		Spec: v1beta1.CellSpec{
			RealmID: flags.realm,
			SpaceID: flags.space,
			StackID: flags.stack,
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
func cloneFromConfig(
	cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags, srcProv *v1beta1.CellProvenance,
) (v1beta1.CellDoc, error) {
	if len(flags.paramArgs) > 0 {
		return v1beta1.CellDoc{}, errors.New(
			"--param is not valid when cloning a Config-lineage cell; the lineage Config carries " +
				"its own spec.values (edit the Config instead)",
		)
	}
	if flags.paramFile != "" {
		return v1beta1.CellDoc{}, errors.New(
			"--param-file is not valid when cloning a Config-lineage cell; the lineage Config " +
				"carries its own spec.values (edit the Config instead)",
		)
	}

	ref := srcProv.BindingRef
	cfgLookup := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	}
	cfgRes, err := client.GetConfig(cmd.Context(), cfgLookup)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !cfgRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (lineage config %q in scope realm=%q space=%q stack=%q referenced by clone source)",
			errdefs.ErrConfigNotFound, ref.Name, ref.Realm, ref.Space, ref.Stack,
		)
	}

	bpRef := cfgRes.Config.Spec.Blueprint
	bpRes, err := client.GetBlueprint(cmd.Context(), v1beta1.CellBlueprintDoc{
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
			"%w (blueprint %q referenced by lineage config %q)",
			errdefs.ErrBlueprintNotFound, bpRef.Name, cfgRes.Config.Metadata.Name,
		)
	}

	cellDoc, err := cellconfig.MaterializeWithName(cfgRes.Config, bpRes.Blueprint, flags.name)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	// Copy the source's provenance verbatim (AC#1), then re-bake its env
	// overrides plus any additional --env (AC#6). applyEnvOverrides reads
	// Spec.Provenance.EnvOverrides as the merged set, so set provenance first.
	cellDoc.Spec.Provenance = v1beta1.CloneCellProvenance(srcProv)
	mergedEnv := mergeEnv(srcProv.EnvOverrides, flags.envArgs)
	applyEnvOverrides(&cellDoc, mergedEnv)
	return cellDoc, nil
}

// cloneFromBlueprint re-materialises a blueprint-lineage clone from the
// source's provenance binding ref. --env is rejected (its symmetric
// counterpart, mirroring the --from-blueprint path); --param/--param-file stack
// on top of the source's recorded params (last-write-wins). The returned doc's
// Spec.Provenance is the source's copied verbatim, with the merged params
// recorded — byte-equal to the source when no extra --param is given.
func cloneFromBlueprint(
	cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags, srcProv *v1beta1.CellProvenance,
) (v1beta1.CellDoc, error) {
	if len(flags.envArgs) > 0 {
		return v1beta1.CellDoc{}, errors.New(
			"--env is not valid when cloning a Blueprint-lineage cell; clone a Config-lineage cell " +
				"(or edit the Blueprint) to layer env overrides",
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
	mergedParams := mergeParamMaps(srcProv.Params, cliParams)

	resolved, err := cellblueprint.Resolve(bpRes.Blueprint, mergedParams, os.LookupEnv)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	cellDoc, err := cellblueprint.MaterializeWithName(resolved, flags.name, mergedParams)
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

// mergeParamMaps layers override params on top of base params (override wins on
// a key collision, last-write-wins per AC#6). Returns a fresh map; never
// mutates either input. A nil/empty result stays nil so a no-override clone's
// provenance is byte-equal to the source's.
func mergeParamMaps(base, override map[string]string) map[string]string {
	if len(base) == 0 && len(override) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}

// setSourceCellAnnotation stamps the inert kukeon.io/source-cell provenance
// annotation on the clone, recording the cell it was forked from (#1073).
func setSourceCellAnnotation(doc *v1beta1.CellDoc, sourceName string) {
	if doc.Metadata.Annotations == nil {
		doc.Metadata.Annotations = map[string]string{}
	}
	doc.Metadata.Annotations[AnnotationSourceCell] = sourceName
}
