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

package teamrender

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"

	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Section titles for the validate report. Exported so consumers (and tests)
// can key off the canonical names rather than string literals.
const (
	SectionCatalog   = "catalog"
	SectionTemplates = "templates"
	SectionPartials  = "partials"
	SectionFacts     = "facts"
)

// ReportLine is one outcome row in a validate section. OK distinguishes a
// satisfied check from a gap; Detail carries the human-readable body the
// report writer prints after the `ok`/`miss` status column.
type ReportLine struct {
	OK     bool
	Detail string
}

// ReportSection groups the outcome lines for one of the four contract
// checks. Title is one of the Section* constants.
type ReportSection struct {
	Title string
	Lines []ReportLine
}

// ValidationReport is the full one-pass contract check produced by Validate:
// four sections (catalog, templates, partials, facts), each carrying its
// ok/miss lines in a stable order. Write renders it; HasMiss / MissCount
// drive the caller's exit code.
type ValidationReport struct {
	Sections []ReportSection
}

// MissCount returns the total number of miss lines across every section.
func (r *ValidationReport) MissCount() int {
	n := 0
	for _, s := range r.Sections {
		for _, l := range s.Lines {
			if !l.OK {
				n++
			}
		}
	}
	return n
}

// HasMiss reports whether any section carries at least one miss line.
func (r *ValidationReport) HasMiss() bool { return r.MissCount() > 0 }

// Write renders the report to w as the multi-section gap report. Each section
// is headed by `== <title> ==` and each line by a fixed-width `ok`/`miss`
// status column, so the catalog/templates ok lines and the partials/facts
// miss lines line up regardless of body width. Sections are separated by a
// blank line; all four section headers are always emitted (a clean section is
// just its header) so the operator can tell a passed check from a skipped one.
func (r *ValidationReport) Write(w io.Writer) error {
	for i, s := range r.Sections {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "== %s ==\n", s.Title); err != nil {
			return err
		}
		for _, l := range s.Lines {
			status := "miss"
			if l.OK {
				status = "ok"
			}
			if _, err := fmt.Fprintf(w, "%-4s %s\n", status, l.Detail); err != nil {
				return err
			}
		}
	}
	return nil
}

// Validate runs the one-pass contract check across a resolved Bundle and a
// project roster. Unlike Render — which fails fast on the first catalog miss
// or template error — Validate visits every (role × harness) pair and every
// selected harness, collecting all gaps into a single report so an operator
// fixes the whole drift in one pass rather than one error per re-run.
//
// The four sections mirror the render pipeline's failure surfaces:
//
//  1. catalog   — for every (role × harness), does SelectImage find a match?
//     ok lines name the selected catalog ref; miss lines name the first
//     unmet capability (or the no-single-image case).
//  2. templates — for every selected harness, does harness.Spec.Template
//     resolve under the materialized cache (relative to the harness dir, per
//     #1110)?
//  3. partials  — parse every harness's blueprint template set; for every
//     `{{ template "name" }}` invocation, does a matching `{{ define }}`
//     exist in the harness's partial set?
//  4. facts     — for every `{{ .operator.X }}` / `{{ .project.X }}`
//     reference in any template body, does the render dot-context expose the
//     key? (Checked against the known fact schema, not the populated subset —
//     an unset-but-known fact renders empty, not a gap.)
//
// A nil or empty bundle (the harness-less roster) yields four empty sections
// and no misses — there is nothing to validate. Validate writes nothing to
// disk beyond reading the already-materialized template files, and never
// returns an error for a gap: gaps live in the report (HasMiss / MissCount).
//
// The fact check is value-independent — it compares referenced keys against
// the dot-context's key *schema*, not an operator's populated subset — so
// Validate needs neither the TeamsConfig nor the per-run Inputs that Render
// consumes.
func Validate(
	bundle *teamsource.Bundle,
	pt *model.ProjectTeam,
) *ValidationReport {
	roles := map[string]*model.Role{}
	harnessesByName := map[string]*model.Harness{}
	var ic *model.ImageCatalog
	cacheDir := ""
	if bundle != nil {
		roles = bundle.Roles
		harnessesByName = bundle.Harnesses
		ic = bundle.ImageCatalog
		cacheDir = bundle.CacheDir
	}

	harnessNames := rosterHarnessNames(pt)

	return &ValidationReport{
		Sections: []ReportSection{
			validateCatalog(pt, roles, ic),
			validateTemplates(cacheDir, harnessNames, harnessesByName),
			validatePartials(cacheDir, harnessNames, harnessesByName),
			validateFacts(cacheDir, harnessNames, harnessesByName),
		},
	}
}

// rosterHarnessNames returns the deduplicated, lexicographically sorted set
// of harness names the roster's defaults declare. Sorting pins a stable
// section order independent of the (slice) declaration order.
func rosterHarnessNames(pt *model.ProjectTeam) []string {
	if pt == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(pt.Spec.Defaults.Harnesses))
	for _, h := range pt.Spec.Defaults.Harnesses {
		name := strings.TrimSpace(h)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// validateCatalog walks every (role × harness) pair and records whether
// SelectImage finds a match. Lines are sorted by their `role × harness`
// body so two runs against the same roster emit byte-identical output.
func validateCatalog(
	pt *model.ProjectTeam,
	roles map[string]*model.Role,
	ic *model.ImageCatalog,
) ReportSection {
	sec := ReportSection{Title: SectionCatalog}
	if pt == nil {
		return sec
	}
	harnessNames := rosterHarnessNames(pt)
	for _, ptRole := range pt.Spec.Roles {
		role := roles[ptRole.Ref]
		var roleImageNeeds []string
		if role != nil {
			roleImageNeeds = role.Spec.Needs.Image
		}
		merged := MergeNeeds(roleImageNeeds, ptRoleImageNeeds(ptRole))
		for _, hname := range harnessNames {
			pair := fmt.Sprintf("%s × %s", ptRole.Ref, hname)
			entry, err := SelectImage(ic, hname, merged)
			if err == nil {
				sec.Lines = append(sec.Lines, ReportLine{
					OK:     true,
					Detail: fmt.Sprintf("%s → %s", pair, entry.Ref),
				})
				continue
			}
			sec.Lines = append(sec.Lines, ReportLine{
				OK:     false,
				Detail: fmt.Sprintf("%s → %s", pair, catalogMissReason(ic, hname, merged)),
			})
		}
	}
	sortLines(sec.Lines)
	return sec
}

// catalogMissReason produces the concise reason a (harness × needs) pick
// failed: the empty-catalog case, the first capability no harness-matching
// image provides, or the no-single-image-carries-all case. It mirrors
// SelectImage's branching without the operator-actionable build/label hint
// (the report header already frames the section as actionable gaps).
func catalogMissReason(ic *model.ImageCatalog, harness string, needs []string) string {
	if ic == nil || len(ic.Spec.Images) == 0 {
		return fmt.Sprintf("no image in images.yaml for harness %q", harness)
	}
	if unmet := firstUnmet(ic, harness, needs); unmet != "" {
		return fmt.Sprintf("capability %q not provided by any %s image", unmet, harness)
	}
	return fmt.Sprintf("no single %s image carries all of %v", harness, needs)
}

// validateTemplates checks that every selected harness's spec.template
// resolves to a file under the materialized cache (relative to the harness
// dir, per #1110). harnessNames is already sorted, so the lines are stable.
func validateTemplates(
	cacheDir string,
	harnessNames []string,
	harnesses map[string]*model.Harness,
) ReportSection {
	sec := ReportSection{Title: SectionTemplates}
	for _, hname := range harnessNames {
		h := harnesses[hname]
		tmpl := ""
		if h != nil {
			tmpl = strings.TrimSpace(h.Spec.Template)
		}
		if tmpl == "" {
			sec.Lines = append(sec.Lines, ReportLine{
				OK:     false,
				Detail: fmt.Sprintf("harnesses/%s — spec.template is empty", hname),
			})
			continue
		}
		rel := templateDisplayPath(hname, tmpl)
		full := tplFullPath(cacheDir, hname, tmpl)
		if _, err := os.Stat(full); err != nil {
			sec.Lines = append(sec.Lines, ReportLine{
				OK:     false,
				Detail: fmt.Sprintf("%s — template: %s not found", rel, tmpl),
			})
			continue
		}
		sec.Lines = append(sec.Lines, ReportLine{OK: true, Detail: rel})
	}
	return sec
}

// validatePartials parses each selected harness's template set (main
// blueprint + sibling *.tmpl.yaml partials) and verifies every
// `{{ template "name" }}` invocation resolves to a `{{ define "name" }}` in
// the set. A template that fails to read or parse is itself a miss. Only
// miss lines are emitted — a resolved invocation is unremarkable and would
// flood the report — so a clean section is just its header.
func validatePartials(
	cacheDir string,
	harnessNames []string,
	harnesses map[string]*model.Harness,
) ReportSection {
	sec := ReportSection{Title: SectionPartials}
	for _, hname := range harnessNames {
		h := harnesses[hname]
		if h == nil || strings.TrimSpace(h.Spec.Template) == "" {
			// Missing/empty template is reported by validateTemplates; nothing
			// to parse here.
			continue
		}
		tmpl := strings.TrimSpace(h.Spec.Template)
		full := tplFullPath(cacheDir, hname, tmpl)
		raw, err := os.ReadFile(full)
		if err != nil {
			// Unresolved path is reported by validateTemplates; skip.
			continue
		}
		tpl, parseErr := parseBlueprintTemplate(full, raw)
		if parseErr != nil {
			sec.Lines = append(sec.Lines, ReportLine{
				OK:     false,
				Detail: fmt.Sprintf("%s — parse error: %v", templateDisplayPath(hname, tmpl), parseErr),
			})
			continue
		}

		defined := definedTemplateNames(tpl)
		type ref struct{ name, loc string }
		seen := map[ref]struct{}{}
		var misses []ReportLine
		for _, t := range tpl.Templates() {
			if t.Tree == nil || t.Tree.Root == nil {
				continue
			}
			loc := templateDisplayPath(hname, t.Name())
			walkNodes(t.Tree.Root, func(n parse.Node) {
				tn, ok := n.(*parse.TemplateNode)
				if !ok {
					return
				}
				if _, isDefined := defined[tn.Name]; isDefined {
					return
				}
				key := ref{name: tn.Name, loc: loc}
				if _, dup := seen[key]; dup {
					return
				}
				seen[key] = struct{}{}
				misses = append(misses, ReportLine{
					OK: false,
					Detail: fmt.Sprintf(
						"%s uses template %q, no {{ define }} found", loc, tn.Name,
					),
				})
			})
		}
		sortLines(misses)
		sec.Lines = append(sec.Lines, misses...)
	}
	return sec
}

// validateFacts parses each selected harness's template set and verifies
// every `{{ .operator.X }}` / `{{ .project.X }}` reference names a key the
// render dot-context exposes. The known key sets are derived from
// renderContextValues itself (see knownFactKeys) so the validator can never
// drift from the renderer. Only miss lines are emitted, deduplicated by
// (root, key, location) and stable-sorted.
func validateFacts(
	cacheDir string,
	harnessNames []string,
	harnesses map[string]*model.Harness,
) ReportSection {
	sec := ReportSection{Title: SectionFacts}
	knownOperator, knownProject := knownFactKeys()

	for _, hname := range harnessNames {
		h := harnesses[hname]
		if h == nil || strings.TrimSpace(h.Spec.Template) == "" {
			continue
		}
		tmpl := strings.TrimSpace(h.Spec.Template)
		full := tplFullPath(cacheDir, hname, tmpl)
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		tpl, parseErr := parseBlueprintTemplate(full, raw)
		if parseErr != nil {
			// Parse error already surfaces in the partials section.
			continue
		}

		type ref struct{ root, key, loc string }
		seen := map[ref]struct{}{}
		var misses []ReportLine
		for _, t := range tpl.Templates() {
			if t.Tree == nil || t.Tree.Root == nil {
				continue
			}
			loc := templateDisplayPath(hname, t.Name())
			walkNodes(t.Tree.Root, func(n parse.Node) {
				fn, ok := n.(*parse.FieldNode)
				if !ok || len(fn.Ident) < 2 {
					return
				}
				root := fn.Ident[0]
				key := fn.Ident[1]
				var known map[string]struct{}
				switch root {
				case "operator":
					known = knownOperator
				case "project":
					known = knownProject
				default:
					return
				}
				if _, isKnown := known[key]; isKnown {
					return
				}
				rk := ref{root: root, key: key, loc: loc}
				if _, dup := seen[rk]; dup {
					return
				}
				seen[rk] = struct{}{}
				misses = append(misses, ReportLine{
					OK: false,
					Detail: fmt.Sprintf(
						".%s.%s referenced in %s, not bound", root, key, loc,
					),
				})
			})
		}
		sortLines(misses)
		sec.Lines = append(sec.Lines, misses...)
	}
	return sec
}

// knownFactKeys returns the set of `.operator.*` and `.project.*` keys the
// render dot-context can expose. It is derived by running renderContextValues
// against a fully-saturated set of inputs (every conditionally-populated key
// made non-empty so the renderer emits all of them) and reading the resulting
// map keys — so the validator's known set is exactly what the renderer
// produces, with no second hand-maintained list to drift. The keys depend
// only on which inputs are present, not their values, so synthetic
// placeholders suffice.
func knownFactKeys() (map[string]struct{}, map[string]struct{}) {
	satTC := &model.TeamsConfig{
		Spec: model.TeamsConfigSpec{
			Registry:  "_",
			HomeDir:   "_",
			RepoOwner: "_",
		},
	}
	// Author is a promoted field from the embedded ContainerGit, so it is set
	// by assignment rather than in the composite literal above.
	satTC.Spec.Git = &model.TeamsConfigGit{}
	satTC.Spec.Git.Author = &v1beta1.GitIdentity{Name: "_", Email: "_"}

	satSrc := teamsource.Source{Host: "_", OwnerRepo: "_/_"}
	satIn := Inputs{ProjectDir: "_", TeamDir: "_"}

	ctx := renderContextValues("_", "_", nil, nil, nil, satTC, satSrc, satIn, "_", DefaultRealm)
	op := keySet(ctx["operator"])
	pr := keySet(ctx["project"])
	return op, pr
}

// keySet returns the set of keys of v when v is a map[string]string (the
// shape renderContextValues uses for the operator/project leaves), or an
// empty set otherwise.
func keySet(v any) map[string]struct{} {
	out := map[string]struct{}{}
	m, ok := v.(map[string]string)
	if !ok {
		return out
	}
	for k := range m {
		out[k] = struct{}{}
	}
	return out
}

// definedTemplateNames returns the set of template names actually defined in
// the parsed set — every associated template whose parse tree is non-nil.
// An invoked-but-undefined `{{ template "x" }}` does not register a tree, so
// it is absent here and reads as a partials miss.
func definedTemplateNames(tpl *template.Template) map[string]struct{} {
	out := map[string]struct{}{}
	for _, t := range tpl.Templates() {
		if t.Tree != nil {
			out[t.Name()] = struct{}{}
		}
	}
	return out
}

// tplFullPath resolves the on-disk path of a harness's blueprint template,
// matching RenderBlueprint's resolution (harness-dir-relative, per #1110).
func tplFullPath(cacheDir, harness, template string) string {
	return teamsource.HarnessDir(cacheDir, harness) + "/" + template
}

// templateDisplayPath renders the report-facing path of a template body. For
// the main blueprint and sibling partial files name is the filename, yielding
// `harnesses/<h>/<file>`; for a `{{ define }}` block the name is the define
// label, yielding `harnesses/<h>/<label>` — informative enough to locate the
// reference without the renderer tracking per-node source positions.
func templateDisplayPath(harness, name string) string {
	return "harnesses/" + harness + "/" + name
}

// sortLines orders report lines by Detail so a section's output is stable
// across runs regardless of map-iteration or discovery order.
func sortLines(lines []ReportLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		return lines[i].Detail < lines[j].Detail
	})
}

// walkNodes visits node and every descendant, invoking visit on each. It
// covers the template parse-tree node kinds the blueprint contract uses
// (actions, pipelines, the if/range/with branches, template invocations, and
// chained field access); leaf nodes (FieldNode, TextNode, …) are visited but
// have no children to recurse into.
func walkNodes(node parse.Node, visit func(parse.Node)) {
	if node == nil {
		return
	}
	visit(node)
	switch n := node.(type) {
	case *parse.ListNode:
		if n == nil {
			return
		}
		for _, c := range n.Nodes {
			walkNodes(c, visit)
		}
	case *parse.ActionNode:
		walkNodes(n.Pipe, visit)
	case *parse.PipeNode:
		if n == nil {
			return
		}
		for _, c := range n.Cmds {
			walkNodes(c, visit)
		}
	case *parse.CommandNode:
		for _, a := range n.Args {
			walkNodes(a, visit)
		}
	case *parse.IfNode:
		walkBranch(n.Pipe, n.List, n.ElseList, visit)
	case *parse.RangeNode:
		walkBranch(n.Pipe, n.List, n.ElseList, visit)
	case *parse.WithNode:
		walkBranch(n.Pipe, n.List, n.ElseList, visit)
	case *parse.TemplateNode:
		walkNodes(n.Pipe, visit)
	case *parse.ChainNode:
		walkNodes(n.Node, visit)
	}
}

// walkBranch recurses into the pipeline, body, and else-body of an
// if/range/with node.
func walkBranch(pipe *parse.PipeNode, list, elseList *parse.ListNode, visit func(parse.Node)) {
	walkNodes(pipe, visit)
	if list != nil {
		walkNodes(list, visit)
	}
	if elseList != nil {
		walkNodes(elseList, visit)
	}
}
