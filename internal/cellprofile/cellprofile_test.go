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

package cellprofile_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const claudeProfile = `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: claude-cell
spec:
  realm: default
  space: agents
  stack: claude
  cell:
    tty:
      default: work
    containers:
      - id: root
        root: true
        image: registry.eminwux.com/busybox:latest
        command: sleep
        args:
          - "3600"
      - id: work
        attachable: true
        image: registry.eminwux.com/claude-runner:latest
        command: /bin/bash
        workingDir: /workspace
`

func writeProfile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write profile %q: %v", path, err)
	}
}

func TestLoad_ByFilename(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "claude-cell.yaml", claudeProfile)

	got, err := cellprofile.Load(dir, "claude-cell")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Metadata.Name != "claude-cell" {
		t.Errorf("metadata.name=%q want claude-cell", got.Metadata.Name)
	}
	if got.Spec.Realm != "default" {
		t.Errorf("spec.realm=%q want default", got.Spec.Realm)
	}
	if got.Spec.Cell.Tty == nil || got.Spec.Cell.Tty.Default != "work" {
		t.Errorf("cell.tty.default not preserved: %+v", got.Spec.Cell.Tty)
	}
	if len(got.Spec.Cell.Containers) != 2 {
		t.Errorf("containers=%d want 2", len(got.Spec.Cell.Containers))
	}
}

func TestLoad_ByMetadataName_FilenameMismatch(t *testing.T) {
	// File named differently than metadata.name. Profile is still findable
	// because the fallback scan keys on metadata.name.
	dir := t.TempDir()
	writeProfile(t, dir, "renamed.yaml", claudeProfile)

	got, err := cellprofile.Load(dir, "claude-cell")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Metadata.Name != "claude-cell" {
		t.Errorf("metadata.name=%q want claude-cell", got.Metadata.Name)
	}
}

func TestLoad_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := cellprofile.Load(dir, "missing")
	if !errors.Is(err, errdefs.ErrProfileNotFound) {
		t.Fatalf("err=%v want ErrProfileNotFound", err)
	}
	if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), dir) {
		t.Errorf("err %q must name profile and dir", err)
	}
}

func TestLoad_DirMissing_NotFound(t *testing.T) {
	// ENOENT on the dir resolves to a profile-not-found error, not a hard
	// filesystem failure: a fresh user with no profiles.d/ should hit the
	// same error path as someone who has the dir but no matching file.
	dir := filepath.Join(t.TempDir(), "absent")
	_, err := cellprofile.Load(dir, "claude-cell")
	if !errors.Is(err, errdefs.ErrProfileNotFound) {
		t.Fatalf("err=%v want ErrProfileNotFound", err)
	}
}

func TestLoad_WrongKind_Rejected(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "bad.yaml", `apiVersion: v1beta1
kind: Cell
metadata:
  name: bad
`)
	_, err := cellprofile.Load(dir, "bad")
	if err == nil {
		t.Fatal("Load returned nil, want ErrProfileInvalid (or NotFound after fallthrough)")
	}
}

func TestLoad_RejectsCellID(t *testing.T) {
	// Materialized cell names are always generated, so a hardcoded spec.cell.id
	// would silently produce N cells sharing one ID. Reject the field at load.
	dir := t.TempDir()
	writeProfile(t, dir, "with-id.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: with-id
spec:
  cell:
    id: pinned
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
`)
	_, err := cellprofile.Load(dir, "with-id")
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "spec.cell.id") {
		t.Errorf("err %q must name spec.cell.id", err)
	}
}

func TestList_SkipsBrokenAndNonYAML(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "claude-cell.yaml", claudeProfile)
	writeProfile(t, dir, "junk.yaml", "not: [valid")
	writeProfile(t, dir, "readme.txt", "ignored")
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}

	profiles, err := cellprofile.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(profiles) != 1 || profiles[0].Metadata.Name != "claude-cell" {
		t.Errorf("got %+v, want exactly [claude-cell]", profiles)
	}
}

func TestResolveDir_EnvOverride(t *testing.T) {
	t.Setenv(cellprofile.EnvProfilesDir, "/tmp/custom-profiles")
	dir, err := cellprofile.ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	if dir != "/tmp/custom-profiles" {
		t.Errorf("dir=%q want /tmp/custom-profiles", dir)
	}
}

func TestResolveDir_DefaultsUnderHome(t *testing.T) {
	t.Setenv(cellprofile.EnvProfilesDir, "")
	t.Setenv("HOME", "/tmp/fake-home")
	dir, err := cellprofile.ResolveDir()
	if err != nil {
		t.Fatalf("ResolveDir: %v", err)
	}
	want := filepath.Join("/tmp/fake-home", cellprofile.DefaultDirSuffix)
	if dir != want {
		t.Errorf("dir=%q want %q", dir, want)
	}
}

func TestMaterialize_DefaultPrefix(t *testing.T) {
	// Without spec.prefix the prefix defaults to metadata.name; the cell name
	// is `<metadata.name>-<6hex>` and the realm/space/stack triple flows
	// through verbatim.
	profile := &v1beta1.CellProfileDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellProfile,
		Metadata:   v1beta1.CellProfileMetadata{Name: "claude-cell"},
		Spec: v1beta1.CellProfileSpec{
			Realm: "default",
			Space: "agents",
			Stack: "claude",
			Cell: v1beta1.CellSpec{
				Containers: []v1beta1.ContainerSpec{
					{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
				},
			},
		},
	}

	doc, err := cellprofile.Materialize(profile)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	if !strings.HasPrefix(doc.Metadata.Name, "claude-cell-") {
		t.Errorf("metadata.name=%q want prefix claude-cell-", doc.Metadata.Name)
	}
	suffix := strings.TrimPrefix(doc.Metadata.Name, "claude-cell-")
	if len(suffix) != 6 {
		t.Errorf("suffix=%q len=%d want 6 lowercase hex chars", suffix, len(suffix))
	}
	if doc.Kind != v1beta1.KindCell || doc.APIVersion != v1beta1.APIVersionV1Beta1 {
		t.Errorf("apiVersion/kind=%q/%q want v1beta1/Cell", doc.APIVersion, doc.Kind)
	}
	if doc.Spec.RealmID != "default" || doc.Spec.SpaceID != "agents" || doc.Spec.StackID != "claude" {
		t.Errorf("location=%q/%q/%q want default/agents/claude",
			doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID)
	}
	if doc.Spec.ID != doc.Metadata.Name {
		t.Errorf("spec.id=%q want %q (mirrors generated cell name)", doc.Spec.ID, doc.Metadata.Name)
	}
	if got := doc.Metadata.Labels[cellprofile.LabelProfile]; got != "claude-cell" {
		t.Errorf("labels[%q]=%q want claude-cell (profile-of-origin label)",
			cellprofile.LabelProfile, got)
	}
}

func TestMaterialize_DefaultPrefix_GeneratesUniqueCells(t *testing.T) {
	// CellProfile is always a template: even without spec.prefix, successive
	// calls must produce distinct names sharing the metadata.name prefix.
	profile := &v1beta1.CellProfileDoc{
		Metadata: v1beta1.CellProfileMetadata{Name: "claude-cell"},
		Spec: v1beta1.CellProfileSpec{
			Cell: v1beta1.CellSpec{
				Containers: []v1beta1.ContainerSpec{
					{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
				},
			},
		},
	}

	const invocations = 3
	seen := make(map[string]struct{}, invocations)
	for i := range invocations {
		doc, err := cellprofile.Materialize(profile)
		if err != nil {
			t.Fatalf("Materialize #%d: %v", i, err)
		}
		name := doc.Metadata.Name
		if !strings.HasPrefix(name, "claude-cell-") {
			t.Errorf("name=%q want prefix claude-cell-", name)
		}
		if _, dup := seen[name]; dup {
			t.Errorf("name=%q repeated across invocations", name)
		}
		seen[name] = struct{}{}
		if got := doc.Metadata.Labels[cellprofile.LabelProfile]; got != "claude-cell" {
			t.Errorf("labels[%q]=%q want claude-cell", cellprofile.LabelProfile, got)
		}
	}
}

func TestMaterialize_PrefixOverride_GeneratesUniqueCells(t *testing.T) {
	profile := &v1beta1.CellProfileDoc{
		Metadata: v1beta1.CellProfileMetadata{Name: "claude"},
		Spec: v1beta1.CellProfileSpec{
			Prefix: "agent",
			Cell: v1beta1.CellSpec{
				Containers: []v1beta1.ContainerSpec{
					{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
				},
			},
		},
	}

	const invocations = 3
	seen := make(map[string]struct{}, invocations)
	for i := range invocations {
		doc, err := cellprofile.Materialize(profile)
		if err != nil {
			t.Fatalf("Materialize #%d: %v", i, err)
		}
		name := doc.Metadata.Name
		if !strings.HasPrefix(name, "agent-") {
			t.Errorf("name=%q want prefix agent- (spec.prefix override)", name)
		}
		suffix := strings.TrimPrefix(name, "agent-")
		if len(suffix) != 6 {
			t.Errorf("suffix=%q len=%d want 6 lowercase hex chars", suffix, len(suffix))
		}
		const hexChars = "0123456789abcdef"
		if strings.Trim(suffix, hexChars) != "" {
			t.Errorf("suffix=%q contains non-lowercase-hex bytes", suffix)
		}
		if _, dup := seen[name]; dup {
			t.Errorf("name=%q repeated across invocations (entropy too narrow?)", name)
		}
		seen[name] = struct{}{}
		if doc.Spec.ID != name {
			t.Errorf("spec.id=%q want %q (mirrors generated cell name)", doc.Spec.ID, name)
		}
		if got := doc.Metadata.Labels[cellprofile.LabelProfile]; got != "claude" {
			t.Errorf("labels[%q]=%q want claude (profile-of-origin label tracks metadata.name, not prefix)",
				cellprofile.LabelProfile, got)
		}
	}
}

func TestMaterialize_LabelsMerged(t *testing.T) {
	// User-set labels on the profile must survive into the cell, alongside
	// the system-injected kukeon.io/profile=<name> label. Conflicts on the
	// reserved key resolve to the system value (the label is identity, not
	// user-controlled metadata).
	profile := &v1beta1.CellProfileDoc{
		Metadata: v1beta1.CellProfileMetadata{
			Name: "claude",
			Labels: map[string]string{
				"team":                   "agents",
				"owner":                  "ops",
				cellprofile.LabelProfile: "should-be-overwritten",
			},
		},
		Spec: v1beta1.CellProfileSpec{
			Cell: v1beta1.CellSpec{
				Containers: []v1beta1.ContainerSpec{
					{ID: "work", Image: "registry.eminwux.com/busybox:latest"},
				},
			},
		},
	}

	doc, err := cellprofile.Materialize(profile)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	labels := doc.Metadata.Labels
	if labels["team"] != "agents" {
		t.Errorf("labels[team]=%q want agents (user label preserved)", labels["team"])
	}
	if labels["owner"] != "ops" {
		t.Errorf("labels[owner]=%q want ops (user label preserved)", labels["owner"])
	}
	if labels[cellprofile.LabelProfile] != "claude" {
		t.Errorf("labels[%q]=%q want claude (system label wins on conflict)",
			cellprofile.LabelProfile, labels[cellprofile.LabelProfile])
	}
}
