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

package main

import "testing"

func TestResolveBuildRoot(t *testing.T) {
	cases := []struct {
		name      string
		root      string
		explicit  bool
		namespace string
		want      string
	}{
		{
			name:      "default root scoped per namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.kukeon.io",
			want:      "/var/lib/kukebuild/default.kukeon.io",
		},
		{
			name:      "default root scoped per different namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "kuke-system.kukeon.io",
			want:      "/var/lib/kukebuild/kuke-system.kukeon.io",
		},
		{
			name:      "default root scoped per custom-suffix namespace",
			root:      defaultBuildRoot,
			explicit:  false,
			namespace: "default.dev.kukeon.io",
			want:      "/var/lib/kukebuild/default.dev.kukeon.io",
		},
		{
			name:      "explicit root honored verbatim",
			root:      "/tmp/freshroot",
			explicit:  true,
			namespace: "default.kukeon.io",
			want:      "/tmp/freshroot",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBuildRoot(tc.root, tc.explicit, tc.namespace); got != tc.want {
				t.Errorf("resolveBuildRoot(%q, %v, %q) = %q, want %q",
					tc.root, tc.explicit, tc.namespace, got, tc.want)
			}
		})
	}
}

// Two consecutive default-root builds into different namespaces must resolve to
// distinct BuildKit state roots — the isolation that prevents the cross-
// namespace cache reuse in issue #663.
func TestResolveBuildRootIsolatesNamespaces(t *testing.T) {
	first := resolveBuildRoot(defaultBuildRoot, false, "default.kukeon.io")
	second := resolveBuildRoot(defaultBuildRoot, false, "kuke-system.kukeon.io")
	if first == second {
		t.Errorf("default roots for distinct namespaces collide: %q == %q", first, second)
	}
}
