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
	"errors"
	"strings"
	"testing"
)

func TestRequireRoot_AsRoot(t *testing.T) {
	restore := SetGeteuidForTesting(func() int { return 0 })
	t.Cleanup(restore)

	if err := requireRoot(); err != nil {
		t.Fatalf("requireRoot returned error when euid=0: %v", err)
	}
}

func TestRequireRoot_NonRoot(t *testing.T) {
	restore := SetGeteuidForTesting(func() int { return 1000 })
	t.Cleanup(restore)

	err := requireRoot()
	if err == nil {
		t.Fatal("requireRoot returned nil when euid=1000; want error")
	}
	if !errors.Is(err, ErrMustRunAsRoot) {
		t.Fatalf("requireRoot error does not wrap ErrMustRunAsRoot: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "sudo") {
		t.Errorf("requireRoot error does not suggest sudo: %q", msg)
	}
}
