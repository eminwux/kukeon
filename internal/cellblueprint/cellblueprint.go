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

// Package cellblueprint resolves daemon-stored CellBlueprint templates into
// CellDocs for `kuke run -b`. It supplies scalar `${KEY}` substitution (via
// substituteScalars in params.go), generates a fresh `<prefix>-<6hex>` cell
// name per invocation, and stamps a kukeon.io/blueprint back-reference label.
// Structural slots (secret slots, repo slots with no url) are *not* fillable
// inline — they require a CellConfig (`kuke run -c`, #625) — so
// materialization drops unfilled optional slots and refuses unfilled required
// ones.
package cellblueprint

import (
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/util/naming"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// LabelBlueprint is the cell label recording the CellBlueprint a cell was
// materialized from. Set on every cell produced by `kuke run -b` so operators
// can list all instances with `kuke get cells -l kukeon.io/blueprint=<name>`.
const LabelBlueprint = "kukeon.io/blueprint"

// Resolve substitutes `${KEY}` scalar parameters in the blueprint body against
// the resolution order cliParams[k] > parameters[k].default > lookupEnv(k),
// returning a resolved copy of the document. Contract (issue #355) for the
// daemon-stored blueprint path:
//
//   - an undeclared --param key errors (typo at call time);
//   - a parameter declared required that resolves to no value errors;
//   - empty string is a valid resolved value; an unset, non-required parameter
//     substitutes to empty so a declared-but-unused `${KEY}` drops out cleanly.
//
// When lookupEnv is nil the env fallback is skipped.
func Resolve(
	doc v1beta1.CellBlueprintDoc,
	cliParams map[string]string,
	lookupEnv func(string) (string, bool),
) (v1beta1.CellBlueprintDoc, error) {
	return resolve(doc, cliParams, lookupEnv, false)
}

// ResolveConfig is the lenient resolution entrypoint for the machine-generated
// CellConfig.Values channel (issue #1124). It behaves exactly like Resolve —
// same cliParams[k] > parameters[k].default > lookupEnv(k) order, same
// required-parameter enforcement — except it does *not* reject value keys that
// are absent from spec.parameters[]:
//
//   - undeclared keys do not error (the typo-strictness Resolve applies is
//     correct for a human typing `--param FOO=…`, but wrong for a generated
//     artifact: a rendered CellConfig carries operator facts — ROLE, GIT_*,
//     HARNESS, PROJECT — that the blueprint never declares as parameters);
//   - undeclared keys are still substituted into the body, so a `${ROLE}` the
//     blueprint body references but never declares as a parameter resolves to
//     the supplied value rather than surviving as a literal.
//
// A declared parameter the values map does not supply still follows the strict
// rule (default → env → required-error), so a genuinely missing required
// parameter is still caught.
func ResolveConfig(
	doc v1beta1.CellBlueprintDoc,
	values map[string]string,
	lookupEnv func(string) (string, bool),
) (v1beta1.CellBlueprintDoc, error) {
	return resolve(doc, values, lookupEnv, true)
}

func resolve(
	doc v1beta1.CellBlueprintDoc,
	cliParams map[string]string,
	lookupEnv func(string) (string, bool),
	lenient bool,
) (v1beta1.CellBlueprintDoc, error) {
	values, err := resolveValues(doc, cliParams, lookupEnv, lenient)
	if err != nil {
		return v1beta1.CellBlueprintDoc{}, err
	}

	raw, err := yaml.Marshal(doc)
	if err != nil {
		return v1beta1.CellBlueprintDoc{}, fmt.Errorf("blueprint %q: marshal for substitution: %w", doc.Metadata.Name, err)
	}
	var node yaml.Node
	if unmarshalErr := yaml.Unmarshal(raw, &node); unmarshalErr != nil {
		return v1beta1.CellBlueprintDoc{}, fmt.Errorf("blueprint %q: parse for substitution: %w", doc.Metadata.Name, unmarshalErr)
	}

	substituteScalars(&node, values)

	var out v1beta1.CellBlueprintDoc
	if decodeErr := node.Decode(&out); decodeErr != nil {
		return v1beta1.CellBlueprintDoc{}, fmt.Errorf(
			"blueprint %q: re-decode after parameter substitution: %w: %w",
			doc.Metadata.Name, errdefs.ErrBlueprintInvalid, decodeErr,
		)
	}
	return out, nil
}

// resolveValues validates cliParams against the declared parameters and builds
// the substitution value map. The substitution leaves `default` declarations
// themselves untouched (substituteScalars rewrites every scalar, but a missing
// key is left literal; declared params are always in the map).
//
// When lenient is true (the CellConfig.Values channel, #1124) an undeclared key
// is not an error and is carried into the value map so it still substitutes;
// when false (the interactive `--param` channel) an undeclared key errors.
func resolveValues(
	doc v1beta1.CellBlueprintDoc,
	cliParams map[string]string,
	lookupEnv func(string) (string, bool),
	lenient bool,
) (map[string]string, error) {
	declared := make(map[string]v1beta1.CellBlueprintParameter, len(doc.Spec.Parameters))
	for _, p := range doc.Spec.Parameters {
		declared[p.Name] = p
	}

	values := make(map[string]string, len(declared)+len(cliParams))
	for k, v := range cliParams {
		if _, ok := declared[k]; ok {
			continue
		}
		if !lenient {
			return nil, fmt.Errorf(
				"blueprint %q: --param %q is not declared in spec.parameters[]: %w",
				doc.Metadata.Name, k, errdefs.ErrBlueprintInvalid,
			)
		}
		// Lenient: carry the undeclared key through so a `${KEY}` the body
		// references but never declares still resolves.
		values[k] = v
	}

	for _, p := range doc.Spec.Parameters {
		if v, ok := cliParams[p.Name]; ok {
			values[p.Name] = v
			continue
		}
		if p.Default != nil {
			values[p.Name] = *p.Default
			continue
		}
		if lookupEnv != nil {
			if v, ok := lookupEnv(p.Name); ok {
				values[p.Name] = v
				continue
			}
		}
		if p.Required {
			return nil, fmt.Errorf(
				"blueprint %q: required parameter %q is not set "+
					"(provide --param %s=... or declare a spec.parameters[].default): %w",
				doc.Metadata.Name, p.Name, p.Name, errdefs.ErrBlueprintInvalid,
			)
		}
		values[p.Name] = ""
	}
	return values, nil
}

// Materialize converts a resolved blueprint into a CellDoc with a generated
// name and no recorded params. See MaterializeWithName for the override- and
// provenance-aware form.
func Materialize(doc v1beta1.CellBlueprintDoc) (v1beta1.CellDoc, error) {
	return MaterializeWithName(doc, "", nil)
}

// MaterializeWithName converts a (resolved) blueprint into a CellDoc named
// `<prefix>-<6hex>` (prefix = spec.prefix or metadata.name), or nameOverride
// verbatim when non-empty. The realm/space/stack triple comes from the
// blueprint metadata. Every cell carries the kukeon.io/blueprint=<name>
// lineage label and a Spec.Provenance block recording the Blueprint binding it
// was stamped from (issue #1021); params are the resolved `--param` map the
// caller substituted into the blueprint (pass the same map handed to Resolve),
// recorded verbatim so a later re-resolution does not depend on re-reading the
// transient CLI invocation.
//
// Structural slots are resolved against the "scalar-only inline" contract:
// a repo whose url is still empty after substitution, and every secret slot
// (which never carries a source — that is a CellConfig's job, #624), are
// unfilled. An unfilled *required* slot makes the blueprint un-runnable inline
// and returns ErrBlueprintStructuralSlots naming the offenders; an unfilled
// *optional* slot is dropped from the materialized container.
func MaterializeWithName(
	doc v1beta1.CellBlueprintDoc, nameOverride string, params map[string]string,
) (v1beta1.CellDoc, error) {
	cellName, err := resolveCellName(doc, nameOverride)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	containers := make([]v1beta1.ContainerSpec, 0, len(doc.Spec.Cell.Containers))
	var blockers []string
	for _, bc := range doc.Spec.Cell.Containers {
		cs, missing := materializeContainer(bc)
		blockers = append(blockers, missing...)
		containers = append(containers, cs)
	}
	if len(blockers) > 0 {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (blueprint %q: %s); use `kuke run --from-config` with a CellConfig that fills the slots",
			errdefs.ErrBlueprintStructuralSlots, doc.Metadata.Name, strings.Join(blockers, ", "),
		)
	}

	return v1beta1.CellDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCell,
		Metadata: v1beta1.CellMetadata{
			Name:   cellName,
			Labels: mergeLabels(doc.Metadata.Labels, LabelBlueprint, doc.Metadata.Name),
		},
		Spec: v1beta1.CellSpec{
			ID:                  cellName,
			RealmID:             strings.TrimSpace(doc.Metadata.Realm),
			SpaceID:             strings.TrimSpace(doc.Metadata.Space),
			StackID:             strings.TrimSpace(doc.Metadata.Stack),
			Tty:                 cloneCellTty(doc.Spec.Cell.Tty),
			Containers:          containers,
			AutoDelete:          doc.Spec.Cell.AutoDelete,
			NestedCgroupRuntime: doc.Spec.Cell.NestedCgroupRuntime,
			Provenance:          blueprintProvenance(doc, params),
		},
	}, nil
}

// blueprintProvenance builds the Spec.Provenance block for a Blueprint-
// materialized cell: bindingKind=blueprint, the Blueprint's scoped name as the
// binding ref, and the resolved `--param` map as the recorded params.
// EnvOverrides is left for the run-time caller to populate from `--env`.
// Issue #1021.
func blueprintProvenance(doc v1beta1.CellBlueprintDoc, params map[string]string) *v1beta1.CellProvenance {
	prov := &v1beta1.CellProvenance{
		BindingKind: v1beta1.BindingKindBlueprint,
		BindingRef: v1beta1.CellBindingRef{
			Name:  strings.TrimSpace(doc.Metadata.Name),
			Realm: strings.TrimSpace(doc.Metadata.Realm),
			Space: strings.TrimSpace(doc.Metadata.Space),
			Stack: strings.TrimSpace(doc.Metadata.Stack),
		},
	}
	if len(params) > 0 {
		p := make(map[string]string, len(params))
		for k, v := range params {
			p[k] = v
		}
		prov.Params = p
	}
	return prov
}

// materializeContainer maps a BlueprintContainer to a runtime ContainerSpec and
// returns the names of any unfilled *required* structural slots (which block
// inline `-b`). Unfilled optional slots are dropped. A repo with a url (scalar
// mode) is carried through unchanged.
func materializeContainer(bc v1beta1.BlueprintContainer) (v1beta1.ContainerSpec, []string) {
	var blockers []string

	repos := make([]v1beta1.ContainerRepo, 0, len(bc.Repos))
	for _, r := range bc.Repos {
		if strings.TrimSpace(r.URL) == "" {
			// Structural repo slot: url is filled by a CellConfig (#624).
			if r.Required {
				blockers = append(blockers, fmt.Sprintf("container %q repo slot %q (url)", bc.ID, r.Name))
			}
			continue
		}
		repos = append(repos, r)
	}

	// Every secret slot is structural: a blueprint never carries the secret
	// source (which kind: Secret provides the bytes) — that is filled by a
	// CellConfig (#624). So inline `-b` can never satisfy one. Required slots
	// block; optional slots drop.
	for _, s := range bc.Secrets {
		if s.Required {
			blockers = append(blockers, fmt.Sprintf("container %q secret slot %q", bc.ID, s.Name))
		}
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
	}
	if len(repos) == 0 {
		cs.Repos = nil
	}
	return cs, blockers
}

// Prefix resolves the cell-name prefix the unified generator uses for a cell
// materialized from this Blueprint (epic:cell-identity #1022): Spec.Prefix when
// set, else the blueprint's metadata.name. Mirrors cellconfig.Prefix.
func Prefix(doc v1beta1.CellBlueprintDoc) string {
	if p := strings.TrimSpace(doc.Spec.Prefix); p != "" {
		return p
	}
	return strings.TrimSpace(doc.Metadata.Name)
}

func resolveCellName(doc v1beta1.CellBlueprintDoc, nameOverride string) (string, error) {
	if override := strings.TrimSpace(nameOverride); override != "" {
		return override, nil
	}
	name, err := naming.GenerateCellName(Prefix(doc))
	if err != nil {
		return "", fmt.Errorf("blueprint %q: %w", doc.Metadata.Name, err)
	}
	return name, nil
}

func cloneCellTty(in *v1beta1.CellTty) *v1beta1.CellTty {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func mergeLabels(in map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for lk, lv := range in {
		out[lk] = lv
	}
	out[k] = v
	return out
}
