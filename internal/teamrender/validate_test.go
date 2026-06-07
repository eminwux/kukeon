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
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/teamsource"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

// sectionByTitle returns the named section from a report, failing the test if
// it is absent (every report must render all four).
func sectionByTitle(t *testing.T, r *ValidationReport, title string) ReportSection {
	t.Helper()
	for _, s := range r.Sections {
		if s.Title == title {
			return s
		}
	}
	t.Fatalf("report missing section %q", title)
	return ReportSection{}
}

// hasMissContaining reports whether the section carries a miss line whose
// Detail contains every one of subs.
func hasMissContaining(s ReportSection, subs ...string) bool {
	return hasLineContaining(s, false, subs...)
}

// hasOKContaining reports whether the section carries an ok line whose Detail
// contains every one of subs.
func hasOKContaining(s ReportSection, subs ...string) bool {
	return hasLineContaining(s, true, subs...)
}

func hasLineContaining(s ReportSection, ok bool, subs ...string) bool {
	for _, l := range s.Lines {
		if l.OK != ok {
			continue
		}
		all := true
		for _, sub := range subs {
			if !strings.Contains(l.Detail, sub) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// twoHarnessBundle builds a bundle with a single "dev" role declaring the
// given image needs, a claude harness + image that satisfies them, and an
// opencode harness whose image is missing one capability — exercising one
// catalog ok and one catalog miss in the same pass. claude's template is
// always "blueprint.tmpl.yaml"; opencodeTmpl varies per test (use "" to leave
// opencode's spec.template empty). The caller pre-writes the matching files
// under cacheDir with writeHarnessFile.
func twoHarnessBundle(
	cacheDir string,
	devNeeds []string,
	opencodeTmpl string,
) (*teamsource.Bundle, *model.ProjectTeam) {
	const claudeTmpl = "blueprint.tmpl.yaml"
	role := &model.Role{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindRole,
		Metadata:   model.Metadata{Name: "dev"},
		Spec: model.RoleSpec{
			Needs: model.RoleNeeds{Image: devNeeds},
		},
	}
	ic := &model.ImageCatalog{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindImageCatalog,
		Spec: model.ImageCatalogSpec{
			Images: []model.ImageCatalogEntry{
				{
					Ref:          "claude",
					Harness:      "claude",
					Image:        "registry.local/claude:latest",
					Capabilities: devNeeds, // superset of itself → claude matches
				},
				{
					Ref:          "opencode",
					Harness:      "opencode",
					Image:        "registry.local/opencode:latest",
					Capabilities: []string{}, // provides nothing → first need unmet
				},
			},
		},
	}
	bundle := &teamsource.Bundle{
		Source:   teamsource.Source{Host: "github.com", OwnerRepo: "eminwux/agents", Ref: "v1"},
		CacheDir: cacheDir,
		Roles:    map[string]*model.Role{"dev": role},
		Harnesses: map[string]*model.Harness{
			"claude": {
				APIVersion: model.APIVersionV1,
				Kind:       model.KindHarness,
				Metadata:   model.Metadata{Name: "claude"},
				Spec:       model.HarnessSpec{Template: claudeTmpl},
			},
			"opencode": {
				APIVersion: model.APIVersionV1,
				Kind:       model.KindHarness,
				Metadata:   model.Metadata{Name: "opencode"},
				Spec:       model.HarnessSpec{Template: opencodeTmpl},
			},
		},
		ImageCatalog: ic,
	}
	pt := &model.ProjectTeam{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindProjectTeam,
		Metadata:   model.Metadata{Name: "proj"},
		Spec: model.ProjectTeamSpec{
			Defaults: model.ProjectTeamDefaults{Harnesses: []string{"claude", "opencode"}},
			Roles:    []model.ProjectTeamRole{{Ref: "dev"}},
		},
	}
	return bundle, pt
}

func TestValidateCatalogOKAndMiss(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	// Both harnesses get a present template so this test isolates the catalog
	// section.
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go", "git"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	cat := sectionByTitle(t, rep, SectionCatalog)

	if !hasOKContaining(cat, "dev × claude", "→ claude") {
		t.Errorf("catalog missing ok line for dev × claude: %+v", cat.Lines)
	}
	if !hasMissContaining(cat, "dev × opencode", `capability "git" not provided by any opencode image`) {
		t.Errorf("catalog missing expected miss for dev × opencode: %+v", cat.Lines)
	}
	if !rep.HasMiss() {
		t.Errorf("report should report a miss")
	}
}

func TestValidateCatalogEmptyCatalog(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")
	bundle.ImageCatalog = &model.ImageCatalog{
		APIVersion: model.APIVersionV1, Kind: model.KindImageCatalog,
		Spec: model.ImageCatalogSpec{},
	}

	rep := Validate(bundle, pt)
	cat := sectionByTitle(t, rep, SectionCatalog)
	if !hasMissContaining(cat, "dev × claude", "no image in images.yaml for harness") {
		t.Errorf("expected empty-catalog miss reason: %+v", cat.Lines)
	}
}

func TestValidateTemplatesOKAndMiss(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	// claude's template exists on disk; opencode's is declared but absent.
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	tmpls := sectionByTitle(t, rep, SectionTemplates)

	if !hasOKContaining(tmpls, "harnesses/claude/blueprint.tmpl.yaml") {
		t.Errorf("templates missing ok line for claude: %+v", tmpls.Lines)
	}
	if !hasMissContaining(tmpls, "harnesses/opencode/blueprint.tmpl.yaml", "template: blueprint.tmpl.yaml not found") {
		t.Errorf("templates missing not-found miss for opencode: %+v", tmpls.Lines)
	}
}

func TestValidateTemplatesEmptyTemplatePath(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	// opencode declares no template at all.
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "")

	rep := Validate(bundle, pt)
	tmpls := sectionByTitle(t, rep, SectionTemplates)
	if !hasMissContaining(tmpls, "harnesses/opencode", "spec.template is empty") {
		t.Errorf("expected empty-template miss for opencode: %+v", tmpls.Lines)
	}
}

func TestValidatePartialsDetectsUnboundReference(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	// claude references a partial that is never defined; opencode references
	// one that a sibling defines.
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml",
		"kind: CellBlueprint\n{{ template \"mount_source\" . }}\n")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml",
		"kind: CellBlueprint\n{{ template \"mounts\" . }}\n")
	writeHarnessFile(t, cacheDir, "opencode", "partials.tmpl.yaml",
		"{{ define \"mounts\" }}m{{ end }}\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	partials := sectionByTitle(t, rep, SectionPartials)

	if !hasMissContaining(
		partials,
		"harnesses/claude/blueprint.tmpl.yaml",
		`uses template "mount_source"`,
		"no {{ define }} found",
	) {
		t.Errorf("partials missing unbound-reference miss: %+v", partials.Lines)
	}
	// opencode's reference resolves → no miss naming it.
	if hasMissContaining(partials, "opencode") {
		t.Errorf("opencode partial reference is defined and must not be flagged: %+v", partials.Lines)
	}
}

func TestValidatePartialsCleanWhenAllResolved(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml",
		"kind: CellBlueprint\n{{ template \"x\" . }}\n")
	writeHarnessFile(t, cacheDir, "claude", "partials.tmpl.yaml",
		"{{ define \"x\" }}y{{ end }}\n")
	// Single-harness roster keeps the focus on partials.
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")

	rep := Validate(bundle, pt)
	partials := sectionByTitle(t, rep, SectionPartials)
	for _, l := range partials.Lines {
		if !l.OK {
			t.Errorf("expected no partial misses, got: %q", l.Detail)
		}
	}
}

func TestValidateFactsDetectsUnboundKey(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	// References one known operator key, one known project key, and one
	// unknown operator key — only the unknown is a miss.
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml",
		"name: {{ .project.NAME }}\nuser: {{ .operator.GIT_USER_NAME }}\nbad: {{ .operator.UNKNOWN_KEY }}\n")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	facts := sectionByTitle(t, rep, SectionFacts)

	if !hasMissContaining(facts, ".operator.UNKNOWN_KEY", "harnesses/claude/blueprint.tmpl.yaml", "not bound") {
		t.Errorf("facts missing unbound-key miss: %+v", facts.Lines)
	}
	if hasMissContaining(facts, "GIT_USER_NAME") || hasMissContaining(facts, "NAME") {
		t.Errorf("known facts must not be flagged: %+v", facts.Lines)
	}
}

func TestValidateFactsAllKnownKeysClean(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	knownOp, knownPr := knownFactKeys()
	var b strings.Builder
	b.WriteString("kind: CellBlueprint\n")
	for k := range knownOp {
		b.WriteString("o: {{ .operator." + k + " }}\n")
	}
	for k := range knownPr {
		b.WriteString("p: {{ .project." + k + " }}\n")
	}
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml", b.String())
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	facts := sectionByTitle(t, rep, SectionFacts)
	for _, l := range facts.Lines {
		if !l.OK {
			t.Errorf("known fact key flagged as unbound: %q", l.Detail)
		}
	}
}

// TestYAMLCommentIndex locks the quote-aware `#`-comment detection that the
// validate-path comment strip relies on: a `#` at line start or after
// whitespace begins a comment, but a `#` inside a quoted scalar (or one glued
// to a preceding non-space char, as in a URL fragment) does not (#1123).
func TestYAMLCommentIndex(t *testing.T) {
	t.Parallel()
	cases := []struct {
		line string
		want int
	}{
		{"# whole line", 0},
		{"key: value # trailing", 11},
		{"key: value\t# tab-led", 11},
		{"no comment here", -1},
		{"url: http://x#frag", -1},                       // glued # is not a comment
		{`note: "a # b" {{ .operator.REAL }}`, -1},       // # inside double quotes
		{`note: 'a # b' after`, -1},                      // # inside single quotes
		{`name: {{ .x }} # doc {{ .operator.X }}`, 15},   // comment after a live action
		{`msg: "escaped \" # still in string" tail`, -1}, // escaped quote keeps string open
	}
	for _, tc := range cases {
		if got := yamlCommentIndex(tc.line); got != tc.want {
			t.Errorf("yamlCommentIndex(%q) = %d, want %d", tc.line, got, tc.want)
		}
	}
}

// TestValidateFactsIgnoresCommentedReferences confirms a template that
// documents its fact contract with `{{ .operator.X }}` / `{{ .project.X }}`
// inside YAML `#` comments produces no facts misses — the comment-stripping
// pass drops those actions before the parser sees them (#1123). A real
// unbound reference on a live (non-comment) line still surfaces.
func TestValidateFactsIgnoresCommentedReferences(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml",
		"#   - {{ .operator.X }} pulls from TeamsConfig (REGISTRY, HOME_DIR, ...).\n"+
			"#   - {{ .project.X }}  pulls from ProjectTeam (AGENTS_REPO, PROJECT_DIR).\n"+
			"kind: CellBlueprint\n"+
			"name: {{ .project.NAME }}  # {{ .operator.ALSO_COMMENTED }} ignored\n"+
			"bad: {{ .operator.REAL_GAP }}\n")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	facts := sectionByTitle(t, rep, SectionFacts)

	for _, commented := range []string{".operator.X", ".project.X", ".operator.ALSO_COMMENTED"} {
		if hasMissContaining(facts, commented) {
			t.Errorf("commented reference %q must not be flagged: %+v", commented, facts.Lines)
		}
	}
	// The genuine gap on a live line still surfaces.
	if !hasMissContaining(facts, ".operator.REAL_GAP", "not bound") {
		t.Errorf("live unbound reference must still be flagged: %+v", facts.Lines)
	}
}

// TestValidatePartialsIgnoresCommentedTemplateInvocation confirms a
// `{{ template "name" }}` invocation documented inside a YAML `#` comment is
// not flagged as an unbound partial reference — the same comment-stripping pass
// applies to the partials walk (#1123).
func TestValidatePartialsIgnoresCommentedTemplateInvocation(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(t, cacheDir, "claude", "blueprint.tmpl.yaml",
		"# e.g. {{ template \"mount_source\" . }} wires a repo mount\n"+
			"kind: CellBlueprint\n")
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml", "kind: CellBlueprint\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go"}, "blueprint.tmpl.yaml")

	rep := Validate(bundle, pt)
	partials := sectionByTitle(t, rep, SectionPartials)
	if hasMissContaining(partials, "mount_source") {
		t.Errorf("commented template invocation must not be flagged: %+v", partials.Lines)
	}
}

// TestKnownFactKeysCoverRendererContract pins the fact schema the validator
// checks against, so a future renderContextValues key add/remove that this
// derivation misses is caught here rather than silently passing validate.
func TestKnownFactKeysCoverRendererContract(t *testing.T) {
	t.Parallel()
	op, pr := knownFactKeys()
	wantOp := []string{"REGISTRY", "GIT_USER_NAME", "GIT_USER_EMAIL", "TEAM_ROOT", "HOME_DIR", "REPO_OWNER"}
	for _, k := range wantOp {
		if _, ok := op[k]; !ok {
			t.Errorf("known operator keys missing %q (have %v)", k, op)
		}
	}
	wantPr := []string{"NAME", "PROJECT_DIR", "AGENTS_REPO"}
	for _, k := range wantPr {
		if _, ok := pr[k]; !ok {
			t.Errorf("known project keys missing %q (have %v)", k, pr)
		}
	}
}

func TestValidateHarnessLessRosterEmptySections(t *testing.T) {
	t.Parallel()
	pt := &model.ProjectTeam{
		APIVersion: model.APIVersionV1, Kind: model.KindProjectTeam,
		Metadata: model.Metadata{Name: "proj"},
		Spec:     model.ProjectTeamSpec{}, // no roles, no harnesses
	}
	rep := Validate(nil, pt)
	if rep.HasMiss() {
		t.Errorf("harness-less roster should have no misses: %d", rep.MissCount())
	}
	for _, title := range []string{SectionCatalog, SectionTemplates, SectionPartials, SectionFacts} {
		s := sectionByTitle(t, rep, title)
		if len(s.Lines) != 0 {
			t.Errorf("section %q should be empty for harness-less roster: %+v", title, s.Lines)
		}
	}
}

func TestValidateReportWriteFormat(t *testing.T) {
	t.Parallel()
	rep := &ValidationReport{Sections: []ReportSection{
		{Title: SectionCatalog, Lines: []ReportLine{
			{OK: true, Detail: "dev × claude → claude"},
			{OK: false, Detail: "pm × opencode → capability \"android\" not provided by any opencode image"},
		}},
		{Title: SectionTemplates},
		{Title: SectionPartials},
		{Title: SectionFacts},
	}}
	var b strings.Builder
	if err := rep.Write(&b); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := b.String()
	for _, want := range []string{
		"== catalog ==\n",
		"ok   dev × claude → claude\n",
		"miss pm × opencode → capability \"android\" not provided by any opencode image\n",
		"== templates ==\n",
		"== partials ==\n",
		"== facts ==\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Write output missing %q\n--- got ---\n%s", want, got)
		}
	}
	// Sections are separated by a blank line.
	if !strings.Contains(got, "image\n\n== templates ==") {
		t.Errorf("expected blank line between sections:\n%s", got)
	}
}

// TestValidateDeterministicOutput asserts two runs against the same inputs
// produce byte-identical reports (the AC's stable-ordering requirement),
// including across the map-iterated harness/fact discovery.
func TestValidateDeterministicOutput(t *testing.T) {
	t.Parallel()
	cacheDir := t.TempDir()
	writeHarnessFile(
		t,
		cacheDir,
		"claude",
		"blueprint.tmpl.yaml",
		"kind: CellBlueprint\na: {{ .operator.UNKNOWN_A }}\nb: {{ .operator.UNKNOWN_B }}\n{{ template \"undef_a\" . }}{{ template \"undef_b\" . }}\n",
	)
	writeHarnessFile(t, cacheDir, "opencode", "blueprint.tmpl.yaml",
		"kind: CellBlueprint\nc: {{ .project.UNKNOWN_C }}\n")
	bundle, pt := twoHarnessBundle(cacheDir, []string{"go", "git"}, "blueprint.tmpl.yaml")

	var first, second strings.Builder
	if err := Validate(bundle, pt).Write(&first); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := Validate(bundle, pt).Write(&second); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if first.String() != second.String() {
		t.Errorf("non-deterministic report:\n--- first ---\n%s\n--- second ---\n%s", first.String(), second.String())
	}
}
