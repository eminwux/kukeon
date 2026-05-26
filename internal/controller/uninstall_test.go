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

package controller_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/controller"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// uninstallNoopRunner accepts the realm-purge plumbing PurgeRealm calls into
// without actually doing anything — the goal is to verify Uninstall's flow
// and reporting, not the runner's purge internals (covered elsewhere).
func uninstallNoopRunner(extraRealms []intmodel.Realm) *fakeRunner {
	f := &fakeRunner{}
	f.ListRealmsFn = func() ([]intmodel.Realm, error) {
		return extraRealms, nil
	}
	f.GetRealmFn = func(r intmodel.Realm) (intmodel.Realm, error) {
		// Echo the input so PurgeRealm sees a "metadata exists" realm with
		// the namespace we provided.
		return r, nil
	}
	f.ListSpacesFn = func(_ string) ([]intmodel.Space, error) {
		return nil, nil
	}
	f.DeleteRealmFn = func(_ intmodel.Realm) error { return nil }
	f.PurgeRealmFn = func(_ intmodel.Realm) (bool, error) { return true, nil }
	return f
}

func TestUninstall_PurgesWellKnownRealmsAndCleansFilesystem(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	// Plant a sentinel file inside each so we can prove RemoveAll fired.
	if err := os.WriteFile(filepath.Join(tmpRunPath, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed run path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpSocketDir, "kukeond.sock"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed socket dir: %v", err)
	}

	purged := map[string]string{} // name -> namespace
	f := uninstallNoopRunner(nil)
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		purged[r.Metadata.Name] = r.Spec.Namespace
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, uninstallErr := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})
	if uninstallErr != nil {
		t.Fatalf("Uninstall returned error: %v", uninstallErr)
	}

	// Both well-known realms must be in the report, with their canonical
	// containerd namespaces (the whole point of preferring well-known
	// names over a stale/missing on-disk realm list).
	wantDefaultNs := consts.RealmNamespace(consts.KukeonDefaultRealmName)
	if got := purged[consts.KukeonDefaultRealmName]; got != wantDefaultNs {
		t.Errorf("default realm purged with namespace %q, want %q", got, wantDefaultNs)
	}
	wantSystemNs := consts.RealmNamespace(consts.KukeSystemRealmName)
	if got := purged[consts.KukeSystemRealmName]; got != wantSystemNs {
		t.Errorf("kuke-system realm purged with namespace %q, want %q", got, wantSystemNs)
	}

	if !report.SocketDirRemove || !report.SocketDirExists {
		t.Errorf("socket dir not removed: existed=%v removed=%v", report.SocketDirExists, report.SocketDirRemove)
	}
	if !report.RunPathRemove || !report.RunPathExists {
		t.Errorf("run path not removed: existed=%v removed=%v", report.RunPathExists, report.RunPathRemove)
	}
	if _, statErr := os.Stat(tmpRunPath); !os.IsNotExist(statErr) {
		t.Errorf("run path %q still present after uninstall", tmpRunPath)
	}
	if _, statErr := os.Stat(tmpSocketDir); !os.IsNotExist(statErr) {
		t.Errorf("socket dir %q still present after uninstall", tmpSocketDir)
	}
}

func TestUninstall_IsIdempotent(t *testing.T) {
	tmpRunPath := filepath.Join(t.TempDir(), "kukeon-absent")
	tmpSocketDir := filepath.Join(t.TempDir(), "run-absent")

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)

	// First run on a never-existed install: must not error, nothing exists
	// to clean up but the realms still get a defensive purge call.
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("first Uninstall errored: %v", err)
	}
	if report.SocketDirExists || report.SocketDirRemove {
		t.Errorf("expected socket dir absent on clean host, got existed=%v removed=%v",
			report.SocketDirExists, report.SocketDirRemove)
	}
	if report.RunPathExists || report.RunPathRemove {
		t.Errorf("expected run path absent on clean host, got existed=%v removed=%v",
			report.RunPathExists, report.RunPathRemove)
	}

	// Re-run: still no error.
	if _, repeatErr := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	}); repeatErr != nil {
		t.Fatalf("repeat Uninstall errored: %v", repeatErr)
	}
}

func TestUninstall_MergesListedRealmsWithWellKnown(t *testing.T) {
	tmpRunPath := t.TempDir()
	custom := buildTestRealm("custom", "custom.kukeon.io")
	f := uninstallNoopRunner([]intmodel.Realm{custom})

	purgedNames := []string{}
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		purgedNames = append(purgedNames, r.Metadata.Name)
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	if _, err := ctrl.Uninstall(controller.UninstallOptions{
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Uninstall errored: %v", err)
	}

	// Listed realm must come first (preserving insertion order), then the
	// missing well-known realms appended deduplicated.
	want := []string{"custom", consts.KukeonDefaultRealmName, consts.KukeSystemRealmName}
	if len(purgedNames) != len(want) {
		t.Fatalf("purge sequence = %v, want %v", purgedNames, want)
	}
	for i := range want {
		if purgedNames[i] != want[i] {
			t.Errorf("purge[%d] = %q, want %q", i, purgedNames[i], want[i])
		}
	}
}

// TestUninstall_PurgeFailureGatesFilesystemCleanup pins the half-cleaned-host
// gate from issue #287: when at least one realm fails to drop its containerd
// namespace, /run/kukeon, /opt/kukeon, and the kukeon system user/group must
// be left intact so the host stays internally consistent. Tearing them out
// while overlay snapshots in the residual namespace are still pinning files
// on disk strands the next `kuke init` with stale containerd state.
func TestUninstall_PurgeFailureGatesFilesystemCleanup(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	runMarker := filepath.Join(tmpRunPath, "marker")
	if err := os.WriteFile(runMarker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed run path: %v", err)
	}
	socketMarker := filepath.Join(tmpSocketDir, "marker")
	if err := os.WriteFile(socketMarker, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed socket dir: %v", err)
	}

	f := uninstallNoopRunner(nil)
	failure := errors.New("synthetic purge failure")
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		if r.Metadata.Name == consts.KukeSystemRealmName {
			return false, failure
		}
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error from failing realm purge, got nil")
	}
	if !strings.Contains(err.Error(), consts.KukeSystemRealmName) {
		t.Errorf("error %q does not mention failing realm %q", err, consts.KukeSystemRealmName)
	}

	if !report.CleanupSkipped {
		t.Errorf("CleanupSkipped=false on a host with a failed realm purge; expected gate to fire")
	}
	if report.RunPathRemove {
		t.Errorf("run path was removed after a realm purge failure; expected gate to skip teardown")
	}
	if report.SocketDirRemove {
		t.Errorf("socket dir was removed after a realm purge failure; expected gate to skip teardown")
	}
	if _, statErr := os.Stat(runMarker); statErr != nil {
		t.Errorf("run-path marker missing — gate failed to preserve /opt/kukeon: %v", statErr)
	}
	if _, statErr := os.Stat(socketMarker); statErr != nil {
		t.Errorf("socket-dir marker missing — gate failed to preserve /run/kukeon: %v", statErr)
	}
	// The failing realm's outcome must be in the report so callers can
	// surface what went wrong. NamespaceRemoved must be false so the renderer
	// can flag the residual namespace from issue #193 instead of misreporting
	// "purged".
	var foundFailure bool
	for _, outcome := range report.Realms {
		if outcome.Name != consts.KukeSystemRealmName {
			continue
		}
		if outcome.Err == nil {
			t.Errorf("expected failing realm to carry its err; got outcome=%+v", outcome)
		}
		if outcome.Purged {
			t.Errorf("failing realm must not be marked Purged=true; got %+v", outcome)
		}
		if outcome.NamespaceRemoved {
			t.Errorf("failing realm must report NamespaceRemoved=false (namespace survived); got %+v", outcome)
		}
		foundFailure = true
		break
	}
	if !foundFailure {
		t.Errorf("expected failing realm to be reported with its error; got %+v", report.Realms)
	}

	// The well-known "default" realm purged successfully — its outcome must
	// be the converse: Purged + NamespaceRemoved both true.
	var foundOK bool
	for _, outcome := range report.Realms {
		if outcome.Name != consts.KukeonDefaultRealmName {
			continue
		}
		if !outcome.Purged || !outcome.NamespaceRemoved || outcome.Err != nil {
			t.Errorf(
				"successful realm outcome should be {Purged:true, NamespaceRemoved:true, Err:nil}; got %+v",
				outcome,
			)
		}
		foundOK = true
		break
	}
	if !foundOK {
		t.Errorf("expected successful default realm in report; got %+v", report.Realms)
	}
}

// TestUninstall_ReclaimsBuildCachePerNamespace pins the issue #904 fix: for
// every realm whose containerd namespace was actually removed, Uninstall
// reclaims the matching per-namespace BuildKit state directory at
// <BuildCacheBaseDir>/<namespace>. Leaving the cache behind strands the next
// `kuke build` with "parent snapshot does not exist" or "content digest ...
// not found" because cache.db references containerd by snapshot ID and
// content digest into a namespace that no longer exists.
func TestUninstall_ReclaimsBuildCachePerNamespace(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	cacheBase := t.TempDir()

	// Seed a populated per-namespace cache dir for each well-known realm so
	// we can prove RemoveAll fired (not just os.Remove of an empty dir).
	for _, ns := range []string{
		consts.RealmNamespace(consts.KukeonDefaultRealmName),
		consts.RealmNamespace(consts.KukeSystemRealmName),
	} {
		nsDir := filepath.Join(cacheBase, ns)
		if err := os.MkdirAll(filepath.Join(nsDir, "buildkit"), 0o700); err != nil {
			t.Fatalf("seed cache %q: %v", nsDir, err)
		}
		if err := os.WriteFile(
			filepath.Join(nsDir, "cache.db"), []byte("seeded"), 0o600,
		); err != nil {
			t.Fatalf("seed cache.db under %q: %v", nsDir, err)
		}
	}

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: cacheBase,
	})
	if err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	// One BuildCachePurgeOutcome per well-known realm, each with Existed +
	// Removed true and no error. Ordering must match Realms.
	if len(report.BuildCaches) != 2 {
		t.Fatalf("BuildCaches len=%d, want 2 (one per well-known realm); got %+v",
			len(report.BuildCaches), report.BuildCaches)
	}
	wantNamespaces := map[string]bool{
		consts.RealmNamespace(consts.KukeonDefaultRealmName): false,
		consts.RealmNamespace(consts.KukeSystemRealmName):    false,
	}
	for _, c := range report.BuildCaches {
		if !c.Existed || !c.Removed || c.Err != nil {
			t.Errorf("cache outcome for %q = %+v; want Existed:true Removed:true Err:nil",
				c.Namespace, c)
		}
		wantPath := filepath.Join(cacheBase, c.Namespace)
		if c.Path != wantPath {
			t.Errorf("cache outcome Path=%q, want %q", c.Path, wantPath)
		}
		if _, ok := wantNamespaces[c.Namespace]; !ok {
			t.Errorf("unexpected namespace in BuildCaches: %q", c.Namespace)
			continue
		}
		wantNamespaces[c.Namespace] = true
	}
	for ns, seen := range wantNamespaces {
		if !seen {
			t.Errorf("expected BuildCache row for namespace %q; got %+v",
				ns, report.BuildCaches)
		}
	}

	// The per-namespace dirs must actually be gone from disk.
	for ns := range wantNamespaces {
		nsDir := filepath.Join(cacheBase, ns)
		if _, statErr := os.Stat(nsDir); !os.IsNotExist(statErr) {
			t.Errorf("cache dir %q survived uninstall: %v", nsDir, statErr)
		}
	}

	// Base dir was emptied by the per-namespace sweep, so the rmdir must
	// have succeeded — issue #904's "rmdir base if empty" branch.
	if !report.BuildCacheBaseExisted {
		t.Errorf("BuildCacheBaseExisted=false; seeded base was not seen")
	}
	if !report.BuildCacheBaseRemoved {
		t.Errorf("BuildCacheBaseRemoved=false; empty base must be rmdir'd")
	}
	if _, statErr := os.Stat(cacheBase); !os.IsNotExist(statErr) {
		t.Errorf("base cache dir %q survived after rmdir-when-empty: %v", cacheBase, statErr)
	}
}

// TestUninstall_BuildCacheSkippedOnNamespaceFailure pins the gate: a realm
// whose containerd namespace was NOT removed (the issue #193 partial-state
// path) must keep its BuildKit cache on disk. Wiping it would prevent the
// follow-up `kuke uninstall` from having the same cache metadata to reason
// about — and the cache references are still meaningful while the namespace
// survives.
func TestUninstall_BuildCacheSkippedOnNamespaceFailure(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	cacheBase := t.TempDir()

	// Seed caches for both well-known realms.
	defaultNs := consts.RealmNamespace(consts.KukeonDefaultRealmName)
	systemNs := consts.RealmNamespace(consts.KukeSystemRealmName)
	for _, ns := range []string{defaultNs, systemNs} {
		if err := os.MkdirAll(filepath.Join(cacheBase, ns), 0o700); err != nil {
			t.Fatalf("seed cache %q: %v", ns, err)
		}
		if err := os.WriteFile(
			filepath.Join(cacheBase, ns, "cache.db"), []byte("x"), 0o600,
		); err != nil {
			t.Fatalf("seed cache.db: %v", err)
		}
	}

	f := uninstallNoopRunner(nil)
	failure := errors.New("synthetic purge failure")
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		// kuke-system reports NamespaceRemoved=false (PurgeRealm returns
		// false + err); default reports NamespaceRemoved=true.
		if r.Metadata.Name == consts.KukeSystemRealmName {
			return false, failure
		}
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, _ := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: cacheBase,
	})

	// Exactly one cache row: the realm that successfully dropped its
	// namespace. The realm whose namespace survived contributes no row.
	if len(report.BuildCaches) != 1 {
		t.Fatalf("BuildCaches len=%d, want 1 (only the successfully purged realm); got %+v",
			len(report.BuildCaches), report.BuildCaches)
	}
	got := report.BuildCaches[0]
	if got.Namespace != defaultNs {
		t.Errorf("BuildCaches[0].Namespace=%q, want %q", got.Namespace, defaultNs)
	}
	if !got.Removed {
		t.Errorf("default-realm cache should be Removed=true; got %+v", got)
	}

	// kuke-system's cache must survive on disk so the operator's follow-up
	// recovery has the same metadata available.
	if _, statErr := os.Stat(filepath.Join(cacheBase, systemNs)); statErr != nil {
		t.Errorf("kuke-system cache dir wiped despite namespace survival: %v", statErr)
	}

	// Base dir must NOT be rmdir'd — kuke-system's subdir keeps it non-empty.
	if report.BuildCacheBaseRemoved {
		t.Errorf("BuildCacheBaseRemoved=true with surviving cousin subdir; rmdir must have stayed a no-op")
	}
	if _, statErr := os.Stat(cacheBase); statErr != nil {
		t.Errorf("base cache dir wiped despite non-empty rmdir: %v", statErr)
	}
}

// TestUninstall_BuildCacheBaseAbsent pins the "host never built" path: a
// clean install where /var/lib/kukebuild was never created must still
// produce a no-op exit (no error, no spurious rows) — uninstall idempotency
// extends to the cache reclaim half.
func TestUninstall_BuildCacheBaseAbsent(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	// Point at a never-created path under a tmp dir so the test cannot
	// accidentally touch /var/lib/kukebuild on the dev host.
	cacheBase := filepath.Join(t.TempDir(), "never-built")

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		SkipUserGroup:     true,
		BuildCacheBaseDir: cacheBase,
	})
	if err != nil {
		t.Fatalf("Uninstall on host with no BuildKit cache errored: %v", err)
	}

	// Every cache outcome should be Existed=false / Removed=false / no err.
	for _, c := range report.BuildCaches {
		if c.Existed || c.Removed || c.Err != nil {
			t.Errorf("cache outcome on absent base should be all-false: %+v", c)
		}
	}

	if report.BuildCacheBaseExisted {
		t.Errorf("BuildCacheBaseExisted=true for never-created dir %q", cacheBase)
	}
	if report.BuildCacheBaseRemoved {
		t.Errorf("BuildCacheBaseRemoved=true for never-created dir %q", cacheBase)
	}
}

// TestUninstall_BuildCacheBaseDefaultsToConst pins the production-code path:
// when UninstallOptions leaves BuildCacheBaseDir empty (the CLI's default),
// the controller falls back to consts.KukebuildBaseDir rather than a silent
// no-op. The fallback is reported on the UninstallReport so the renderer can
// label the row. Pointing at a host path is undesirable in tests, so this
// guard only asserts the BuildCacheBaseDir field gets surfaced — verifying
// the constant flows through.
func TestUninstall_BuildCacheBaseDefaultsToConst(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()

	// Stand in a fakeRunner that always returns NamespaceRemoved=false so
	// no per-namespace cache reclaim fires — the test is about the base-dir
	// default surfacing only, not about touching /var/lib/kukebuild.
	f := uninstallNoopRunner(nil)
	f.PurgeRealmFn = func(_ intmodel.Realm) (bool, error) { return false, nil }

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, _ := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:     tmpSocketDir,
		SkipUserGroup: true,
		// BuildCacheBaseDir deliberately empty — exercise the default.
	})
	if report.BuildCacheBaseDir != consts.KukebuildBaseDir {
		t.Errorf("BuildCacheBaseDir=%q, want default %q",
			report.BuildCacheBaseDir, consts.KukebuildBaseDir)
	}
}

// setupTestControllerWithRunPath mirrors setupTestController but lets the
// caller pin a temporary run path so filesystem assertions can target it.
func setupTestControllerWithRunPath(t *testing.T, mockRunner *fakeRunner, runPath string) *controller.Exec {
	t.Helper()
	ctx := setupTestContext(t)
	logger := setupTestLogger(t)
	opts := controller.Options{
		RunPath:          runPath,
		ContainerdSocket: "/test/containerd.sock",
	}
	return controller.NewControllerExecForTesting(ctx, logger, opts, mockRunner)
}

// TestUninstall_DaemonStopStep_PIDPresent verifies the daemon-stop step from
// issue #195 fires before the realm-purge loop and surfaces the stub's report
// verbatim. The whole point of running this step first is that the live
// daemon's containerd session must be gone before PurgeRealm starts draining
// `kuke-system.kukeon.io` — otherwise the daemon pins containers we are
// trying to delete and the namespace delete fails.
func TestUninstall_DaemonStopStep_PIDPresent(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()

	// Match the production write path (cmd/kukeond/serve.go): kukeond
	// writes its PID to <socketDir>/kukeond.pid (sibling of the socket, per
	// the storage-layout.md design). Putting the seed file under the run
	// path is the #287 misconfiguration this test must not pin.
	pidFilePath := filepath.Join(tmpSocketDir, "kukeond.pid")
	if err := os.WriteFile(pidFilePath, []byte("12345\n"), 0o644); err != nil {
		t.Fatalf("seed pid file: %v", err)
	}

	// Record purge ordering vs. daemon-stop ordering so we can assert the
	// daemon was signalled before any realm purge ran.
	var (
		stopperCalledAt int
		firstPurgeAt    int
		callIdx         int
	)

	stopper := func(_ context.Context, gotPidFile string, gotGrace time.Duration) (controller.DaemonStopReport, error) {
		callIdx++
		stopperCalledAt = callIdx
		if gotPidFile != pidFilePath {
			t.Errorf("stopper got pidFile %q, want %q", gotPidFile, pidFilePath)
		}
		if gotGrace != 250*time.Millisecond {
			t.Errorf("stopper got grace %v, want %v", gotGrace, 250*time.Millisecond)
		}
		return controller.DaemonStopReport{
			PIDFilePresent: true,
			PIDFile:        gotPidFile,
			PID:            12345,
			Signalled:      true,
		}, nil
	}

	f := uninstallNoopRunner(nil)
	f.PurgeRealmFn = func(_ intmodel.Realm) (bool, error) {
		callIdx++
		if firstPurgeAt == 0 {
			firstPurgeAt = callIdx
		}
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:             tmpSocketDir,
		KukeondPIDFile:        pidFilePath,
		DaemonStopper:         stopper,
		DaemonStopGracePeriod: 250 * time.Millisecond,
		SkipUserGroup:         true,
		BuildCacheBaseDir:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	if stopperCalledAt == 0 {
		t.Fatalf("daemon stopper was never called; report.Daemon=%+v", report.Daemon)
	}
	if firstPurgeAt == 0 {
		t.Fatalf("expected at least one PurgeRealm call (well-known realms); report=%+v", report)
	}
	if stopperCalledAt >= firstPurgeAt {
		t.Errorf(
			"daemon-stop must run before realm purge — stopperCalledAt=%d firstPurgeAt=%d",
			stopperCalledAt, firstPurgeAt,
		)
	}

	if !report.Daemon.PIDFilePresent {
		t.Errorf("report.Daemon.PIDFilePresent=false, want true; got %+v", report.Daemon)
	}
	if report.Daemon.PID != 12345 {
		t.Errorf("report.Daemon.PID=%d, want 12345", report.Daemon.PID)
	}
	if !report.Daemon.Signalled {
		t.Errorf("report.Daemon.Signalled=false, want true; got %+v", report.Daemon)
	}
	if report.Daemon.PIDFile != pidFilePath {
		t.Errorf("report.Daemon.PIDFile=%q, want %q", report.Daemon.PIDFile, pidFilePath)
	}
}

// TestUninstall_DaemonStopStep_PIDAbsent covers the partial-uninstall path
// from issue #193: the stopper still runs (Uninstall has to ask, since only
// the stopper can read the PID file), but the report comes back saying no
// daemon was found. The realm-purge loop must still execute — uninstall on
// a host with no daemon running must not regress.
func TestUninstall_DaemonStopStep_PIDAbsent(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	// Deliberately do NOT create kukeond.pid in tmpSocketDir.

	var stopperCalls int
	stopper := func(_ context.Context, gotPidFile string, _ time.Duration) (controller.DaemonStopReport, error) {
		stopperCalls++
		// PIDFilePresent=false signals "no live daemon to stop" without an error
		// (matches the production stopper's read-ENOENT branch).
		return controller.DaemonStopReport{
			PIDFile:        gotPidFile,
			PIDFilePresent: false,
		}, nil
	}

	purgedNames := []string{}
	f := uninstallNoopRunner(nil)
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		purgedNames = append(purgedNames, r.Metadata.Name)
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		DaemonStopper:     stopper,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	if stopperCalls != 1 {
		t.Errorf("daemon stopper called %d times, want exactly 1", stopperCalls)
	}
	if report.Daemon.PIDFilePresent {
		t.Errorf("report.Daemon.PIDFilePresent=true on a host with no PID file; got %+v", report.Daemon)
	}
	if report.Daemon.Signalled {
		t.Errorf("report.Daemon.Signalled=true with no PID file; got %+v", report.Daemon)
	}
	// The well-known realms must still be purged — a stale or missing PID
	// file must not block subsequent cleanup.
	if len(purgedNames) == 0 {
		t.Errorf("realm-purge loop did not run after PID-absent daemon-stop; want at least the well-known realms")
	}
}

// TestUninstall_SuffixEnumeratorPurgesKukeonNamespacesOnly is the AC-required
// suffix-enumeration test from issue #195: a containerd-namespace lister
// returning a mix of `.kukeon.io` and unrelated namespaces (`moby`) must yield
// purge calls only for the kukeon-suffixed ones, and never for `moby`.
//
// This is the partial-uninstall recovery path (#193): user-created realms
// whose on-disk metadata was wiped are still cleaned up because containerd
// is the source of truth for which namespaces actually exist.
func TestUninstall_SuffixEnumeratorPurgesKukeonNamespacesOnly(t *testing.T) {
	tmpRunPath := t.TempDir()

	f := uninstallNoopRunner(nil)
	// ListRealms returns nothing — the on-disk metadata path is empty,
	// forcing the suffix-enumerator path to be the source of truth.
	f.ListRealmsFn = func() ([]intmodel.Realm, error) { return nil, nil }
	f.ListContainerdNamespacesFn = func() ([]string, error) {
		return []string{
			"default.kukeon.io",
			"kuke-system.kukeon.io",
			"myteam.kukeon.io",
			"moby",
		}, nil
	}

	purgedNames := map[string]string{} // name -> namespace
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		purgedNames[r.Metadata.Name] = r.Spec.Namespace
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	if _, err := ctrl.Uninstall(controller.UninstallOptions{
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	wantPurged := map[string]string{
		"default":     "default.kukeon.io",
		"kuke-system": "kuke-system.kukeon.io",
		"myteam":      "myteam.kukeon.io",
	}
	if len(purgedNames) != len(wantPurged) {
		// Sort for deterministic error output.
		got := make([]string, 0, len(purgedNames))
		for k := range purgedNames {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf(
			"purged realms = %v, want exactly %v (3 .kukeon.io namespaces)",
			got,
			[]string{"default", "kuke-system", "myteam"},
		)
	}
	for name, wantNs := range wantPurged {
		if got := purgedNames[name]; got != wantNs {
			t.Errorf("realm %q purged with namespace %q, want %q", name, got, wantNs)
		}
	}
	if _, leaked := purgedNames["moby"]; leaked {
		t.Errorf("moby was purged — non-kukeon namespaces must be filtered out; purged=%v", purgedNames)
	}
}

// TestUninstall_SuffixIsolationAcrossInstances pins the issue #284 AC:
// an uninstall configured for a non-default containerd-namespace suffix
// (e.g. "dev.kukeon.io") must not enumerate or purge namespaces belonging
// to the default-suffix instance (".kukeon.io"). The suffix-gated
// enumeration in collectRealmsForUninstall + IsKukeonNamespace is the
// barrier — when the dev daemon's ServerConfiguration is loaded, consts.
// ConfigureRuntime mutates the package-level RealmNamespaceSuffix and
// every downstream `IsKukeonNamespace`/`RealmFromNamespace` call observes
// the dev suffix; the prod-suffix namespaces fall through as foreign and
// the well-known realm floor lands on the dev-suffix variants. This test
// fakes the runtime override directly (rather than going through the CLI
// loader) so the controller-level invariant is locked independently of
// the cmd/-side plumbing.
func TestUninstall_SuffixIsolationAcrossInstances(t *testing.T) {
	// Save/restore the runtime-override globals so the test mirrors the
	// consts_test withRuntime helper without taking a dependency on its
	// package. Cannot run in parallel — the package vars are process state.
	prevSuffix := consts.RealmNamespaceSuffix
	prevRoot := consts.KukeonCgroupRoot
	t.Cleanup(func() {
		consts.RealmNamespaceSuffix = prevSuffix //nolint:reassign // restore runtime-overridable global
		consts.KukeonCgroupRoot = prevRoot       //nolint:reassign // restore runtime-overridable global
	})
	if err := consts.ConfigureRuntime("dev.kukeon.io", "/kukeon-dev"); err != nil {
		t.Fatalf("ConfigureRuntime(dev.kukeon.io, /kukeon-dev): %v", err)
	}

	tmpRunPath := t.TempDir()

	f := uninstallNoopRunner(nil)
	// ListRealms returns nothing — the suffix-enumerator path is the source
	// of truth, and the well-known realm floor will land on the dev-suffix
	// variants because consts.RealmNamespace observes the configured suffix.
	f.ListRealmsFn = func() ([]intmodel.Realm, error) { return nil, nil }
	// A containerd-namespace lister returning a mix of dev-instance,
	// default-instance, and unrelated namespaces. The dev-configured
	// uninstall must touch only `*.dev.kukeon.io`.
	f.ListContainerdNamespacesFn = func() ([]string, error) {
		return []string{
			"default.dev.kukeon.io",     // dev instance — must purge
			"kuke-system.dev.kukeon.io", // dev instance — must purge
			"myteam.dev.kukeon.io",      // dev instance — must purge
			"default.kukeon.io",         // prod instance — must NOT purge
			"kuke-system.kukeon.io",     // prod instance — must NOT purge
			"prodteam.kukeon.io",        // prod instance — must NOT purge
			"moby",                      // foreign — must NOT purge
		}, nil
	}

	purgedNames := map[string]string{} // name -> namespace
	f.PurgeRealmFn = func(r intmodel.Realm) (bool, error) {
		purgedNames[r.Metadata.Name] = r.Spec.Namespace
		return true, nil
	}

	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	if _, err := ctrl.Uninstall(controller.UninstallOptions{
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	wantPurged := map[string]string{
		"default":     "default.dev.kukeon.io",
		"kuke-system": "kuke-system.dev.kukeon.io",
		"myteam":      "myteam.dev.kukeon.io",
	}
	if len(purgedNames) != len(wantPurged) {
		got := make([]string, 0, len(purgedNames))
		for k := range purgedNames {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Fatalf(
			"purged realms = %v, want exactly %v (only dev-suffixed namespaces)",
			got, []string{"default", "kuke-system", "myteam"},
		)
	}
	for name, wantNs := range wantPurged {
		if got := purgedNames[name]; got != wantNs {
			t.Errorf("realm %q purged with namespace %q, want %q", name, got, wantNs)
		}
	}

	// Prod-instance namespaces must not have been touched. A leak here is
	// the failure mode the AC's "uninstall on dev cannot enumerate or purge
	// prod namespaces" prohibits.
	for _, prodNs := range []string{
		"default.kukeon.io",
		"kuke-system.kukeon.io",
		"prodteam.kukeon.io",
		"moby",
	} {
		for purgedName, purgedNs := range purgedNames {
			if purgedNs == prodNs {
				t.Errorf(
					"prod-instance namespace %q leaked into dev uninstall (purged as realm %q); "+
						"cross-instance isolation must filter it out",
					prodNs, purgedName,
				)
			}
		}
	}
}

// TestUninstall_ReleasesBindMountsBeforeRmdir pins the #434 fix: a kukeon-
// owned bind mount under /run/kukeon (or /opt/kukeon) must be unmounted by
// the controller before the rmdir step, so a post-attach uninstall on a
// `make dev-init` host (which leaves /run/kukeon/tty bind-mounted) exits 0.
func TestUninstall_ReleasesBindMountsBeforeRmdir(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()

	socketMount := filepath.Join(tmpSocketDir, "tty")
	runMount := filepath.Join(tmpRunPath, "default", "space")

	// Order in which the releaser was called; the release of the socket-dir
	// mount must happen before the rmdir on its parent fires, and the same
	// for the run-path mount. We assert by checking the directory still
	// exists at release-time.
	released := []string{}
	releaser := func(root string) ([]controller.MountReleaseAttempt, error) {
		var target string
		switch root {
		case tmpSocketDir:
			target = socketMount
		case tmpRunPath:
			target = runMount
		default:
			t.Errorf("releaser called with unexpected root %q", root)
			return nil, nil
		}
		released = append(released, target)
		return []controller.MountReleaseAttempt{{Target: target, Released: true}}, nil
	}

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		MountReleaser:     releaser,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("Uninstall returned err=%v", err)
	}

	wantReleased := []string{socketMount, runMount}
	sort.Strings(released)
	sort.Strings(wantReleased)
	if !equalStringSlices(released, wantReleased) {
		t.Errorf("releaser invocations = %v, want %v", released, wantReleased)
	}

	if len(report.SocketDirMounts) != 1 || report.SocketDirMounts[0].Target != socketMount {
		t.Errorf("report.SocketDirMounts = %+v, want one entry for %q", report.SocketDirMounts, socketMount)
	}
	if len(report.RunPathMounts) != 1 || report.RunPathMounts[0].Target != runMount {
		t.Errorf("report.RunPathMounts = %+v, want one entry for %q", report.RunPathMounts, runMount)
	}

	// Successful release must let the dir teardown succeed.
	if !report.SocketDirRemove {
		t.Errorf("expected socket dir removed after successful unmount; got %+v", report)
	}
	if !report.RunPathRemove {
		t.Errorf("expected run path removed after successful unmount; got %+v", report)
	}
}

// TestUninstall_ResidualMountSurfacesInReportAndError pins the second half of
// the #434 AC: when a kukeon-owned mount cannot be released, the report row
// names the surviving mountpoint and the call returns a non-nil error so
// automation can branch on exit status.
func TestUninstall_ResidualMountSurfacesInReportAndError(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()

	stuckMount := filepath.Join(tmpSocketDir, "tty")
	stuckErr := errors.New("synthetic EBUSY")

	releaser := func(root string) ([]controller.MountReleaseAttempt, error) {
		if root == tmpSocketDir {
			return []controller.MountReleaseAttempt{{Target: stuckMount, Err: stuckErr}}, nil
		}
		return nil, nil
	}

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	report, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:         tmpSocketDir,
		MountReleaser:     releaser,
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	})

	if err == nil {
		t.Fatalf("expected Uninstall error from residual mount; got nil")
	}
	if !errors.Is(err, stuckErr) {
		t.Errorf("expected wrapped err to carry residual cause; got %v", err)
	}
	if !strings.Contains(err.Error(), stuckMount) {
		t.Errorf("expected err to name surviving mountpoint %q; got %v", stuckMount, err)
	}

	if len(report.SocketDirMounts) != 1 || report.SocketDirMounts[0].Err == nil {
		t.Errorf("expected report.SocketDirMounts to carry residual err; got %+v", report.SocketDirMounts)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestUninstall_DefaultPIDFileResolvesToSocketDir pins the controller's
// default PID-file path against the production write path in
// cmd/kukeond/serve.go.
//
// kukeond writes its PID to <socketDir>/kukeond.pid (sibling of the socket),
// matching the storage-layout.md design "Sockets and pid files belong in
// /run". Issue #287 was the inverse misalignment — Uninstall used to default
// to <socketDir>/kukeond.pid while kukeond wrote <runPath>/kukeond.pid; the
// fix kept the two aligned. This test guards against either side drifting
// again — it asserts the path the controller hands the stopper is exactly
// filepath.Join(opts.SocketDir, "kukeond.pid").
func TestUninstall_DefaultPIDFileResolvesToSocketDir(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()

	wantPIDFile := filepath.Join(tmpSocketDir, "kukeond.pid")
	var gotPIDFile string
	stopper := func(_ context.Context, gotPidFile string, _ time.Duration) (controller.DaemonStopReport, error) {
		gotPIDFile = gotPidFile
		return controller.DaemonStopReport{PIDFile: gotPidFile}, nil
	}

	f := uninstallNoopRunner(nil)
	ctrl := setupTestControllerWithRunPath(t, f, tmpRunPath)
	if _, err := ctrl.Uninstall(controller.UninstallOptions{
		SocketDir:     tmpSocketDir,
		DaemonStopper: stopper,
		// KukeondPIDFile deliberately empty — the whole point is to exercise
		// the controller's default-resolution path.
		SkipUserGroup:     true,
		BuildCacheBaseDir: t.TempDir(),
	}); err != nil {
		t.Fatalf("Uninstall returned error: %v", err)
	}

	if gotPIDFile != wantPIDFile {
		t.Errorf(
			"default PID-file path mismatch:\n  controller passed: %q\n  cmd/kukeond/serve.go writes: <socketDir>/kukeond.pid -> %q",
			gotPIDFile,
			wantPIDFile,
		)
	}
	if filepath.Dir(gotPIDFile) == tmpRunPath {
		t.Errorf(
			"controller defaulted PID file under run path %q — that is the #287 regression",
			tmpRunPath,
		)
	}
}
