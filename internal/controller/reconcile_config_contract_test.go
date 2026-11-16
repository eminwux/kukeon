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

// Tests for the apply-time gate of a CellConfig against a component- and
// mode-bearing blueprint (issue #1472, steps 10–11).

package controller_test

import (
	"errors"
	"testing"

	applypkg "github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// applyContractConfig runs cfg through both write paths (`kuke apply` and the
// create-only RPC) against bp and reports each path's error and whether it
// reached the store.
func applyContractConfig(
	t *testing.T, cfg ext.CellConfigDoc, bp ext.CellBlueprintDoc,
) map[string]struct {
	err   error
	wrote bool
} {
	t.Helper()
	out := map[string]struct {
		err   error
		wrote bool
	}{}
	for name, call := range map[string]func(runner.Runner, intmodel.CellConfig) (applypkg.ReconcileResult, error){
		"ReconcileConfig": applypkg.ReconcileConfig,
		"CreateConfig":    applypkg.CreateConfig,
	} {
		wrote := false
		f := &fakeRunner{
			GetRealmFn: func(realm intmodel.Realm) (intmodel.Realm, error) { return realm, nil },
			GetBlueprintFn: func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
				return blueprintCarrier(t, bp), nil
			},
			WriteConfigFn:         func(intmodel.CellConfig) (bool, error) { wrote = true; return true, nil },
			WriteConfigIfAbsentFn: func(intmodel.CellConfig) error { wrote = true; return nil },
		}
		_, err := call(f, configCarrier(t, cfg))
		out[name] = struct {
			err   error
			wrote bool
		}{err, wrote}
	}
	return out
}

// TestApplyConfig_ContractAccepts: a Config is stored before any cell — and so
// any mode — exists, so only always-on slots are demanded at apply time. The
// headless mode's required API key and an optional component's required token
// may be left unfilled, or filled ahead of use.
func TestApplyConfig_ContractAccepts(t *testing.T) {
	for name, mutate := range map[string]func(*ext.CellConfigDoc){
		"only the always-on slots filled": func(*ext.CellConfigDoc) {},
		"mode and component slots filled ahead of use": func(c *ext.CellConfigDoc) {
			c.Spec.Secrets = map[string]ext.CellConfigSecretFill{
				"api-key":  {SecretRef: contractSecretRef("anthropic")},
				"gh-token": {SecretRef: contractSecretRef("gh")},
			}
		},
		"component enabled and modes narrowed": func(c *ext.CellConfigDoc) {
			c.Spec.Components = []string{"github"}
			c.Spec.Modes = &ext.CellConfigModes{Enabled: []string{ext.ModeHeadless}, Default: ext.ModeHeadless}
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sampleConfig()
			mutate(&cfg)
			for path, got := range applyContractConfig(t, cfg, contractBlueprint()) {
				if got.err != nil || !got.wrote {
					t.Errorf("%s: err = %v wrote = %v, want the Config stored", path, got.err, got.wrote)
				}
			}
		})
	}
}

func TestApplyConfig_ContractRejects(t *testing.T) {
	for name, tc := range map[string]struct {
		blueprint func() ext.CellBlueprintDoc
		mutate    func(*ext.CellConfigDoc)
		want      error
	}{
		"unknown component": {
			blueprint: contractBlueprint,
			mutate:    func(c *ext.CellConfigDoc) { c.Spec.Components = []string{"gitlab"} },
			want:      errdefs.ErrBlueprintComponent,
		},
		"unknown enabled mode": {
			blueprint: contractBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Enabled: []string{"batch"}}
			},
			want: errdefs.ErrBlueprintMode,
		},
		"unknown default mode": {
			blueprint: contractBlueprint,
			mutate:    func(c *ext.CellConfigDoc) { c.Spec.Modes = &ext.CellConfigModes{Default: "batch"} },
			want:      errdefs.ErrBlueprintMode,
		},
		"default mode outside the enabled set": {
			blueprint: contractBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{
					Enabled: []string{ext.ModeInteractive}, Default: ext.ModeHeadless,
				}
			},
			want: errdefs.ErrBlueprintMode,
		},
		"modes on a blueprint that declares none": {
			blueprint: sampleReferencedBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Modes = &ext.CellConfigModes{Default: ext.ModeInteractive}
			},
			want: errdefs.ErrBlueprintMode,
		},
		"always-on repo slot unfilled": {
			blueprint: contractBlueprint,
			mutate:    func(c *ext.CellConfigDoc) { c.Spec.Repos = nil },
			want:      errdefs.ErrConfigRequiredSlotUnfilled,
		},
		"always-on component's required slot unfilled": {
			blueprint: func() ext.CellBlueprintDoc {
				bp := contractBlueprint()
				bp.Spec.Components[0].Secrets = []ext.BlueprintSecretSlot{
					{Name: "state-key", EnvName: "STATE_KEY", Required: true},
				}
				return bp
			},
			mutate: func(*ext.CellConfigDoc) {},
			want:   errdefs.ErrConfigRequiredSlotUnfilled,
		},
		"fill for a slot nothing declares": {
			blueprint: contractBlueprint,
			mutate: func(c *ext.CellConfigDoc) {
				c.Spec.Secrets = map[string]ext.CellConfigSecretFill{"typo": {SecretRef: contractSecretRef("x")}}
			},
			want: errdefs.ErrConfigUnknownSecretSlot,
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := sampleConfig()
			tc.mutate(&cfg)
			for path, got := range applyContractConfig(t, cfg, tc.blueprint()) {
				if !errors.Is(got.err, tc.want) {
					t.Errorf("%s: err = %v, want %v", path, got.err, tc.want)
				}
				if got.wrote {
					t.Errorf("%s: the Config was stored despite the rejection", path)
				}
			}
		})
	}
}
