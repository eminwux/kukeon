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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/health"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	actionFailed = "failed"
)

// ApplyResult represents the result of applying a set of resources.
type ApplyResult struct {
	Resources []ResourceResult
}

// ResourceResult represents the result of applying a single resource.
type ResourceResult struct {
	Index   int
	Kind    string
	Name    string
	Action  string // "created", "updated", "unchanged", "failed"
	Error   error
	Changes []string
	Details map[string]string
}

// resourceResultJSON is a helper type for JSON/YAML serialization.
type resourceResultJSON struct {
	Index   int               `json:"index"             yaml:"index"`
	Kind    string            `json:"kind"              yaml:"kind"`
	Name    string            `json:"name"              yaml:"name"`
	Action  string            `json:"action"            yaml:"action"`
	Error   *string           `json:"error,omitempty"   yaml:"error,omitempty"`
	Changes []string          `json:"changes,omitempty" yaml:"changes,omitempty"`
	Details map[string]string `json:"details,omitempty" yaml:"details,omitempty"`
}

// MarshalJSON implements json.Marshaler for ResourceResult.
func (r ResourceResult) MarshalJSON() ([]byte, error) {
	result := resourceResultJSON{
		Index:   r.Index,
		Kind:    r.Kind,
		Name:    r.Name,
		Action:  r.Action,
		Changes: r.Changes,
		Details: r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return json.Marshal(result)
}

// MarshalYAML implements yaml.Marshaler for ResourceResult.
func (r ResourceResult) MarshalYAML() (interface{}, error) {
	result := resourceResultJSON{
		Index:   r.Index,
		Kind:    r.Kind,
		Name:    r.Name,
		Action:  r.Action,
		Changes: r.Changes,
		Details: r.Details,
	}
	if r.Error != nil {
		errMsg := r.Error.Error()
		result.Error = &errMsg
	}
	return result, nil
}

// ApplyDocuments applies a set of resource documents in dependency order.
// Documents are sorted: Realm → Space → Stack → Cell → Container.
// Returns a summary of actions taken for each resource.
//
// When team is non-empty (issue #1027 per-team prune apply), the daemon
// stamps `kukeon.io/team=<team>` on every applied CellBlueprint / CellConfig
// before persistence, and after the apply loop enumerates daemon-stored
// Blueprint / Config objects carrying the same team label, deleting those
// not in the applied set. The empty-string team preserves the historical
// no-stamp, no-prune behavior of `kuke apply -f`.
func (b *Exec) ApplyDocuments(docs []parser.Document, team string) (ApplyResult, error) {
	result := ApplyResult{
		Resources: make([]ResourceResult, 0, len(docs)),
	}

	// applied{Blueprint,Config}s collect (realm,space,stack,name) tuples
	// for every successfully-persisted Blueprint / Config in this apply,
	// so the post-loop prune step (team != "") can delete the same-team
	// daemon objects that fell out of the applied set.
	var appliedBlueprints, appliedConfigs []scopedRef

	// previousShared is what the team's configs referenced before this apply:
	// the candidates for shared-blueprint pruning afterwards.
	var previousShared []scopedRef
	if team != "" {
		var listErr error
		if previousShared, listErr = b.teamBlueprintRefs(team); listErr != nil {
			return result, listErr
		}
	}

	// Sort documents by dependency order
	sortedDocs := SortDocumentsByKind(docs, false)

	// Apply each document in order
	for _, doc := range sortedDocs {
		resourceResult := ResourceResult{
			Index:   doc.Index,
			Kind:    string(doc.Kind),
			Details: make(map[string]string),
		}

		// Convert to internal model and reconcile
		var reconcileResult applypkg.ReconcileResult
		var reconcileErr error

		switch doc.Kind {
		case v1beta1.KindRealm:
			if doc.RealmDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("realm document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			realm, _, err := apischeme.NormalizeRealm(*doc.RealmDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = realm.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileRealm(b.runner, realm)

		case v1beta1.KindSpace:
			if doc.SpaceDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("space document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			space, _, err := apischeme.NormalizeSpace(*doc.SpaceDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = space.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileSpace(b.runner, space)

		case v1beta1.KindStack:
			if doc.StackDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("stack document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			stack, _, err := apischeme.NormalizeStack(*doc.StackDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = stack.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileStack(b.runner, stack)

		case v1beta1.KindCell:
			if doc.CellDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("cell document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			cell, _, err := apischeme.NormalizeCell(*doc.CellDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = cell.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileCell(b.runner, cell)

		case v1beta1.KindContainer:
			if doc.ContainerDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("container document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			container, _, err := apischeme.NormalizeContainer(*doc.ContainerDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = container.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileContainer(b.runner, container)

		case v1beta1.KindSecret:
			if doc.SecretDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("secret document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			secret, _, err := apischeme.NormalizeSecret(*doc.SecretDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = secret.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileSecret(b.runner, secret)

		case v1beta1.KindCellBlueprint:
			if doc.CellBlueprintDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("blueprint document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			bpDoc := *doc.CellBlueprintDoc
			// A shared blueprint belongs to no team: every team in the realm
			// (and the web UI) may reference it, so it is never team-stamped
			// and only goes away once nothing references it (issue #1472).
			if team != "" && bpDoc.Metadata.Labels[v1beta1.LabelShared] != "true" {
				bpDoc.Metadata.Labels = stampTeamLabel(bpDoc.Metadata.Labels, team)
			}
			blueprint, _, err := apischeme.NormalizeCellBlueprint(bpDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = blueprint.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileBlueprint(b.runner, blueprint)
			if reconcileErr == nil {
				appliedBlueprints = append(appliedBlueprints, scopedRefFromMetadata(
					blueprint.Metadata.Name,
					blueprint.Metadata.Realm,
					blueprint.Metadata.Space,
					blueprint.Metadata.Stack,
				))
			}

		case v1beta1.KindCellConfig:
			if doc.CellConfigDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("config document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			cfgDoc := *doc.CellConfigDoc
			if team != "" {
				cfgDoc.Metadata.Labels = stampTeamLabel(cfgDoc.Metadata.Labels, team)
			}
			config, _, err := apischeme.NormalizeCellConfig(cfgDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = config.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileConfig(b.runner, config)
			if reconcileErr == nil {
				appliedConfigs = append(appliedConfigs, scopedRefFromMetadata(
					config.Metadata.Name,
					config.Metadata.Realm,
					config.Metadata.Space,
					config.Metadata.Stack,
				))
			}

		case v1beta1.KindVolume:
			if doc.VolumeDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("volume document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			// A Volume is deliberately not team-label-stamped or pruned: it is
			// decoupled from any cell and outlives the apply that created it, so
			// `kuke apply --prune` removing a volume would wipe persistent data.
			// Volume reclaim is owning-scope cascade purge only (#1018); the
			// `kukeon.io/team` lifecycle stays with the blueprint/config kinds.
			volume, _, err := apischeme.NormalizeVolume(*doc.VolumeDoc)
			if err != nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = fmt.Errorf("%w: %w", errdefs.ErrConversionFailed, err)
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			resourceResult.Name = volume.Metadata.Name
			reconcileResult, reconcileErr = applypkg.ReconcileVolume(b.runner, volume)

		case v1beta1.KindHealthReport:
			if doc.HealthReportDoc == nil {
				resourceResult.Action = actionFailed
				resourceResult.Error = errors.New("health report document is nil")
				result.Resources = append(result.Resources, resourceResult)
				continue
			}
			// A report is immutable and create-only (issue #1619): apply takes
			// the same path as SubmitHealthReport — kukeond assigns the name
			// and createdAt when they are left out, marks recurring findings,
			// rejects a stored name, and keeps the newest reports.
			resourceResult.Name = doc.HealthReportDoc.Metadata.Name
			reconcileResult, reconcileErr = b.applyHealthReport(*doc.HealthReportDoc)
			if reconcileErr == nil {
				resourceResult.Name = reconcileResult.Name
			}

		default:
			resourceResult.Action = actionFailed
			resourceResult.Error = fmt.Errorf("%w: %s", errdefs.ErrUnknownKind, doc.Kind)
			result.Resources = append(result.Resources, resourceResult)
			continue
		}

		if reconcileErr != nil {
			resourceResult.Action = actionFailed
			resourceResult.Error = reconcileErr
		} else {
			resourceResult.Action = reconcileResult.Action
			resourceResult.Changes = reconcileResult.Changes
			resourceResult.Details = reconcileResult.Details
		}

		result.Resources = append(result.Resources, resourceResult)
	}

	if team != "" {
		pruneResults, pruneErr := b.pruneTeamObjects(team, appliedBlueprints, appliedConfigs, previousShared)
		if pruneErr != nil {
			return result, pruneErr
		}
		result.Resources = append(result.Resources, pruneResults...)
	}

	return result, nil
}

// applyHealthReport stores a HealthReport document through the health store.
func (b *Exec) applyHealthReport(doc v1beta1.HealthReportDoc) (applypkg.ReconcileResult, error) {
	stored, err := health.NewStore(b.opts.RunPath).Submit(doc)
	if err != nil {
		return applypkg.ReconcileResult{}, err
	}
	return applypkg.ReconcileResult{
		Action: "created",
		Kind:   string(v1beta1.KindHealthReport),
		Name:   stored.Metadata.Name,
		Details: map[string]string{
			"realm":    stored.Metadata.Realm,
			"findings": strconv.Itoa(len(stored.Spec.Findings)),
		},
	}, nil
}

// scopedRef identifies one Blueprint or Config by its scope-coordinate tuple
// plus name. Two refs are equal under == when every field matches, so a
// `map[scopedRef]struct{}` is a cheap set for the prune-difference scan.
type scopedRef struct {
	Realm string
	Space string
	Stack string
	Name  string
}

func scopedRefFromMetadata(name, realm, space, stack string) scopedRef {
	return scopedRef{Realm: realm, Space: space, Stack: stack, Name: name}
}

// stampTeamLabel returns labels with `kukeon.io/team` set to team. A nil
// input is upgraded to a single-key map so the daemon never persists nil
// labels alongside a team stamp. The input is not mutated — apply iterates
// over caller-owned parser documents.
func stampTeamLabel(labels map[string]string, team string) map[string]string {
	out := make(map[string]string, len(labels)+1)
	for k, v := range labels {
		out[k] = v
	}
	out[v1beta1.LabelTeam] = team
	return out
}

// pruneTeamObjects deletes daemon-stored CellBlueprint / CellConfig objects
// carrying `kukeon.io/team=<team>` that the just-completed apply set did
// not include (issue #1027). The two enumeration calls walk every realm
// (an empty realm filter is the "all realms" subtree prefix in
// runner.{ListBlueprints,ListConfigs}); a label-mismatch entry is skipped
// silently. Each successful delete becomes a ResourceResult with
// Action="pruned" so callers can render the prune count without re-querying
// the daemon.
//
// Prune order is Config-then-Blueprint: a Config references a Blueprint at
// apply time, so removing the Config first cannot orphan a reference. (Both
// kinds independently support DeleteWithoutLiveCellTeardown, so the order
// is purely consistency-with-apply, not safety.)
func (b *Exec) pruneTeamObjects(
	team string, appliedBlueprints, appliedConfigs, previousShared []scopedRef,
) ([]ResourceResult, error) {
	appliedBP := refsToSet(appliedBlueprints)
	appliedCfg := refsToSet(appliedConfigs)

	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		return nil, fmt.Errorf("prune team %q: list configs: %w", team, err)
	}
	blueprints, err := b.runner.ListBlueprints("", "", "")
	if err != nil {
		return nil, fmt.Errorf("prune team %q: list blueprints: %w", team, err)
	}

	var out []ResourceResult

	for _, cfg := range configs {
		if cfg.Metadata.Labels[v1beta1.LabelTeam] != team {
			continue
		}
		ref := scopedRefFromMetadata(cfg.Metadata.Name, cfg.Metadata.Realm, cfg.Metadata.Space, cfg.Metadata.Stack)
		if _, kept := appliedCfg[ref]; kept {
			continue
		}
		pruneResult := ResourceResult{
			Kind:    "CellConfig",
			Name:    cfg.Metadata.Name,
			Action:  "pruned",
			Details: scopeDetails(cfg.Metadata.Realm, cfg.Metadata.Space, cfg.Metadata.Stack),
		}
		if delErr := b.runner.DeleteConfig(cfg); delErr != nil {
			pruneResult.Action = actionFailed
			pruneResult.Error = fmt.Errorf("prune team %q: delete config %q: %w", team, cfg.Metadata.Name, delErr)
		}
		out = append(out, pruneResult)
	}

	for _, bp := range blueprints {
		if bp.Metadata.Labels[v1beta1.LabelTeam] != team {
			continue
		}
		ref := scopedRefFromMetadata(bp.Metadata.Name, bp.Metadata.Realm, bp.Metadata.Space, bp.Metadata.Stack)
		if _, kept := appliedBP[ref]; kept {
			continue
		}
		pruneResult := ResourceResult{
			Kind:    "CellBlueprint",
			Name:    bp.Metadata.Name,
			Action:  "pruned",
			Details: scopeDetails(bp.Metadata.Realm, bp.Metadata.Space, bp.Metadata.Stack),
		}
		if delErr := b.runner.DeleteBlueprint(bp); delErr != nil {
			pruneResult.Action = actionFailed
			pruneResult.Error = fmt.Errorf("prune team %q: delete blueprint %q: %w", team, bp.Metadata.Name, delErr)
		}
		out = append(out, pruneResult)
	}

	shared, err := b.pruneSharedBlueprints(team, appliedBP, previousShared, blueprints)
	if err != nil {
		return nil, err
	}
	return append(out, shared...), nil
}

// teamBlueprintRefs lists the blueprints the team's stored CellConfigs
// reference. Taken before an apply, it is what the team used until now.
//
// A config that cannot be read is skipped here: the snapshot only nominates
// blueprints for pruning, so missing one merely leaves a blueprint behind —
// while failing would block the very re-apply that repairs the config.
func (b *Exec) teamBlueprintRefs(team string) ([]scopedRef, error) {
	refs, err := b.configBlueprintRefs(team, false)
	if err != nil {
		return nil, fmt.Errorf("team %q: %w", team, err)
	}
	return refs, nil
}

// configBlueprintRefs returns the blueprint each stored CellConfig references,
// restricted to one team's configs when team is non-empty. ListConfigs carries
// metadata only, so each config's document is read for its spec.blueprint.
// With strict set, an unreadable config fails the call rather than being
// skipped: the "is this blueprint still referenced" scan decides what to
// delete from this list, and a reference it cannot see must not read as
// "unreferenced".
func (b *Exec) configBlueprintRefs(team string, strict bool) ([]scopedRef, error) {
	configs, err := b.runner.ListConfigs("", "", "")
	if err != nil {
		return nil, fmt.Errorf("list configs: %w", err)
	}
	var out []scopedRef
	for _, listed := range configs {
		if team != "" && listed.Metadata.Labels[v1beta1.LabelTeam] != team {
			continue
		}
		stored, getErr := b.runner.GetConfig(listed)
		if getErr != nil {
			if errors.Is(getErr, errdefs.ErrConfigNotFound) || !strict {
				continue
			}
			return nil, fmt.Errorf("read config %q: %w", listed.Metadata.Name, getErr)
		}
		doc, convErr := apischeme.ConvertCellConfigToExternal(stored)
		if convErr != nil {
			if !strict {
				continue
			}
			return nil, fmt.Errorf("decode config %q: %w", listed.Metadata.Name, convErr)
		}
		ref := doc.Spec.Blueprint
		out = append(out, scopedRefFromMetadata(ref.Name, ref.Realm, ref.Space, ref.Stack))
	}
	return out, nil
}

// pruneSharedBlueprints removes the shared blueprints the team stopped using
// in this apply. A shared blueprint carries no team label, so the ordinary
// label scan never sees it; instead a blueprint the team's configs referenced
// before the apply and none of its documents reference now is deleted — unless
// any stored CellConfig (another team's, or a hand-written one) still
// references it.
func (b *Exec) pruneSharedBlueprints(
	team string, applied map[scopedRef]struct{}, previous []scopedRef, blueprints []intmodel.CellBlueprint,
) ([]ResourceResult, error) {
	if len(previous) == 0 {
		return nil, nil
	}
	inUse, err := b.configBlueprintRefs("", true)
	if err != nil {
		return nil, fmt.Errorf("prune team %q: %w", team, err)
	}
	referenced := refsToSet(inUse)
	var out []ResourceResult
	for ref := range refsToSet(previous) {
		if _, kept := applied[ref]; kept {
			continue
		}
		if _, inUse := referenced[ref]; inUse {
			continue
		}
		for _, bp := range blueprints {
			md := bp.Metadata
			if scopedRefFromMetadata(md.Name, md.Realm, md.Space, md.Stack) != ref ||
				md.Labels[v1beta1.LabelShared] != "true" {
				continue
			}
			pruneResult := ResourceResult{
				Kind:    "CellBlueprint",
				Name:    md.Name,
				Action:  "pruned",
				Details: scopeDetails(md.Realm, md.Space, md.Stack),
			}
			if delErr := b.runner.DeleteBlueprint(bp); delErr != nil {
				pruneResult.Action = actionFailed
				pruneResult.Error = fmt.Errorf("prune team %q: delete blueprint %q: %w", team, md.Name, delErr)
			}
			out = append(out, pruneResult)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func refsToSet(refs []scopedRef) map[scopedRef]struct{} {
	out := make(map[scopedRef]struct{}, len(refs))
	for _, r := range refs {
		out[r] = struct{}{}
	}
	return out
}

func scopeDetails(realm, space, stack string) map[string]string {
	d := map[string]string{"realm": realm}
	if space != "" {
		d["space"] = space
	}
	if stack != "" {
		d["stack"] = stack
	}
	return d
}
