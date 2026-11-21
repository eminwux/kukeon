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

package config

import (
	"path/filepath"
	"testing"
)

// TestDefaultPathsUnknownHome pins the unknown-home path of the default-path
// helpers: with $HOME unset they fall back to a relative "tmp" base and
// HomeDirKnown reports false, which is the signal writers gate on so nothing
// lands under the working directory (#1550).
func TestDefaultPathsUnknownHome(t *testing.T) {
	t.Setenv("HOME", "")

	if HomeDirKnown() {
		t.Fatal("HomeDirKnown() = true with HOME unset, want false")
	}
	if got, want := DefaultKukeDir(), filepath.Join("tmp", ".kuke"); got != want {
		t.Errorf("DefaultKukeDir() = %q, want %q", got, want)
	}
	if got, want := DefaultProfilesFile(), filepath.Join("tmp", ".kukeon", "profiles.yaml"); got != want {
		t.Errorf("DefaultProfilesFile() = %q, want %q", got, want)
	}
	if got, want := DefaultClientConfigurationFile(), filepath.Join("tmp", ".kuke", "kuke.yaml"); got != want {
		t.Errorf("DefaultClientConfigurationFile() = %q, want %q", got, want)
	}
}

// TestDefaultPathsKnownHome is the counterpart: a set $HOME roots every
// default path under it and HomeDirKnown reports true.
func TestDefaultPathsKnownHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if !HomeDirKnown() {
		t.Fatal("HomeDirKnown() = false with HOME set, want true")
	}
	if got, want := DefaultKukeDir(), filepath.Join(home, ".kuke"); got != want {
		t.Errorf("DefaultKukeDir() = %q, want %q", got, want)
	}
	if got, want := DefaultProfilesFile(), filepath.Join(home, ".kukeon", "profiles.yaml"); got != want {
		t.Errorf("DefaultProfilesFile() = %q, want %q", got, want)
	}
}
