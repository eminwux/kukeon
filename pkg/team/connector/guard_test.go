// Copyright 2026 Emiliano Spinella (eminwux)
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

package connector_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/team/connector"
)

// repoRoot is the repository root, relative to this package.
const repoRoot = "../../.."

// The two places that may name a connector (issue #1548): the catalog itself
// and the delivery handlers — git and registry, the two connectors whose
// delivery is code rather than a secret slot.
const (
	catalogDir   = "team/connectors/"
	handlersFile = "internal/connector/handlers.go"
)

// sameWordEntry is one place where a literal equals a connector's name but
// means something else: a path prefix, the literal, and what it is there.
type sameWordEntry struct{ prefix, literal, meaning string }

// legacyTokenEntry is one file that spells a connector's own token for a
// feature that is not connector machinery.
type legacyTokenEntry struct{ path, meaning string }

// sameWord lists the string literals that equal a connector's name but mean
// something else — a connector is named after the thing it gives access to, so
// the harness, the tool and the delivery handler of the same name exist
// independently of it. Naming a connector in any other place fails the guard:
// read the connector from the catalog instead (connector.Catalog), or — for a
// connector whose delivery is code — put the code in the delivery handlers.
func sameWord() []sameWordEntry {
	return []sameWordEntry{
		// Harness names. A harness and the connector that authenticates it share
		// a name; the link between them is the connector's spec.harness.
		{"pkg/api/model/kuketeams/doc.go", "claude", "bundled harness name"},
		{"pkg/api/model/kuketeams/doc.go", "codex", "bundled harness name"},
		{"pkg/team/compose/render.go", "claude", "harness renderer"},
		{"pkg/team/compose/render.go", "codex", "harness renderer"},
		{"pkg/team/config/team.go", "claude", "default harness selection"},
		{"pkg/team/config/team.go", "codex", "default harness selection"},
		// The delivery handler kinds of the descriptor format, and the kind of a
		// rendered value: API vocabulary a connector.yaml refers to.
		{"pkg/api/model/v1beta1/connector.go", "git", "delivery handler kind"},
		{"pkg/api/model/v1beta1/connector.go", "registry", "delivery handler kind"},
		{"pkg/api/model/v1beta1/defaults.go", "git", "rendered-value kind"},
		{"internal/controller/apply/diff.go", "git", "name of the spec field in a diff"},
		// The git program and the image capability that provides it.
		{"pkg/team/compose/render.go", "git", "image capability"},
		{"cmd/kuketty/", "git", "the git program"},
		{"internal/gitcred/", "git", "the git program and the default SSH user"},
		{"internal/connector/gitimport/gitconfig.go", "git", "the git program"},
		{"pkg/team/config/yaml.go", "git", "the removed spec.git field, named in its decode error"},
		{"internal/team/gitremote/", "git", "the git program"},
		{"internal/webui/frontend/src/connectors.mjs", "git", "delivery handler kind"},
		{"internal/webui/frontend/src/connectors.mjs", "registry", "delivery handler kind"},
		{"internal/webui/frontend/src/config-form.mjs", "git", "rendered-value kind"},
		{"internal/webui/frontend/src/creation-schema.mjs", "git", "the `git` block of a spec"},
		{"internal/webui/frontend/src/new-config.tsx", "git", "default SSH user, as a placeholder"},
		// A command-line word.
		{"team/harnesses/cmd/images/", "registry", "flag name"},
	}
}

// legacyTokens lists the files that spell one of a connector's own tokens (a
// secret slot, its alias, its environment variable) for a feature that
// predates connectors and is not connector machinery.
func legacyTokens() []legacyTokenEntry {
	return []legacyTokenEntry{
		// The majordomo health sweep files issues with the operator's GitHub
		// token: the secret it opts in with and the variable it reads.
		{"cmd/kuke/health/filing.go", "sweep opt-in secret and its token variable"},
	}
}

// catalogWords collects what the guard looks for: the connectors' names, and
// the tokens that belong to one connector only — its component names, secret
// slots, default Secret names, aliases and environment variables.
func catalogWords(t *testing.T) ([]string, []string) {
	t.Helper()
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	names := catalog.Names()
	var tokens []string
	add := func(values ...string) {
		for _, value := range values {
			// A token that is also a connector's name is covered as a name.
			if value != "" && !slices.Contains(names, value) && !slices.Contains(tokens, value) {
				tokens = append(tokens, value)
			}
		}
	}
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		for _, method := range conn.Spec.Methods {
			add(conn.ComponentName(method))
			if s := method.Secret; s != nil {
				add(s.Name, s.SecretName, s.EnvName)
				add(s.Aliases...)
			}
		}
	}
	if len(names) == 0 || len(tokens) == 0 {
		t.Fatalf("the embedded catalog is empty: names=%v tokens=%v", names, tokens)
	}
	return names, tokens
}

// allowances answers the guard's two exceptions and records which ones were
// needed, so an entry nothing uses any more is reported.
type allowances struct{ names, tokens map[int]bool }

func newAllowances() *allowances { return &allowances{names: map[int]bool{}, tokens: map[int]bool{}} }

func (a *allowances) name(path, literal string) bool {
	for i, entry := range sameWord() {
		if entry.literal == literal && strings.HasPrefix(path, entry.prefix) {
			a.names[i] = true
			return true
		}
	}
	return false
}

func (a *allowances) token(path string) bool {
	for i, entry := range legacyTokens() {
		if entry.path == path {
			a.tokens[i] = true
			return true
		}
	}
	return false
}

// unused fails for every exception the scan did not need.
func (a *allowances) unused(t *testing.T) {
	t.Helper()
	for i, entry := range sameWord() {
		if !a.names[i] {
			t.Errorf("sameWord entry {%s, %q} matches nothing any more; remove it", entry.prefix, entry.literal)
		}
	}
	for i, entry := range legacyTokens() {
		if !a.tokens[i] {
			t.Errorf("legacyTokens entry %s matches nothing any more; remove it", entry.path)
		}
	}
}

// skippedDir reports whether a directory holds no source the guard reads:
// dependencies, generated output, fixtures and tests.
func skippedDir(name string) bool {
	return slices.Contains([]string{
		".git", "node_modules", "dist", "testdata", "connectortest", "e2e", "tests", "test-results",
		"playwright-report",
	}, name)
}

// mayNameConnectors reports whether a file is one of the two places that may
// name a connector.
func mayNameConnectors(rel string) bool {
	return strings.HasPrefix(rel, catalogDir) || rel == handlersFile
}

// isTestSource reports whether a file is a test, a spec or a mock.
func isTestSource(base string) bool {
	return strings.HasSuffix(base, "_test.go") || strings.Contains(base, ".test.") ||
		strings.Contains(base, ".spec.") || strings.Contains(base, "mock")
}

// sources walks the repository for the files with one of the extensions,
// skipping tests, generated output and dependencies.
func sources(t *testing.T, extensions ...string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, repoRoot+string(filepath.Separator)))
		if d.IsDir() {
			if skippedDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if slices.Contains(extensions, filepath.Ext(rel)) && !mayNameConnectors(rel) && !isTestSource(d.Name()) {
			out = append(out, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no %v sources found under %s", extensions, repoRoot)
	}
	return out
}

// TestNoConnectorIsHardCoded is the guard of issue #1548: outside the catalog
// and the delivery handlers, no source names a connector. It checks string
// literals — comments are free to give examples — in every non-test Go file
// and in the web UI's sources: none equals a connector's name, and none
// mentions a token that belongs to one connector.
func TestNoConnectorIsHardCoded(t *testing.T) {
	names, tokens := catalogWords(t)
	g := &guard{t: t, names: names, tokens: tokens, allow: newAllowances()}
	g.scanGo()
	g.scanWebUI()
	g.allow.unused(t)
}

// guard checks string literals against the catalog's words.
type guard struct {
	t             *testing.T
	names, tokens []string
	allow         *allowances
}

// check fails for a literal that names a connector or mentions one of its
// tokens outside the listed exceptions.
func (g *guard) check(rel string, line int, value, advice string) {
	if slices.Contains(g.names, value) && !g.allow.name(rel, value) {
		g.t.Errorf("%s:%d: string literal %q names a connector; %s", rel, line, value, advice)
	}
	for _, tok := range g.tokens {
		if containsWord(value, tok) && !g.allow.token(rel) {
			g.t.Errorf("%s:%d: string literal mentions %q, which belongs to a connector; %s", rel, line, tok, advice)
		}
	}
}

// scanGo checks the string literals of every non-test Go file. Import paths
// are not literals the code acts on.
func (g *guard) scanGo() {
	fset := token.NewFileSet()
	for _, rel := range sources(g.t, ".go") {
		file, err := parser.ParseFile(fset, filepath.Join(repoRoot, rel), nil, parser.SkipObjectResolution)
		if err != nil {
			g.t.Fatalf("%s: %v", rel, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if _, isImport := node.(*ast.ImportSpec); isImport {
				return false
			}
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if value, unquoteErr := strconv.Unquote(lit.Value); unquoteErr == nil {
				g.check(rel, fset.Position(lit.Pos()).Line, value, "read it from the connector catalog")
			}
			return true
		})
	}
}

// scanWebUI checks the quoted literals of the web UI's sources: the UI renders
// what GET /api/connectors returns.
func (g *guard) scanWebUI() {
	const webUI = "internal/webui/frontend/src/"
	quoted := regexp.MustCompile("(['\"`])([^'\"`\\n]*)(['\"`])")
	comment := regexp.MustCompile(`^\s*(//|\*|/\*)`)
	checked := 0
	for _, rel := range sources(g.t, ".ts", ".tsx", ".mts", ".mjs", ".js", ".jsx") {
		if !strings.HasPrefix(rel, webUI) {
			continue
		}
		checked++
		raw, err := os.ReadFile(filepath.Join(repoRoot, rel))
		if err != nil {
			g.t.Fatal(err)
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if comment.MatchString(line) {
				continue
			}
			for _, m := range quoted.FindAllStringSubmatch(line, -1) {
				if m[1] == m[3] {
					g.check(rel, i+1, m[2], "render it from /api/connectors")
				}
			}
		}
	}
	if checked == 0 {
		g.t.Fatalf("no web UI sources found under %s", webUI)
	}
}

// isWordByte reports whether a byte can be part of an identifier.
func isWordByte(r byte) bool {
	switch {
	case r == '_', r == '-':
		return true
	case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	}
	return false
}

// containsWord reports whether text contains word as a whole word: not as
// part of a longer identifier.
func containsWord(text, word string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(word)
		if (start == 0 || !isWordByte(text[start-1])) && (end == len(text) || !isWordByte(text[end])) {
			return true
		}
		from = start + 1
	}
}

// The guard is only as good as its word list: prove it flags what it should.
func TestGuardWords(t *testing.T) {
	names, tokens := catalogWords(t)
	for _, want := range []string{"example-token"} {
		if slices.Contains(names, want) {
			t.Fatalf("the test-only connector %q is in the embedded catalog", want)
		}
	}
	if !containsWord("export GH_TOKEN=x", "GH_TOKEN") || containsWord("MY_GH_TOKEN_2", "GH_TOKEN") ||
		!containsWord("a,gh-token", "gh-token") || containsWord("gh-tokens", "gh-token") {
		t.Fatal("containsWord does not match whole words")
	}
	allow := newAllowances()
	for _, name := range names {
		if allow.name("internal/controller/render_cell.go", name) || allow.token("internal/controller/render_cell.go") {
			t.Fatalf("connector name %q is allowed in connector-agnostic code", name)
		}
	}
	if len(tokens) < len(names) {
		t.Fatalf("suspiciously few connector tokens: %v", tokens)
	}
}
