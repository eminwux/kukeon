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

package run

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseEnvArgs_HappyPath pins the canonical KEY=VALUE flow: a list of
// distinct keys passes through verbatim in declaration order. Order matters
// because the runner's merge appends runtime entries after spec entries, and
// stable output keeps OCI process env reproducible across re-runs with the
// same flag set.
func TestParseEnvArgs_HappyPath(t *testing.T) {
	in := []string{"LABEL=bug", "PRIORITY=A", "REGION=us-east-1"}
	got, err := parseEnvArgs(in)
	if err != nil {
		t.Fatalf("parseEnvArgs: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("parseEnvArgs(%v) = %v, want %v (declaration order preserved)", in, got, in)
	}
}

// TestParseEnvArgs_EmptyInput is the no-flag case — `kuke run --from-config <cfg>` with
// no --env flags must produce nil so cellDoc.Spec.RuntimeEnv stays its
// zero value and the divergent-spec check sees an unchanged spec.
func TestParseEnvArgs_EmptyInput(t *testing.T) {
	got, err := parseEnvArgs(nil)
	if err != nil {
		t.Fatalf("parseEnvArgs(nil): %v", err)
	}
	if got != nil {
		t.Errorf("parseEnvArgs(nil) = %v, want nil", got)
	}

	got, err = parseEnvArgs([]string{})
	if err != nil {
		t.Fatalf("parseEnvArgs([]): %v", err)
	}
	if got != nil {
		t.Errorf("parseEnvArgs([]) = %v, want nil", got)
	}
}

// TestParseEnvArgs_EmptyValueAccepted pins the AC: `--env KEY=` is allowed
// — the operator may want to unset a spec env entry by overriding it with
// an empty value (POSIX env conventions treat `KEY=` and unset KEY as
// distinct in some shells; preserve the operator's explicit intent).
func TestParseEnvArgs_EmptyValueAccepted(t *testing.T) {
	got, err := parseEnvArgs([]string{"LABEL="})
	if err != nil {
		t.Fatalf("parseEnvArgs([LABEL=]): %v", err)
	}
	want := []string{"LABEL="}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnvArgs([LABEL=]) = %v, want %v", got, want)
	}
}

// TestParseEnvArgs_MissingEqualsRejected pins the AC: an entry without `=`
// is rejected with a clear pointer to the original input. The error
// message names the offending input so the operator sees exactly what was
// supplied, rather than a generic "format invalid".
func TestParseEnvArgs_MissingEqualsRejected(t *testing.T) {
	_, err := parseEnvArgs([]string{"LABELbug"})
	if err == nil {
		t.Fatalf("parseEnvArgs([LABELbug]) = nil, want error")
	}
	if !strings.Contains(err.Error(), "--env requires KEY=VALUE") {
		t.Errorf("error %q, want substring %q", err.Error(), "--env requires KEY=VALUE")
	}
	if !strings.Contains(err.Error(), `"LABELbug"`) {
		t.Errorf("error %q, want substring %q (preserves operator input)", err.Error(), `"LABELbug"`)
	}
}

// TestParseEnvArgs_EmptyKeyRejected covers the edge of `=VALUE` and `=`
// inputs: the AC says empty value is allowed but the KEY half must be
// non-empty for the entry to land in a meaningful env list (containerd's
// OCI process env has no concept of a nameless entry).
func TestParseEnvArgs_EmptyKeyRejected(t *testing.T) {
	for _, in := range []string{"=VAL", "=", "  =VAL"} {
		t.Run(in, func(t *testing.T) {
			_, err := parseEnvArgs([]string{in})
			if err == nil {
				t.Fatalf("parseEnvArgs([%q]) = nil, want error", in)
			}
			if !strings.Contains(err.Error(), "--env KEY must be non-empty") {
				t.Errorf("error %q, want substring %q", err.Error(), "--env KEY must be non-empty")
			}
		})
	}
}

// TestParseEnvArgs_DuplicateKeyDifferentValueRejected pins the AC's
// "don't silently take last wins; explicit is better" rule. The operator
// has to pick one — surfacing both supplied values plus the conflicting
// key name makes the fix obvious.
func TestParseEnvArgs_DuplicateKeyDifferentValueRejected(t *testing.T) {
	_, err := parseEnvArgs([]string{"LABEL=bug", "LABEL=enh"})
	if err == nil {
		t.Fatalf("parseEnvArgs([LABEL=bug, LABEL=enh]) = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "LABEL") {
		t.Errorf("error %q, want substring LABEL (key naming)", msg)
	}
	if !strings.Contains(msg, "bug") || !strings.Contains(msg, "enh") {
		t.Errorf("error %q, want both conflicting values surfaced", msg)
	}
	if !strings.Contains(msg, "pick one") {
		t.Errorf("error %q, want 'pick one' resolution hint", msg)
	}
}

// TestParseEnvArgs_DuplicateKeySameValueDeduped covers the lenient half of
// the dedup rule: same KEY same VALUE is harmless duplication (cron lines
// can drift over time and end up with the same env twice without changing
// behavior). Dedup to one entry rather than rejecting — there's nothing
// to clarify.
func TestParseEnvArgs_DuplicateKeySameValueDeduped(t *testing.T) {
	got, err := parseEnvArgs([]string{"LABEL=bug", "LABEL=bug"})
	if err != nil {
		t.Fatalf("parseEnvArgs: %v", err)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnvArgs(LABEL=bug ×2) = %v, want %v (deduplicated)", got, want)
	}
}

// TestParseEnvArgs_ValueWithEquals pins the strings.Cut semantics: only
// the FIRST `=` separates KEY from VALUE. A value that itself contains
// `=` (e.g. base64-padded tokens, URLs with query strings, OAuth client
// secrets) must round-trip verbatim.
func TestParseEnvArgs_ValueWithEquals(t *testing.T) {
	got, err := parseEnvArgs([]string{"TOKEN=abc=def=ghi"})
	if err != nil {
		t.Fatalf("parseEnvArgs: %v", err)
	}
	want := []string{"TOKEN=abc=def=ghi"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnvArgs = %v, want %v (only first = separates)", got, want)
	}
}

// TestParseEnvArgs_KeyWhitespaceTrimmed pins the trim-on-key behaviour:
// a stray space around the KEY (operator typo, copy-paste from a config
// file) is silently trimmed rather than rejected, but a key that is
// pure whitespace falls through to the empty-key rejection above. The
// value half is preserved verbatim (spaces inside an env value are
// meaningful — think `MESSAGE="hello world"` styles).
func TestParseEnvArgs_KeyWhitespaceTrimmed(t *testing.T) {
	got, err := parseEnvArgs([]string{"  LABEL =bug"})
	if err != nil {
		t.Fatalf("parseEnvArgs: %v", err)
	}
	want := []string{"LABEL=bug"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseEnvArgs = %v, want %v (KEY trimmed)", got, want)
	}
}
