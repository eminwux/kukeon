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

package ctr

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestFreshProcessEnv_ReplacesStaleRuntimeOnlyEnv(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KUKEON_TEST_SECRET", "secret")
	authored := intmodel.ContainerSpec{
		ID:       "work",
		Env:      []string{"VALUE=spec"},
		CellName: "cell",
		Secrets:  []intmodel.ContainerSecret{{Name: "SECRET", FromEnv: "KUKEON_TEST_SECRET"}},
	}
	process := &runtimespec.Process{
		Args: []string{"/.kukeon/bin/kuketty"},
		Cwd:  "/workspace",
		User: runtimespec.User{UID: 1000},
		Env:  []string{"VALUE=old", "STALE=old"},
	}
	spec := &runtimespec.Spec{Process: process, Mounts: []runtimespec.Mount{{Destination: "/kept"}}}
	// Apply the same capture option used immediately after WithImageConfig.
	spec.Process.Env = []string{"IMAGE_ONLY=kept", "VALUE=image", "PATH=/image/bin"}
	if err := withOriginalImageEnv(ctx, nil, nil, spec); err != nil {
		t.Fatal(err)
	}
	spec.Process.Env = []string{"VALUE=old", "STALE=old"}
	for _, runtime := range [][]string{{"VALUE=first", "RUNTIME_ONLY=first"}, {"VALUE=second", "RUNTIME_ONLY=second"}, nil} {
		invocation := authored
		if len(runtime) > 0 {
			invocation.Env = runtime
		}
		if err := WithFreshProcessEnv(invocation, "")(ctx, nil, nil, spec); err != nil {
			t.Fatal(err)
		}
		got := make(map[string]string)
		for _, entry := range spec.Process.Env {
			key, value, _ := strings.Cut(entry, "=")
			got[key] = value
		}
		value, addition := "spec", ""
		if len(runtime) > 0 {
			value = runtime[0][6:]
			addition = value
		}
		if got["VALUE"] != value || got["RUNTIME_ONLY"] != addition || got["STALE"] != "" {
			t.Fatalf("stale env: %v", got)
		}
		if _, exists := got["STALE"]; exists {
			t.Fatal("stale key remains")
		}
		if len(runtime) == 0 {
			if _, exists := got["RUNTIME_ONLY"]; exists {
				t.Fatal("runtime-only key remains")
			}
		}
		if got["IMAGE_ONLY"] != "kept" || got["SECRET"] != "secret" || got["KUKEON_CELL_NAME"] != "cell" {
			t.Fatalf("lost base env: %v", got)
		}
		if !reflect.DeepEqual(spec.Process.Args, []string{"/.kukeon/bin/kuketty"}) ||
			spec.Process.Cwd != "/workspace" ||
			spec.Process.User.UID != 1000 ||
			spec.Mounts[0].Destination != "/kept" {
			t.Fatalf("modified non-env settings: %+v", spec)
		}
	}
	if !reflect.DeepEqual(authored.Env, []string{"VALUE=spec"}) {
		t.Fatalf("mutated authored env: %v", authored.Env)
	}
}

func TestFreshProcessEnv_LegacyBaseline(t *testing.T) {
	ctx := context.Background()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{Env: []string{"IMAGE_ONLY=original", "VALUE=old", "UNKNOWN_HISTORICAL=kept"}},
	}
	for _, value := range []string{"current", "spec"} {
		if err := WithFreshProcessEnv(intmodel.ContainerSpec{Env: []string{"VALUE=" + value}}, "")(ctx, nil, nil, spec); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.Join(spec.Process.Env, "\n"), "VALUE="+value) {
			t.Fatal(spec.Process.Env)
		}
		if !strings.Contains(strings.Join(spec.Process.Env, "\n"), "UNKNOWN_HISTORICAL=kept") {
			t.Fatal("legacy baseline lost")
		}
	}
	var baseline []string
	if err := json.Unmarshal([]byte(spec.Annotations[originalImageEnvAnnotation]), &baseline); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline, []string{"IMAGE_ONLY=original", "VALUE=old", "UNKNOWN_HISTORICAL=kept"}) {
		t.Fatal(baseline)
	}
}

func TestFreshProcessEnv_EmptyOriginalBaseline(t *testing.T) {
	ctx := context.Background()
	spec := &runtimespec.Spec{Process: &runtimespec.Process{}}
	if err := withOriginalImageEnv(ctx, nil, nil, spec); err != nil {
		t.Fatal(err)
	}
	if spec.Annotations[originalImageEnvAnnotation] != "[]" {
		t.Fatal("empty baseline must be explicit")
	}
	spec.Process.Env = []string{"RUNTIME_ONLY=stale"}
	if err := WithFreshProcessEnv(intmodel.ContainerSpec{}, "")(ctx, nil, nil, spec); err != nil {
		t.Fatal(err)
	}
	for _, env := range spec.Process.Env {
		if strings.HasPrefix(env, "RUNTIME_ONLY=") {
			t.Fatal("stale runtime-only env remains")
		}
	}
}
