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

package cellblueprint

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// LabelMode is the cell label recording the harness mode a cell was created
// in, so `kuke get cells -l kukeon.io/mode=headless` lists dispatched runs.
const LabelMode = "kukeon.io/mode"

// FlattenOptions selects what Flatten folds into the cell template.
type FlattenOptions struct {
	// Components are the optional components to enable, by name (a
	// CellConfig's spec.components). Non-optional components are always on.
	Components []string
	// Mode is the harness mode picked for the cell; empty selects the default.
	Mode string
	// AllowedModes restricts the modes a cell may be created in (a
	// CellConfig's spec.modes.enabled); empty allows every declared mode.
	AllowedModes []string
	// DefaultMode is the mode used when Mode is empty (spec.modes.default).
	DefaultMode string
	// All renders the validation view instead of a runnable one: every
	// component and every mode is folded in, nothing an optional component or
	// a mode contributes is required, and no mode's onInit is applied. It is
	// what a CellConfig is checked against at apply time, before any cell —
	// and therefore any mode — exists.
	All bool
}

// Flattened is a blueprint with its components folded into the cell template.
type Flattened struct {
	// Doc carries no components: their parameters joined spec.parameters,
	// their env and secret slots landed on a container, and spec.requires is
	// the union of the blueprint's and the enabled components' requires.
	Doc v1beta1.CellBlueprintDoc
	// Mode is the picked harness mode; empty when the blueprint declares none
	// (or for the All view).
	Mode string
	// Components lists the enabled components, in declaration order.
	Components []string
	// ModeParameters names the parameters the picked mode declares — the only
	// ones a cell may set for itself at create time.
	ModeParameters []string
}

// Flatten folds a blueprint's enabled components and the picked harness mode
// into its cell template (issue #1472, steps 10–11). A blueprint without
// components is returned unchanged apart from a normalized Requires. Two
// enabled contributions that set the same parameter, env key or secret
// destination fail with ErrBlueprintComponentConflict.
//
//nolint:funlen // one pass over the components; splitting it would scatter the fold order.
func Flatten(doc v1beta1.CellBlueprintDoc, opts FlattenOptions) (Flattened, error) {
	out := Flattened{Doc: cloneBlueprint(doc)}
	spec := &out.Doc.Spec
	components := spec.Components
	spec.Components = nil
	requires := append([]string(nil), spec.Requires...)

	if err := checkEnabledNames(doc, components, opts.Components); err != nil {
		return Flattened{}, err
	}
	if err := checkGroups(doc, components, opts.Components); err != nil {
		return Flattened{}, err
	}

	modeOwner := -1
	for i := range components {
		if len(components[i].Modes) > 0 {
			modeOwner = i
		}
	}
	if modeOwner < 0 && !opts.All && (opts.Mode != "" || len(opts.AllowedModes) > 0 || opts.DefaultMode != "") {
		return Flattened{}, fmt.Errorf(
			"%w: blueprint %q declares no harness modes", errdefs.ErrBlueprintMode, doc.Metadata.Name)
	}

	for i := range components {
		comp := components[i]
		enabled := !comp.Optional || opts.All || slices.Contains(opts.Components, comp.Name)
		if !enabled {
			continue
		}
		out.Components = append(out.Components, comp.Name)
		target, err := componentContainer(spec, comp)
		if err != nil {
			return Flattened{}, fmt.Errorf("blueprint %q: %w", doc.Metadata.Name, err)
		}
		f := folder{spec: spec, container: &spec.Cell.Containers[target], origin: "component " + comp.Name}
		f.relaxed = opts.All && comp.Optional
		f.tolerant = opts.All
		if err = f.fold(comp.Parameters, comp.Env, comp.Secrets); err != nil {
			return Flattened{}, fmt.Errorf("blueprint %q: %w", doc.Metadata.Name, err)
		}
		requires = append(requires, comp.Requires...)

		if i != modeOwner {
			continue
		}
		if opts.All {
			for _, name := range sortedModes(comp.Modes) {
				mode := comp.Modes[name]
				mf := folder{
					spec: spec, container: f.container, origin: "mode " + name, relaxed: true, tolerant: true,
				}
				if err = mf.fold(mode.Parameters, mode.Env, mode.Secrets); err != nil {
					return Flattened{}, fmt.Errorf("blueprint %q: %w", doc.Metadata.Name, err)
				}
			}
			continue
		}
		picked, err := pickMode(doc.Metadata.Name, comp.Modes, opts)
		if err != nil {
			return Flattened{}, err
		}
		mode := comp.Modes[picked]
		mf := folder{spec: spec, container: f.container, origin: "mode " + picked}
		if err = mf.fold(mode.Parameters, mode.Env, mode.Secrets); err != nil {
			return Flattened{}, fmt.Errorf("blueprint %q: %w", doc.Metadata.Name, err)
		}
		if len(mode.OnInit) > 0 {
			tty := v1beta1.ContainerTty{}
			if f.container.Tty != nil {
				tty = *f.container.Tty
			}
			tty.OnInit = append([]v1beta1.TtyStage(nil), mode.OnInit...)
			f.container.Tty = &tty
		}
		out.Mode = picked
		for _, p := range mode.Parameters {
			out.ModeParameters = append(out.ModeParameters, p.Name)
		}
	}

	// A mode was asked for but the component declaring modes is optional and
	// left off: the request cannot be honoured, so say so rather than create a
	// cell in no mode at all.
	if !opts.All && opts.Mode != "" && out.Mode == "" {
		return Flattened{}, fmt.Errorf(
			"%w: blueprint %q: mode %q needs component %q, which the config does not enable",
			errdefs.ErrBlueprintMode, doc.Metadata.Name, opts.Mode, components[modeOwner].Name)
	}

	spec.Requires = NormalizeRequires(requires)
	return out, nil
}

// checkEnabledNames rejects a request to enable a component the blueprint does
// not declare. Naming a non-optional component is harmless and allowed.
func checkEnabledNames(doc v1beta1.CellBlueprintDoc, components []v1beta1.BlueprintComponent, enabled []string) error {
	for _, name := range enabled {
		if !slices.ContainsFunc(components, func(c v1beta1.BlueprintComponent) bool { return c.Name == name }) {
			return fmt.Errorf("%w: blueprint %q declares no component %q",
				errdefs.ErrBlueprintComponent, doc.Metadata.Name, name)
		}
	}
	return nil
}

// checkGroups rejects a selection that enables two components of one group:
// the members of a group are alternatives (the ways one harness authenticates,
// issue #1548). It looks at what a CellConfig asks for, not at the render
// view, so the apply-time validation view refuses such a Config too.
func checkGroups(doc v1beta1.CellBlueprintDoc, components []v1beta1.BlueprintComponent, enabled []string) error {
	taken := map[string]string{}
	for _, comp := range components {
		group := strings.TrimSpace(comp.Group)
		if group == "" || (comp.Optional && !slices.Contains(enabled, comp.Name)) {
			continue
		}
		if other, dup := taken[group]; dup {
			return fmt.Errorf(
				"%w: blueprint %q: components %q and %q both belong to group %q; enable one",
				errdefs.ErrBlueprintComponentGroup, doc.Metadata.Name, other, comp.Name, group)
		}
		taken[group] = comp.Name
	}
	return nil
}

// componentContainer resolves the container a component's env, secrets and
// mode onInit land on: its explicit Container, else the cell's default attach
// container, else the first attachable container, else the first non-root one.
func componentContainer(spec *v1beta1.CellBlueprintSpec, comp v1beta1.BlueprintComponent) (int, error) {
	containers := spec.Cell.Containers
	want := strings.TrimSpace(comp.Container)
	if want == "" && spec.Cell.Tty != nil {
		want = strings.TrimSpace(spec.Cell.Tty.Default)
	}
	if want != "" {
		for i := range containers {
			if containers[i].ID == want {
				return i, nil
			}
		}
		return 0, fmt.Errorf("%w: component %q targets unknown container %q",
			errdefs.ErrBlueprintComponent, comp.Name, want)
	}
	for i := range containers {
		if containers[i].Attachable && !containers[i].Root {
			return i, nil
		}
	}
	for i := range containers {
		if !containers[i].Root {
			return i, nil
		}
	}
	return 0, fmt.Errorf("%w: component %q has no container to land on", errdefs.ErrBlueprintComponent, comp.Name)
}

// pickMode resolves the harness mode of a cell: the requested one, else the
// configured default, else the only allowed mode, else "interactive", else the
// first allowed mode by name.
func pickMode(blueprint string, modes map[string]v1beta1.BlueprintComponentMode, opts FlattenOptions) (string, error) {
	declared := sortedModes(modes)
	allowed := declared
	if len(opts.AllowedModes) > 0 {
		allowed = nil
		for _, name := range opts.AllowedModes {
			if _, ok := modes[name]; !ok {
				return "", fmt.Errorf("%w: blueprint %q declares no mode %q (declared: %s)",
					errdefs.ErrBlueprintMode, blueprint, name, strings.Join(declared, ", "))
			}
			allowed = append(allowed, name)
		}
		sort.Strings(allowed)
	}
	picked := strings.TrimSpace(opts.Mode)
	if picked == "" {
		picked = strings.TrimSpace(opts.DefaultMode)
	}
	if picked == "" {
		switch {
		case len(allowed) == 1:
			picked = allowed[0]
		case slices.Contains(allowed, v1beta1.ModeInteractive):
			picked = v1beta1.ModeInteractive
		default:
			picked = allowed[0]
		}
	}
	if _, ok := modes[picked]; !ok {
		return "", fmt.Errorf("%w: blueprint %q declares no mode %q (declared: %s)",
			errdefs.ErrBlueprintMode, blueprint, picked, strings.Join(declared, ", "))
	}
	if !slices.Contains(allowed, picked) {
		return "", fmt.Errorf("%w: mode %q is not enabled (enabled: %s)",
			errdefs.ErrBlueprintMode, picked, strings.Join(allowed, ", "))
	}
	return picked, nil
}

func sortedModes(modes map[string]v1beta1.BlueprintComponentMode) []string {
	names := make([]string, 0, len(modes))
	for name := range modes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Modes returns the harness mode names a blueprint declares, sorted.
func Modes(doc v1beta1.CellBlueprintDoc) []string {
	for i := range doc.Spec.Components {
		if len(doc.Spec.Components[i].Modes) > 0 {
			return sortedModes(doc.Spec.Components[i].Modes)
		}
	}
	return nil
}

// folder adds one contribution (a component or a mode) to the cell template.
type folder struct {
	spec      *v1beta1.CellBlueprintSpec
	container *v1beta1.BlueprintContainer
	origin    string
	// relaxed strips Required from what is added (the All view of an optional
	// component or a mode).
	relaxed bool
	// tolerant skips, instead of rejecting, a parameter or secret slot that
	// is already present under the same name (alternative modes legitimately
	// repeat each other in the All view).
	tolerant bool
}

func (f folder) fold(
	parameters []v1beta1.CellBlueprintParameter, env []string, secrets []v1beta1.BlueprintSecretSlot,
) error {
	for _, p := range parameters {
		if slices.ContainsFunc(f.spec.Parameters, func(e v1beta1.CellBlueprintParameter) bool {
			return e.Name == p.Name
		}) {
			if f.tolerant {
				continue
			}
			return fmt.Errorf("%w: %s redeclares parameter %q", errdefs.ErrBlueprintComponentConflict, f.origin, p.Name)
		}
		if f.relaxed {
			p.Required = false
		}
		f.spec.Parameters = append(f.spec.Parameters, p)
	}
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if f.envTaken(key) {
			if f.tolerant {
				continue
			}
			return fmt.Errorf("%w: %s sets env %q, which container %q already sets",
				errdefs.ErrBlueprintComponentConflict, f.origin, key, f.container.ID)
		}
		f.container.Env = append(f.container.Env, entry)
	}
	for _, slot := range secrets {
		if conflict := f.secretConflict(slot); conflict != "" {
			if f.tolerant {
				continue
			}
			return fmt.Errorf("%w: %s secret slot %q: %s",
				errdefs.ErrBlueprintComponentConflict, f.origin, slot.Name, conflict)
		}
		if f.relaxed {
			slot.Required = false
		}
		f.container.Secrets = append(f.container.Secrets, slot)
	}
	return nil
}

// envTaken reports whether key is already an env var of the container, either
// a literal entry or the destination of an env-mode secret slot.
func (f folder) envTaken(key string) bool {
	for _, entry := range f.container.Env {
		if existing, _, _ := strings.Cut(entry, "="); existing == key {
			return true
		}
	}
	for _, slot := range f.container.Secrets {
		if secretEnvName(slot) == key {
			return true
		}
	}
	return false
}

func (f folder) secretConflict(slot v1beta1.BlueprintSecretSlot) string {
	for _, existing := range f.container.Secrets {
		switch {
		case existing.Name == slot.Name:
			return "slot name already declared"
		case secretEnvName(slot) != "" && secretEnvName(existing) == secretEnvName(slot):
			return fmt.Sprintf("env destination %q already used by slot %q", secretEnvName(slot), existing.Name)
		case slot.MountPath != "" && existing.MountPath == slot.MountPath:
			return fmt.Sprintf("mount path %q already used by slot %q", slot.MountPath, existing.Name)
		}
	}
	if name := secretEnvName(slot); name != "" {
		for _, entry := range f.container.Env {
			if existing, _, _ := strings.Cut(entry, "="); existing == name {
				return fmt.Sprintf("env destination %q is already set by the container", name)
			}
		}
	}
	return ""
}

// secretEnvName is the env var an env-mode slot lands in ("" for file mode).
func secretEnvName(slot v1beta1.BlueprintSecretSlot) string {
	if strings.TrimSpace(slot.Mode) == v1beta1.BlueprintSecretModeFile {
		return ""
	}
	return strings.TrimSpace(slot.EnvName)
}

// NormalizeRequires trims, de-duplicates and sorts a capability list.
func NormalizeRequires(in []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, capability := range in {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if _, dup := seen[capability]; dup {
			continue
		}
		seen[capability] = struct{}{}
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
}

// cloneBlueprint deep-copies the parts of a blueprint Flatten mutates: the
// parameter list and each container's env, secrets and tty.
func cloneBlueprint(doc v1beta1.CellBlueprintDoc) v1beta1.CellBlueprintDoc {
	out := doc
	out.Spec.Parameters = append([]v1beta1.CellBlueprintParameter(nil), doc.Spec.Parameters...)
	out.Spec.Requires = append([]string(nil), doc.Spec.Requires...)
	out.Spec.Components = append([]v1beta1.BlueprintComponent(nil), doc.Spec.Components...)
	out.Spec.Cell.Containers = make([]v1beta1.BlueprintContainer, len(doc.Spec.Cell.Containers))
	for i, c := range doc.Spec.Cell.Containers {
		c.Env = append([]string(nil), c.Env...)
		c.Secrets = append([]v1beta1.BlueprintSecretSlot(nil), c.Secrets...)
		if c.Tty != nil {
			tty := *c.Tty
			tty.OnInit = append([]v1beta1.TtyStage(nil), c.Tty.OnInit...)
			c.Tty = &tty
		}
		out.Spec.Cell.Containers[i] = c
	}
	return out
}

// ValidateDeclaration checks the blueprint contract declarations (issue #1472,
// steps 8, 10, 11): typed parameters, components and harness modes. It is the
// apply-time gate; Flatten and the resolver enforce the per-cell rules.
func ValidateDeclaration(doc v1beta1.CellBlueprintDoc) error {
	if err := ValidateParameters("spec.parameters", doc.Spec.Parameters); err != nil {
		return err
	}
	names := map[string]struct{}{}
	modeOwners := 0
	for i := range doc.Spec.Components {
		comp := doc.Spec.Components[i]
		field := fmt.Sprintf("spec.components[%d]", i)
		if !componentNameRE.MatchString(comp.Name) {
			return fmt.Errorf(
				"%w: %s.name %q must be a lowercase identifier",
				errdefs.ErrBlueprintComponent,
				field,
				comp.Name,
			)
		}
		if _, dup := names[comp.Name]; dup {
			return fmt.Errorf("%w: %s.name %q is declared twice", errdefs.ErrBlueprintComponent, field, comp.Name)
		}
		names[comp.Name] = struct{}{}
		switch comp.Kind {
		case v1beta1.ComponentKindHarness, v1beta1.ComponentKindConnector, v1beta1.ComponentKindForge:
		default:
			return fmt.Errorf("%w: %s.kind %q must be harness, connector or forge",
				errdefs.ErrBlueprintComponent, field, comp.Kind)
		}
		if err := ValidateParameters(field+".parameters", comp.Parameters); err != nil {
			return err
		}
		if err := validateEnvEntries(field+".env", comp.Env); err != nil {
			return err
		}
		if comp.Group != "" && !componentNameRE.MatchString(comp.Group) {
			return fmt.Errorf("%w: %s.group %q must be a lowercase identifier",
				errdefs.ErrBlueprintComponent, field, comp.Group)
		}
		if err := validateConnector(field, comp); err != nil {
			return err
		}
		if len(comp.Modes) == 0 {
			continue
		}
		if comp.Kind != v1beta1.ComponentKindHarness {
			return fmt.Errorf("%w: %s.modes: only a harness component declares modes",
				errdefs.ErrBlueprintComponent, field)
		}
		if modeOwners++; modeOwners > 1 {
			return fmt.Errorf("%w: %s.modes: only one component may declare modes",
				errdefs.ErrBlueprintComponent, field)
		}
		for _, name := range sortedModes(comp.Modes) {
			mode := comp.Modes[name]
			modeField := field + ".modes." + name
			if !componentNameRE.MatchString(name) {
				return fmt.Errorf(
					"%w: %s: mode name must be a lowercase identifier",
					errdefs.ErrBlueprintMode,
					modeField,
				)
			}
			if err := ValidateParameters(modeField+".parameters", mode.Parameters); err != nil {
				return err
			}
			if err := validateEnvEntries(modeField+".env", mode.Env); err != nil {
				return err
			}
		}
	}
	// The validation view exercises target-container resolution and the
	// conflicts between always-on parts.
	if _, err := Flatten(doc, FlattenOptions{All: true}); err != nil {
		return err
	}
	// Mandatory components must also compose without the tolerance the All
	// view grants: a conflict among them would fail every cell.
	strict := doc
	strict.Spec.Components = nil
	for _, comp := range doc.Spec.Components {
		if !comp.Optional {
			comp.Modes = nil
			strict.Spec.Components = append(strict.Spec.Components, comp)
		}
	}
	_, err := Flatten(strict, FlattenOptions{})
	return err
}

// validateConnector checks the connector definition a component carries: only
// a connector component has one, it is well formed, and its delivery matches
// what the component declares — an env or file connector delivers through
// exactly one secret slot of that mode, the others through none.
func validateConnector(field string, comp v1beta1.BlueprintComponent) error {
	desc := comp.Connector
	if desc == nil {
		return nil
	}
	if comp.Kind != v1beta1.ComponentKindConnector {
		return fmt.Errorf("%w: %s.connector: only a connector component carries a connector definition",
			errdefs.ErrBlueprintComponent, field)
	}
	if err := connector.ValidateDescriptor(*desc); err != nil {
		return fmt.Errorf("%w: %s.connector: %w", errdefs.ErrBlueprintComponent, field, err)
	}
	switch desc.Delivery {
	case v1beta1.ConnectorDeliveryEnv, v1beta1.ConnectorDeliveryFile:
		if len(comp.Secrets) != 1 {
			return fmt.Errorf("%w: %s.connector: %s delivery needs exactly one secret slot",
				errdefs.ErrBlueprintComponent, field, desc.Delivery)
		}
		mode := strings.TrimSpace(comp.Secrets[0].Mode)
		if mode == "" {
			mode = v1beta1.BlueprintSecretModeEnv
		}
		if mode != desc.Delivery {
			return fmt.Errorf("%w: %s.connector: %s delivery needs a secret slot of mode %q",
				errdefs.ErrBlueprintComponent, field, desc.Delivery, desc.Delivery)
		}
	case v1beta1.ConnectorDeliveryRegistry:
		return fmt.Errorf("%w: %s.connector: a registry connector is a Realm concern, not a component",
			errdefs.ErrBlueprintComponent, field)
	default:
		if len(comp.Secrets) != 0 {
			return fmt.Errorf("%w: %s.connector: %s delivery declares no secret slot",
				errdefs.ErrBlueprintComponent, field, desc.Delivery)
		}
	}
	return nil
}

func validateEnvEntries(field string, env []string) error {
	for i, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !paramNameRE.MatchString(key) {
			return fmt.Errorf("%w: %s[%d] %q: want KEY=value", errdefs.ErrBlueprintComponent, field, i, entry)
		}
	}
	return nil
}
