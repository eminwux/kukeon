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
//     harness's own directory under the materialized cache, see #1110),
//     parse it as a Go text/template against a typed dot-context
//     (`.role`, `.harness`, `.needs`, `.harnesses`, `.operator`,
//     `.project`, `.image`, `.realm`/`.space`/`.stack`), pull in every
//     sibling `*.tmpl.yaml` partial in the same dir so `{{ template
//     "name" . }}` calls resolve against them, execute, and yaml-parse
//     the result into a CellBlueprintDoc. Per the umbrella's key decision
//     the role's native per-harness config is wired verbatim — no
//     transpile.
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
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

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

// partialGlob is the pattern teamrender scans the blueprint template's
// directory with to discover sibling partials. Every match (other than the
// main template itself) is parsed into the same template set so a
// `{{ template "name" . }}` call in the blueprint resolves against a
// `{{ define "name" }}…{{ end }}` block in any sibling.
const partialGlob = "*.tmpl.yaml"

// Inputs collects the per-project facts that vary by `kuke team init`
// invocation. Project is the rendered objects' team label and the basis
// for the blueprint/config name when the template did not supply one.
// ProjectRepoURL is the clone URL for the project repo — typically read
// from `git -C <projectDir> remote get-url origin` by the caller.
type Inputs struct {
	Project        string
	ProjectRepoURL string
	// ProjectDir is the on-host directory the project's kuketeam.yaml was read
	// from (composeTeam's os.Getwd()). Exposed to blueprint templates as
	// `.project.PROJECT_DIR` so the operator can reference the source-tree
	// path without bouncing it through a CellConfig parameter.
	ProjectDir string
	// TeamDir is the per-team host-state root resolved by composeTeam
	// (TeamEntry.spec.teamDir override, else Layout.TeamDir(team)). Exposed to
	// blueprint templates as `.operator.TEAM_ROOT`. Per-team-scoped: two teams
	// running on the same operator host see two different TEAM_ROOT values.
	TeamDir string
	Realm   string
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
//
// Secrets is the merged shared+per-team secret set composed by the
// teamsecrets package (#1113) and is included in the apply payload by
// MarshalYAML before Blueprints and Configs — the apply bundle order is
// Secrets → Blueprints → Configs so a Blueprint that references a Secret
// via ContainerSecret.secretRef sees a daemon-side record present at
// reconcile time.
type Result struct {
	Secrets    []*v1beta1.SecretDoc
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
				merged, entry, tc, bundle.Source, in, project, realm,
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

// RenderBlueprint loads the harness's blueprint template, parses it as a
// Go text/template (alongside every sibling `*.tmpl.yaml` partial in the
// same directory), executes it against the typed dot-context the agents
// repo's published blueprints are authored against, and yaml-parses the
// result into a CellBlueprintDoc.
//
// Template path resolution: harness.Spec.Template resolves relative to the
// harness's own directory (`<cacheDir>/harnesses/<harness>/`), not the
// cache root. The agents repo's `harnesses/<name>/harness.yaml` declares
// `template: blueprint.tmpl.yaml` as a sibling, so the bare-filename form
// is the canonical shape.
//
// Sibling partials: every `*.tmpl.yaml` in the resolved template's
// directory is parsed into the same `*template.Template` set so a
// blueprint's `{{ template "mount_source" . }}` call resolves against a
// `{{ define "mount_source" }}…{{ end }}` block in any sibling
// (`partials.tmpl.yaml`, `mounts.tmpl.yaml`, …) without the renderer
// owning a partials directory of its own.
//
// Dot-context: see renderContextValues for the full shape. The
// `.operator.*` and `.project.*` leaves are bound from tc, src, and in:
// the agents source's owner/repo path drives `.operator.REPO_OWNER` (when
// no tc.spec.repoOwner override is set) and `.project.AGENTS_REPO`;
// in.ProjectDir and in.TeamDir surface as `.project.PROJECT_DIR` and
// `.operator.TEAM_ROOT` respectively; tc.spec.homeDir (or `$HOME` when
// unset) fills `.operator.HOME_DIR`. The metadata.labels are populated
// with `kukeon.io/team = project`. metadata.realm is forced to realm (the
// template need not pre-fill it). If the template did not supply
// metadata.name, the default `<role>-<harness>` is stamped so the
// blueprint and its companion config share a deterministic identity.
//
// in.Build + in.SourceRef drive the `.image` bind decision (see
// renderContextValues): in build mode the locally-built
// `kukeon.internal/<ref>:<sourceRef>` image is bound; otherwise the
// catalog entry's published Image is.
func RenderBlueprint(
	cacheDir string,
	h *model.Harness,
	r *model.Role,
	harness, roleRef string,
	needs []string,
	image *model.ImageCatalogEntry,
	tc *model.TeamsConfig,
	src teamsource.Source,
	in Inputs,
	project, realm string,
) (*v1beta1.CellBlueprintDoc, error) {
	if h == nil || strings.TrimSpace(h.Spec.Template) == "" {
		return nil, fmt.Errorf(
			"%w: harness=%q (spec.template is empty)",
			errdefs.ErrTeamBlueprintTemplateMissing, harness,
		)
	}
	harnessDir := teamsource.HarnessDir(cacheDir, harness)
	tplPath := filepath.Join(harnessDir, h.Spec.Template)
	raw, err := os.ReadFile(tplPath)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: %q: %w", errdefs.ErrTeamBlueprintTemplateMissing, tplPath, err,
		)
	}

	tpl, err := parseBlueprintTemplate(tplPath, raw)
	if err != nil {
		return nil, err
	}

	ctx := renderContextValues(roleRef, harness, image, needs, r, tc, src, in, project, realm)
	var buf bytes.Buffer
	if execErr := tpl.ExecuteTemplate(&buf, filepath.Base(tplPath), ctx); execErr != nil {
		return nil, fmt.Errorf("execute blueprint template %q: %w", tplPath, execErr)
	}

	var bp v1beta1.CellBlueprintDoc
	if unmarshalErr := yaml.Unmarshal(buf.Bytes(), &bp); unmarshalErr != nil {
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

// MarshalYAML returns the Result as a single multi-document YAML stream.
// Bundle order: every Secret, then every Blueprint, then every Config —
// the "Secrets → Blueprints → Configs" ordering called out in #1113. The
// per-section order matches the slice order on Result (Secrets sorted by
// metadata.name by teamsecrets.Render; Blueprints/Configs in
// (role × harness) iteration order) so the dry-run output and the
// apply-bundle payload are both deterministic.
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
	for i, s := range res.Secrets {
		if err := emit(s); err != nil {
			return nil, fmt.Errorf("marshal secret %d: %w", i, err)
		}
	}
	for i, bp := range res.Blueprints {
		if err := emit(bp); err != nil {
			return nil, fmt.Errorf("marshal blueprint %d: %w", i, err)
		}
	}
	for i, cfg := range res.Configs {
		if err := emit(cfg); err != nil {
			return nil, fmt.Errorf("marshal config %d: %w", i, err)
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

// templateFuncs is the function map exposed to harness blueprint
// templates. Kept small — a sprig subset (upper, replace) — to keep the
// renderer's surface narrow per the umbrella's "no full sprig" decision.
// `replace` mirrors sprig's `(old, new, src)` arg order so the pipe idiom
// `{{ . | upper | replace "-" "_" }}` flows the chained value into src as
// the last positional, matching what published blueprints expect.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"upper":   strings.ToUpper,
		"replace": func(from, to, src string) string { return strings.ReplaceAll(src, from, to) },
	}
}

// parseBlueprintTemplate parses the blueprint at tplPath (already-read raw
// body) as a Go text/template and pulls in every sibling *.tmpl.yaml in
// the same directory as additional members of the template set. The
// blueprint can then call `{{ template "name" . }}` against a
// `{{ define "name" }}…{{ end }}` block defined in any sibling without
// the renderer owning a partials directory layout of its own.
func parseBlueprintTemplate(tplPath string, raw []byte) (*template.Template, error) {
	return parseBlueprintTemplateBody(tplPath, raw, nil)
}

// parseBlueprintTemplateBody is parseBlueprintTemplate with an optional
// pre-parse transform applied to the main body and to every sibling partial
// before each is handed to text/template. A nil transform is identity — the
// render path (RenderBlueprint) uses it so the generated YAML keeps its
// documentation comments verbatim. The validate path passes stripYAMLComments
// so a `{{ ... }}` action a template *documents* inside a YAML `#` comment is
// dropped before the parser turns it into a live FieldNode / TemplateNode that
// validateFacts / validatePartials would flag as a spurious gap (#1123).
func parseBlueprintTemplateBody(
	tplPath string, raw []byte, transform func([]byte) []byte,
) (*template.Template, error) {
	if transform == nil {
		transform = func(b []byte) []byte { return b }
	}
	mainName := filepath.Base(tplPath)
	tpl := template.New(mainName).Funcs(templateFuncs())
	if _, err := tpl.Parse(string(transform(raw))); err != nil {
		return nil, fmt.Errorf("parse blueprint template %q: %w", tplPath, err)
	}

	siblings, err := filepath.Glob(filepath.Join(filepath.Dir(tplPath), partialGlob))
	if err != nil {
		return nil, fmt.Errorf("scan partials next to %q: %w", tplPath, err)
	}
	// Deterministic order so a multi-partial set parses the same way every
	// run — filepath.Glob already returns lexicographically sorted matches,
	// but pinning the contract here makes the intent explicit for future
	// readers.
	sort.Strings(siblings)
	for _, sib := range siblings {
		if sib == tplPath {
			continue
		}
		sibRaw, readErr := os.ReadFile(sib)
		if readErr != nil {
			return nil, fmt.Errorf("read partial %q: %w", sib, readErr)
		}
		if _, parseErr := tpl.New(filepath.Base(sib)).Parse(string(transform(sibRaw))); parseErr != nil {
			return nil, fmt.Errorf("parse partial %q: %w", sib, parseErr)
		}
	}
	return tpl, nil
}

// renderContextValues builds the dot-context the harness blueprint template
// is rendered against. The top-level shape (lowercase keys) matches the
// `.role`, `.harness`, `.needs`, `.harnesses`, `.operator`, `.project`,
// `.image`, `.image_ref`, `.realm`, `.space`, `.stack` contract the
// agents-side blueprints reference. Inner leaves keep their authored case:
// `.needs.repos` etc. stay lowercase (role.yaml field names), while
// `.operator.GIT_USER_NAME` etc. stay uppercase (env-var protocol).
//
// All leaves are exposed as maps rather than typed structs because Go
// text/template's struct-field lookup is case-sensitive against exported
// (uppercase) field names, which would force every blueprint to write
// `.Needs.Repos` instead of the lowercase `.needs.repos` the agents repo
// templates are authored with.
//
// The `.image` bind is mode-dependent: in `--build` mode (in.Build && a
// non-empty in.SourceRef) it is the locally-built
// `kukeon.internal/<ref>:<sourceRef>` ref — byte-identical to the tag
// teambuild produces, so the runtime resolves the in-realm image without a
// network pull — otherwise it is the catalog entry's published,
// registry-qualified Image. `.image_ref` always carries the catalog-local
// selector key (`image.Ref`) regardless of mode.
//
// The `.operator.*` and `.project.*` leaves are the full fact contract the
// published blueprints reference. Operator-side: GIT_USER_NAME /
// GIT_USER_EMAIL / REGISTRY come from tc; TEAM_ROOT is per-team
// (in.TeamDir, resolved from TeamEntry.spec.teamDir or the Layout default
// for this render call); HOME_DIR is tc.spec.homeDir or `$HOME` when
// unset; REPO_OWNER is tc.spec.repoOwner or the owner segment of the
// agents source's `<owner>/<repo>` when unset. Project-side: NAME is the
// team label; PROJECT_DIR is in.ProjectDir (composeTeam's os.Getwd());
// AGENTS_REPO is the agents source's `<owner>/<repo>` path. A blueprint
// referencing an unbound key gets an empty string at render time (Go
// template's default for a missing map key), not an error.
func renderContextValues(
	roleRef, harness string,
	image *model.ImageCatalogEntry,
	needs []string,
	r *model.Role,
	tc *model.TeamsConfig,
	src teamsource.Source,
	in Inputs,
	project, realm string,
) map[string]any {
	img := ""
	imgRef := ""
	if image != nil {
		img = image.Image
		if in.Build && strings.TrimSpace(in.SourceRef) != "" {
			img = consts.InternalImageRef(image.Ref, in.SourceRef)
		}
		imgRef = image.Ref
	}

	// .needs.image is the merged role+project capability set the renderer
	// just used to pick an image; .needs.{repos,mounts,params,secrets} are
	// the role.yaml-authored selector lists the agents-side blueprints
	// iterate to wire repo and mount slots.
	needsView := map[string]any{
		"image":   needs,
		"repos":   roleNeedsRepos(r),
		"mounts":  roleNeedsMounts(r),
		"params":  roleNeedsParams(r),
		"secrets": roleNeedsSecrets(r),
	}

	// .harnesses.<h>.{settings,sandbox,approval,permissions} mirrors
	// role.yaml's harnesses map verbatim — a blueprint may switch behaviour
	// per harness (claude reads Settings; codex reads Sandbox/Approval;
	// opencode reads Permissions) without the renderer transpiling.
	harnessesView := map[string]map[string]string{}
	if r != nil {
		for name, rh := range r.Spec.Harnesses {
			harnessesView[name] = map[string]string{
				"settings":    rh.Settings,
				"sandbox":     rh.Sandbox,
				"approval":    rh.Approval,
				"permissions": rh.Permissions,
			}
		}
	}

	operatorView := map[string]string{}
	if tc != nil {
		if v := strings.TrimSpace(tc.Spec.Registry); v != "" {
			operatorView["REGISTRY"] = v
		}
		if tc.Spec.Git != nil && tc.Spec.Git.Author != nil {
			if v := strings.TrimSpace(tc.Spec.Git.Author.Name); v != "" {
				operatorView["GIT_USER_NAME"] = v
			}
			if v := strings.TrimSpace(tc.Spec.Git.Author.Email); v != "" {
				operatorView["GIT_USER_EMAIL"] = v
			}
		}
	}
	if v := strings.TrimSpace(in.TeamDir); v != "" {
		operatorView["TEAM_ROOT"] = v
	}
	if v := resolveHomeDir(tc); v != "" {
		operatorView["HOME_DIR"] = v
	}
	if v := resolveRepoOwner(tc, src); v != "" {
		operatorView["REPO_OWNER"] = v
	}

	projectView := map[string]string{
		"NAME": project,
	}
	if v := strings.TrimSpace(in.ProjectDir); v != "" {
		projectView["PROJECT_DIR"] = v
	}
	if v := strings.TrimSpace(src.OwnerRepo); v != "" {
		projectView["AGENTS_REPO"] = v
	}

	roleView := map[string]any{
		"name": roleRef,
		"ref":  roleRef,
	}

	return map[string]any{
		"role":      roleView,
		"harness":   harness,
		"needs":     needsView,
		"harnesses": harnessesView,
		"operator":  operatorView,
		"project":   projectView,
		"image":     img,
		"image_ref": imgRef,
		"realm":     realm,
		"space":     "",
		"stack":     "",
	}
}

// resolveHomeDir returns the `.operator.HOME_DIR` fact: tc.spec.homeDir
// when the operator set it, else the process's `$HOME` env var. Empty when
// both are unset (a blueprint that references HOME_DIR then renders the
// empty string — Go template's default for a missing map key).
func resolveHomeDir(tc *model.TeamsConfig) string {
	if tc != nil {
		if v := strings.TrimSpace(tc.Spec.HomeDir); v != "" {
			return v
		}
	}
	return strings.TrimSpace(os.Getenv("HOME"))
}

// resolveRepoOwner returns the `.operator.REPO_OWNER` fact: tc.spec.repoOwner
// when the operator set it, else the owner segment of the agents source's
// `<owner>/<repo>` path (the segment before the first `/`). The common
// single-owner case (operator owns both the agents source and their
// projects) needs no override.
func resolveRepoOwner(tc *model.TeamsConfig, src teamsource.Source) string {
	if tc != nil {
		if v := strings.TrimSpace(tc.Spec.RepoOwner); v != "" {
			return v
		}
	}
	if owner, _, ok := strings.Cut(strings.TrimSpace(src.OwnerRepo), "/"); ok {
		return owner
	}
	return ""
}

// roleNeedsRepos, roleNeedsMounts, roleNeedsParams, roleNeedsSecrets
// return the matching role.yaml needs slice, or an empty slice (never
// nil) so a `{{ range .needs.<x> }}` over an absent slot iterates zero
// times rather than tripping a nil-deref in the template engine.
func roleNeedsRepos(r *model.Role) []string {
	if r == nil {
		return []string{}
	}
	return appendNonNil(r.Spec.Needs.Repos)
}

func roleNeedsMounts(r *model.Role) []string {
	if r == nil {
		return []string{}
	}
	return appendNonNil(r.Spec.Needs.Mounts)
}

func roleNeedsParams(r *model.Role) []string {
	if r == nil {
		return []string{}
	}
	return appendNonNil(r.Spec.Needs.Params)
}

func roleNeedsSecrets(r *model.Role) []string {
	if r == nil {
		return []string{}
	}
	return appendNonNil(r.Spec.Needs.Secrets)
}

func appendNonNil(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
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
