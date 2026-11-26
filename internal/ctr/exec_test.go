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

package ctr_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/eminwux/kukeon/internal/ctr"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func containerProcess() *runtimespec.Process {
	return &runtimespec.Process{
		Terminal: false,
		User:     runtimespec.User{UID: 1000, GID: 1000, AdditionalGids: []uint32{1000, 988}},
		Args:     []string{"/bin/kuketty", "--", "claude"},
		Env:      []string{"PATH=/usr/bin", "HOME=/home/agent", "TERM=dumb", "MODE=prod"},
		Cwd:      "/workspace",
		Capabilities: &runtimespec.LinuxCapabilities{
			Bounding:  []string{"CAP_CHOWN", "CAP_KILL"},
			Effective: []string{"CAP_CHOWN"},
		},
		Rlimits:         []runtimespec.POSIXRlimit{{Type: "RLIMIT_NOFILE", Hard: 1024, Soft: 1024}},
		NoNewPrivileges: true,
	}
}

func build(t *testing.T, spec ctr.ExecSpec) *runtimespec.Process {
	t.Helper()
	if len(spec.Command) == 0 {
		spec.Command = []string{"id", "-u"}
	}
	proc, err := ctr.BuildExecProcessSpec(containerProcess(), spec)
	if err != nil {
		t.Fatalf("BuildExecProcessSpec: %v", err)
	}
	return proc
}

func TestBuildExecProcessSpecDefaultIdentity(t *testing.T) {
	proc := build(t, ctr.ExecSpec{Command: []string{"sh", "-c", "id"}})
	want := containerProcess()

	if !reflect.DeepEqual(proc.User, want.User) {
		t.Errorf("User = %+v, want the container's %+v", proc.User, want.User)
	}
	if !slices.Equal(proc.Args, []string{"sh", "-c", "id"}) {
		t.Errorf("Args = %v", proc.Args)
	}
	if proc.Terminal {
		t.Error("Terminal set without a TTY request")
	}
	if !reflect.DeepEqual(proc.Capabilities, want.Capabilities) || !reflect.DeepEqual(proc.Rlimits, want.Rlimits) ||
		!proc.NoNewPrivileges {
		t.Errorf("capabilities, rlimits or NoNewPrivileges were not inherited: %+v", proc)
	}
	if !slices.Equal(proc.Env, want.Env) {
		t.Errorf("Env = %v, want the container's %v", proc.Env, want.Env)
	}
}

func TestBuildExecProcessSpecRoot(t *testing.T) {
	proc := build(t, ctr.ExecSpec{Root: true})
	if proc.User.UID != 0 || proc.User.GID != 0 || len(proc.User.AdditionalGids) != 0 {
		t.Fatalf("User = %+v, want uid 0, gid 0 and no additional gids", proc.User)
	}
	if proc.Capabilities == nil || !slices.Equal(proc.Capabilities.Bounding, []string{"CAP_CHOWN", "CAP_KILL"}) {
		t.Errorf("root exec lost the container's capabilities: %+v", proc.Capabilities)
	}
}

func TestBuildExecProcessSpecEnv(t *testing.T) {
	proc := build(t, ctr.ExecSpec{Env: []string{"EXTRA=1", "MODE=debug", "EXTRA=2"}})
	want := []string{"PATH=/usr/bin", "HOME=/home/agent", "TERM=dumb", "MODE=debug", "EXTRA=2"}
	if !slices.Equal(proc.Env, want) {
		t.Fatalf("Env = %v, want %v (appended, a later duplicate key wins)", proc.Env, want)
	}
}

func TestBuildExecProcessSpecWorkdir(t *testing.T) {
	if got := build(t, ctr.ExecSpec{}).Cwd; got != "/workspace" {
		t.Errorf("Cwd with no workdir = %q, want the container's /workspace", got)
	}
	if got := build(t, ctr.ExecSpec{Workdir: "/tmp"}).Cwd; got != "/tmp" {
		t.Errorf("Cwd = %q, want /tmp", got)
	}
}

func TestBuildExecProcessSpecTerminal(t *testing.T) {
	proc := build(t, ctr.ExecSpec{TTY: true})
	if !proc.Terminal {
		t.Error("Terminal not set for a TTY request")
	}
	if !slices.Contains(proc.Env, "TERM=xterm-256color") || slices.Contains(proc.Env, "TERM=dumb") {
		t.Errorf("Env = %v, want TERM=xterm-256color in place of the container's TERM", proc.Env)
	}

	proc = build(t, ctr.ExecSpec{TTY: true, Env: []string{"TERM=vt100"}})
	if !slices.Contains(proc.Env, "TERM=vt100") || slices.Contains(proc.Env, "TERM=xterm-256color") {
		t.Errorf("Env = %v, want the caller's TERM=vt100 kept", proc.Env)
	}

	// Without a TTY the container's TERM is left alone.
	if proc = build(t, ctr.ExecSpec{}); !slices.Contains(proc.Env, "TERM=dumb") {
		t.Errorf("Env = %v, want the container's TERM without a TTY", proc.Env)
	}
}

func TestBuildExecProcessSpecDoesNotMutateContainer(t *testing.T) {
	container := containerProcess()
	spec := ctr.ExecSpec{
		Command: []string{"bash"}, Root: true, TTY: true, Workdir: "/tmp", Env: []string{"MODE=debug", "A=b"},
	}
	proc, err := ctr.BuildExecProcessSpec(container, spec)
	if err != nil {
		t.Fatal(err)
	}
	// Writing through every slice of the result must not reach the source.
	proc.Args[0] = "changed"
	proc.Env[0] = "changed"
	proc.Capabilities.Bounding[0] = "changed"
	proc.Rlimits[0].Hard = 1
	proc.User.AdditionalGids = append(proc.User.AdditionalGids, 1)

	if !reflect.DeepEqual(container, containerProcess()) {
		t.Fatalf("the container's process spec was modified: %+v", container)
	}
	if !slices.Equal(spec.Env, []string{"MODE=debug", "A=b"}) {
		t.Fatalf("the exec spec's env was modified: %v", spec.Env)
	}
}

func TestBuildExecProcessSpecRejectsBadInput(t *testing.T) {
	if _, err := ctr.BuildExecProcessSpec(nil, ctr.ExecSpec{Command: []string{"id"}}); err == nil {
		t.Error("a container without a process spec was accepted")
	}
	if _, err := ctr.BuildExecProcessSpec(containerProcess(), ctr.ExecSpec{}); err == nil {
		t.Error("an empty command was accepted")
	}
}
