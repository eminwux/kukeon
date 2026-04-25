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

package ctr_test

import (
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
)

func TestSbshCachePath_KeysOffImageArchNotHostArch(t *testing.T) {
	// The cache must lay out by *image* arch, not host arch — that's the
	// regression #57 calls out: a cross-arch image running under emulation
	// would otherwise pick a binary the in-container interpreter can't run.
	cases := []struct {
		runPath string
		arch    string
		want    string
	}{
		{"/opt/kukeon", "amd64", "/opt/kukeon/cache/sbsh/amd64/sbsh"},
		{"/opt/kukeon", "arm64", "/opt/kukeon/cache/sbsh/arm64/sbsh"},
		{"/var/lib/kukeon", "amd64", "/var/lib/kukeon/cache/sbsh/amd64/sbsh"},
	}
	for _, tc := range cases {
		got := ctr.SbshCachePath(tc.runPath, tc.arch)
		if got != tc.want {
			t.Errorf("SbshCachePath(%q, %q) = %q, want %q", tc.runPath, tc.arch, got, tc.want)
		}
	}
}
