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

package config

import "testing"

func TestKukeonSourcePinsTheBuildRevision(t *testing.T) {
	const sha1 = "8eb9583e0123456789abcdef0123456789abcdef"
	sha256 := sha1 + "0123456789abcdef01234567"
	for _, tc := range []struct {
		name     string
		version  string
		commit   string
		modified bool
		want     Source
	}{
		{"release tag", "v0.10.0", sha1, false, Source{Tag: "v0.10.0"}},
		{"prerelease tag", "v0.11.0-rc.1", "", false, Source{Tag: "v0.11.0-rc.1"}},
		{"release tag without VCS stamp", "v0.10.0", "", false, Source{Tag: "v0.10.0"}},
		{"describe after tag", "v0.10.0-77-g8eb9583e", sha1, false, Source{Commit: sha1}},
		{"sha256 repository", "v0.10.0-77-g8eb9583e", sha256, false, Source{Commit: sha256}},
		{"unversioned go build", "0.1.0", sha1, false, Source{Commit: sha1}},
		{"dirty release tag", "v0.10.0-dirty", sha1, true, Source{Branch: "main"}},
		{"dirty describe", "v0.10.0-77-g8eb9583e-dirty", sha1, true, Source{Branch: "main"}},
		{"modified tree", "0.1.0", sha1, true, Source{Branch: "main"}},
		{"short commit", "8eb9583e", "8eb9583e", false, Source{Branch: "main"}},
		{"no build information", "", "", false, Source{Branch: "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.want.Name, tc.want.URL = KukeonSourceName, KukeonSourceURL
			if got := kukeonSource(tc.version, tc.commit, tc.modified); got != tc.want {
				t.Errorf("kukeonSource(%q, %q, %v) = %+v, want %+v", tc.version, tc.commit, tc.modified, got, tc.want)
			}
		})
	}
}

// TestMajordomoReferencesTheKukeonSource: the kuke team names the stored
// kukeon Source without an address or ref (#1643), so it reads the revision
// the stored Source is pinned to; KukeonSourceSpec pins that one to the build.
func TestMajordomoReferencesTheKukeonSource(t *testing.T) {
	source := Majordomo().Spec.Sources[0]
	if want := (Source{Name: KukeonSourceName, ClonePath: "/home/user/kukeon-src"}); source != want {
		t.Errorf("majordomo source = %+v, want %+v", source, want)
	}
	if got := MajordomoSource(); got != KukeonSource() {
		t.Errorf("MajordomoSource() = %+v, want the pinned %+v", got, KukeonSource())
	}
	pinned, spec := KukeonSource(), KukeonSourceSpec()
	if spec.URL != pinned.URL || spec.Branch != pinned.Branch || spec.Tag != pinned.Tag ||
		spec.Commit != pinned.Commit {
		t.Errorf("KukeonSourceSpec() = %+v, want the pinned %+v", spec, pinned)
	}
	if err := ValidateSource(KukeonSourceName, spec); err != nil {
		t.Errorf("the pinned kukeon Source must validate: %v", err)
	}
}

func TestSourceRevision(t *testing.T) {
	for source, want := range map[Source]string{
		{Tag: "v1.0.0"}:  "tag v1.0.0",
		{Commit: "abc"}:  "commit abc",
		{Branch: "main"}: "branch main",
		{URL: "x"}:       "default branch",
	} {
		if got := source.Revision(); got != want {
			t.Errorf("%+v.Revision() = %q, want %q", source, got, want)
		}
	}
}
