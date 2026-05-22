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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// errRequiredStageFailed signals that at least one runOn: create stage failed.
// run() maps it to a non-zero exit before sbshserver.Serve so the daemon
// observes the container task as Failed — the required-failure contract phase
// 1a established for repos (issue #617), applied to create stages here: a
// TtyStage carries no per-stage Required knob, so every create stage is
// implicitly required and any failure is fatal. The per-stage detail is logged
// to kuketty's slog output as each stage runs. Issue #635.
var errRequiredStageFailed = errors.New("one or more required create stages failed")

// processStages is kuketty's pre-Serve step for runOn: create TtyStages: it
// runs each create stage's Script to completion, in declaration order, before
// the workload starts. Stages run with kuketty's own environment (the
// container's OCI Process.Env — the same identity onInit scripts and repo
// clones use), via `sh -c <script>`.
//
// An empty stage list is a no-op — the common case for a container with no
// create stages. On the first failing stage processStages logs the outcome and
// returns errRequiredStageFailed, so run() exits before Serve and the daemon
// sees the task as Failed; subsequent stages do not run. Reporting per-stage
// outcomes into ContainerStatus.Stages over the GetSetupStatus RPC is deferred
// to phase B (#689); this phase wires the executor and routing only. Issue
// #635.
func processStages(ctx context.Context, stages []indexedStage, logger *slog.Logger) error {
	for _, st := range stages {
		logger.InfoContext(ctx, "running create stage", "index", st.Index)
		if err := runStage(ctx, st.Stage); err != nil {
			logger.WarnContext(ctx, "create stage failed", "index", st.Index, "error", err)
			return fmt.Errorf("%w: stage %d: %v", errRequiredStageFailed, st.Index, err)
		}
		logger.InfoContext(ctx, "create stage completed", "index", st.Index)
	}
	return nil
}

// runStage runs a single create stage's Script through `sh -c`, inheriting
// kuketty's environment. On failure the combined output is folded into the
// error so the log line carries an actionable message. An empty Script is a
// no-op success.
func runStage(ctx context.Context, stage v1beta1.TtyStage) error {
	if strings.TrimSpace(stage.Script) == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", stage.Script)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		trimmed := strings.TrimSpace(string(out))
		if trimmed != "" {
			return fmt.Errorf("%w: %s", err, trimmed)
		}
		return err
	}
	return nil
}
