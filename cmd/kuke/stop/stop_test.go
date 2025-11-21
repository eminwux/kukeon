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

package stop_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/stop"
	"github.com/spf13/cobra"
)

func TestNewStopCmdMetadata(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T, cmd *cobra.Command)
	}{
		{
			name: "use statement",
			check: func(t *testing.T, cmd *cobra.Command) {
				if cmd.Use != "stop" {
					t.Fatalf("expected Use to be %q, got %q", "stop", cmd.Use)
				}
			},
		},
		{
			name: "short description",
			check: func(t *testing.T, cmd *cobra.Command) {
				expected := "Stop Kukeon resources (cell, container)"
				if cmd.Short != expected {
					t.Fatalf("expected Short to be %q, got %q", expected, cmd.Short)
				}
			},
		},
		{
			name: "run invokes help",
			check: func(t *testing.T, cmd *cobra.Command) {
				buf := &bytes.Buffer{}
				cmd.SetOut(buf)
				cmd.SetErr(buf)

				cmd.Run(cmd, nil)

				output := buf.String()
				if !strings.Contains(output, "Usage:") {
					t.Fatalf("expected help output to contain %q, got %q", "Usage:", output)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := stop.NewStopCmd()
			tt.check(t, cmd)
		})
	}
}

func TestNewStopCmdRegistersSubcommands(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "cell"},
		{name: "container"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := stop.NewStopCmd()
			if findSubCommand(cmd, tt.name) == nil {
				t.Fatalf("expected %q subcommand to be registered", tt.name)
			}
		})
	}
}

func findSubCommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, sc := range cmd.Commands() {
		if sc.Name() == name || sc.HasAlias(name) {
			return sc
		}
	}
	return nil
}
