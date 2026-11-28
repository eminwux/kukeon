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

package controller

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	outOfSyncReasonConfigDeleted = "lineage Config deleted"
	outOfSyncReasonSpecDiffers   = "spec differs"
)

// reconcileCellOutOfSync runs the per-cell OutOfSync detection pass for
// Config-lineage cells (issue #820, foundation phase of #819's umbrella).
//
// Cells without a `kukeon.io/config=<name>` lineage label are skipped (the
// `-b`-lineage and hand-built cases are deliberately out of scope per the
// umbrella). For a Config-lineage cell, the pass re-derives the would-be
// CellDoc from the daemon-stored Config + its referenced Blueprint via the
// same `cellconfig.MaterializeWithName` call the CellConfig materialisation
// path uses, then compares the materialized spec against the live cell with
// `apply.DiffCell`. The outcome lands on three status fields:
//
//   - OutOfSync = true with OutOfSyncReason describing the divergence when
//     the live spec differs from the materialized spec, or when the lineage
//     Config has been deleted (the operator removed it but the cell
//     remains).
//   - OutOfSync = false with empty Reason when the live spec matches the
//     materialized spec (Synced).
//   - OutOfSync = false with OutOfSyncError set when divergence is
//     undecidable — referenced Blueprint missing, slot-fill validation
//     failure, or any other materialization error. Distinct from OutOfSync
//     so `kuke get cell`'s SYNC column can report the error case separately
//     from a routine drift. The start-time reapply does not read it: it
//     renders the Config itself (issue #1569).
//
// Persists via a locked runner.PatchCellMetadata only when any of the three
// fields would change, so an already-Synced cell costs at most one Config + one
// Blueprint read per tick. The caller (ReconcileCells) treats a persisted
// write as a CellsUpdated bump.
func reconcileCellOutOfSync(r runner.Runner, cell intmodel.Cell) (bool, error) {
	configName, ok := configLineage(cell)
	if !ok {
		// Not a Config-lineage cell — leave all OutOfSync fields alone. A
		// cell that lost its lineage label between ticks keeps whatever
		// the previous tick wrote, which is the correct read: until a
		// human reconciles the labels, the daemon has no Config to
		// re-derive against.
		return false, nil
	}

	desired := desiredCellOutOfSync(r, cell, configName)
	return persistCellOutOfSync(r, cell, desired)
}

// cellOutOfSync bundles the three OutOfSync status fields the detection
// pass writes so the helper functions can return them as a unit.
type cellOutOfSync struct {
	OutOfSync bool
	Reason    string
	Err       string

	// ImageDigests, when non-empty, is the back-filled value of the cell's
	// render image-digests annotation: realm images whose digest was unknown
	// at render time (a registry ref not pulled yet) now record the digest
	// the namespace holds, so a later re-pull can be detected (#1418).
	ImageDigests string

	// BlueprintRevision, when non-empty, back-fills the cell's render
	// blueprint-revision annotation on a Synced cell rendered before the
	// annotation existed, so a later blueprint change is named for it too
	// (issue #1569), or one whose blueprint changed without changing the
	// render (issue #1772).
	BlueprintRevision string

	// ConfigRevision, when non-empty, back-fills the cell's render
	// config-revision annotation on a Synced cell that carries none or the
	// pre-#1569 form (legacyConfigRevision), so a later schedule edit is not
	// mistaken for a Config change. Like BlueprintRevision, it also records
	// a new revision whose edit left the render unchanged (issue #1772).
	ConfigRevision string

	// ValuesHash, when non-empty, back-fills the cell's render values-hash
	// annotation on a Synced cell whose fill changed without changing the
	// render (issue #1772).
	ValuesHash string
}

// hasBackfill reports whether the verdict carries an annotation back-fill.
func (o cellOutOfSync) hasBackfill() bool {
	return o.ImageDigests != "" || o.BlueprintRevision != "" || o.ConfigRevision != "" || o.ValuesHash != ""
}

// lineageDiff is one render of a Config-lineage cell from its lineage Config,
// compared with the cell as stored. The drift check (desiredCellOutOfSync) and
// the on-demand diff (DiffCellFromConfig) both build on renderLineageDiff, so
// the two can never disagree about what drifted (issue #1768).
type lineageDiff struct {
	// ConfigFound is false when the lineage Config was deleted; nothing else
	// is set then.
	ConfigFound bool
	// Desired is the cell re-rendered from the Config and its blueprint.
	Desired intmodel.Cell
	// Diff is apply.DiffCell(Desired, live).
	Diff apply.CellDiffResult
	// Drift is the render provenance drift (renderProvenanceDriftKeys),
	// without the drift that only records a new revision of an unchanged
	// render (dropMetadataOnlyDrift).
	Drift []provenanceDrift
}

// reasons returns the drift reasons in renderProvenanceDrift's order, then the
// "spec differs" summary when the spec diff has changes: the text the drift
// check stores as OutOfSyncReason, split into its parts.
func (d lineageDiff) reasons() []string {
	reasons := driftReasons(d.Drift)
	if d.Diff.HasChanges {
		reasons = append(reasons, outOfSyncSpecDifferReason(d.Diff))
	}
	return reasons
}

// renderLineageDiff looks up the cell's lineage Config, renders the cell from
// it (materializeCellFromConfig) and compares the result with the live cell.
// A deleted Config is not an error: it returns ConfigFound=false. A lookup or
// render failure returns the error whose text the drift check stores as
// OutOfSyncError. Nothing is written.
func renderLineageDiff(r runner.Runner, cell intmodel.Cell, configName string) (lineageDiff, error) {
	cfg, found, getErr := lookupLineageConfig(r, cell, configName)
	if getErr != nil {
		return lineageDiff{}, fmt.Errorf("read lineage config %q: %w", configName, getErr)
	}
	if !found {
		return lineageDiff{}, nil
	}

	desired, materializeErr := materializeCellFromConfig(r, cfg, bindingFromCell(cell))
	if materializeErr != nil {
		return lineageDiff{}, materializeErr
	}

	diff := apply.DiffCell(desired, cell)
	return lineageDiff{
		ConfigFound: true,
		Desired:     desired,
		Diff:        diff,
		Drift: dropMetadataOnlyDrift(
			renderProvenanceDriftKeys(desired, cell, legacyConfigRevision(cfg)), desired, cell, diff),
	}, nil
}

// isMetadataOnlyDriftKey reports whether key is a render input whose drift is
// only a record when the cell it renders is unchanged (issue #1772): a
// blueprint or Config document edit, or a fill change, that leaves the render
// as it was.
func isMetadataOnlyDriftKey(key string) bool {
	switch key {
	case cellconfig.AnnotationRenderBlueprintRevision,
		cellconfig.AnnotationRenderConfigRevision,
		cellconfig.AnnotationRenderValuesHash:
		return true
	}
	return false
}

// dropMetadataOnlyDrift removes blueprint, config and values drift from drift
// when the fresh render is the cell as it runs: apply.DiffCell reports no
// changes and the render digest (identity, labels, spec and realm image
// digests, cellDigest) equals the live cell's. The new revisions are then
// back-filled on the Synced cell (desiredCellOutOfSync) instead of marking it
// OutOfSync, which only a restart would clear and which would restart a
// workload for a change that alters nothing (issue #1772).
//
// Defaults and image drift are kept: a level the cell depends on moving is
// reported even when the render is unchanged, and a rebuilt image changes
// the render digest anyway. A live cell without a render digest (rendered
// before #1418) keeps all its drift, since nothing shows its render is the
// same.
func dropMetadataOnlyDrift(
	drift []provenanceDrift, desired, live intmodel.Cell, diff apply.CellDiffResult,
) []provenanceDrift {
	if len(drift) == 0 || diff.HasChanges {
		return drift
	}
	liveDigest := live.Metadata.Annotations[cellconfig.AnnotationRenderDigest]
	if liveDigest == "" || liveDigest != desired.Metadata.Annotations[cellconfig.AnnotationRenderDigest] {
		return drift
	}
	var kept []provenanceDrift
	for _, d := range drift {
		if !isMetadataOnlyDriftKey(d.Key) {
			kept = append(kept, d)
		}
	}
	return kept
}

// desiredCellOutOfSync computes the OutOfSync verdict the live cell
// should carry after this pass. The function is pure with respect to the
// runner reads it makes — no metadata writes — so it can be inspected in
// isolation by tests that pin the runner's responses.
func desiredCellOutOfSync(r runner.Runner, cell intmodel.Cell, configName string) cellOutOfSync {
	render, err := renderLineageDiff(r, cell, configName)
	if err != nil {
		return cellOutOfSync{Err: err.Error()}
	}
	if !render.ConfigFound {
		return cellOutOfSync{OutOfSync: true, Reason: outOfSyncReasonConfigDeleted}
	}

	desiredCell := render.Desired
	backfill := backfillImageDigests(desiredCell, cell)
	if !render.Diff.HasChanges && len(render.Drift) == 0 {
		return cellOutOfSync{
			ImageDigests:      backfill,
			BlueprintRevision: backfillStamp(desiredCell, cell, cellconfig.AnnotationRenderBlueprintRevision),
			ConfigRevision:    backfillStamp(desiredCell, cell, cellconfig.AnnotationRenderConfigRevision),
			ValuesHash:        backfillStamp(desiredCell, cell, cellconfig.AnnotationRenderValuesHash),
		}
	}
	return cellOutOfSync{
		OutOfSync:    true,
		Reason:       strings.Join(render.reasons(), "; "),
		ImageDigests: backfill,
	}
}

// backfillImageDigests returns the live cell's image-digests annotation with
// every unknown ("") digest replaced by the one the fresh render resolved, or
// "" when nothing is back-filled. A registry realm image that was not pulled
// at render time is pulled by the cell's create; recording its digest once it
// is known keeps imageDigestDrift able to see a later re-pull.
func backfillImageDigests(desired, live intmodel.Cell) string {
	liveValue, ok := live.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests]
	if !ok || liveValue == "" {
		return ""
	}
	liveDigests := parseImageDigests(liveValue)
	desiredDigests := parseImageDigests(desired.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests])
	changed := false
	for name, was := range liveDigests {
		if now := desiredDigests[name]; was == "" && now != "" {
			liveDigests[name] = now
			changed = true
		}
	}
	if !changed {
		return ""
	}
	names := make([]string, 0, len(liveDigests))
	for name := range liveDigests {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+liveDigests[name])
	}
	return strings.Join(parts, ",")
}

// backfillStamp returns the fresh render's value of a provenance annotation
// when the live cell's differs from it — absent (rendered before the stamp
// existed), in an older form, or a revision whose change left the render as it
// was (dropMetadataOnlyDrift, issue #1772) — or "" when they already agree
// (issue #1569). The caller only back-fills a Synced cell, for which the drift
// left after dropMetadataOnlyDrift is empty and apply.DiffCell reports no
// changes, so the recorded value is the one the cell's spec actually reflects.
// A live cell with no render provenance at all (rendered
// before #1418) is left alone, as backfillImageDigests leaves it: nothing
// compares its stamps.
func backfillStamp(desired, live intmodel.Cell, key string) string {
	if live.Metadata.Annotations[cellconfig.AnnotationRenderDigest] == "" {
		return ""
	}
	want := desired.Metadata.Annotations[key]
	if want == "" || live.Metadata.Annotations[key] == want {
		return ""
	}
	return want
}

// renderProvenanceDrift compares the render provenance annotations of a fresh
// render against the live cell's and returns one reason per drifted input,
// in a fixed order (issues #1418, #1569):
//
//   - "blueprint <name> changed" when the blueprint revision differs: the
//     blueprint's stored content changed under the same name, or the Config
//     now points at a different blueprint. <name> is the blueprint the fresh
//     render resolved;
//   - "config changed" when the content hash of the CellConfig document
//     (spec.schedule left out) differs;
//   - "values changed" when the hash of the merged fill (values, repo, secret
//     and git fills, after every level is folded in) differs;
//   - "<level> defaults changed" when the content hash of a Realm, Space or
//     Stack spec.defaults fill differs from the one the cell was rendered
//     with — even when the change does not alter the rendered spec, so the
//     operator sees that a level the cell depends on moved;
//   - "image <name> digest changed" when a realm image the cell uses now
//     resolves to a different digest (a rebuild under the same tag), which
//     apply.DiffCell cannot see because the container's image reference is
//     unchanged.
//
// The order is blueprint, config, values, then the realm, space and stack
// defaults, then images, so the same drift always yields the same reason and
// the persisted status stays stable across ticks. A live cell without the
// annotation for an input (rendered before that stamp existed, or not
// rendered from a Config) reports nothing for that input, and an image digest
// only counts when both sides know it, so a not-yet-pulled image never flaps
// the verdict.
//
// legacyConfigRev is the current Config document's pre-#1569 revision
// (legacyConfigRevision); a live config stamp equal to it is unchanged.
//
// It maps renderProvenanceDriftKeys to its reasons, so callers that need only
// the text keep their shape.
func renderProvenanceDrift(desired, live intmodel.Cell, legacyConfigRev string) []string {
	return driftReasons(renderProvenanceDriftKeys(desired, live, legacyConfigRev))
}

// provenanceDrift is one drifted render input: the reason
// renderProvenanceDrift reports for it, and the `kukeon.io/render-*`
// annotation that drifted (issue #1768). Several image reasons share the
// image-digests key.
type provenanceDrift struct {
	Key    string
	Reason string
}

// driftReasons returns the reasons of drift, in order.
func driftReasons(drift []provenanceDrift) []string {
	if len(drift) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(drift))
	for _, d := range drift {
		reasons = append(reasons, d.Reason)
	}
	return reasons
}

// renderProvenanceDriftKeys is renderProvenanceDrift with the annotation key
// each reason comes from, under the same rules and in the same order.
func renderProvenanceDriftKeys(desired, live intmodel.Cell, legacyConfigRev string) []provenanceDrift {
	var drift []provenanceDrift
	if liveRev := live.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]; liveRev != "" {
		desiredRev := desired.Metadata.Annotations[cellconfig.AnnotationRenderBlueprintRevision]
		if desiredRev != liveRev {
			name := blueprintRevisionName(desiredRev)
			if name == "" {
				name = blueprintRevisionName(liveRev)
			}
			drift = append(drift, provenanceDrift{
				Key: cellconfig.AnnotationRenderBlueprintRevision, Reason: "blueprint " + name + " changed",
			})
		}
	}
	if liveRev := live.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision]; liveRev != "" &&
		liveRev != desired.Metadata.Annotations[cellconfig.AnnotationRenderConfigRevision] &&
		liveRev != legacyConfigRev {
		drift = append(drift, provenanceDrift{Key: cellconfig.AnnotationRenderConfigRevision, Reason: "config changed"})
	}
	if liveHash := live.Metadata.Annotations[cellconfig.AnnotationRenderValuesHash]; liveHash != "" &&
		liveHash != desired.Metadata.Annotations[cellconfig.AnnotationRenderValuesHash] {
		drift = append(drift, provenanceDrift{Key: cellconfig.AnnotationRenderValuesHash, Reason: "values changed"})
	}
	levels := []struct{ level, key string }{
		{v1beta1.RenderOriginRealm, cellconfig.AnnotationRenderRealmDefaults},
		{v1beta1.RenderOriginSpace, cellconfig.AnnotationRenderSpaceDefaults},
		{v1beta1.RenderOriginStack, cellconfig.AnnotationRenderStackDefaults},
	}
	for _, l := range levels {
		liveStamp, ok := live.Metadata.Annotations[l.key]
		if !ok || liveStamp == "" {
			continue
		}
		if stampHash(liveStamp) != stampHash(desired.Metadata.Annotations[l.key]) {
			drift = append(drift, provenanceDrift{Key: l.key, Reason: l.level + " defaults changed"})
		}
	}
	for _, reason := range imageDigestDrift(desired, live) {
		drift = append(drift, provenanceDrift{Key: cellconfig.AnnotationRenderImageDigests, Reason: reason})
	}
	return drift
}

// imageDigestDrift returns one "image <name> digest changed" reason per realm
// image whose known digest differs between the fresh render and the live cell.
func imageDigestDrift(desired, live intmodel.Cell) []string {
	liveDigests := parseImageDigests(live.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests])
	desiredDigests := parseImageDigests(desired.Metadata.Annotations[cellconfig.AnnotationRenderImageDigests])
	names := make([]string, 0, len(desiredDigests))
	for name := range desiredDigests {
		names = append(names, name)
	}
	sort.Strings(names)
	var reasons []string
	for _, name := range names {
		was, now := liveDigests[name], desiredDigests[name]
		if was != "" && now != "" && was != now {
			reasons = append(reasons, "image "+name+" digest changed")
		}
	}
	return reasons
}

// configLineage returns the lineage Config name carried on the cell's
// `kukeon.io/config` label, if any. The second return is false for cells
// without the label (the bulk of the host — hand-built cells, `-b` and
// `-p` lineage cells are skipped per the umbrella's v1 scope).
func configLineage(cell intmodel.Cell) (string, bool) {
	name := strings.TrimSpace(cell.Metadata.Labels[cellconfig.LabelConfig])
	if name == "" {
		return "", false
	}
	return name, true
}

// provenanceConfigRef returns the exact identity of the Config a cell was
// rendered from, as recorded in Spec.Provenance.BindingRef (issue #1569). The
// second return is false for a cell without usable provenance — no
// provenance, a non-Config binding, or an empty BindingRef.Name — which
// falls back to the label-name probe. An empty BindingRef.Realm defaults to
// the cell's own realm.
func provenanceConfigRef(cell intmodel.Cell) (intmodel.CellConfigMetadata, bool) {
	prov := cell.Spec.Provenance
	if prov == nil || prov.BindingKind != v1beta1.BindingKindConfig {
		return intmodel.CellConfigMetadata{}, false
	}
	ref := prov.BindingRef
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return intmodel.CellConfigMetadata{}, false
	}
	realm := strings.TrimSpace(ref.Realm)
	if realm == "" {
		realm = strings.TrimSpace(cell.Spec.RealmName)
	}
	return intmodel.CellConfigMetadata{
		Name:  name,
		Realm: realm,
		Space: strings.TrimSpace(ref.Space),
		Stack: strings.TrimSpace(ref.Stack),
	}, true
}

// cellBoundToConfig reports whether a Config-lineage cell was rendered from
// the Config identified by md. It is the single "is this cell bound to this
// Config" comparison the OutOfSync lookup and the `kuke delete config`
// back-reference notice share (issue #1569):
//
//   - A cell with usable provenance matches only the exact scope its
//     BindingRef records (the same comparison cellIsScheduledBy makes), so a
//     same-named Config at another scope never shadows the real one.
//   - A cell without provenance (rendered before #1021) falls back to the
//     legacy match: lineage label == Config name, same realm, and the
//     Config's space/stack, when set, equal the cell's.
func cellBoundToConfig(cell intmodel.Cell, md intmodel.CellConfigMetadata) bool {
	name := strings.TrimSpace(md.Name)
	if label, ok := configLineage(cell); !ok || label != name {
		return false
	}
	realm := strings.TrimSpace(md.Realm)
	space := strings.TrimSpace(md.Space)
	stack := strings.TrimSpace(md.Stack)
	if ref, ok := provenanceConfigRef(cell); ok {
		return ref.Name == name && ref.Realm == realm && ref.Space == space && ref.Stack == stack
	}
	if strings.TrimSpace(cell.Spec.RealmName) != realm {
		return false
	}
	if space != "" && strings.TrimSpace(cell.Spec.SpaceName) != space {
		return false
	}
	if stack != "" && strings.TrimSpace(cell.Spec.StackName) != stack {
		return false
	}
	return true
}

// lookupLineageConfig fetches the daemon-stored Config the cell was rendered
// from, returning (cfg, true, nil) on hit, (zero, false, nil) when the
// operator deleted the Config (the canonical OutOfSync trigger the umbrella
// documents), or (zero, false, err) on any other error.
//
// A cell with usable provenance is looked up exactly once, at the scope its
// Spec.Provenance.BindingRef records (issue #1569). Not found there means the
// lineage Config was deleted; the lookup does not widen, so a same-named
// Config at a deeper or shallower scope can neither shadow the real one nor
// hide its deletion.
//
// Only a cell without provenance takes the legacy widening probe. Its
// `kukeon.io/config` lineage label records only the Config name, not the
// scope it was bound at, so the lookup probes progressively shallower scopes:
// full (realm/space/stack) → space-only (realm/space) → realm-only (realm).
// A realm-scoped Config can be referenced by a cell materialized at the full
// default scope (#921). Probes are deduplicated when the cell's space or
// stack is already empty.
func lookupLineageConfig(
	r runner.Runner, cell intmodel.Cell, configName string,
) (intmodel.CellConfig, bool, error) {
	if ref, ok := provenanceConfigRef(cell); ok {
		cfg, err := r.GetConfig(intmodel.CellConfig{Metadata: ref})
		if err == nil {
			return cfg, true, nil
		}
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return intmodel.CellConfig{}, false, nil
		}
		return intmodel.CellConfig{}, false, err
	}

	realm, space, stack := cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName

	type probe struct{ space, stack string }
	probes := []probe{{space, stack}}
	if stack != "" {
		probes = append(probes, probe{space, ""})
	}
	if space != "" {
		probes = append(probes, probe{"", ""})
	}

	for _, p := range probes {
		cfg, err := r.GetConfig(intmodel.CellConfig{
			Metadata: intmodel.CellConfigMetadata{
				Name:  configName,
				Realm: realm,
				Space: p.space,
				Stack: p.stack,
			},
		})
		if err == nil {
			return cfg, true, nil
		}
		if !errors.Is(err, errdefs.ErrConfigNotFound) {
			return intmodel.CellConfig{}, false, err
		}
	}
	return intmodel.CellConfig{}, false, nil
}

// materializeCellFromConfig re-runs the daemon-side CellConfig render
// (renderCellFromConfig, #1418) against a daemon-stored Config: decode the
// Config's body, resolve the referenced Blueprint, merge the Realm → Space →
// Stack defaults, then `cellconfig.MaterializeWithName`. Returns
// the materialized cell in the same internal-model shape the reconciler's live
// cell uses, so apply.DiffCell can compare them directly.
//
// The live cell's own name is threaded through as the materialized name so the
// diff never trips on metadata.name — materialization no longer derives the
// name from the Config name (epic:cell-identity #1021), and the OutOfSync
// detector compares *spec drift*, not identity. This keeps the detector
// correct now that P2 (#1022) lands generated cell names that no longer match
// the Config name (the legacy StableName pin, retired in P2).
//
// The binding is rebuilt from the cell's recorded provenance (bindingFromCell):
// its harness mode and --param overrides (#1472), and — as explained next — its
// env overrides. Its EnvOverrides carry `Spec.Provenance.EnvOverrides` (the
// per-cell `--env KEY=VALUE` baked at create time, P3 #1023). Re-resolving from
// the Config alone re-runs `MaterializeWithName`, which never re-applies those
// overrides — so without re-applying them here a cell created
// `--from-config --env K=V` reports a spurious OutOfSync (the override is in the
// live spec but not the freshly materialized one) and has it stripped on
// reapply. ApplyEnvOverrides re-bakes them last (P3 precedence) through the same
// helper the create and clone paths use (epic:cell-identity P5, #1024), so the
// re-materialized spec matches the live cell's attachable-container Env.
func materializeCellFromConfig(
	r runner.Runner, cfg intmodel.CellConfig, binding cellBinding,
) (intmodel.Cell, error) {
	rendered, err := renderCellFromConfig(r, cfg, binding)
	if err != nil {
		return intmodel.Cell{}, err
	}

	desired, convErr := apischeme.ConvertCellDocToInternal(rendered.Cell)
	if convErr != nil {
		return intmodel.Cell{}, fmt.Errorf("convert materialized cell: %w", convErr)
	}

	// Reproduce the runner-injected, space-derived container fields the
	// re-materialization does not, so apply.DiffCell does not report them as
	// spurious drift (#1136).
	if err := applyRunnerInjectedContainerFields(r, &desired); err != nil {
		return intmodel.Cell{}, err
	}

	return desired, nil
}

// applyRunnerInjectedContainerFields re-applies the space-derived container
// fields the runner bakes onto every persisted container spec at create/start
// but cellconfig.MaterializeWithName (Config + Blueprint only) never
// reproduces. Without this, apply.DiffCell flags those runner-injected values
// as spurious drift and a Config-lineage cell sticks on a permanent OutOfSync
// even though nothing actually changed (#1136). Two field classes are
// reproduced, mirroring provision.go's create/start path:
//
//   - Space container defaults (user, readOnlyRootFilesystem, capabilities,
//     securityOpts, tmpfs, resources) via ApplySpaceDefaultsToContainer — the
//     same helper applyCellContainerDefaults funnels every create flow
//     through. The `default` space declares no container defaults, so this is
//     dormant today but closes the latent class the moment a space sets one.
//   - The space/runtime-derived cniConfigPath, resolved by the same
//     ResolveSpaceCNIConfigPath the runner uses, set only on containers that
//     do not already carry an explicit one (mirroring the `if == ""` guard in
//     provision.go). This is the field firing today.
//
// The cell's own realm/space drive the lookups, matching the scope the runner
// resolved against at create time. A GetSpace / ResolveSpaceCNIConfigPath error
// surfaces (via the caller) as an undecidable OutOfSync (OutOfSyncError), the
// same class materialization errors already use; the runner-synthesized root
// container is untouched because DiffCell skips it.
func applyRunnerInjectedContainerFields(r runner.Runner, cell *intmodel.Cell) error {
	realm, space := cell.Spec.RealmName, cell.Spec.SpaceName

	parentSpace, err := r.GetSpace(intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: space},
		Spec:     intmodel.SpaceSpec{RealmName: realm},
	})
	if err != nil {
		return fmt.Errorf("resolve space %q defaults: %w", space, err)
	}

	cniConfigPath, err := r.ResolveSpaceCNIConfigPath(realm, space)
	if err != nil {
		return fmt.Errorf("resolve space %q cni config path: %w", space, err)
	}

	for i := range cell.Spec.Containers {
		intmodel.ApplySpaceDefaultsToContainer(parentSpace, &cell.Spec.Containers[i])
		if cell.Spec.Containers[i].CNIConfigPath == "" {
			cell.Spec.Containers[i].CNIConfigPath = cniConfigPath
		}
	}
	return nil
}

// outOfSyncSpecDifferReason renders the DiffCell verdict as a stable,
// human-readable one-line summary suitable for the OutOfSyncReason status
// field. Always starts with "spec differs" so downstream parsers (a
// future `kuke get cell` SYNC column, the `kuke restart` verb) can
// recognize the divergence class without parsing the full diff payload.
// Field paths are sorted so the same diff produces the same reason
// across ticks, keeping the persisted status stable.
func outOfSyncSpecDifferReason(diff apply.CellDiffResult) string {
	fields := collectDiffFieldPaths(diff)
	if len(fields) == 0 {
		return outOfSyncReasonSpecDiffers
	}
	return fmt.Sprintf("%s at %s", outOfSyncReasonSpecDiffers, strings.Join(fields, ", "))
}

// collectDiffFieldPaths gathers every divergent field path the DiffCell
// result names, deduplicated and sorted. The set spans the cell-level
// changes, the root-container marker, per-container ChangedFields /
// BreakingChanges qualified by container name, and orphan container IDs.
func collectDiffFieldPaths(diff apply.CellDiffResult) []string {
	seen := make(map[string]struct{})
	add := func(path string) {
		if path == "" {
			return
		}
		seen[path] = struct{}{}
	}

	for _, f := range diff.ChangedFields {
		add(f)
	}
	for _, f := range diff.BreakingChanges {
		add(f)
	}
	if diff.RootContainerChanged {
		add("spec.rootContainer")
	}
	for _, cd := range diff.Containers {
		switch cd.Action {
		case "add":
			add(fmt.Sprintf("containers[%s] (added)", cd.Name))
		case "update":
			for _, f := range cd.ChangedFields {
				add(fmt.Sprintf("containers[%s].%s", cd.Name, f))
			}
			for _, f := range cd.BreakingChanges {
				add(fmt.Sprintf("containers[%s].%s", cd.Name, f))
			}
		}
	}
	for _, id := range diff.Orphans {
		add(fmt.Sprintf("containers[%s] (removed)", id))
	}

	fields := make([]string, 0, len(seen))
	for k := range seen {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	return fields
}

// persistCellOutOfSync writes the new OutOfSync verdict to the cell's
// metadata when any of the three status fields would change or an annotation
// is back-filled, and reports whether a write actually occurred. The no-write
// fast-path keeps a Synced fleet from spinning the metadata file once per tick.
//
// The write is a locked patch on a fresh read (runner.PatchCellMetadata,
// issue #1569), not a write of the snapshot the verdict was computed from: it
// changes only the three OutOfSync status fields and the back-filled
// annotations, so a stop, start or update that persisted after the tick
// listed the cell keeps its state. When another writer moved the cell's
// generation or annotations since that snapshot — so the verdict may be about
// an older spec or provenance — or the cell is gone, the write is skipped with
// no error; the next tick recomputes.
func persistCellOutOfSync(
	r runner.Runner, cell intmodel.Cell, desired cellOutOfSync,
) (bool, error) {
	if cell.Status.OutOfSync == desired.OutOfSync &&
		cell.Status.OutOfSyncReason == desired.Reason &&
		cell.Status.OutOfSyncError == desired.Err &&
		!desired.hasBackfill() {
		return false, nil
	}

	written := false
	err := r.PatchCellMetadata(cell, func(fresh *intmodel.Cell) bool {
		if fresh.Metadata.Generation != cell.Metadata.Generation ||
			!maps.Equal(fresh.Metadata.Annotations, cell.Metadata.Annotations) {
			return false
		}
		if desired.hasBackfill() {
			annotations := make(map[string]string, len(fresh.Metadata.Annotations))
			for k, v := range fresh.Metadata.Annotations {
				annotations[k] = v
			}
			if desired.ImageDigests != "" {
				annotations[cellconfig.AnnotationRenderImageDigests] = desired.ImageDigests
			}
			if desired.BlueprintRevision != "" {
				annotations[cellconfig.AnnotationRenderBlueprintRevision] = desired.BlueprintRevision
			}
			if desired.ConfigRevision != "" {
				annotations[cellconfig.AnnotationRenderConfigRevision] = desired.ConfigRevision
			}
			if desired.ValuesHash != "" {
				annotations[cellconfig.AnnotationRenderValuesHash] = desired.ValuesHash
			}
			fresh.Metadata.Annotations = annotations
		}
		fresh.Status.OutOfSync = desired.OutOfSync
		fresh.Status.OutOfSyncReason = desired.Reason
		fresh.Status.OutOfSyncError = desired.Err
		written = true
		return true
	})
	if errors.Is(err, errdefs.ErrCellNotFound) || errors.Is(err, errdefs.ErrStaleResource) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("persist OutOfSync status: %w", err)
	}
	return written, nil
}
