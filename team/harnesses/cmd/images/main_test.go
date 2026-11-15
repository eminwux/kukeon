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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCatalogWriteCheckAndDrift(t *testing.T) {
	original, err := os.ReadFile("../../codex/harness.yaml")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "harnesses/codex")
	if err = os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "harness.yaml"), original, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	filename := filepath.Join(dir, "images.yaml")
	args := []string{"catalog", "--registry=", "--tag=dev"}
	if err = run(args); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(before, []byte("image: codex:dev")) || bytes.Contains(before, []byte("image: /")) {
		t.Fatalf("local names: %s", before)
	}
	if err = run(append(args, "--check")); err != nil {
		t.Fatal(err)
	}
	if err = run([]string{"catalog", "--registry=", "--tag=other", "--check"}); err == nil ||
		!strings.Contains(err.Error(), "harnesses/codex/images.yaml") ||
		!strings.Contains(err.Error(), "commit/merge") {
		t.Fatalf("drift error: %v", err)
	}
	after, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("check mutated catalog")
	}
	if err = os.Remove(filename); err != nil {
		t.Fatal(err)
	}
	if err = run(append(args, "--check")); err == nil {
		t.Fatal("missing catalog passed")
	}
	if _, err = os.Stat(filename); !os.IsNotExist(err) {
		t.Fatal("check wrote missing catalog")
	}
	if err = run([]string{"catalog", "--registry=ghcr.io/example", "--tag=v2.3.4"}); err != nil {
		t.Fatal(err)
	}
	after, err = os.ReadFile(filename)
	if err != nil || !bytes.Contains(after, []byte("image: ghcr.io/example/codex:v2.3.4")) {
		t.Fatal("published generation")
	}
}
