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

package initialize

import (
	"bytes"
	"encoding/pem"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/team/teamtest"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/config"
)

func fixtureGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("fixture Git failed: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

// addDefaults is the TeamDefaults the add tests start from: the kuke team's
// sources and role on the codex harness.
func addDefaults() *config.TeamDefaults {
	cfg := config.EmptyDefaults(config.DefaultRealm)
	cfg.Spec.Defaults = config.Majordomo().Spec.Defaults
	cfg.Spec.Defaults.Harnesses = cfg.Spec.Defaults.Harnesses[:1]
	// The sources as a TeamDefaults listed them before #1643; storeDefaults
	// stores them as Sources.
	kukeon := config.KukeonSource()
	kukeon.ClonePath = cfg.Spec.Defaults.Sources[0].ClonePath
	cfg.Spec.Defaults.Sources = []config.Source{kukeon}
	return cfg
}

// Use actual Git object transfer through a controlled OpenSSH-compatible shim.
// No provider, real identity, network host, or recorded credential is involved.
func setupAddRun(t *testing.T) Options {
	t.Helper()
	f := newFake()
	storeDefaults(t, f, addDefaults())
	return Options{Client: f, Sources: sourceFS()}
}

func setupAdd(t *testing.T) (AddOptions, string) {
	t.Helper()
	return setupAddObjectFormat(t, "sha1")
}

func setupAddObjectFormat(t *testing.T, objectFormat string) (AddOptions, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_SSH", "")
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	repo := t.TempDir()
	fixtureGit(t, repo, "init", "--initial-branch=trunk", "--object-format="+objectFormat)
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("project fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureGit(t, repo, "add", "README")
	fixtureGit(
		t,
		repo,
		"-c",
		"commit.gpgsign=false",
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.test",
		"commit",
		"-m",
		"fixture",
	)
	fixtureGit(t, repo, "-c", "tag.gpgsign=false", "tag", "v1")
	commit := fixtureGit(t, repo, "rev-parse", "HEAD")
	shim := filepath.Join(t.TempDir(), "ssh")
	writeFixture(t, shim, `#!/bin/sh
case " $* " in *" -oBatchMode=yes "*) ;; *) exit 1;; esac
if [ -n "$ADD_EXPECT_PORT" ]; then
  case " $* " in *" -p $ADD_EXPECT_PORT "*) ;; *) exit 1;; esac
fi
[ "$GIT_TERMINAL_PROMPT" = 0 ] || exit 1
[ "$GCM_INTERACTIVE" = never ] || exit 1
[ "$SSH_AUTH_SOCK" = /fixture/agent ] || exit 1
# Git's final argument is the upload-pack command; map its remote to the fixture.
exec git-upload-pack "$ADD_FIXTURE_REPO"
`)
	t.Setenv("ADD_FIXTURE_REPO", repo)
	t.Setenv("SSH_AUTH_SOCK", "/fixture/agent")
	t.Setenv("GIT_SSH_COMMAND", "'"+strings.ReplaceAll(shim, "'", "'\\''")+"'")
	opts := AddOptions{Options: setupAddRun(t), Name: "acme", Source: "git@work-alias:owner/project.git"}
	return opts, commit
}

func writeFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o700); err != nil {
		t.Fatal(err)
	}
}

// readAdded reads the stored Team of opts.Name from the fake daemon.
func readAdded(t *testing.T, opts AddOptions) ([]byte, *config.Team) {
	t.Helper()
	f := opts.Client.(*fakeClient)
	return f.teams[opts.Name], storedTeam(t, f, opts.Name)
}

// TestAddNativeSSHRevisionsAndRepeat fetches the project over a controlled
// SSH shim at each kind of revision. A repeated add of a stored team is
// refused (AC8a); with --force it replaces the declaration and converges.
func TestAddNativeSSHRevisionsAndRepeat(t *testing.T) {
	for _, mode := range []string{"default", "branch", "tag", "commit", "ssh-url", "ssh-alias", "example", "core-ssh-command"} {
		t.Run(mode, func(t *testing.T) {
			opts, commit := setupAdd(t)
			switch mode {
			case "branch":
				opts.Branch = "trunk"
			case "tag":
				opts.Tag = "v1"
			case "commit":
				opts.Commit = commit
			case "example":
				opts.Source = "git@github.com:eminwux/kukeon.git"
			case "ssh-alias":
				opts.Source = "work-alias:owner/project.git"
			case "ssh-url":
				opts.Source = "ssh://git@work-alias:2222/owner/project.git"
				t.Setenv("ADD_EXPECT_PORT", "2222")
			case "core-ssh-command":
				path := filepath.Join(t.TempDir(), "gitconfig")
				t.Setenv("GIT_CONFIG_GLOBAL", path)
				fixtureGit(t, ".", "config", "--global", "core.sshCommand", os.Getenv("GIT_SSH_COMMAND"))
				t.Setenv("GIT_SSH_COMMAND", "")
			}
			fake := opts.Client.(*fakeClient)
			defaults := slices.Clone(fake.defaults)
			for attempt := range 2 {
				opts.Force = attempt == 1
				out, err := Add(t.Context(), opts)
				if err != nil {
					t.Fatal(err)
				}
				// One blueprint, one config and the Team, and the first time
				// the project's Source.
				if want := 3 + 1 - attempt; out.Result.Resources != want {
					t.Fatalf("resources = %d", out.Result.Resources)
				}
				if out.Plan.Origin.Kind != OriginDefaults {
					t.Fatalf("origin = %+v, want defaults (the fixture has no kuketeam.yaml)", out.Plan.Origin)
				}
				assertAcceptedAdd(t, opts, mode, commit)
				if !bytes.Equal(defaults, fake.defaults) {
					t.Fatal("add changed the TeamDefaults")
				}
			}
			if len(fake.teams) != 1 || fake.applies != 2 {
				t.Fatalf("teams=%d applies=%d", len(fake.teams), fake.applies)
			}
		})
	}
}

func TestAddNativeSSHRevisionObjectFormats(t *testing.T) {
	for _, format := range []string{"sha1", sha256ObjectFormat} {
		for _, mode := range []string{"default", "branch", "tag", "annotated-tag", "commit"} {
			t.Run(format+"/"+mode, func(t *testing.T) {
				opts, commit := setupAddObjectFormat(t, format)
				source := config.Source{Name: "project", URL: opts.Source}
				switch mode {
				case "branch":
					opts.Branch, source.Branch = "trunk", "trunk"
				case "tag":
					opts.Tag, source.Tag = "v1", "v1"
				case "annotated-tag":
					fixtureGit(t, os.Getenv("ADD_FIXTURE_REPO"), "-c", "user.name=Fixture",
						"-c", "user.email=fixture@example.test", "tag", "-f", "-a", "v1",
						"-m", "fixture", "--no-sign")
					opts.Tag, source.Tag = "v1", "v1"
				case "commit":
					opts.Commit, source.Commit = commit, commit
				}
				// The caller's default must not override the remote's format.
				otherFormat := sha256ObjectFormat
				if format == sha256ObjectFormat {
					otherFormat = "sha1"
				}
				t.Setenv("GIT_DEFAULT_HASH", otherFormat)
				if mode != "default" {
					sources, cleanup, err := materializeSources(t.Context(), config.Defaults{
						Sources: []config.Source{source},
					}, true)
					if err != nil {
						t.Fatal(err)
					}
					raw, err := fs.ReadFile(sources["project"], "README")
					cleanup()
					if err != nil || string(raw) != "project fixture" {
						t.Fatalf("revision checkout content = %q, error = %v", raw, err)
					}
				}
				out, err := Add(t.Context(), opts)
				if err != nil {
					t.Fatal(err)
				}
				// The plan records the commit the project was fetched at.
				if len(out.Plan.Sources) == 0 || !slices.ContainsFunc(out.Plan.Sources, func(s PlannedSource) bool {
					return s.Name == "project" && s.Commit == commit
				}) {
					t.Fatalf("planned sources = %+v, want project at %s", out.Plan.Sources, commit)
				}
				acceptedMode := mode
				if mode == "annotated-tag" {
					acceptedMode = "tag"
				}
				assertAcceptedAdd(t, opts, acceptedMode, commit)
			})
		}
	}
}

// changeAddConfig edits the TeamDefaults stored in the add's fake daemon.
func changeAddConfig(t *testing.T, opts AddOptions, change func(*config.TeamDefaults)) {
	t.Helper()
	f := opts.Client.(*fakeClient)
	cfg := storedDefaults(t, f)
	change(cfg)
	storeDefaults(t, f, cfg)
}

// TestAddFailuresPreserveAcceptedTeam: after a successful add, a forced add
// that fails anywhere before or during the apply leaves the stored Team as it
// was, and nothing is applied before the preflight passes.
func TestAddFailuresPreserveAcceptedTeam(t *testing.T) {
	for _, mode := range []string{"missing-branch", "missing-tag", "missing-commit", "rpc", "partial", "scope", "secret-lookup", "image", "definition", "collision", "clone-collision", "credentials"} {
		t.Run(mode, func(t *testing.T) {
			opts, _ := setupAdd(t)
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			fake := opts.Client.(*fakeClient)
			before, _ := readAdded(t, opts)
			accepted := opts
			applies, creates := fake.applies, fake.creates
			opts.Force = true
			configureAddFailure(t, &opts, fake, mode)
			out, err := Add(t.Context(), opts)
			if err == nil || out.Result != nil || strings.Contains(err.Error(), "unsafe-canary") {
				t.Fatalf("failure not safe: %v", err)
			}
			if mode != "rpc" && mode != "partial" && fake.applies != applies {
				t.Fatal("applied before preflight")
			}
			if fake.creates != creates {
				t.Fatal("a secret was created")
			}
			if after, _ := readAdded(t, accepted); !bytes.Equal(before, after) {
				t.Fatal("failed update replaced the stored team")
			}
		})
	}
}

// HTTPS is served by Git's actual smart HTTP backend. A local credential helper
// supplies fixture-only authentication when challenged; no embedded URL secret.
func TestAddHTTPSCredentialHelperAndPublic(t *testing.T) {
	for _, mode := range []string{"public", "private-global", "private-local", "private-conditional", "private-relative-ca", "private-scoped-relative-ca"} {
		t.Run(mode, func(t *testing.T) {
			private := mode != "public"
			opts, _ := setupAdd(t)
			repo := os.Getenv("ADD_FIXTURE_REPO")
			fixtureGit(t, repo, "config", "http.receivepack", "false")
			execPath := fixtureGit(t, repo, "--exec-path")
			backend := &cgi.Handler{
				Path: filepath.Join(execPath, "git-http-backend"),
				Env:  []string{"GIT_PROJECT_ROOT=" + filepath.Dir(repo), "GIT_HTTP_EXPORT_ALL=1"},
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if private {
					user, password, ok := r.BasicAuth()
					if !ok || user != "fixture" || password != "fixture-only" {
						w.Header().Set("WWW-Authenticate", `Basic realm="fixture"`)
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
				}
				backend.ServeHTTP(w, r)
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GIT_SSL_CAINFO", ca)
			global := filepath.Join(t.TempDir(), "gitconfig")
			t.Setenv("GIT_CONFIG_GLOBAL", global)
			helper := filepath.Join(t.TempDir(), "helper")
			writeFixture(
				t,
				helper,
				"#!/bin/sh\n[ \"$1\" = get ] || exit 0\ncat >/dev/null\nprintf 'username=fixture\\npassword=fixture-only\\n'\n",
			)
			if private {
				scope := "--global"
				if mode == "private-local" {
					scope = "--local"
				}
				if mode == "private-conditional" {
					include := filepath.Join(t.TempDir(), "auth.config")
					fixtureGit(t, repo, "config", "--file", include, "credential.helper", helper)
					fixtureGit(t, repo, "config", "--global", "includeIf.gitdir:"+repo+"/.git.path", include)
				} else {
					fixtureGit(t, repo, "config", scope, "credential.helper", helper)
				}
			} else {
				fixtureGit(t, repo, "config", "--global", "credential.helper", "")
			}
			opts.Source = server.URL + "/" + filepath.Base(repo)
			relativeCA := mode == "private-relative-ca" || mode == "private-scoped-relative-ca"
			if relativeCA {
				// A native fetch resolves the CA relative to the input checkout.
				t.Setenv("GIT_SSL_CAINFO", "")
				if err := os.Unsetenv("GIT_SSL_CAINFO"); err != nil {
					t.Fatal(err)
				}
				cert, err := os.ReadFile(ca)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(repo, "ca.pem"), cert, 0o600); err != nil {
					t.Fatal(err)
				}
				key := "http.sslCAInfo"
				if mode == "private-scoped-relative-ca" {
					key = "http." + server.URL + ".sslCAInfo"
				}
				fixtureGit(t, repo, "config", "--local", key, "ca.pem")
			}
			if mode == "private-local" || mode == "private-conditional" || relativeCA {
				fixtureGit(t, repo, "remote", "add", "origin", opts.Source)
				// Native Git succeeds in the very same shell/configuration context.
				fixtureGit(t, repo, "ls-remote", "--exit-code", "origin", "refs/heads/trunk")
				opts.Directory, opts.Name, opts.Source = repo, "private-project", ""
			}
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			raw, _ := readAdded(t, opts)
			if bytes.Contains(raw, []byte("fixture-only")) ||
				bytes.Contains(opts.Client.(*fakeClient).raw, []byte("fixture-only")) {
				t.Fatal("helper credential persisted")
			}
			if private {
				before := append([]byte(nil), raw...)
				switch mode {
				case "private-local":
					fixtureGit(t, repo, "config", "--local", "credential.helper", "")
				case "private-conditional":
					fixtureGit(t, repo, "config", "--global", "--unset-all", "includeIf.gitdir:"+repo+"/.git.path")
				default:
					fixtureGit(t, repo, "config", "--global", "credential.helper", "")
				}
				fake := opts.Client.(*fakeClient)
				applies := fake.applies
				opts.Force = true
				if _, err := Add(t.Context(), opts); err == nil || strings.Contains(err.Error(), "fixture-only") {
					t.Fatal("missing HTTPS credentials did not fail safely")
				}
				after, _ := readAdded(t, opts)
				if !bytes.Equal(before, after) || fake.applies != applies {
					t.Fatal("authentication failure mutated accepted state")
				}
			}
		})
	}
}

func TestAddTransportFailuresAreNoninteractiveAndSafe(t *testing.T) {
	for _, diagnostic := range []string{"Permission denied (publickey)", "Host key verification failed", "REMOTE HOST IDENTIFICATION HAS CHANGED", "Connection timed out", "Enter passphrase for key"} {
		t.Run(diagnostic, func(t *testing.T) {
			opts, _ := setupAdd(t)
			if _, err := Add(t.Context(), opts); err != nil {
				t.Fatal(err)
			}
			before, _ := readAdded(t, opts)
			fake := opts.Client.(*fakeClient)
			writes := len(fake.writes)
			opts.Force = true
			shim := filepath.Join(t.TempDir(), "failed-ssh")
			writeFixture(
				t,
				shim,
				"#!/bin/sh\ncase \" $* \" in *\" -oBatchMode=yes \"*) ;; *) exit 9;; esac\nprintf '%s\\n' \"$FIXTURE_SSH_FAILURE unsafe-canary\" >&2\nexit 255\n",
			)
			t.Setenv("FIXTURE_SSH_FAILURE", diagnostic)
			t.Setenv("GIT_SSH_COMMAND", "'"+strings.ReplaceAll(shim, "'", "'\\''")+"'")
			for _, branch := range []string{"", "trunk"} {
				opts.Branch = branch
				_, err := Add(t.Context(), opts)
				if err == nil || strings.Contains(err.Error(), "unsafe-canary") ||
					!strings.Contains(err.Error(), "known_hosts") ||
					!strings.Contains(err.Error(), "credential helper") {
					t.Fatalf("unsafe/unactionable Git failure: %v", err)
				}
			}
			after, _ := readAdded(t, opts)
			if !bytes.Equal(before, after) || len(fake.writes) != writes {
				t.Fatal("Git failure mutated the stored team or the daemon")
			}
		})
	}
}

func TestAddReusesScopedSecretsAndNewDefaults(t *testing.T) {
	opts, _ := setupAdd(t)
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Roles[0].Name = "updated"
		cfg.Spec.Defaults.Harnesses = []config.Harness{
			{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"},
		}
		cfg.Spec.Secrets = []config.SecretBinding{binding(&config.SecretSource{File: "/nonexistent/host-token"})}
	})
	fake := opts.Client.(*fakeClient)
	fake.secrets[metadata(binding(nil).SecretRef)] = "unsafe-canary"
	lookups := fake.lookups
	opts.Force = true
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	raw, team := readAdded(t, opts)
	if team.Spec.Roles != nil || team.Spec.Harnesses != nil || fake.creates != 0 || fake.lookups != lookups+1 ||
		!bytes.Contains(fake.raw, []byte("acme-updated-opencode")) ||
		bytes.Contains(raw, []byte("unsafe-canary")) ||
		bytes.Contains(fake.raw, []byte("unsafe-canary")) {
		t.Fatal("new defaults or secret reuse failed")
	}
}

// TestAddRecordsSecretBindingsAndWarnsOnMissing: the stored Team records the
// secret bindings it was composed with; an operator edit to a recorded
// binding outlives the TeamDefaults' bindings, also through add --force.
func TestAddRecordsSecretBindingsAndWarnsOnMissing(t *testing.T) {
	opts, _ := setupAdd(t)
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Harnesses = []config.Harness{
			{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"},
		}
		cfg.Spec.Secrets = nil
	})
	fake := opts.Client.(*fakeClient)
	out, err := Add(t.Context(), opts)
	want := "openrouter-api-key  →  kuke create secret openrouter-api-key --realm default --from-file /path/to/token"
	if err != nil || fake.applies != 1 || fake.creates != 0 ||
		!slices.Equal(out.Result.MissingSecrets, []string{want}) {
		t.Fatalf("missing=%q err=%v applies=%d", out.Result.MissingSecrets, err, fake.applies)
	}
	_, team := readAdded(t, opts)
	if len(team.Spec.Secrets) != 1 || team.Spec.Secrets[0].EnvName != "OPENROUTER_API_KEY" ||
		team.Spec.Secrets[0].SecretRef != (config.SecretRef{Name: "openrouter-api-key", Realm: "default"}) {
		t.Fatalf("team secrets = %+v", team.Spec.Secrets)
	}
	// The edit repoints the runtime secret; the destination is the harness's.
	editTeam(t, fake, opts.Name, func(team *config.Team) { team.Spec.Secrets[0].SecretRef.Name = "edited-router-key" })
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) { cfg.Spec.Secrets = []config.SecretBinding{binding(nil)} })
	fake.secrets[api.SecretMetadata{Name: "edited-router-key", Realm: "default"}] = "present"
	opts.Force = true
	if out, err = Add(t.Context(), opts); err != nil || len(out.Result.MissingSecrets) != 0 {
		t.Fatalf("missing=%q err=%v", out.Result.MissingSecrets, err)
	}
	if _, team = readAdded(t, opts); team.Spec.Secrets[0].SecretRef.Name != "edited-router-key" ||
		!bytes.Contains(fake.raw, []byte("name: edited-router-key")) ||
		bytes.Contains(fake.raw, []byte("provider-account")) {
		t.Fatal("recorded binding replaced")
	}
	// A recorded destination the shared blueprint cannot honor is refused
	// before anything is applied, and the stored team is kept.
	applies := fake.applies
	editTeam(t, fake, opts.Name, func(team *config.Team) { team.Spec.Secrets[0].EnvName = "ROUTER_KEY" })
	edited := slices.Clone(fake.teams[opts.Name])
	if _, err = Add(t.Context(), opts); err == nil || fake.applies != applies ||
		!strings.Contains(err.Error(), "differs from the harness destination") {
		t.Fatalf("moved destination: err=%v applies=%d", err, fake.applies)
	}
	if !bytes.Equal(edited, fake.teams[opts.Name]) {
		t.Fatal("a refused add rewrote the stored team")
	}
	editTeam(t, fake, opts.Name, func(team *config.Team) { team.Spec.Secrets[0].EnvName = "OPENROUTER_API_KEY" })
	// Deselecting the requirement drops its binding.
	changeAddConfig(t, opts, func(cfg *config.TeamDefaults) {
		cfg.Spec.Defaults.Harnesses = []config.Harness{{Name: "codex", Source: "kukeon", Path: "team/harnesses/codex"}}
	})
	if _, err = Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
	if _, team = readAdded(t, opts); team.Spec.Secrets != nil {
		t.Fatalf("stale bindings kept: %+v", team.Spec.Secrets)
	}
}

func TestAddFetchesDefinitionsAndProjectWithoutLocalSources(t *testing.T) {
	opts, _ := setupAdd(t)
	repo := os.Getenv("ADD_FIXTURE_REPO")
	for _, dir := range []string{"team/roles", "team/harnesses"} {
		sourceRoot := filepath.Join("../../..", dir)
		err := fs.WalkDir(os.DirFS(sourceRoot), ".", func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			target := filepath.Join(repo, dir, path)
			if entry.IsDir() {
				return os.MkdirAll(target, 0o700)
			}
			raw, readErr := os.ReadFile(filepath.Join(sourceRoot, path))
			if readErr != nil {
				return readErr
			}
			if filepath.ToSlash(filepath.Join(dir, path)) == teamtest.MajordomoRole {
				if raw, readErr = teamtest.PlainRole(raw); readErr != nil {
					return readErr
				}
			}
			return os.WriteFile(target, raw, 0o600)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	fixtureGit(t, repo, "add", "team")
	fixtureGit(
		t,
		repo,
		"-c",
		"commit.gpgsign=false",
		"-c",
		"user.name=Fixture",
		"-c",
		"user.email=fixture@example.test",
		"commit",
		"-m",
		"definitions",
	)
	// The kukeon definitions are the stored kukeon Source (#1643).
	opts.Client.(*fakeClient).storeSource(config.KukeonSourceName, config.SourceSpec{
		URL: "git@work-alias:owner/definitions.git", Branch: "trunk",
	})
	opts.Sources = nil
	out, err := Add(t.Context(), opts)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := readAdded(t, opts)
	if bytes.Contains(raw, []byte("definitions.git")) {
		t.Fatal("effective defaults frozen into the stored team")
	}
	if len(out.Plan.Sources) != 2 {
		t.Fatalf("planned sources = %+v, want kukeon and project", out.Plan.Sources)
	}
}

func TestAddRejectsCredentialURLBeforeAnyGitInvocation(t *testing.T) {
	opts, _ := setupAdd(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "invoked")
	writeFixture(t, filepath.Join(dir, "git"), "#!/bin/sh\n: > '"+marker+"'\nexit 1\n")
	t.Setenv("PATH", dir)
	opts.Source = "https://user:unsafe-canary@example.test/repo"
	if _, err := Add(t.Context(), opts); err == nil || strings.Contains(err.Error(), "unsafe-canary") {
		t.Fatal("credential URL not safely rejected")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Git invoked before credential URL rejection")
	}
	if f := opts.Client.(*fakeClient); len(f.teams) != 0 || len(f.writes) != 0 {
		t.Fatal("rejected URL reached the daemon")
	}
}

func assertAcceptedAdd(t *testing.T, opts AddOptions, mode, commit string) {
	t.Helper()
	raw, team := readAdded(t, opts)
	// The project is stored as a Source of the team's name, and the Team
	// refers to it (#1643).
	if team.Spec.ProjectSource == nil || team.Spec.ProjectSource.URL != "" ||
		team.Spec.ProjectSource.Stored != opts.Name || team.Spec.Sources != nil ||
		team.Spec.Roles != nil ||
		team.Spec.Harnesses != nil {
		t.Fatalf("inheritance lost: %s", raw)
	}
	if source := storedSource(t, opts.Client.(*fakeClient), opts.Name); source.URL != opts.Source {
		t.Fatalf("project Source = %+v, want %s", source, opts.Source)
	}
	project := team.Spec.ProjectSource
	switch mode {
	case "tag":
		if project.Tag != "v1" || project.Branch != "" ||
			!bytes.Contains(opts.Client.(*fakeClient).raw, []byte("refs/tags/v1")) {
			t.Fatal("wrong tag")
		}
	case "commit":
		if project.Commit != commit || project.Branch != "" {
			t.Fatal("wrong commit")
		}
	default:
		if project.Branch != "trunk" {
			t.Fatal("assumed main instead of remote HEAD")
		}
	}
	fake := opts.Client.(*fakeClient)
	if fake.team != "acme" || !bytes.Contains(fake.raw, []byte("acme-majordomo-codex")) ||
		!bytes.Contains(fake.raw, []byte(opts.Source)) ||
		!bytes.Contains(fake.raw, []byte("/home/user/acme")) ||
		!bytes.Contains(fake.raw, []byte("https://github.com/eminwux/kukeon")) {
		t.Fatal("named ownership/project/default binding missing")
	}
}

func configureAddFailure(t *testing.T, opts *AddOptions, fake *fakeClient, mode string) {
	t.Helper()
	switch mode {
	case "missing-branch":
		opts.Branch = "absent"
	case "missing-tag":
		opts.Tag = "absent"
	case "missing-commit":
		opts.Commit = strings.Repeat("a", 40)
	case "rpc":
		fake.applyErr = io.EOF
	case "partial":
		fake.resources = []kukeonv1.ApplyResourceResult{
			{Action: "created"},
			{Action: "failed", Error: "fixture failure"},
		}
	case "scope":
		fake.missingRealm = true
	case "secret-lookup":
		changeAddConfig(t, *opts, func(cfg *config.TeamDefaults) {
			cfg.Spec.Defaults.Harnesses = []config.Harness{
				{Name: "opencode", Source: "kukeon", Path: "team/harnesses/opencode"},
			}
			cfg.Spec.Secrets = []config.SecretBinding{binding(nil)}
		})
		fake.lookupErr = errors.New("unsafe-canary")
	case "image":
		changeAddConfig(t, *opts, func(cfg *config.TeamDefaults) {
			cfg.Spec.Defaults.Roles[0].Needs = &config.Needs{Image: []string{"unavailable-capability"}}
		})
	case "definition":
		opts.Sources = map[string]fs.FS{}
	case "collision":
		// The project's Source is stored with another repository (AC6).
		fake.storeSource(opts.Name, config.SourceSpec{URL: "https://example.test/repo", Branch: "main"})
	case "clone-collision":
		// A TeamDefaults written before #1643 clones kukeon at
		// /home/user/kukeon-src, where the project of team kukeon-src goes.
		storeLegacyDefaults(t, fake, addDefaults())
		opts.Name = "kukeon-src"
	case "credentials":
		opts.Source = "https://user:unsafe-canary@host/repo"
	}
}

func TestAddDetachedRemoteHEADRequiresSelector(t *testing.T) {
	opts, commit := setupAdd(t)
	fixtureGit(t, os.Getenv("ADD_FIXTURE_REPO"), "checkout", "--detach", commit)
	if _, err := Add(t.Context(), opts); err == nil || !strings.Contains(err.Error(), "no default branch") {
		t.Fatalf("error=%v", err)
	}
	if opts.Client.(*fakeClient).applies != 0 {
		t.Fatal("mutated daemon without a selector")
	}
	opts.Commit = commit
	if _, err := Add(t.Context(), opts); err != nil {
		t.Fatal(err)
	}
}
