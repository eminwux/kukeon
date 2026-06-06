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

package teamhost

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

const (
	// filePermissionMask is the rwxrwxrwx file-permission bit mask used to
	// clamp a YAML-supplied seeds[].mode value before casting to FileMode.
	filePermissionMask = 0o777
	// defaultSeedMode is the FileMode applied to a seed entry whose YAML
	// omits `mode` (rendered as 0 by Go's int zero value).
	defaultSeedMode os.FileMode = 0o644
)

// SeedPair names one roster (role × harness) pair the provisioning pass
// materializes a state dir + seed set for.
type SeedPair struct {
	Role    string
	Harness string
}

// ProvisionInputs is the per-team provisioning request.
//
// TeamRoot is the resolved per-team root directory — `<base>/teams/<team>`
// by default, or the operator-supplied TeamEntry.spec.teamDir override when
// set. The provisioning pass anchors every state dir, seed write, and the
// one-shot host-config copy under this path so a relocated TeamDir lands a
// fully self-contained tree.
//
// Pairs lists the roster (role × harness) entries to mkdir state dirs for.
// Harnesses maps each harness referenced by Pairs to its loaded Harness
// document; ProvisionTeam reads `Spec.Seeds` off the corresponding entry to
// write seed files. A harness referenced by Pairs but absent from Harnesses
// gets a state dir but no seeds (the resolve layer would surface the
// missing-harness error earlier — this is a defense-in-depth path).
//
// HomeConfigDir is the source of the one-shot `$HOME/.config/. →
// HostConfigDir(team)` copy. The empty string disables the copy; a path that
// does not exist on disk is treated the same as the empty string.
type ProvisionInputs struct {
	TeamRoot      string
	Pairs         []SeedPair
	Harnesses     map[string]*model.Harness
	HomeConfigDir string
	DryRun        bool
	Out           io.Writer
}

// ProvisionTeam runs the per-team provisioning pass: it mkdir -p's each
// roster pair's state dir, writes any harness seeds that are not already
// present (hand-edited files survive), and performs the one-shot host
// config copy on a clean per-team config dir. The pass is idempotent — a
// re-run on a healthy host is a no-op.
//
// DryRun reports what would change to Out without touching disk; an empty
// Out is treated as io.Discard.
func ProvisionTeam(in ProvisionInputs) error {
	teamRoot := strings.TrimSpace(in.TeamRoot)
	if teamRoot == "" {
		return errors.New("ProvisionTeam: TeamRoot is required")
	}
	out := in.Out
	if out == nil {
		out = io.Discard
	}

	if !in.DryRun {
		if err := os.MkdirAll(teamRoot, TeamsRootPerm); err != nil {
			return fmt.Errorf("create team root %q: %w", teamRoot, err)
		}
	}

	if err := provisionStateDirs(out, teamRoot, in.Pairs, in.DryRun); err != nil {
		return err
	}
	if err := provisionSeeds(out, teamRoot, in.Pairs, in.Harnesses, in.DryRun); err != nil {
		return err
	}
	if err := provisionHostConfig(out, teamRoot, in.HomeConfigDir, in.DryRun); err != nil {
		return err
	}
	return nil
}

func provisionStateDirs(out io.Writer, teamRoot string, pairs []SeedPair, dryRun bool) error {
	for _, p := range pairs {
		role := strings.TrimSpace(p.Role)
		harness := strings.TrimSpace(p.Harness)
		if role == "" || harness == "" {
			continue
		}
		dir := filepath.Join(teamRoot, role+"-"+harness)
		if dryRun {
			fmt.Fprintf(out, "# dry-run: mkdir %s (mode %#o)\n", dir, TeamsRootPerm)
			continue
		}
		if err := os.MkdirAll(dir, TeamsRootPerm); err != nil {
			return fmt.Errorf("create state dir %q: %w", dir, err)
		}
	}
	return nil
}

func provisionSeeds(
	out io.Writer, teamRoot string,
	pairs []SeedPair, harnesses map[string]*model.Harness, dryRun bool,
) error {
	// Visit each harness at most once even when multiple roles select it —
	// the seed set is harness-scoped, not pair-scoped.
	seen := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		h := strings.TrimSpace(p.Harness)
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		harness := harnesses[h]
		if harness == nil {
			continue
		}
		for _, seed := range harness.Spec.Seeds {
			path, err := expandSeedPath(seed.Path, teamRoot, h)
			if err != nil {
				return fmt.Errorf("harness %q seed %q: %w", h, seed.Path, err)
			}
			if writeErr := writeSeedIfAbsent(out, path, seed, dryRun); writeErr != nil {
				return writeErr
			}
		}
	}
	return nil
}

// expandSeedPath resolves a harness seed's spec.path template against the
// per-team root. ${TEAM_ROOT} expands to teamRoot, ${HARNESS} to the harness
// name; the resulting path is anchored under teamRoot when relative, and
// must never escape teamRoot once cleaned.
func expandSeedPath(raw, teamRoot, harness string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errdefs.ErrTeamHarnessSeedPathRequired
	}
	expanded := strings.NewReplacer(
		"${TEAM_ROOT}", teamRoot,
		"${HARNESS}", harness,
	).Replace(raw)
	if !filepath.IsAbs(expanded) {
		expanded = filepath.Join(teamRoot, expanded)
	}
	clean := filepath.Clean(expanded)
	rootClean := filepath.Clean(teamRoot)
	if clean != rootClean &&
		!strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
		return "", fmt.Errorf("%w (got %q)", errdefs.ErrTeamHarnessSeedPathEscapes, clean)
	}
	return clean, nil
}

func writeSeedIfAbsent(out io.Writer, path string, seed model.HarnessSeed, dryRun bool) error {
	if _, err := os.Stat(path); err == nil {
		if dryRun {
			fmt.Fprintf(out, "# dry-run: seed %s present (skip)\n", path)
		}
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat seed %q: %w", path, err)
	}
	// Parser validation bounds seed.Mode to filePermissionMask, so the
	// int→FileMode cast cannot overflow despite gosec's structural complaint.
	mode := os.FileMode(uint32(seed.Mode & filePermissionMask)) //nolint:gosec // bounded by validateHarness
	if mode == 0 {
		mode = defaultSeedMode
	}
	if dryRun {
		fmt.Fprintf(out, "# dry-run: write %s mode=%#o (%d byte(s))\n", path, mode, len(seed.Content))
		return nil
	}
	parent := filepath.Dir(path)
	if mkErr := os.MkdirAll(parent, TeamsRootPerm); mkErr != nil {
		return fmt.Errorf("create seed parent %q: %w", parent, mkErr)
	}
	if writeErr := os.WriteFile(path, []byte(seed.Content), mode); writeErr != nil {
		return fmt.Errorf("write seed %q: %w", path, writeErr)
	}
	// os.WriteFile honours the process umask; chmod explicitly so a 0o644
	// declaration is not silently masked to 0o600 on operator hosts.
	if chmodErr := os.Chmod(path, mode); chmodErr != nil {
		return fmt.Errorf("chmod seed %q: %w", path, chmodErr)
	}
	return nil
}

// provisionHostConfig performs the one-shot `$HOME/.config/. →
// HostConfigDir(team)` copy when the per-team config dir is empty AND
// homeConfigDir exists. An already-populated config dir is left untouched
// (the operator may have hand-edited it); a missing homeConfigDir is a
// silent skip (operator may not have one yet).
func provisionHostConfig(out io.Writer, teamRoot, homeConfigDir string, dryRun bool) error {
	src := strings.TrimSpace(homeConfigDir)
	if src == "" {
		return nil
	}
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat host config source %q: %w", src, err)
	}
	dst := filepath.Join(teamRoot, hostConfigDirName)
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		if dryRun {
			fmt.Fprintf(out, "# dry-run: host config %s already populated (skip)\n", dst)
		}
		return nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read host config dir %q: %w", dst, err)
	}
	if dryRun {
		fmt.Fprintf(out, "# dry-run: copy %s/. → %s\n", src, dst)
		return nil
	}
	if err := os.MkdirAll(dst, TeamsRootPerm); err != nil {
		return fmt.Errorf("create host config dir %q: %w", dst, err)
	}
	return copyTree(src, dst)
}

// copyTree mirrors src into dst preserving each entry's file mode bits.
// Symlinks are skipped — the host-config copy is operator state that
// should not span filesystem boundaries opaquely.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		info, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&fs.ModeSymlink != 0:
			return nil
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %q: %w", src, err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %q: %w", dst, err)
	}
	defer func() { _ = out.Close() }()
	if _, copyErr := io.Copy(out, in); copyErr != nil {
		return fmt.Errorf("copy %q → %q: %w", src, dst, copyErr)
	}
	if chmodErr := os.Chmod(dst, mode); chmodErr != nil {
		return fmt.Errorf("chmod %q: %w", dst, chmodErr)
	}
	return nil
}
