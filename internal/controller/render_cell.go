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
	"sort"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/ctr"
	"github.com/eminwux/kukeon/internal/errdefs"
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
// #1418). It resolves the referenced Blueprint, merges the Realm → Space →
// Stack defaults under the Config's own fill (deeper wins per key), runs
// cellconfig.MaterializeWithName, re-applies the per-cell --env overrides,
// resolves realm image handles to their reference + digest, and stamps the
// provenance annotations. The create RPCs, the preview RPC, the OutOfSync
// reconciler and the restart re-apply all go through it, so every caller
// renders the same Cell for the same inputs.
//
// The cell lands in the Config's scope with empty coordinates defaulted
// (cellconfig.CellScope); that scope selects the defaults levels.
func renderCellFromConfig(
	r runner.Runner, cfg intmodel.CellConfig, cellName string, envOverrides []string,
) (cellRender, error) {
	cfgDoc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return cellRender{}, fmt.Errorf("decode lineage config: %w", err)
	}

	bpDoc, err := lookupConfigBlueprint(r, cfgDoc)
	if err != nil {
		return cellRender{}, err
	}

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

	cellDoc, err := cellconfig.MaterializeWithName(merged, bpDoc, cellName)
	if err != nil {
		return cellRender{}, fmt.Errorf("materialize cell: %w", err)
	}
	// Re-apply the cell's per-cell --env overrides last (P3 precedence, #1023).
	cellconfig.ApplyEnvOverrides(&cellDoc, envOverrides)

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

	digest, err := cellDigest(cellDoc)
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
	}{merged.Spec.Values, merged.Spec.Repos, merged.Spec.Secrets})
	annotations[cellconfig.AnnotationRenderOrigins] = formatOrigins(values)
	annotations[cellconfig.AnnotationRenderKukeonVersion] = buildinfo.Version()
	cellDoc.Metadata.Annotations = annotations

	return cellRender{Cell: cellDoc, Digest: digest, Values: values}, nil
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

// cellDigest hashes the rendered cell's identity, labels and spec. The
// provenance annotations are deliberately excluded so the digest only moves
// when the Cell itself would.
func cellDigest(doc v1beta1.CellDoc) (string, error) {
	data, err := json.Marshal(struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels,omitempty"`
		Spec   v1beta1.CellSpec  `json:"spec"`
	}{doc.Metadata.Name, doc.Metadata.Labels, doc.Spec})
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
