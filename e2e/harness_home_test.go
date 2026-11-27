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

package e2e_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/clientconfig"
)

// e2eClientHome is the suite-scoped HOME the kuke and kukeond children get
// in place of the one the test process inherited (issue #1765). Without it
// every invocation that has no per-run kuke.yaml reads, and on first run
// writes, the developer's ~/.kuke/kuke.yaml: a stale file there (a renamed
// key, the wrong kind) failed most of the suite on that host only. TestMain
// creates it; it holds the default ClientConfiguration kuke writes on first
// run, so parallel first invocations do not race to create it.
//
//nolint:gochecknoglobals // suite-wide directory shared by parallel tests and TestMain
var e2eClientHome string

// setupE2EClientHome creates e2eClientHome and returns its cleanup.
func setupE2EClientHome() (func(), error) {
	home, err := os.MkdirTemp("", "kuke-e2e-home-")
	if err != nil {
		return nil, fmt.Errorf("create the e2e client HOME: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(home) }
	if _, err = clientconfig.WriteDefault(filepath.Join(home, ".kuke", "kuke.yaml")); err != nil {
		cleanup()
		return nil, fmt.Errorf("seed the e2e client configuration: %w", err)
	}
	e2eClientHome = home
	return cleanup, nil
}

// isolateClientHome replaces the HOME and drops the KUKE_CONFIGURATION that
// env carries from the test process's own environment. A value a test set on
// purpose differs from the inherited one and is kept: nonRootEnv's HOME, a
// HOME=<t.TempDir()>, and e2eRunEnv's per-run KUKE_CONFIGURATION, which it
// appends after this runs. Matching by value rather than by whether env came
// from os.Environ also covers callers that extend os.Environ().
func isolateClientHome(env []string) []string {
	home, hasHome := os.LookupEnv("HOME")
	configuration, hasConfiguration := os.LookupEnv("KUKE_CONFIGURATION")
	result := make([]string, 0, len(env))
	for _, value := range env {
		switch {
		case hasConfiguration && value == "KUKE_CONFIGURATION="+configuration:
			continue
		case hasHome && e2eClientHome != "" && value == "HOME="+home:
			value = "HOME=" + e2eClientHome
		}
		result = append(result, value)
	}
	return result
}

func TestE2ERunEnvIsolatesInheritedHome(t *testing.T) {
	t.Setenv("HOME", "/home/developer")
	t.Setenv("KUKE_CONFIGURATION", "/home/developer/.kuke/kuke.yaml")

	env := e2eRunEnv(nil, []string{"--host", "unix:///tmp/k.sock", "get", "realms"})
	if !slices.Contains(env, "HOME="+e2eClientHome) {
		t.Fatalf("inherited HOME not replaced with %s: %v", e2eClientHome, env)
	}
	for _, value := range env {
		if value == "HOME=/home/developer" || value == "KUKE_CONFIGURATION=/home/developer/.kuke/kuke.yaml" {
			t.Fatalf("inherited %q reached the child: %v", value, env)
		}
	}

	// os.Environ() extended by the caller is isolated too.
	env = e2eRunEnv(append(os.Environ(), "PATH=/opt/bin"), []string{"version"})
	if !slices.Contains(env, "HOME="+e2eClientHome) || !slices.Contains(env, "PATH=/opt/bin") {
		t.Fatalf("extended environment not isolated: %v", env)
	}

	// A HOME or KUKE_CONFIGURATION the test chose is kept.
	chosen := []string{"HOME=/tmp/chosen", "KUKE_CONFIGURATION=/tmp/chosen/kuke.yaml"}
	if got := e2eRunEnv(chosen, []string{"version"}); !slices.Equal(got, chosen) {
		t.Fatalf("deliberate environment changed: got %v, want %v", got, chosen)
	}
}

func TestE2EClientHomeHoldsDefaultConfiguration(t *testing.T) {
	path := filepath.Join(e2eClientHome, ".kuke", "kuke.yaml")
	doc, err := clientconfig.Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if doc.Kind == "" {
		t.Fatalf("%s is not the seeded default: %+v", path, doc)
	}
}
