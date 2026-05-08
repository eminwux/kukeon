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

package cellprofile

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// paramRefRE matches `${KEY}` substitution references in profile scalars.
// KEY must start with a letter or underscore and contain only letters,
// digits, and underscores — the same shape POSIX shells use, so existing
// envsubst-style profiles port over without re-quoting.
var paramRefRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// paramNameRE is the same alphabet used to validate `--param KEY=...` and
// `--param-file` keys. Kept aligned with paramRefRE so a key that survives
// CLI parsing is guaranteed to match a `${KEY}` reference shape.
var paramNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// LoadResolved is Load + parameter substitution. Resolution order per
// declared parameter: cliParams[k] > parameters[k].default > lookupEnv(k).
// When lookupEnv is nil the env fallback is skipped — useful for tests and
// for callers that want to keep substitution narrow (e.g., a daemon that
// must not draw from the user's shell).
//
// Returns errdefs.ErrProfileInvalid wrapped when:
//   - the profile body references `${KEY}` for a KEY not declared in
//     spec.parameters[] (typo at install time);
//   - cliParams contains a key not declared in spec.parameters[] (typo at
//     call time);
//   - a parameter declared `required: true` resolves to no value.
//
// Empty string is a valid resolved value: only "unset" — every provider
// declines — triggers the required-vs-default decision.
func LoadResolved(
	dir, name string,
	cliParams map[string]string,
	lookupEnv func(string) (string, bool),
) (*v1beta1.CellProfileDoc, error) {
	profile, node, err := locate(dir, name)
	if err != nil {
		return nil, err
	}
	return resolve(profile, node, cliParams, lookupEnv)
}

// resolve is the lower-level half of LoadResolved: given an already-parsed
// profile and its yaml.Node tree, validate cliParams, build the value map,
// substitute scalars in a clone of the node tree, and decode the result.
//
// The clone is deliberate: callers that want to replay resolution against a
// different cliParams map (tests do this) need the input tree to stay
// untouched. The cost is one round-trip through yaml.Marshal/Unmarshal per
// resolution — negligible given profile sizes.
func resolve(
	profile *v1beta1.CellProfileDoc,
	node *yaml.Node,
	cliParams map[string]string,
	lookupEnv func(string) (string, bool),
) (*v1beta1.CellProfileDoc, error) {
	declared := make(map[string]v1beta1.CellProfileParameter, len(profile.Spec.Parameters))
	for _, p := range profile.Spec.Parameters {
		declared[p.Name] = p
	}

	for k := range cliParams {
		if _, ok := declared[k]; !ok {
			return nil, fmt.Errorf(
				"profile %q: --param %q is not declared in spec.parameters[]: %w",
				profile.Metadata.Name, k, errdefs.ErrProfileInvalid,
			)
		}
	}

	values := make(map[string]string, len(declared))
	for _, p := range profile.Spec.Parameters {
		if v, ok := cliParams[p.Name]; ok {
			values[p.Name] = v
			continue
		}
		if p.Default != nil {
			values[p.Name] = *p.Default
			continue
		}
		if lookupEnv != nil {
			if v, ok := lookupEnv(p.Name); ok {
				values[p.Name] = v
				continue
			}
		}
		if p.Required {
			return nil, fmt.Errorf(
				"profile %q: required parameter %q is not set "+
					"(provide --param %s=... or declare a spec.parameters[].default): %w",
				profile.Metadata.Name, p.Name, p.Name, errdefs.ErrProfileInvalid,
			)
		}
		// Unset, not required: substitute with empty string. Matches
		// `envsubst` behavior for unset env vars and the proposal's
		// "empty string is a valid value" rule — declared but unused
		// params drop out cleanly instead of leaving a literal `${KEY}`
		// in the materialized cell.
		values[p.Name] = ""
	}

	resolved := cloneNode(node)
	substituteScalars(resolved, values)

	var out v1beta1.CellProfileDoc
	if err := resolved.Decode(&out); err != nil {
		return nil, fmt.Errorf(
			"profile %q: re-decode after parameter substitution: %w: %w",
			profile.Metadata.Name, errdefs.ErrProfileInvalid, err,
		)
	}
	return &out, nil
}

// validateParameters runs the at-load-time parameter checks: every
// declared name must be unique and lexically valid; every `${KEY}` reference
// in scalar values must point at a declared parameter. Called from loadFile
// so any caller (Load, LoadResolved, the List fallback) gets the same
// rejection rather than re-implementing the check.
func validateParameters(profile *v1beta1.CellProfileDoc, node *yaml.Node, path string) error {
	declared := make(map[string]struct{}, len(profile.Spec.Parameters))
	for _, p := range profile.Spec.Parameters {
		if !paramNameRE.MatchString(p.Name) {
			return fmt.Errorf(
				"profile %q: parameter name %q must match [A-Za-z_][A-Za-z0-9_]*: %w",
				path, p.Name, errdefs.ErrProfileInvalid,
			)
		}
		if _, dup := declared[p.Name]; dup {
			return fmt.Errorf(
				"profile %q: parameter %q is declared twice in spec.parameters[]: %w",
				path, p.Name, errdefs.ErrProfileInvalid,
			)
		}
		declared[p.Name] = struct{}{}
	}

	// Skip ref scanning under the spec.parameters block itself — `default`
	// values legitimately contain literal `${...}`-shaped text on profiles
	// that proxy substitutions through to a downstream tool. The reference
	// check is meant to catch typos in the cell body, not double-quote the
	// declarations.
	for _, ref := range referencedParamsInBody(node) {
		if _, ok := declared[ref]; !ok {
			return fmt.Errorf(
				"profile %q: references undeclared parameter %q "+
					"(declare it in spec.parameters[] to fix): %w",
				path, ref, errdefs.ErrProfileInvalid,
			)
		}
	}
	return nil
}

// referencedParamsInBody returns the unique set of `${KEY}` references in
// the profile's scalar values, EXCLUDING the spec.parameters declaration
// block. Document order is preserved so error messages name the first
// offender the operator sees when they scroll through the file.
func referencedParamsInBody(node *yaml.Node) []string {
	body := bodyForRefScan(node)
	if body == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var out []string
	walkScalars(body, func(s *yaml.Node) {
		for _, m := range paramRefRE.FindAllStringSubmatch(s.Value, -1) {
			k := m[1]
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
	})
	return out
}

// bodyForRefScan returns a yaml.Node that mirrors the input but with the
// spec.parameters subtree spliced out. We intentionally do NOT mutate the
// input — the caller still needs the original tree for substitution against
// the entire profile (parameters block included, so any `${OTHER}` chain
// declared inside a default still resolves).
func bodyForRefScan(node *yaml.Node) *yaml.Node {
	clone := cloneNode(node)
	if clone == nil {
		return nil
	}
	root := clone
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return clone
	}
	specNode := mappingValue(root, "spec")
	if specNode == nil || specNode.Kind != yaml.MappingNode {
		return clone
	}
	// Drop the parameters mapping entry from the spec mapping content.
	out := make([]*yaml.Node, 0, len(specNode.Content))
	for i := 0; i+1 < len(specNode.Content); i += 2 {
		key := specNode.Content[i]
		if key.Kind == yaml.ScalarNode && key.Value == "parameters" {
			continue
		}
		out = append(out, specNode.Content[i], specNode.Content[i+1])
	}
	specNode.Content = out
	return clone
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k := m.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// substituteScalars rewrites every `${KEY}` occurrence inside scalar values
// with values[KEY]. Mapping keys are not rewritten (per the proposal). Keys
// not present in values are left as literal `${KEY}` — callers must validate
// references first.
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

// cloneNode returns a deep copy of n so substitutions don't mutate the input.
// yaml.v3 doesn't expose a clone primitive — round-tripping through bytes is
// the simplest faithful copy and stays within the bounds of a profile-sized
// document. On marshal/unmarshal failure we fall back to the input pointer:
// the caller still gets a working tree, just one shared with the source.
func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	raw, err := yaml.Marshal(n)
	if err != nil {
		return n
	}
	var out yaml.Node
	if uerr := yaml.Unmarshal(raw, &out); uerr != nil {
		return n
	}
	return &out
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
				arg, errdefs.ErrProfileInvalid,
			)
		}
		if !paramNameRE.MatchString(k) {
			return nil, fmt.Errorf(
				"invalid --param %q: KEY must match [A-Za-z_][A-Za-z0-9_]*: %w",
				arg, errdefs.ErrProfileInvalid,
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
				path, i+1, line, errdefs.ErrProfileInvalid,
			)
		}
		if !paramNameRE.MatchString(k) {
			return nil, fmt.Errorf(
				"%s:%d: invalid KEY %q: must match [A-Za-z_][A-Za-z0-9_]*: %w",
				path, i+1, k, errdefs.ErrProfileInvalid,
			)
		}
		out[k] = v
	}
	return out, nil
}

// MergeParams returns the union of base and overrides, with overrides
// winning on duplicate keys. Used by `kuke run` to layer --param on top of
// --param-file (CLI flag is later-binding than the file).
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
