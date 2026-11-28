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
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/cell"
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

// TestPrintConnectorWarnings: a connector that resolved with a warning — a
// login identity another role uses (issue #1789) — gets one `warning:` line
// naming it; the others print nothing.
func TestPrintConnectorWarnings(t *testing.T) {
	var buf bytes.Buffer
	err := cell.PrintConnectorWarnings(&buf, []v1beta1.ConnectorStatus{
		{Connector: "forge", State: v1beta1.ConnectorResolved},
		{
			Connector: "claude",
			State:     v1beta1.ConnectorResolved,
			Warning:   "volume v is also the login identity of role a",
		},
	})
	if err != nil {
		t.Fatalf("PrintConnectorWarnings: %v", err)
	}
	want := "warning: connector \"claude\": volume v is also the login identity of role a\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
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

// TestCreateCell_FromConfig_ConnectorOverrides: `--connector
// <connector>=<secret>[@scope]` reaches the daemon as a per-cell override
// keyed by connector name (issue #1548); a secret named without a scope is
// looked up in the Config's realm, and the connector name is not interpreted
// by the CLI — the daemon checks it against the Config's blueprint.
func TestCreateCell_FromConfig_ConnectorOverrides(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd, out := newTestExecCmd(t, dryRunClient(t))
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "dry-run", "true")
	setFlag(t, cmd, "connector", "forge=other-token")
	setFlag(t, cmd, "connector", "example-token=shared@team/eng/web")
	cmd.SetArgs([]string{"prod-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := map[string]v1beta1.ContainerSecretRef{
		"forge":         {Name: "other-token", Realm: "cfg-realm"},
		"example-token": {Name: "shared", Realm: "team", Space: "eng", Stack: "web"},
	}
	if got := lastConfigRequest.ConnectorOverrides; !reflect.DeepEqual(got, want) {
		t.Errorf("request overrides = %+v, want %+v", got, want)
	}
	// The rendered cell records them, so a restart and a recreate keep them.
	if !strings.Contains(out.String(), "connectorOverrides:") || !strings.Contains(out.String(), "other-token") {
		t.Errorf("rendered cell does not record the overrides:\n%s", out.String())
	}
}

// TestCreateCell_ConnectorFlagRejections: a malformed override, one connector
// named twice, and --connector without --from-config are refused before the
// daemon is asked for anything.
func TestCreateCell_ConnectorFlagRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		source  [2]string
		values  []string
		wantErr string
	}{
		"no secret":         {[2]string{"from-config", "prod"}, []string{"forge"}, "want <connector>=<secret>"},
		"empty secret":      {[2]string{"from-config", "prod"}, []string{"forge="}, "want <connector>=<secret>"},
		"too deep a scope":  {[2]string{"from-config", "prod"}, []string{"forge=t@a/b/c/d"}, "name[@realm[/space[/stack]]]"},
		"named twice":       {[2]string{"from-config", "prod"}, []string{"forge=a", "forge=b"}, "more than once"},
		"with a blueprint":  {[2]string{"from-blueprint", "web"}, []string{"forge=a"}, "only valid with --from-config"},
		"with a clone":      {[2]string{"clone", "src"}, []string{"forge=a"}, "only valid with --from-config"},
		"with a bare image": {[2]string{"image", "busybox"}, []string{"forge=a"}, "only valid with --from-config"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			cmd, _ := newTestExecCmd(t, &fakeClient{})
			setFlag(t, cmd, "realm", "cfg-realm")
			setFlag(t, cmd, tc.source[0], tc.source[1])
			for _, value := range tc.values {
				setFlag(t, cmd, "connector", value)
			}
			cmd.SetArgs([]string{"prod-1"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Execute error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestCreateCell_FromConfig_ConnectorMethods: `--connector-method
// <connector>=<method>` reaches the daemon as a per-cell method keyed by
// connector name (issue #1551), next to a --connector identity for it; the
// CLI does not interpret the names — the daemon checks them against the
// Config's blueprint.
func TestCreateCell_FromConfig_ConnectorMethods(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd, out := newTestExecCmd(t, dryRunClient(t))
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "dry-run", "true")
	setFlag(t, cmd, "connector-method", "codex=api-key")
	setFlag(t, cmd, "connector", "codex=openai-key")
	cmd.SetArgs([]string{"prod-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := map[string]string{"codex": "api-key"}
	if got := lastConfigRequest.ConnectorMethods; !reflect.DeepEqual(got, want) {
		t.Errorf("request methods = %+v, want %+v", got, want)
	}
	if got := lastConfigRequest.ConnectorOverrides["codex"].Name; got != "openai-key" {
		t.Errorf("request override = %q, want openai-key", got)
	}
	// The rendered cell records the method, so a restart and a recreate keep it.
	if !strings.Contains(out.String(), "connectorMethods:") || !strings.Contains(out.String(), "api-key") {
		t.Errorf("rendered cell does not record the method:\n%s", out.String())
	}
}

// TestCreateCell_ConnectorMethodFlagRejections: a malformed choice, one
// connector named twice, and --connector-method without --from-config are
// refused before the daemon is asked for anything.
func TestCreateCell_ConnectorMethodFlagRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		source  [2]string
		values  []string
		wantErr string
	}{
		"no method":         {[2]string{"from-config", "prod"}, []string{"codex"}, "want <connector>=<method>"},
		"empty method":      {[2]string{"from-config", "prod"}, []string{"codex="}, "want <connector>=<method>"},
		"bad method":        {[2]string{"from-config", "prod"}, []string{"codex=API KEY"}, "want <connector>=<method>"},
		"named twice":       {[2]string{"from-config", "prod"}, []string{"codex=login", "codex=api-key"}, "more than once"},
		"with a blueprint":  {[2]string{"from-blueprint", "web"}, []string{"codex=api-key"}, "only valid with --from-config"},
		"with a clone":      {[2]string{"clone", "src"}, []string{"codex=api-key"}, "only valid with --from-config"},
		"with a bare image": {[2]string{"image", "busybox"}, []string{"codex=api-key"}, "only valid with --from-config"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			cmd, _ := newTestExecCmd(t, &fakeClient{})
			setFlag(t, cmd, "realm", "cfg-realm")
			setFlag(t, cmd, tc.source[0], tc.source[1])
			for _, value := range tc.values {
				setFlag(t, cmd, "connector-method", value)
			}
			cmd.SetArgs([]string{"prod-1"})
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Execute error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestCreateCell_FromConfig_State: `--state` reaches the daemon as the cell's
// harness state choice (issue #1685); the CLI does not interpret it beyond
// requiring --from-config.
func TestCreateCell_FromConfig_State(t *testing.T) {
	t.Cleanup(viper.Reset)
	cmd, _ := newTestExecCmd(t, dryRunClient(t))
	setFlag(t, cmd, "realm", "cfg-realm")
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "dry-run", "true")
	setFlag(t, cmd, "state", "ephemeral")
	cmd.SetArgs([]string{"prod-1"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := lastConfigRequest.State; got != "ephemeral" {
		t.Errorf("request state = %q, want ephemeral", got)
	}

	for _, source := range [][2]string{{"from-blueprint", "web"}, {"clone", "src"}, {"image", "busybox"}} {
		t.Run("with "+source[0], func(t *testing.T) {
			t.Cleanup(viper.Reset)
			rejected, _ := newTestExecCmd(t, &fakeClient{})
			setFlag(t, rejected, "realm", "cfg-realm")
			setFlag(t, rejected, source[0], source[1])
			setFlag(t, rejected, "state", "ephemeral")
			rejected.SetArgs([]string{"prod-1"})
			err := rejected.Execute()
			if err == nil || !strings.Contains(err.Error(), "--state is only valid with --from-config") {
				t.Fatalf("Execute error = %v, want --state refused without --from-config", err)
			}
		})
	}
}
