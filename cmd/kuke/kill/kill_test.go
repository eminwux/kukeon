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

package kill_test

import (
	"strings"
	"testing"

	killcmd "github.com/eminwux/kukeon/cmd/kuke/kill"
	"github.com/spf13/cobra"
)

func TestNewKillCmdMetadata(t *testing.T) {
	cmd := killcmd.NewKillCmd()

	if cmd.Use != "kill [name]" {
		t.Fatalf("unexpected Use: got %q want %q", cmd.Use, "kill [name]")
	}

	if cmd.Short != "Kill Kukeon resources (cell, container)" {
		t.Fatalf("unexpected Short: got %q want %q", cmd.Short, "Kill Kukeon resources (cell, container)")
	}

	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be configured for subcommand completion")
	}
}

func TestNewKillCmdRegistersSubcommands(t *testing.T) {
	cmd := killcmd.NewKillCmd()

	subcommands := cmd.Commands()
	if len(subcommands) != 2 {
		t.Fatalf("expected 2 subcommands, got %d", len(subcommands))
	}

	found := map[string]bool{}
	for _, sub := range subcommands {
		found[sub.Name()] = true
	}

	for _, name := range []string{"cell", "container"} {
		if !found[name] {
			t.Fatalf("expected %q subcommand to be registered", name)
		}
	}
}

func TestCompleteKillSubcommands(t *testing.T) {
	cmd := killcmd.NewKillCmd()

	t.Run("no prefix returns all subcommands", func(t *testing.T) {
		results, directive := cmd.ValidArgsFunction(cmd, []string{}, "")
		if directive != cobra.ShellCompDirectiveNoFileComp {
			t.Fatalf("unexpected directive %v", directive)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("prefix filters subcommands", func(t *testing.T) {
		results, _ := cmd.ValidArgsFunction(cmd, []string{}, "c")
		for _, res := range results {
			if !strings.HasPrefix(res, "c") {
				t.Fatalf("result %q does not match prefix", res)
			}
		}
	})
}
