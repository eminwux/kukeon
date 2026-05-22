//go:build !integration

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

package cellconfig_test

import (
	"errors"
	"testing"

	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// TestStableName_Verbatim pins the issue #624 decision: the materialized cell
// name is the config name verbatim (trimmed), value-independent so the identity
// survives value edits.
func TestStableName_Verbatim(t *testing.T) {
	cases := map[string]string{
		"kukeon-dev":   "kukeon-dev",
		"  spaced  ":   "spaced",
		"team-a-build": "team-a-build",
	}
	for in, want := range cases {
		if got := cellconfig.StableName(in); got != want {
			t.Errorf("StableName(%q) = %q, want %q", in, got, want)
		}
	}
}

// blueprintWithSlots builds a one-container blueprint declaring the given repo
// slots (name→required, all url-empty/structural) and secret slots.
func blueprintWithSlots(repos, secrets map[string]bool) v1beta1.CellBlueprintDoc {
	var repoSlots []v1beta1.ContainerRepo
	for name, required := range repos {
		repoSlots = append(repoSlots, v1beta1.ContainerRepo{
			Name: name, Target: "/work/" + name, Required: required,
		})
	}
	var secretSlots []v1beta1.BlueprintSecretSlot
	for name, required := range secrets {
		secretSlots = append(secretSlots, v1beta1.BlueprintSecretSlot{
			Name: name, Mode: v1beta1.BlueprintSecretModeEnv, EnvName: "E_" + name, Required: required,
		})
	}
	return v1beta1.CellBlueprintDoc{
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "main", Image: "img", Repos: repoSlots, Secrets: secretSlots},
				},
			},
		},
	}
}

func TestValidateSlotFill_AllFilled(t *testing.T) {
	bp := blueprintWithSlots(
		map[string]bool{"project": true},
		map[string]bool{"token": true},
	)
	cfg := v1beta1.CellConfigDoc{
		Spec: v1beta1.CellConfigSpec{
			Repos:   map[string]v1beta1.CellConfigRepoFill{"project": {URL: "git@x:y.git"}},
			Secrets: map[string]v1beta1.CellConfigSecretFill{"token": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "r"}}},
		},
	}
	if err := cellconfig.ValidateSlotFill(cfg, bp); err != nil {
		t.Fatalf("ValidateSlotFill() error = %v, want nil", err)
	}
}

func TestValidateSlotFill_RequiredRepoUnfilled(t *testing.T) {
	bp := blueprintWithSlots(map[string]bool{"project": true}, nil)
	cfg := v1beta1.CellConfigDoc{} // no fills
	err := cellconfig.ValidateSlotFill(cfg, bp)
	if !errors.Is(err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Fatalf("err = %v, want ErrConfigRequiredSlotUnfilled", err)
	}
}

func TestValidateSlotFill_RequiredSecretUnfilled(t *testing.T) {
	bp := blueprintWithSlots(nil, map[string]bool{"token": true})
	cfg := v1beta1.CellConfigDoc{}
	err := cellconfig.ValidateSlotFill(cfg, bp)
	if !errors.Is(err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Fatalf("err = %v, want ErrConfigRequiredSlotUnfilled", err)
	}
}

func TestValidateSlotFill_OptionalUnfilledOK(t *testing.T) {
	bp := blueprintWithSlots(
		map[string]bool{"project": false},
		map[string]bool{"token": false},
	)
	if err := cellconfig.ValidateSlotFill(v1beta1.CellConfigDoc{}, bp); err != nil {
		t.Fatalf("ValidateSlotFill() error = %v, want nil for optional unfilled slots", err)
	}
}

func TestValidateSlotFill_UnknownRepoSlot(t *testing.T) {
	bp := blueprintWithSlots(map[string]bool{"project": false}, nil)
	cfg := v1beta1.CellConfigDoc{
		Spec: v1beta1.CellConfigSpec{
			Repos: map[string]v1beta1.CellConfigRepoFill{"typo": {URL: "git@x:y.git"}},
		},
	}
	err := cellconfig.ValidateSlotFill(cfg, bp)
	if !errors.Is(err, errdefs.ErrConfigUnknownRepoSlot) {
		t.Fatalf("err = %v, want ErrConfigUnknownRepoSlot", err)
	}
}

func TestValidateSlotFill_UnknownSecretSlot(t *testing.T) {
	bp := blueprintWithSlots(nil, map[string]bool{"token": false})
	cfg := v1beta1.CellConfigDoc{
		Spec: v1beta1.CellConfigSpec{
			Secrets: map[string]v1beta1.CellConfigSecretFill{"typo": {SecretRef: &v1beta1.ContainerSecretRef{Name: "t", Realm: "r"}}},
		},
	}
	err := cellconfig.ValidateSlotFill(cfg, bp)
	if !errors.Is(err, errdefs.ErrConfigUnknownSecretSlot) {
		t.Fatalf("err = %v, want ErrConfigUnknownSecretSlot", err)
	}
}

// TestValidateSlotFill_InlineRepoNotASlot confirms a blueprint repo with an
// inline url is scalar-mode, not a fillable slot: a config that names it is an
// unknown-slot error, not a match.
func TestValidateSlotFill_InlineRepoNotASlot(t *testing.T) {
	bp := v1beta1.CellBlueprintDoc{
		Spec: v1beta1.CellBlueprintSpec{
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "main", Image: "img", Repos: []v1beta1.ContainerRepo{
						{Name: "inlined", Target: "/work", URL: "git@x:y.git"},
					}},
				},
			},
		},
	}
	cfg := v1beta1.CellConfigDoc{
		Spec: v1beta1.CellConfigSpec{
			Repos: map[string]v1beta1.CellConfigRepoFill{"inlined": {URL: "git@a:b.git"}},
		},
	}
	if err := cellconfig.ValidateSlotFill(cfg, bp); !errors.Is(err, errdefs.ErrConfigUnknownRepoSlot) {
		t.Fatalf("err = %v, want ErrConfigUnknownRepoSlot (inline-url repo is not a slot)", err)
	}
}
