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

package restart_test

import (
	"bytes"
	"strings"
	"testing"

	restartpkg "github.com/eminwux/kukeon/cmd/kuke/restart"
	"github.com/spf13/cobra"
)

func TestNewRestartCmdMetadata(t *testing.T) {
	cmd := restartpkg.NewRestartCmd()

	if cmd.Use != "restart [name]" {
		t.Errorf("Use mismatch: got %q want %q", cmd.Use, "restart [name]")
	}
	if cmd.Short != "Restart Kukeon resources (cell)" {
		t.Errorf("Short mismatch: got %q", cmd.Short)
	}
	if !cmd.HasAlias("res") {
		t.Errorf("expected alias %q to be registered", "res")
	}
}

func TestNewRestartCmdRegistersCellSubcommand(t *testing.T) {
	cmd := restartpkg.NewRestartCmd()
	if findSubCommand(cmd, "cell") == nil {
		t.Fatalf("expected %q subcommand to be registered", "cell")
	}
}

func TestNewRestartCmdHelp(t *testing.T) {
	cmd := restartpkg.NewRestartCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	cmd.Run(cmd, nil)

	if !strings.Contains(buf.String(), "Usage:") {
		t.Fatalf("expected help output to include %q, got:\n%s", "Usage:", buf.String())
	}
}

func TestNewRestartCmd_AutocompleteRegistration(t *testing.T) {
	cmd := restartpkg.NewRestartCmd()
	if cmd.ValidArgsFunction == nil {
		t.Fatal("expected ValidArgsFunction to be set for subcommand completion")
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
