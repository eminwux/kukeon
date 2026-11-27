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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
)

// pruneBuildDockerfile has a cached step (RUN one) below a step that the
// STEP build-arg invalidates (RUN two), the shape of the #1705 repro: the
// second build reuses the cached parent and prepares a new snapshot on it.
const pruneBuildDockerfile = `FROM docker.io/library/busybox:latest
RUN echo one > /one
ARG STEP=a
RUN echo "$STEP" > /two
`

// TestKuke_ImagePrune_BuildAfterPrune pins #1705: `kuke image prune` on a
// realm used to release the leases holding BuildKit's cache snapshots while
// kukebuild's cache.db (/var/lib/kukebuild/<namespace>) kept pointing at
// them, so the next `kuke build` into the realm failed with "parent snapshot
// ... does not exist" until the operator deleted that directory by hand. The
// prune now drops the directory and says so, and a build after it succeeds.
func TestKuke_ImagePrune_BuildAfterPrune(t *testing.T) {
	t.Parallel()

	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	binDir, err := filepath.Abs(binDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(filepath.Join(binDir, "kukebuild")); statErr != nil {
		t.Skipf("kukebuild not found in %s: %v", binDir, statErr)
	}
	// `kuke build` resolves kukebuild on PATH; `make e2e` builds it into
	// the repo root.
	env := append(os.Environ(), "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	runPath := getRandomRunPath(t)
	host := startKukeondDaemon(t, runPath)
	realmName := generateUniqueRealmName(t)
	namespace := consts.RealmNamespace(realmName)
	cacheDir := filepath.Join(consts.KukebuildBaseDir, namespace)
	t.Cleanup(func() {
		cleanupRealm(t, host, realmName)
		_ = os.RemoveAll(cacheDir)
	})

	runReturningBinary(t, nil, kuke, append(buildKukeDaemonArgs(host), "create", "realm", realmName)...)

	contextDir := t.TempDir()
	if err = os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte(pruneBuildDockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	build := func(step string) (int, string) {
		t.Helper()
		code, stdout, stderr := runBinary(t, env, kuke,
			"build", "--realm", realmName,
			"--kukeond-config", filepath.Join(runPath, "kukeond.yaml"),
			"--build-arg", "STEP="+step,
			"-t", "kukeon.internal/e2e-prune:"+step,
			contextDir,
		)
		return code, string(stdout) + string(stderr)
	}

	if buildCode, out := build("a"); buildCode != 0 {
		t.Fatalf("first kuke build: exit %d\n%s", buildCode, out)
	}
	if _, statErr := os.Stat(filepath.Join(cacheDir, "cache.db")); statErr != nil {
		t.Fatalf("kukebuild cache %s not written by the first build: %v", cacheDir, statErr)
	}

	code, stdout, stderr := runBinary(t, nil, kuke,
		append(buildKukeRunPathArgs(runPath), "image", "prune", "--realm", realmName)...)
	if code != 0 {
		t.Fatalf("kuke image prune: exit %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(string(stdout), "removed build cache "+cacheDir) {
		t.Errorf("prune output does not report the removed build cache %s:\n%s", cacheDir, stdout)
	}
	if _, statErr := os.Stat(cacheDir); !os.IsNotExist(statErr) {
		t.Errorf("build cache %s still present after prune (stat err %v)", cacheDir, statErr)
	}

	if buildCode, out := build("b"); buildCode != 0 {
		t.Fatalf("kuke build after kuke image prune: exit %d\n%s", buildCode, out)
	}
}
