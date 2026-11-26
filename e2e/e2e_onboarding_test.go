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

package e2e_test

import (
	"bufio"
	"context"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	kukefs "github.com/eminwux/kukeon/internal/util/fs"
	"github.com/eminwux/kukeon/pkg/team/config"
)

// onboardingEnv gates the onboarding e2e: it needs Node, Chromium, git and
// network access (the kukeon team source and a harness image), which the Go
// e2e job does not have. `make e2e-onboarding` sets it.
const onboardingEnv = "KUKEON_E2E_ONBOARDING"

// reclaimBuildEnv makes the test prune the kuke-system realm after `kuke init`:
// the layers and leases the kukebuild image build left in containerd. The
// harness image the agent cell pulls does not fit beside them on a host whose
// containerd store is small (a kukeon dev cell's 4 GB tmpfs).
const reclaimBuildEnv = "KUKEON_E2E_RECLAIM_BUILD"

// onboardingTimeout bounds the browser run: one team init (a git fetch), one
// harness image pull and two cell starts.
const onboardingTimeout = 25 * time.Minute

// TestOnboarding_WebUI is first-run onboarding end to end (issue #1694): on a
// host where only `kuke init` ran, a browser signs in to `kuke serve`, follows
// the Workbench's Get started checklist — Initialize, a GitHub token that also
// serves git, a login, a first agent — and enables majordomo, against a real
// kukeond. The browser side is internal/webui/frontend/tests/onboarding.spec.mjs;
// this test owns the daemon: it bootstraps the runtime the way the installer
// does, hands the spec the daemon's endpoint and tears everything down.
//
// No vendor login happens: the spec fakes the login marker in the agent
// template's state volume, as TestKuke_LoginIdentity does, which is why the
// spec is told where the default scope keeps its volumes.
//
// Team init fetches github.com/eminwux/kukeon at the revision `kuke` was built
// from (pkg/team/config/source.go): the commit of a clean checkout, which
// GitHub serves by SHA for a pull request's merge commit too, or branch main
// for a modified tree.
//
// Not run with t.Parallel(): `kuke init` owns the host-global kuke-system and
// default containerd namespaces, like TestKuke_DaemonReset_RoundTrip.
func TestOnboarding_WebUI(t *testing.T) {
	if os.Getenv(onboardingEnv) == "" {
		t.Skipf("%s unset; run via 'make e2e-onboarding' (needs Node, Chromium, git and network access)", onboardingEnv)
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		t.Fatalf("npx not found on PATH: the onboarding e2e drives a browser with Playwright: %v", err)
	}
	binDir := os.Getenv("E2E_BIN_DIR")
	if binDir == "" {
		binDir = ".."
	}
	binDir, err = filepath.Abs(binDir)
	if err != nil {
		t.Fatalf("resolve E2E_BIN_DIR: %v", err)
	}
	frontend := filepath.Join(binDir, "internal", "webui", "frontend")
	if _, statErr := os.Stat(filepath.Join(frontend, "node_modules")); statErr != nil {
		t.Fatalf("%s has no node_modules; run 'npm ci' there (make e2e-onboarding does): %v", frontend, statErr)
	}

	requireInitEgressRemoved(t)
	runPath := getRandomRunPath(t)
	t.Cleanup(func() {
		// The same teardown as TestKuke_DaemonReset_RoundTrip. The default
		// realm goes first and with --cascade: it holds the agent and
		// majordomo cells this test started.
		cleanupRealmNoDaemon(t, runPath, consts.KukeonDefaultRealmName)
		args := append(buildKukeRunPathArgs(runPath), "daemon", "reset", "--purge-system")
		_, _, _ = runBinary(t, nil, kuke, args...)
		cleanupCellNoDaemon(
			t, runPath,
			consts.KukeSystemRealmName,
			consts.KukeSystemSpaceName,
			consts.KukeSystemStackName,
			consts.KukeSystemCellName,
		)
		cleanupRealmNoDaemon(t, runPath, consts.KukeSystemRealmName)
	})

	kukeondImage := loadKukeondImageIntoContainerd(t)
	args := append(buildKukeRunPathArgs(runPath), "init", "--kukeond-image", kukeondImage)
	if code, stdout, stderr := runBinary(t, nil, kuke, args...); code != 0 {
		t.Fatalf("kuke init failed: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if os.Getenv(reclaimBuildEnv) != "" {
		pruneArgs := append(buildKukeRunPathArgs(runPath), "image", "prune", "--realm", consts.KukeSystemRealmName)
		code, stdout, stderr := runBinary(t, nil, kuke, pruneArgs...)
		t.Logf("%s: kuke image prune --realm %s: code=%d %s %s",
			reclaimBuildEnv, consts.KukeSystemRealmName, code, stdout, stderr)
	}
	// `kuke init --run-path X` brings kukeond up on X/kukeond.sock.
	host := "unix://" + filepath.Join(runPath, "kukeond.sock")

	ctx, cancel := context.WithTimeout(context.Background(), onboardingTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, npx, "playwright", "test", "onboarding.spec.mjs", "--reporter=line")
	cmd.Dir = frontend
	cmd.Env = append(os.Environ(),
		"KUKE_WEB_SMOKE_HOST="+host,
		"KUKE_WEB_BINARY="+filepath.Join(binDir, kuke),
		// Where the spec fakes the login marker: the volumes of the scope
		// team Configs live in.
		"KUKE_ONBOARDING_VOLUMES="+kukefs.VolumesDir(
			runPath, config.DefaultRealm, consts.KukeonDefaultSpaceName, consts.KukeonDefaultStackName),
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.WaitDelay = 10 * time.Second
	if runErr := cmd.Run(); runErr != nil {
		t.Errorf("onboarding browser run failed: %v (traces are under %s)",
			runErr, filepath.Join(frontend, "test-results"))
		logKukeondTail(t, runPath)
	}
}

// logKukeondTail prints the end of the kukeond container log under runPath,
// so a failed browser run shows what the daemon was doing.
func logKukeondTail(t *testing.T, runPath string) {
	t.Helper()
	const lines = 80
	var logPath string
	_ = filepath.WalkDir(runPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort search: an unreadable subtree is skipped
		}
		if !d.IsDir() && d.Name() == "log" &&
			strings.HasSuffix(filepath.Dir(path), filepath.Join(consts.KukeSystemCellName, consts.KukeSystemCellName)) {
			logPath = path
			return fs.SkipAll
		}
		return nil
	})
	if logPath == "" {
		t.Logf("no kukeond log found under %s", runPath)
		return
	}
	file, err := os.Open(logPath)
	if err != nil {
		t.Logf("open %s: %v", logPath, err)
		return
	}
	defer func() { _ = file.Close() }()
	var tail []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for scanner.Scan() {
		tail = append(tail, scanner.Text())
		if len(tail) > lines {
			tail = tail[1:]
		}
	}
	t.Logf("last %d lines of %s:\n%s", len(tail), logPath, strings.Join(tail, "\n"))
}
