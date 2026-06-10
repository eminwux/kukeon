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

//nolint:testpackage // exercises *Exec.PurgeRealm against an in-package ctr.Client fake
package runner

import (
	"errors"
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/consts"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestPurgeRealm_DrainsAndDeletesBuildKitHistoryCompanion pins issue #1183:
// runner.PurgeRealm tears down the realm namespace's BuildKit history-store
// companion (<namespace>_history) with the same drain-then-delete the realm
// namespace gets, so a full uninstall (which routes every realm through this
// path) leaves no `*.kukeon.io*` namespaces behind.
func TestPurgeRealm_DrainsAndDeletesBuildKitHistoryCompanion(t *testing.T) {
	var deleted, drained []string
	fake := &deleteCellFakeClient{
		deleteNamespaceFn: func(ns string) error {
			deleted = append(deleted, ns)
			return nil
		},
		cleanupNamespaceFn: func(ns, _ string) error {
			drained = append(drained, ns)
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)

	const namespace = "kuke-system.kukeon.io"
	historyNS := consts.BuildKitHistoryNamespace(namespace)

	// No metadata is seeded, so GetRealm returns ErrRealmNotFound and PurgeRealm
	// falls back to the provided realm — exactly the partial-uninstall path the
	// companion leak was observed on.
	removed, err := r.PurgeRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "kuke-system"},
		Spec:     intmodel.RealmSpec{Namespace: namespace},
	})
	if err != nil {
		t.Fatalf("PurgeRealm returned error: %v", err)
	}
	if !removed {
		t.Error("expected namespaceRemoved=true on a clean purge")
	}

	if !slices.Contains(deleted, namespace) {
		t.Errorf("realm namespace %q not deleted; deleted=%v", namespace, deleted)
	}
	if !slices.Contains(deleted, historyNS) {
		t.Errorf("history companion %q not deleted; deleted=%v", historyNS, deleted)
	}
	if !slices.Contains(drained, historyNS) {
		t.Errorf("history companion %q not drained before delete; drained=%v", historyNS, drained)
	}
}

// TestPurgeRealm_HistoryCompanionFailureFoldsIntoNamespaceRemoved confirms a
// stranded history companion blocks the uninstall half-cleaned-host gate: when
// the companion fails to delete, namespaceRemoved is false even though the
// realm namespace itself was removed, and the error names the companion.
func TestPurgeRealm_HistoryCompanionFailureFoldsIntoNamespaceRemoved(t *testing.T) {
	const namespace = "kuke-system.kukeon.io"
	historyNS := consts.BuildKitHistoryNamespace(namespace)
	wantErr := errors.New("namespace not empty")

	fake := &deleteCellFakeClient{
		deleteNamespaceFn: func(ns string) error {
			if ns == historyNS {
				return wantErr
			}
			return nil
		},
	}
	r := newDeleteCellTestExec(t, fake)

	removed, err := r.PurgeRealm(intmodel.Realm{
		Metadata: intmodel.RealmMetadata{Name: "kuke-system"},
		Spec:     intmodel.RealmSpec{Namespace: namespace},
	})
	if removed {
		t.Error("expected namespaceRemoved=false when the history companion survives")
	}
	if err == nil {
		t.Fatal("expected an error naming the stranded history companion")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error chain should wrap the delete failure; got %v", err)
	}
}
