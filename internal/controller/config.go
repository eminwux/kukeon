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
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// GetConfigResult reports the full document view of a single `kind: CellConfig`
// (issue #644). Unlike a Secret, a Config carries no credential bytes — only a
// blueprint reference, scalar values, and repo/secret slot fills — so the whole
// document is returned for `kuke get config` to surface.
type GetConfigResult struct {
	Config         intmodel.CellConfig
	MetadataExists bool
}

// CreateConfigResult reports the outcome of an atomic create-only CellConfig
// write (issue #839). `Created` is true on success; the carrier always
// surfaces back the desired document so callers can echo the persisted name
// without re-issuing GetConfig.
type CreateConfigResult struct {
	Config  intmodel.CellConfig
	Created bool
}

// CreateConfig persists a new CellConfig document under atomic create-only
// semantics — the same scope / blueprint / slot-fill validation as
// ApplyDocuments-driven create, but the runner write is gated on the file
// not existing (issue #839). The caller is `kuke create config`; the path
// returns errdefs.ErrConfigExists on collision, which the CLI surfaces as
// a hard error rather than the write-through overwrite ApplyDocuments
// would perform.
func (b *Exec) CreateConfig(config intmodel.CellConfig) (CreateConfigResult, error) {
	var res CreateConfigResult

	if err := validateConfigLookup(config.Metadata); err != nil {
		return res, err
	}

	reconcile, err := applypkg.CreateConfig(b.runner, config)
	if err != nil {
		return res, err
	}
	res.Created = reconcile.Action == "created"
	res.Config = config
	return res, nil
}

// DeleteConfigResult reports the outcome of removing a single CellConfig's
// daemon-stored document file (issue #644). BackRefCells lists the scope paths
// of every live cell that still carries the kukeon.io/config back-reference
// label to this config. It is informational, never a refusal: deleting a Config
// does not delete the cell it materialized (that is `kuke delete cell`), so the
// CLI surfaces a one-line notice pointing the operator there.
type DeleteConfigResult struct {
	Config       intmodel.CellConfig
	Deleted      bool
	BackRefCells []string
}

// GetConfig reads one named, scoped CellConfig's document. The scope
// coordinates are validated for completeness (a deeper coordinate requires
// every shallower one; a Config may not be cell-scoped); realm and name are
// mandatory. Returns MetadataExists=false (no error) when the config is absent,
// mirroring GetBlueprint's "report, don't error on not-found" shape.
func (b *Exec) GetConfig(config intmodel.CellConfig) (GetConfigResult, error) {
	var res GetConfigResult

	if err := validateConfigLookup(config.Metadata); err != nil {
		return res, err
	}

	got, err := b.runner.GetConfig(config)
	if err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			res.MetadataExists = false
			return res, nil
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrGetConfig, err)
	}

	res.MetadataExists = true
	res.Config = got
	res.Config.ScheduleStatus = b.configScheduleStatus(got)
	res.Config.Connectors = b.configConnectors(got)
	return res, nil
}

// configConnectors reports how each connector a Config requires resolves right
// now (issue #1548): the identity a cell created from it would use and the
// level it comes from, or that nothing fills it. It is computed from the
// Config, its blueprint and the Realm, Space and Stack defaults on every read,
// so a default set a moment ago shows at once. Best-effort, like the schedule
// status: a Config whose blueprint is gone, or that no longer fits it, reports
// no connectors rather than failing the get — creating a cell from it reports
// the actual error.
func (b *Exec) configConnectors(cfg intmodel.CellConfig) []v1beta1.ConnectorStatus {
	resolved, err := resolveConfig(b.runner, cfg, cellBinding{})
	if err != nil {
		b.logger.DebugContext(b.ctx, "get config: resolve connectors",
			"config", cfg.Metadata.Name, "error", err)
		return nil
	}
	return resolved.connectors
}

// configScheduleStatus returns the stored schedule state of a Config that has
// a spec.schedule (#1503), and nil for one that has none — so state the
// scheduler has not yet pruned after a schedule was removed is never shown.
// Best-effort: the document itself is what a get is for, so a body that does
// not decode or state that cannot be read leaves the Config without status
// rather than failing the get.
func (b *Exec) configScheduleStatus(cfg intmodel.CellConfig) *intmodel.CellConfigScheduleStatus {
	doc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil || doc.Spec.Schedule == nil {
		return nil
	}
	status, found, err := b.runner.GetConfigScheduleStatus(cfg)
	if err != nil {
		b.logger.DebugContext(b.ctx, "get config: read schedule status",
			"config", cfg.Metadata.Name, "error", err)
		return nil
	}
	if !found {
		return nil
	}
	return &status
}

// ListConfigs lists the metadata of every CellConfig bound to the filter scope
// or any scope nested within it (issue #644). An empty realm lists across all
// realms; the filter coordinates must still be contiguous (no gap below a set
// level), and — a Config never being cell-scoped — there is no cell coordinate.
func (b *Exec) ListConfigs(realmName, spaceName, stackName string) ([]intmodel.CellConfig, error) {
	if err := validateConfigScopeFilter(realmName, spaceName, stackName); err != nil {
		return nil, err
	}
	configs, err := b.runner.ListConfigs(
		strings.TrimSpace(realmName),
		strings.TrimSpace(spaceName),
		strings.TrimSpace(stackName),
	)
	if err != nil {
		return nil, err
	}
	// A scheduled Config also carries its schedule state (#1503), so a list
	// can show the next fire. Best-effort, like the rest of the list's
	// per-document reads: unreadable state leaves the entry without it.
	for i := range configs {
		if configs[i].Schedule == nil {
			continue
		}
		status, found, statusErr := b.runner.GetConfigScheduleStatus(configs[i])
		if statusErr != nil {
			b.logger.DebugContext(b.ctx, "list configs: read schedule status",
				"config", configs[i].Metadata.Name, "error", statusErr)
			continue
		}
		if found {
			configs[i].ScheduleStatus = &status
		}
	}
	// Every Config also reports how its connectors resolve (#1548), so a list
	// can flag the ones a cell cannot be created from yet. The list carries
	// no documents, so each is read for this; best-effort like the rest.
	for i := range configs {
		full, getErr := b.runner.GetConfig(configs[i])
		if getErr != nil {
			b.logger.DebugContext(b.ctx, "list configs: read config",
				"config", configs[i].Metadata.Name, "error", getErr)
			continue
		}
		configs[i].Connectors = b.configConnectors(full)
	}
	return configs, nil
}

// DeleteConfig removes a single named, scoped CellConfig's daemon-stored
// document. Returns a "not found" error when the config does not exist, matching
// the DeleteBlueprint contract.
//
// Back-reference notice (issue #644 AC): deleting a Config does NOT delete the
// cell it materialized. Before unlinking, scan every persisted cell for the
// kukeon.io/config back-reference label pointing at this config and report the
// matches in the result so the CLI can emit a one-line notice pointing at
// `kuke delete cell <name>`. Unlike DeleteSecret's live-reference gate this is
// informational only — it never refuses the delete.
func (b *Exec) DeleteConfig(config intmodel.CellConfig) (DeleteConfigResult, error) {
	var res DeleteConfigResult

	if err := validateConfigLookup(config.Metadata); err != nil {
		return res, err
	}

	refs, err := b.configBackRefCells(config.Metadata)
	if err != nil {
		return res, fmt.Errorf("%w: check back-references: %w", errdefs.ErrDeleteConfig, err)
	}

	if err := b.runner.DeleteConfig(config); err != nil {
		if errors.Is(err, errdefs.ErrConfigNotFound) {
			return res, fmt.Errorf("config %q not found", config.Metadata.Name)
		}
		return res, fmt.Errorf("%w: %w", errdefs.ErrDeleteConfig, err)
	}

	res.Deleted = true
	res.Config = config
	res.BackRefCells = refs
	return res, nil
}

// configBackRefCells returns the scope paths of every persisted cell bound to
// this config. The match is cellBoundToConfig, the same one the OutOfSync
// lookup uses (issue #1569): a cell with render provenance matches only when
// its BindingRef names this exact Config scope, so a same-named config in a
// sibling or nested scope never produces a false positive; a cell without
// provenance falls back to label value == config name plus a scope prefix
// (the cell's realm always matches; space/stack match only when the config
// sets them).
func (b *Exec) configBackRefCells(md intmodel.CellConfigMetadata) ([]string, error) {
	cells, err := b.runner.ListCells("", "", "")
	if err != nil {
		return nil, err
	}

	var refs []string
	for _, cell := range cells {
		if !cellBoundToConfig(cell, md) {
			continue
		}
		refs = append(refs, fmt.Sprintf("%s/%s/%s/%s",
			cell.Spec.RealmName, cell.Spec.SpaceName, cell.Spec.StackName, cell.Metadata.Name))
	}
	return refs, nil
}

// validateConfigLookup enforces the scope contract for a single-config get or
// delete: name and realm are mandatory and the scope coordinates must be
// contiguous, with the Config-specific rule that a cell coordinate is never
// valid (a Config is realm/space/stack-scoped only).
func validateConfigLookup(md intmodel.CellConfigMetadata) error {
	if strings.TrimSpace(md.Name) == "" {
		return errdefs.ErrConfigNameRequired
	}
	if strings.TrimSpace(md.Realm) == "" {
		return errdefs.ErrConfigRealmRequired
	}
	if strings.TrimSpace(md.Stack) != "" && strings.TrimSpace(md.Space) == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrConfigScopeIncomplete)
	}
	return nil
}

// validateConfigScopeFilter enforces the scope contract for a list filter:
// realm is optional (an empty realm lists across all realms), but a set deeper
// coordinate still requires every shallower one. A Config is never cell-scoped,
// so the filter bottoms out at stack.
func validateConfigScopeFilter(realm, space, stack string) error {
	realm = strings.TrimSpace(realm)
	space = strings.TrimSpace(space)
	stack = strings.TrimSpace(stack)

	if realm == "" && (space != "" || stack != "") {
		return fmt.Errorf("%w (scope set without realm)", errdefs.ErrConfigScopeIncomplete)
	}
	if stack != "" && space == "" {
		return fmt.Errorf("%w (stack set without space)", errdefs.ErrConfigScopeIncomplete)
	}
	return nil
}
