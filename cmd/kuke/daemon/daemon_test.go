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

package daemon_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/daemon"
)

func TestDaemonCmd_HasStartSubcommand(t *testing.T) {
	cmd := daemon.NewDaemonCmd()

	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Name() == "start" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected `kuke daemon` to register a `start` subcommand")
	}
}

func TestDaemonCmd_HasStopSubcommand(t *testing.T) {
	cmd := daemon.NewDaemonCmd()

	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Name() == "stop" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected `kuke daemon` to register a `stop` subcommand")
	}
}

func TestDaemonCmd_HasKillSubcommand(t *testing.T) {
	cmd := daemon.NewDaemonCmd()

	var found bool
	for _, sub := range cmd.Commands() {
		if sub.Name() == "kill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected `kuke daemon` to register a `kill` subcommand")
	}
}

func TestDaemonCmd_HelpRunsWithoutArgs(t *testing.T) {
	cmd := daemon.NewDaemonCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error running `kuke daemon` with no args: %v", err)
	}
	if !strings.Contains(buf.String(), "daemon") {
		t.Errorf("expected help output to mention `daemon`, got:\n%s", buf.String())
	}
}
