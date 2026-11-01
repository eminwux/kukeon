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
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/plugins/content/local"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	"github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

type envTestImage struct {
	containerd.Image

	store      content.Store
	descriptor imagespec.Descriptor
}

func (i envTestImage) Config(context.Context) (imagespec.Descriptor, error) { return i.descriptor, nil }
func (i envTestImage) ContentStore() content.Store                          { return i.store }

type envTestContainer struct {
	containerd.Container

	image containerd.Image
}

func (c envTestContainer) Image(context.Context) (containerd.Image, error) { return c.image, nil }

func TestFreshProcessEnv_ReplacesStaleRuntimeOnlyEnv(t *testing.T) {
	ctx := context.Background()
	store, err := local.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(
		imagespec.Image{
			Config: imagespec.ImageConfig{Env: []string{"IMAGE_ONLY=kept", "VALUE=image", "PATH=/image/bin"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	descriptor := imagespec.Descriptor{
		MediaType: imagespec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(blob),
		Size:      int64(len(blob)),
	}
	if err = content.WriteBlob(ctx, store, "env-test", bytes.NewReader(blob), descriptor); err != nil {
		t.Fatal(err)
	}
	container := envTestContainer{image: envTestImage{store: store, descriptor: descriptor}}
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
	for _, runtime := range [][]string{{"VALUE=first", "RUNTIME_ONLY=first"}, {"VALUE=second", "RUNTIME_ONLY=second"}, nil} {
		invocation := authored
		if len(runtime) > 0 {
			invocation.Env = runtime
		}
		if err = WithFreshProcessEnv(container, invocation, "")(ctx, nil, nil, spec); err != nil {
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
