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

package ctr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// DefaultSecretsStagingDir is the host directory the daemon uses to stage
// file-mounted secrets before bind-mounting them into containers. The
// directory lives under /run so contents are ephemeral across reboots on
// typical deployments.
const DefaultSecretsStagingDir = "/run/kukeon/secrets"

// secretFileMode is the mode applied to staged secret files that are bind-
// mounted into containers. Fixed at 0400 per the Container-secret contract.
const secretFileMode os.FileMode = 0o400

// resolvedSecrets captures the side effects produced by resolveSecrets —
// environment-variable entries ("NAME=value") to append to the container env
// and bind mounts that expose file-mounted secrets at their declared mount
// path.
type resolvedSecrets struct {
	EnvAdds   []string
	MountAdds []intmodel.VolumeMount
}

// resolveSecrets reads each declared ContainerSecret from its source — a host
// file (fromFile), a daemon-host env var (fromEnv), or a daemon-managed
// kind: Secret (secretRef, issue #623) — and returns env-var strings plus bind
// mounts for file mode. Resolved values never appear in the returned strings
// beyond the env entries themselves; callers must not log EnvAdds.
//
// stagingDir is the host directory under which per-container secret files
// are written (mode 0400). The directory is created on demand. runPath is the
// daemon's RunPath, used to locate secretRef material under the referenced
// scope's secrets tree (the same <RunPath>/data/<scope>/secrets/<name> layout
// the storage primitive writes — issue #619); callers that declare no
// secretRef may pass "".
func resolveSecrets(
	containerdID string,
	secrets []intmodel.ContainerSecret,
	stagingDir string,
	runPath string,
) (resolvedSecrets, error) {
	var out resolvedSecrets
	if len(secrets) == 0 {
		return out, nil
	}
	if strings.TrimSpace(stagingDir) == "" {
		stagingDir = DefaultSecretsStagingDir
	}

	var perContainerDir string
	for i, s := range secrets {
		name := strings.TrimSpace(s.Name)
		fromFile := strings.TrimSpace(s.FromFile)
		fromEnv := strings.TrimSpace(s.FromEnv)
		mountPath := strings.TrimSpace(s.MountPath)

		if name == "" {
			return resolvedSecrets{}, fmt.Errorf("%w (secrets[%d])", errdefs.ErrSecretNameRequired, i)
		}
		// Exactly one of fromFile / fromEnv / secretRef must be set. This is
		// already enforced by the apply parser, but the runner is reachable
		// from reconcile paths that bypass apply, so re-check defensively.
		sources := 0
		if fromFile != "" {
			sources++
		}
		if fromEnv != "" {
			sources++
		}
		if s.SecretRef != nil {
			sources++
		}
		switch {
		case sources == 0:
			return resolvedSecrets{}, fmt.Errorf(
				"%w (secrets[%d] %q)", errdefs.ErrSecretSourceRequired, i, name,
			)
		case sources > 1:
			return resolvedSecrets{}, fmt.Errorf(
				"%w (secrets[%d] %q)", errdefs.ErrSecretMultipleSources, i, name,
			)
		}
		if mountPath != "" && !filepath.IsAbs(mountPath) {
			return resolvedSecrets{}, fmt.Errorf(
				"%w (secrets[%d] %q mountPath %q)",
				errdefs.ErrSecretMountPathNotAbsolute, i, name, mountPath,
			)
		}

		value, err := readSecretValue(s, runPath)
		if err != nil {
			return resolvedSecrets{}, err
		}

		if mountPath == "" {
			out.EnvAdds = append(out.EnvAdds, name+"="+value)
			continue
		}

		if perContainerDir == "" {
			perContainerDir = filepath.Join(stagingDir, containerdID)
			if mkErr := os.MkdirAll(perContainerDir, 0o700); mkErr != nil {
				return resolvedSecrets{}, fmt.Errorf(
					"%w (secrets[%d] %q): %w",
					errdefs.ErrSecretStagingFailed, i, name, mkErr,
				)
			}
		}

		stagedPath := filepath.Join(perContainerDir, name)
		if writeErr := writeSecretFile(stagedPath, value); writeErr != nil {
			return resolvedSecrets{}, fmt.Errorf(
				"%w (secrets[%d] %q): %w",
				errdefs.ErrSecretStagingFailed, i, name, writeErr,
			)
		}

		out.MountAdds = append(out.MountAdds, intmodel.VolumeMount{
			Source:   stagedPath,
			Target:   mountPath,
			ReadOnly: true,
		})
	}

	return out, nil
}

func readSecretValue(s intmodel.ContainerSecret, runPath string) (string, error) {
	name := strings.TrimSpace(s.Name)
	fromFile := strings.TrimSpace(s.FromFile)
	fromEnv := strings.TrimSpace(s.FromEnv)

	switch {
	case s.SecretRef != nil:
		return readSecretRefValue(*s.SecretRef, runPath, name)
	case fromFile != "":
		data, err := os.ReadFile(fromFile)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf(
					"%w (secret %q path %q)",
					errdefs.ErrSecretFromFileNotFound, name, fromFile,
				)
			}
			return "", fmt.Errorf("failed to read secret %q from %q: %w", name, fromFile, err)
		}
		return string(data), nil
	default:
		value, ok := os.LookupEnv(fromEnv)
		if !ok {
			return "", fmt.Errorf(
				"%w (secret %q env %q)",
				errdefs.ErrSecretFromEnvNotSet, name, fromEnv,
			)
		}
		return value, nil
	}
}

// readSecretRefValue reads a daemon-managed kind: Secret's bytes from its
// scope's storage tree (issue #619 layout, fs.SecretPath). A missing file
// surfaces ErrSecretRefNotFound naming the referenced secret and its expected
// scope path; the path lets the operator confirm the scope, but the bytes are
// never logged. Issue #623.
func readSecretRefValue(ref intmodel.ContainerSecretRef, runPath, name string) (string, error) {
	path := fs.SecretPath(runPath, ref.Realm, ref.Space, ref.Stack, ref.Cell, ref.Name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf(
				"%w (secret %q ref %q at %q)",
				errdefs.ErrSecretRefNotFound, name, ref.Name, path,
			)
		}
		return "", fmt.Errorf("failed to read referenced secret %q from %q: %w", ref.Name, path, err)
	}
	return string(data), nil
}

// writeSecretFile writes the secret value atomically with 0400 perms so the
// final file is never briefly world-readable. The caller is responsible for
// the parent directory's creation and permissions.
func writeSecretFile(path, value string) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename didn't succeed.
		_ = os.Remove(tmpPath)
	}()
	if _, writeErr := tmp.WriteString(value); writeErr != nil {
		_ = tmp.Close()
		return writeErr
	}
	if chmodErr := tmp.Chmod(secretFileMode); chmodErr != nil {
		_ = tmp.Close()
		return chmodErr
	}
	if closeErr := tmp.Close(); closeErr != nil {
		return closeErr
	}
	return os.Rename(tmpPath, path)
}
