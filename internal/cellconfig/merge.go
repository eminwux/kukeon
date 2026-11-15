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
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// DefaultsLayer is one level of the Realm → Space → Stack defaults hierarchy
// fed to MergeDefaults. Origin is one of the v1beta1.RenderOrigin* constants.
type DefaultsLayer struct {
	Origin   string
	Defaults *v1beta1.ScopeDefaults
}

// MergeDefaults folds the scope defaults into the Config's own fill (issue
// #1418). Layers are applied in the order given (the caller passes Realm,
// Space, Stack) and the Config is applied last, so for every key the deepest
// level that sets it wins. Maps merge per key; nothing is deep-merged inside a
// single repo or secret fill.
//
// Repo and secret defaults are kept only for slots the blueprint declares, so a
// realm-wide default never trips ValidateSlotFill's unknown-slot gate for a
// blueprint that does not use it. The Config's own fills are passed through
// untouched and stay strictly validated by MaterializeWithName.
//
// The returned CellConfigDoc is a copy; cfg is not modified. The second return
// lists every resolved value with its origin, including blueprint parameter
// defaults and blueprint inline repos that no level overrides, sorted by kind
// then key.
func MergeDefaults(
	cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc, layers []DefaultsLayer,
) (v1beta1.CellConfigDoc, []v1beta1.RenderedValue) {
	repoSlots, secretSlots := declaredSlots(bp)

	values := map[string]string{}
	repos := map[string]v1beta1.CellConfigRepoFill{}
	secrets := map[string]v1beta1.CellConfigSecretFill{}
	valueOrigin := map[string]string{}
	repoOrigin := map[string]string{}
	secretOrigin := map[string]string{}

	for _, layer := range layers {
		if layer.Defaults.IsZero() {
			continue
		}
		for k, v := range layer.Defaults.Values {
			values[k] = v
			valueOrigin[k] = layer.Origin
		}
		for k, v := range layer.Defaults.Repos {
			if _, ok := repoSlots[strings.TrimSpace(k)]; !ok {
				continue
			}
			repos[k] = v
			repoOrigin[k] = layer.Origin
		}
		for k, v := range layer.Defaults.Secrets {
			if _, ok := secretSlots[strings.TrimSpace(k)]; !ok {
				continue
			}
			secrets[k] = v
			secretOrigin[k] = layer.Origin
		}
	}
	for k, v := range cfg.Spec.Values {
		values[k] = v
		valueOrigin[k] = v1beta1.RenderOriginConfig
	}
	for k, v := range cfg.Spec.Repos {
		repos[k] = v
		repoOrigin[k] = v1beta1.RenderOriginConfig
	}
	for k, v := range cfg.Spec.Secrets {
		secrets[k] = v
		secretOrigin[k] = v1beta1.RenderOriginConfig
	}

	merged := cfg
	merged.Spec.Values = nilIfEmpty(values)
	merged.Spec.Repos = nil
	if len(repos) > 0 {
		merged.Spec.Repos = repos
	}
	merged.Spec.Secrets = nil
	if len(secrets) > 0 {
		merged.Spec.Secrets = secrets
	}

	return merged, explainMerge(
		bp,
		mergedFill{values, repos, secrets},
		mergedOrigins{valueOrigin, repoOrigin, secretOrigin},
	)
}

type mergedFill struct {
	values  map[string]string
	repos   map[string]v1beta1.CellConfigRepoFill
	secrets map[string]v1beta1.CellConfigSecretFill
}

type mergedOrigins struct {
	values  map[string]string
	repos   map[string]string
	secrets map[string]string
}

// explainMerge lists every merged value with its origin, adding the blueprint
// parameter defaults and inline repos no level overrides.
func explainMerge(bp v1beta1.CellBlueprintDoc, fill mergedFill, origins mergedOrigins) []v1beta1.RenderedValue {
	out := make([]v1beta1.RenderedValue, 0, len(fill.values)+len(fill.repos)+len(fill.secrets))
	for k, v := range fill.values {
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindValue, Key: k, Value: v, Origin: origins.values[k],
		})
	}
	for _, p := range bp.Spec.Parameters {
		if _, set := fill.values[p.Name]; set || p.Default == nil {
			continue
		}
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindValue, Key: p.Name, Value: *p.Default, Origin: v1beta1.RenderOriginBlueprint,
		})
	}
	for k, v := range fill.repos {
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindRepo, Key: k, Value: describeRepoFill(v), Origin: origins.repos[k],
		})
	}
	for _, repo := range inlineRepos(bp) {
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindRepo, Key: repo.key, Value: repo.value, Origin: v1beta1.RenderOriginBlueprint,
		})
	}
	for k, v := range fill.secrets {
		out = append(out, v1beta1.RenderedValue{
			Kind: v1beta1.RenderKindSecret, Key: k, Value: describeSecretFill(v), Origin: origins.secrets[k],
		})
	}
	SortRenderedValues(out)
	return out
}

// SortRenderedValues orders values by kind then key, the stable order the
// explain output and the provenance annotation use.
func SortRenderedValues(values []v1beta1.RenderedValue) {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Kind != values[j].Kind {
			return values[i].Kind < values[j].Kind
		}
		return values[i].Key < values[j].Key
	})
}

// declaredSlots returns the repo and secret slot names the blueprint declares
// across all containers (a repo with an inline url is not a slot).
func declaredSlots(bp v1beta1.CellBlueprintDoc) (map[string]struct{}, map[string]struct{}) {
	repos := map[string]struct{}{}
	secrets := map[string]struct{}{}
	for _, c := range bp.Spec.Cell.Containers {
		for _, r := range c.Repos {
			if strings.TrimSpace(r.URL) == "" {
				repos[strings.TrimSpace(r.Name)] = struct{}{}
			}
		}
		for _, s := range c.Secrets {
			secrets[strings.TrimSpace(s.Name)] = struct{}{}
		}
	}
	return repos, secrets
}

type inlineRepo struct{ key, value string }

// inlineRepos lists the blueprint repos that carry their own url.
func inlineRepos(bp v1beta1.CellBlueprintDoc) []inlineRepo {
	seen := map[string]struct{}{}
	var out []inlineRepo
	for _, c := range bp.Spec.Cell.Containers {
		for _, r := range c.Repos {
			if strings.TrimSpace(r.URL) == "" {
				continue
			}
			key := strings.TrimSpace(r.Name)
			if key == "" {
				key = strings.TrimSpace(r.URL)
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, inlineRepo{key: key, value: describeRepoFill(v1beta1.CellConfigRepoFill{
				URL: r.URL, Branch: r.Branch, Ref: r.Ref,
			})})
		}
	}
	return out
}

func describeRepoFill(f v1beta1.CellConfigRepoFill) string {
	out := strings.TrimSpace(f.URL)
	switch {
	case strings.TrimSpace(f.Ref) != "":
		out += "@" + strings.TrimSpace(f.Ref)
	case strings.TrimSpace(f.Branch) != "":
		out += "#" + strings.TrimSpace(f.Branch)
	}
	return out
}

func describeSecretFill(f v1beta1.CellConfigSecretFill) string {
	if f.SecretRef == nil {
		return ""
	}
	ref := f.SecretRef
	parts := []string{ref.Realm}
	for _, p := range []string{ref.Space, ref.Stack, ref.Cell} {
		if strings.TrimSpace(p) != "" {
			parts = append(parts, p)
		}
	}
	return "secret:" + strings.Join(parts, "/") + "/" + ref.Name
}

func nilIfEmpty(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	return in
}

// CellScope returns the realm/space/stack a cell materialized from cfg lands
// in — the Config's own coordinates with empty ones defaulted, exactly as
// MaterializeWithName stamps them onto the CellDoc.
func CellScope(cfg v1beta1.CellConfigDoc) (string, string, string) {
	return defaultScope(cfg.Metadata.Realm, consts.KukeonDefaultRealmName),
		defaultScope(cfg.Metadata.Space, consts.KukeonDefaultSpaceName),
		defaultScope(cfg.Metadata.Stack, consts.KukeonDefaultStackName)
}
