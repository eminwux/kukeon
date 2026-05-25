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

package shared

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// LabelSelectorFlagName is the canonical long flag name for the
// `-l`/`--selector` label query flag wired onto every `kuke get <kind>`
// subcommand that supports label filtering.
const LabelSelectorFlagName = "selector"

// labelSelectorFlagUsage is the shared help text for `-l`/`--selector`.
// One example covers equality, inequality, existence, and AND-comma so
// the AC's "documents the flag with one example" requirement is met by
// every verb that calls RegisterLabelSelectorFlag.
const labelSelectorFlagUsage = "Selector (label query) to filter on, " +
	"supports '=', '==', '!=', existence ('key'), absence ('!key'), " +
	"and comma-separated AND (e.g. 'env=prod,tier!=db' or 'env,!debug')"

// LabelSelector is a parsed kubectl-style label selector. The zero value
// (nil receiver or no requirements) matches every label set, including
// nil maps.
type LabelSelector struct {
	requirements []labelRequirement
}

type labelOp int

const (
	labelOpEquals labelOp = iota
	labelOpNotEquals
	labelOpExists
	labelOpDoesNotExist
)

type labelRequirement struct {
	key   string
	op    labelOp
	value string
}

// RegisterLabelSelectorFlag adds the standard `-l`/`--selector` flag to
// cmd. Keeping the registration in one place ensures every `kuke get
// <kind>` verb advertises the same long-name, short-name, and help text.
func RegisterLabelSelectorFlag(cmd *cobra.Command) {
	cmd.Flags().StringP(LabelSelectorFlagName, "l", "", labelSelectorFlagUsage)
}

// ParseLabelSelectorFlag reads the `-l`/`--selector` flag from cmd and
// returns the parsed selector. An unset or blank flag yields a non-nil
// empty selector (matches every label set) so callers can pass it
// through Matches unconditionally. Malformed selectors return a parse
// error before any controller call — see the issue AC's "fail fast"
// requirement.
func ParseLabelSelectorFlag(cmd *cobra.Command) (*LabelSelector, error) {
	if cmd == nil {
		return &LabelSelector{}, nil
	}
	raw, _ := cmd.Flags().GetString(LabelSelectorFlagName)
	return ParseLabelSelector(raw)
}

// ParseLabelSelector parses a kubectl-style label selector string into a
// LabelSelector. Supported operators per clause:
//
//   - `key=value`   — label `key` equals `value`
//   - `key==value`  — kubectl alias for `=`
//   - `key!=value`  — label `key` not equal to `value` (or absent)
//   - `key`         — label `key` exists (any value)
//   - `!key`        — label `key` does not exist
//
// Clauses are comma-separated and logically ANDed. Whitespace around
// commas and operands is trimmed. The empty string parses to a selector
// that matches every label set.
//
// Set-based operators (`in`, `notin`) are explicitly out of scope per
// issue #614 — feed them in as a follow-up if needed.
func ParseLabelSelector(s string) (*LabelSelector, error) {
	sel := &LabelSelector{}
	s = strings.TrimSpace(s)
	if s == "" {
		return sel, nil
	}
	for _, part := range strings.Split(s, ",") {
		clause := strings.TrimSpace(part)
		if clause == "" {
			return nil, fmt.Errorf("invalid selector %q: empty clause", s)
		}
		req, err := parseLabelRequirement(clause)
		if err != nil {
			return nil, err
		}
		sel.requirements = append(sel.requirements, req)
	}
	return sel, nil
}

func parseLabelRequirement(clause string) (labelRequirement, error) {
	// `!=` is matched before `=` because `=` is a substring of `!=`.
	if i := strings.Index(clause, "!="); i >= 0 {
		key := strings.TrimSpace(clause[:i])
		val := strings.TrimSpace(clause[i+2:])
		if key == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector clause %q: empty key", clause)
		}
		if val == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector clause %q: empty value after '!='", clause)
		}
		return labelRequirement{key: key, op: labelOpNotEquals, value: val}, nil
	}
	if i := strings.Index(clause, "="); i >= 0 {
		// Accept `==` as a kubectl-style alias for `=` by consuming an
		// optional second `=` immediately after the first.
		rhs := i + 1
		if rhs < len(clause) && clause[rhs] == '=' {
			rhs++
		}
		key := strings.TrimSpace(clause[:i])
		val := strings.TrimSpace(clause[rhs:])
		if key == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector clause %q: empty key", clause)
		}
		if val == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector clause %q: empty value after '='", clause)
		}
		return labelRequirement{key: key, op: labelOpEquals, value: val}, nil
	}
	// No operator — existence (`key`) or absence (`!key`) predicate.
	if strings.HasPrefix(clause, "!") {
		key := strings.TrimSpace(clause[1:])
		if key == "" {
			return labelRequirement{}, fmt.Errorf("invalid selector clause %q: empty key after '!'", clause)
		}
		return labelRequirement{key: key, op: labelOpDoesNotExist}, nil
	}
	return labelRequirement{key: clause, op: labelOpExists}, nil
}

// Matches reports whether every requirement in the selector is satisfied
// by labels. A nil selector or one with zero requirements matches every
// label set (including a nil map).
func (s *LabelSelector) Matches(labels map[string]string) bool {
	if s == nil {
		return true
	}
	for _, r := range s.requirements {
		if !r.matches(labels) {
			return false
		}
	}
	return true
}

// Empty reports whether the selector carries zero requirements — i.e.
// it matches every label set. Useful for "did the user actually pass a
// selector" gates without inspecting the raw flag value.
func (s *LabelSelector) Empty() bool {
	return s == nil || len(s.requirements) == 0
}

func (r labelRequirement) matches(labels map[string]string) bool {
	v, ok := labels[r.key]
	switch r.op {
	case labelOpEquals:
		return ok && v == r.value
	case labelOpNotEquals:
		return !ok || v != r.value
	case labelOpExists:
		return ok
	case labelOpDoesNotExist:
		return !ok
	}
	return false
}
