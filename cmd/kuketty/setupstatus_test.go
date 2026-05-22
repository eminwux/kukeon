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
	"testing"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
)

func TestSetupStatusHandler_ReturnsStoredReposAndStages(t *testing.T) {
	repos := []setupstatus.Repo{
		{Name: "a", Target: "/work/a", State: setupstatus.StateCloned, Commit: "deadbeef"},
		{Name: "b", Target: "/work/b", State: setupstatus.StateFailed, Error: "boom"},
	}
	stages := []setupstatus.Stage{
		{Index: 0, State: setupstatus.StageDone},
		{Index: 2, State: setupstatus.StageFailed, Error: "stage 2: exit 1"},
	}
	h := &setupStatusHandler{repos: repos, stages: stages}

	var reply setupstatus.Reply
	if err := h.GetSetupStatus(setupstatus.Args{}, &reply); err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if len(reply.Repos) != 2 {
		t.Fatalf("want 2 repos, got %d: %+v", len(reply.Repos), reply.Repos)
	}
	if reply.Repos[0] != repos[0] || reply.Repos[1] != repos[1] {
		t.Errorf("reply.Repos = %+v, want %+v", reply.Repos, repos)
	}
	if len(reply.Stages) != 2 {
		t.Fatalf("want 2 stages, got %d: %+v", len(reply.Stages), reply.Stages)
	}
	if reply.Stages[0] != stages[0] || reply.Stages[1] != stages[1] {
		t.Errorf("reply.Stages = %+v, want %+v", reply.Stages, stages)
	}
}

func TestSetupStatusHandler_EmptyIsEmptyReply(t *testing.T) {
	h := &setupStatusHandler{repos: nil, stages: nil}

	var reply setupstatus.Reply
	if err := h.GetSetupStatus(setupstatus.Args{}, &reply); err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if len(reply.Repos) != 0 {
		t.Errorf("want empty repos, got %+v", reply.Repos)
	}
	if len(reply.Stages) != 0 {
		t.Errorf("want empty stages, got %+v", reply.Stages)
	}
}

func TestSetupStatusOption_RegistersVerb(t *testing.T) {
	// The option splat must always register exactly one handler under the
	// agreed service name, even when there is nothing to report — kukeond's
	// pull is a single code path that expects the verb to exist.
	opts := setupStatusOption(nil, nil)
	if len(opts) != 1 {
		t.Fatalf("want exactly one server option, got %d", len(opts))
	}
}
