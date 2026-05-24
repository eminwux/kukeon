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

package run

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// cloneAllocMaxAttempts caps the gap-fill counter retry loop. The loop only
// restarts when a concurrent invocation wins the same N race (errdefs.
// ErrConfigExists from CreateConfig), so the cap is generous — at the
// 10-parallel AC each loser-retry sees one more candidate occupied, so the
// worst-case attempts is O(parallelism). The cap guards against unexpected
// daemon-side persistent EEXIST without spinning forever.
const cloneAllocMaxAttempts = 64

// counterSuffixPattern matches the trailing `-<digits>` of a clone name.
// The base name is the captured prefix (the source CellConfig's name).
var counterSuffixPattern = regexp.MustCompile(`^(.+)-(\d+)$`)

// cloneCellConfig forks the source CellConfig into a new persistent
// CellConfig and returns its document (issue #839). The function owns the
// gap-fill counter loop (or the explicit-name single-shot path when
// nameOverride is non-empty) and the atomic CreateConfig retry — the caller
// receives a Config that was successfully written under create-only
// semantics and can move on to materialise the cell from the clone.
func cloneCellConfig(
	ctx context.Context,
	client kukeonv1.Client,
	source v1beta1.CellConfigDoc,
	nameOverride string,
) (v1beta1.CellConfigDoc, error) {
	if nameOverride != "" {
		clone := buildCloneDoc(source, nameOverride)
		if _, err := client.CreateConfig(ctx, clone); err != nil {
			if errors.Is(err, errdefs.ErrConfigExists) {
				return v1beta1.CellConfigDoc{}, fmt.Errorf(
					"cellconfig %q already exists; --clone --name requires the name to be free",
					nameOverride,
				)
			}
			return v1beta1.CellConfigDoc{}, err
		}
		return clone, nil
	}

	for range cloneAllocMaxAttempts {
		candidate, err := nextCloneCandidate(ctx, client, source)
		if err != nil {
			return v1beta1.CellConfigDoc{}, err
		}
		clone := buildCloneDoc(source, candidate)
		_, createErr := client.CreateConfig(ctx, clone)
		if createErr == nil {
			return clone, nil
		}
		if errors.Is(createErr, errdefs.ErrConfigExists) {
			// A concurrent --clone won the race on this slot. Re-list to
			// pick the next free N — the loser advances by one each iteration
			// at minimum, so the worst case is O(parallelism).
			continue
		}
		return v1beta1.CellConfigDoc{}, createErr
	}
	return v1beta1.CellConfigDoc{}, fmt.Errorf(
		"failed to allocate clone of %q after %d attempts (persistent collision)",
		source.Metadata.Name, cloneAllocMaxAttempts,
	)
}

// nextCloneCandidate computes the lowest unused integer N >= 0 such that
// `<source>-<N>` is both (a) not the name of any existing CellConfig in scope
// and (b) not the suffix of an existing clone of <source>. The two sets are
// usually the same, but they can diverge: a manually-created Config named
// `<source>-3` (without the source-config annotation) shouldn't be picked as
// "clone N=3" but it does occupy the name slot, so a later --clone must skip
// past N=3 anyway. Combining both checks here makes the post-allocation
// CreateConfig call almost always succeed on the first try; the retry loop
// in cloneCellConfig only fires for true concurrent races.
func nextCloneCandidate(
	ctx context.Context,
	client kukeonv1.Client,
	source v1beta1.CellConfigDoc,
) (string, error) {
	configs, err := client.ListConfigs(
		ctx, source.Metadata.Realm, source.Metadata.Space, source.Metadata.Stack,
	)
	if err != nil {
		return "", fmt.Errorf("failed to list cellconfigs in scope: %w", err)
	}

	takenNames := make(map[string]struct{}, len(configs))
	cloneNs := make(map[int]struct{})
	for _, cfg := range configs {
		// ListConfigs returns a subtree (configs at scope or below). Filter to
		// the exact target scope so a clone of source@realm doesn't bump into
		// a same-named Config in a nested space — they live in different
		// directories and cannot collide on the create path.
		if cfg.Metadata.Realm != source.Metadata.Realm ||
			cfg.Metadata.Space != source.Metadata.Space ||
			cfg.Metadata.Stack != source.Metadata.Stack {
			continue
		}
		takenNames[cfg.Metadata.Name] = struct{}{}
		match := counterSuffixPattern.FindStringSubmatch(cfg.Metadata.Name)
		if match == nil || match[1] != source.Metadata.Name {
			continue
		}
		n, parseErr := strconv.Atoi(match[2])
		if parseErr != nil || n < 0 {
			continue
		}
		// The name suggests "clone of source"; verify via the source-config
		// annotation so a manually-named `<src>-<N>` Config doesn't reserve
		// the counter slot.
		body, getErr := client.GetConfig(ctx, v1beta1.CellConfigDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindCellConfig,
			Metadata: v1beta1.CellConfigMetadata{
				Name:  cfg.Metadata.Name,
				Realm: cfg.Metadata.Realm,
				Space: cfg.Metadata.Space,
				Stack: cfg.Metadata.Stack,
			},
		})
		if getErr != nil {
			return "", fmt.Errorf(
				"failed to read cellconfig %q while scanning clones of %q: %w",
				cfg.Metadata.Name, source.Metadata.Name, getErr,
			)
		}
		if !body.MetadataExists {
			continue
		}
		if body.Config.Metadata.Annotations[cellconfig.AnnotationSourceConfig] !=
			source.Metadata.Name {
			continue
		}
		cloneNs[n] = struct{}{}
	}

	for n := range cloneAllocMaxAttempts * 2 {
		if _, ok := cloneNs[n]; ok {
			continue
		}
		name := fmt.Sprintf("%s-%d", source.Metadata.Name, n)
		if _, ok := takenNames[name]; ok {
			continue
		}
		return name, nil
	}
	return "", fmt.Errorf(
		"no free counter slot found for clone of %q in realm %q",
		source.Metadata.Name, source.Metadata.Realm,
	)
}

// buildCloneDoc deep-copies source's spec into a fresh CellConfigDoc with the
// clone's name and the source-config annotation lineage marker. The clone
// inherits the source's scope coordinates and labels but otherwise stands
// alone — editing source.spec post-clone never propagates to existing clones.
func buildCloneDoc(source v1beta1.CellConfigDoc, cloneName string) v1beta1.CellConfigDoc {
	annotations := copyStringMap(source.Metadata.Annotations)
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[cellconfig.AnnotationSourceConfig] = source.Metadata.Name

	return v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:        cloneName,
			Realm:       source.Metadata.Realm,
			Space:       source.Metadata.Space,
			Stack:       source.Metadata.Stack,
			Labels:      copyStringMap(source.Metadata.Labels),
			Annotations: annotations,
		},
		Spec: deepCopyConfigSpec(source.Spec),
	}
}

// deepCopyConfigSpec returns a clone of spec where every map and pointer
// field is independent of the original. The map values themselves are
// either value types (CellConfigRepoFill, scalar string values) or carry
// pointer fields (CellConfigSecretFill.SecretRef) that the loop dereferences
// and re-wraps so a downstream `kuke apply -f` on either source or clone
// cannot mutate the other through shared pointers.
func deepCopyConfigSpec(spec v1beta1.CellConfigSpec) v1beta1.CellConfigSpec {
	out := v1beta1.CellConfigSpec{
		Blueprint: spec.Blueprint,
		Values:    copyStringMap(spec.Values),
	}
	if len(spec.Repos) > 0 {
		out.Repos = make(map[string]v1beta1.CellConfigRepoFill, len(spec.Repos))
		for k, v := range spec.Repos {
			out.Repos[k] = v
		}
	}
	if len(spec.Secrets) > 0 {
		out.Secrets = make(map[string]v1beta1.CellConfigSecretFill, len(spec.Secrets))
		for k, v := range spec.Secrets {
			fill := v1beta1.CellConfigSecretFill{}
			if v.SecretRef != nil {
				ref := *v.SecretRef
				fill.SecretRef = &ref
			}
			out.Secrets[k] = fill
		}
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
