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

// Package gitplan is the contract between the daemon, which renders a
// container's stored git credentials into a staged directory, and kuketty,
// which consumes it inside the container (issue #1472). It is stdlib-only so
// kuketty's import set stays small.
package gitplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
)

const (
	// MountPath is where the staged git directory is mounted, read-only,
	// inside the container.
	MountPath = "/.kukeon/git"
	// ConfigFile is the cell-global gitconfig inside MountPath.
	ConfigFile = "gitconfig"
	// PlanFile is the kuketty plan inside MountPath.
	PlanFile = "plan.json"
	// SSHConfigFile is the generated ssh config inside MountPath.
	SSHConfigFile = "ssh_config"
	// KnownHostsFile holds the built-in forge host keys inside MountPath.
	KnownHostsFile = "known_hosts"
	// ConfigEnv is the environment variable that points git at ConfigFile. It
	// replaces /etc/gitconfig as git's system scope, the lowest-precedence
	// level, so ~/.gitconfig and a repo's local config still override it.
	ConfigEnv = "GIT_CONFIG_SYSTEM"
	// AskpassFileEnv makes kuketty act as an SSH_ASKPASS helper when set: it
	// prints the named file and exits.
	AskpassFileEnv = "KUKETTY_ASKPASS_FILE" //nolint:gosec // an environment variable name, not a credential
)

// ConfigPath is the in-container path of the cell-global gitconfig.
func ConfigPath() string { return path.Join(MountPath, ConfigFile) }

// PlanPath is the in-container path of the kuketty plan.
func PlanPath() string { return path.Join(MountPath, PlanFile) }

// Plan is what kuketty does before the workload starts: load passphrase-
// protected keys into an ssh-agent, then clone each repo with the URL and the
// repo-local config resolved for it.
type Plan struct {
	// SSHCommand reports that the cell-global gitconfig sets core.sshCommand,
	// so kuketty must not install its own GIT_SSH_COMMAND fallback.
	SSHCommand bool `json:"sshCommand,omitempty"`
	// AgentKeys are the passphrase-protected private keys to add to the
	// per-container ssh-agent.
	AgentKeys []AgentKey `json:"agentKeys,omitempty"`
	// Repos holds the per-source resolution, keyed by ContainerRepo.Name.
	Repos map[string]Repo `json:"repos,omitempty"`
}

// AgentKey is one key kuketty adds to the ssh-agent. Both fields are
// in-container file paths; the passphrase never travels in the plan.
type AgentKey struct {
	Key            string `json:"key"`
	PassphraseFile string `json:"passphraseFile"`
}

// Repo is the resolved git setup of one source.
type Repo struct {
	// URL is the clone URL whose transport follows the credential bound to
	// the source; empty keeps the declared URL.
	URL string `json:"url,omitempty"`
	// Config is the repo-local git config kuketty passes to the clone and
	// writes into the checkout, in order.
	Config []ConfigEntry `json:"config,omitempty"`
}

// ConfigEntry is one repo-local git config assignment. Add appends a value to
// a multi-valued key instead of replacing it.
type ConfigEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Add   bool   `json:"add,omitempty"`
}

// Load reads the plan at path. A missing file is not an error: it returns a
// nil plan, the state of every container that declares no git block.
func Load(path string) (*Plan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil //nolint:nilnil // absent plan is the documented no-op.
		}
		return nil, fmt.Errorf("read git plan %s: %w", path, err)
	}
	var plan Plan
	if err = json.Unmarshal(raw, &plan); err != nil {
		return nil, fmt.Errorf("parse git plan %s: %w", path, err)
	}
	return &plan, nil
}
