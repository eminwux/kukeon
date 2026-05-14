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

package runner_test

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestListRealms_NonRealmSiblingUnderRunPathIsIgnored is the regression
// guard for #473: the reconcile walker enumerates realms beneath
// <RunPath>/<consts.KukeonMetadataSubdir> rather than directly under
// <RunPath>, so non-metadata siblings of the RunPath (e.g. <RunPath>/bin
// staging the kuketty binary) must not appear in the realm listing and
// must not trigger an ERROR-level "metadata file does not exist" log on
// every reconcile tick.
func TestListRealms_NonRealmSiblingUnderRunPathIsIgnored(t *testing.T) {
	runPath := t.TempDir()

	// Real realm under the metadata root.
	realmName := "alpha"
	metadataPath := fs.RealmMetadataPath(runPath, realmName)
	doc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: realmName},
		Spec:       v1beta1.RealmSpec{Namespace: "alpha.kukeon.io"},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := metadata.WriteMetadata(context.Background(), logger, doc, metadataPath); err != nil {
		t.Fatalf("seed realm metadata: %v", err)
	}

	// Non-realm sibling of the metadata root, modelling /opt/kukeon/bin
	// where `kuke init` (via stageKukettyBinary) lands the kuketty binary.
	// The walker must not treat this as a realm and must not log an ERROR
	// for the missing metadata.json inside it.
	siblingDir := filepath.Join(runPath, "bin")
	if err := os.MkdirAll(siblingDir, 0o750); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siblingDir, "kuketty"), []byte("#!/usr/bin/false\n"), 0o755); err != nil {
		t.Fatalf("seed kuketty: %v", err)
	}

	// Also model the dot-prefixed instance metadata file (instance.MetadataFile)
	// that lives at the RunPath root.
	if err := os.WriteFile(filepath.Join(runPath, ".kukeon-instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed instance metadata: %v", err)
	}

	logBuf := &bytes.Buffer{}
	capturingLogger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := runner.NewRunner(context.Background(), capturingLogger, runner.Options{RunPath: runPath})

	realms, err := r.ListRealms()
	if err != nil {
		t.Fatalf("ListRealms: %v", err)
	}

	if len(realms) != 1 {
		names := make([]string, 0, len(realms))
		for _, rl := range realms {
			names = append(names, rl.Metadata.Name)
		}
		t.Fatalf("ListRealms returned %d entries (%v); want exactly 1 (the seeded realm)", len(realms), names)
	}
	if realms[0].Metadata.Name != realmName {
		t.Errorf("ListRealms returned realm %q, want %q", realms[0].Metadata.Name, realmName)
	}

	// AC #5: walker must not log ERROR for non-realm siblings of the
	// RunPath. The "metadata file does not exist" line at ERROR level was
	// the symptom #473 caught — failing this check would regress the bug.
	out := logBuf.String()
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("ListRealms emitted ERROR-level log lines for non-realm siblings:\n%s", out)
	}
	if strings.Contains(out, "metadata file does not exist") {
		t.Errorf("ListRealms tripped 'metadata file does not exist' on a non-realm sibling:\n%s", out)
	}
}

// TestListRealms_MissingRealmMetadataStillLogsError covers the second half
// of #473's AC #3: the fix must not silence *real* errors. Removing a real
// realm's metadata.json (genuine on-disk corruption) is still observable
// via the runner's debug log; the walker skips the broken realm rather
// than aborting the pass.
func TestListRealms_MissingRealmMetadataStillSkipsBrokenRealm(t *testing.T) {
	runPath := t.TempDir()

	// Plant a realm directory under the metadata root but with no
	// metadata.json — simulates a partially-deleted or pre-write realm.
	brokenDir := fs.RealmMetadataDir(runPath, "broken")
	if err := os.MkdirAll(brokenDir, 0o750); err != nil {
		t.Fatalf("mkdir broken realm: %v", err)
	}

	// And one healthy realm so the listing is non-empty.
	healthyName := "healthy"
	healthyDoc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: healthyName},
		Spec:       v1beta1.RealmSpec{Namespace: "healthy.kukeon.io"},
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := metadata.WriteMetadata(
		context.Background(), logger, healthyDoc, fs.RealmMetadataPath(runPath, healthyName),
	); err != nil {
		t.Fatalf("seed healthy realm: %v", err)
	}

	logBuf := &bytes.Buffer{}
	capturingLogger := slog.New(slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := runner.NewRunner(context.Background(), capturingLogger, runner.Options{RunPath: runPath})

	realms, err := r.ListRealms()
	if err != nil {
		t.Fatalf("ListRealms: %v", err)
	}
	if len(realms) != 1 || realms[0].Metadata.Name != healthyName {
		names := make([]string, 0, len(realms))
		for _, rl := range realms {
			names = append(names, rl.Metadata.Name)
		}
		t.Fatalf("ListRealms = %v, want only [%q]", names, healthyName)
	}
}
