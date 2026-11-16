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
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/viper"
)

// TestCreateCell_ModeRequiresFromConfig: a harness mode is one of the modes a
// CellConfig enables, so --mode is rejected on every other create source
// rather than silently dropped (#1472, step 11).
func TestCreateCell_ModeRequiresFromConfig(t *testing.T) {
	for name, source := range map[string]map[string]string{
		"from-blueprint": {"from-blueprint": "web"},
		"image":          {"image": "docker.io/library/alpine:3"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Cleanup(viper.Reset)
			cmd, _ := newTestExecCmd(t, &fakeClient{})
			for flag, value := range source {
				setFlag(t, cmd, flag, value)
			}
			setFlag(t, cmd, "mode", "headless")
			cmd.SetArgs([]string{"x"})

			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "--mode is only valid with --from-config") {
				t.Fatalf("err = %v, want the --mode rejection", err)
			}
		})
	}
}

// TestCreateCell_NoModeOrParamForwardsNone: a plain --from-config create sends
// neither a mode nor per-cell params, leaving both to the Config.
func TestCreateCell_NoModeOrParamForwardsNone(t *testing.T) {
	t.Cleanup(viper.Reset)
	lastConfigRequest = kukeonv1.CellFromConfigRequest{Mode: "stale", Params: map[string]string{"STALE": "1"}}

	cmd, _ := newTestExecCmd(t, &fakeClient{
		getConfigFn: func(v1beta1.CellConfigDoc) (kukeonv1.GetConfigResult, error) {
			return kukeonv1.GetConfigResult{}, nil
		},
	})
	setFlag(t, cmd, "from-config", "prod")
	cmd.SetArgs([]string{"x"})

	// The fake reports no such Config; the request it received is the assertion.
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected the fake's config-not-found error")
	}
	if lastConfigRequest.Config.Name != "prod" {
		t.Fatalf("the create did not reach the daemon render: %+v", lastConfigRequest)
	}
	if lastConfigRequest.Mode != "" || len(lastConfigRequest.Params) != 0 {
		t.Errorf("request mode %q params %v, want neither", lastConfigRequest.Mode, lastConfigRequest.Params)
	}
}

func TestCreateCell_InvalidParamWithFromConfig(t *testing.T) {
	t.Cleanup(viper.Reset)
	lastConfigRequest = kukeonv1.CellFromConfigRequest{}

	cmd, _ := newTestExecCmd(t, &fakeClient{})
	setFlag(t, cmd, "from-config", "prod")
	setFlag(t, cmd, "param", "NOEQUALS")
	cmd.SetArgs([]string{"x"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("expected a malformed --param to fail before the daemon is asked to render")
	}
	if lastConfigRequest.Config.Name != "" {
		t.Errorf("a malformed --param still reached the daemon: %+v", lastConfigRequest)
	}
}

// TestClone_ModeOverridesSourceMode: --mode on a Config-lineage clone replaces
// the source's recorded mode in the render request, and the clone's provenance
// records the binding the daemon rendered — not the source's.
func TestClone_ModeOverridesSourceMode(t *testing.T) {
	t.Cleanup(viper.Reset)
	lastConfigRequest = kukeonv1.CellFromConfigRequest{}
	src := sourceConfigCell()
	src.Spec.Provenance.Mode = "interactive"
	var got v1beta1.CellDoc
	cmd, _ := newTestExecCmd(t, cloneFakeClient(t, src, &got))
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "mode", "headless")
	setFlag(t, cmd, "param", "PROMPT=go")
	cmd.SetArgs([]string{"mclone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lastConfigRequest.Mode != "headless" {
		t.Errorf("request Mode = %q, want --mode over the source's interactive", lastConfigRequest.Mode)
	}
	if got.Spec.Provenance == nil || got.Spec.Provenance.Mode != "headless" {
		t.Fatalf("clone provenance = %+v, want mode headless", got.Spec.Provenance)
	}
	if want := map[string]string{"PROMPT": "go"}; !reflect.DeepEqual(got.Spec.Provenance.ParamOverrides, want) {
		t.Errorf("clone ParamOverrides = %v, want %v", got.Spec.Provenance.ParamOverrides, want)
	}
	// The rest of the lineage is still the source's.
	if !reflect.DeepEqual(got.Spec.Provenance.BindingRef, src.Spec.Provenance.BindingRef) ||
		!reflect.DeepEqual(got.Spec.Provenance.EnvOverrides, src.Spec.Provenance.EnvOverrides) {
		t.Errorf("clone provenance lost the source lineage: %+v", got.Spec.Provenance)
	}
	if src.Spec.Provenance.Mode != "interactive" || len(src.Spec.Provenance.ParamOverrides) != 0 {
		t.Errorf("the source's provenance was modified: %+v", src.Spec.Provenance)
	}
}

// TestClone_InheritsSourceBindingVerbatim: with no --mode / --param the clone
// is created in the source's mode with the source's per-cell params.
func TestClone_InheritsSourceBindingVerbatim(t *testing.T) {
	t.Cleanup(viper.Reset)
	lastConfigRequest = kukeonv1.CellFromConfigRequest{}
	src := sourceConfigCell()
	src.Spec.Provenance.Mode = "headless"
	src.Spec.Provenance.ParamOverrides = map[string]string{"PROMPT": "first"}
	var got v1beta1.CellDoc
	cmd, _ := newTestExecCmd(t, cloneFakeClient(t, src, &got))
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	cmd.SetArgs([]string{"vclone"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got.Spec.Provenance, src.Spec.Provenance) {
		t.Errorf("clone provenance = %+v, want the source's verbatim %+v", got.Spec.Provenance, src.Spec.Provenance)
	}
}

// TestClone_RejectsModeOnBlueprintLineage: a Blueprint-lineage cell has no
// Config to enable modes, so --mode is refused rather than silently dropped.
func TestClone_RejectsModeOnBlueprintLineage(t *testing.T) {
	t.Cleanup(viper.Reset)
	src := sourceBlueprintCell()
	var got v1beta1.CellDoc
	cmd, _ := newTestExecCmd(t, cloneFakeClient(t, src, &got))
	setSourceScope(t, cmd, src)
	setFlag(t, cmd, "clone", src.Metadata.Name)
	setFlag(t, cmd, "mode", "headless")
	cmd.SetArgs([]string{"badclone"})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--mode is not valid when cloning a Blueprint-lineage cell") {
		t.Fatalf("err = %v, want --mode rejection on blueprint-lineage clone", err)
	}
	if got.Metadata.Name != "" {
		t.Errorf("a cell was materialized despite the rejection: %+v", got.Metadata)
	}
}
