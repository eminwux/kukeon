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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/buildinfo"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// noDefaultsHash is the hash a level without a spec.defaults fill records.
const noDefaultsHash = "none"

// cellRender is the outcome of the daemon-side CellConfig render (issue
// #1418): the rendered cell (external shape, provenance annotations stamped),
// its digest, and every resolved value with the level that set it.
type cellRender struct {
	Cell   v1beta1.CellDoc
	Digest string
	Values []v1beta1.RenderedValue
}

// cellBinding is the per-cell input of a CellConfig render: everything a cell
// carries beyond what its Config says. The create RPCs fill it from the
// request; the OutOfSync reconciler and the restart re-apply rebuild it from
// the live cell's provenance, so a re-render reproduces the cell as created.
type cellBinding struct {
	// Name is the cell's name.
	Name string
	// EnvOverrides are the per-cell `--env KEY=VALUE` overrides.
	EnvOverrides []string
	// Mode is the harness mode picked for the cell; empty selects the
	// Config's default (#1472, step 11).
	Mode string
	// Params are per-cell `--param KEY=VALUE` values overriding the Config's.
	Params map[string]string
}

// bindingFromCell rebuilds the binding a live cell was created with from its
// recorded provenance.
func bindingFromCell(cell intmodel.Cell) cellBinding {
	binding := cellBinding{Name: cell.Metadata.Name}
	if prov := cell.Spec.Provenance; prov != nil {
		binding.EnvOverrides = prov.EnvOverrides
		binding.Mode = prov.Mode
		binding.Params = prov.ParamOverrides
	}
	return binding
}

// renderedImage is one realm image a render resolved.
type renderedImage struct {
	Name   string
	Ref    string
	Digest string
}

// scopeLevels holds the Realm, Space and Stack records a render reads its
// defaults (and the realm's images) from. A missing level is the zero value
// with found=false — a Config may be rendered before its target space/stack
// exists (CreateCell creates the parents).
type scopeLevels struct {
	realm      v1beta1.RealmDoc
	realmFound bool
	space      v1beta1.SpaceDoc
	spaceFound bool
	stack      v1beta1.StackDoc
	stackFound bool
}

// renderCellFromConfig is the single place a CellConfig becomes a Cell (issue
// #1418). It resolves the referenced Blueprint, folds in the components the
// Config enables and the harness mode picked for the cell (#1472), merges the
// Realm → Space → Stack defaults — values, slot fills and the git identity —
// under the Config's own fill (deeper wins per key), applies the per-cell
// --param overrides, runs cellconfig.MaterializeWithName, re-applies the
// per-cell --env overrides, resolves realm image handles to their reference +
// digest, and stamps the provenance annotations. The create RPCs, the preview
// RPC, the OutOfSync reconciler and the restart re-apply all go through it, so
// every caller renders the same Cell for the same inputs.
// The cell lands in the Config's scope with empty coordinates defaulted
// (cellconfig.CellScope); that scope selects the defaults levels.
//
//nolint:funlen // the single render pipeline, kept linear so its step order reads top to bottom.
func renderCellFromConfig(r runner.Runner, cfg intmodel.CellConfig, binding cellBinding) (cellRender, error) {
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return cellRender{}, fmt.Errorf("decode lineage config: %w", err)
	}

	bpDoc, err := lookupConfigBlueprint(r, cfgDoc)
	if err != nil {
		return cellRender{}, err
	}

	binding = inferMode(bpDoc, cfgDoc, binding)
	flattenOpts := cellblueprint.FlattenOptions{Components: cfgDoc.Spec.Components, Mode: binding.Mode}
	if modes := cfgDoc.Spec.Modes; modes != nil {
		flattenOpts.AllowedModes, flattenOpts.DefaultMode = modes.Enabled, modes.Default
	}
	flat, err := cellblueprint.Flatten(bpDoc, flattenOpts)
	if err != nil {
		return cellRender{}, fmt.Errorf("config %q: %w", cfgDoc.Metadata.Name, err)
	}
	// A Config carries its own values; the one thing a cell may set for
	// itself is what the mode it was created in asks for.
	for key := range binding.Params {
		if !slices.Contains(flat.ModeParameters, key) {
			return cellRender{}, fmt.Errorf(
				"%w: --param %q is not a parameter of harness mode %q (mode parameters: %s); "+
					"set other values in the CellConfig",
				errdefs.ErrBlueprintInvalid, key, flat.Mode, strings.Join(flat.ModeParameters, ", "))
		}
	}
	contract := contractValues(bpDoc, cfgDoc, flat, binding)
	if cfgDoc, err = dropInactiveSecretFills(cfgDoc, bpDoc, flat.Doc); err != nil {
		return cellRender{}, fmt.Errorf("config %q: %w", cfgDoc.Metadata.Name, err)
	}
	bpDoc = flat.Doc

	realmName, spaceName, stackName := cellconfig.CellScope(cfgDoc)
	levels, err := loadScopeLevels(r, realmName, spaceName, stackName)
	if err != nil {
		return cellRender{}, err
	}

	merged, values := cellconfig.MergeDefaults(cfgDoc, bpDoc, []cellconfig.DefaultsLayer{
		{Origin: v1beta1.RenderOriginRealm, Defaults: levels.realm.Spec.Defaults},
		{Origin: v1beta1.RenderOriginSpace, Defaults: levels.space.Spec.Defaults.Fill()},
		{Origin: v1beta1.RenderOriginStack, Defaults: levels.stack.Spec.Defaults},
	})
	merged, values = applyParamOverrides(merged, values, binding.Params)
	values = append(values, contract...)

	cellDoc, err := cellconfig.MaterializeWithName(merged, bpDoc, binding.Name)
	if err != nil {
		return cellRender{}, fmt.Errorf("materialize cell: %w", err)
	}
	// Re-apply the cell's per-cell --env overrides last (P3 precedence, #1023).
	cellconfig.ApplyEnvOverrides(&cellDoc, binding.EnvOverrides)

	// Record what the Config alone does not say, so a re-render of this cell
	// (OutOfSync, restart) picks the same mode and params; and stamp the
	// capability requirement the container create enforces.
	cellDoc.Spec.Requires = cellblueprint.NormalizeRequires(append(
		append([]string(nil), bpDoc.Spec.Requires...), cfgDoc.Spec.Requires...))
	if prov := cellDoc.Spec.Provenance; prov != nil {
		prov.Mode = flat.Mode
		if len(binding.Params) > 0 {
			prov.ParamOverrides = make(map[string]string, len(binding.Params))
			for k, v := range binding.Params {
				prov.ParamOverrides[k] = v
			}
		}
	}
	if flat.Mode != "" {
		if cellDoc.Metadata.Labels == nil {
			cellDoc.Metadata.Labels = map[string]string{}
		}
		cellDoc.Metadata.Labels[cellblueprint.LabelMode] = flat.Mode
	}

	sourceValues, err := sourceIdentityValues(r, cellDoc, values)
	if err != nil {
		return cellRender{}, err
	}
	values = append(values, sourceValues...)

	images, err := resolveRealmImages(r, &cellDoc, levels)
	if err != nil {
		return cellRender{}, err
	}
	for _, img := range images {
		value := img.Ref
		if img.Digest != "" {
			value += "@" + img.Digest
		}
		values = append(values, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindImage, Key: img.Name, Value: value, Origin: v1beta1.RenderOriginRealm,
		})
	}
	cellconfig.SortRenderedValues(values)

	digest, err := cellDigest(cellDoc, images)
	if err != nil {
		return cellRender{}, err
	}

	annotations := map[string]string{}
	for k, v := range cellDoc.Metadata.Annotations {
		annotations[k] = v
	}
	annotations[cellconfig.AnnotationRenderDigest] = digest
	annotations[cellconfig.AnnotationRenderConfigRevision] = shortHash(cfg.Document)
	annotations[cellconfig.AnnotationRenderRealmDefaults] = levelStamp(
		levels.realm.Metadata.Generation, levels.realm.Spec.Defaults)
	annotations[cellconfig.AnnotationRenderSpaceDefaults] = levelStamp(
		levels.space.Metadata.Generation, levels.space.Spec.Defaults.Fill())
	annotations[cellconfig.AnnotationRenderStackDefaults] = levelStamp(
		levels.stack.Metadata.Generation, levels.stack.Spec.Defaults)
	annotations[cellconfig.AnnotationRenderImageDigests] = formatImageDigests(images)
	annotations[cellconfig.AnnotationRenderValuesHash] = hashJSON(struct {
		Values  map[string]string                       `json:"values,omitempty"`
		Repos   map[string]v1beta1.CellConfigRepoFill   `json:"repos,omitempty"`
		Secrets map[string]v1beta1.CellConfigSecretFill `json:"secrets,omitempty"`
		Git     *v1beta1.ContainerGit                   `json:"git,omitempty"`
	}{merged.Spec.Values, merged.Spec.Repos, merged.Spec.Secrets, merged.Spec.Git})
	annotations[cellconfig.AnnotationRenderOrigins] = formatOrigins(values)
	annotations[cellconfig.AnnotationRenderKukeonVersion] = buildinfo.Version()
	cellDoc.Metadata.Annotations = annotations

	return cellRender{Cell: cellDoc, Digest: digest, Values: values}, nil
}

// inferMode picks the harness mode a cell's own inputs imply when the create
// names none: a cell that supplies a parameter exactly one enabled mode
// declares is created in that mode. `--param PROMPT=…` therefore needs no
// `--mode headless`, and neither does `--env PROMPT=…` — the way a dispatched
// run was started before modes existed (#1472, step 11), when one onInit
// branched on $PROMPT. A mode parameter supplied only through --env is taken
// as that parameter's value, so a required one is satisfied. An explicit mode,
// or inputs that fit no mode or more than one, are left alone.
func inferMode(bp v1beta1.CellBlueprintDoc, cfg v1beta1.CellConfigDoc, binding cellBinding) cellBinding {
	if binding.Mode != "" || (len(binding.Params) == 0 && len(binding.EnvOverrides) == 0) {
		return binding
	}
	env := map[string]string{}
	for _, kv := range binding.EnvOverrides {
		if key, value, ok := strings.Cut(kv, "="); ok && value != "" {
			env[key] = value
		}
	}
	supplied := func(name string) bool {
		_, param := binding.Params[name]
		_, override := env[name]
		return param || override
	}
	var picked string
	var parameters []v1beta1.CellBlueprintParameter
	for _, comp := range bp.Spec.Components {
		if comp.Optional && !slices.Contains(cfg.Spec.Components, comp.Name) {
			continue
		}
		for name, mode := range comp.Modes {
			if cfg.Spec.Modes != nil && len(cfg.Spec.Modes.Enabled) > 0 &&
				!slices.Contains(cfg.Spec.Modes.Enabled, name) {
				continue
			}
			if !slices.ContainsFunc(mode.Parameters, func(p v1beta1.CellBlueprintParameter) bool {
				return supplied(p.Name)
			}) {
				continue
			}
			if picked != "" {
				return binding // ambiguous: more than one mode fits
			}
			picked, parameters = name, mode.Parameters
		}
	}
	if picked == "" {
		return binding
	}
	binding.Mode = picked
	params := make(map[string]string, len(binding.Params))
	for k, v := range binding.Params {
		params[k] = v
	}
	for _, p := range parameters {
		if _, set := params[p.Name]; !set {
			if value, ok := env[p.Name]; ok {
				params[p.Name] = value
			}
		}
	}
	binding.Params = params
	return binding
}

// dropInactiveSecretFills removes the Config's fills for secret slots that
// belong to a part of the blueprint this cell does not use: a harness mode
// other than the picked one, or an optional component the Config leaves off.
// One Config serves cells in every mode it enables (#1472, step 11), so it
// fills the headless API-key slot even when the cell being rendered is an
// interactive one — where that slot is not declared at all and the strict
// unknown-slot gate would otherwise refuse the Config. A fill for a slot no
// component or mode declares is left in place and still fails that gate.
func dropInactiveSecretFills(
	cfg v1beta1.CellConfigDoc, bp, picked v1beta1.CellBlueprintDoc,
) (v1beta1.CellConfigDoc, error) {
	if len(cfg.Spec.Secrets) == 0 || len(bp.Spec.Components) == 0 {
		return cfg, nil
	}
	all, err := cellblueprint.Flatten(bp, cellblueprint.FlattenOptions{All: true})
	if err != nil {
		return cfg, err
	}
	slots := func(doc v1beta1.CellBlueprintDoc) map[string]struct{} {
		out := map[string]struct{}{}
		for _, c := range doc.Spec.Cell.Containers {
			for _, slot := range c.Secrets {
				out[strings.TrimSpace(slot.Name)] = struct{}{}
			}
		}
		return out
	}
	declared, active := slots(all.Doc), slots(picked)
	kept := make(map[string]v1beta1.CellConfigSecretFill, len(cfg.Spec.Secrets))
	for name, fill := range cfg.Spec.Secrets {
		_, isDeclared := declared[strings.TrimSpace(name)]
		_, isActive := active[strings.TrimSpace(name)]
		if isDeclared && !isActive {
			continue
		}
		kept[name] = fill
	}
	if len(kept) == 0 {
		kept = nil
	}
	cfg.Spec.Secrets = kept
	return cfg, nil
}

// contractValues explains the blueprint-contract choices of a render: the
// enabled components, the picked harness mode and the required image
// capabilities, each with the level that decided it.
func contractValues(
	bp v1beta1.CellBlueprintDoc, cfg v1beta1.CellConfigDoc, flat cellblueprint.Flattened, binding cellBinding,
) []v1beta1.RenderedValue {
	var out []v1beta1.RenderedValue
	for _, name := range flat.Components {
		for _, comp := range bp.Spec.Components {
			if comp.Name != name {
				continue
			}
			origin := v1beta1.RenderOriginBlueprint
			if comp.Optional {
				origin = v1beta1.RenderOriginConfig
			}
			out = append(out, v1beta1.RenderedValue{
				Kind: v1beta1.RenderKindComponent, Key: name, Value: comp.Kind, Origin: origin,
			})
		}
	}
	if flat.Mode != "" {
		origin := v1beta1.RenderOriginBlueprint
		switch {
		case binding.Mode != "":
			origin = v1beta1.RenderOriginCell
		case cfg.Spec.Modes != nil && (cfg.Spec.Modes.Default != "" || len(cfg.Spec.Modes.Enabled) > 0):
			origin = v1beta1.RenderOriginConfig
		}
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindMode, Key: "mode", Value: flat.Mode, Origin: origin,
		})
	}
	fromBlueprint := flat.Doc.Spec.Requires
	for _, capability := range cellblueprint.NormalizeRequires(append(
		append([]string(nil), fromBlueprint...), cfg.Spec.Requires...)) {
		origin := v1beta1.RenderOriginConfig
		if slices.Contains(fromBlueprint, capability) {
			origin = v1beta1.RenderOriginBlueprint
		}
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindRequires, Key: capability, Value: capability, Origin: origin,
		})
	}
	return out
}

// applyParamOverrides layers the per-cell --param values over the merged
// Config values and re-attributes them to the cell in the explanation.
func applyParamOverrides(
	merged v1beta1.CellConfigDoc, values []v1beta1.RenderedValue, params map[string]string,
) (v1beta1.CellConfigDoc, []v1beta1.RenderedValue) {
	if len(params) == 0 {
		return merged, values
	}
	combined := make(map[string]string, len(merged.Spec.Values)+len(params))
	for k, v := range merged.Spec.Values {
		combined[k] = v
	}
	kept := values[:0:0]
	for _, v := range values {
		if _, overridden := params[v.Key]; v.Kind == v1beta1.RenderKindValue && overridden {
			continue
		}
		kept = append(kept, v)
	}
	for k, v := range params {
		combined[k] = v
		kept = append(kept, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindValue, Key: k, Value: v, Origin: v1beta1.RenderOriginCell,
		})
	}
	merged.Spec.Values = combined
	return merged, kept
}

// sourceIdentityValues explains, per repo, the git identity and credential the
// cell will actually use (#1472): the clone URL after its transport followed
// the credential, the author and committer, the credential's secret and type,
// and the signing key. Each is attributed to the level that set it — the
// level that filled the repo slot when the source states it itself, else the
// level that set the default. Secret material is never read: only a secret's
// type is looked up.
func sourceIdentityValues(
	r runner.Runner, cellDoc v1beta1.CellDoc, values []v1beta1.RenderedValue,
) ([]v1beta1.RenderedValue, error) {
	cell, err := apischeme.ConvertCellDocToInternal(cellDoc)
	if err != nil {
		return nil, fmt.Errorf("convert rendered cell: %w", err)
	}
	origins := map[string]string{}
	for _, v := range values {
		origins[v.Kind+":"+v.Key] = v.Origin
	}
	originOf := func(kind, key string) string {
		if origin, ok := origins[kind+":"+key]; ok {
			return origin
		}
		return v1beta1.RenderOriginBlueprint
	}
	typeOf := func(ref intmodel.ContainerSecretRef) string {
		got, getErr := r.GetSecret(intmodel.Secret{Metadata: intmodel.SecretMetadata(ref)})
		if getErr != nil {
			return ""
		}
		return got.Spec.EffectiveType()
	}

	var out []v1beta1.RenderedValue
	seen := map[string]struct{}{}
	for _, c := range cell.Spec.Containers {
		for _, id := range gitcred.Describe(c, typeOf) {
			if _, dup := seen[id.Repo]; dup {
				continue
			}
			seen[id.Repo] = struct{}{}
			repoOrigin := originOf(v1beta1.RenderKindRepo, id.Repo)
			add := func(key string, field gitcred.Field, defaultKey string) {
				if field.Value == "" {
					return
				}
				origin := originOf(v1beta1.RenderKindGit, defaultKey)
				if field.FromSource {
					origin = repoOrigin
				}
				out = append(out, v1beta1.RenderedValue{
					Kind: v1beta1.RenderKindGit, Key: id.Repo + "." + key, Value: field.Value, Origin: origin,
				})
			}
			add("author", id.Author, "default.author")
			add("committer", id.Committer, "default.committer")
			add("credential", id.Credential, "default.credential["+id.CredentialHost+"]")
			add("signing", id.Signing, "default.signing")
			out = append(out, v1beta1.RenderedValue{
				Kind: v1beta1.RenderKindGit, Key: id.Repo + ".url", Value: id.URL, Origin: repoOrigin,
			})
		}
	}
	return out, nil
}

// lookupConfigBlueprint fetches and decodes the Blueprint a Config references.
func lookupConfigBlueprint(r runner.Runner, cfgDoc v1beta1.CellConfigDoc) (v1beta1.CellBlueprintDoc, error) {
	ref := cfgDoc.Spec.Blueprint
	bpCarrier, getErr := r.GetBlueprint(intmodel.CellBlueprint{
		Metadata: intmodel.CellBlueprintMetadata{
			Name:  ref.Name,
			Realm: ref.Realm,
			Space: ref.Space,
			Stack: ref.Stack,
		},
	})
	if getErr != nil {
		if errors.Is(getErr, errdefs.ErrBlueprintNotFound) {
			return v1beta1.CellBlueprintDoc{}, fmt.Errorf(
				"%w: referenced blueprint %q not found", errdefs.ErrBlueprintNotFound, ref.Name)
		}
		return v1beta1.CellBlueprintDoc{}, fmt.Errorf("read referenced blueprint %q: %w", ref.Name, getErr)
	}
	bpDoc, bpErr := apischeme.ConvertCellBlueprintToExternal(bpCarrier)
	if bpErr != nil {
		return v1beta1.CellBlueprintDoc{}, fmt.Errorf("decode referenced blueprint: %w", bpErr)
	}
	return bpDoc, nil
}

// loadScopeLevels reads the cell's Realm, Space and Stack. A not-found level
// contributes no defaults; any other read error fails the render, so a
// transient read failure never renders a cell without its defaults.
func loadScopeLevels(r runner.Runner, realmName, spaceName, stackName string) (scopeLevels, error) {
	var levels scopeLevels

	realm, err := r.GetRealm(intmodel.Realm{Metadata: intmodel.RealmMetadata{Name: realmName}})
	switch {
	case err == nil:
		doc, convErr := apischeme.BuildRealmExternalFromInternal(realm, apischeme.VersionV1Beta1)
		if convErr != nil {
			return levels, fmt.Errorf("convert realm %q: %w", realmName, convErr)
		}
		levels.realm, levels.realmFound = doc, true
	case !errors.Is(err, errdefs.ErrRealmNotFound):
		return levels, fmt.Errorf("read realm %q defaults: %w", realmName, err)
	}

	space, err := r.GetSpace(intmodel.Space{
		Metadata: intmodel.SpaceMetadata{Name: spaceName},
		Spec:     intmodel.SpaceSpec{RealmName: realmName},
	})
	switch {
	case err == nil:
		doc, convErr := apischeme.BuildSpaceExternalFromInternal(space, apischeme.VersionV1Beta1)
		if convErr != nil {
			return levels, fmt.Errorf("convert space %q: %w", spaceName, convErr)
		}
		levels.space, levels.spaceFound = doc, true
	case !errors.Is(err, errdefs.ErrSpaceNotFound) && !errors.Is(err, errdefs.ErrRealmNotFound):
		return levels, fmt.Errorf("read space %q defaults: %w", spaceName, err)
	}

	stack, err := r.GetStack(intmodel.Stack{
		Metadata: intmodel.StackMetadata{Name: stackName},
		Spec:     intmodel.StackSpec{RealmName: realmName, SpaceName: spaceName},
	})
	switch {
	case err == nil:
		doc, convErr := apischeme.BuildStackExternalFromInternal(stack, apischeme.VersionV1Beta1)
		if convErr != nil {
			return levels, fmt.Errorf("convert stack %q: %w", stackName, convErr)
		}
		levels.stack, levels.stackFound = doc, true
	case !errors.Is(err, errdefs.ErrStackNotFound) &&
		!errors.Is(err, errdefs.ErrSpaceNotFound) &&
		!errors.Is(err, errdefs.ErrRealmNotFound):
		return levels, fmt.Errorf("read stack %q defaults: %w", stackName, err)
	}

	return levels, nil
}

// resolveRealmImages rewrites every container whose image names a realm image
// entry to the entry's reference, and looks the reference's digest up in the
// realm's containerd namespace. An image that is not present yet (a registry
// image not pulled, a Dockerfile entry not built) resolves with an empty
// digest; the OutOfSync detector only compares two known digests.
func resolveRealmImages(r runner.Runner, cellDoc *v1beta1.CellDoc, levels scopeLevels) ([]renderedImage, error) {
	if !levels.realmFound || len(levels.realm.Spec.Images) == 0 {
		return nil, nil
	}
	realmName := levels.realm.Metadata.Name
	namespace := levels.realm.Spec.Namespace

	byName := map[string]renderedImage{}
	for i := range cellDoc.Spec.Containers {
		c := &cellDoc.Spec.Containers[i]
		img, ok := cellconfig.FindRealmImage(levels.realm.Spec.Images, c.Image)
		if !ok {
			continue
		}
		ref := cellconfig.RealmImageRef(realmName, img)
		c.Image = ref
		if _, seen := byName[img.Name]; seen {
			continue
		}
		digest, err := imageDigest(r, namespace, ref)
		if err != nil {
			return nil, fmt.Errorf("resolve realm image %q: %w", img.Name, err)
		}
		byName[img.Name] = renderedImage{Name: img.Name, Ref: ref, Digest: digest}
	}

	out := make([]renderedImage, 0, len(byName))
	for _, img := range byName {
		out = append(out, img)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// imageDigest returns the digest of ref in namespace, or "" when the image is
// not present. Other lookup failures surface.
func imageDigest(r runner.Runner, namespace, ref string) (string, error) {
	if strings.TrimSpace(namespace) == "" {
		return "", nil
	}
	info, err := r.GetImage(namespace, ctr.NormalizeImageReference(ref))
	if err != nil {
		if errors.Is(err, errdefs.ErrImageNotFound) {
			return "", nil
		}
		return "", err
	}
	return info.Digest, nil
}

// cellDigest hashes the rendered cell's identity, labels and spec, plus the
// digest each realm image the cell uses resolved to. The images are folded in
// because a rebuild under the same tag moves the digest without changing the
// spec, and a create guarded by ExpectedDigest must refuse that change. The
// provenance annotations are otherwise excluded so the digest only moves when
// the Cell (or an image it runs) would.
func cellDigest(doc v1beta1.CellDoc, images []renderedImage) (string, error) {
	imageDigests := make(map[string]string, len(images))
	for _, img := range images {
		imageDigests[img.Name] = img.Ref + "@" + img.Digest
	}
	data, err := json.Marshal(struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels,omitempty"`
		Spec   v1beta1.CellSpec  `json:"spec"`
		Images map[string]string `json:"images,omitempty"`
	}{doc.Metadata.Name, doc.Metadata.Labels, doc.Spec, imageDigests})
	if err != nil {
		return "", fmt.Errorf("digest rendered cell: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// levelStamp formats a level's defaults provenance as
// "generation=<n> hash=<h>".
func levelStamp(generation int64, defaults *v1beta1.ScopeDefaults) string {
	return "generation=" + strconv.FormatInt(generation, 10) + " hash=" + defaultsHash(defaults)
}

// defaultsHash is the content hash of a level's defaults fill, "none" when the
// level sets none.
func defaultsHash(defaults *v1beta1.ScopeDefaults) string {
	if defaults.IsZero() {
		return noDefaultsHash
	}
	return hashJSON(defaults)
}

// stampHash extracts the hash part of a levelStamp value.
func stampHash(stamp string) string {
	for _, field := range strings.Fields(stamp) {
		if h, ok := strings.CutPrefix(field, "hash="); ok {
			return h
		}
	}
	return ""
}

func hashJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return shortHash(data)
}

func shortHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func formatImageDigests(images []renderedImage) string {
	parts := make([]string, 0, len(images))
	for _, img := range images {
		parts = append(parts, img.Name+"="+img.Digest)
	}
	return strings.Join(parts, ",")
}

// parseImageDigests is the inverse of formatImageDigests.
func parseImageDigests(value string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(value, ",") {
		name, digest, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name != "" {
			out[name] = digest
		}
	}
	return out
}

func formatOrigins(values []v1beta1.RenderedValue) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, v.Kind+":"+v.Key+"="+v.Origin)
	}
	return strings.Join(parts, ",")
}

// RealmImages resolves a realm's spec.images to their references and their
// digests in the realm's containerd namespace (#1418). An image that is not
// present yet reports an empty digest; a lookup failure is reported the same
// way so a listing never fails on one bad entry.
func (b *Exec) RealmImages(realm intmodel.Realm) []v1beta1.RealmImageStatus {
	doc, err := apischeme.BuildRealmExternalFromInternal(realm, apischeme.VersionV1Beta1)
	if err != nil || len(doc.Spec.Images) == 0 {
		return nil
	}
	out := make([]v1beta1.RealmImageStatus, 0, len(doc.Spec.Images))
	for _, img := range doc.Spec.Images {
		ref := cellconfig.RealmImageRef(doc.Metadata.Name, img)
		digest, digestErr := imageDigest(b.runner, doc.Spec.Namespace, ref)
		if digestErr != nil {
			b.logger.DebugContext(b.ctx, "realm image digest lookup failed",
				"realm", doc.Metadata.Name, "image", img.Name, "error", digestErr)
			digest = ""
		}
		out = append(out, v1beta1.RealmImageStatus{
			Name:   img.Name,
			Ref:    ref,
			Digest: digest,
			Built:  img.Build != nil,
		})
	}
	return out
}
