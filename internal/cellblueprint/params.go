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

package cellblueprint

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	"gopkg.in/yaml.v3"
)

// Moved from internal/cellprofile in #626 (CellProfile removed; Resolve is the
// sole remaining consumer).

// paramRefRE matches `${KEY}` substitution references in blueprint scalars.
// KEY must start with a letter or underscore and contain only letters,
// digits, and underscores — the same shape POSIX shells use, so existing
// envsubst-style blueprints port over without re-quoting.
var paramRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// paramNameRE is the same alphabet used to validate `--param KEY=...` and
// `--param-file` keys. Kept aligned with paramRefRE so a key that survives
// CLI parsing is guaranteed to match a `${KEY}` reference shape.
var paramNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// substituteScalars rewrites every `${KEY}` occurrence inside scalar values
// with values[KEY]. Mapping keys are not rewritten. Keys not present in
// values are left as literal `${KEY}` — callers must validate references
// first (Resolve does, against spec.parameters[]).
func substituteScalars(node *yaml.Node, values map[string]string) {
	walkScalars(node, func(s *yaml.Node) {
		if !strings.Contains(s.Value, "${") {
			return
		}
		s.Value = paramRefRE.ReplaceAllStringFunc(s.Value, func(match string) string {
			k := paramRefRE.FindStringSubmatch(match)[1]
			if v, ok := values[k]; ok {
				return v
			}
			return match
		})
		// Clear the resolved type tag: the post-substitution value may no
		// longer match the pre-substitution shape (e.g., `${PORT}` typed as
		// !!int when PORT="8080" must stay a string after `${PORT}-foo`
		// substitutes to `8080-foo`). Style is preserved so quoting hints
		// (DoubleQuoted, Literal block) flow through.
		s.Tag = ""
	})
}

// walkScalars traverses the YAML node tree, invoking f on every scalar value
// node — but NOT on mapping keys. Aliases are not followed: callers that
// rely on anchor/alias references must substitute on the anchored node only.
func walkScalars(n *yaml.Node, f func(*yaml.Node)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			walkScalars(c, f)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			walkScalars(n.Content[i+1], f)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkScalars(c, f)
		}
	case yaml.ScalarNode:
		f(n)
	case yaml.AliasNode:
		// No-op: aliases share state with the anchor; substituting via the
		// alias would double-mutate the underlying node.
	}
}

// ParseParamArgs validates a slice of `KEY=VALUE` strings (from --param) and
// returns the equivalent map. Empty values are allowed (`KEY=` → "");
// missing `=` errors. Duplicate keys: last occurrence wins, matching `docker
// run -e` semantics.
func ParseParamArgs(args []string) (map[string]string, error) {
	out := make(map[string]string, len(args))
	for _, arg := range args {
		k, v, ok := splitKV(arg)
		if !ok {
			return nil, fmt.Errorf(
				"invalid --param %q: want KEY=VALUE: %w",
				arg, errdefs.ErrBlueprintInvalid,
			)
		}
		if !paramNameRE.MatchString(k) {
			return nil, fmt.Errorf(
				"invalid --param %q: KEY must match [A-Za-z_][A-Za-z0-9_]*: %w",
				arg, errdefs.ErrBlueprintInvalid,
			)
		}
		out[k] = v
	}
	return out, nil
}

// ParseParamFile reads path and parses it as a list of `KEY=VALUE` lines.
// Blank lines and lines whose first non-whitespace character is `#` are
// skipped (POSIX-ish comment shape). Same KEY=VALUE rules as ParseParamArgs.
//
// Values are taken verbatim after the first `=` — no quote stripping, no
// shell expansion. Callers that need shell semantics should pre-render their
// own file. Trailing CR is stripped so files written on Windows host editors
// still parse cleanly.
func ParseParamFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read --param-file %q: %w", path, err)
	}
	out := make(map[string]string)
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		k, v, ok := splitKV(line)
		if !ok {
			return nil, fmt.Errorf(
				"%s:%d: invalid line %q: want KEY=VALUE: %w",
				path, i+1, line, errdefs.ErrBlueprintInvalid,
			)
		}
		if !paramNameRE.MatchString(k) {
			return nil, fmt.Errorf(
				"%s:%d: invalid KEY %q: must match [A-Za-z_][A-Za-z0-9_]*: %w",
				path, i+1, k, errdefs.ErrBlueprintInvalid,
			)
		}
		out[k] = v
	}
	return out, nil
}

// MergeParams returns the union of base and overrides, with overrides
// winning on duplicate keys. Used by `kuke run -b` / `kuke apply -b` to
// layer --param on top of --param-file (CLI flag is later-binding than the
// file).
func MergeParams(base, overrides map[string]string) map[string]string {
	out := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func splitKV(s string) (string, string, bool) {
	eq := strings.IndexByte(s, '=')
	if eq < 0 {
		return "", "", false
	}
	return strings.TrimSpace(s[:eq]), s[eq+1:], true
}
