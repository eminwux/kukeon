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

package consts_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
)

// withRuntime saves the package-level overridable values, runs fn with the
// supplied (suffix, cgroupRoot) applied via ConfigureRuntime, then restores
// the originals. Tests that exercise non-default runtime configuration must
// not call t.Parallel — the package vars are shared process state. The
// linter suppression in the cleanup mirrors what ConfigureRuntime itself
// does in production; the test save/restore reuses that contract.
func withRuntime(t *testing.T, suffix, cgroupRoot string, fn func(t *testing.T)) {
	t.Helper()
	prevSuffix := consts.RealmNamespaceSuffix
	prevRoot := consts.KukeonCgroupRoot
	t.Cleanup(func() {
		consts.RealmNamespaceSuffix = prevSuffix //nolint:reassign // restore runtime-overridable global
		consts.KukeonCgroupRoot = prevRoot       //nolint:reassign // restore runtime-overridable global
	})
	if err := consts.ConfigureRuntime(suffix, cgroupRoot); err != nil {
		t.Fatalf("ConfigureRuntime(%q, %q): %v", suffix, cgroupRoot, err)
	}
	fn(t)
}

func TestRealmNamespaceRoundTripDefault(t *testing.T) {
	const realm = "default"
	ns := consts.RealmNamespace(realm)
	if want := "default.kukeon.io"; ns != want {
		t.Fatalf("RealmNamespace(%q): got %q, want %q", realm, ns, want)
	}
	if got := consts.RealmFromNamespace(ns); got != realm {
		t.Errorf("RealmFromNamespace(%q): got %q, want %q", ns, got, realm)
	}
	if !consts.IsKukeonNamespace(ns) {
		t.Errorf("IsKukeonNamespace(%q) = false, want true", ns)
	}
}

func TestRealmNamespaceRoundTripCustomSuffix(t *testing.T) {
	withRuntime(t, "dev.kukeon.io", "/kukeon-dev", func(t *testing.T) {
		const realm = "default"
		ns := consts.RealmNamespace(realm)
		if want := "default.dev.kukeon.io"; ns != want {
			t.Fatalf("RealmNamespace(%q) under dev suffix: got %q, want %q",
				realm, ns, want)
		}
		if got := consts.RealmFromNamespace(ns); got != realm {
			t.Errorf("RealmFromNamespace(%q) under dev suffix: got %q, want %q",
				ns, got, realm)
		}
		if !consts.IsKukeonNamespace(ns) {
			t.Errorf("IsKukeonNamespace(%q) under dev suffix: got false, want true", ns)
		}
	})
}

func TestRealmNamespaceRoundTripPreservesKukeSystem(t *testing.T) {
	withRuntime(t, "dev.kukeon.io", "/kukeon-dev", func(t *testing.T) {
		ns := consts.RealmNamespace(consts.KukeSystemRealmName)
		if got := consts.RealmFromNamespace(ns); got != consts.KukeSystemRealmName {
			t.Errorf("RealmFromNamespace(%q): got %q, want %q",
				ns, got, consts.KukeSystemRealmName)
		}
	})
}

func TestIsKukeonNamespaceRejectsAnotherInstance(t *testing.T) {
	// With this instance configured for "kukeon.io", a namespace that
	// belongs to a parallel instance ("dev.kukeon.io") must not be
	// claimed: stripping the wrong suffix would have RealmFromNamespace
	// hand back a bogus realm name like "default.dev".
	withRuntime(t, "kukeon.io", "/kukeon", func(t *testing.T) {
		const otherInstanceNamespace = "default.dev.kukeon.io"
		// The other-instance namespace ends in ".kukeon.io", so the
		// suffix-only check accepts it (intended — the helper is a
		// strip-the-trailing-dot-domain test, not full instance
		// disambiguation, which is the recommended-follow-up
		// "Cross-instance refusal at startup"). Round-tripping through
		// RealmFromNamespace returns "default.dev", flagging the cross-
		// instance overlap so a higher-level caller (uninstall, list)
		// can reject it.
		if got := consts.RealmFromNamespace(otherInstanceNamespace); got == "default" {
			t.Errorf("RealmFromNamespace(%q) collapsed to bare realm %q under "+
				"plain suffix; it should still surface the dev-instance prefix",
				otherInstanceNamespace, got)
		}

		// And the inverse direction: a ns with a suffix that does NOT
		// end in this instance's must be rejected.
		const otherSuffixNamespace = "default.example.com"
		if consts.IsKukeonNamespace(otherSuffixNamespace) {
			t.Errorf("IsKukeonNamespace(%q) = true; want false (different suffix)",
				otherSuffixNamespace)
		}
		if got := consts.RealmFromNamespace(otherSuffixNamespace); got != "" {
			t.Errorf("RealmFromNamespace(%q) = %q; want empty (different suffix)",
				otherSuffixNamespace, got)
		}
	})
}

func TestIsKukeonNamespaceRejectsForeignSuffixUnderCustom(t *testing.T) {
	// Symmetric to the default-suffix test: when this instance is on a
	// non-default suffix, the default-instance namespace must be rejected.
	withRuntime(t, "dev.kukeon.io", "/kukeon-dev", func(t *testing.T) {
		const defaultInstanceNamespace = "default.kukeon.io"
		if consts.IsKukeonNamespace(defaultInstanceNamespace) {
			t.Errorf("IsKukeonNamespace(%q) under dev suffix: got true, want false",
				defaultInstanceNamespace)
		}
		if got := consts.RealmFromNamespace(defaultInstanceNamespace); got != "" {
			t.Errorf("RealmFromNamespace(%q) under dev suffix: got %q, want empty",
				defaultInstanceNamespace, got)
		}
	})
}

func TestConfigureRuntimeRejectsInvalidSuffix(t *testing.T) {
	cases := []struct {
		name   string
		suffix string
	}{
		{"empty", ""},
		{"leading dot", ".kukeon.io"},
		{"trailing dot", "kukeon.io."},
		{"slash", "kukeon/io"},
		{"whitespace", "kukeon io"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := consts.ConfigureRuntime(tc.suffix, "/kukeon")
			if err == nil {
				t.Fatalf("ConfigureRuntime(%q, /kukeon): err = nil, want error", tc.suffix)
			}
			if !errors.Is(err, errdefs.ErrServerConfigurationInvalid) {
				t.Errorf("ConfigureRuntime(%q, /kukeon) error: got %v, want wrapped %v",
					tc.suffix, err, errdefs.ErrServerConfigurationInvalid)
			}
		})
	}
}

func TestConfigureRuntimeRejectsInvalidCgroupRoot(t *testing.T) {
	cases := []struct {
		name string
		root string
	}{
		{"empty", ""},
		{"relative", "kukeon"},
		{"root only", "/"},
		{"trailing slashes only", "////"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := consts.ConfigureRuntime("kukeon.io", tc.root)
			if err == nil {
				t.Fatalf("ConfigureRuntime(kukeon.io, %q): err = nil, want error", tc.root)
			}
			if !errors.Is(err, errdefs.ErrServerConfigurationInvalid) {
				t.Errorf("ConfigureRuntime(kukeon.io, %q) error: got %v, want wrapped %v",
					tc.root, err, errdefs.ErrServerConfigurationInvalid)
			}
		})
	}
}

func TestConfigureRuntimeAccepted(t *testing.T) {
	withRuntime(t, "dev.kukeon.io", "/kukeon-dev/", func(t *testing.T) {
		if got, want := consts.RealmNamespaceSuffix, ".dev.kukeon.io"; got != want {
			t.Errorf("RealmNamespaceSuffix = %q, want %q", got, want)
		}
		// Trailing slash trimmed.
		if got, want := consts.KukeonCgroupRoot, "/kukeon-dev"; got != want {
			t.Errorf("KukeonCgroupRoot = %q, want %q", got, want)
		}
	})
}
