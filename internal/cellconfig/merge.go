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
	return MergeDefaultsForCell(cfg, bp, layers, nil)
}

// gitLayersFor lists the git identity layers of a render, shallowest first
// (#1472): Realm → Space → Stack → Config, then what the cell chose for itself.
// A deeper layer wins per field; credentials merge by host.
func gitLayersFor(cfg v1beta1.CellConfigDoc, layers []DefaultsLayer, cell *v1beta1.ScopeDefaults) []GitLayer {
	out := make([]GitLayer, 0, len(layers)+1)
	for _, layer := range layers {
		if layer.Defaults != nil {
			out = append(out, GitLayer{Origin: layer.Origin, Git: layer.Defaults.Git})
		}
	}
	out = append(out, GitLayer{Origin: v1beta1.RenderOriginConfig, Git: cfg.Spec.Git})
	if cell != nil {
		out = append(out, GitLayer{Origin: v1beta1.RenderOriginCell, Git: cell.Git})
	}
	return out
}

// MergeDefaultsForCell is MergeDefaults with one more level applied after the
// Config: what a single cell chose for itself at create time (issue #1548).
// Only the cell's secret fills and git identity are read — a cell's values
// have their own channel (the mode parameters) — and they win over the Config
// and every scope default, attributed to the cell.
func MergeDefaultsForCell(
	cfg v1beta1.CellConfigDoc, bp v1beta1.CellBlueprintDoc, layers []DefaultsLayer, cell *v1beta1.ScopeDefaults,
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
	if cell != nil {
		for k, v := range cell.Secrets {
			secrets[k] = v
			secretOrigin[k] = v1beta1.RenderOriginCell
		}
	}

	mergedGit, gitOrigins := MergeGit(gitLayersFor(cfg, layers, cell))

	merged := cfg
	merged.Spec.Git = mergedGit
	merged.Spec.Values = nilIfEmpty(values)
	merged.Spec.Repos = nil
	if len(repos) > 0 {
		merged.Spec.Repos = repos
	}
	merged.Spec.Secrets = nil
	if len(secrets) > 0 {
		merged.Spec.Secrets = secrets
	}

	explained := explainMerge(
		bp,
		mergedFill{values, repos, secrets},
		mergedOrigins{valueOrigin, repoOrigin, secretOrigin},
	)
	explained = append(explained, explainGit(mergedGit, gitOrigins)...)
	SortRenderedValues(explained)
	return merged, explained
}

// GitLayer is one level of the git-identity hierarchy fed to MergeGit.
type GitLayer struct {
	Origin string
	Git    *v1beta1.ContainerGit
}

// GitOrigins records which level set each part of a merged git block.
type GitOrigins struct {
	Author, Committer, Signing, Sign string
	// Credentials maps a credential host to the level that set it.
	Credentials map[string]string
}

// MergeGit folds git blocks in the order given, later layers winning (issue
// #1472, step 6): author, committer and the sign list are replaced whole; the
// signing key group (signing, or the signingKey / allowedSigners paths) is
// replaced whole by any layer that sets a key; credentials merge by host, a
// later layer replacing the entry of a host an earlier one set and otherwise
// appending in first-seen order. Returns nil when no layer sets anything.
func MergeGit(layers []GitLayer) (*v1beta1.ContainerGit, GitOrigins) {
	origins := GitOrigins{Credentials: map[string]string{}}
	out := &v1beta1.ContainerGit{}
	found := false
	for _, layer := range layers {
		g := layer.Git
		if g == nil {
			continue
		}
		found = true
		if g.Author != nil {
			author := *g.Author
			out.Author, origins.Author = &author, layer.Origin
		}
		if g.Committer != nil {
			committer := *g.Committer
			out.Committer, origins.Committer = &committer, layer.Origin
		}
		if g.Signing != nil || g.SigningKey != "" {
			out.Signing, out.SigningKey, out.AllowedSigners = nil, g.SigningKey, g.AllowedSigners
			if g.Signing != nil {
				signing := *g.Signing
				out.Signing = &signing
			}
			origins.Signing = layer.Origin
		} else if g.AllowedSigners != "" {
			out.AllowedSigners = g.AllowedSigners
		}
		if len(g.Sign) > 0 {
			out.Sign, origins.Sign = append([]string(nil), g.Sign...), layer.Origin
		}
		for _, cred := range g.Credentials {
			host := strings.TrimSpace(cred.Host)
			replaced := false
			for i := range out.Credentials {
				if strings.TrimSpace(out.Credentials[i].Host) == host {
					out.Credentials[i], replaced = cred, true
					break
				}
			}
			if !replaced {
				out.Credentials = append(out.Credentials, cred)
			}
			origins.Credentials[host] = layer.Origin
		}
	}
	if !found {
		return nil, origins
	}
	return out, origins
}

// explainGit lists the merged default identity with the level that set each
// part. Credentials and signing keys are shown as secret coordinates only.
func explainGit(git *v1beta1.ContainerGit, origins GitOrigins) []v1beta1.RenderedValue {
	if git == nil {
		return nil
	}
	var out []v1beta1.RenderedValue
	add := func(key, value, origin string) {
		if value != "" {
			out = append(out, v1beta1.RenderedValue{
				Kind: v1beta1.RenderKindGit, Key: key, Value: value, Origin: origin,
			})
		}
	}
	if git.Author != nil {
		add("default.author", git.Author.Name+" <"+git.Author.Email+">", origins.Author)
	}
	if git.Committer != nil {
		add("default.committer", git.Committer.Name+" <"+git.Committer.Email+">", origins.Committer)
	}
	switch {
	case git.Signing != nil && git.Signing.SecretRef != nil:
		add("default.signing", describeSecretFill(v1beta1.CellConfigSecretFill{SecretRef: git.Signing.SecretRef}),
			origins.Signing)
	case git.SigningKey != "":
		add("default.signing", git.SigningKey, origins.Signing)
	}
	for _, cred := range git.Credentials {
		host := strings.TrimSpace(cred.Host)
		add("default.credential["+host+"]",
			describeSecretFill(v1beta1.CellConfigSecretFill{SecretRef: cred.SecretRef}), origins.Credentials[host])
	}
	return out
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
