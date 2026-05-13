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

package shared

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestRequireRoot_AsRoot(t *testing.T) {
	prev := geteuid
	t.Cleanup(func() { geteuid = prev })
	geteuid = func() int { return 0 }

	if err := RequireRoot("kuke init"); err != nil {
		t.Fatalf("RequireRoot returned error when euid=0: %v", err)
	}
}

func TestRequireRoot_NonRoot(t *testing.T) {
	prev := geteuid
	t.Cleanup(func() { geteuid = prev })
	geteuid = func() int { return 1000 }

	err := RequireRoot("kuke init")
	if err == nil {
		t.Fatal("RequireRoot returned nil when euid=1000; want error")
	}
	if !errors.Is(err, errdefs.ErrMustRunAsRoot) {
		t.Fatalf("RequireRoot error does not wrap ErrMustRunAsRoot: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "kuke init") {
		t.Errorf("RequireRoot error does not name the subcommand: %q", msg)
	}
	if !strings.Contains(msg, "sudo") {
		t.Errorf("RequireRoot error does not suggest sudo: %q", msg)
	}
}
