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

//nolint:testpackage // exercises the unexported merge/hash helpers
package runner

import (
	"testing"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestStageContentHash_Deterministic locks the run-once "done" key: the same
// stage content always hashes to the same digest, and any change to Script or
// RunOn flips it. The merge gate downstream uses this — a stable digest is
// what makes prior done records survive stop/start; a changing digest is what
// drops them on edit. Phase C1 (#690).
func TestStageContentHash_Deterministic(t *testing.T) {
	stage := v1beta1.TtyStage{Script: "echo hi", RunOn: v1beta1.RunOnCreate}
	first := stageContentHash(stage)
	second := stageContentHash(stage)
	if first != second {
		t.Errorf("stageContentHash not deterministic: %q vs %q", first, second)
	}
	if len(first) != stageHashLen {
		t.Errorf("stageContentHash len = %d, want %d", len(first), stageHashLen)
	}
}

// TestStageContentHash_DetectsEdits is the load-bearing edit-detection
// guarantee the AC restates: an edited stage's hash must differ so the
// merge drops the prior done. Covers both Script edits and RunOn flips
// (a stage that turns from runOn: start to runOn: create is a real edit
// to the create-stage set — see toV1beta1Stage / stageContentHash).
func TestStageContentHash_DetectsEdits(t *testing.T) {
	base := v1beta1.TtyStage{Script: "echo hi", RunOn: v1beta1.RunOnCreate}
	editedScript := v1beta1.TtyStage{Script: "echo bye", RunOn: v1beta1.RunOnCreate}
	editedRunOn := v1beta1.TtyStage{Script: "echo hi", RunOn: v1beta1.RunOnStart}

	baseHash := stageContentHash(base)
	if stageContentHash(editedScript) == baseHash {
		t.Errorf("stageContentHash collided after Script edit")
	}
	if stageContentHash(editedRunOn) == baseHash {
		t.Errorf("stageContentHash collided after RunOn edit")
	}
}

// TestStageContentHash_NoFieldAliasing pins the NUL-delimited concat: a Script
// whose tail aliases the RunOn field (or vice versa) must not collide with the
// "natural" stage that carries the same bytes split across the two fields.
// Without the delimiter, {"a", "b"} and {"ab", ""} would hash to the same
// digest — a real source of merge mis-carry once kuketty grows scripts that
// contain literal "create"/"start" suffixes.
func TestStageContentHash_NoFieldAliasing(t *testing.T) {
	concat := v1beta1.TtyStage{Script: "abcreate"}
	split := v1beta1.TtyStage{Script: "ab", RunOn: "create"}
	if stageContentHash(concat) == stageContentHash(split) {
		t.Errorf("stageContentHash aliased on field-boundary concat: %q vs %q",
			stageContentHash(concat), stageContentHash(split))
	}
}

// TestMergeStageStatuses_Cases walks the merge contract documented on
// mergeStageStatuses — the (Index, Hash)-keyed carry of done records across
// stop/start, the live-pull supersedes path, the edited-hash drop, and the
// reset-on-recreate (empty prior). Each case mirrors one AC item from #690.
func TestMergeStageStatuses_Cases(t *testing.T) {
	stage0 := intmodel.TtyStage{Script: "git clone", RunOn: v1beta1.RunOnCreate}
	stage0Edited := intmodel.TtyStage{Script: "git clone --depth 1", RunOn: v1beta1.RunOnCreate}
	startStage := intmodel.TtyStage{Script: "echo hello", RunOn: v1beta1.RunOnStart}
	stage2 := intmodel.TtyStage{Script: "npm ci", RunOn: v1beta1.RunOnCreate}

	hash0 := stageContentHash(toV1beta1Stage(stage0))
	hash2 := stageContentHash(toV1beta1Stage(stage2))
	// The post-edit hash isn't directly asserted (mergeStageStatuses recomputes
	// from the spec internally) — what matters in the edit case is that the
	// prior entry carries the pre-edit Hash, which no longer matches.
	if stageContentHash(toV1beta1Stage(stage0Edited)) == hash0 {
		t.Fatalf("stage0Edited should hash differently than stage0; got identical")
	}

	specWithTwoCreate := intmodel.ContainerSpec{
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{stage0, startStage, stage2},
		},
	}
	specWithEditedStage0 := intmodel.ContainerSpec{
		Tty: &intmodel.ContainerTty{
			OnInit: []intmodel.TtyStage{stage0Edited, startStage, stage2},
		},
	}

	tests := []struct {
		name  string
		spec  intmodel.ContainerSpec
		prior []intmodel.StageStatus
		live  []intmodel.StageStatus
		want  []intmodel.StageStatus
	}{
		{
			// AC: "Done-record (State == 'done') survives kuke stop -> kuke start".
			// Live pull is empty (container not Ready while stopped); the prior
			// done records carry forward verbatim because Hash still matches.
			name: "survives stop: live empty, prior done at matching hash carries",
			spec: specWithTwoCreate,
			prior: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0},
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
			live: nil,
			want: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0},
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
		},
		{
			// AC: "Done-record resets on kuke delete -> recreate: Stages is
			// empty on the new instance". The recreate path arrives here with
			// no prior status (fresh ContainerStatus). Live empty too (not
			// Ready yet) yields nil — phase C2's render gate then runs every
			// stage.
			name:  "reset on recreate: no prior, no live yields nil",
			spec:  specWithTwoCreate,
			prior: nil,
			live:  nil,
			want:  nil,
		},
		{
			// AC: "Edited create stage (new content hash) drops the prior
			// done-record on the next populate." Index 0's content changed
			// (script edit -> new Hash); the prior done at Index 0 no longer
			// matches and drops, while the untouched Index 2 carries forward.
			name: "edited stage drops prior done; sibling carries",
			spec: specWithEditedStage0,
			prior: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0}, // stale
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
			live: nil,
			want: []intmodel.StageStatus{
				// Index 0 absent — it will run again on the next boot.
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
		},
		{
			// Live wins, and Hash is restamped from the current spec because
			// kuketty does not stamp it on the wire today. A live entry with
			// no Hash must still surface the daemon-computed key for the
			// next populate's merge.
			name: "live wins and Hash is restamped from spec",
			spec: specWithTwoCreate,
			prior: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0},
			},
			live: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone}, // wire payload has no Hash
				{Index: 2, State: setupstatus.StageFailed, Error: "boom"},
			},
			want: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0},
				{Index: 2, State: setupstatus.StageFailed, Error: "boom", Hash: hash2},
			},
		},
		{
			// Failed prior records do not carry — only done. Phase C2's gate
			// must give a failed stage a fresh chance on restart, not gate it
			// out forever on the failed marker.
			name: "prior failed does not carry across stop",
			spec: specWithTwoCreate,
			prior: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageFailed, Error: "boom", Hash: hash0},
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
			live: nil,
			want: []intmodel.StageStatus{
				// Index 0 dropped: failed never carries.
				{Index: 2, State: setupstatus.StageDone, Hash: hash2},
			},
		},
		{
			// Container declares no Tty (no create stages) -> nil, regardless
			// of prior junk. The merge is anchored on spec.Tty.OnInit; an
			// absent Tty block means no create stages exist in the live spec.
			name: "nil tty yields nil",
			spec: intmodel.ContainerSpec{Tty: nil},
			prior: []intmodel.StageStatus{
				{Index: 0, State: setupstatus.StageDone, Hash: hash0},
			},
			live: nil,
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mergeStageStatuses(tt.spec, tt.prior, tt.live)
			if len(got) != len(tt.want) {
				t.Fatalf("len(got) = %d, want %d (got=%+v want=%+v)",
					len(got), len(tt.want), got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("stage[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestMergeStageStatuses_AnchorsOnIndexInFullOnInit guards against a regression
// where the merge would re-index create-stages contiguously (0, 1, ...) instead
// of using their position in the full OnInit slice. The setupstatus wire
// protocol and cmd/kuketty's createStages both emit Index relative to
// Tty.OnInit, so a contiguous re-index would silently mis-align prior records
// against live entries.
func TestMergeStageStatuses_AnchorsOnIndexInFullOnInit(t *testing.T) {
	startStage := intmodel.TtyStage{Script: "echo s", RunOn: v1beta1.RunOnStart}
	createStage := intmodel.TtyStage{Script: "npm ci", RunOn: v1beta1.RunOnCreate}

	// Create stage sits at OnInit index 1, not 0 — the start stage at 0 must
	// not consume the create-slot.
	spec := intmodel.ContainerSpec{
		Tty: &intmodel.ContainerTty{OnInit: []intmodel.TtyStage{startStage, createStage}},
	}
	hash1 := stageContentHash(toV1beta1Stage(createStage))

	prior := []intmodel.StageStatus{
		{Index: 1, State: setupstatus.StageDone, Hash: hash1},
	}
	got := mergeStageStatuses(spec, prior, nil)
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1 (got=%+v)", len(got), got)
	}
	if got[0].Index != 1 || got[0].Hash != hash1 || got[0].State != setupstatus.StageDone {
		t.Errorf("stage[0] = %+v, want Index=1 Hash=%q State=done", got[0], hash1)
	}
}
