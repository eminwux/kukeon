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

package gitremote

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/gitcred/gitagent"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
)

// TestMain lets the test binary stand in for kuke's SSH_ASKPASS mode:
// WithStagedIdentity points ssh-add at os.Executable(), which under `go test`
// is this binary.
func TestMain(m *testing.M) {
	if file := os.Getenv(gitagent.HostAskpassFileEnv); file != "" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		os.Exit(gitagent.Askpass(file, prompt, os.Stdout))
	}
	os.Exit(m.Run())
}

// recordingGit puts a git on PATH that records its arguments and environment
// under dir and answers ls-remote with a main HEAD, or fails when fail is set.
func recordingGit(t *testing.T, fail bool) string {
	t.Helper()
	dir := t.TempDir()
	exit := "0"
	if fail {
		exit = "128"
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > '" + filepath.Join(dir, "args") + "'\n" +
		"env > '" + filepath.Join(dir, "env") + "'\n" +
		"if [ -n \"$SSH_AUTH_SOCK\" ] && [ -S \"$SSH_AUTH_SOCK\" ]; then ssh-add -l > '" +
		filepath.Join(dir, "agent") + "' 2>&1; fi\n" +
		"echo 'fatal: could not read from " + dir + "' >&2\n" +
		"[ " + exit + " = 0 ] || exit " + exit + "\n" +
		"printf 'ref: refs/heads/main\\tHEAD\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	return dir
}

func recorded(t *testing.T, dir, name string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func envValue(env []string, key string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == key {
			return v, true
		}
	}
	return "", false
}

// stageBundle writes the parts of a staged bundle kuke reads: the plan and a
// gitconfig.
func stageBundle(t *testing.T, plan gitplan.Plan) string {
	t.Helper()
	dir := t.TempDir()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gitplan.PlanPathAt(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(gitplan.ConfigPathAt(dir), []byte("[core]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// callerTransport is a caller environment a staged fetch must not use.
func callerTransport(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "/caller/agent.sock")
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /caller/id")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", "/caller/gitconfig")
}

// TestStagedSSHIdentity: an SSH key stored for an https address is fetched
// over scp-like ssh with the staged ssh_config, and nothing of the caller's
// transport reaches git.
func TestStagedSSHIdentity(t *testing.T) {
	git := recordingGit(t, false)
	callerTransport(t)
	dir := stageBundle(t, gitplan.Plan{
		SSHCommand: true,
		Repos:      map[string]gitplan.Repo{gitplan.HostFetchRepo: {URL: "git@github.com:acme/app"}},
	})
	ctx, release, err := WithStagedIdentity(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := Remote(ctx, "https://github.com/acme/app"); got != "git@github.com:acme/app" {
		t.Fatalf("Remote = %q, want the scp-like URL the plan rewrote", got)
	}
	branch, err := DefaultBranch(ctx, "https://github.com/acme/app")
	if err != nil || branch != "main" {
		t.Fatalf("DefaultBranch = %q, %v", branch, err)
	}
	if args := recorded(t, git, "args"); !slices.Contains(args, "git@github.com:acme/app") {
		t.Errorf("git args = %q, want the scp-like URL", args)
	}
	env := recorded(t, git, "env")
	want := map[string]string{
		"GIT_SSH_COMMAND":   "ssh -F " + filepath.Join(dir, gitplan.SSHConfigFile) + " -oBatchMode=yes",
		"GIT_CONFIG_SYSTEM": gitplan.ConfigPathAt(dir),
		"GIT_CONFIG_GLOBAL": os.DevNull,
	}
	for key, value := range want {
		if got, _ := envValue(env, key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	for _, key := range []string{"SSH_AUTH_SOCK", "GIT_CONFIG_NOSYSTEM"} {
		if value, ok := envValue(env, key); ok {
			t.Errorf("%s = %q reached git under a staged identity", key, value)
		}
	}
}

// TestStagedBasicAuth: basic auth stored for a git@host:path address is
// fetched over https with the staged credential helper as command config.
func TestStagedBasicAuth(t *testing.T) {
	git := recordingGit(t, false)
	callerTransport(t)
	helper := "!f() { cat /staged/basic; }; f"
	dir := stageBundle(t, gitplan.Plan{Repos: map[string]gitplan.Repo{gitplan.HostFetchRepo: {
		URL:    "https://git.example.org/team/app",
		Config: []gitplan.ConfigEntry{{Key: "credential.helper", Value: helper}},
	}}})
	ctx, release, err := WithStagedIdentity(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err = DefaultBranch(ctx, "git@git.example.org:team/app"); err != nil {
		t.Fatal(err)
	}
	if args := recorded(t, git, "args"); !slices.Contains(args, "https://git.example.org/team/app") {
		t.Errorf("git args = %q, want the https URL", args)
	}
	env := recorded(t, git, "env")
	for key, value := range map[string]string{
		"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "credential.helper", "GIT_CONFIG_VALUE_0": helper,
		"GIT_SSH_COMMAND": "ssh -oBatchMode=yes",
	} {
		if got, _ := envValue(env, key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
}

// TestUnstagedKeepsCallerTransport: without a staged identity the fetch uses
// the caller's own transport, as before.
func TestUnstagedKeepsCallerTransport(t *testing.T) {
	git := recordingGit(t, false)
	t.Setenv("SSH_AUTH_SOCK", "/caller/agent.sock")
	t.Setenv("GIT_SSH_COMMAND", "ssh -i /caller/id")
	ctx := t.Context()
	if got := Remote(ctx, "github.com/acme/app"); got != "https://github.com/acme/app" {
		t.Fatalf("Remote = %q", got)
	}
	if _, err := DefaultBranch(ctx, "git@github.com:acme/app"); err != nil {
		t.Fatal(err)
	}
	env := recorded(t, git, "env")
	if got, _ := envValue(env, "SSH_AUTH_SOCK"); got != "/caller/agent.sock" {
		t.Errorf("SSH_AUTH_SOCK = %q, want the caller's", got)
	}
	if got, _ := envValue(env, "GIT_SSH_COMMAND"); got != "ssh -i /caller/id -oBatchMode=yes" {
		t.Errorf("GIT_SSH_COMMAND = %q, want the caller's", got)
	}
}

// TestStagedPassphraseKey: a passphrase key is unlocked in a private agent
// git sees, and release stops the agent and removes its socket.
func TestStagedPassphraseKey(t *testing.T) {
	for _, tool := range []string{"ssh-agent", "ssh-add", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	git := recordingGit(t, false)
	callerTransport(t)
	keys := t.TempDir()
	key := filepath.Join(keys, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "pass phrase", "-f", key).
		CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	passphrase := filepath.Join(keys, "passphrase")
	if err := os.WriteFile(passphrase, []byte("pass phrase\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := stageBundle(t, gitplan.Plan{
		SSHCommand: true,
		AgentKeys:  []gitplan.AgentKey{{Key: key, PassphraseFile: passphrase}},
		Repos:      map[string]gitplan.Repo{gitplan.HostFetchRepo: {URL: "git@github.com:acme/app"}},
	})
	ctx, release, err := WithStagedIdentity(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DefaultBranch(ctx, "git@github.com:acme/app"); err != nil {
		release()
		t.Fatal(err)
	}
	socket, _ := envValue(recorded(t, git, "env"), "SSH_AUTH_SOCK")
	if socket == "" || socket == "/caller/agent.sock" {
		release()
		t.Fatalf("SSH_AUTH_SOCK = %q, want the private agent", socket)
	}
	if listed := strings.Join(recorded(t, git, "agent"), "\n"); !strings.Contains(listed, "ED25519") {
		release()
		t.Fatalf("the private agent does not hold the key: %s", listed)
	}
	release()
	if _, err = os.Stat(filepath.Dir(socket)); !os.IsNotExist(err) {
		t.Errorf("agent directory %s survived release: %v", filepath.Dir(socket), err)
	}
}

// TestStagedWrongPassphrase: a passphrase the key refuses fails the staging
// without hanging and without the passphrase in the error.
func TestStagedWrongPassphrase(t *testing.T) {
	for _, tool := range []string{"ssh-agent", "ssh-add", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	keys := t.TempDir()
	key := filepath.Join(keys, "id")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "right", "-f", key).
		CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	passphrase := filepath.Join(keys, "passphrase")
	if err := os.WriteFile(passphrase, []byte("canary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := stageBundle(t, gitplan.Plan{AgentKeys: []gitplan.AgentKey{{Key: key, PassphraseFile: passphrase}}})
	_, _, err := WithStagedIdentity(t.Context(), dir)
	if err == nil || strings.Contains(err.Error(), "canary") || strings.Contains(err.Error(), dir) {
		t.Fatalf("WithStagedIdentity = %v; want a refusal naming neither passphrase nor path", err)
	}
}

// TestStagedFetchFailure: a failed fetch reports the guidance error, with no
// git output and no path of the staging in it.
func TestStagedFetchFailure(t *testing.T) {
	recordingGit(t, true)
	dir := stageBundle(t, gitplan.Plan{})
	ctx, release, err := WithStagedIdentity(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	_, err = DefaultBranch(ctx, "github.com/acme/app")
	if err == nil || err.Error() != guidance || strings.Contains(err.Error(), "gitfetch") ||
		strings.Contains(err.Error(), dir) {
		t.Fatalf("DefaultBranch = %v; want the guidance error alone", err)
	}
}

// TestStagedUnusable: a directory with no plan is refused without its path.
func TestStagedUnusable(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := WithStagedIdentity(t.Context(), dir); err == nil || strings.Contains(err.Error(), dir) {
		t.Fatalf("WithStagedIdentity = %v; want a refusal without the path", err)
	}
}
