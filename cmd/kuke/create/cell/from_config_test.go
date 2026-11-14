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

package cell_test

import (
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

func dryRunClient(t *testing.T) *fakeClient {
	t.Helper()
	return &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{Config: configDoc(), MetadataExists: true}, nil
		},
		getBlueprintFn: func(v1beta1.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error) {
			return kukeonv1.GetBlueprintResult{Blueprint: blueprintDoc(), MetadataExists: true}, nil
		},
		materializeCellFn: func(v1beta1.CellDoc) (kukeonv1.CreateCellResult, error) {
			t.Fatal("--dry-run must not create the cell")
			return kukeonv1.CreateCellResult{}, nil
		},
	}
}

// TestCreateCell_FromConfig_DryRunPrintsRenderedCell: --dry-run asks the
// daemon to render and prints the Cell without creating it (#1418).
func TestCreateCell_FromConfig_DryRunPrintsRenderedCell(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd, out := newTestExecCmd(t, dryRunClient(t))
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "dry-run", "true")
	cmd.SetArgs([]string{"prod-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.String(), "kind: Cell") || !strings.Contains(out.String(), "name: prod-1") {
		t.Errorf("dry-run output is not the rendered Cell YAML:\n%s", out.String())
	}
}

// TestCreateCell_FromConfig_DryRunExplain: --dry-run --explain prints the
// digest and one row per merged value with its origin.
func TestCreateCell_FromConfig_DryRunExplain(t *testing.T) {
	t.Cleanup(viper.Reset)
	fc := dryRunClient(t)
	cmd, out := newTestExecCmd(t, fc)
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "dry-run", "true")
	setFlag(t, cmd, "explain", "true")
	cmd.SetArgs([]string{"prod-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"Digest: " + fakeRenderDigest, "KIND", "ORIGIN"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("explain output missing %q:\n%s", want, out.String())
		}
	}
}

// TestCreateCell_DryRunRequiresFromConfig: --dry-run is a daemon-render
// preview, so it is refused with other sources.
func TestCreateCell_DryRunRequiresFromConfig(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd, _ := newTestExecCmd(t, &fakeClient{})
	setFlag(t, cmd, "from-blueprint", "web")
	setFlag(t, cmd, "dry-run", "true")
	cmd.SetArgs([]string{"web-1"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--from-config") {
		t.Fatalf("Execute error = %v, want the --from-config requirement", err)
	}
}
