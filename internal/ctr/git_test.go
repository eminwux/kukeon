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
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/internal/util/fs"
)

// writeTypedScopedSecret stores a typed realm-scoped Secret the way the
// storage runner does: type sidecar plus the encoded bytes file.
func writeTypedScopedSecret(t *testing.T, runPath, realm, name string, spec intmodel.SecretSpec) {
	t.Helper()
	md := intmodel.SecretMetadata{Name: name, Realm: realm}
	if err := os.MkdirAll(fs.SecretsDir(runPath, realm, "", "", ""), 0o700); err != nil {
		t.Fatalf("mkdir secrets dir: %v", err)
	}
	if err := secretstore.WriteType(runPath, md, spec.EffectiveType()); err != nil {
		t.Fatalf("WriteType: %v", err)
	}
	body, err := secretstore.Encode(spec)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if err = os.WriteFile(fs.SecretPath(runPath, realm, "", "", "", name), body, 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
}

// TestResolveSecrets_RejectsTypedSecretRef locks that a typed Secret can only
// be consumed through a git block: referencing it from secrets[] would inject
// its JSON body (password, private key) as an env var or a plain file.
func TestResolveSecrets_RejectsTypedSecretRef(t *testing.T) {
	t.Parallel()

	const material = "tok-do-not-leak"
	runPath := t.TempDir()
	writeTypedScopedSecret(t, runPath, "default", "forge-token", intmodel.SecretSpec{
		Type:      intmodel.SecretTypeBasicAuth,
		BasicAuth: &intmodel.SecretBasicAuth{Username: "u", Token: material},
	})

	for _, mountPath := range []string{"", "/etc/secrets/forge"} {
		stagingDir := t.TempDir()
		got, err := resolveSecrets("cntr-typed", []intmodel.ContainerSecret{{
			Name:      "FORGE_TOKEN",
			SecretRef: &intmodel.ContainerSecretRef{Name: "forge-token", Realm: "default"},
			MountPath: mountPath,
		}}, stagingDir, runPath)
		if !errors.Is(err, errdefs.ErrSecretTypedRef) {
			t.Fatalf("mountPath %q: err = %v, want ErrSecretTypedRef", mountPath, err)
		}
		if strings.Contains(err.Error(), material) {
			t.Errorf("mountPath %q: error echoes secret material: %v", mountPath, err)
		}
		if len(got.EnvAdds) != 0 || len(got.MountAdds) != 0 {
			t.Errorf("mountPath %q: typed secret leaked into env/mounts: %+v", mountPath, got)
		}
	}
}

func TestContainerGitStagingDir(t *testing.T) {
	t.Parallel()

	got := ContainerGitStagingDir(intmodel.ContainerSpec{ID: "work", ContainerdID: "s_st_c_work"}, "")
	if want := filepath.Join(DefaultSecretsStagingDir, "s_st_c_work", "git"); got != want {
		t.Errorf("ContainerGitStagingDir = %q, want %q", got, want)
	}
	// Falls back to the container ID when no containerd ID is assigned yet.
	got = ContainerGitStagingDir(intmodel.ContainerSpec{ID: "work"}, "")
	if want := filepath.Join(DefaultSecretsStagingDir, "work", "git"); got != want {
		t.Errorf("ContainerGitStagingDir (no containerd id) = %q, want %q", got, want)
	}
	// A daemon on another socket stages beside that socket.
	root := SecretsStagingDir("/run/kukeon-dev/kukeond.sock")
	got = ContainerGitStagingDir(intmodel.ContainerSpec{ID: "work", ContainerdID: "s_st_c_work"}, root)
	if want := "/run/kukeon-dev/secrets/s_st_c_work/git"; got != want {
		t.Errorf("ContainerGitStagingDir (staging root %q) = %q, want %q", root, got, want)
	}
}

func TestSecretsStagingDir(t *testing.T) {
	t.Parallel()

	for socket, want := range map[string]string{
		"":                             DefaultSecretsStagingDir,
		"  ":                           DefaultSecretsStagingDir,
		"/run/kukeon/kukeond.sock":     DefaultSecretsStagingDir,
		"/run/kukeon-dev/kukeond.sock": "/run/kukeon-dev/secrets",
		"/tmp/e2e/run/kukeond.sock":    "/tmp/e2e/run/secrets",
	} {
		if got := SecretsStagingDir(socket); got != want {
			t.Errorf("SecretsStagingDir(%q) = %q, want %q", socket, got, want)
		}
	}
}

// A git block is staged under the staging root the caller names, and the bind
// mount it returns points there: the directory beside the daemon's socket,
// which is the one the daemon's cell shares with the host.
func TestStageContainerGit_StagingRoot(t *testing.T) {
	t.Parallel()

	runPath := t.TempDir()
	root := filepath.Join(t.TempDir(), "secrets")
	spec := intmodel.ContainerSpec{
		ID:           "work",
		ContainerdID: "s_st_c_work",
		Git:          &intmodel.ContainerGit{Author: &intmodel.GitIdentity{Name: "Dev", Email: "dev@example.com"}},
	}
	mount, err := StageContainerGit(spec, runPath, root)
	if err != nil {
		t.Fatalf("StageContainerGit: %v", err)
	}
	if mount == nil {
		t.Fatal("StageContainerGit returned no mount for a git block")
	}
	if want := filepath.Join(root, "s_st_c_work", "git"); mount.Source != want {
		t.Errorf("mount source = %q, want %q", mount.Source, want)
	}
	if _, statErr := os.Stat(mount.Source); statErr != nil {
		t.Errorf("staging directory: %v", statErr)
	}
}

// TestStageContainerGit_NoGitNoMount locks the no-op: a container with no git
// declaration gets no mount and nothing is staged for it.
func TestStageContainerGit_NoGitNoMount(t *testing.T) {
	t.Parallel()

	spec := intmodel.ContainerSpec{
		ID:           "work",
		ContainerdID: uniqueContainerdID(t),
		Repos:        []intmodel.ContainerRepo{{Name: "src", URL: "https://example.com/o/r.git"}},
	}
	mount, err := StageContainerGit(spec, t.TempDir(), "")
	if err != nil {
		t.Fatalf("StageContainerGit: %v", err)
	}
	if mount != nil {
		t.Errorf("mount = %+v, want nil for a container with no git block", mount)
	}
	if _, statErr := os.Stat(ContainerGitStagingDir(spec, "")); !os.IsNotExist(statErr) {
		t.Errorf("staging dir exists for a container with no git block (err=%v)", statErr)
	}
}

// TestStageContainerGit_MissingSecretStagesNothing asserts a credential whose
// secret does not exist fails the render before anything is written.
func TestStageContainerGit_MissingSecretStagesNothing(t *testing.T) {
	t.Parallel()

	spec := intmodel.ContainerSpec{
		ID:           "work",
		ContainerdID: uniqueContainerdID(t),
		Git: &intmodel.ContainerGit{Credentials: []intmodel.GitCredential{{
			Host:      "github.com",
			SecretRef: &intmodel.ContainerSecretRef{Name: "absent", Realm: "default"},
		}}},
	}
	mount, err := StageContainerGit(spec, t.TempDir(), "")
	if !errors.Is(err, errdefs.ErrSecretRefNotFound) {
		t.Fatalf("err = %v, want ErrSecretRefNotFound", err)
	}
	if mount != nil {
		t.Errorf("mount = %+v, want nil on error", mount)
	}
	if _, statErr := os.Stat(ContainerGitStagingDir(spec, "")); !os.IsNotExist(statErr) {
		t.Errorf("staging dir exists after a failed render (err=%v)", statErr)
	}
}

// TestStageContainerGit_StagesBundle covers the success path end to end: the
// bundle (gitconfig, plan, credential helper) lands under the container's
// staging directory, owner-only, and the returned mount binds it read-only at
// the fixed in-container path. The staging root is the daemon's fixed
// /run/kukeon/secrets, so this runs only where that is writable (root).
func TestStageContainerGit_StagesBundle(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skipf("staging root %s is root-only", DefaultSecretsStagingDir)
	}
	_, statErr := os.Stat(DefaultSecretsStagingDir)
	rootExisted := statErr == nil

	const material = "tok-123"
	runPath := t.TempDir()
	writeTypedScopedSecret(t, runPath, "default", "forge-token", intmodel.SecretSpec{
		Type:      intmodel.SecretTypeBasicAuth,
		BasicAuth: &intmodel.SecretBasicAuth{Username: "x-access-token", Token: material},
	})
	spec := intmodel.ContainerSpec{
		ID:           "work",
		ContainerdID: uniqueContainerdID(t),
		Git: &intmodel.ContainerGit{
			Author: &intmodel.GitIdentity{Name: "Dev", Email: "dev@example.com"},
			Credentials: []intmodel.GitCredential{{
				Host:      "https://git.example.com",
				SecretRef: &intmodel.ContainerSecretRef{Name: "forge-token", Realm: "default"},
			}},
		},
	}
	dir := ContainerGitStagingDir(spec, "")
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Dir(dir))
		if !rootExisted {
			// Only succeeds when the test was the directory's sole user.
			_ = os.Remove(DefaultSecretsStagingDir)
		}
	})

	mount, err := StageContainerGit(spec, runPath, "")
	if err != nil {
		if errors.Is(err, errdefs.ErrSecretStagingFailed) {
			t.Skipf("staging root not writable here: %v", err)
		}
		t.Fatalf("StageContainerGit: %v", err)
	}
	if mount == nil || mount.Source != dir || mount.Target != gitplan.MountPath || !mount.ReadOnly {
		t.Fatalf("mount = %+v, want read-only %s -> %s", mount, dir, gitplan.MountPath)
	}

	config, err := os.ReadFile(filepath.Join(dir, gitplan.ConfigFile))
	if err != nil {
		t.Fatalf("read staged gitconfig: %v", err)
	}
	if !strings.Contains(string(config), "dev@example.com") {
		t.Errorf("staged gitconfig lacks the identity:\n%s", config)
	}
	if strings.Contains(string(config), material) {
		t.Errorf("staged gitconfig carries the token itself:\n%s", config)
	}
	if _, err = os.Stat(filepath.Join(dir, gitplan.PlanFile)); err != nil {
		t.Errorf("plan file not staged: %v", err)
	}

	var sawToken bool
	walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode = %o, want owner-only", p, info.Mode().Perm())
		}
		if !d.IsDir() {
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			sawToken = sawToken || strings.Contains(string(data), material)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk staging dir: %v", walkErr)
	}
	if !sawToken {
		t.Errorf("no staged file carries the credential the helper must serve")
	}

	// Re-staging replaces the contents in place rather than failing or
	// accumulating: a stray file from an earlier render is gone.
	stray := filepath.Join(dir, "stale")
	if err = os.WriteFile(stray, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = StageContainerGit(spec, runPath, ""); err != nil {
		t.Fatalf("re-stage: %v", err)
	}
	if _, statErr = os.Stat(stray); !os.IsNotExist(statErr) {
		t.Errorf("stale file survived a re-stage (err=%v)", statErr)
	}
}

// uniqueContainerdID returns an ID no real container uses, so a test that
// computes (or, as root, writes) a staging path can never collide with one.
func uniqueContainerdID(t *testing.T) string {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return "kukeon-unit-test-" + hex.EncodeToString(b[:])
}
