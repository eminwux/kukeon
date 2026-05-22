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
	"fmt"
	"log/slog"

	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	sbshapi "github.com/eminwux/sbsh/pkg/api"
	sbshbuilder "github.com/eminwux/sbsh/pkg/builder"
)

// Reserved in-container paths and modes kuketty claims when it builds the sbsh
// TerminalSpec from the mounted ContainerDoc (issue #641). These are kukeon
// contract constants — the in-container layout the daemon's bind mounts
// produce — so they live on both sides by construction rather than travelling
// in the doc. Kept in sync with the Attachable* constants in
// internal/ctr/attachable.go (kuketty cannot import internal/ctr without
// dragging in the containerd/gRPC closure that issue #165 keeps it clear of —
// the same reason main.go's defaultConfigPath duplicates ctr.AttachableMetadataPath).
const (
	// attachableTTYDir is sbsh's per-terminal run path inside the container;
	// the daemon bind-mounts the per-container tty directory here. Mirrors
	// ctr.AttachableTTYDir.
	attachableTTYDir = "/run/kukeon/tty"

	// attachableSocketPath is where kuketty listens for the attach control
	// connection. Mirrors ctr.AttachableSocketPath.
	attachableSocketPath = attachableTTYDir + "/socket"

	// attachableCapturePath is where sbsh writes the workload capture
	// transcript. Mirrors ctr.AttachableCapturePath.
	attachableCapturePath = attachableTTYDir + "/capture"

	// attachableKukettyLogPath is the default in-container path of kuketty's
	// own slog output, peer to the socket/capture inodes inside the tty bind
	// mount. Mirrors ctr.AttachableKukettyLogPath
	// (= AttachableTTYDir + "/" + consts.KukeonContainerKukettyLogFile).
	attachableKukettyLogPath = attachableTTYDir + "/kuketty.log"

	// attachableSocketMode / attachableCaptureMode / attachableLogFileMode are
	// the octal modes applied to the respective inodes when a kukeon group GID
	// is configured. Mirror ctr.AttachableSocketMode / AttachableCaptureMode /
	// AttachableLogFileMode.
	attachableSocketMode  = "0660"
	attachableCaptureMode = "0640"
	attachableLogFileMode = "0640"
)

// buildTerminalSpec is the ContainerSpec -> sbsh TerminalSpec transform that
// used to live daemon-side in the runner's writeKukettyMetadata. Moving it
// into kuketty (issue #641) lets the daemon mount kukeon's own ContainerDoc
// instead of a pre-rendered sbsh TerminalDoc, so the crew-absorption layers
// (#617 repos, #635 run-once stages) can act on kukeon's spec inside the
// container and report into ContainerStatus without bolting fields onto the
// external sbsh type.
//
// The transform is a pure ContainerSpec -> TerminalSpec mapping: every input
// it needs is either a kukeon contract constant kuketty knows directly (the
// fixed socket/capture/log paths and modes above) or a value the daemon
// resolved and stamped into the doc — the resolved workload argv in
// Spec.Command/Spec.Args, the kukeon-group GID in Spec.KukeonGroupGID, and the
// resolved log level in Spec.Tty.LogLevel. kuketty applies no daemon-side
// defaulting of its own.
func buildTerminalSpec(
	ctx context.Context,
	logger *slog.Logger,
	spec v1beta1.ContainerSpec,
) (*sbshapi.TerminalSpec, error) {
	kukeonGroupGID := spec.KukeonGroupGID

	prompt := ""
	if spec.Tty != nil {
		prompt = spec.Tty.Prompt
	}

	opts := []sbshbuilder.TerminalOption{
		sbshbuilder.WithSocketFile(attachableSocketPath),
		// Capture file is anchored to the kukeon-controlled per-container tty
		// dir so `kuke log` (which tails ContainerCapturePath on the host) and
		// the in-container writer see the same inode through the directory bind
		// mount. Without an explicit WithCaptureFile, sbsh's inline builder
		// derives a capture path from runPath + ID that would land outside the
		// bind-mount, hiding the transcript from the host.
		sbshbuilder.WithCaptureFile(attachableCapturePath),
		// Spec.Prompt + Spec.SetPrompt are stamped from the cell's inline
		// Tty.Prompt with no profile loader involved (#494): an empty prompt
		// pairs with DisableSetPrompt(true) so a non-shell workload (nginx,
		// python) never receives a literal `export PS1=…` on stdin; a non-empty
		// prompt flips SetPrompt on (sbsh's `!DisableSetPrompt` rule).
		sbshbuilder.WithPrompt(prompt),
		sbshbuilder.WithDisableSetPrompt(prompt == ""),
		// Spec.EnvInherit=true tells sbsh's terminal runner to forward
		// kuketty's os.Environ() (which is the container's OCI Process.Env —
		// user-supplied containerSpec.Env merged with kukeon's KUKEON_* identity
		// vars at ctr.BuildContainerSpec) to the workload child. Without it,
		// sbsh's runner spawns the workload with only HOME + SBSH_* set
		// (sbsh@v0.11.2/internal/terminal/terminalrunner/terminal.go:54) — every
		// user env entry and every KUKEON_* entry gets stripped at the
		// kuketty → workload boundary. Pinned by the comprehensive transform
		// test so a future refactor cannot drop it (#494).
		sbshbuilder.WithEnvInherit(true),
	}
	if spec.Tty != nil && len(spec.Tty.OnInit) > 0 {
		// Inline OnInit (#494): map the cell's TtyStage{Script} entries to
		// sbsh's api.ExecStep{Script}. Only runOn: start (and absent) stages
		// forward here — runOn: create stages run in kuketty's pre-Serve
		// executor instead and are never handed to sbsh (#635). An all-create
		// OnInit therefore yields a nil ExecStep slice, leaving Stages.OnInit
		// zero just as an absent block would.
		opts = append(opts, sbshbuilder.WithOnInit(ttyStagesToExecSteps(spec.Tty.OnInit)))
	}
	if mode := modeIfGroupSet(kukeonGroupGID, attachableSocketMode); mode != "" {
		opts = append(opts, sbshbuilder.WithSocketMode(mode))
	}
	if mode := modeIfGroupSet(kukeonGroupGID, attachableCaptureMode); mode != "" {
		opts = append(opts, sbshbuilder.WithCaptureMode(mode))
	}
	if kukeonGroupGID > 0 {
		opts = append(opts, sbshbuilder.WithSocketGID(kukeonGroupGID))
		opts = append(opts, sbshbuilder.WithCaptureGID(kukeonGroupGID))
	}
	// Kuketty's own slog output is always-on at a daemon-controlled path by
	// default (peer to socket/capture inside the tty bind mount). A cell may
	// pin Tty.LogFile to an alternate in-container path; kuketty honors it
	// verbatim (#599).
	opts = append(opts, sbshbuilder.WithLogFile(resolveTtyLogFile(spec.Tty)))
	if mode := modeIfGroupSet(kukeonGroupGID, attachableLogFileMode); mode != "" {
		opts = append(opts, sbshbuilder.WithLogFileMode(mode))
	}
	if kukeonGroupGID > 0 {
		opts = append(opts, sbshbuilder.WithLogFileGID(kukeonGroupGID))
	}
	// The daemon resolved the per-container → server-config → "info" log-level
	// chain and stamped the result onto Tty.LogLevel; kuketty reads it
	// verbatim (sbsh's NewFileLogger rejects an empty level).
	opts = append(opts, sbshbuilder.WithLogLevel(resolveTtyLogLevel(spec.Tty)))
	if argv := workloadArgv(spec); len(argv) > 0 {
		opts = append(opts, sbshbuilder.WithCommand(argv))
	}

	terminalSpec, err := sbshbuilder.BuildTerminalSpec(ctx, logger, attachableTTYDir, opts...)
	if err != nil {
		return nil, fmt.Errorf("build kuketty terminal spec: %w", err)
	}

	// Anchor sbsh's metadata.json inside the per-container tty bind mount
	// (issue #672). sbsh writes metadata.json via an atomic
	// create-temp-then-rename that needs write+exec on the holding directory at
	// every point in the terminal lifecycle — create AND close. The legacy
	// layout (RunPath/terminals/<id>/, mode 0700, owned by the creating uid)
	// denied the close-time write whenever the closing uid differed from the
	// creating uid. We pin MetadataDir to attachableTTYDir — the directory the
	// daemon bind-mounts and attachablePostCreateChown re-owns to the resolved
	// container uid — so the holding dir is always the kukeon-owned, host-
	// visible tty dir. Setting it explicitly (rather than leaning on sbsh's
	// implicit "MetadataDir = dirname(LogFile) when all three artifact paths are
	// caller-supplied" derivation) keeps metadata.json in the bind mount even
	// when an operator pins Tty.LogFile outside it, and survives a future
	// refactor that drops one of the WithSocketFile/WithCaptureFile/WithLogFile
	// options (which would otherwise empty MetadataDir and resurrect the
	// terminals/<id>/ layout). No basename collision: kuketty's own rendered doc
	// is kuketty-metadata.json; sbsh writes metadata.json.
	terminalSpec.MetadataDir = attachableTTYDir

	return terminalSpec, nil
}

// workloadArgv reconstructs the resolved workload argv the daemon stamped into
// Spec.Command / Spec.Args. An empty Command means the args-wrap captured no
// resolved Process.Args (image with no ENTRYPOINT/CMD and no override); the
// caller then leaves sbsh's inline-builder default (/bin/bash -i) in place.
func workloadArgv(spec v1beta1.ContainerSpec) []string {
	if spec.Command == "" {
		return nil
	}
	return append([]string{spec.Command}, spec.Args...)
}

// resolveTtyLogFile returns the in-container path kuketty's slog output lands
// at: the operator-pinned Tty.LogFile when set, else the daemon-controlled
// default peer to the capture inode inside the tty bind mount. The always-on
// invariant holds either way — the return is never empty, so kuketty's
// openTerminalLogger never falls through to the discard logger (#599).
func resolveTtyLogFile(t *v1beta1.ContainerTty) string {
	if t != nil && t.LogFile != "" {
		return t.LogFile
	}
	return attachableKukettyLogPath
}

// resolveTtyLogLevel reads the log level the daemon resolved and stamped onto
// Tty.LogLevel. The "info" fallback is a defensive guard against a malformed
// doc (a nil Tty or empty level) — not daemon-side defaulting: the daemon owns
// the per-container → server-config → "info" chain in writeKukettyDoc and
// always stamps a non-empty value. sbsh's NewFileLogger rejects an empty level.
func resolveTtyLogLevel(t *v1beta1.ContainerTty) string {
	if t != nil && t.LogLevel != "" {
		return t.LogLevel
	}
	return "info"
}

// ttyStagesToExecSteps maps the cell's inline Tty.OnInit entries into sbsh's
// api.ExecStep slice. Only runOn: start (and absent — the default) stages are
// forwarded; runOn: create stages are routed into kuketty's pre-Serve executor
// (see createStages / processStages) and skipped here. Each forwarded stage
// carries only Script today; sbsh's ExecStep also supports Env, but the cell
// schema does not surface it yet (a future TtyStage knob lands here without
// touching the transform). Issue #635.
func ttyStagesToExecSteps(in []v1beta1.TtyStage) []sbshapi.ExecStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]sbshapi.ExecStep, 0, len(in))
	for _, s := range in {
		if s.RunOn == v1beta1.RunOnCreate {
			continue
		}
		out = append(out, sbshapi.ExecStep{Script: s.Script})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// createStages returns the runOn: create stages from the container's
// Tty.OnInit, in declaration order, paired with their index within the full
// OnInit slice so the pre-Serve executor and the (phase B) status reporter
// share one stable identity. Returns nil when the tty block is absent or
// declares no create stages. Issue #635.
func createStages(tty *v1beta1.ContainerTty) []indexedStage {
	if tty == nil {
		return nil
	}
	var out []indexedStage
	for i, s := range tty.OnInit {
		if s.RunOn == v1beta1.RunOnCreate {
			out = append(out, indexedStage{Index: i, Stage: s})
		}
	}
	return out
}

// indexedStage pairs a create stage with its position in the container's full
// OnInit slice — the identity carried into ContainerStatus.Stages (#635). The
// run-once "done" key (e.g. a content hash) is settled in phase C (#690).
type indexedStage struct {
	Index int
	Stage v1beta1.TtyStage
}

// modeIfGroupSet returns the octal-mode string only when the kukeon group GID
// is configured; otherwise empty so kuketty applies the OS-default
// (umask-clipped) mode on the inode the mode applies to (socket, capture file,
// log file). Matches the legacy 0o600-owner-only fallback when no kukeon group
// exists.
func modeIfGroupSet(gid int, mode string) string {
	if gid > 0 {
		return mode
	}
	return ""
}
