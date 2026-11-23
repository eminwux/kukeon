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

package status

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// volumeGroupRWX is the group permission triple every volume directory must
// keep: the runner creates each volume dir root:kukeon setgid 0o770 (or
// root:root 0o770 without a kukeon GID), and a volume that lost group-write
// can no longer be written by the non-root cells that mount it.
const volumeGroupRWX os.FileMode = 0o070

// volumeScopeDepth is how many scope levels can own a volumes/ dir:
// realm, space, and stack (volumes are never cell-scoped).
const volumeScopeDepth = 3

// reservedScopeSubdir reports whether name is a per-scope resource subdir
// rather than a child scope. Mirrors the runner's reservedScopeSubdirs
// (internal/controller/runner/scope_children.go), which is unexported.
func reservedScopeSubdir(name string) bool {
	switch name {
	case consts.KukeonSecretsSubdir,
		consts.KukeonBlueprintsSubdir,
		consts.KukeonConfigsSubdir,
		consts.KukeonConfigStatusSubdir,
		consts.KukeonVolumesSubdir,
		consts.KukeonVolumeMetaSubdir,
		consts.KukeonHealthReportsSubdir:
		return true
	}
	return false
}

// checkStateVolumePerms walks every <RunPath>/data/<realm>/[<space>/[<stack>/]]
// volumes/<name> directory and WARNs on any that is missing group rwx
// (mode & 0o070 != 0o070). This is the class of breakage the pre-#1415
// `kuke init` ownership sweep caused (it reset volume dirs to 2750, stripping
// group-write), so the check catches a regression of that sweep or any
// out-of-band chmod (issue #1416).
func checkStateVolumePerms(rc *runCtx) Result {
	r := Result{
		Section: sectionState,
		Name:    "volume-perms",
	}

	dataRoot := fs.MetadataRoot(rc.runPath)
	if _, err := os.Stat(dataRoot); err != nil {
		if os.IsNotExist(err) {
			r.Status = StatusOK
			r.Detail = fmt.Sprintf("%s (not present; no volumes)", dataRoot)
			return r
		}
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%s (stat failed: %v)", dataRoot, err)
		return r
	}

	var (
		checked int
		bad     []string
		errs    []string
	)
	scopes := []string{dataRoot}
	for level := 0; level < volumeScopeDepth+1 && len(scopes) > 0; level++ {
		var next []string
		for _, scope := range scopes {
			if level > 0 {
				c, b, e := inspectVolumesDir(filepath.Join(scope, consts.KukeonVolumesSubdir))
				checked += c
				bad = append(bad, b...)
				errs = append(errs, e...)
			}
			if level == volumeScopeDepth {
				continue
			}
			children, err := childScopes(scope)
			if err != nil {
				errs = append(errs, err.Error())
				continue
			}
			next = append(next, children...)
		}
		scopes = next
	}

	if len(bad) > 0 {
		sort.Strings(bad)
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%d of %d volume dirs missing group rwx: %s",
			len(bad), checked, strings.Join(bad, ", "))
		r.Remediation = "restore the runner contract: `chmod 2770 <volume-dir>` (non-root cells mounting it need group-write)"
		return r
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		r.Status = StatusWARN
		r.Detail = fmt.Sprintf("%d volume dirs ok; read failed: %s", checked, strings.Join(errs, "; "))
		r.Remediation = "run `kuke status` as root to inspect every volume dir"
		return r
	}
	r.Status = StatusOK
	r.Detail = fmt.Sprintf("%d volume dirs, all group-writable", checked)
	return r
}

// childScopes returns the child-scope directories directly under dir,
// skipping per-scope resource subdirs. A missing dir yields no children.
func childScopes(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%s: %w", dir, err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || reservedScopeSubdir(e.Name()) {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	return out, nil
}

// inspectVolumesDir checks every volume directory directly under volumesDir,
// returning how many it checked, the paths (with their mode) missing group
// rwx, and any read/stat errors. A missing volumes/ dir is not an error.
func inspectVolumesDir(volumesDir string) (int, []string, []string) {
	entries, err := os.ReadDir(volumesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil, nil
		}
		return 0, nil, []string{fmt.Sprintf("%s: %v", volumesDir, err)}
	}
	var (
		checked int
		bad     []string
		errs    []string
	)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(volumesDir, e.Name())
		info, infoErr := e.Info()
		if infoErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", path, infoErr))
			continue
		}
		checked++
		if info.Mode().Perm()&volumeGroupRWX != volumeGroupRWX {
			bad = append(bad, fmt.Sprintf("%s (%#o)", path, info.Mode().Perm()))
		}
	}
	return checked, bad, errs
}
