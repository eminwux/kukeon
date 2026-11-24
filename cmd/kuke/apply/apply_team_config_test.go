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

package apply_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	apply "github.com/eminwux/kukeon/cmd/kuke/apply"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
)

const teamConfigs = `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: kuke-majordomo-claude
  labels:
    kukeon.io/team: kuke
    kukeon.io/role: majordomo
spec:
  blueprint:
    name: bp
---
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: mine
spec:
  blueprint:
    name: bp
`

// runApplyConfigs runs kuke apply -f on teamConfigs and returns stdout and
// stderr apart.
func runApplyConfigs(t *testing.T, extra ...string) (string, string) {
	t.Helper()
	cmd := apply.NewApplyCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	var sent []byte
	fc := &fakeClient{applyFn: func(raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
		sent = raw
		return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{
			{Index: 0, Kind: "CellConfig", Name: "kuke-majordomo-claude", Action: "updated"},
			{Index: 1, Kind: "CellConfig", Name: "mine", Action: "created"},
		}}, nil
	}}
	ctx := context.WithValue(context.Background(), types.CtxLogger, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx = context.WithValue(ctx, apply.MockControllerKey{}, kukeonv1.Client(fc))
	cmd.SetContext(ctx)
	cmd.SetArgs(append([]string{"-f", writeTempYAML(t, teamConfigs)}, extra...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("apply: %v\n%s", err, stderr.String())
	}
	if !bytes.Contains(sent, []byte("name: kuke-majordomo-claude")) || !bytes.Contains(sent, []byte("name: mine")) {
		t.Fatalf("the warning must not stop the apply; sent:\n%s", sent)
	}
	return stdout.String(), stderr.String()
}

// TestApplyWarnsOnTeamOwnedConfig is #1649 AC11: one warning on stderr per
// Config a team owns, the apply unchanged.
func TestApplyWarnsOnTeamOwnedConfig(t *testing.T) {
	stdout, stderr := runApplyConfigs(t)
	want := "warning: config kuke-majordomo-claude belongs to team kuke; the team's next apply replaces this " +
		"edit. Change the team instead: kuke team connector, kuke team apply -f, or the Teams page.\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
	if strings.Contains(stdout, "warning") || !strings.Contains(stdout, `CellConfig "mine": created`) {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestApplyTeamConfigWarningKeepsJSON(t *testing.T) {
	stdout, stderr := runApplyConfigs(t, "-o", "json")
	if strings.Count(stderr, "warning:") != 1 {
		t.Fatalf("stderr = %q", stderr)
	}
	var out struct {
		Resources []kukeonv1.ApplyResourceResult `json:"resources"`
	}
	if err := json.Unmarshal([]byte(stdout), &out); err != nil || len(out.Resources) != 2 {
		t.Fatalf("stdout is not the JSON result: %v\n%s", err, stdout)
	}
}
