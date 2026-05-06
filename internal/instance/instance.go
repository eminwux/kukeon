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

// Package instance records the host-instance ServerConfiguration values
// (containerd namespace suffix and cgroup root) that a given runPath was
// bootstrapped under, so the daemon can refuse to start when reconfigured
// to a different layout. Migration between layouts is out of scope — the
// operator destroys the runPath and re-runs `kuke init` against the new
// config.
package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/errdefs"
)

// MetadataFile is the basename of the instance-metadata file written
// under runPath. The dot prefix keeps it visually distinct from realm
// directories (which never start with a dot) so a directory listing of
// /opt/kukeon makes the instance file obvious.
const MetadataFile = ".kukeon-instance.json"

// metadataFileMode is the on-disk mode of the instance metadata file.
// 0o640 mirrors the per-file mode `kuke init` applies recursively under
// /opt/kukeon (kukeonRunPathFileMode in cmd/kuke/init/init.go) so the
// kukeon group can read it without world access.
const metadataFileMode os.FileMode = 0o640

// Metadata is the on-disk shape of the instance file. The suffix is
// stored in operator-facing form (no leading dot, e.g. "kukeon.io"); the
// cgroup root is stored as written by the operator with trailing slashes
// trimmed.
type Metadata struct {
	ContainerdNamespaceSuffix string `json:"containerdNamespaceSuffix"`
	CgroupRoot                string `json:"cgroupRoot"`
}

// Path returns the absolute path of the instance metadata file under the
// given runPath.
func Path(runPath string) string {
	return filepath.Join(runPath, MetadataFile)
}

// Load reads and parses the instance metadata at runPath. Returns
// (zero, false, nil) if the file does not exist; (md, true, nil) when the
// file is present and parses; any other read or parse failure is wrapped
// with errdefs.ErrServerConfigurationInvalid.
func Load(runPath string) (Metadata, bool, error) {
	path := Path(runPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Metadata{}, false, nil
		}
		return Metadata{}, false, fmt.Errorf("read instance metadata %q: %w: %w",
			path, errdefs.ErrServerConfigurationInvalid, err)
	}
	var m Metadata
	if unmarshalErr := json.Unmarshal(raw, &m); unmarshalErr != nil {
		return Metadata{}, false, fmt.Errorf("parse instance metadata %q: %w: %w",
			path, errdefs.ErrServerConfigurationInvalid, unmarshalErr)
	}
	return m, true, nil
}

// Write atomically writes the instance metadata to runPath. Creates the
// runPath directory if missing. Idempotent: re-writing the same content
// is a no-op visible to the operator only as a fresh mtime.
func Write(runPath string, m Metadata) error {
	if err := os.MkdirAll(runPath, 0o750); err != nil {
		return fmt.Errorf("create runPath %q: %w", runPath, err)
	}
	raw, marshalErr := json.MarshalIndent(m, "", "  ")
	if marshalErr != nil {
		return fmt.Errorf("marshal instance metadata: %w", marshalErr)
	}
	raw = append(raw, '\n')

	path := Path(runPath)
	tmp, createErr := os.CreateTemp(runPath, ".kukeon-instance-*.tmp")
	if createErr != nil {
		return fmt.Errorf("create temp file under %q: %w", runPath, createErr)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if chmodErr := tmp.Chmod(metadataFileMode); chmodErr != nil {
		return fmt.Errorf("chmod %q: %w", tmpPath, chmodErr)
	}
	if _, writeErr := tmp.Write(raw); writeErr != nil {
		return fmt.Errorf("write %q: %w", tmpPath, writeErr)
	}
	if syncErr := tmp.Sync(); syncErr != nil {
		return fmt.Errorf("fsync %q: %w", tmpPath, syncErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return fmt.Errorf("close %q: %w", tmpPath, closeErr)
	}
	if renameErr := os.Rename(tmpPath, path); renameErr != nil {
		return fmt.Errorf("rename %q -> %q: %w", tmpPath, path, renameErr)
	}
	return nil
}

// VerifyOrWrite enforces the runPath / configuration consistency contract:
// if a prior instance metadata exists under runPath and disagrees with the
// supplied (suffix, cgroupRoot), return an ErrInstanceMismatch-wrapped
// error so the caller can refuse to start. If absent, write the supplied
// values so the next start can verify against them. If present and
// matching, do nothing.
//
// suffix is the operator-facing form without a leading dot. cgroupRoot is
// the absolute cgroup path; callers must pass the already-trimmed value.
func VerifyOrWrite(runPath, suffix, cgroupRoot string) error {
	prior, found, err := Load(runPath)
	if err != nil {
		return err
	}
	if !found {
		return Write(runPath, Metadata{
			ContainerdNamespaceSuffix: suffix,
			CgroupRoot:                cgroupRoot,
		})
	}
	if prior.ContainerdNamespaceSuffix != suffix || prior.CgroupRoot != cgroupRoot {
		return fmt.Errorf(
			"runPath %q was bootstrapped with containerdNamespaceSuffix=%q "+
				"cgroupRoot=%q but the running configuration requests "+
				"containerdNamespaceSuffix=%q cgroupRoot=%q: %w",
			runPath,
			prior.ContainerdNamespaceSuffix, prior.CgroupRoot,
			suffix, cgroupRoot,
			errdefs.ErrInstanceMismatch,
		)
	}
	return nil
}
