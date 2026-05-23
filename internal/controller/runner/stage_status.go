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
	"crypto/sha256"
	"encoding/hex"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// stageHashLen is the prefix of the SHA-256 hex digest kept as the stage's
// content Hash. 16 hex chars (64 bits of entropy) keeps the persisted
// ContainerStatus.Stages compact in `kuke get -o yaml` while leaving
// collision probability well below the population of create stages any cell
// will ever carry. Same byte-budget rationale as the metadata-dir basename
// in internal/util/fs/metadata.go.
const stageHashLen = 16

// stageContentHash returns the run-once "done" key for a TtyStage. The key
// is a SHA-256 over the stage's Script and RunOn (delimited so a stage with
// the script "a\x00b" can never collide with two scripts "a" / "b"), truncated
// to stageHashLen hex characters. Same hash for the same content, anchored on
// the (Index, Hash) tuple by the merge below.
//
// The hash spans Script + RunOn rather than Script alone so a stage that flips
// from runOn: start to runOn: create (a real edit to the create-stage set) is
// treated as a content change, dropping any prior done record at the same
// Index instead of silently carrying it forward.
func stageContentHash(stage v1beta1.TtyStage) string {
	h := sha256.New()
	// NUL-delimited fields so concatenated payloads can never alias —
	// {"a", "b"} and {"a\x00b", ""} hash to distinct digests.
	_, _ = h.Write([]byte(stage.Script))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(stage.RunOn))
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:stageHashLen]
}

// mergeStageStatuses returns the durable per-create-stage status for the
// container, computed from the current spec, the prior persisted Stages
// snapshot (carried from the previous populate, possibly across a stop/start
// cycle), and the live pull from kuketty (when available).
//
// For each runOn: create stage in spec.Tty.OnInit (anchored on its position
// in the full OnInit slice — the Index identity cmd/kuketty's createStages
// emits — and stamped with the current spec's content Hash), the merge picks:
//
//   - the live entry at the matching Index, when present: the just-completed
//     run is authoritative and its Hash is restamped from the current spec
//     so a wire payload that omits Hash (kuketty does not stamp it today)
//     still surfaces the daemon-computed key for the next populate's merge.
//   - otherwise, the prior persisted entry at the same Index — but only when
//     its Hash still matches the current spec's stage Hash and its State is
//     "done": this is the run-once carry-forward across stop/start. A failed
//     prior entry does not carry (the next boot gets a fresh chance), and a
//     prior entry whose Hash no longer matches the current stage is dropped
//     so an edited create stage re-runs on the next boot.
//   - otherwise, nothing — the stage has not run yet for the current content.
//
// Returns nil rather than an empty slice so a container with no create stages
// (or one whose every prior done was invalidated by edits) round-trips through
// the omitempty wire/YAML shape unchanged. Phase C1 (#690).
func mergeStageStatuses(
	spec intmodel.ContainerSpec,
	prior []intmodel.StageStatus,
	live []intmodel.StageStatus,
) []intmodel.StageStatus {
	if spec.Tty == nil {
		return nil
	}
	liveByIndex := make(map[int]intmodel.StageStatus, len(live))
	for _, s := range live {
		liveByIndex[s.Index] = s
	}
	priorByIndex := make(map[int]intmodel.StageStatus, len(prior))
	for _, s := range prior {
		priorByIndex[s.Index] = s
	}

	var out []intmodel.StageStatus
	for i, stage := range spec.Tty.OnInit {
		if stage.RunOn != v1beta1.RunOnCreate {
			continue
		}
		hash := stageContentHash(toV1beta1Stage(stage))
		// Live wins; restamp Hash from the current spec (kuketty does not
		// stamp it on the wire today; see setupstatus.Stage doc).
		if liveStat, ok := liveByIndex[i]; ok {
			liveStat.Hash = hash
			out = append(out, liveStat)
			continue
		}
		// Prior done entry carries forward only when its Hash still matches
		// the current spec's stage Hash — an edited stage drops here.
		if priorStat, ok := priorByIndex[i]; ok &&
			priorStat.Hash == hash &&
			priorStat.State == setupstatus.StageDone {
			out = append(out, priorStat)
			continue
		}
		// Otherwise: pending on the next boot — leave the slot empty so the
		// (phase C2, #737) render gate sees the missing record and the
		// stage runs again.
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// toV1beta1Stage adapts an internal modelhub.TtyStage to the v1beta1 shape
// the hash helper consumes. The two structs are byte-equivalent — both carry
// Script + RunOn — and the hash function lives against v1beta1 so cmd/kuketty
// (which only imports v1beta1) could share it in a later phase without
// reaching into the internal model.
func toV1beta1Stage(in intmodel.TtyStage) v1beta1.TtyStage {
	return v1beta1.TtyStage{Script: in.Script, RunOn: in.RunOn}
}
