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

package teamsecrets

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestKebabCase(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"ANTHROPIC_AUTH_TOKEN", "anthropic-auth-token"},
		{"OPENROUTER_API_KEY", "openrouter-api-key"},
		{"FOO", "foo"},
		{"already-kebab", "already-kebab"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := KebabCase(tc.in); got != tc.want {
			t.Errorf("KebabCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestScaffoldEnvFileCreatesWithMode0600(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "teams", "alpha")
	path := filepath.Join(dir, "secrets.env")

	created, err := ScaffoldEnvFile(path, []string{"FOO", "BAR"})
	if err != nil {
		t.Fatalf("ScaffoldEnvFile: %v", err)
	}
	if !created {
		t.Errorf("created = false, want true on first scaffold")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat scaffold: %v", err)
	}
	if mode := info.Mode().Perm(); mode != FilePerm {
		t.Errorf("file mode = %o, want %o", mode, FilePerm)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scaffold: %v", err)
	}
	// Sorted lexicographically — BAR first, then FOO.
	want := "BAR=\nFOO=\n"
	if string(raw) != want {
		t.Errorf("scaffold body = %q, want %q", raw, want)
	}
}

func TestScaffoldEnvFileNeverOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.env")
	if err := os.WriteFile(path, []byte("ANTHROPIC_AUTH_TOKEN=populated-by-operator\n"), 0o600); err != nil {
		t.Fatalf("seed populated file: %v", err)
	}

	created, err := ScaffoldEnvFile(path, []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"})
	if err != nil {
		t.Fatalf("ScaffoldEnvFile re-run: %v", err)
	}
	if created {
		t.Errorf("created = true, want false on re-run against populated file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read scaffold: %v", err)
	}
	if !strings.Contains(string(raw), "populated-by-operator") {
		t.Errorf("re-run overwrote populated file: %q", raw)
	}
}

func TestScaffoldEnvFileEmptyKeysIsNoOp(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.env")
	created, err := ScaffoldEnvFile(path, nil)
	if err != nil {
		t.Fatalf("ScaffoldEnvFile nil keys: %v", err)
	}
	if created {
		t.Errorf("created = true, want false on empty-keys no-op")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("empty-keys scaffold still wrote file: err=%v", statErr)
	}
}

func TestLoadEnvFileMissingReturnsEmpty(t *testing.T) {
	t.Parallel()
	got, err := LoadEnvFile(filepath.Join(t.TempDir(), "absent.env"))
	if err != nil {
		t.Fatalf("LoadEnvFile missing: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file = %v, want empty map", got)
	}
}

func TestLoadEnvFileParsesAndIgnoresCommentsAndBlanks(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "secrets.env")
	body := strings.Join([]string{
		"# comment line",
		"",
		"  # leading-ws comment",
		"FOO=bar",
		"   BAZ=qux value with spaces",
		"NO_EQUALS_SIGN",
		"EMPTY_VALUE=",
		"=value-without-key",
		"WITH_EQUALS_IN_VALUE=a=b=c",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	want := map[string]string{
		"FOO":                  "bar",
		"BAZ":                  "qux value with spaces",
		"EMPTY_VALUE":          "",
		"WITH_EQUALS_IN_VALUE": "a=b=c",
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d: %#v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("got[%q] = %q, want %q", k, got[k], v)
		}
	}
}

func TestMergePerTeamNonEmptyWins(t *testing.T) {
	t.Parallel()
	shared := map[string]string{"A": "shared-a", "B": "shared-b", "C": "shared-c"}
	perTeam := map[string]string{"B": "team-b", "D": "team-d"}
	got := Merge(shared, perTeam)
	want := map[string]string{
		"A": "shared-a",
		"B": "team-b", // per-team non-empty wins
		"C": "shared-c",
		"D": "team-d",
	}
	if len(got) != len(want) {
		t.Errorf("merged len = %d, want %d: %#v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("merged[%q] = %q, want %q", k, got[k], v)
		}
	}
	// Inputs unmutated.
	if shared["B"] != "shared-b" {
		t.Errorf("Merge mutated shared input: %q", shared["B"])
	}
}

// TestMergePerTeamEmptyFallsBackToShared pins the scaffold-friendly
// semantics: an empty per-team value is a placeholder, not an override.
// Re-init writes one empty KEY= line per role-declared secret into
// every per-team file; if those scaffold lines were treated as active
// overrides, the shared default would be null-out on every re-init.
func TestMergePerTeamEmptyFallsBackToShared(t *testing.T) {
	t.Parallel()
	shared := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "shared-anth",
		"OPENROUTER_API_KEY":   "shared-or",
	}
	perTeam := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "", // empty placeholder — defer to shared
		"OPENROUTER_API_KEY":   "team-or",
		"TEAM_ONLY_KEY":        "", // empty + no shared → still empty
	}
	got := Merge(shared, perTeam)
	if got["ANTHROPIC_AUTH_TOKEN"] != "shared-anth" {
		t.Errorf("empty per-team should fall back to shared, got %q", got["ANTHROPIC_AUTH_TOKEN"])
	}
	if got["OPENROUTER_API_KEY"] != "team-or" {
		t.Errorf("non-empty per-team should win, got %q", got["OPENROUTER_API_KEY"])
	}
	if v, ok := got["TEAM_ONLY_KEY"]; !ok || v != "" {
		t.Errorf("team-only empty key should carry as empty, got (%q, %v)", v, ok)
	}
}

func TestRenderEmitsNonEmptyValuesOnly(t *testing.T) {
	t.Parallel()
	merged := map[string]string{
		"ANTHROPIC_AUTH_TOKEN": "secret-anth",
		"OPENROUTER_API_KEY":   "",
		"UNREFERENCED_KEY":     "should-not-render",
	}
	needs := []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY", "MISSING_KEY"}
	secrets, empties := Render(merged, needs, "default")

	if len(secrets) != 1 {
		t.Fatalf("rendered %d secrets, want 1: %+v", len(secrets), secrets)
	}
	if secrets[0].Metadata.Name != "anthropic-auth-token" {
		t.Errorf("name = %q, want kebab-cased", secrets[0].Metadata.Name)
	}
	if secrets[0].Metadata.Realm != "default" {
		t.Errorf("realm = %q, want default", secrets[0].Metadata.Realm)
	}
	if secrets[0].Spec.Data != "secret-anth" {
		t.Errorf("data = %q, want secret-anth", secrets[0].Spec.Data)
	}
	if secrets[0].Kind != v1beta1.KindSecret {
		t.Errorf("kind = %q, want %q", secrets[0].Kind, v1beta1.KindSecret)
	}
	if secrets[0].APIVersion != v1beta1.APIVersionV1Beta1 {
		t.Errorf("apiVersion = %q, want %q", secrets[0].APIVersion, v1beta1.APIVersionV1Beta1)
	}

	sort.Strings(empties)
	wantEmpty := []string{"MISSING_KEY", "OPENROUTER_API_KEY"}
	if len(empties) != len(wantEmpty) {
		t.Fatalf("empties = %v, want %v", empties, wantEmpty)
	}
	for i, k := range wantEmpty {
		if empties[i] != k {
			t.Errorf("empties[%d] = %q, want %q", i, empties[i], k)
		}
	}
}

func TestRenderUnreferencedKeyDropped(t *testing.T) {
	t.Parallel()
	merged := map[string]string{
		"OPENROUTER_API_KEY":  "from-shared",
		"OPERATOR_NOTES_FILE": "not-a-secret-leak",
	}
	needs := []string{"OPENROUTER_API_KEY"}
	secrets, _ := Render(merged, needs, "default")
	if len(secrets) != 1 {
		t.Fatalf("rendered %d, want 1", len(secrets))
	}
	if secrets[0].Metadata.Name != "openrouter-api-key" {
		t.Errorf("name = %q, want openrouter-api-key", secrets[0].Metadata.Name)
	}
}

func TestRenderIsSortedByName(t *testing.T) {
	t.Parallel()
	merged := map[string]string{
		"ZZZ_TOKEN":    "z",
		"ALPHA_TOKEN":  "a",
		"MIDDLE_TOKEN": "m",
	}
	needs := []string{"ZZZ_TOKEN", "ALPHA_TOKEN", "MIDDLE_TOKEN"}
	secrets, _ := Render(merged, needs, "default")
	if len(secrets) != 3 {
		t.Fatalf("rendered %d, want 3", len(secrets))
	}
	wantOrder := []string{"alpha-token", "middle-token", "zzz-token"}
	for i, name := range wantOrder {
		if secrets[i].Metadata.Name != name {
			t.Errorf("secrets[%d].name = %q, want %q", i, secrets[i].Metadata.Name, name)
		}
	}
}

func TestUnionNeedsSecrets(t *testing.T) {
	t.Parallel()
	roles := map[string]*model.Role{
		"dev": {Spec: model.RoleSpec{Needs: model.RoleNeeds{Secrets: []string{"ANTHROPIC_AUTH_TOKEN"}}}},
		"pm": {
			Spec: model.RoleSpec{
				Needs: model.RoleNeeds{Secrets: []string{"OPENROUTER_API_KEY", "ANTHROPIC_AUTH_TOKEN"}},
			},
		},
	}
	roster := []model.ProjectTeamRole{
		{Ref: "dev"},
		{Ref: "pm"},
		{Ref: "unknown-role"}, // skipped silently
	}
	got := UnionNeedsSecrets(roles, roster)
	want := []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, k := range want {
		if got[i] != k {
			t.Errorf("got[%d] = %q, want %q", i, got[i], k)
		}
	}
}

func TestComposeFullPipeline(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	sharedPath := filepath.Join(base, "teams", "secrets.env")
	teamPath := filepath.Join(base, "teams", "alpha", "secrets.env")

	// First-init: scaffold both, both empty → all warnings, no rendered secrets.
	first, err := Compose(ComposeInputs{
		SharedPath: sharedPath,
		TeamPath:   teamPath,
		Needs:      []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"},
		Realm:      "default",
	})
	if err != nil {
		t.Fatalf("first Compose: %v", err)
	}
	if !first.SharedScaffolded || !first.TeamScaffolded {
		t.Errorf("first run did not scaffold both: %+v", first)
	}
	if len(first.Secrets) != 0 {
		t.Errorf("first run rendered %d secrets, want 0 (all empty)", len(first.Secrets))
	}
	if len(first.EmptyKeys) != 2 {
		t.Errorf("first run empty keys = %v, want both", first.EmptyKeys)
	}

	// Operator fills in shared value; per-team overrides one key.
	if writeErr := os.WriteFile(sharedPath, []byte("ANTHROPIC_AUTH_TOKEN=shared-anth\nOPENROUTER_API_KEY=shared-or\n"), 0o600); writeErr != nil {
		t.Fatalf("populate shared: %v", writeErr)
	}
	if writeErr := os.WriteFile(teamPath, []byte("OPENROUTER_API_KEY=team-or\n"), 0o600); writeErr != nil {
		t.Fatalf("populate team: %v", writeErr)
	}

	// Second Compose: per-team wins for OPENROUTER, shared carries for ANTHROPIC.
	second, err := Compose(ComposeInputs{
		SharedPath: sharedPath,
		TeamPath:   teamPath,
		Needs:      []string{"ANTHROPIC_AUTH_TOKEN", "OPENROUTER_API_KEY"},
		Realm:      "default",
	})
	if err != nil {
		t.Fatalf("second Compose: %v", err)
	}
	if second.SharedScaffolded || second.TeamScaffolded {
		t.Errorf("re-run reported scaffold of populated files: %+v", second)
	}
	if len(second.EmptyKeys) != 0 {
		t.Errorf("populated run reported empty keys: %v", second.EmptyKeys)
	}
	if len(second.Secrets) != 2 {
		t.Fatalf("rendered %d secrets, want 2", len(second.Secrets))
	}
	byName := map[string]string{}
	for _, s := range second.Secrets {
		byName[s.Metadata.Name] = s.Spec.Data
	}
	if byName["anthropic-auth-token"] != "shared-anth" {
		t.Errorf("shared-only carry-through wrong: %q", byName["anthropic-auth-token"])
	}
	if byName["openrouter-api-key"] != "team-or" {
		t.Errorf("per-team override wrong: %q", byName["openrouter-api-key"])
	}
}

func TestComposeDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	sharedPath := filepath.Join(base, "teams", "secrets.env")
	teamPath := filepath.Join(base, "teams", "alpha", "secrets.env")

	got, err := Compose(ComposeInputs{
		SharedPath: sharedPath,
		TeamPath:   teamPath,
		Needs:      []string{"ANTHROPIC_AUTH_TOKEN"},
		Realm:      "default",
		DryRun:     true,
	})
	if err != nil {
		t.Fatalf("Compose dry-run: %v", err)
	}
	if got.SharedScaffolded || got.TeamScaffolded {
		t.Errorf("dry-run reported scaffold: %+v", got)
	}
	if _, statErr := os.Stat(sharedPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote shared file: %v", statErr)
	}
	if _, statErr := os.Stat(teamPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("dry-run wrote team file: %v", statErr)
	}
	if len(got.Secrets) != 0 {
		t.Errorf("dry-run with no on-disk values rendered %d secrets", len(got.Secrets))
	}
	if len(got.EmptyKeys) != 1 || got.EmptyKeys[0] != "ANTHROPIC_AUTH_TOKEN" {
		t.Errorf("dry-run empties = %v, want [ANTHROPIC_AUTH_TOKEN]", got.EmptyKeys)
	}
}

func TestComposeEmptyNeedsShortCircuits(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	got, err := Compose(ComposeInputs{
		SharedPath: filepath.Join(base, "teams", "secrets.env"),
		TeamPath:   filepath.Join(base, "teams", "alpha", "secrets.env"),
		Needs:      nil,
		Realm:      "default",
	})
	if err != nil {
		t.Fatalf("Compose empty needs: %v", err)
	}
	if got.SharedScaffolded || got.TeamScaffolded ||
		len(got.Secrets) != 0 || len(got.EmptyKeys) != 0 {
		t.Errorf("empty needs produced output: %+v", got)
	}
}

func TestRenderEmptyRealmReturnsNothing(t *testing.T) {
	t.Parallel()
	// Defense-in-depth: an empty realm would produce invalid Secrets (the
	// parser requires metadata.realm). Render short-circuits so the caller
	// can never accidentally apply schema-invalid output.
	merged := map[string]string{"FOO": "bar"}
	secrets, _ := Render(merged, []string{"FOO"}, "")
	if len(secrets) != 0 {
		t.Errorf("empty realm rendered %d secrets, want 0", len(secrets))
	}
}
