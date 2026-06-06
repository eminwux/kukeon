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

package teamhost_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/teamhost"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

func claudeHarness(seeds []model.HarnessSeed) *model.Harness {
	return &model.Harness{
		APIVersion: model.APIVersionV1,
		Kind:       model.KindHarness,
		Metadata:   model.Metadata{Name: "claude"},
		Spec: model.HarnessSpec{
			SkillPath:  "/skills",
			MakeTarget: "claude",
			Template:   "blueprint.tmpl.yaml",
			Seeds:      seeds,
		},
	}
}

func defaultSeedHarness() *model.Harness {
	return claudeHarness([]model.HarnessSeed{
		{Path: "${TEAM_ROOT}/${HARNESS}.json", Mode: 0o644, Content: "{}\n"},
		{Path: "${HARNESS}.json-root", Mode: 0o644, Content: "{}\n"},
	})
}

// TestProvisionTeamCreatesStateDirsAndSeeds is the AC's clean-host case:
// every roster pair gets a state dir, every harness seed is written, and
// the per-team root materializes with mode 0o700.
func TestProvisionTeamCreatesStateDirsAndSeeds(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")
	in := teamhost.ProvisionInputs{
		TeamRoot: teamRoot,
		Pairs: []teamhost.SeedPair{
			{Role: "dev", Harness: "claude"},
			{Role: "pm", Harness: "claude"},
		},
		Harnesses: map[string]*model.Harness{"claude": defaultSeedHarness()},
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("ProvisionTeam: %v", err)
	}

	for _, role := range []string{"dev", "pm"} {
		dir := l.RoleHarnessStateDir("dezot", role, "claude")
		info, err := os.Stat(dir)
		if err != nil {
			t.Errorf("state dir %q not created: %v", dir, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a dir", dir)
		}
		if perm := info.Mode().Perm(); perm != 0o700 {
			t.Errorf("state dir %q perm = %#o, want 0o700", dir, perm)
		}
	}

	for _, want := range []string{
		l.HarnessSeedPath("dezot", "claude", ""),
		l.HarnessSeedPath("dezot", "claude", "root"),
	} {
		info, err := os.Stat(want)
		if err != nil {
			t.Errorf("seed %q not written: %v", want, err)
			continue
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Errorf("seed %q perm = %#o, want 0o644", want, perm)
		}
		got, readErr := os.ReadFile(want)
		if readErr != nil {
			t.Errorf("read seed %q: %v", want, readErr)
		}
		if string(got) != "{}\n" {
			t.Errorf("seed %q content = %q, want %q", want, got, "{}\n")
		}
	}
}

// TestProvisionTeamIdempotent re-runs ProvisionTeam against an already-
// provisioned tree and checks that hand-edited seed files survive — the
// AC's "re-running is a no-op for state dirs and seeds; hand-edited seed
// files are never overwritten" invariant.
func TestProvisionTeamIdempotent(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")
	in := teamhost.ProvisionInputs{
		TeamRoot: teamRoot,
		Pairs:    []teamhost.SeedPair{{Role: "dev", Harness: "claude"}},
		Harnesses: map[string]*model.Harness{"claude": claudeHarness([]model.HarnessSeed{
			{Path: "${TEAM_ROOT}/${HARNESS}.json", Mode: 0o644, Content: "{}\n"},
		})},
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("first ProvisionTeam: %v", err)
	}

	seedPath := l.HarnessSeedPath("dezot", "claude", "")
	const sentinel = `{"oauth_token": "edited-by-operator"}`
	if err := os.WriteFile(seedPath, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("seed hand-edit: %v", err)
	}

	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("re-run ProvisionTeam: %v", err)
	}
	got, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatalf("read seed after re-run: %v", err)
	}
	if string(got) != sentinel {
		t.Errorf("re-run overwrote hand-edited seed: %q", got)
	}

	// State dir still readable after re-run.
	if _, statErr := os.Stat(l.RoleHarnessStateDir("dezot", "dev", "claude")); statErr != nil {
		t.Errorf("state dir lost on re-run: %v", statErr)
	}
}

// TestProvisionTeamTwoProjectsIsolated covers the AC: dezot and kukeon
// running provisioning against the same layout get isolated state trees
// with no cross-contamination.
func TestProvisionTeamTwoProjectsIsolated(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	h := defaultSeedHarness()

	for _, team := range []string{"dezot", "kukeon"} {
		in := teamhost.ProvisionInputs{
			TeamRoot:  l.TeamDir(team),
			Pairs:     []teamhost.SeedPair{{Role: "dev", Harness: "claude"}},
			Harnesses: map[string]*model.Harness{"claude": h},
		}
		if err := teamhost.ProvisionTeam(in); err != nil {
			t.Fatalf("ProvisionTeam(%q): %v", team, err)
		}
	}

	for _, team := range []string{"dezot", "kukeon"} {
		stateDir := l.RoleHarnessStateDir(team, "dev", "claude")
		if _, err := os.Stat(stateDir); err != nil {
			t.Errorf("%q state dir not created: %v", team, err)
		}
		if _, err := os.Stat(l.HarnessSeedPath(team, "claude", "")); err != nil {
			t.Errorf("%q claude.json not seeded: %v", team, err)
		}
	}

	// Tamper one team's seed; the other's must remain byte-identical.
	const tampered = "tampered-by-test\n"
	dezotSeed := l.HarnessSeedPath("dezot", "claude", "")
	kukeonSeedBefore, err := os.ReadFile(l.HarnessSeedPath("kukeon", "claude", ""))
	if err != nil {
		t.Fatalf("read kukeon seed: %v", err)
	}
	if writeErr := os.WriteFile(dezotSeed, []byte(tampered), 0o644); writeErr != nil {
		t.Fatalf("tamper dezot seed: %v", writeErr)
	}
	kukeonSeedAfter, err := os.ReadFile(l.HarnessSeedPath("kukeon", "claude", ""))
	if err != nil {
		t.Fatalf("read kukeon seed after tamper: %v", err)
	}
	if !bytes.Equal(kukeonSeedBefore, kukeonSeedAfter) {
		t.Errorf("kukeon seed disturbed by dezot edit:\nbefore=%q\nafter=%q",
			kukeonSeedBefore, kukeonSeedAfter)
	}
}

// TestProvisionTeamDryRunWritesNothing pins the dry-run inversion: nothing
// hits disk, the announcement is on Out.
func TestProvisionTeamDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")
	var out bytes.Buffer
	in := teamhost.ProvisionInputs{
		TeamRoot:  teamRoot,
		Pairs:     []teamhost.SeedPair{{Role: "dev", Harness: "claude"}},
		Harnesses: map[string]*model.Harness{"claude": defaultSeedHarness()},
		DryRun:    true,
		Out:       &out,
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("ProvisionTeam dry-run: %v", err)
	}

	if _, err := os.Stat(teamRoot); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dry-run created team root: stat err=%v", err)
	}
	body := out.String()
	for _, want := range []string{
		filepath.Join(teamRoot, "dev-claude"),
		l.HarnessSeedPath("dezot", "claude", ""),
		l.HarnessSeedPath("dezot", "claude", "root"),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("dry-run output missing path %q:\n%s", want, body)
		}
	}
}

// TestProvisionTeamHostConfigCopiesOnce covers the per-team config seed:
// the first run copies $HOME/.config/. → HostConfigDir(team); a re-run
// against a populated config dir is a no-op even if the source has new
// files.
func TestProvisionTeamHostConfigCopiesOnce(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")

	homeConfig := filepath.Join(t.TempDir(), ".config")
	if err := os.MkdirAll(filepath.Join(homeConfig, "kuke"), 0o755); err != nil {
		t.Fatalf("seed home config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(homeConfig, "kuke", "settings.yaml"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	in := teamhost.ProvisionInputs{
		TeamRoot:      teamRoot,
		HomeConfigDir: homeConfig,
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("first ProvisionTeam: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(l.HostConfigDir("dezot"), "kuke", "settings.yaml"))
	if err != nil {
		t.Fatalf("config file not copied: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("copied content = %q", got)
	}

	// Add a new file in $HOME/.config; the re-run must NOT copy it (target
	// dir is no longer empty).
	if writeErr := os.WriteFile(filepath.Join(homeConfig, "kuke", "extra.yaml"), []byte("extra\n"), 0o644); writeErr != nil {
		t.Fatalf("add extra: %v", writeErr)
	}
	if rerunErr := teamhost.ProvisionTeam(in); rerunErr != nil {
		t.Fatalf("re-run ProvisionTeam: %v", rerunErr)
	}
	if _, statErr := os.Stat(filepath.Join(l.HostConfigDir("dezot"), "kuke", "extra.yaml")); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Errorf("re-run copied a new file into a populated config dir: err=%v", statErr)
	}
}

// TestProvisionTeamHostConfigMissingSourceSilentSkip pins the silent-skip
// invariant: a CI host without a $HOME/.config produces no error and no
// per-team config dir.
func TestProvisionTeamHostConfigMissingSourceSilentSkip(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")

	in := teamhost.ProvisionInputs{
		TeamRoot:      teamRoot,
		HomeConfigDir: filepath.Join(t.TempDir(), "absent"),
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("ProvisionTeam: %v", err)
	}
	if _, statErr := os.Stat(l.HostConfigDir("dezot")); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("config dir created without a source: err=%v", statErr)
	}
}

// TestProvisionTeamSeedPathEscapeRejected confirms the defense-in-depth
// guard: a harness seed whose expanded path escapes the per-team root is
// refused via ErrTeamHarnessSeedPathEscapes — the seed is never written.
func TestProvisionTeamSeedPathEscapeRejected(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")

	in := teamhost.ProvisionInputs{
		TeamRoot: teamRoot,
		Pairs:    []teamhost.SeedPair{{Role: "dev", Harness: "claude"}},
		Harnesses: map[string]*model.Harness{"claude": claudeHarness([]model.HarnessSeed{
			{Path: "../escape.json", Mode: 0o644, Content: "{}"},
		})},
	}
	err := teamhost.ProvisionTeam(in)
	if !errors.Is(err, errdefs.ErrTeamHarnessSeedPathEscapes) {
		t.Fatalf("err = %v, want ErrTeamHarnessSeedPathEscapes", err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(teamRoot), "escape.json")); !errors.Is(
		statErr,
		os.ErrNotExist,
	) {
		t.Errorf("escape seed was written: err=%v", statErr)
	}
}

// TestProvisionTeamSeedDefaultMode pins the AC's "mode defaults to 0o644"
// behaviour — a seed with no explicit Mode is still written at 0o644
// regardless of process umask.
func TestProvisionTeamSeedDefaultMode(t *testing.T) {
	t.Parallel()
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)

	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")
	in := teamhost.ProvisionInputs{
		TeamRoot: teamRoot,
		Pairs:    []teamhost.SeedPair{{Role: "dev", Harness: "claude"}},
		Harnesses: map[string]*model.Harness{"claude": claudeHarness([]model.HarnessSeed{
			{Path: "${HARNESS}.json", Content: "{}"},
		})},
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("ProvisionTeam: %v", err)
	}
	info, err := os.Stat(l.HarnessSeedPath("dezot", "claude", ""))
	if err != nil {
		t.Fatalf("seed not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("seed perm = %#o, want 0o644 (default)", perm)
	}
}

// TestProvisionTeamEmptyTeamRoot guards the precondition: an empty
// TeamRoot is a programmer error and surfaces as a top-level error.
func TestProvisionTeamEmptyTeamRoot(t *testing.T) {
	t.Parallel()
	err := teamhost.ProvisionTeam(teamhost.ProvisionInputs{TeamRoot: ""})
	if err == nil {
		t.Fatal("ProvisionTeam(\"\") returned nil error")
	}
}

// TestProvisionTeamUnknownHarnessSkippedSeeds covers the defense-in-depth
// path: a roster pair referencing a harness absent from Harnesses still
// gets a state dir but no seeds (no panic).
func TestProvisionTeamUnknownHarnessSkippedSeeds(t *testing.T) {
	t.Parallel()
	l := teamhost.NewLayout(filepath.Join(t.TempDir(), ".kuke"))
	teamRoot := l.TeamDir("dezot")
	in := teamhost.ProvisionInputs{
		TeamRoot:  teamRoot,
		Pairs:     []teamhost.SeedPair{{Role: "dev", Harness: "ghost"}},
		Harnesses: map[string]*model.Harness{}, // empty
	}
	if err := teamhost.ProvisionTeam(in); err != nil {
		t.Fatalf("ProvisionTeam: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(teamRoot, "dev-ghost")); statErr != nil {
		t.Errorf("state dir not created for unknown harness: %v", statErr)
	}
}
