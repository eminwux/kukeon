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
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/metadata"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestListSpaces_AllRealms_NonRealmSiblingUnderRunPathIsIgnored is the
// regression guard for #500: when callers pass an empty realm filter,
// the runner must walk <RunPath>/<KukeonMetadataSubdir> (i.e.
// /opt/kukeon/data) rather than <RunPath> itself. Otherwise non-metadata
// siblings of the RunPath (e.g. /opt/kukeon/bin staging the kuketty
// binary, or the .kukeon-instance.json file) get enumerated as realms,
// the walker descends one level too shallow, and `kuke get spaces` /
// `kuke get stacks` / `kuke get cells` / `kuke get containers` return
// empty or the wrong rows.
//
// #489 fixed exactly this for ListRealms; #500 propagates the fix into
// the shared resolveRealmDirs helper. We exercise the empty-realm
// branch through ListSpaces (the smallest public consumer) and assert
// the seeded space is returned and the non-realm sibling is not.
func TestListSpaces_AllRealms_NonRealmSiblingUnderRunPathIsIgnored(t *testing.T) {
	runPath := t.TempDir()
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Seed one real realm under the metadata root, with one space inside.
	realmName := "alpha"
	realmDoc := v1beta1.RealmDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindRealm,
		Metadata:   v1beta1.RealmMetadata{Name: realmName},
		Spec:       v1beta1.RealmSpec{Namespace: "alpha.kukeon.io"},
	}
	if err := metadata.WriteMetadata(ctx, logger, realmDoc, fs.RealmMetadataPath(runPath, realmName)); err != nil {
		t.Fatalf("seed realm metadata: %v", err)
	}
	spaceName := "myspace"
	spaceDoc := v1beta1.SpaceDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindSpace,
		Metadata:   v1beta1.SpaceMetadata{Name: spaceName},
		Spec:       v1beta1.SpaceSpec{RealmID: realmName},
	}
	if err := metadata.WriteMetadata(ctx, logger, spaceDoc, fs.SpaceMetadataPath(runPath, realmName, spaceName)); err != nil {
		t.Fatalf("seed space metadata: %v", err)
	}

	// Plant the non-metadata siblings: <RunPath>/bin/kuketty and the
	// dot-prefixed instance metadata file at the RunPath root. These
	// must not be mistaken for realms.
	siblingDir := filepath.Join(runPath, "bin")
	if err := os.MkdirAll(siblingDir, 0o750); err != nil {
		t.Fatalf("mkdir sibling: %v", err)
	}
	if err := os.WriteFile(filepath.Join(siblingDir, "kuketty"), []byte("#!/usr/bin/false\n"), 0o755); err != nil {
		t.Fatalf("seed kuketty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runPath, ".kukeon-instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed instance metadata: %v", err)
	}

	r := runner.NewRunner(ctx, logger, runner.Options{RunPath: runPath})

	spaces, err := r.ListSpaces("")
	if err != nil {
		t.Fatalf("ListSpaces(\"\"): %v", err)
	}
	if len(spaces) != 1 {
		names := make([]string, 0, len(spaces))
		for _, s := range spaces {
			names = append(names, s.Metadata.Name+"@"+s.Spec.RealmName)
		}
		t.Fatalf("ListSpaces(\"\") returned %d entries (%v); want exactly 1 (the seeded space)", len(spaces), names)
	}
	if spaces[0].Metadata.Name != spaceName {
		t.Errorf("ListSpaces(\"\") returned space %q, want %q", spaces[0].Metadata.Name, spaceName)
	}
	if spaces[0].Spec.RealmName != realmName {
		t.Errorf("ListSpaces(\"\") returned realm %q, want %q", spaces[0].Spec.RealmName, realmName)
	}
}
