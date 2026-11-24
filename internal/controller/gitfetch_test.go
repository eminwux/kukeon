// Copyright 2026 Emiliano Spinella (eminwux)
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

package controller_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/eminwux/kukeon/internal/secretstore"
	"github.com/eminwux/kukeon/internal/util/fs"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// fetchFixture is a run path holding git identity secrets, a realm whose
// default git block is realmGit, and a controller over both.
type fetchFixture struct {
	runPath, root string
	secrets       map[string]intmodel.SecretSpec
	realmGit      *intmodel.ContainerGit
}

func newFetchFixture(t *testing.T) *fetchFixture {
	t.Helper()
	runPath := t.TempDir()
	return &fetchFixture{
		runPath: runPath, root: filepath.Join(runPath, "gitfetch"), secrets: map[string]intmodel.SecretSpec{},
	}
}

// storeSecret writes a secret's material where secretstore.Read finds it and
// keeps its identity fields for the runner's GetSecret.
func (fx *fetchFixture) storeSecret(t *testing.T, name string, spec intmodel.SecretSpec) {
	t.Helper()
	md := intmodel.SecretMetadata{Name: name, Realm: "default"}
	raw, err := secretstore.Encode(spec)
	if err != nil {
		t.Fatal(err)
	}
	path := fs.SecretPath(fx.runPath, md.Realm, "", "", "", md.Name)
	if err = os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = secretstore.WriteType(fx.runPath, md, spec.EffectiveType()); err != nil {
		t.Fatal(err)
	}
	fx.secrets[name] = spec
}

func (fx *fetchFixture) controller(t *testing.T) *controller.Exec {
	t.Helper()
	f := &fakeRunner{
		GetRealmFn: func(r intmodel.Realm) (intmodel.Realm, error) {
			realm := buildTestRealm(r.Metadata.Name, "")
			if fx.realmGit != nil {
				realm.Spec.Defaults = &intmodel.ScopeDefaults{Git: fx.realmGit}
			}
			return realm, nil
		},
		GetSecretFn: func(secret intmodel.Secret) (intmodel.Secret, error) {
			spec, ok := fx.secrets[secret.Metadata.Name]
			if !ok {
				return intmodel.Secret{}, errdefs.ErrSecretNotFound
			}
			secret.Spec = spec
			return secret, nil
		},
	}
	return controller.NewControllerExecForTesting(t.Context(), setupTestLogger(t), controller.Options{
		RunPath: fx.runPath, ContainerdSocket: "/test/containerd.sock", GitFetchDir: fx.root,
	}, f)
}

func fetchKey(t *testing.T, passphrase string) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	path := filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", passphrase, "-f", path).
		CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func sshIdentity(private, hosts string) intmodel.SecretSpec {
	return intmodel.SecretSpec{
		Type: intmodel.SecretTypeSSHKey, SSHKey: &intmodel.SecretSSHKey{PrivateKey: private},
		Connector: "git", Identity: map[string]string{"hosts": hosts},
	}
}

func stagedPlan(t *testing.T, stage controller.GitFetchStage) *gitplan.Plan {
	t.Helper()
	plan, err := gitplan.Load(gitplan.PlanPathAt(stage.Dir))
	if err != nil || plan == nil {
		t.Fatalf("plan at %s: %v, %v", stage.Dir, plan, err)
	}
	return plan
}

// TestStageGitFetch is #1643 Step 3.2: the Source's identity, else the realm
// default with a credential for the host, is staged in a directory the caller
// owns; the transport follows the credential.
func TestStageGitFetch(t *testing.T) {
	fx := newFetchFixture(t)
	fx.storeSecret(t, "git-ssh", sshIdentity(fetchKey(t, ""), "github.com"))
	fx.storeSecret(t, "git-https", intmodel.SecretSpec{
		Type: intmodel.SecretTypeBasicAuth, BasicAuth: &intmodel.SecretBasicAuth{Username: "bot", Token: "t0ken"},
		Connector: "git", Identity: map[string]string{"hosts": "git.example.org"},
	})
	fx.realmGit = &intmodel.ContainerGit{IdentityRef: &intmodel.ContainerSecretRef{Name: "git-ssh", Realm: "default"}}
	ctrl := fx.controller(t)
	uid := os.Getuid()

	// An SSH-key identity on the Source: an https address goes over ssh.
	stage, err := ctrl.StageGitFetch("", "https://github.com/eminwux/agents",
		&ext.ContainerSecretRef{Name: "git-ssh"}, uid)
	if err != nil || !stage.Staged {
		t.Fatalf("stage with an ssh identity = %+v, %v", stage, err)
	}
	repo := stagedPlan(t, stage).Repos["source"]
	if repo.URL != "git@github.com:eminwux/agents" {
		t.Fatalf("repo url = %q, want the scp-like ssh form", repo.URL)
	}
	if len(repo.Config) == 0 || repo.Config[0].Key != "core.sshCommand" ||
		repo.Config[0].Value != "ssh -F "+filepath.Join(stage.Dir, "repos", "r0", "ssh_config") {
		t.Fatalf("repo config = %+v, want ssh pointed at the staged config", repo.Config)
	}
	info, err := os.Stat(stage.Dir)
	if err != nil || info.Mode().Perm() != 0o700 || int(info.Sys().(*syscall.Stat_t).Uid) != uid {
		t.Fatalf("staged dir = %v, %v; want 0700 owned by %d", info, err, uid)
	}
	if rootInfo, statErr := os.Stat(fx.root); statErr != nil || rootInfo.Mode().Perm() != 0o711 {
		t.Fatalf("gitfetch root = %v, %v; want 0711", rootInfo, statErr)
	}

	// A basic-auth identity: an scp-like address goes over https.
	stage, err = ctrl.StageGitFetch("default", "git@git.example.org:team/app",
		&ext.ContainerSecretRef{Name: "git-https", Realm: "default"}, uid)
	if err != nil || !stage.Staged {
		t.Fatalf("stage with a basic-auth identity = %+v, %v", stage, err)
	}
	repo = stagedPlan(t, stage).Repos["source"]
	if repo.URL != "https://git.example.org/team/app" {
		t.Fatalf("repo url = %q", repo.URL)
	}
	helper := ""
	for _, entry := range repo.Config {
		if strings.HasSuffix(entry.Key, ".helper") && entry.Value != "" {
			helper = entry.Value
		}
	}
	if !strings.Contains(helper, stage.Dir) {
		t.Fatalf("credential helper = %q, want the staged one", helper)
	}

	// No identity: the realm default covers github.com.
	stage, err = ctrl.StageGitFetch("", "github.com/eminwux/kukeon", nil, uid)
	if err != nil || !stage.Staged {
		t.Fatalf("stage with the realm default = %+v, %v", stage, err)
	}
	if plan := stagedPlan(t, stage); !plan.SSHCommand || plan.Repos["source"].URL != "git@github.com:eminwux/kukeon" {
		t.Fatalf("plan = %+v", plan)
	}
	// ... but not another host.
	stage, err = ctrl.StageGitFetch("", "https://gitlab.com/team/app", nil, uid)
	if err != nil || stage.Staged {
		t.Fatalf("stage for a host the default does not cover = %+v, %v", stage, err)
	}
	// No realm default at all.
	fx.realmGit = nil
	if stage, err = fx.controller(t).StageGitFetch("", "github.com/eminwux/kukeon", nil, uid); err != nil ||
		stage.Staged {
		t.Fatalf("stage without a realm default = %+v, %v", stage, err)
	}
	// An identity that does not exist is an error, not a silent fallback.
	if _, err = ctrl.StageGitFetch("", "github.com/eminwux/kukeon",
		&ext.ContainerSecretRef{Name: "absent"}, uid); !errors.Is(err, errdefs.ErrGitFetchStaging) {
		t.Fatalf("stage with a missing identity = %v", err)
	}
	// An address that fails the check is refused and not repeated.
	if _, err = ctrl.StageGitFetch("", "https://tok:en@github.com/x/y", nil, uid); err == nil ||
		strings.Contains(err.Error(), "tok:en") {
		t.Fatalf("stage of a credentialed url = %v", err)
	}
}

// TestReleaseGitFetch: a release removes one staging by its id; an id that is
// not kukeond's hex name is rejected, and stale stagings are pruned.
func TestReleaseGitFetch(t *testing.T) {
	fx := newFetchFixture(t)
	fx.storeSecret(t, "git-ssh", sshIdentity(fetchKey(t, ""), "github.com"))
	ctrl := fx.controller(t)
	uid := os.Getuid()
	ref := &ext.ContainerSecretRef{Name: "git-ssh"}

	stage, err := ctrl.StageGitFetch("", "github.com/a/b", ref, uid)
	if err != nil || !stage.Staged {
		t.Fatalf("stage = %+v, %v", stage, err)
	}
	for _, id := range []string{"../x", "", "ABCDEF0123456789", stage.ID + "/.."} {
		if err = ctrl.ReleaseGitFetch(id, uid); !errors.Is(err, errdefs.ErrGitFetchStaging) {
			t.Errorf("ReleaseGitFetch(%q) = %v, want rejected", id, err)
		}
	}
	if err = ctrl.ReleaseGitFetch(stage.ID, uid); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stage.Dir); !os.IsNotExist(err) {
		t.Fatalf("staged dir survived release: %v", err)
	}
	if err = ctrl.ReleaseGitFetch(stage.ID, uid); err != nil {
		t.Fatalf("second release = %v, want nothing to do", err)
	}

	stale, err := ctrl.StageGitFetch("", "github.com/a/b", ref, uid)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err = os.Chtimes(stale.Dir, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err = ctrl.StageGitFetch("", "github.com/a/b", ref, uid); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(stale.Dir); !os.IsNotExist(err) {
		t.Fatalf("a staging older than ten minutes survived the next stage: %v", err)
	}
}
