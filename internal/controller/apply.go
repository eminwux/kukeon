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

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/apply/parser"
	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
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
			if team != "" {
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
		pruneResults, pruneErr := b.pruneTeamObjects(team, appliedBlueprints, appliedConfigs)
		if pruneErr != nil {
			return result, pruneErr
		}
		result.Resources = append(result.Resources, pruneResults...)
	}

	return result, nil
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
func (b *Exec) pruneTeamObjects(team string, appliedBlueprints, appliedConfigs []scopedRef) ([]ResourceResult, error) {
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
