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

package runner

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"gopkg.in/yaml.v3"
)

// sbshProfileDoc mirrors the subset of the sbsh TerminalProfile YAML schema
// the daemon writes for an attachable container. Only fields driven by the
// container-level tty block live here; the rest are sbsh's own defaults.
type sbshProfileDoc struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   sbshProfileMeta    `yaml:"metadata"`
	Spec       sbshProfileSpecDoc `yaml:"spec"`
}

type sbshProfileMeta struct {
	Name string `yaml:"name"`
}

type sbshProfileSpecDoc struct {
	Shell  sbshProfileShell   `yaml:"shell,omitempty"`
	Stages *sbshProfileStages `yaml:"stages,omitempty"`
}

type sbshProfileShell struct {
	// Cmd is required by sbsh's profile validator. Populated from the
	// container's command (with a /bin/sh fallback when the container
	// inherits its command from the image's ENTRYPOINT/CMD).
	Cmd string `yaml:"cmd"`
	// CmdArgs carries the container's args alongside Cmd; sbsh executes
	// `Cmd CmdArgs...` under the profile-driven shell wrapper.
	CmdArgs []string `yaml:"cmdArgs,omitempty"`
	// InheritEnv tells sbsh to inherit the parent process's env, which is
	// the container's OCI-injected env — so the profile does not have to
	// duplicate ContainerSpec.Env.
	InheritEnv bool `yaml:"inheritEnv,omitempty"`
	// Prompt is the literal prompt expression sbsh sets in the wrapped shell.
	Prompt string `yaml:"prompt,omitempty"`
}

// fallbackShellCmd is the shell binary the daemon writes into shell.cmd
// when ContainerSpec.Command is empty (image-default ENTRYPOINT/CMD).
// The same binary is present in busybox, alpine, debian, and ubuntu — the
// images used in the smoke and e2e tests.
const fallbackShellCmd = "/bin/sh"

type sbshProfileStages struct {
	OnInit []sbshProfileStage `yaml:"onInit,omitempty"`
}

type sbshProfileStage struct {
	Script string `yaml:"script,omitempty"`
}

// writeSbshProfile renders the container's tty config plus the inherited
// command/args as an sbsh TerminalProfile YAML and writes it to
// <dir>/<ctr.AttachableProfileFile>. Returns nil without touching the
// filesystem when the container's tty block is empty (the caller must
// not pass UseProfile=true in that case).
//
// sbsh's profile validator requires `shell.cmd` — even when the post-`--`
// workload would supply it — so the daemon mirrors ContainerSpec.Command
// (and Args) into shell.cmd/cmdArgs, and falls back to /bin/sh for the
// image-inherited-command case. inheritEnv is set so the container's
// OCI-injected env flows through without the profile duplicating it.
//
// The file is written 0600 because it lives inside the per-container tty
// dir, which is daemon-private (0700); keeping the inner file similarly
// tight guards against a future loosening of the parent dir.
func writeSbshProfile(dir string, spec intmodel.ContainerSpec) error {
	if spec.Tty.IsEmpty() {
		return nil
	}
	cmd := spec.Command
	cmdArgs := append([]string(nil), spec.Args...)
	if cmd == "" {
		cmd = fallbackShellCmd
		cmdArgs = nil
	}
	doc := sbshProfileDoc{
		APIVersion: "sbsh/v1beta1",
		Kind:       "TerminalProfile",
		Metadata:   sbshProfileMeta{Name: ctr.AttachableProfileName},
		Spec: sbshProfileSpecDoc{
			Shell: sbshProfileShell{
				Cmd:        cmd,
				CmdArgs:    cmdArgs,
				InheritEnv: true,
				Prompt:     spec.Tty.Prompt,
			},
		},
	}
	if len(spec.Tty.OnInit) > 0 {
		stages := make([]sbshProfileStage, 0, len(spec.Tty.OnInit))
		for _, s := range spec.Tty.OnInit {
			if s.Script == "" {
				continue
			}
			stages = append(stages, sbshProfileStage{Script: s.Script})
		}
		if len(stages) > 0 {
			doc.Spec.Stages = &sbshProfileStages{OnInit: stages}
		}
	}
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal sbsh profile: %w", err)
	}
	path := filepath.Join(dir, ctr.AttachableProfileFile)
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write sbsh profile %q: %w", path, err)
	}
	return nil
}
