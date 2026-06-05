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

// Package teamrender renders a project's ProjectTeam roster into a set of
// CellBlueprint and CellConfig documents — one (CellBlueprint, CellConfig)
// pair per `(role × harness)` (epic #792, step 3 #1042). For each pair the
// pipeline:
//
//  1. needs-merge — `union(role.yaml.needs.image ⊕ kuketeam per-role
//     needs.image)`, deduplicated and lexicographically sorted so two runs
//     against the same inputs produce byte-identical output.
//  2. image-select — match the merged capability set against the agents
//     source's ImageCatalog, picking the first entry whose `harness`
//     matches and whose `capabilities` superset the merged needs. A miss
//     surfaces errdefs.ErrTeamImageNoMatch naming the first unmet
//     capability + the operator-actionable "build/label an image" hint.
//  3. render — load the harness's blueprint template (relative to the
//     materialized cache dir), substitute `${KEY}` placeholders for
//     `${ROLE}`, `${HARNESS}`, `${IMAGE}`, `${NEEDS}`, and the role's
//     native per-harness config knobs (`${SETTINGS}` for claude,
//     `${SANDBOX}`/`${APPROVAL}` for codex, `${PERMISSIONS}` for
//     opencode), then yaml-parse into a CellBlueprintDoc. Per the
//     umbrella's key decision the role's native per-harness config is
//     wired verbatim — no transpile.
//  4. bind — produce a CellConfig that references the just-rendered
//     blueprint and carries (a) operator facts pulled from
//     `~/.kuke/kuketeams.yaml` (git author/committer/signingKey/registry
//     stamped into Values), (b) the project's cloned repo URL stamped
//     into the `project` repo slot fill, and (c) any operator secret
//     refs from `tc.spec.secrets` that the role declares it needs.
//  5. label — every rendered CellBlueprint and CellConfig carries
//     `metadata.labels[kukeon.io/team] = <project>` so the daemon-side
//     prune-apply machinery from #1029 can converge the project's slice
//     in step 4 (#1043) without touching other teams' objects.
//
// The package writes nothing to disk and runs no external commands — it
// reads the materialized template files prepared by teamsource (#1041) and
// produces in-memory v1beta1 documents. `--dry-run` consumers marshal the
// Result to YAML; the apply path in step 4 (#1043) hands the same Result
// straight to ApplyDocumentsForTeam.
package teamrender

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// DefaultRealm is the realm rendered objects bind to when Inputs.Realm is
// empty. Matches the `default` user realm provisioned by `kuke init`
// (see internal/consts) — team workloads live in the user realm, not in
// `kuke-system`.
const DefaultRealm = "default"

// ProjectRepoSlotName is the convention for the structural repo slot that
// carries the project's clone URL. The umbrella (epic #792) names `project`
// and `agents` as the two repos a team cell typically clones; this package
// owns the contract that blueprint templates declare the slot with this
// name, and `BindConfig` fills it from `Inputs.ProjectRepoURL`.
const ProjectRepoSlotName = "project"

// AgentsRepoSlotName mirrors ProjectRepoSlotName for the pinned agents
// source clone. `BindConfig` fills it from the operator's
// `TeamsConfig.spec.sources[<owner/repo>]` mapping when the blueprint
// declares the slot.
const AgentsRepoSlotName = "agents"

// Inputs collects the per-project facts that vary by `kuke team init`
// invocation. Project is the rendered objects' team label and the basis
// for the blueprint/config name when the template did not supply one.
// ProjectRepoURL is the clone URL for the project repo — typically read
// from `git -C <projectDir> remote get-url origin` by the caller.
type Inputs struct {
	Project        string
	ProjectRepoURL string
	Realm          string
	// Build is true under `kuke team init --build`: the rendered blueprint
	// binds the locally-built `kukeon.internal/<ref>:<version>` image (the tag
	// teambuild produces) instead of the catalog entry's published `Image`.
	// SourceRef supplies the `<version>` tag suffix — the agents source's
	// pinned ref — so the bound ref matches the built tag byte-for-byte. When
	// Build is false (the default), the catalog's published Image is bound.
	Build     bool
	SourceRef string
}

// Result carries the rendered objects from one project's roster. Each
// CellBlueprint at index i has its companion CellConfig at the same
// index in Configs, simplifying the apply loop in step 4 (#1043).
// Selections carries the ImageCatalog entry that satisfied each
// (role × harness) pair at the same index — `kuke team init --build`
// hands this slice to teambuild.BuildAll to drive the local build path
// (#1064). The slice is deduplicated by entry.Ref so a catalog entry
// reused across multiple (role × harness) pairs builds once.
type Result struct {
	Blueprints []*v1beta1.CellBlueprintDoc
	Configs    []*v1beta1.CellConfigDoc
	Selections []*model.ImageCatalogEntry
}

// Render runs the full per-(role × harness) pipeline against a loaded
// Bundle. The outer loop is project-deterministic: roles are visited in
// the order pt.Spec.Roles declares them and harnesses in the order
// pt.Spec.Defaults.Harnesses declares them, so the same inputs always
// produce the same output ordering.
func Render(
	bundle *teamsource.Bundle,
	pt *model.ProjectTeam,
	tc *model.TeamsConfig,
	in Inputs,
) (*Result, error) {
	if bundle == nil {
		return nil, errors.New("teamrender.Render: nil bundle")
	}
	if pt == nil {
		return nil, errors.New("teamrender.Render: nil ProjectTeam")
	}
	project := strings.TrimSpace(in.Project)
	if project == "" {
		return nil, errors.New("teamrender.Render: project name required")
	}
	realm := strings.TrimSpace(in.Realm)
	if realm == "" {
		realm = DefaultRealm
	}

	res := &Result{}
	seenSelections := map[string]struct{}{}
	for _, ptRole := range pt.Spec.Roles {
		role, ok := bundle.Roles[ptRole.Ref]
		if !ok {
			return nil, fmt.Errorf("%w: %q", errdefs.ErrTeamRoleNotLoaded, ptRole.Ref)
		}
		for _, hname := range pt.Spec.Defaults.Harnesses {
			harness, hok := bundle.Harnesses[hname]
			if !hok {
				return nil, fmt.Errorf("%w: %q", errdefs.ErrTeamHarnessNotLoaded, hname)
			}

			merged := MergeNeeds(role.Spec.Needs.Image, ptRoleImageNeeds(ptRole))
			entry, selErr := SelectImage(bundle.ImageCatalog, hname, merged)
			if selErr != nil {
				return nil, fmt.Errorf(
					"render %s/%s: %w", ptRole.Ref, hname, selErr,
				)
			}

			bp, renderErr := RenderBlueprint(
				bundle.CacheDir, harness, role, hname, ptRole.Ref,
				merged, entry, project, realm, in.Build, in.SourceRef,
			)
			if renderErr != nil {
				return nil, fmt.Errorf(
					"render %s/%s: %w", ptRole.Ref, hname, renderErr,
				)
			}
			cfg := BindConfig(bp, role, ptRole.Ref, hname, tc, bundle.Source, in, project, realm)

			res.Blueprints = append(res.Blueprints, bp)
			res.Configs = append(res.Configs, cfg)
			if entry != nil {
				if _, seen := seenSelections[entry.Ref]; !seen {
					seenSelections[entry.Ref] = struct{}{}
					res.Selections = append(res.Selections, entry)
				}
			}
		}
	}
	return res, nil
}

// MergeNeeds returns the lexicographically sorted union of a and b. Empty
// and whitespace-only entries are dropped, so a per-role override that
// repeats a role.yaml capability yields a single entry rather than a
// duplicate. The output is a fresh slice — callers may mutate it without
// affecting the inputs.
func MergeNeeds(a, b []string) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, s := range a {
		if v := strings.TrimSpace(s); v != "" {
			seen[v] = struct{}{}
		}
	}
	for _, s := range b {
		if v := strings.TrimSpace(s); v != "" {
			seen[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// SelectImage picks the ImageCatalog entry whose `harness` matches and
// whose `capabilities` superset every entry in needs. Catalog order is
// the tiebreaker — the first matching entry wins. A miss surfaces
// errdefs.ErrTeamImageNoMatch naming the first capability no
// harness-matching image provides + the operator-actionable hint. Empty
// needs match any image carrying the requested harness; an empty catalog
// is always a miss (the operator must populate images.yaml).
func SelectImage(
	ic *model.ImageCatalog,
	harness string,
	needs []string,
) (*model.ImageCatalogEntry, error) {
	if ic == nil || len(ic.Spec.Images) == 0 {
		return nil, fmt.Errorf(
			"%w: empty ImageCatalog for harness=%q "+
				"(build or label an image in harnesses/images.yaml that provides %v)",
			errdefs.ErrTeamImageNoMatch, harness, needs,
		)
	}

	for i := range ic.Spec.Images {
		e := &ic.Spec.Images[i]
		if e.Harness != harness {
			continue
		}
		if hasAll(e.Capabilities, needs) {
			return e, nil
		}
	}

	unmet := firstUnmet(ic, harness, needs)
	if unmet == "" {
		// Every needed capability is provided by *some* harness-matching
		// image, but no single image carries all of them. Name the merged
		// set so the operator knows what to consolidate.
		return nil, fmt.Errorf(
			"%w: harness=%q needs=%v (no single image in images.yaml carries all "+
				"capabilities; build or label one that does)",
			errdefs.ErrTeamImageNoMatch, harness, needs,
		)
	}
	return nil, fmt.Errorf(
		"%w: harness=%q capability=%q (build or label an image in "+
			"harnesses/images.yaml that provides %q)",
		errdefs.ErrTeamImageNoMatch, harness, unmet, unmet,
	)
}

// RenderBlueprint loads the harness's blueprint template (resolved
// relative to cacheDir), substitutes `${KEY}` placeholders for the
// `(role × harness)` pair's facts, and yaml-parses into a
// CellBlueprintDoc. The blueprint's metadata.labels are populated with
// `kukeon.io/team = project`. metadata.realm is forced to realm (the
// template need not pre-fill it). If the template did not supply
// metadata.name, the default `<role>-<harness>` is stamped so the
// blueprint and its companion config share a deterministic identity.
//
// build + sourceRef drive the `${IMAGE}` bind decision (see blueprintVars):
// in build mode the locally-built `kukeon.internal/<ref>:<sourceRef>` image is
// bound; otherwise the catalog entry's published Image is.
func RenderBlueprint(
	cacheDir string,
	h *model.Harness,
	r *model.Role,
	harness, roleRef string,
	needs []string,
	image *model.ImageCatalogEntry,
	project, realm string,
	build bool,
	sourceRef string,
) (*v1beta1.CellBlueprintDoc, error) {
	if h == nil || strings.TrimSpace(h.Spec.Template) == "" {
		return nil, fmt.Errorf(
			"%w: harness=%q (spec.template is empty)",
			errdefs.ErrTeamBlueprintTemplateMissing, harness,
		)
	}
	tplPath := filepath.Join(cacheDir, h.Spec.Template)
	raw, err := os.ReadFile(tplPath)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %q: %w", errdefs.ErrTeamBlueprintTemplateMissing, tplPath, err,
		)
	}

	vars := blueprintVars(roleRef, harness, image, needs, r, build, sourceRef)
	rendered := substitute(string(raw), vars)

	var bp v1beta1.CellBlueprintDoc
	if unmarshalErr := yaml.Unmarshal([]byte(rendered), &bp); unmarshalErr != nil {
		return nil, fmt.Errorf("parse rendered blueprint %q: %w", tplPath, unmarshalErr)
	}

	bp.APIVersion = v1beta1.APIVersionV1Beta1
	bp.Kind = v1beta1.KindCellBlueprint
	if strings.TrimSpace(bp.Metadata.Name) == "" {
		bp.Metadata.Name = defaultObjectName(roleRef, harness)
	}
	bp.Metadata.Realm = realm
	if bp.Metadata.Labels == nil {
		bp.Metadata.Labels = map[string]string{}
	}
	bp.Metadata.Labels[v1beta1.LabelTeam] = project
	return &bp, nil
}

// BindConfig produces a CellConfig referencing bp. The operator-fact
// values (git author/committer/signingKey/allowedSigners/registry) land
// in Values keyed by the same `${KEY}` names the blueprint may declare as
// CellBlueprintParameters; the daemon resolves them at run time per the
// CellConfig contract. The project's clone URL fills the `project` repo
// slot; the agents source URL (from tc.spec.sources[<owner/repo>]) fills
// the `agents` slot — both only when the blueprint actually declares the
// slot, so a template that doesn't carry the slot produces a config
// without a stray fill. role.Spec.Needs.Secrets entries are matched
// against the blueprint's BlueprintSecretSlot declarations and filled
// from tc.spec.secrets — when the operator has a matching entry, an
// in-realm ContainerSecretRef points the slot at it (the runtime
// resolves the actual bytes via the Secret kind from #623).
func BindConfig(
	bp *v1beta1.CellBlueprintDoc,
	r *model.Role,
	roleRef, harness string,
	tc *model.TeamsConfig,
	src teamsource.Source,
	in Inputs,
	project, realm string,
) *v1beta1.CellConfigDoc {
	cfg := &v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  bp.Metadata.Name,
			Realm: realm,
			Space: bp.Metadata.Space,
			Stack: bp.Metadata.Stack,
			Labels: map[string]string{
				v1beta1.LabelTeam: project,
			},
		},
		Spec: v1beta1.CellConfigSpec{
			Blueprint: v1beta1.CellConfigBlueprintRef{
				Name:  bp.Metadata.Name,
				Realm: realm,
				Space: bp.Metadata.Space,
				Stack: bp.Metadata.Stack,
			},
			Values: operatorValues(tc, roleRef, harness, project),
		},
	}

	declaredRepos := collectRepoSlots(bp)
	declaredSecrets := collectSecretSlots(bp)

	if in.ProjectRepoURL != "" && declaredRepos[ProjectRepoSlotName] {
		if cfg.Spec.Repos == nil {
			cfg.Spec.Repos = map[string]v1beta1.CellConfigRepoFill{}
		}
		cfg.Spec.Repos[ProjectRepoSlotName] = v1beta1.CellConfigRepoFill{
			URL: in.ProjectRepoURL,
		}
	}
	if agentsURL := agentsCloneURL(tc, src); agentsURL != "" && declaredRepos[AgentsRepoSlotName] {
		if cfg.Spec.Repos == nil {
			cfg.Spec.Repos = map[string]v1beta1.CellConfigRepoFill{}
		}
		cfg.Spec.Repos[AgentsRepoSlotName] = v1beta1.CellConfigRepoFill{
			URL: agentsURL,
		}
	}

	if len(declaredSecrets) > 0 && r != nil && len(r.Spec.Needs.Secrets) > 0 && tc != nil {
		for _, secretName := range r.Spec.Needs.Secrets {
			if !declaredSecrets[secretName] {
				continue
			}
			if _, ok := tc.Spec.Secrets[secretName]; !ok {
				continue
			}
			if cfg.Spec.Secrets == nil {
				cfg.Spec.Secrets = map[string]v1beta1.CellConfigSecretFill{}
			}
			cfg.Spec.Secrets[secretName] = v1beta1.CellConfigSecretFill{
				SecretRef: &v1beta1.ContainerSecretRef{
					Name:  secretName,
					Realm: realm,
				},
			}
		}
	}

	return cfg
}

// MarshalYAML returns the Result as a single multi-document YAML stream —
// every blueprint followed by its companion config. The order matches the
// per-(role × harness) iteration order so the dry-run output is
// deterministic.
func MarshalYAML(res *Result) ([]byte, error) {
	if res == nil {
		return nil, nil
	}
	var buf strings.Builder
	first := true
	emit := func(doc any) error {
		raw, err := yaml.Marshal(doc)
		if err != nil {
			return err
		}
		if !first {
			buf.WriteString("---\n")
		}
		first = false
		buf.Write(raw)
		return nil
	}
	for i, bp := range res.Blueprints {
		if err := emit(bp); err != nil {
			return nil, fmt.Errorf("marshal blueprint %d: %w", i, err)
		}
		if i < len(res.Configs) {
			if err := emit(res.Configs[i]); err != nil {
				return nil, fmt.Errorf("marshal config %d: %w", i, err)
			}
		}
	}
	return []byte(buf.String()), nil
}

// ptRoleImageNeeds returns the project-side image-capability overrides for
// ptRole, or nil when the role declared no overrides. Encapsulates the
// pointer nilness so MergeNeeds receives a plain slice.
func ptRoleImageNeeds(ptRole model.ProjectTeamRole) []string {
	if ptRole.Needs == nil {
		return nil
	}
	return ptRole.Needs.Image
}

// hasAll reports whether haystack contains every entry of needles
// (string set semantics, order-insensitive).
func hasAll(haystack, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	set := make(map[string]struct{}, len(haystack))
	for _, h := range haystack {
		set[h] = struct{}{}
	}
	for _, n := range needles {
		if _, ok := set[n]; !ok {
			return false
		}
	}
	return true
}

// firstUnmet walks needs in iteration order and returns the first
// capability no harness-matching ImageCatalog entry provides. Returns the
// empty string when every needed capability is provided by some image but
// no single image carries all of them — the caller renders a different
// error message for that case.
func firstUnmet(ic *model.ImageCatalog, harness string, needs []string) string {
	for _, n := range needs {
		provided := false
		for i := range ic.Spec.Images {
			e := &ic.Spec.Images[i]
			if e.Harness != harness {
				continue
			}
			for _, c := range e.Capabilities {
				if c == n {
					provided = true
					break
				}
			}
			if provided {
				break
			}
		}
		if !provided {
			return n
		}
	}
	return ""
}

// substitutionPattern matches a `${KEY}` placeholder. KEY is the standard
// shell-style identifier: a leading letter or underscore followed by
// letters/digits/underscores. Unknown placeholders pass through verbatim
// rather than substituting empty — a CellBlueprint may legitimately carry
// $-prefixed literals (env-var defaults, shell snippets), and the
// downstream CellBlueprintParameter resolver surfaces a truly missing var
// at apply time with a clearer error than a silent empty.
var substitutionPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// substitute applies render-time `${KEY}` substitution. The function
// looks up keys in vars; an unknown key passes through the original
// `${KEY}` literal unchanged.
func substitute(in string, vars map[string]string) string {
	return substitutionPattern.ReplaceAllStringFunc(in, func(match string) string {
		key := match[2 : len(match)-1]
		if v, ok := vars[key]; ok {
			return v
		}
		return match
	})
}

// blueprintVars builds the `${KEY}` substitution dict the harness's
// blueprint template is rendered against. The role's native per-harness
// config is wired verbatim — a role that has no per-harness config for
// `harness` still gets the per-harness keys, just bound to empty strings,
// so a template that always references `${SETTINGS}` does not break for
// roles that omit it.
//
// The `${IMAGE}` bind is mode-dependent: in `--build` mode (build && a
// non-empty sourceRef) it is the locally-built `kukeon.internal/<ref>:
// <sourceRef>` ref — byte-identical to the tag teambuild produces, so the
// runtime resolves the in-realm image without a network pull — otherwise it
// is the catalog entry's published, registry-qualified `Image`. `${IMAGE_REF}`
// always carries the catalog-local selector key (`image.Ref`) regardless of
// mode; only the bound image reference flips.
func blueprintVars(
	roleRef, harness string,
	image *model.ImageCatalogEntry,
	needs []string,
	r *model.Role,
	build bool,
	sourceRef string,
) map[string]string {
	vars := map[string]string{
		"ROLE":    roleRef,
		"HARNESS": harness,
		"NEEDS":   strings.Join(needs, ","),
	}
	if image != nil {
		img := image.Image
		if build && strings.TrimSpace(sourceRef) != "" {
			img = consts.InternalImageRef(image.Ref, sourceRef)
		}
		vars["IMAGE"] = img
		vars["IMAGE_REF"] = image.Ref
	}
	if r != nil {
		if rh, ok := r.Spec.Harnesses[harness]; ok {
			vars["SETTINGS"] = rh.Settings
			vars["SANDBOX"] = rh.Sandbox
			vars["APPROVAL"] = rh.Approval
			vars["PERMISSIONS"] = rh.Permissions
		} else {
			vars["SETTINGS"] = ""
			vars["SANDBOX"] = ""
			vars["APPROVAL"] = ""
			vars["PERMISSIONS"] = ""
		}
	}
	return vars
}

// operatorValues builds the CellConfig.Values scalar map from operator
// facts. The keys mirror the GIT_AUTHOR_NAME / GIT_AUTHOR_EMAIL / etc.
// env-var protocol so a blueprint that declares those names as
// CellBlueprintParameters resolves them at apply time without any
// per-blueprint translation. Empty operator facts are omitted (the
// blueprint's parameter default, if any, wins).
func operatorValues(tc *model.TeamsConfig, roleRef, harness, project string) map[string]string {
	vals := map[string]string{
		"ROLE":    roleRef,
		"HARNESS": harness,
		"PROJECT": project,
	}
	if tc == nil {
		return vals
	}
	if g := tc.Spec.Git; g != nil {
		if g.Author != nil {
			if g.Author.Name != "" {
				vals["GIT_AUTHOR_NAME"] = g.Author.Name
			}
			if g.Author.Email != "" {
				vals["GIT_AUTHOR_EMAIL"] = g.Author.Email
			}
		}
		if g.Committer != nil {
			if g.Committer.Name != "" {
				vals["GIT_COMMITTER_NAME"] = g.Committer.Name
			}
			if g.Committer.Email != "" {
				vals["GIT_COMMITTER_EMAIL"] = g.Committer.Email
			}
		}
		if g.SigningKey != "" {
			vals["GIT_SIGNING_KEY"] = g.SigningKey
		}
		if g.AllowedSigners != "" {
			vals["GIT_ALLOWED_SIGNERS"] = g.AllowedSigners
		}
		if g.SSHKey != "" {
			vals["GIT_SSH_KEY"] = g.SSHKey
		}
	}
	if tc.Spec.Registry != "" {
		vals["REGISTRY"] = tc.Spec.Registry
	}
	return vals
}

// agentsCloneURL returns the clone URL of the materialized agents source —
// the SSH default expanded from src.Host/src.OwnerRepo, or a tc.spec.sources
// transport override when one is present. It reuses teamsource.CloneURL so the
// agents-side slot fill stays consistent with the bundle that produced it. A
// zero Source (no resolved agents repo) yields the empty string so an
// undeclared slot is never filled with a garbage URL.
func agentsCloneURL(tc *model.TeamsConfig, src teamsource.Source) string {
	if src.OwnerRepo == "" || src.Host == "" {
		return ""
	}
	return teamsource.CloneURL(tc, src)
}

// collectRepoSlots returns the set of repo slot names declared by bp's
// containers — a repo entry with an empty URL is a structural slot a
// CellConfig fills (per BlueprintContainer.Repos contract). Inline-URL
// repos are not slots and never appear here.
func collectRepoSlots(bp *v1beta1.CellBlueprintDoc) map[string]bool {
	out := map[string]bool{}
	if bp == nil {
		return out
	}
	for _, c := range bp.Spec.Cell.Containers {
		for _, repo := range c.Repos {
			if strings.TrimSpace(repo.URL) == "" {
				out[repo.Name] = true
			}
		}
	}
	return out
}

// collectSecretSlots returns the set of BlueprintSecretSlot names
// declared by bp's containers.
func collectSecretSlots(bp *v1beta1.CellBlueprintDoc) map[string]bool {
	out := map[string]bool{}
	if bp == nil {
		return out
	}
	for _, c := range bp.Spec.Cell.Containers {
		for _, s := range c.Secrets {
			out[s.Name] = true
		}
	}
	return out
}

// defaultObjectName is the CellBlueprint/CellConfig name used when the
// template did not set metadata.name. Kept simple and predictable so the
// blueprint and its companion config share an identity in `kuke get`
// output.
func defaultObjectName(roleRef, harness string) string {
	return roleRef + "-" + harness
}
