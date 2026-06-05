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
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Materialize converts a CellConfig + its referenced CellBlueprint into a
// runtime CellDoc (issue #625). It is the config analog of
// cellblueprint.Materialize, but the two channels differ:
//
//   - Scalar values come from cfg.Spec.Values (not --param CLI args); the env
//     fallback is intentionally absent because a Config's values are the
//     persistent record of what the operator chose at apply time.
//   - Structural slot fills (repo URLs, secret sources) come from cfg.Spec.Repos
//     and cfg.Spec.Secrets, keyed by slot name. Unknown fills and unfilled
//     required slots are rejected via ValidateSlotFill; optional unfilled slots
//     are dropped from the materialized container.
//
// The materialized cell carries the cellconfig.LabelConfig back-reference (AC
// of #625) plus every label the operator set on cfg.Metadata.Labels, and uses
// the deterministic StableName(cfg.Metadata.Name) — the affordance that makes
// `kuke run -c` idempotent (at most one live cell per Config). The cell's
// scope coordinates come from the Config's metadata, not the blueprint's, so a
// Config in one realm may instantiate a Blueprint in another (cross-realm
// references are explicitly supported by CellConfigBlueprintRef).
func Materialize(cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc) (v1beta1.CellDoc, error) {
	return MaterializeWithName(cfg, bp, "")
}

// MaterializeWithName is the Materialize variant that lets the caller pin a
// non-stable cell name — the substrate for `kuke run <config> --new` (#833;
// originally shipped as `-c --generate-name` in #754, renamed in #833).
// When nameOverride is empty the cell uses StableName(cfg.Metadata.Name) and the
// idempotent-attach identity contract from #742 holds; when non-empty the name
// is used verbatim and the caller owns identity (the `kuke run <config> --new`
// path supplies `<cfg.Metadata.Name>-<6hex>` for the bare --new form, or the
// operator's `--name X` value for the `--new --name X` create-or-fail form,
// leaving the kukeon.io/config back-reference label intact so `kuke get cells
// -l kukeon.io/config=<name>` still enumerates every spawn).
func MaterializeWithName(
	cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc, nameOverride string,
) (v1beta1.CellDoc, error) {
	if err := ValidateSlotFill(cfg, bp); err != nil {
		return v1beta1.CellDoc{}, err
	}

	resolved, err := cellblueprint.Resolve(bp, cfg.Spec.Values, nil)
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

	cellName := strings.TrimSpace(nameOverride)
	if cellName == "" {
		cellName = StableName(cfg.Metadata.Name)
	}
	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   cellName,
			Labels: mergeConfigLabel(cfg.Metadata.Labels, cfg.Metadata.Name),
		},
		Spec: v1beta1.CellSpec{
			ID:                  cellName,
			RealmID:             strings.TrimSpace(cfg.Metadata.Realm),
			SpaceID:             strings.TrimSpace(cfg.Metadata.Space),
			StackID:             strings.TrimSpace(cfg.Metadata.Stack),
			Tty:                 cloneCellTty(resolved.Spec.Cell.Tty),
			Containers:          containers,
			AutoDelete:          resolved.Spec.Cell.AutoDelete,
			NestedCgroupRuntime: resolved.Spec.Cell.NestedCgroupRuntime,
		},
	}, nil
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
// from the Config's metadata with the kukeon.io/config back-reference pointing
// at the Config that materialized the cell. The back-reference wins on a
// collision so a Config cannot accidentally shadow its own identity label.
func mergeConfigLabel(in map[string]string, configName string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	out[LabelConfig] = configName
	return out
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

// GenerateName returns `<configName>-<6hex>`, the cell-name shape
// `kuke run <config> --new` produces (#833; originally shipped as `-c
// --generate-name` in #754, renamed in #833). The 6-hex suffix matches
// cellblueprint's `<prefix>-<6hex>` shape used by `-b`/`-p`, so generated
// `--new` cells are visually indistinguishable from generated-cell-per-
// invocation spawns of the other run verbs while preserving the
// kukeon.io/config back-reference label.
func GenerateName(configName string) (string, error) {
	suffix, err := naming.RandomHexSuffix(naming.DefaultCellNameSuffixBytes)
	if err != nil {
		return "", fmt.Errorf("config %q: generate cell name suffix: %w", configName, err)
	}
	return strings.TrimSpace(configName) + "-" + suffix, nil
}
