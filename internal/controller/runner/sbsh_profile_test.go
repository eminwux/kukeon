//go:build !integration

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

//nolint:testpackage // exercises private writeSbshProfile.
package runner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"gopkg.in/yaml.v3"
)

func TestWriteSbshProfile_NilOrEmptySkipsWrite(t *testing.T) {
	cases := []struct {
		name string
		tty  *intmodel.ContainerTty
	}{
		{"nil", nil},
		{"empty struct", &intmodel.ContainerTty{}},
		{"only blank scripts", &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{{Script: ""}, {Script: ""}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			spec := intmodel.ContainerSpec{Command: "/bin/bash", Tty: tc.tty}
			if err := writeSbshProfile(dir, spec); err != nil {
				t.Fatalf("writeSbshProfile: %v", err)
			}
			path := filepath.Join(dir, ctr.AttachableProfileFile)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("expected no profile file at %s, got err=%v", path, err)
			}
		})
	}
}

func TestWriteSbshProfile_RendersTerminalProfileYAML(t *testing.T) {
	dir := t.TempDir()
	spec := intmodel.ContainerSpec{
		Command: "/bin/bash",
		Args:    []string{"-l"},
		Tty: &intmodel.ContainerTty{
			Prompt: `"\[\e[1;36m\]claude \u@\h\[\e[0m\]:\w\$ "`,
			OnInit: []intmodel.TtyStage{
				{Script: "git pull"},
				{Script: "claude"},
			},
		},
	}
	if err := writeSbshProfile(dir, spec); err != nil {
		t.Fatalf("writeSbshProfile: %v", err)
	}

	path := filepath.Join(dir, ctr.AttachableProfileFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat profile: %v", err)
	}
	// The file lives inside a 0700 daemon-private dir; keep the inner
	// file at 0600 so a future loosening of the parent does not silently
	// expose the script body.
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("profile mode = %#o, want 0600", mode)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}

	var parsed sbshProfileDoc
	if err = yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal profile: %v\n%s", err, string(data))
	}
	if parsed.APIVersion != "sbsh/v1beta1" || parsed.Kind != "TerminalProfile" {
		t.Errorf("apiVersion/kind = %q/%q, want sbsh/v1beta1 / TerminalProfile",
			parsed.APIVersion, parsed.Kind)
	}
	if parsed.Metadata.Name != ctr.AttachableProfileName {
		t.Errorf("metadata.name = %q, want %q", parsed.Metadata.Name, ctr.AttachableProfileName)
	}
	if parsed.Spec.Shell.Cmd != spec.Command {
		t.Errorf("spec.shell.cmd = %q, want %q", parsed.Spec.Shell.Cmd, spec.Command)
	}
	if len(parsed.Spec.Shell.CmdArgs) != 1 || parsed.Spec.Shell.CmdArgs[0] != "-l" {
		t.Errorf("spec.shell.cmdArgs = %+v, want [-l]", parsed.Spec.Shell.CmdArgs)
	}
	if !parsed.Spec.Shell.InheritEnv {
		t.Errorf("spec.shell.inheritEnv = false, want true")
	}
	if parsed.Spec.Shell.Prompt != spec.Tty.Prompt {
		t.Errorf("spec.shell.prompt = %q, want %q", parsed.Spec.Shell.Prompt, spec.Tty.Prompt)
	}
	if parsed.Spec.Stages == nil || len(parsed.Spec.Stages.OnInit) != 2 {
		t.Fatalf("spec.stages = %+v, want 2 onInit entries", parsed.Spec.Stages)
	}
	if parsed.Spec.Stages.OnInit[0].Script != "git pull" ||
		parsed.Spec.Stages.OnInit[1].Script != "claude" {
		t.Errorf("onInit scripts = %+v, want [git pull, claude]", parsed.Spec.Stages.OnInit)
	}
}

// TestWriteSbshProfile_EmptyCommandFallsBackToShell covers the case where
// the container inherits its command from the image's ENTRYPOINT/CMD —
// sbsh's profile validator still requires shell.cmd, so the daemon
// substitutes /bin/sh rather than emit an unloadable profile.
func TestWriteSbshProfile_EmptyCommandFallsBackToShell(t *testing.T) {
	dir := t.TempDir()
	spec := intmodel.ContainerSpec{
		// Command and Args intentionally unset — image-default path.
		Tty: &intmodel.ContainerTty{Prompt: `"\u\$ "`},
	}
	if err := writeSbshProfile(dir, spec); err != nil {
		t.Fatalf("writeSbshProfile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ctr.AttachableProfileFile))
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var parsed sbshProfileDoc
	if err = yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Spec.Shell.Cmd != fallbackShellCmd {
		t.Errorf("fallback shell.cmd = %q, want %q", parsed.Spec.Shell.Cmd, fallbackShellCmd)
	}
	if len(parsed.Spec.Shell.CmdArgs) != 0 {
		t.Errorf("fallback shell.cmdArgs = %+v, want empty", parsed.Spec.Shell.CmdArgs)
	}
}

func TestWriteSbshProfile_DropsBlankScripts(t *testing.T) {
	dir := t.TempDir()
	spec := intmodel.ContainerSpec{
		Command: "/bin/bash",
		Tty: &intmodel.ContainerTty{
			Prompt: `"\u\$ "`,
			OnInit: []intmodel.TtyStage{
				{Script: ""},
				{Script: "echo hi"},
				{Script: "  "}, // not stripped — caller-supplied whitespace is preserved
			},
		},
	}
	if err := writeSbshProfile(dir, spec); err != nil {
		t.Fatalf("writeSbshProfile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ctr.AttachableProfileFile))
	if err != nil {
		t.Fatalf("read profile: %v", err)
	}
	var parsed sbshProfileDoc
	if err = yaml.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Spec.Stages == nil || len(parsed.Spec.Stages.OnInit) != 2 {
		t.Fatalf("expected 2 stages after dropping empty, got %+v", parsed.Spec.Stages)
	}
	if parsed.Spec.Stages.OnInit[0].Script != "echo hi" ||
		parsed.Spec.Stages.OnInit[1].Script != "  " {
		t.Errorf("unexpected stages: %+v", parsed.Spec.Stages.OnInit)
	}
}
