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

package ctr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	internalerrdefs "github.com/eminwux/kukeon/internal/errdefs"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const (
	// execDefaultTerm is the TERM an exec with a PTY gets unless the caller
	// set one.
	execDefaultTerm = "TERM=xterm-256color"
	// execIODrainTimeout bounds the wait for the output copiers after the
	// process exited. They end on their own once every holder of the
	// process's stdout/stderr is gone; a background child that kept them
	// open must not keep the exec open (the shim allows itself the same 2s).
	execIODrainTimeout = 2 * time.Second
	execFIFODirMode    = 0o700
)

// ExecSpec describes one extra process to run in a container's running
// task (issue #1432).
type ExecSpec struct {
	// ID is the containerd exec ID, unique within the task.
	ID string
	// Command is the argv, at least one element.
	Command []string
	// Root runs the process as uid 0 / gid 0 with no additional gids.
	Root bool
	// User is "uid", "uid:gid" or a name, resolved like `ctr tasks exec
	// --user`; empty keeps the container's own process user. Ignored when
	// Root is set.
	User string
	// Env is KEY=VAL entries layered over the container's env.
	Env []string
	// Workdir overrides the container's cwd when non-empty.
	Workdir string
	// TTY allocates a PTY inside the container.
	TTY bool
	// FIFODir is where the stdio FIFOs are created. The shim on the host
	// opens the same paths, so it must be identical inside and outside the
	// kukeond cell. Empty uses containerd's default.
	FIFODir string
}

// ExecStdio is the caller's end of an exec's streams. A nil Stdin gives the
// process an already-ended stdin; with a TTY, Stderr is unused.
type ExecStdio struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecProcess is a started exec. Every method takes the caller's context;
// the containerd namespace is added to it here.
type ExecProcess interface {
	// Wait blocks until the process exited and its output has been copied
	// to the caller's writers (bounded, see execIODrainTimeout), and
	// returns the exit code. Safe to call more than once.
	Wait(ctx context.Context) (exitCode uint32, err error)
	// Resize sets the PTY size.
	Resize(ctx context.Context, cols, rows uint32) error
	// CloseStdin makes the process read EOF once the stdin already written
	// has been consumed.
	CloseStdin(ctx context.Context) error
	// Kill sends SIGKILL; a process that already exited is not an error.
	Kill(ctx context.Context) error
	// Delete removes the exited process from the task and closes its IO,
	// which removes the FIFOs.
	Delete(ctx context.Context) error
}

// BuildExecProcessSpec derives the OCI process spec of an exec from the
// container's own process spec, which is not modified. Capabilities,
// rlimits and NoNewPrivileges are inherited. The user is the container's
// unless spec.Root is set; a spec.User is resolved separately because it
// may need the container's rootfs (see ExecTask).
func BuildExecProcessSpec(container *runtimespec.Process, spec ExecSpec) (*runtimespec.Process, error) {
	if container == nil {
		return nil, errors.New("container spec has no Process")
	}
	if len(spec.Command) == 0 {
		return nil, errors.New("exec command is empty")
	}
	// A JSON round trip is a deep copy of every slice and pointer field.
	raw, err := json.Marshal(container)
	if err != nil {
		return nil, fmt.Errorf("copy container process spec: %w", err)
	}
	proc := &runtimespec.Process{}
	if err = json.Unmarshal(raw, proc); err != nil {
		return nil, fmt.Errorf("copy container process spec: %w", err)
	}

	proc.Args = append([]string(nil), spec.Command...)
	proc.CommandLine = ""
	proc.Terminal = spec.TTY
	proc.ConsoleSize = nil
	if spec.Workdir != "" {
		proc.Cwd = spec.Workdir
	}
	if spec.Root {
		proc.User = runtimespec.User{UID: 0, GID: 0}
	}

	env := spec.Env
	if spec.TTY && !envHasKey(env, "TERM") {
		env = append(append([]string(nil), env...), execDefaultTerm)
	}
	proc.Env = mergeEnv(proc.Env, env)
	return proc, nil
}

// mergeEnv appends extra to base. A key set again later replaces the
// earlier entry, so the result carries each key once with its last value.
func mergeEnv(base, extra []string) []string {
	all := append(append([]string(nil), base...), extra...)
	last := make(map[string]int, len(all))
	for i, entry := range all {
		last[envKey(entry)] = i
	}
	merged := make([]string, 0, len(all))
	for i, entry := range all {
		if last[envKey(entry)] == i {
			merged = append(merged, entry)
		}
	}
	return merged
}

func envKey(entry string) string {
	key, _, _ := strings.Cut(entry, "=")
	return key
}

// ExecTask starts spec as a new process in the running task of the
// container. The process's exit is being waited on before it starts, so a
// fast exit is never missed.
func (c *client) ExecTask(
	namespace, containerdID string,
	spec ExecSpec,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (ExecProcess, error) {
	if containerdID == "" {
		return nil, internalerrdefs.ErrEmptyContainerID
	}
	if spec.ID == "" {
		return nil, errors.New("exec id is required")
	}
	task, err := c.loadTask(namespace, containerdID)
	if err != nil {
		return nil, err
	}
	container, err := c.loadContainer(namespace, containerdID)
	if err != nil {
		return nil, err
	}
	nsCtx := c.namespaceCtx(namespace)
	containerSpec, err := container.Spec(nsCtx)
	if err != nil {
		return nil, fmt.Errorf("read container spec: %w", err)
	}
	if containerSpec == nil {
		return nil, errors.New("container has no spec")
	}
	proc, err := BuildExecProcessSpec(containerSpec.Process, spec)
	if err != nil {
		return nil, err
	}
	if spec.User != "" && !spec.Root {
		// The way `ctr tasks exec --user` does it: a name or a bare uid is
		// looked up in the container's rootfs (a temporary read-only mount
		// of its snapshot); "uid:gid" needs no lookup.
		info, infoErr := container.Info(nsCtx)
		if infoErr != nil {
			return nil, fmt.Errorf("read container info: %w", infoErr)
		}
		// WithUser reads Root to find the rootfs and refuses a spec without
		// its Linux section when it has to look a name up.
		scratch := &oci.Spec{Process: proc, Root: containerSpec.Root, Linux: containerSpec.Linux}
		if err = oci.WithUser(spec.User)(nsCtx, c.conn(), &info, scratch); err != nil {
			return nil, fmt.Errorf("resolve user %q: %w", spec.User, err)
		}
		proc = scratch.Process
	}

	ioOpts := []cio.Opt{cio.WithStreams(stdin, stdout, stderr)}
	if spec.FIFODir != "" {
		if err = os.MkdirAll(spec.FIFODir, execFIFODirMode); err != nil {
			return nil, fmt.Errorf("create exec fifo dir: %w", err)
		}
		ioOpts = append(ioOpts, cio.WithFIFODir(spec.FIFODir))
	}
	if spec.TTY {
		ioOpts = append(ioOpts, cio.WithTerminal)
	}

	process, err := task.Exec(nsCtx, spec.ID, proc, cio.NewCreator(ioOpts...))
	if err != nil {
		return nil, fmt.Errorf("failed to create exec process: %w", err)
	}
	p := &execProcess{namespace: namespace, process: process, exited: make(chan struct{})}
	statusC, err := process.Wait(nsCtx)
	if err != nil {
		_ = p.Delete(nsCtx)
		return nil, fmt.Errorf("failed to wait on exec process: %w", err)
	}
	if err = process.Start(nsCtx); err != nil {
		_ = p.Delete(nsCtx)
		return nil, fmt.Errorf("failed to start exec process: %w", err)
	}
	go func() {
		status := <-statusC
		p.exitCode, _, p.exitErr = status.Result()
		close(p.exited)
	}()
	return p, nil
}

// execProcess adapts a containerd exec process to ExecProcess.
type execProcess struct {
	namespace string
	process   containerd.Process

	// exited is closed once exitCode and exitErr are set.
	exited   chan struct{}
	exitCode uint32
	exitErr  error

	drain sync.Once
}

func (p *execProcess) Wait(ctx context.Context) (uint32, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-p.exited:
	}
	if p.exitErr != nil {
		return 0, p.exitErr
	}
	p.drain.Do(p.drainIO)
	return p.exitCode, nil
}

// drainIO waits for the output copiers to reach the end of the process's
// stdout and stderr.
func (p *execProcess) drainIO() {
	streams := p.process.IO()
	if streams == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		streams.Wait()
		close(done)
	}()
	timer := time.NewTimer(execIODrainTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}

func (p *execProcess) Resize(ctx context.Context, cols, rows uint32) error {
	return p.process.Resize(p.ctx(ctx), cols, rows)
}

func (p *execProcess) CloseStdin(ctx context.Context) error {
	return p.process.CloseIO(p.ctx(ctx), containerd.WithStdinCloser)
}

func (p *execProcess) Kill(ctx context.Context) error {
	err := p.process.Kill(p.ctx(ctx), syscall.SIGKILL)
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func (p *execProcess) Delete(ctx context.Context) error {
	_, err := p.process.Delete(p.ctx(ctx))
	// A successful Delete already closed the IO; closing it again is
	// harmless, and after a failed one it is what removes the FIFOs.
	if streams := p.process.IO(); streams != nil {
		streams.Cancel()
		_ = streams.Close()
	}
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return nil
}

func (p *execProcess) ctx(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, p.namespace)
}
