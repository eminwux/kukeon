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

// Package teamsecrets composes a team's secret material from two `secrets.env`
// layers — a shared host-wide default at `~/.kuke/teams/secrets.env` and a
// per-team override at `<teamDir>/secrets.env` — and renders the merged
// non-empty entries as `kind: Secret` documents the daemon-side apply pipeline
// (#1029) consumes alongside the project's CellBlueprints and CellConfigs.
//
// The two-layer model splits operator-wide tokens (e.g. an ANTHROPIC OAuth
// token used in every team) from per-team overrides (e.g. project `dezot`
// using a different OPENROUTER token than project `kukeon`). Per-key per-team
// values win over the shared default; per-team-only keys join the set;
// shared-only keys carry through.
//
// On first run the package scaffolds both files in place with one empty
// `KEY=` line per secret name the team's roles reference, leaving values for
// the operator to fill in by hand — mode 0o600, never overwritten on re-run
// once populated. Values are never logged or echoed: the empty-value warning
// names keys only, and the rendered Secret documents themselves are the only
// channel through which the values reach the daemon.
package teamsecrets

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

const (
	// FileName is the secrets-env filename used at both layers: shared at
	// <base>/teams/secrets.env and per-team at <teamDir>/secrets.env.
	FileName = "secrets.env"
	// FilePerm is the mode applied to a scaffolded secrets.env — operator
	// read/write only. Values that land in the file are never echoed to
	// stdout/stderr by this package.
	FilePerm = 0o600
	// DirPerm is the mode applied to a freshly-created parent directory of
	// a scaffolded secrets.env. Matches the teams/ root mode used by
	// teamhost so the two scaffolding passes converge on the same shape.
	DirPerm = 0o700
)

// ComposeInputs is the per-team request handed to Compose.
//
// SharedPath is the host-wide defaults file (`<base>/teams/secrets.env`).
// TeamPath is the per-team override (`<teamDir>/secrets.env`); when teamDir
// is relocated via TeamEntry.spec.teamDir the caller passes the relocated
// path so the override lands beside the rest of the per-team state.
//
// Needs is the union of secret names every role in the team references —
// see UnionNeedsSecrets. The scaffold lines are written for every Needs
// entry; merged values for keys not in Needs are dropped so a hand-edited
// override file with extra junk doesn't leak unrelated bytes into the
// apply bundle.
//
// Realm is the realm rendered Secrets bind to. Team workloads live in the
// `default` user realm — pass teamrender.DefaultRealm.
//
// DryRun suppresses both file writes so the dry-run path stays "renders to
// stdout, touches no files on disk". The merged read still happens against
// whatever's already there.
type ComposeInputs struct {
	SharedPath string
	TeamPath   string
	Needs      []string
	Realm      string
	DryRun     bool
}

// ComposeResult carries the per-invocation outcome.
//
// Secrets is the set of `kind: Secret` documents to bundle into the apply
// payload (ordered alphabetically by metadata.name — deterministic so two
// runs against the same inputs marshal byte-identically). EmptyKeys lists
// the original env keys (SCREAMING_SNAKE_CASE — the casing in the file the
// operator opens to fix it) whose merged value was empty; the caller emits
// one warning per empty key.
//
// SharedScaffolded / TeamScaffolded report whether the corresponding file
// was just freshly written by this Compose call — the caller may surface
// these as one-time announcements so the operator notices the new file to
// fill in.
type ComposeResult struct {
	Secrets          []*v1beta1.SecretDoc
	EmptyKeys        []string
	SharedScaffolded bool
	TeamScaffolded   bool
}

// Compose runs the four-step pipeline: scaffold (skipped under DryRun),
// load both layers, merge with per-team precedence, render every non-empty
// entry as a SecretDoc. An empty Needs short-circuits the whole pipeline:
// nothing is written, nothing is read, no warnings are surfaced — a team
// whose roles declare no `needs.secrets` has no secret pipeline.
func Compose(in ComposeInputs) (*ComposeResult, error) {
	if len(in.Needs) == 0 {
		return &ComposeResult{}, nil
	}
	out := &ComposeResult{}
	if !in.DryRun {
		sharedCreated, err := ScaffoldEnvFile(in.SharedPath, in.Needs)
		if err != nil {
			return nil, fmt.Errorf("scaffold shared secrets.env: %w", err)
		}
		out.SharedScaffolded = sharedCreated
		teamCreated, err := ScaffoldEnvFile(in.TeamPath, in.Needs)
		if err != nil {
			return nil, fmt.Errorf("scaffold team secrets.env: %w", err)
		}
		out.TeamScaffolded = teamCreated
	}
	shared, err := LoadEnvFile(in.SharedPath)
	if err != nil {
		return nil, err
	}
	perTeam, err := LoadEnvFile(in.TeamPath)
	if err != nil {
		return nil, err
	}
	merged := Merge(shared, perTeam)
	secrets, emptyKeys := Render(merged, in.Needs, in.Realm)
	out.Secrets = secrets
	out.EmptyKeys = emptyKeys
	return out, nil
}

// ScaffoldEnvFile writes path with one `KEY=` line per entry in keys when
// path does not exist. Keys are sorted lexicographically and de-duplicated
// so re-runs against the same input produce byte-identical output. The
// parent directory is created (DirPerm) if absent; the file is written
// with mode FilePerm. An existing file at path is left untouched — the
// "re-run never overwrites a populated file" AC.
//
// Returns created=true when the file was freshly written; created=false
// when an existing file was found and skipped. An empty keys slice is a
// no-op (created=false, err=nil) — Compose's empty-Needs short-circuit
// covers this for the high-level path, but the primitive itself is also
// guarded so direct callers don't trip an "empty file" surprise.
func ScaffoldEnvFile(path string, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %q: %w", path, err)
	}
	parent := filepath.Dir(path)
	if mkErr := os.MkdirAll(parent, DirPerm); mkErr != nil {
		return false, fmt.Errorf("create dir %q: %w", parent, mkErr)
	}
	sorted := uniqueSortedNonEmpty(keys)
	var buf strings.Builder
	for _, k := range sorted {
		buf.WriteString(k)
		buf.WriteString("=\n")
	}
	if writeErr := os.WriteFile(path, []byte(buf.String()), FilePerm); writeErr != nil {
		return false, fmt.Errorf("write %q: %w", path, writeErr)
	}
	// WriteFile honors the process umask; chmod explicitly so 0o600 isn't
	// silently widened on operator hosts running with a permissive umask.
	if chmodErr := os.Chmod(path, FilePerm); chmodErr != nil {
		return false, fmt.Errorf("chmod %q: %w", path, chmodErr)
	}
	return true, nil
}

// LoadEnvFile reads path as a flat KEY=VALUE env file. Blank lines and
// lines whose first non-whitespace character is `#` are ignored; the key
// is whitespace-trimmed, the value is preserved verbatim past the first
// `=`. A line missing the `=` is silently skipped (it cannot be acted on
// as a secret). A missing file returns an empty map and no error so the
// caller can compose against a partially-scaffolded host.
func LoadEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return parseEnv(f)
}

func parseEnv(r io.Reader) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		if key == "" {
			continue
		}
		out[key] = trimmed[eq+1:]
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan env: %w", err)
	}
	return out, nil
}

// Merge overlays perTeam onto shared with per-team precedence: a key
// present in both maps with a non-empty perTeam value takes the perTeam
// value; a key in only one map carries through.
//
// An *empty* perTeam value is treated as a scaffold placeholder, not an
// override: the scaffold writes one empty `KEY=` line per role-declared
// secret name into every per-team `secrets.env` so the operator sees
// every key they can override, even before they have a value to override
// with. Treating that empty as an active override would null-out the
// shared default in a single re-init pass and force every per-team file
// to be hand-curated. The pragmatic reading — non-empty per-team wins,
// empty per-team falls back to shared — keeps the shared layer useful as
// a default while leaving the operator free to override per-key by
// writing a value. The result is a fresh map; neither input is mutated.
func Merge(shared, perTeam map[string]string) map[string]string {
	out := make(map[string]string, len(shared)+len(perTeam))
	for k, v := range shared {
		out[k] = v
	}
	for k, v := range perTeam {
		if v == "" {
			if _, sharedHas := shared[k]; sharedHas {
				continue
			}
		}
		out[k] = v
	}
	return out
}

// Render builds one SecretDoc per non-empty entry in merged whose key is
// also a member of needs. Keys outside needs are dropped — a stray entry
// in a hand-edited override file doesn't bleed unrelated bytes into the
// apply bundle. Output is sorted by metadata.name so two runs against the
// same input marshal byte-identically.
//
// emptyKeys carries the needs entries whose merged value is empty (or
// absent from merged) — the caller emits one operator-facing warning per
// entry. Casing is preserved at SCREAMING_SNAKE_CASE so the operator can
// grep the line in the secrets.env file.
func Render(merged map[string]string, needs []string, realm string) ([]*v1beta1.SecretDoc, []string) {
	if len(needs) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(realm) == "" {
		return nil, nil
	}
	keys := uniqueSortedNonEmpty(needs)
	var (
		secrets   []*v1beta1.SecretDoc
		emptyKeys []string
	)
	for _, k := range keys {
		v, ok := merged[k]
		if !ok || v == "" {
			emptyKeys = append(emptyKeys, k)
			continue
		}
		name := KebabCase(k)
		if name == "" {
			continue
		}
		secrets = append(secrets, &v1beta1.SecretDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindSecret,
			Metadata: v1beta1.SecretMetadata{
				Name:  name,
				Realm: realm,
			},
			Spec: v1beta1.SecretSpec{
				Data: v,
			},
		})
	}
	// Output is sorted by the *kebab-case* metadata.name, not the source
	// key — the apply bundle's reader sees the rendered name first.
	sort.Slice(secrets, func(i, j int) bool {
		return secrets[i].Metadata.Name < secrets[j].Metadata.Name
	})
	return secrets, emptyKeys
}

// KebabCase lowercases s and replaces every `_` with `-`. The operator
// convention is SCREAMING_SNAKE_CASE for env keys and kebab-case for
// rendered Secret metadata.name — a single key `ANTHROPIC_AUTH_TOKEN`
// surfaces as Secret `anthropic-auth-token`.
func KebabCase(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "_", "-")
}

// UnionNeedsSecrets walks the project roster and returns the
// lexicographically-sorted set of secret names every referenced role's
// `needs.secrets` declares. Roles missing from roles map are skipped
// silently — Render would surface the missing reference, but at the
// pre-compose secret-name discovery stage there's nothing to warn about
// (the parent renderer already surfaces missing roles).
func UnionNeedsSecrets(roles map[string]*model.Role, roster []model.ProjectTeamRole) []string {
	if len(roster) == 0 || len(roles) == 0 {
		return nil
	}
	set := map[string]struct{}{}
	for _, ptRole := range roster {
		role, ok := roles[ptRole.Ref]
		if !ok {
			continue
		}
		for _, s := range role.Spec.Needs.Secrets {
			if k := strings.TrimSpace(s); k != "" {
				set[k] = struct{}{}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// uniqueSortedNonEmpty returns the lexicographically-sorted set of
// whitespace-trimmed non-empty entries in keys.
func uniqueSortedNonEmpty(keys []string) []string {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if v := strings.TrimSpace(k); v != "" {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
