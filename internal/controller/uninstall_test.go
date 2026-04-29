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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
