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
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/errdefs"
)

const paramProfile = `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: dev
spec:
  parameters:
    - name: PROMPT
      description: "Slash command passed to claude --print"
      required: true
    - name: PROJECT_REPO
      description: "Git URL of the target project"
      required: true
    - name: PROJECT_DIR
      description: "Short dirname for the project clone"
      required: true
    - name: AGENTS_REPO
      description: "owner/repo of the agents repo"
      default: "eminwux/agents"
    - name: CLAUDE_IMAGE
      description: "Container image tag for the worker"
      default: "registry.eminwux.com/claude:latest"
  cell:
    containers:
      - id: work
        image: ${CLAUDE_IMAGE}
        env:
          - PROMPT=${PROMPT}
          - PROJECT_REPO=${PROJECT_REPO}
          - PROJECT_DIR=${PROJECT_DIR}
          - AGENTS_REPO=${AGENTS_REPO}
`

// noEnv is the env-fallback callers pass when they want to disable env
// lookup entirely — substitution then comes from --param + defaults only.
func noEnv(string) (string, bool) { return "", false }

func TestLoadResolved_RequiredParamsViaCLI(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	cli := map[string]string{
		"PROMPT":       "/pick-issue 354",
		"PROJECT_REPO": "https://github.com/eminwux/crew",
		"PROJECT_DIR":  "crew",
	}

	got, err := cellprofile.LoadResolved(dir, "dev", cli, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}

	if len(got.Spec.Cell.Containers) != 1 {
		t.Fatalf("containers=%d want 1", len(got.Spec.Cell.Containers))
	}
	c := got.Spec.Cell.Containers[0]
	if c.Image != "registry.eminwux.com/claude:latest" {
		t.Errorf("image=%q want default substituted", c.Image)
	}
	wantEnv := []string{
		"PROMPT=/pick-issue 354",
		"PROJECT_REPO=https://github.com/eminwux/crew",
		"PROJECT_DIR=crew",
		"AGENTS_REPO=eminwux/agents",
	}
	if !reflect.DeepEqual(c.Env, wantEnv) {
		t.Errorf("env=%v\nwant %v", c.Env, wantEnv)
	}
}

func TestLoadResolved_CLIBeatsDefault(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	got, err := cellprofile.LoadResolved(dir, "dev", map[string]string{
		"PROMPT":       "test",
		"PROJECT_REPO": "test",
		"PROJECT_DIR":  "test",
		"AGENTS_REPO":  "override/agents",
	}, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[3] != "AGENTS_REPO=override/agents" {
		t.Errorf("env[3]=%q want AGENTS_REPO=override/agents (CLI override wins over default)",
			got.Spec.Cell.Containers[0].Env[3])
	}
}

func TestLoadResolved_EnvFallback(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	env := map[string]string{
		"PROMPT":       "from-env",
		"PROJECT_REPO": "env://repo",
		"PROJECT_DIR":  "envdir",
	}
	lookup := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}

	got, err := cellprofile.LoadResolved(dir, "dev", nil, lookup)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[0] != "PROMPT=from-env" {
		t.Errorf("env[0]=%q want PROMPT=from-env (env fallback)",
			got.Spec.Cell.Containers[0].Env[0])
	}
}

func TestLoadResolved_DefaultBeatsEnv(t *testing.T) {
	// Defaults are declared in the profile and must win over the env fallback —
	// otherwise a stray env var on the host would silently shadow what the
	// profile author wrote down.
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	env := map[string]string{"AGENTS_REPO": "from-env/agents"}
	got, err := cellprofile.LoadResolved(dir, "dev", map[string]string{
		"PROMPT":       "x",
		"PROJECT_REPO": "x",
		"PROJECT_DIR":  "x",
	}, func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	})
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[3] != "AGENTS_REPO=eminwux/agents" {
		t.Errorf("env[3]=%q want AGENTS_REPO=eminwux/agents (default beats env)",
			got.Spec.Cell.Containers[0].Env[3])
	}
}

func TestLoadResolved_RequiredMissing_Errors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	_, err := cellprofile.LoadResolved(dir, "dev", nil, noEnv)
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "PROMPT") {
		t.Errorf("err %q must name the missing required parameter", err)
	}
}

func TestLoadResolved_UndeclaredCLIParam_Errors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dev.yaml", paramProfile)

	_, err := cellprofile.LoadResolved(dir, "dev", map[string]string{
		"PROMPT":       "x",
		"PROJECT_REPO": "x",
		"PROJECT_DIR":  "x",
		"NOT_A_PARAM":  "oops",
	}, noEnv)
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "NOT_A_PARAM") {
		t.Errorf("err %q must name the undeclared --param key", err)
	}
}

func TestLoad_UndeclaredReference_Errors(t *testing.T) {
	// Refs to undeclared params are caught at install time so a typo doesn't
	// silently leave a literal `${KEY}` in the rendered cell at runtime.
	dir := t.TempDir()
	writeProfile(t, dir, "bad.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: bad
spec:
  parameters:
    - name: GOOD
      default: "ok"
  cell:
    containers:
      - id: work
        image: ${TYPO}
`)

	_, err := cellprofile.Load(dir, "bad")
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "TYPO") {
		t.Errorf("err %q must name the undeclared reference", err)
	}
}

func TestLoad_DuplicateParameter_Errors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "dup.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: dup
spec:
  parameters:
    - name: KEY
      default: "a"
    - name: KEY
      default: "b"
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
`)

	_, err := cellprofile.Load(dir, "dup")
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), "KEY") {
		t.Errorf("err %q must name the duplicated parameter", err)
	}
}

func TestLoad_InvalidParameterName_Errors(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "bad.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: bad
spec:
  parameters:
    - name: "1NOT-VALID"
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
`)

	_, err := cellprofile.Load(dir, "bad")
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
}

func TestLoadResolved_EmptyStringIsValidValue(t *testing.T) {
	// Empty strings are a valid resolved value and must not trigger the
	// required/default fallback — they're how callers say "explicitly
	// nothing here." Distinct from "unset" (no provider returns a value).
	dir := t.TempDir()
	writeProfile(t, dir, "p.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: p
spec:
  parameters:
    - name: NOTE
      required: true
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
        env:
          - NOTE=${NOTE}
`)

	got, err := cellprofile.LoadResolved(dir, "p", map[string]string{"NOTE": ""}, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[0] != "NOTE=" {
		t.Errorf("env[0]=%q want NOTE= (empty string is a valid value)",
			got.Spec.Cell.Containers[0].Env[0])
	}
}

func TestLoadResolved_NonRequiredUnset_SubstitutesEmpty(t *testing.T) {
	// A declared, referenced, non-required parameter with no provider
	// resolves to "" — matching `envsubst` behavior. Profiles that need
	// "literal ${KEY}" should escape via shell/templating layer above.
	dir := t.TempDir()
	writeProfile(t, dir, "p.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: p
spec:
  parameters:
    - name: NOTE
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
        env:
          - NOTE=${NOTE}
`)

	got, err := cellprofile.LoadResolved(dir, "p", nil, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[0] != "NOTE=" {
		t.Errorf("env[0]=%q want NOTE= (unset non-required → empty)",
			got.Spec.Cell.Containers[0].Env[0])
	}
}

func TestLoadResolved_NoParameters_NoOp(t *testing.T) {
	// Profiles without parameters[] keep working — the new substitution path
	// is a no-op for them. This is the migration safety net the proposal
	// promises ("No breaking change").
	dir := t.TempDir()
	writeProfile(t, dir, "claude-cell.yaml", claudeProfile)

	got, err := cellprofile.LoadResolved(dir, "claude-cell", nil, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Metadata.Name != "claude-cell" {
		t.Errorf("metadata.name=%q want claude-cell", got.Metadata.Name)
	}
	if len(got.Spec.Cell.Containers) != 2 {
		t.Errorf("containers=%d want 2", len(got.Spec.Cell.Containers))
	}
}

func TestLoadResolved_PartialReferences(t *testing.T) {
	// Substitution must work mid-string (not only when ${KEY} is the entire
	// value) — this is the common case for image tags and env values.
	dir := t.TempDir()
	writeProfile(t, dir, "p.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: p
spec:
  parameters:
    - name: TAG
      required: true
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:${TAG}-stable
`)

	got, err := cellprofile.LoadResolved(dir, "p",
		map[string]string{"TAG": "1.36"}, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Image != "registry.eminwux.com/busybox:1.36-stable" {
		t.Errorf("image=%q want registry.eminwux.com/busybox:1.36-stable",
			got.Spec.Cell.Containers[0].Image)
	}
}

func TestLoadResolved_ParametersBlockExempted(t *testing.T) {
	// Defaults that contain `${VAR}`-shaped text (e.g., a profile that
	// proxies its substitutions through to a downstream tool) must not
	// trigger the "undeclared reference" check. The validator scans the
	// cell body, not the parameter declarations.
	dir := t.TempDir()
	writeProfile(t, dir, "p.yaml", `apiVersion: v1beta1
kind: CellProfile
metadata:
  name: p
spec:
  parameters:
    - name: SHELL_LIT
      default: "${HOME}/bin"
  cell:
    containers:
      - id: work
        image: registry.eminwux.com/busybox:latest
        env:
          - SHELL_LIT=${SHELL_LIT}
`)

	got, err := cellprofile.LoadResolved(dir, "p", nil, noEnv)
	if err != nil {
		t.Fatalf("LoadResolved: %v", err)
	}
	if got.Spec.Cell.Containers[0].Env[0] != "SHELL_LIT=${HOME}/bin" {
		t.Errorf("env[0]=%q want SHELL_LIT=${HOME}/bin (literal default flows through)",
			got.Spec.Cell.Containers[0].Env[0])
	}
}

func TestParseParamArgs_HappyPath(t *testing.T) {
	got, err := cellprofile.ParseParamArgs([]string{
		"A=alpha",
		"B=value with spaces",
		"C=",
		"D=has=equals",
	})
	if err != nil {
		t.Fatalf("ParseParamArgs: %v", err)
	}
	want := map[string]string{
		"A": "alpha",
		"B": "value with spaces",
		"C": "",
		"D": "has=equals",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseParamArgs_MissingEquals_Errors(t *testing.T) {
	_, err := cellprofile.ParseParamArgs([]string{"NOEQ"})
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
}

func TestParseParamArgs_InvalidName_Errors(t *testing.T) {
	_, err := cellprofile.ParseParamArgs([]string{"1BAD=x"})
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
}

func TestParseParamFile_HappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.params")
	body := "# leading comment\n" +
		"\n" +
		"  # indented comment\n" +
		"A=alpha\n" +
		"B=value with spaces\n" +
		"C=\n" +
		"D=has=equals\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := cellprofile.ParseParamFile(path)
	if err != nil {
		t.Fatalf("ParseParamFile: %v", err)
	}
	want := map[string]string{
		"A": "alpha",
		"B": "value with spaces",
		"C": "",
		"D": "has=equals",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseParamFile_BadLine_Errors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "p.params")
	if err := os.WriteFile(path, []byte("OK=1\nBAD-LINE\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := cellprofile.ParseParamFile(path)
	if !errors.Is(err, errdefs.ErrProfileInvalid) {
		t.Fatalf("err=%v want ErrProfileInvalid", err)
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("err %q must name the offending line number", err)
	}
}

func TestMergeParams_OverridesWin(t *testing.T) {
	got := cellprofile.MergeParams(
		map[string]string{"A": "from-base", "B": "kept"},
		map[string]string{"A": "from-override", "C": "added"},
	)
	want := map[string]string{
		"A": "from-override",
		"B": "kept",
		"C": "added",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestMaterializeWithName_OverridesGenerated(t *testing.T) {
	dir := t.TempDir()
	writeProfile(t, dir, "claude-cell.yaml", claudeProfile)

	profile, err := cellprofile.Load(dir, "claude-cell")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := cellprofile.MaterializeWithName(profile, "pinned-name-1")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if got.Metadata.Name != "pinned-name-1" {
		t.Errorf("name=%q want pinned-name-1 (no suffix)", got.Metadata.Name)
	}
	if got.Spec.ID != "pinned-name-1" {
		t.Errorf("spec.id=%q want pinned-name-1 (mirrors metadata.name)", got.Spec.ID)
	}
	// Profile-of-origin label still flows through under the override.
	if got.Metadata.Labels[cellprofile.LabelProfile] != "claude-cell" {
		t.Errorf("profile label=%q want claude-cell",
			got.Metadata.Labels[cellprofile.LabelProfile])
	}
}

func TestMaterializeWithName_EmptyFallsBackToGenerated(t *testing.T) {
	// Empty/whitespace nameOverride must behave exactly like Materialize —
	// the override path is opt-in, and a blank --name on the CLI must not
	// silently produce a cell named "".
	dir := t.TempDir()
	writeProfile(t, dir, "claude-cell.yaml", claudeProfile)

	profile, err := cellprofile.Load(dir, "claude-cell")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := cellprofile.MaterializeWithName(profile, "  ")
	if err != nil {
		t.Fatalf("MaterializeWithName: %v", err)
	}
	if !strings.HasPrefix(got.Metadata.Name, "claude-cell-") {
		t.Errorf("name=%q want generated <claude-cell>-<6hex>", got.Metadata.Name)
	}
}
