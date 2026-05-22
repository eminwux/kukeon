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
	"os"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestProcessStages_EmptyIsNoOp(t *testing.T) {
	statuses, err := processStages(context.Background(), nil, discardLogger())
	if err != nil {
		t.Fatalf("nil stages should be a no-op, got %v", err)
	}
	if statuses != nil {
		t.Errorf("nil stages should report nil statuses, got %+v", statuses)
	}
	statuses, err = processStages(context.Background(), []indexedStage{}, discardLogger())
	if err != nil {
		t.Fatalf("empty stages should be a no-op, got %v", err)
	}
	if statuses != nil {
		t.Errorf("empty stages should report nil statuses, got %+v", statuses)
	}
}

// TestProcessStages_RunsScriptsInOrder confirms the executor invokes each
// create stage's Script: two stages each touch a marker file, asserted present
// after processStages returns, and reports each as StageDone carrying its
// declaration-order Index.
func TestProcessStages_RunsScriptsInOrder(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	stages := []indexedStage{
		{Index: 1, Stage: v1beta1.TtyStage{Script: "touch " + a, RunOn: v1beta1.RunOnCreate}},
		{Index: 3, Stage: v1beta1.TtyStage{Script: "touch " + b, RunOn: v1beta1.RunOnCreate}},
	}
	statuses, err := processStages(context.Background(), stages, discardLogger())
	if err != nil {
		t.Fatalf("processStages: %v", err)
	}
	for _, f := range []string{a, b} {
		if _, statErr := os.Stat(f); statErr != nil {
			t.Errorf("expected marker %q to exist: %v", f, statErr)
		}
	}
	want := []setupstatus.Stage{
		{Index: 1, State: setupstatus.StageDone},
		{Index: 3, State: setupstatus.StageDone},
	}
	if len(statuses) != len(want) {
		t.Fatalf("want %d statuses, got %d: %+v", len(want), len(statuses), statuses)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Errorf("status[%d] = %+v, want %+v", i, statuses[i], want[i])
		}
	}
}

// TestProcessStages_FailurePropagates: a failing create stage returns
// errRequiredStageFailed (the required-failure contract) and stops before the
// next stage runs. The returned statuses record the completed stage as
// StageDone and the failing stage as StageFailed; the never-run stage is absent.
func TestProcessStages_FailurePropagates(t *testing.T) {
	dir := t.TempDir()
	after := filepath.Join(dir, "after")
	stages := []indexedStage{
		{Index: 0, Stage: v1beta1.TtyStage{Script: "true", RunOn: v1beta1.RunOnCreate}},
		{Index: 1, Stage: v1beta1.TtyStage{Script: "exit 7", RunOn: v1beta1.RunOnCreate}},
		{Index: 2, Stage: v1beta1.TtyStage{Script: "touch " + after, RunOn: v1beta1.RunOnCreate}},
	}
	statuses, err := processStages(context.Background(), stages, discardLogger())
	if err == nil {
		t.Fatal("processStages: expected error for failing stage, got nil")
	}
	if !errors.Is(err, errRequiredStageFailed) {
		t.Errorf("error = %v, want errRequiredStageFailed", err)
	}
	if _, statErr := os.Stat(after); statErr == nil {
		t.Errorf("stage after the failing one ran; expected execution to stop")
	}
	if len(statuses) != 2 {
		t.Fatalf("want 2 statuses (done + failed), got %d: %+v", len(statuses), statuses)
	}
	if statuses[0] != (setupstatus.Stage{Index: 0, State: setupstatus.StageDone}) {
		t.Errorf("status[0] = %+v, want done stage at index 0", statuses[0])
	}
	if statuses[1].Index != 1 || statuses[1].State != setupstatus.StageFailed || statuses[1].Error == "" {
		t.Errorf("status[1] = %+v, want failed stage at index 1 with error detail", statuses[1])
	}
}

// TestProcessStages_CapturesOutputInError: a failing stage folds its combined
// output into the returned error and into the StageFailed status's Error so the
// log line and the reported status are both actionable.
func TestProcessStages_CapturesOutputInError(t *testing.T) {
	stages := []indexedStage{
		{Index: 0, Stage: v1beta1.TtyStage{Script: "echo boom >&2; exit 1", RunOn: v1beta1.RunOnCreate}},
	}
	statuses, err := processStages(context.Background(), stages, discardLogger())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := err.Error(); !contains(got, "boom") {
		t.Errorf("error %q does not carry the stage's output", got)
	}
	if len(statuses) != 1 {
		t.Fatalf("want 1 status, got %d: %+v", len(statuses), statuses)
	}
	if statuses[0].State != setupstatus.StageFailed || !contains(statuses[0].Error, "boom") {
		t.Errorf("status[0] = %+v, want failed status carrying the stage output", statuses[0])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
