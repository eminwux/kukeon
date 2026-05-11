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
	"reflect"
	"testing"
)

func TestParseArgs_Happy(t *testing.T) {
	got, err := parseArgs([]string{"--", "/bin/sh", "-c", "echo hello"})
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	want := []string{"/bin/sh", "-c", "echo hello"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("workload = %v, want %v", got, want)
	}
}

func TestParseArgs_MissingSeparator(t *testing.T) {
	_, err := parseArgs([]string{"/bin/sh"})
	if err == nil {
		t.Fatalf("parseArgs returned nil, want usage error")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("parseArgs error = %T, want *usageError", err)
	}
}

func TestParseArgs_EmptyWorkloadAfterSeparator(t *testing.T) {
	_, err := parseArgs([]string{"--"})
	if err == nil {
		t.Fatalf("parseArgs returned nil, want usage error")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("parseArgs error = %T, want *usageError", err)
	}
}
