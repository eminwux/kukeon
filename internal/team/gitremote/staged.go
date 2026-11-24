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
	"context"
	"errors"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/internal/gitcred"
	"github.com/eminwux/kukeon/internal/gitcred/gitagent"
	"github.com/eminwux/kukeon/internal/gitcred/gitplan"
)

type stagedKey struct{}

// stagedIdentity is a git bundle kukeond staged for a fetch on the host
// (issue #1643, D10): the directory it lives in, its plan, the ssh command
// the plan implies and the private agent holding its passphrase keys.
type stagedIdentity struct {
	dir        string
	plan       gitplan.Plan
	sshCommand string
	agent      string
}

// errStaged is the error of a staged directory kuke cannot use; it names no
// path inside it.
var errStaged = errors.New(
	"git source access failed: the stored git identity kukeond staged for this fetch is unusable (Git output withheld)")

// WithStagedIdentity makes every Run under the returned context authenticate
// with the bundle staged at dir instead of the caller's own git and ssh
// configuration: the transport follows the stored credential, a passphrase
// key is unlocked in a private ssh-agent (kuke itself is the askpass helper),
// and the caller's agent and credential helpers are not used. release stops
// the agent; the caller releases the staging with kukeond.
func WithStagedIdentity(ctx context.Context, dir string) (context.Context, func(), error) {
	plan, err := gitplan.Load(gitplan.PlanPathAt(dir))
	if err != nil || plan == nil {
		return nil, nil, errStaged
	}
	staged := stagedIdentity{dir: dir, plan: *plan, sshCommand: stagedSSHCommand(dir, *plan)}
	release := func() {}
	if len(plan.AgentKeys) > 0 {
		socket, stop, agentErr := gitagent.Run(context.WithoutCancel(ctx))
		if agentErr != nil {
			return nil, nil, errors.New("git source access failed: ssh-agent unavailable for the stored key")
		}
		self, selfErr := os.Executable()
		if selfErr != nil {
			stop()
			return nil, nil, errStaged
		}
		for _, key := range plan.AgentKeys {
			if addErr := gitagent.AddKey(ctx, self, socket, gitagent.HostAskpassFileEnv, key); addErr != nil {
				stop()
				return nil, nil, errors.New(
					"git source access failed: the stored key's passphrase was not accepted (ssh-add output withheld)")
			}
		}
		staged.agent, release = socket, stop
	}
	return context.WithValue(ctx, stagedKey{}, staged), release, nil
}

// stagedSSHCommand is the ssh command the bundle configures: the repo's own
// core.sshCommand, else the cell-global one when the plan has it, else plain
// ssh. It must be passed as GIT_SSH_COMMAND, which wins over core.sshCommand.
func stagedSSHCommand(dir string, plan gitplan.Plan) string {
	for _, entry := range plan.Repos[gitplan.HostFetchRepo].Config {
		if strings.EqualFold(entry.Key, "core.sshCommand") {
			return entry.Value
		}
	}
	if plan.SSHCommand {
		return "ssh -F " + path.Join(dir, gitplan.SSHConfigFile)
	}
	return sshScheme
}

func stagedFrom(ctx context.Context) (stagedIdentity, bool) {
	staged, ok := ctx.Value(stagedKey{}).(stagedIdentity)
	return staged, ok
}

// Remote is the URL to fetch address from: the one the staged plan rewrote it
// to (its transport follows the stored credential), else the address expanded
// with gitcred.CloneURL.
func Remote(ctx context.Context, address string) string {
	if staged, ok := stagedFrom(ctx); ok {
		if url := staged.plan.Repos[gitplan.HostFetchRepo].URL; url != "" {
			return url
		}
	}
	return gitcred.CloneURL(address)
}

// stagedEnv is git's environment under a staged identity: the caller's
// environment without its git configuration, ssh command and agent, then the
// staged gitconfig as the system scope (no global scope), the repo's config
// entries as command scope, and the plan's ssh command.
//
// GIT_CONFIG_NOSYSTEM is not set: it would make git skip GIT_CONFIG_SYSTEM.
func stagedEnv(staged stagedIdentity) ([]string, string) {
	var env []string
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "SSH_AUTH_SOCK", key == "GIT_SSH_COMMAND", key == "GIT_SSH", key == "GIT_SSH_VARIANT",
			key == "GIT_CONFIG_NOSYSTEM", key == "GIT_CONFIG_SYSTEM", key == "GIT_CONFIG_GLOBAL",
			key == "GIT_CONFIG_PARAMETERS", key == "GIT_CONFIG_COUNT", strings.HasPrefix(key, "GIT_CONFIG_KEY_"),
			strings.HasPrefix(key, "GIT_CONFIG_VALUE_"), key == "GIT_ASKPASS", key == "SSH_ASKPASS":
			continue
		}
		env = append(env, kv)
	}
	config := staged.plan.Repos[gitplan.HostFetchRepo].Config
	env = append(env,
		"GIT_CONFIG_SYSTEM="+gitplan.ConfigPathAt(staged.dir),
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT="+strconv.Itoa(len(config)))
	for i, entry := range config {
		suffix := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+suffix+"="+entry.Key, "GIT_CONFIG_VALUE_"+suffix+"="+entry.Value)
	}
	if staged.agent != "" {
		env = append(env, "SSH_AUTH_SOCK="+staged.agent)
	}
	return env, staged.sshCommand
}
