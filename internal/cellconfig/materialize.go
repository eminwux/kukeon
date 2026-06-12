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

package cellconfig

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// MaterializeWithName converts a CellConfig + its referenced CellBlueprint into
// a runtime CellDoc (issue #625), naming the cell `name` verbatim. It is the
// config analog of cellblueprint.MaterializeWithName, but the two channels
// differ:
//
//   - Scalar values come from cfg.Spec.Values (not --param CLI args); the env
//     fallback is intentionally absent because a Config's values are the
//     persistent record of what the operator chose at apply time. Resolution
//     goes through cellblueprint.ResolveConfig (the lenient channel, #1124):
//     a machine-generated Config carries operator facts the blueprint never
//     declares as parameters, so undeclared keys are tolerated and substituted
//     rather than rejected with the interactive `--param` typo-strictness.
//   - Structural slot fills (repo URLs, secret sources) come from cfg.Spec.Repos
//     and cfg.Spec.Secrets, keyed by slot name. Unknown fills and unfilled
//     required slots are rejected via ValidateSlotFill; optional unfilled slots
//     are dropped from the materialized container.
//
// The materialized cell carries the cellconfig.LabelConfig lineage label (AC
// of #625) plus every label the operator set on cfg.Metadata.Labels, and a
// Spec.Provenance block recording the Config binding it was stamped from
// (issue #1021). The cell's scope coordinates come from the Config's metadata,
// not the blueprint's, so a Config in one realm may instantiate a Blueprint in
// another (cross-realm references are explicitly supported by
// CellConfigBlueprintRef).
//
// The cell name is supplied by the caller — materialization no longer derives
// it from cfg.Metadata.Name (epic:cell-identity #1021 severs that assumption;
// the cell's identity is the CellDoc, and the Config name is demoted to
// lineage). Callers resolve the name via the unified generator
// (naming.AllocCellName over cellconfig.Prefix, #1022): an explicit name is
// used verbatim, an omitted one becomes a generated `<prefix>-<6hex>`. An empty
// name is the caller's responsibility (it yields an empty-named cell that fails
// downstream validation) — materialization intentionally does not paper over
// it by reaching back to the Config name.
func MaterializeWithName(
	cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc, name string,
) (v1beta1.CellDoc, error) {
	if err := ValidateSlotFill(cfg, bp); err != nil {
		return v1beta1.CellDoc{}, err
	}

	resolved, err := cellblueprint.ResolveConfig(bp, cfg.Spec.Values, nil)
	if err != nil {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"config %q: resolve blueprint %q: %w",
			cfg.Metadata.Name, bp.Metadata.Name, err,
		)
	}

	containers := make([]v1beta1.ContainerSpec, 0, len(resolved.Spec.Cell.Containers))
	for _, bc := range resolved.Spec.Cell.Containers {
		cs, fillErr := materializeContainer(bc, cfg)
		if fillErr != nil {
			return v1beta1.CellDoc{}, fillErr
		}
		containers = append(containers, cs)
	}

	cellName := strings.TrimSpace(name)
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   cellName,
			Labels: mergeConfigLabel(cfg.Metadata.Labels, cfg.Metadata.Name),
		},
		Spec: v1beta1.CellSpec{
			ID:                  cellName,
			RealmID:             defaultScope(cfg.Metadata.Realm, consts.KukeonDefaultRealmName),
			SpaceID:             defaultScope(cfg.Metadata.Space, consts.KukeonDefaultSpaceName),
			StackID:             defaultScope(cfg.Metadata.Stack, consts.KukeonDefaultStackName),
			Tty:                 cloneCellTty(resolved.Spec.Cell.Tty),
			Containers:          containers,
			AutoDelete:          resolved.Spec.Cell.AutoDelete,
			NestedCgroupRuntime: resolved.Spec.Cell.NestedCgroupRuntime,
			Provenance:          configProvenance(cfg),
		},
	}, nil
}

// configProvenance builds the Spec.Provenance block for a Config-materialized
// cell: bindingKind=config, the Config's scoped name as the binding ref, and
// the Config's spec.values as the recorded params. EnvOverrides is left for
// the run-time caller to populate from `--env` (the Config path's materialize
// has no CLI env). Issue #1021.
func configProvenance(cfg v1beta1.CellConfigDoc) *v1beta1.CellProvenance {
	prov := &v1beta1.CellProvenance{
		BindingKind: v1beta1.BindingKindConfig,
		BindingRef: v1beta1.CellBindingRef{
			Name:  strings.TrimSpace(cfg.Metadata.Name),
			Realm: strings.TrimSpace(cfg.Metadata.Realm),
			Space: strings.TrimSpace(cfg.Metadata.Space),
			Stack: strings.TrimSpace(cfg.Metadata.Stack),
		},
	}
	if len(cfg.Spec.Values) > 0 {
		params := make(map[string]string, len(cfg.Spec.Values))
		for k, v := range cfg.Spec.Values {
			params[k] = v
		}
		prov.Params = params
	}
	return prov
}

// materializeContainer maps a (resolved) BlueprintContainer to a runtime
// ContainerSpec, applying the Config's repo and secret slot fills. Repos with
// an inline URL pass through (scalar mode); repos with an empty URL whose slot
// the Config fills get the fill's URL/Branch; optional unfilled slots drop.
// Every blueprint secret slot is structural, so a filled slot produces a
// ContainerSecret keyed by the slot's mode (env → env var named EnvName; file
// → read-only mount at MountPath); optional unfilled secret slots drop.
//
// ValidateSlotFill is the gate for "required slot must be filled" / "fill must
// match a declared slot", so this function trusts those invariants and treats
// any still-unfilled required slot here as an internal error rather than a
// user-facing one.
func materializeContainer(
	bc v1beta1.BlueprintContainer, cfg v1beta1.CellConfigDoc,
) (v1beta1.ContainerSpec, error) {
	repos := make([]v1beta1.ContainerRepo, 0, len(bc.Repos))
	for _, r := range bc.Repos {
		if strings.TrimSpace(r.URL) != "" {
			repos = append(repos, r)
			continue
		}
		fill, ok := cfg.Spec.Repos[strings.TrimSpace(r.Name)]
		if !ok {
			if r.Required {
				return v1beta1.ContainerSpec{}, fmt.Errorf(
					"%w: container %q repo slot %q",
					errdefs.ErrConfigRequiredSlotUnfilled, bc.ID, r.Name,
				)
			}
			continue
		}
		filled := r
		filled.URL = strings.TrimSpace(fill.URL)
		if branch := strings.TrimSpace(fill.Branch); branch != "" {
			filled.Branch = branch
		}
		if ref := strings.TrimSpace(fill.Ref); ref != "" {
			filled.Ref = ref
		}
		repos = append(repos, filled)
	}

	secrets := make([]v1beta1.ContainerSecret, 0, len(bc.Secrets))
	for _, slot := range bc.Secrets {
		fill, ok := cfg.Spec.Secrets[strings.TrimSpace(slot.Name)]
		if !ok {
			if slot.Required {
				return v1beta1.ContainerSpec{}, fmt.Errorf(
					"%w: container %q secret slot %q",
					errdefs.ErrConfigRequiredSlotUnfilled, bc.ID, slot.Name,
				)
			}
			continue
		}
		cs, err := materializeSecretSlot(bc.ID, slot, fill)
		if err != nil {
			return v1beta1.ContainerSpec{}, err
		}
		secrets = append(secrets, cs)
	}

	cs := v1beta1.ContainerSpec{
		ID:                     bc.ID,
		Root:                   bc.Root,
		Image:                  bc.Image,
		Command:                bc.Command,
		Args:                   bc.Args,
		WorkingDir:             bc.WorkingDir,
		Env:                    bc.Env,
		Ports:                  bc.Ports,
		Volumes:                bc.Volumes,
		Networks:               bc.Networks,
		NetworksAliases:        bc.NetworksAliases,
		Privileged:             bc.Privileged,
		HostNetwork:            bc.HostNetwork,
		HostPID:                bc.HostPID,
		HostCgroup:             bc.HostCgroup,
		User:                   bc.User,
		ReadOnlyRootFilesystem: bc.ReadOnlyRootFilesystem,
		Capabilities:           bc.Capabilities,
		SecurityOpts:           bc.SecurityOpts,
		Devices:                bc.Devices,
		Tmpfs:                  bc.Tmpfs,
		Resources:              bc.Resources,
		Repos:                  repos,
		Git:                    bc.Git,
		RestartPolicy:          bc.RestartPolicy,
		Attachable:             bc.Attachable,
		Tty:                    bc.Tty,
		Secrets:                secrets,
	}
	if len(repos) == 0 {
		cs.Repos = nil
	}
	if len(secrets) == 0 {
		cs.Secrets = nil
	}
	return cs, nil
}

// materializeSecretSlot translates a (slot, fill) pair into a runtime
// ContainerSecret. The blueprint owns the consumption side (mode + env-var name
// or mount path); the Config supplies the source (which kind: Secret provides
// the bytes). Mode "env" (the default when empty) emits an env-var-mode
// ContainerSecret keyed by EnvName; mode "file" emits a file-mode
// ContainerSecret mounted at MountPath. The slot's own Name carries through as
// the ContainerSecret's Name for file mode (used as the staged filename and
// the back-trace label); env mode replaces it with EnvName because
// ContainerSecret.Name doubles as the env-var name when MountPath is empty.
func materializeSecretSlot(
	containerID string, slot v1beta1.BlueprintSecretSlot, fill v1beta1.CellConfigSecretFill,
) (v1beta1.ContainerSecret, error) {
	if fill.SecretRef == nil {
		return v1beta1.ContainerSecret{}, fmt.Errorf(
			"%w: container %q secret slot %q (fill has no secretRef)",
			errdefs.ErrConfigRequiredSlotUnfilled, containerID, slot.Name,
		)
	}
	ref := *fill.SecretRef

	mode := strings.TrimSpace(slot.Mode)
	if mode == "" {
		mode = v1beta1.BlueprintSecretModeEnv
	}
	switch mode {
	case v1beta1.BlueprintSecretModeEnv:
		return v1beta1.ContainerSecret{
			Name:      strings.TrimSpace(slot.EnvName),
			SecretRef: &ref,
		}, nil
	case v1beta1.BlueprintSecretModeFile:
		return v1beta1.ContainerSecret{
			Name:      strings.TrimSpace(slot.Name),
			SecretRef: &ref,
			MountPath: strings.TrimSpace(slot.MountPath),
		}, nil
	default:
		return v1beta1.ContainerSecret{}, fmt.Errorf(
			"%w: container %q secret slot %q mode %q",
			errdefs.ErrBlueprintInvalid, containerID, slot.Name, mode,
		)
	}
}

// mergeConfigLabel returns a label map combining the operator-authored labels
// from the Config's metadata with the kukeon.io/config lineage label pointing
// at the Config that materialized the cell. The lineage label wins on a
// collision so a Config cannot accidentally shadow its own provenance. The
// relationship is 1:N — a single Config may stamp many cells (epic:cell-
// identity #1021 demotes this label from identity to lineage); the cell's
// identity is its own name, recorded on the CellDoc, not this label.
func mergeConfigLabel(in map[string]string, configName string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[LabelConfig] = configName
	return out
}

// defaultScope returns the trimmed scope coordinate, or def when it is empty.
// The CLI create path (cmd/kuke/run/run.go) defaults an omitted realm/space/
// stack to `default` before persisting the cell, so a Config that carries an
// empty coordinate must materialize to the same `default` here — otherwise the
// reconciler's re-materialization yields `"" != "default"` against the live
// cell and apply.DiffCell reports a permanent spurious OutOfSync (#1133). This
// hardens the reconcile path at its source, independent of whichever binding
// produced the Config.
func defaultScope(v, def string) string {
	if trimmed := strings.TrimSpace(v); trimmed != "" {
		return trimmed
	}
	return def
}

// cloneCellTty deep-copies a *CellTty so mutations on the materialized cell do
// not leak back into the blueprint document. Mirrors
// cellblueprint.cloneCellTty's contract.
func cloneCellTty(in *v1beta1.CellTty) *v1beta1.CellTty {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
