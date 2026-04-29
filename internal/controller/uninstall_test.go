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
		SocketDir:     tmpSocketDir,
		SkipUserGroup: true,
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
		SocketDir:     tmpSocketDir,
		SkipUserGroup: true,
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
		SocketDir:     tmpSocketDir,
		SkipUserGroup: true,
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
		SkipUserGroup: true,
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

func TestUninstall_PurgeFailureIsRecordedButCleanupContinues(t *testing.T) {
	tmpRunPath := t.TempDir()
	tmpSocketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpRunPath, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed run path: %v", err)
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
		SocketDir:     tmpSocketDir,
		SkipUserGroup: true,
	})
	if err == nil {
		t.Fatalf("expected error from failing realm purge, got nil")
	}
	if !strings.Contains(err.Error(), consts.KukeSystemRealmName) {
		t.Errorf("error %q does not mention failing realm %q", err, consts.KukeSystemRealmName)
	}

	// The cleanup steps must still have run despite the realm failure.
	if !report.RunPathRemove {
		t.Errorf("run path not removed after a realm purge failure (expected best-effort cleanup)")
	}
	// And the failing realm's outcome must be in the report so callers can
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
		SocketDir:     tmpSocketDir,
		DaemonStopper: stopper,
		SkipUserGroup: true,
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
		SkipUserGroup: true,
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
