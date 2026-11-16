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

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestMain lets the test binary stand in for kuketty's SSH_ASKPASS mode:
// startGitAgent points ssh-add at os.Executable(), which under `go test` is
// this binary, so it must answer the passphrase prompt exactly as main does
// instead of re-running the test suite.
func TestMain(m *testing.M) {
	if file := os.Getenv(gitplan.AskpassFileEnv); file != "" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		os.Exit(askpass(file, prompt, os.Stdout))
	}
	os.Exit(m.Run())
}

// hermeticGit isolates the git kuketty spawns from the host's global and
// system config and from any ssh override, so the assertions below only see
// what the plan wrote.
func hermeticGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("HOME", t.TempDir())
}

// gitOut runs a read-only git command in dir and returns its trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v in %q: %v", args, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// makeBareOrigin returns a bare repo holding one commit on main, the shape of
// a forge remote.
func makeBareOrigin(t *testing.T) string {
	t.Helper()
	src := makeSourceRepo(t, "README.md")
	bare := filepath.Join(t.TempDir(), "origin.git")
	gitRun(t, "", "clone", "--bare", src, bare)
	return bare
}

// TestProcessRepos_PlanRewritesURLAndWritesLocalConfig covers the per-source
// resolution (#1472, steps 3 and 7): the clone goes to the plan's URL rather
// than the declared one, origin is left pointing there, and the planned
// identity lands in the checkout's local config where it outranks the
// cell-global default — with Add entries accumulating on a multi-valued key.
func TestProcessRepos_PlanRewritesURLAndWritesLocalConfig(t *testing.T) {
	hermeticGit(t)
	origin := makeBareOrigin(t)
	target := filepath.Join(t.TempDir(), "checkout")

	// The declared URL does not exist: the clone can only succeed through the
	// plan's rewrite.
	declared := filepath.Join(t.TempDir(), "declared-but-absent.git")
	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: declared, Required: true},
	}
	plan := &gitplan.Plan{Repos: map[string]gitplan.Repo{
		"project": {
			URL: origin,
			Config: []gitplan.ConfigEntry{
				{Key: "user.name", Value: "Repo Dev"},
				{Key: "user.email", Value: "repo@example.com"},
				{Key: "kukeon.test", Value: "first"},
				{Key: "kukeon.test", Value: "second", Add: true},
			},
		},
		// An entry for a repo the container does not declare is inert.
		"other": {URL: declared},
	}}

	statuses, err := processRepos(context.Background(), repos, plan, discardLogger())
	if err != nil {
		t.Fatalf("processRepos: %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateCloned || statuses[0].Error != "" {
		t.Fatalf("status = %+v, want a single clean clone", statuses)
	}
	if got := gitOut(t, target, "remote", "get-url", "origin"); got != origin {
		t.Errorf("origin = %q, want the planned URL %q", got, origin)
	}
	if got := gitOut(t, target, "config", "--local", "--get", "user.email"); got != "repo@example.com" {
		t.Errorf("local user.email = %q, want repo@example.com", got)
	}
	if got := gitOut(t, target, "config", "--local", "--get", "user.name"); got != "Repo Dev" {
		t.Errorf("local user.name = %q, want Repo Dev", got)
	}
	if got := gitOut(t, target, "config", "--local", "--get-all", "kukeon.test"); got != "first\nsecond" {
		t.Errorf("multi-valued key = %q, want first then second", got)
	}

	// A commit made in the checkout carries the source's identity even though
	// a different cell-global identity is configured.
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err = os.WriteFile(global, []byte("[user]\n\tname = Cell Default\n\temail = cell@example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "from the workload")
	commit.Dir = target
	// A minimal environment: an inherited GIT_AUTHOR_* / GIT_COMMITTER_* would
	// outrank every config file and hide what the local config resolves to.
	commit.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(),
		"GIT_CONFIG_SYSTEM=" + global, "GIT_CONFIG_GLOBAL=" + os.DevNull,
	}
	if out, runErr := commit.CombinedOutput(); runErr != nil {
		t.Fatalf("commit in checkout: %v\n%s", runErr, out)
	}
	if got := gitOut(t, target, "log", "-1", "--format=%an <%ae>"); got != "Repo Dev <repo@example.com>" {
		t.Errorf("commit author = %q, want the per-source identity", got)
	}
}

// TestProcessRepos_PlanRefreshedOnRestart covers the fetch path: an existing
// checkout is re-pointed and its local config rewritten from the current plan
// before fetching, so an edited identity or credential takes effect on the
// next start, and a replaced multi-valued key does not accumulate stale values.
func TestProcessRepos_PlanRefreshedOnRestart(t *testing.T) {
	hermeticGit(t)
	origin := makeBareOrigin(t)
	target := filepath.Join(t.TempDir(), "checkout")
	declared := filepath.Join(t.TempDir(), "declared-but-absent.git")
	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: declared, Required: true},
	}
	planFor := func(email, extra string) *gitplan.Plan {
		return &gitplan.Plan{Repos: map[string]gitplan.Repo{"project": {
			URL: origin,
			Config: []gitplan.ConfigEntry{
				{Key: "user.name", Value: "Repo Dev"},
				{Key: "user.email", Value: email},
				{Key: "kukeon.test", Value: "base"},
				{Key: "kukeon.test", Value: extra, Add: true},
			},
		}}}
	}

	if _, err := processRepos(context.Background(), repos, planFor("old@example.com", "old"), discardLogger()); err != nil {
		t.Fatalf("first processRepos (clone): %v", err)
	}
	// The workload (or an operator) re-pointed origin; a restart restores it.
	gitRun(t, target, "remote", "set-url", "origin", declared)

	statuses, err := processRepos(context.Background(), repos, planFor("new@example.com", "new"), discardLogger())
	if err != nil {
		t.Fatalf("second processRepos (fetch): %v", err)
	}
	if len(statuses) != 1 || statuses[0].State != setupstatus.StateFetched || statuses[0].Error != "" {
		t.Fatalf("status = %+v, want a single clean fetch", statuses)
	}
	if got := gitOut(t, target, "remote", "get-url", "origin"); got != origin {
		t.Errorf("origin = %q, want it re-pointed at %q", got, origin)
	}
	if got := gitOut(t, target, "config", "--local", "--get", "user.email"); got != "new@example.com" {
		t.Errorf("local user.email = %q, want the refreshed new@example.com", got)
	}
	if got := gitOut(t, target, "config", "--local", "--get-all", "kukeon.test"); got != "base\nnew" {
		t.Errorf("multi-valued key = %q, want base then new with no stale value", got)
	}
}

// TestProcessRepos_PlanLeavesUnplannedRepoAlone asserts a repo with no plan
// entry clones from its declared URL and gets no local identity written.
func TestProcessRepos_PlanLeavesUnplannedRepoAlone(t *testing.T) {
	hermeticGit(t)
	origin := makeBareOrigin(t)
	target := filepath.Join(t.TempDir(), "checkout")
	repos := []v1beta1.ContainerRepo{
		{Name: "project", Target: target, Branch: "main", URL: origin, Required: true},
	}
	plan := &gitplan.Plan{Repos: map[string]gitplan.Repo{
		"elsewhere": {Config: []gitplan.ConfigEntry{{Key: "user.email", Value: "x@example.com"}}},
	}}
	if _, err := processRepos(context.Background(), repos, plan, discardLogger()); err != nil {
		t.Fatalf("processRepos: %v", err)
	}
	if got := gitOut(t, target, "remote", "get-url", "origin"); got != origin {
		t.Errorf("origin = %q, want the declared %q", got, origin)
	}
	cmd := exec.Command("git", "config", "--local", "--get", "user.email")
	cmd.Dir = target
	if out, err := cmd.Output(); err == nil {
		t.Errorf("unplanned repo got a local user.email %q", bytes.TrimSpace(out))
	}
}

func TestPlanRepoGit(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /image/key")
	repo := v1beta1.ContainerRepo{Name: "project", URL: "https://example.com/o/r.git"}
	hasImageSSH := func(env []string) bool {
		for _, kv := range env {
			if kv == "GIT_SSH_COMMAND=ssh -i /image/key" {
				return true
			}
		}
		return false
	}

	t.Run("nil plan keeps the declared url and the image ssh command", func(t *testing.T) {
		rg := planRepoGit(repo, nil)
		if rg.url != repo.URL || len(rg.config) != 0 || len(rg.configFlags()) != 0 {
			t.Errorf("rg = %+v, want the declared url and no config", rg)
		}
		if !hasImageSSH(rg.env) {
			t.Errorf("image GIT_SSH_COMMAND dropped with no managed ssh")
		}
	})

	t.Run("cell-global ssh command is managed", func(t *testing.T) {
		rg := planRepoGit(repo, &gitplan.Plan{SSHCommand: true})
		if rg.url != repo.URL {
			t.Errorf("url = %q, want the declared url for a repo with no entry", rg.url)
		}
		if hasImageSSH(rg.env) {
			t.Errorf("inherited GIT_SSH_COMMAND kept; it would outrank core.sshCommand")
		}
	})

	t.Run("repo-local ssh command is managed and rendered as flags", func(t *testing.T) {
		rg := planRepoGit(repo, &gitplan.Plan{Repos: map[string]gitplan.Repo{"project": {
			URL: "git@example.com:o/r.git",
			Config: []gitplan.ConfigEntry{
				{Key: "core.sshCommand", Value: "ssh -F /.kukeon/git/repos/project/ssh_config"},
				{Key: "user.email", Value: "repo@example.com"},
			},
		}}})
		if rg.url != "git@example.com:o/r.git" {
			t.Errorf("url = %q, want the planned ssh url", rg.url)
		}
		if hasImageSSH(rg.env) {
			t.Errorf("inherited GIT_SSH_COMMAND kept despite a repo-local core.sshCommand")
		}
		want := []string{
			"-c", "core.sshCommand=ssh -F /.kukeon/git/repos/project/ssh_config",
			"-c", "user.email=repo@example.com",
		}
		if got := rg.configFlags(); strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("configFlags = %q, want %q", got, want)
		}
	})

	t.Run("entry without url keeps the declared one", func(t *testing.T) {
		rg := planRepoGit(repo, &gitplan.Plan{Repos: map[string]gitplan.Repo{"project": {
			Config: []gitplan.ConfigEntry{{Key: "user.email", Value: "repo@example.com"}},
		}}})
		if rg.url != repo.URL {
			t.Errorf("url = %q, want the declared url", rg.url)
		}
		if !hasImageSSH(rg.env) {
			t.Errorf("image GIT_SSH_COMMAND dropped though the entry manages no ssh")
		}
	})
}

// envValues returns every value env carries for key.
func envValues(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			out = append(out, v)
		}
	}
	return out
}

func TestGitEnv(t *testing.T) {
	unsetenv := func(t *testing.T, key string) {
		t.Helper()
		// t.Setenv registers the restore; Unsetenv then gives the "not set"
		// state gitEnv distinguishes from "set to empty".
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("unmanaged installs the strict fallback", func(t *testing.T) {
		unsetenv(t, "GIT_SSH_COMMAND")
		unsetenv(t, "GIT_TERMINAL_PROMPT")
		env := gitEnv(false)
		if got := envValues(env, "GIT_TERMINAL_PROMPT"); len(got) != 1 || got[0] != "0" {
			t.Errorf("GIT_TERMINAL_PROMPT = %q, want a single 0", got)
		}
		got := envValues(env, "GIT_SSH_COMMAND")
		if len(got) != 1 || got[0] != fallbackSSHCommand() {
			t.Errorf("GIT_SSH_COMMAND = %q, want the fallback %q", got, fallbackSSHCommand())
		}
	})

	t.Run("unmanaged keeps an image-provided ssh command", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", "ssh -i /image/key")
		t.Setenv("GIT_TERMINAL_PROMPT", "1")
		env := gitEnv(false)
		if got := envValues(env, "GIT_SSH_COMMAND"); len(got) != 1 || got[0] != "ssh -i /image/key" {
			t.Errorf("GIT_SSH_COMMAND = %q, want the image's value untouched", got)
		}
		if got := envValues(env, "GIT_TERMINAL_PROMPT"); len(got) != 1 || got[0] != "1" {
			t.Errorf("GIT_TERMINAL_PROMPT = %q, want the operator's value untouched", got)
		}
	})

	t.Run("managed drops any ssh command", func(t *testing.T) {
		t.Setenv("GIT_SSH_COMMAND", "ssh -i /image/key")
		unsetenv(t, "GIT_TERMINAL_PROMPT")
		env := gitEnv(true)
		if got := envValues(env, "GIT_SSH_COMMAND"); len(got) != 0 {
			t.Errorf("GIT_SSH_COMMAND = %q, want none: git prefers it over core.sshCommand", got)
		}
		if got := envValues(env, "GIT_TERMINAL_PROMPT"); len(got) != 1 || got[0] != "0" {
			t.Errorf("GIT_TERMINAL_PROMPT = %q, want a single 0", got)
		}
		// The rest of the environment (HOME, the agent socket, …) is inherited.
		if len(envValues(env, "PATH")) != 1 {
			t.Errorf("PATH not inherited: %v", envValues(env, "PATH"))
		}
	})

	t.Run("managed with no ssh command installs no fallback", func(t *testing.T) {
		unsetenv(t, "GIT_SSH_COMMAND")
		if got := envValues(gitEnv(true), "GIT_SSH_COMMAND"); len(got) != 0 {
			t.Errorf("GIT_SSH_COMMAND = %q, want none", got)
		}
	})
}

// TestFallbackSSHCommand locks the default ssh for a remote no stored
// credential covers: non-interactive, strict host-key checking — never the
// pre-#1472 accept-new — against the image's known_hosts plus the built-in
// forge keys, given as ONE option (ssh keeps only the first
// UserKnownHostsFile it sees).
func TestFallbackSSHCommand(t *testing.T) {
	command := fallbackSSHCommand()
	for _, want := range []string{"-o BatchMode=yes", "-o StrictHostKeyChecking=yes"} {
		if !strings.Contains(command, want) {
			t.Errorf("fallback %q lacks %q", command, want)
		}
	}
	if strings.Contains(command, "accept-new") {
		t.Errorf("fallback %q trusts unseen hosts", command)
	}
	if n := strings.Count(command, "UserKnownHostsFile="); n != 1 {
		t.Fatalf("fallback %q sets UserKnownHostsFile %d times, want 1", command, n)
	}
	if command != fallbackSSHCommand() {
		t.Errorf("fallback is not stable across calls")
	}

	_, rest, _ := strings.Cut(command, "'UserKnownHostsFile=")
	files := strings.Fields(strings.TrimSuffix(rest, "'"))
	if len(files) != 2 || files[0] != "~/.ssh/known_hosts" {
		t.Fatalf("known hosts files = %q, want ~/.ssh/known_hosts then the built-in file", files)
	}
	builtin, err := os.ReadFile(files[1])
	if err != nil {
		t.Fatalf("read built-in known hosts: %v", err)
	}
	if string(builtin) != gitplan.AllBuiltinKnownHosts() {
		t.Errorf("built-in known hosts file differs from gitplan.AllBuiltinKnownHosts()")
	}
	for _, host := range gitplan.BuiltinHosts() {
		if !strings.Contains(string(builtin), host) {
			t.Errorf("built-in known hosts lacks %s", host)
		}
	}

	// ssh itself must accept the option as written (quoting, two files).
	if _, lookErr := exec.LookPath("ssh"); lookErr != nil {
		t.Skip("ssh not on PATH")
	}
	args := strings.Fields(strings.ReplaceAll(command, "'", ""))
	// Re-join the quoted two-file value that Fields split apart.
	var sshArgs []string
	for i := 1; i < len(args); i++ {
		if strings.HasPrefix(args[i], "UserKnownHostsFile=") && i+1 < len(args) {
			sshArgs = append(sshArgs, args[i]+" "+args[i+1])
			i++
			continue
		}
		sshArgs = append(sshArgs, args[i])
	}
	cmd := exec.Command("ssh", append(append([]string{"-G", "-F", os.DevNull}, sshArgs...), "github.com")...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ssh -G rejected the fallback options: %v\n%s", err, out)
	}
	resolved := strings.ToLower(string(out))
	if !strings.Contains(resolved, "stricthostkeychecking true") &&
		!strings.Contains(resolved, "stricthostkeychecking yes") {
		t.Errorf("ssh did not resolve strict host key checking:\n%s", out)
	}
	if !strings.Contains(string(out), files[1]) {
		t.Errorf("ssh did not pick up the built-in known hosts file %s:\n%s", files[1], out)
	}
}

func TestAskpass(t *testing.T) {
	file := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(file, []byte("hunter2\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if code := askpass(file, "Enter passphrase for /k: ", &out); code != 0 {
		t.Fatalf("askpass = %d, want 0", code)
	}
	if out.String() != "hunter2\n" {
		t.Errorf("askpass printed %q, want the passphrase and one newline", out.String())
	}

	// ssh-add re-prompts until it gets an empty answer; answering the retry
	// with the same passphrase would loop forever.
	out.Reset()
	if code := askpass(file, "Bad passphrase, try again for /k: ", &out); code == 0 || out.Len() != 0 {
		t.Errorf("askpass on a retry prompt = %d printing %q, want a failure printing nothing", code, out.String())
	}

	out.Reset()
	if code := askpass(filepath.Join(t.TempDir(), "absent"), "", &out); code == 0 || out.Len() != 0 {
		t.Errorf("askpass on a missing file = %d printing %q, want a failure printing nothing", code, out.String())
	}
}

// agentIdentities lists the fingerprints the agent at socket holds ("" when it
// holds none).
func agentIdentities(t *testing.T, socket string) string {
	t.Helper()
	cmd := exec.Command("ssh-add", "-l")
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
	out, err := cmd.Output()
	if err != nil {
		// ssh-add -l exits 1 when the agent is reachable but empty.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return ""
		}
		t.Fatalf("ssh-add -l: %v", err)
	}
	return string(out)
}

// encryptedKey generates a passphrase-protected ed25519 key and returns its
// path and SHA256 fingerprint.
func encryptedKey(t *testing.T, passphrase string) (string, string) {
	t.Helper()
	for _, tool := range []string{"ssh-keygen", "ssh-agent", "ssh-add"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not on PATH", tool)
		}
	}
	key := filepath.Join(t.TempDir(), "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", passphrase, "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	out, err := exec.Command("ssh-keygen", "-lf", key+".pub").Output()
	if err != nil {
		t.Fatalf("ssh-keygen -lf: %v", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		t.Fatalf("unexpected ssh-keygen -lf output %q", out)
	}
	return key, fields[1]
}

// TestStartGitAgent_LoadsPassphraseKey is step 4 end to end: a per-container
// ssh-agent is started, the encrypted key is unlocked through kuketty's own
// SSH_ASKPASS mode reading the passphrase file (never an env var or argv), and
// SSH_AUTH_SOCK is exported for git's ssh and the workload. Stopping tears the
// agent and its socket down.
func TestStartGitAgent_LoadsPassphraseKey(t *testing.T) {
	const passphrase = "correct horse battery"
	key, fingerprint := encryptedKey(t, passphrase)
	passFile := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(passFile, []byte(passphrase+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", "")

	stop := startGitAgent(context.Background(), &gitplan.Plan{
		AgentKeys: []gitplan.AgentKey{{Key: key, PassphraseFile: passFile}},
	}, discardLogger())
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			stop()
		}
	})

	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		t.Fatal("SSH_AUTH_SOCK not exported")
	}
	if got := agentIdentities(t, socket); !strings.Contains(got, fingerprint) {
		t.Fatalf("agent identities = %q, want the key %s loaded", got, fingerprint)
	}

	stop()
	stopped = true
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Errorf("agent socket still present after stop (err=%v)", err)
	}
}

// TestStartGitAgent_WrongPassphraseDoesNotHang asserts a stored passphrase
// that does not open the key ends in a logged, non-fatal failure: the agent is
// up but empty, and startup returns instead of looping on ssh-add's re-prompt.
func TestStartGitAgent_WrongPassphraseDoesNotHang(t *testing.T) {
	key, _ := encryptedKey(t, "the real passphrase")
	passFile := filepath.Join(t.TempDir(), "passphrase")
	if err := os.WriteFile(passFile, []byte("not the passphrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SSH_AUTH_SOCK", "")

	done := make(chan func(), 1)
	go func() {
		done <- startGitAgent(context.Background(), &gitplan.Plan{
			AgentKeys: []gitplan.AgentKey{{Key: key, PassphraseFile: passFile}},
		}, discardLogger())
	}()
	var stop func()
	select {
	case stop = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("startGitAgent did not return: ssh-add is looping on the passphrase prompt")
	}
	defer stop()

	socket := os.Getenv("SSH_AUTH_SOCK")
	if socket == "" {
		t.Fatal("SSH_AUTH_SOCK not exported")
	}
	if got := agentIdentities(t, socket); got != "" {
		t.Errorf("agent identities = %q, want none after a wrong passphrase", got)
	}
}

// TestStartGitAgent_NoKeysStartsNothing asserts a plan without
// passphrase-protected keys leaves the environment alone.
func TestStartGitAgent_NoKeysStartsNothing(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/preexisting/agent.sock")
	for _, plan := range []*gitplan.Plan{nil, {}, {SSHCommand: true, Repos: map[string]gitplan.Repo{"r": {}}}} {
		stop := startGitAgent(context.Background(), plan, discardLogger())
		stop()
		if got := os.Getenv("SSH_AUTH_SOCK"); got != "/preexisting/agent.sock" {
			t.Errorf("SSH_AUTH_SOCK = %q, want it untouched", got)
		}
	}
}
