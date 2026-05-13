//go:build !integration

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

package controller

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseMountsUnder_FiltersAndSortsDeepestFirst pins the enumerator's two
// guarantees: only mountpoints under the requested root are returned, and
// the result is ordered deepest-first so callers can drain nested mounts
// before their parent without needing to reason about hierarchy.
func TestParseMountsUnder_FiltersAndSortsDeepestFirst(t *testing.T) {
	// Synthetic /proc/self/mounts content. Fields per procfs(5):
	// source target fstype options dump pass.
	const procMounts = `proc /proc proc rw 0 0
sysfs /sys sysfs rw 0 0
/dev/sda1 /run/kukeon/tty ext4 rw 0 0
/dev/sda1 /run/kukeon/tty/socket ext4 rw 0 0
/dev/sda1 /opt/kukeon/default/space ext4 rw 0 0
/dev/sda1 /run/something-else ext4 rw 0 0
/dev/sda1 /run/kukeon ext4 rw 0 0
tmpfs /tmp tmpfs rw 0 0
`

	got, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("parseMountsUnder returned err=%v", err)
	}
	// Deepest first: /run/kukeon/tty/socket (4 slashes) before /run/kukeon/tty
	// and /run/kukeon (3 and 2 slashes). /run/something-else has the same
	// /run/-prefix length but is NOT under /run/kukeon so it must be absent.
	want := []string{"/run/kukeon/tty/socket", "/run/kukeon/tty", "/run/kukeon"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountsUnder = %v, want %v", got, want)
	}
}

// TestParseMountsUnder_TrailingSlashRoot pins the trailing-slash invariant:
// "/run/kukeon" and "/run/kukeon/" must yield the same enumeration, so a
// caller passing the rendered config string (which may or may not carry a
// trailing slash depending on the config source) does not change behavior.
func TestParseMountsUnder_TrailingSlashRoot(t *testing.T) {
	const procMounts = `/dev/sda1 /run/kukeon/tty ext4 rw 0 0
`
	noSlash, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("no-slash: %v", err)
	}
	withSlash, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon/")
	if err != nil {
		t.Fatalf("with-slash: %v", err)
	}
	if !reflect.DeepEqual(noSlash, withSlash) {
		t.Fatalf("trailing slash changed result: no-slash=%v with-slash=%v", noSlash, withSlash)
	}
}

// TestParseMountsUnder_PrefixMatchNotSubstring guards against an accidental
// strings.HasPrefix match that would catch "/run/kukeon2/tty" as living
// under "/run/kukeon". The matcher must enforce a `/` separator between the
// root and the target's tail.
func TestParseMountsUnder_PrefixMatchNotSubstring(t *testing.T) {
	const procMounts = `/dev/sda1 /run/kukeon2/tty ext4 rw 0 0
/dev/sda1 /run/kukeon/tty ext4 rw 0 0
`
	got, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("parseMountsUnder: %v", err)
	}
	want := []string{"/run/kukeon/tty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountsUnder = %v, want %v (must reject /run/kukeon2/...)", got, want)
	}
}

// TestParseMountsUnder_RefusesEmptyOrSlashRoot pins the safety floor: an
// empty or bare-slash root would match every mount on the host. Refusing
// keeps a stray empty option from turning uninstall into a host-wide sweep.
func TestParseMountsUnder_RefusesEmptyOrSlashRoot(t *testing.T) {
	for _, root := range []string{"", "/", "//"} {
		_, err := parseMountsUnder(strings.NewReader(""), root)
		if err == nil {
			t.Errorf("parseMountsUnder(_, %q) = nil err, want refusal", root)
		}
	}
}

// TestParseMountsUnder_DecodesOctalEscapes pins the procfs escape decoding:
// space in a mount target is encoded as `\040`, tab as `\011`, backslash as
// `\134`. Without decoding, a mountpoint like `/run/kukeon/with space` would
// arrive as `/run/kukeon/with\040space` and fall through HasPrefix because
// the literal backslash never appears in the configured root.
func TestParseMountsUnder_DecodesOctalEscapes(t *testing.T) {
	const procMounts = `/dev/sda1 /run/kukeon/with\040space ext4 rw 0 0
`
	got, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("parseMountsUnder: %v", err)
	}
	want := []string{"/run/kukeon/with space"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountsUnder = %v, want %v (procfs octal escape must decode)", got, want)
	}
}

// TestDecodeMountPath_TableDriven covers the helper directly for the round-
// trip cases the enumerator depends on: plain ASCII, all the common
// procfs-escaped whitespace bytes, and a malformed escape that must fall
// through verbatim.
func TestDecodeMountPath_TableDriven(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"/run/kukeon/tty", "/run/kukeon/tty"},
		{`/run/kukeon/with\040space`, "/run/kukeon/with space"},
		{`/run/kukeon/with\011tab`, "/run/kukeon/with\ttab"},
		{`/run/kukeon/back\134slash`, "/run/kukeon/back\\slash"},
		// Malformed (non-octal digit after the backslash) — fall through.
		{`/run/kukeon/raw\x40no`, `/run/kukeon/raw\x40no`},
	} {
		got := decodeMountPath(tc.in)
		if got != tc.want {
			t.Errorf("decodeMountPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestParseMountsUnder_EmptyLinesAreSkipped pins robustness against the
// trailing empty line a writer would leave after the final \n.
func TestParseMountsUnder_EmptyLinesAreSkipped(t *testing.T) {
	const procMounts = `/dev/sda1 /run/kukeon/tty ext4 rw 0 0

`
	got, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("parseMountsUnder: %v", err)
	}
	want := []string{"/run/kukeon/tty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountsUnder = %v, want %v", got, want)
	}
}

// TestParseMountsUnder_MalformedLinesAreSkipped guards against a single-
// column garbled line (procfs format violation, "ENOMEM eaten" log
// fragments) from short-circuiting the scan and dropping later valid
// mountpoints.
func TestParseMountsUnder_MalformedLinesAreSkipped(t *testing.T) {
	const procMounts = `only-one-field
/dev/sda1 /run/kukeon/tty ext4 rw 0 0
`
	got, err := parseMountsUnder(strings.NewReader(procMounts), "/run/kukeon")
	if err != nil {
		t.Fatalf("parseMountsUnder: %v", err)
	}
	want := []string{"/run/kukeon/tty"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountsUnder = %v, want %v (malformed line must not abort scan)", got, want)
	}
}
