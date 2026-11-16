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

package gitcred_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func ref(name string) *intmodel.ContainerSecretRef {
	return &intmodel.ContainerSecretRef{Name: name, Realm: "default"}
}

// secrets is an in-memory SecretReader.
type secrets map[string]intmodel.SecretSpec

func (s secrets) read(r intmodel.ContainerSecretRef) (intmodel.SecretSpec, error) {
	spec, ok := s[r.Name]
	if !ok {
		return intmodel.SecretSpec{}, errdefs.ErrSecretRefNotFound
	}
	return spec, nil
}

// genKey generates a key pair with ssh-keygen, skipping the test when the
// tool is unavailable.
func genKey(t *testing.T, keyType, passphrase string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	path := filepath.Join(t.TempDir(), "key")
	args := []string{"-q", "-t", keyType, "-N", passphrase, "-C", "kukeon-test", "-f", path}
	if keyType == "rsa" {
		args = append(args, "-b", "2048")
	}
	if out, err := exec.Command("ssh-keygen", args...).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	private, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	public, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	return string(private), strings.TrimSpace(string(public))
}

func sshKeySecret(private string) intmodel.SecretSpec {
	return intmodel.SecretSpec{
		Type:   intmodel.SecretTypeSSHKey,
		SSHKey: &intmodel.SecretSSHKey{PrivateKey: private},
	}
}

func basicSecret(user, password string) intmodel.SecretSpec {
	return intmodel.SecretSpec{
		Type:      intmodel.SecretTypeBasicAuth,
		BasicAuth: &intmodel.SecretBasicAuth{Username: user, Password: password},
	}
}

func fileOf(t *testing.T, b *gitcred.Bundle, name string) string {
	t.Helper()
	for _, f := range b.Files {
		if f.Path == name {
			return string(f.Data)
		}
	}
	var names []string
	for _, f := range b.Files {
		names = append(names, f.Path)
	}
	t.Fatalf("bundle has no file %q (have %v)", name, names)
	return ""
}

func hasFile(b *gitcred.Bundle, name string) bool {
	for _, f := range b.Files {
		if f.Path == name {
			return true
		}
	}
	return false
}

// stageRelocated stages the bundle in a temp dir with every in-container
// gitplan.MountPath reference rewritten to that dir, so real git and ssh can
// consume it on the test host.
func stageRelocated(t *testing.T, b *gitcred.Bundle) string {
	t.Helper()
	dir := t.TempDir()
	relocated := &gitcred.Bundle{Plan: b.Plan}
	for _, f := range b.Files {
		f.Data = bytes.ReplaceAll(f.Data, []byte(gitplan.MountPath), []byte(dir))
		relocated.Files = append(relocated.Files, f)
	}
	if err := gitcred.Stage(dir, relocated); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	return dir
}

func git(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	home := t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GIT_CONFIG_NOGLOBAL=1", "GIT_TERMINAL_PROMPT=0",
	}, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestNeeded(t *testing.T) {
	if gitcred.Needed(intmodel.ContainerSpec{}) {
		t.Fatal("empty spec must not need a bundle")
	}
	if !gitcred.Needed(intmodel.ContainerSpec{Git: &intmodel.ContainerGit{}}) {
		t.Fatal("a git block needs a bundle")
	}
	if !gitcred.Needed(intmodel.ContainerSpec{Repos: []intmodel.ContainerRepo{{Git: &intmodel.RepoGit{}}}}) {
		t.Fatal("a repo git block needs a bundle")
	}
}

func TestBuild_IdentityRendersConfigFileNotEnv(t *testing.T) {
	spec := intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
		Author:         &intmodel.GitIdentity{Name: `Dev "Bot"`, Email: "dev@example.com"},
		Committer:      &intmodel.GitIdentity{Name: "CI", Email: "ci@example.com"},
		SigningKey:     "/home/user/.ssh/id.pub",
		Sign:           []string{"commits"},
		AllowedSigners: "/home/user/.ssh/allowed",
	}}
	b, err := gitcred.Build(spec, secrets{}.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := stageRelocated(t, b)
	env := []string{gitplan.ConfigEnv + "=" + filepath.Join(dir, gitplan.ConfigFile)}
	for key, want := range map[string]string{
		"user.name":                  `Dev "Bot"`,
		"author.email":               "dev@example.com",
		"committer.name":             "CI",
		"committer.email":            "ci@example.com",
		"user.signingkey":            "/home/user/.ssh/id.pub",
		"gpg.format":                 "ssh",
		"commit.gpgsign":             "true",
		"gpg.ssh.allowedSignersFile": "/home/user/.ssh/allowed",
	} {
		if got := git(t, dir, env, "config", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// A repo-local value overrides the cell-global default — the point of
	// moving the identity out of GIT_AUTHOR_* env (step 7).
	repo := t.TempDir()
	git(t, repo, env, "init", "-q")
	git(t, repo, env, "config", "--local", "author.email", "other@example.com")
	if got := git(t, repo, env, "config", "--get", "author.email"); got != "other@example.com" {
		t.Errorf("repo-local author.email = %q, want override", got)
	}
}

func TestBuild_BasicAuthHelperServesCredential(t *testing.T) {
	spec := intmodel.ContainerSpec{
		Git: &intmodel.ContainerGit{Credentials: []intmodel.GitCredential{
			{Host: "git.example.com", SecretRef: ref("https-cred")},
			{Host: "https://git.example.com/team", SecretRef: ref("team-cred")},
		}},
		Repos: []intmodel.ContainerRepo{
			{Name: "app", URL: "git@git.example.com:other/app.git", Target: "/w/app"},
			{Name: "lib", URL: "https://git.example.com/team/lib.git", Target: "/w/lib"},
		},
	}
	store := secrets{
		"https-cred": basicSecret("alice", "s3cr3t pass"),
		"team-cred": {
			Type:      intmodel.SecretTypeBasicAuth,
			BasicAuth: &intmodel.SecretBasicAuth{Username: "bot", Token: "tok-123"},
		},
	}
	b, err := gitcred.Build(spec, store.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	// The SSH-declared repo follows its basic-auth credential onto HTTPS.
	if got := b.Plan.Repos["app"].URL; got != "https://git.example.com/other/app.git" {
		t.Errorf("app URL = %q, want https transport", got)
	}
	if _, planned := b.Plan.Repos["lib"]; planned {
		t.Errorf("lib needs no plan entry: its URL and helper are already global, got %+v", b.Plan.Repos["lib"])
	}
	for _, f := range b.Files {
		if f.Path == gitplan.ConfigFile || f.Path == gitplan.PlanFile {
			if strings.Contains(string(f.Data), "s3cr3t") || strings.Contains(string(f.Data), "tok-123") {
				t.Fatalf("%s leaks credential material", f.Path)
			}
		}
	}

	dir := stageRelocated(t, b)
	env := []string{gitplan.ConfigEnv + "=" + filepath.Join(dir, gitplan.ConfigFile)}
	fill := func(input string) string {
		cmd := exec.Command("git", "credential", "fill")
		cmd.Stdin = strings.NewReader(input)
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_TERMINAL_PROMPT=0"}, env...)
		out, runErr := cmd.CombinedOutput()
		if runErr != nil {
			t.Fatalf("git credential fill: %v: %s", runErr, out)
		}
		return string(out)
	}
	host := fill("protocol=https\nhost=git.example.com\npath=other/app.git\n\n")
	if !strings.Contains(host, "username=alice\n") || !strings.Contains(host, "password=s3cr3t pass\n") {
		t.Errorf("host credential = %q", host)
	}
	team := fill("protocol=https\nhost=git.example.com\npath=team/lib.git\n\n")
	if !strings.Contains(team, "username=bot\n") || !strings.Contains(team, "password=tok-123\n") {
		t.Errorf("prefix credential = %q", team)
	}
}

// A remote on a non-default port (or plain http) keeps that port and scheme in
// its clone URL, which the cell-global helper section — scoped to the
// credential's own `https://host` — does not cover. The checkout then carries
// a helper section for the URL it really uses.
func TestBuild_BasicAuthHelperFollowsRemotePortAndScheme(t *testing.T) {
	spec := intmodel.ContainerSpec{
		Git: &intmodel.ContainerGit{Credentials: []intmodel.GitCredential{
			{Host: "git.example.com", SecretRef: ref("https-cred")},
		}},
		Repos: []intmodel.ContainerRepo{
			{Name: "default", URL: "https://git.example.com/o/default.git", Target: "/w/default"},
			{Name: "port", URL: "https://git.example.com:8443/o/port.git", Target: "/w/port"},
			{Name: "plain", URL: "http://git.example.com/o/plain.git", Target: "/w/plain"},
		},
	}
	store := secrets{"https-cred": basicSecret("alice", "s3cr3t")}
	b, err := gitcred.Build(spec, store.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, planned := b.Plan.Repos["default"]; planned {
		t.Errorf("default-port https repo needs no plan entry, got %+v", b.Plan.Repos["default"])
	}

	dir := stageRelocated(t, b)
	for name, want := range map[string]struct{ protocol, host string }{
		"port":  {"https", "git.example.com:8443"},
		"plain": {"http", "git.example.com"},
	} {
		entry, planned := b.Plan.Repos[name]
		if !planned || len(entry.Config) == 0 {
			t.Fatalf("%s: no repo-level helper section planned: %+v", name, entry)
		}
		// Apply the planned repo-local config to a real repository and ask git
		// for the credential the way a clone of that URL would.
		repo := filepath.Join(t.TempDir(), name)
		run := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			cmd.Env = []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "GIT_TERMINAL_PROMPT=0",
				gitplan.ConfigEnv + "=" + filepath.Join(dir, gitplan.ConfigFile),
			}
			cmd.Stdin = strings.NewReader(
				"protocol=" + want.protocol + "\nhost=" + want.host + "\npath=o/" + name + ".git\n\n",
			)
			out, runErr := cmd.CombinedOutput()
			if runErr != nil {
				t.Fatalf("%s: git %v: %v: %s", name, args, runErr, out)
			}
			return string(out)
		}
		if err = os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		run("init", "-q")
		for _, c := range entry.Config {
			value := strings.ReplaceAll(c.Value, gitplan.MountPath, dir)
			if c.Add {
				run("config", "--local", "--add", c.Key, value)
			} else {
				run("config", "--local", c.Key, value)
			}
		}
		got := run("credential", "fill")
		if !strings.Contains(got, "username=alice\n") || !strings.Contains(got, "password=s3cr3t\n") {
			t.Errorf("%s: credential for %s://%s = %q, want alice's", name, want.protocol, want.host, got)
		}
	}
}

func TestBuild_SSHCredentialRendersHostConfig(t *testing.T) {
	private, public := genKey(t, "ed25519", "")
	spec := intmodel.ContainerSpec{
		Git: &intmodel.ContainerGit{Credentials: []intmodel.GitCredential{
			{Host: "github.com", SecretRef: ref("gh")},
			{
				Host: "git.internal:2222", SecretRef: ref("internal"), User: "gitea",
				HostKeyPolicy: intmodel.GitHostKeyPolicyAcceptNew,
			},
		}},
		Repos: []intmodel.ContainerRepo{
			{Name: "agents", URL: "https://github.com/eminwux/agents", Target: "/w/agents"},
			{Name: "infra", URL: "https://git.internal/ops/infra.git", Target: "/w/infra"},
			{Name: "local", URL: "/srv/git/local.git", Target: "/w/local"},
		},
	}
	pinned := "git.internal ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPinnedPinnedPinnedPinnedPinnedPinnedPinnedPinn\n"
	internal := sshKeySecret(private)
	internal.SSHKey.KnownHosts = pinned
	internal.SSHKey.Certificate = "ssh-ed25519-cert-v01@openssh.com AAAAcert kukeon"
	b, err := gitcred.Build(spec, secrets{"gh": sshKeySecret(private), "internal": internal}.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !b.Plan.SSHCommand {
		t.Error("plan must report the managed core.sshCommand")
	}
	// HTTPS-declared repos follow their ssh-key credential onto SSH (#1441).
	if got := b.Plan.Repos["agents"].URL; got != "git@github.com:eminwux/agents" {
		t.Errorf("agents URL = %q", got)
	}
	if got := b.Plan.Repos["infra"].URL; got != "ssh://gitea@git.internal:2222/ops/infra.git" {
		t.Errorf("infra URL = %q", got)
	}
	if _, planned := b.Plan.Repos["local"]; planned {
		t.Error("a local path must not be planned")
	}
	// Host keys: built-in for github.com, pinned (not built-in) for the other.
	if got := fileOf(t, b, "known_hosts.d/c0"); got != gitplan.BuiltinKnownHosts("github.com", 0) || got == "" {
		t.Errorf("github known_hosts = %q, want built-in keys", got)
	}
	if got := fileOf(t, b, "known_hosts.d/c1"); got != pinned {
		t.Errorf("pinned known_hosts = %q", got)
	}
	// The public key is derived from the OpenSSH private key header.
	keyDirs := map[string]string{}
	for _, f := range b.Files {
		if strings.HasSuffix(f.Path, "/key.pub") {
			name, _, _ := strings.Cut(filepath.Base(filepath.Dir(f.Path)), "-")
			keyDirs[name] = filepath.Dir(f.Path)
			if !strings.HasPrefix(public, strings.TrimSpace(string(f.Data))) {
				t.Errorf("derived public key %q does not match %q", f.Data, public)
			}
		}
		if f.Mode&0o077 != 0 {
			t.Errorf("%s mode %v is not owner-only", f.Path, f.Mode)
		}
	}
	if keyDirs["gh"] == "" || keyDirs["internal"] == "" {
		t.Fatalf("expected one staged key per secret, got %v", keyDirs)
	}

	if _, lookErr := exec.LookPath("ssh"); lookErr != nil {
		t.Skip("ssh not available")
	}
	dir := stageRelocated(t, b)
	resolve := func(host string) string {
		out, runErr := exec.Command("ssh", "-G", "-F", filepath.Join(dir, gitplan.SSHConfigFile), host).CombinedOutput()
		if runErr != nil {
			t.Fatalf("ssh -G %s: %v: %s", host, runErr, out)
		}
		return string(out)
	}
	gh := resolve("github.com")
	for _, want := range []string{
		"user git\n", "identitiesonly yes\n", "batchmode yes\n", "stricthostkeychecking true\n",
		"identityfile " + filepath.Join(dir, keyDirs["gh"], "key") + "\n",
		"userknownhostsfile " + filepath.Join(dir, "known_hosts.d", "c0") + "\n",
	} {
		if !strings.Contains(gh, want) {
			t.Errorf("github.com ssh config missing %q in:\n%s", want, gh)
		}
	}
	in := resolve("git.internal")
	for _, want := range []string{
		"user gitea\n", "port 2222\n", "stricthostkeychecking accept-new\n",
		"certificatefile " + filepath.Join(dir, keyDirs["internal"], "key-cert.pub") + "\n",
	} {
		if !strings.Contains(in, want) {
			t.Errorf("git.internal ssh config missing %q in:\n%s", want, in)
		}
	}
	// A host no credential covers still gets the built-in forge keys.
	if other := resolve("gitlab.com"); !strings.Contains(other, filepath.Join(dir, gitplan.KnownHostsFile)) {
		t.Errorf("uncovered host does not check the built-in keys:\n%s", other)
	}
}

func TestBuild_PerRepoIdentityAndCredential(t *testing.T) {
	private, _ := genKey(t, "ecdsa", "")
	deploy, _ := genKey(t, "rsa", "")
	spec := intmodel.ContainerSpec{
		Git: &intmodel.ContainerGit{
			Author:      &intmodel.GitIdentity{Name: "Default", Email: "default@example.com"},
			Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: ref("default-key")}},
		},
		Repos: []intmodel.ContainerRepo{
			{Name: "role-source", URL: "git@github.com:eminwux/agents.git", Target: "/w/agents"},
			{
				Name: "project", URL: "https://github.com/eminwux/kukeon.git", Target: "/w/kukeon",
				Git: &intmodel.RepoGit{
					Author:     &intmodel.GitIdentity{Name: "Bot", Email: "bot@kukeon.io"},
					Credential: &intmodel.GitCredential{SecretRef: ref("deploy-key")},
					Signing:    &intmodel.GitSigning{SecretRef: ref("deploy-key")},
				},
			},
		},
	}
	b, err := gitcred.Build(
		spec,
		secrets{"default-key": sshKeySecret(private), "deploy-key": sshKeySecret(deploy)}.read,
	)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, planned := b.Plan.Repos["role-source"]; planned {
		t.Error("role-source uses the default identity and needs no plan entry")
	}
	project := b.Plan.Repos["project"]
	if project.URL != "git@github.com:eminwux/kukeon.git" {
		t.Errorf("project URL = %q", project.URL)
	}
	got := map[string]string{}
	for _, c := range project.Config {
		got[c.Key] = c.Value
	}
	if got["user.email"] != "bot@kukeon.io" || got["author.name"] != "Bot" {
		t.Errorf("project identity = %v", got)
	}
	if !strings.HasPrefix(got["core.sshCommand"], "ssh -F "+gitplan.MountPath+"/repos/r1/") {
		t.Errorf("project core.sshCommand = %q", got["core.sshCommand"])
	}
	if got["gpg.format"] != "ssh" || got["commit.gpgsign"] != "true" || got["tag.gpgsign"] != "true" {
		t.Errorf("project signing = %v", got)
	}
	if !strings.HasSuffix(got["user.signingkey"], "/key") {
		t.Errorf("unencrypted signing key should be named by its private key file, got %q", got["user.signingkey"])
	}
	repoSSH := fileOf(t, b, "repos/r1/ssh_config")
	globalSSH := fileOf(t, b, gitplan.SSHConfigFile)
	if repoSSH == globalSSH || !strings.Contains(repoSSH, "Host github.com\n") {
		t.Errorf("project needs its own ssh config:\n%s", repoSSH)
	}
}

func TestBuild_SignsAndVerifiesWithStoredKey(t *testing.T) {
	private, public := genKey(t, "ed25519", "")
	secret := sshKeySecret(private)
	secret.SSHKey.PublicKey = public
	spec := intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
		Author:  &intmodel.GitIdentity{Name: "Signer", Email: "signer@example.com"},
		Signing: &intmodel.GitSigning{SecretRef: ref("signing")},
	}}
	b, err := gitcred.Build(spec, secrets{"signing": secret}.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	dir := stageRelocated(t, b)
	env := []string{gitplan.ConfigEnv + "=" + filepath.Join(dir, gitplan.ConfigFile)}
	repo := t.TempDir()
	git(t, repo, env, "init", "-q")
	git(t, repo, env, "commit", "-q", "--allow-empty", "-m", "signed")
	git(t, repo, env, "tag", "-m", "v1", "v1")
	// The generated allowed-signers file trusts the stored key for the
	// committer email, so the cell verifies its own signatures.
	git(t, repo, env, "verify-commit", "HEAD")
	git(t, repo, env, "verify-tag", "v1")
}

func TestBuild_PassphraseKeyIsPlannedForTheAgent(t *testing.T) {
	private, public := genKey(t, "ed25519", "open sesame")
	withPass := sshKeySecret(private)
	withPass.SSHKey.Passphrase = "open sesame"
	spec := intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
		Author:      &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
		Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: ref("locked")}},
		Signing:     &intmodel.GitSigning{SecretRef: ref("locked")},
	}}
	b, err := gitcred.Build(spec, secrets{"locked": withPass}.read)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(b.Plan.AgentKeys) != 1 {
		t.Fatalf("AgentKeys = %+v, want one key", b.Plan.AgentKeys)
	}
	agentKey := b.Plan.AgentKeys[0]
	rel := strings.TrimPrefix(agentKey.PassphraseFile, gitplan.MountPath+"/")
	if got := fileOf(t, b, rel); got != "open sesame\n" {
		t.Errorf("passphrase file = %q", got)
	}
	if plan := fileOf(t, b, gitplan.PlanFile); strings.Contains(plan, "open sesame") {
		t.Error("the plan must carry the passphrase file path, never the passphrase")
	}
	// A locked key signs through the agent, so git is given the public half —
	// derived here from the encrypted key's cleartext header.
	config := fileOf(t, b, gitplan.ConfigFile)
	if !strings.Contains(config, "/key.pub\"") {
		t.Errorf("signing key should be the public key file:\n%s", config)
	}
	pub := fileOf(t, b, strings.TrimPrefix(agentKey.Key, gitplan.MountPath+"/")+".pub")
	if !strings.HasPrefix(public, strings.TrimSpace(pub)) {
		t.Errorf("derived public key %q does not match %q", pub, public)
	}

	// The same key without its passphrase is refused up front.
	_, err = gitcred.Build(spec, secrets{"locked": sshKeySecret(private)}.read)
	if !errors.Is(err, errdefs.ErrSecretTypeFields) {
		t.Errorf("missing passphrase error = %v, want ErrSecretTypeFields", err)
	}
}

func TestBuild_RejectsWrongSecretType(t *testing.T) {
	opaque := intmodel.SecretSpec{Data: "ghp_token"}
	cases := map[string]intmodel.ContainerSpec{
		"credential": {Git: &intmodel.ContainerGit{
			Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: ref("token")}},
		}},
		"signing": {Git: &intmodel.ContainerGit{Signing: &intmodel.GitSigning{SecretRef: ref("token")}}},
		"repo credential": {Repos: []intmodel.ContainerRepo{{
			Name: "r", URL: "https://github.com/o/r", Target: "/w/r",
			Git: &intmodel.RepoGit{Credential: &intmodel.GitCredential{SecretRef: ref("token")}},
		}}},
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			// A GitHub API token is an opaque secret: it can never become a
			// git credential, so it cannot take over a push (#1441).
			if _, err := gitcred.Build(spec, secrets{"token": opaque}.read); !errors.Is(
				err,
				errdefs.ErrSecretTypeMismatch,
			) {
				t.Errorf("err = %v, want ErrSecretTypeMismatch", err)
			}
		})
	}
	missing := intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
		Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: ref("absent")}},
	}}
	if _, err := gitcred.Build(missing, secrets{}.read); !errors.Is(err, errdefs.ErrSecretRefNotFound) {
		t.Errorf("err = %v, want ErrSecretRefNotFound", err)
	}
}

func TestStage_ReplacesContentsAndKeepsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stale"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := gitcred.Build(intmodel.ContainerSpec{Git: &intmodel.ContainerGit{
		Author: &intmodel.GitIdentity{Name: "A", Email: "a@example.com"},
	}}, secrets{}.read)
	if err != nil {
		t.Fatal(err)
	}
	if err = gitcred.Stage(dir, b); err != nil {
		t.Fatalf("Stage: %v", err)
	}
	after, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("Stage must keep the directory inode: it is a live bind-mount source")
	}
	if _, err = os.Stat(filepath.Join(dir, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale file survived: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, gitplan.ConfigFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("gitconfig stat = %v, %v", info, err)
	}
	if !hasFile(b, gitplan.PlanFile) || !hasFile(b, gitplan.KnownHostsFile) {
		t.Error("bundle must always carry the plan and the built-in host keys")
	}
}
