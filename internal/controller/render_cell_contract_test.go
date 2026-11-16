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

// Tests for the blueprint-contract half of the daemon-side CellConfig render
// (issue #1472): components a Config enables, the harness mode picked per
// cell, per-cell mode parameters, image capability requirements, and the
// re-render of a live cell from its recorded provenance.

package controller_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/controller"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// contractBlueprint is a shared harness blueprint: an always-on harness with
// an interactive and a headless mode, and an optional GitHub connector.
func contractBlueprint() ext.CellBlueprintDoc {
	model := "opus"
	return ext.CellBlueprintDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellBlueprint,
		Metadata:   ext.CellBlueprintMetadata{Name: "dev", Realm: "kuke-system"},
		Spec: ext.CellBlueprintSpec{
			Requires:   []string{"git"},
			Parameters: []ext.CellBlueprintParameter{{Name: "IMAGE", Type: ext.ParameterTypeImage}},
			Components: []ext.BlueprintComponent{
				{
					Kind:     ext.ComponentKindHarness,
					Name:     "claude",
					Requires: []string{"claude"},
					Parameters: []ext.CellBlueprintParameter{{
						Name: "MODEL", Type: ext.ParameterTypeEnum, Enum: []string{"opus", "sonnet"}, Default: &model,
					}},
					Env: []string{"MODEL=${MODEL}"},
					Modes: map[string]ext.BlueprintComponentMode{
						ext.ModeInteractive: {OnInit: []ext.TtyStage{{Script: "exec claude"}}},
						ext.ModeHeadless: {
							OnInit:     []ext.TtyStage{{Script: `exec claude -p "$PROMPT"`}},
							Parameters: []ext.CellBlueprintParameter{{Name: "PROMPT", Required: true}},
							Env:        []string{"PROMPT=${PROMPT}"},
							Secrets: []ext.BlueprintSecretSlot{
								{Name: "api-key", EnvName: "ANTHROPIC_API_KEY", Required: true},
							},
						},
					},
				},
				{
					Kind:     ext.ComponentKindConnector,
					Name:     "github",
					Optional: true,
					Requires: []string{"gh"},
					Env:      []string{"GH_PROMPT_DISABLED=1"},
					Secrets:  []ext.BlueprintSecretSlot{{Name: "gh-token", EnvName: "GH_TOKEN", Required: true}},
				},
			},
			Cell: ext.BlueprintCellSpec{Containers: []ext.BlueprintContainer{{
				ID:         "main",
				Image:      "${IMAGE}",
				Attachable: true,
				Repos:      []ext.ContainerRepo{{Name: "project", Target: "/work", Required: true}},
			}}},
		},
	}
}

func contractSecretRef(name string) *ext.ContainerSecretRef {
	return &ext.ContainerSecretRef{Name: name, Realm: "kuke-system"}
}

func newContractRunner(t *testing.T, cfg ext.CellConfigDoc) *fakeRunner {
	t.Helper()
	mock := newRenderRunner(t, &renderScope{}, cfg)
	mock.GetBlueprintFn = func(intmodel.CellBlueprint) (intmodel.CellBlueprint, error) {
		return blueprintCarrier(t, contractBlueprint()), nil
	}
	return mock
}

func contractPreview(
	t *testing.T, cfg ext.CellConfigDoc, mode string, params map[string]string,
) (controller.CellFromConfigPreview, error) {
	t.Helper()
	req := previewRequest()
	req.Mode, req.Params = mode, params
	return setupTestController(t, newContractRunner(t, cfg)).PreviewCellFromConfig(req)
}

func secretNames(c ext.ContainerSpec) []string {
	var out []string
	for _, s := range c.Secrets {
		out = append(out, s.Name)
	}
	return out
}

// TestPreviewCellFromConfig_ContractDefaults: with no components enabled and
// no mode requested, the always-on harness is folded in, the cell is created
// in the blueprint's default (interactive) mode, and the capability
// requirement is the union of the blueprint's and the Config's.
func TestPreviewCellFromConfig_ContractDefaults(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Requires = []string{"node", "git"}

	preview, err := contractPreview(t, cfg, "", nil)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	cell := preview.Cell
	if got := cell.Metadata.Labels[cellblueprint.LabelMode]; got != ext.ModeInteractive {
		t.Errorf("label %s = %q, want interactive", cellblueprint.LabelMode, got)
	}
	if cell.Spec.Provenance == nil || cell.Spec.Provenance.Mode != ext.ModeInteractive ||
		len(cell.Spec.Provenance.ParamOverrides) != 0 {
		t.Errorf("provenance = %+v, want mode interactive and no per-cell params", cell.Spec.Provenance)
	}
	if want := []string{"claude", "git", "node"}; !reflect.DeepEqual(cell.Spec.Requires, want) {
		t.Errorf("Requires = %v, want %v", cell.Spec.Requires, want)
	}
	main := cell.Spec.Containers[0]
	if !containsString(main.Env, "MODEL=opus") {
		t.Errorf("env = %v, want the harness component's MODEL default", main.Env)
	}
	if containsString(main.Env, "GH_PROMPT_DISABLED=1") || len(main.Secrets) != 0 {
		t.Errorf("env %v / secrets %v carry a disabled component or an unpicked mode", main.Env, secretNames(main))
	}
	if main.Tty == nil || len(main.Tty.OnInit) != 1 || main.Tty.OnInit[0].Script != "exec claude" {
		t.Errorf("onInit = %+v, want the interactive entrypoint", main.Tty)
	}

	for _, want := range []ext.RenderedValue{
		{Kind: ext.RenderKindComponent, Key: "claude", Value: ext.ComponentKindHarness, Origin: ext.RenderOriginBlueprint},
		{Kind: ext.RenderKindMode, Key: "mode", Value: ext.ModeInteractive, Origin: ext.RenderOriginBlueprint},
		{Kind: ext.RenderKindRequires, Key: "claude", Value: "claude", Origin: ext.RenderOriginBlueprint},
		{Kind: ext.RenderKindRequires, Key: "git", Value: "git", Origin: ext.RenderOriginBlueprint},
		{Kind: ext.RenderKindRequires, Key: "node", Value: "node", Origin: ext.RenderOriginConfig},
	} {
		if got, ok := valueOrigin(preview.Values, want.Kind, want.Key); !ok || got != want {
			t.Errorf("explain %s:%s = %+v (found=%v), want %+v", want.Kind, want.Key, got, ok, want)
		}
	}
	if _, ok := valueOrigin(preview.Values, ext.RenderKindComponent, "github"); ok {
		t.Error("explain lists the github component although the Config does not enable it")
	}

	// The contract fields survive the conversion the create path persists.
	internal, err := apischeme.ConvertCellDocToInternal(cell)
	if err != nil {
		t.Fatalf("ConvertCellDocToInternal: %v", err)
	}
	if !reflect.DeepEqual(internal.Spec.Requires, cell.Spec.Requires) ||
		internal.Spec.Provenance == nil || internal.Spec.Provenance.Mode != ext.ModeInteractive {
		t.Errorf("internal cell lost contract fields: requires %v provenance %+v",
			internal.Spec.Requires, internal.Spec.Provenance)
	}
}

// TestPreviewCellFromConfig_EnabledComponent: a component the Config enables
// contributes its env, secret slot and capability, attributed to the Config.
func TestPreviewCellFromConfig_EnabledComponent(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Components = []string{"github"}
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"gh-token": {SecretRef: contractSecretRef("gh")}}

	preview, err := contractPreview(t, cfg, "", nil)
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	main := preview.Cell.Spec.Containers[0]
	if !containsString(main.Env, "GH_PROMPT_DISABLED=1") {
		t.Errorf("env = %v, want the connector's env", main.Env)
	}
	if len(main.Secrets) != 1 || main.Secrets[0].SecretRef == nil || main.Secrets[0].SecretRef.Name != "gh" {
		t.Errorf("secrets = %+v, want the filled gh-token slot", main.Secrets)
	}
	if want := []string{"claude", "gh", "git"}; !reflect.DeepEqual(preview.Cell.Spec.Requires, want) {
		t.Errorf("Requires = %v, want %v", preview.Cell.Spec.Requires, want)
	}
	if got, ok := valueOrigin(preview.Values, ext.RenderKindComponent, "github"); !ok ||
		got.Origin != ext.RenderOriginConfig || got.Value != ext.ComponentKindConnector {
		t.Errorf("explain component:github = %+v (found=%v), want connector from config", got, ok)
	}

	// Enabling the component makes its required slot the Config's to fill.
	cfg.Spec.Secrets = nil
	if _, err = contractPreview(t, cfg, "", nil); !errors.Is(err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Errorf("err = %v, want ErrConfigRequiredSlotUnfilled for the enabled component's slot", err)
	}
}

func TestPreviewCellFromConfig_UnknownComponent(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Components = []string{"gitlab"}
	if _, err := contractPreview(t, cfg, "", nil); !errors.Is(err, errdefs.ErrBlueprintComponent) {
		t.Fatalf("err = %v, want ErrBlueprintComponent", err)
	}
}

// TestPreviewCellFromConfig_ModeSelection walks who decides the mode — the
// cell's create request, else the Config — and that the explanation says so.
func TestPreviewCellFromConfig_ModeSelection(t *testing.T) {
	headlessFill := map[string]ext.CellConfigSecretFill{"api-key": {SecretRef: contractSecretRef("anthropic")}}
	prompt := map[string]string{"PROMPT": "fix the build"}

	t.Run("config default", func(t *testing.T) {
		cfg := renderConfig()
		cfg.Spec.Secrets = headlessFill
		cfg.Spec.Values["PROMPT"] = "from config"
		cfg.Spec.Modes = &ext.CellConfigModes{Default: ext.ModeHeadless}
		preview, err := contractPreview(t, cfg, "", nil)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		if got, _ := valueOrigin(preview.Values, ext.RenderKindMode, "mode"); got.Value != ext.ModeHeadless ||
			got.Origin != ext.RenderOriginConfig {
			t.Errorf("explain mode = %+v, want headless from config", got)
		}
		if !containsString(preview.Cell.Spec.Containers[0].Env, "PROMPT=from config") {
			t.Errorf("env = %v, want the Config's PROMPT value", preview.Cell.Spec.Containers[0].Env)
		}
	})

	t.Run("create request wins", func(t *testing.T) {
		cfg := renderConfig()
		cfg.Spec.Secrets = headlessFill
		cfg.Spec.Modes = &ext.CellConfigModes{
			Enabled: []string{ext.ModeInteractive, ext.ModeHeadless}, Default: ext.ModeInteractive,
		}
		preview, err := contractPreview(t, cfg, ext.ModeHeadless, prompt)
		if err != nil {
			t.Fatalf("PreviewCellFromConfig: %v", err)
		}
		cell := preview.Cell
		if cell.Metadata.Labels[cellblueprint.LabelMode] != ext.ModeHeadless ||
			cell.Spec.Provenance.Mode != ext.ModeHeadless {
			t.Errorf("label %q / provenance mode %q, want headless",
				cell.Metadata.Labels[cellblueprint.LabelMode], cell.Spec.Provenance.Mode)
		}
		if got, _ := valueOrigin(preview.Values, ext.RenderKindMode, "mode"); got.Origin != ext.RenderOriginCell {
			t.Errorf("explain mode = %+v, want it attributed to the cell", got)
		}
		main := cell.Spec.Containers[0]
		if main.Tty == nil || len(main.Tty.OnInit) != 1 || !strings.Contains(main.Tty.OnInit[0].Script, "-p") {
			t.Errorf("onInit = %+v, want the headless entrypoint", main.Tty)
		}
		if len(main.Secrets) != 1 || main.Secrets[0].SecretRef == nil || main.Secrets[0].SecretRef.Name != "anthropic" {
			t.Errorf("secrets = %+v, want the headless api-key slot filled", main.Secrets)
		}
	})

	t.Run("mode not enabled by the config", func(t *testing.T) {
		cfg := renderConfig()
		cfg.Spec.Modes = &ext.CellConfigModes{Enabled: []string{ext.ModeInteractive}}
		if _, err := contractPreview(t, cfg, ext.ModeHeadless, prompt); !errors.Is(err, errdefs.ErrBlueprintMode) {
			t.Fatalf("err = %v, want ErrBlueprintMode", err)
		}
	})

	t.Run("mode not declared by the blueprint", func(t *testing.T) {
		if _, err := contractPreview(t, renderConfig(), "batch", nil); !errors.Is(err, errdefs.ErrBlueprintMode) {
			t.Fatalf("err = %v, want ErrBlueprintMode", err)
		}
	})
}

// TestPreviewCellFromConfig_RequiredSlotsOnlyForPickedMode is the step-11 AC:
// one Config serves both an attached cell and a dispatched run. The headless
// mode's required API-key slot and PROMPT parameter gate headless cells only —
// and a Config that fills the headless slot still renders interactive cells.
func TestPreviewCellFromConfig_RequiredSlotsOnlyForPickedMode(t *testing.T) {
	prompt := map[string]string{"PROMPT": "fix the build"}

	bare := renderConfig()
	if _, err := contractPreview(t, bare, ext.ModeInteractive, nil); err != nil {
		t.Fatalf("interactive render without the headless slot filled: %v", err)
	}
	if _, err := contractPreview(t, bare, ext.ModeHeadless, prompt); !errors.Is(
		err, errdefs.ErrConfigRequiredSlotUnfilled) {
		t.Fatalf("headless without api-key: err = %v, want ErrConfigRequiredSlotUnfilled", err)
	}

	both := renderConfig()
	both.Spec.Secrets = map[string]ext.CellConfigSecretFill{"api-key": {SecretRef: contractSecretRef("anthropic")}}
	if _, err := contractPreview(t, both, ext.ModeHeadless, nil); err == nil ||
		!strings.Contains(err.Error(), `required parameter "PROMPT"`) {
		t.Fatalf("headless without PROMPT: err = %v, want the required-parameter error", err)
	}
	if _, err := contractPreview(t, both, ext.ModeHeadless, prompt); err != nil {
		t.Fatalf("headless with api-key and PROMPT: %v", err)
	}
	interactive, err := contractPreview(t, both, ext.ModeInteractive, nil)
	if err != nil {
		t.Fatalf("interactive render of a Config that also fills the headless slot: %v", err)
	}
	if got := secretNames(interactive.Cell.Spec.Containers[0]); len(got) != 0 {
		t.Errorf("interactive cell secrets = %v, want none: the API key belongs to headless cells", got)
	}
}

// TestPreviewCellFromConfig_InactiveSlotFills: a fill for a slot of a component
// the Config leaves off is kept as an unused answer (the toggle can come back
// on without re-entering it), while a fill no component or mode declares is
// still the unknown-slot error it always was.
func TestPreviewCellFromConfig_InactiveSlotFills(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"gh-token": {SecretRef: contractSecretRef("gh")}}
	preview, err := contractPreview(t, cfg, "", nil)
	if err != nil {
		t.Fatalf("render with a fill for a disabled component's slot: %v", err)
	}
	if got := secretNames(preview.Cell.Spec.Containers[0]); len(got) != 0 {
		t.Errorf("secrets = %v, want none: the github component is off", got)
	}
	if _, ok := valueOrigin(preview.Values, ext.RenderKindSecret, "gh-token"); ok {
		t.Error("explain lists the gh-token secret although the cell does not receive it")
	}

	cfg.Spec.Secrets["typo-token"] = ext.CellConfigSecretFill{SecretRef: contractSecretRef("gh")}
	if _, err = contractPreview(t, cfg, "", nil); !errors.Is(err, errdefs.ErrConfigUnknownSecretSlot) {
		t.Fatalf("err = %v, want ErrConfigUnknownSecretSlot for a slot nothing declares", err)
	}
}

// TestPreviewCellFromConfig_PerCellParams: a cell may set for itself only what
// the mode it is created in asks for; everything else stays the Config's.
func TestPreviewCellFromConfig_PerCellParams(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"api-key": {SecretRef: contractSecretRef("anthropic")}}
	cfg.Spec.Values["PROMPT"] = "from config"

	preview, err := contractPreview(t, cfg, ext.ModeHeadless, map[string]string{"PROMPT": "from cell"})
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	cell := preview.Cell
	if !containsString(cell.Spec.Containers[0].Env, "PROMPT=from cell") {
		t.Errorf("env = %v, want the per-cell PROMPT over the Config's", cell.Spec.Containers[0].Env)
	}
	if want := map[string]string{"PROMPT": "from cell"}; !reflect.DeepEqual(cell.Spec.Provenance.ParamOverrides, want) {
		t.Errorf("provenance ParamOverrides = %v, want %v", cell.Spec.Provenance.ParamOverrides, want)
	}
	if got, ok := valueOrigin(preview.Values, ext.RenderKindValue, "PROMPT"); !ok ||
		got.Value != "from cell" || got.Origin != ext.RenderOriginCell {
		t.Errorf("explain value:PROMPT = %+v (found=%v), want the cell's value", got, ok)
	}
	count := 0
	for _, v := range preview.Values {
		if v.Kind == ext.RenderKindValue && v.Key == "PROMPT" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("explain lists value:PROMPT %d times, want once", count)
	}

	for name, tc := range map[string]struct {
		mode   string
		params map[string]string
	}{
		"a Config-owned parameter":        {ext.ModeHeadless, map[string]string{"IMAGE": "evil:latest", "PROMPT": "x"}},
		"a component parameter":           {ext.ModeHeadless, map[string]string{"MODEL": "sonnet", "PROMPT": "x"}},
		"another mode's parameter":        {ext.ModeInteractive, map[string]string{"PROMPT": "x"}},
		"an undeclared parameter":         {ext.ModeHeadless, map[string]string{"NOPE": "x", "PROMPT": "x"}},
		"no mode and no mode's parameter": {"", map[string]string{"IMAGE": "evil:latest"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, previewErr := contractPreview(t, cfg, tc.mode, tc.params)
			if !errors.Is(previewErr, errdefs.ErrBlueprintInvalid) || !strings.Contains(previewErr.Error(), "--param") {
				t.Fatalf("err = %v, want the --param rejection (ErrBlueprintInvalid)", previewErr)
			}
		})
	}
}

// TestPreviewCellFromConfig_ModeInferredFromParam: a create that names no mode
// but supplies a parameter only one mode declares is created in that mode —
// by --param, and by --env as dispatches were written before modes existed.
func TestPreviewCellFromConfig_ModeInferredFromParam(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"api-key": {SecretRef: contractSecretRef("anthropic")}}

	preview, err := contractPreview(t, cfg, "", map[string]string{"PROMPT": "from cell"})
	if err != nil {
		t.Fatalf("PreviewCellFromConfig: %v", err)
	}
	if got := preview.Cell.Spec.Provenance.Mode; got != ext.ModeHeadless {
		t.Errorf("provenance mode = %q, want the inferred headless", got)
	}
	if !containsString(preview.Cell.Spec.Containers[0].Env, "PROMPT=from cell") {
		t.Errorf("env = %v, want PROMPT from the per-cell param", preview.Cell.Spec.Containers[0].Env)
	}
}

// TestPreviewCellFromConfig_ModeMovesDigest: the mode is part of the cell, so
// the preview → create handshake refuses a create in another mode.
func TestPreviewCellFromConfig_ModeMovesDigest(t *testing.T) {
	cfg := renderConfig()
	cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{"api-key": {SecretRef: contractSecretRef("anthropic")}}
	interactive, err := contractPreview(t, cfg, "", nil)
	if err != nil {
		t.Fatalf("interactive preview: %v", err)
	}
	headless, err := contractPreview(t, cfg, ext.ModeHeadless, map[string]string{"PROMPT": "x"})
	if err != nil {
		t.Fatalf("headless preview: %v", err)
	}
	if interactive.Digest == headless.Digest {
		t.Errorf("interactive and headless renders share digest %q", interactive.Digest)
	}
	explicit, err := contractPreview(t, cfg, ext.ModeInteractive, nil)
	if err != nil {
		t.Fatalf("explicit interactive preview: %v", err)
	}
	if explicit.Digest != interactive.Digest {
		t.Errorf("naming the default mode moved the digest: %q vs %q", explicit.Digest, interactive.Digest)
	}
}

// TestReconcileCells_OutOfSync_ContractCellStaysSynced is the bindingFromCell
// round-trip: the reconciler re-renders a live cell from its recorded mode and
// per-cell params, so a headless cell is not re-rendered as an interactive one
// (which would flag every dispatched run OutOfSync) and does not lose PROMPT.
func TestReconcileCells_OutOfSync_ContractCellStaysSynced(t *testing.T) {
	for name, tc := range map[string]struct {
		mode   string
		params map[string]string
	}{
		"default mode":             {"", nil},
		"headless with PROMPT":     {ext.ModeHeadless, map[string]string{"PROMPT": "fix the build"}},
		"explicitly named default": {ext.ModeInteractive, nil},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := renderConfig()
			cfg.Spec.Components = []string{"github"}
			cfg.Spec.Secrets = map[string]ext.CellConfigSecretFill{
				"api-key":  {SecretRef: contractSecretRef("anthropic")},
				"gh-token": {SecretRef: contractSecretRef("gh")},
			}
			mock := newContractRunner(t, cfg)
			req := previewRequest()
			req.Mode, req.Params = tc.mode, tc.params
			preview, err := setupTestController(t, mock).PreviewCellFromConfig(req)
			if err != nil {
				t.Fatalf("render live cell: %v", err)
			}
			live, err := apischeme.ConvertCellDocToInternal(preview.Cell)
			if err != nil {
				t.Fatalf("convert live cell: %v", err)
			}
			if tc.mode == ext.ModeHeadless && (live.Spec.Provenance.Mode != ext.ModeHeadless ||
				live.Spec.Provenance.ParamOverrides["PROMPT"] != "fix the build") {
				t.Fatalf("live provenance = %+v, want the headless binding recorded", live.Spec.Provenance)
			}

			mock.ListRealmsFn = func() ([]intmodel.Realm, error) {
				return []intmodel.Realm{buildTestRealm("kuke-system", "")}, nil
			}
			mock.ListSpacesFn = func(string) ([]intmodel.Space, error) {
				return []intmodel.Space{buildTestSpace("default", "kuke-system")}, nil
			}
			mock.ListStacksFn = func(string, string) ([]intmodel.Stack, error) {
				return []intmodel.Stack{buildTestStack("default", "kuke-system", "default")}, nil
			}
			mock.ReconcileCellFn = passthroughReconcile
			res, captured := runOutOfSyncHarness(t, live, mock)
			if captured != nil {
				t.Errorf("unexpected OutOfSync write for an unchanged contract cell: OutOfSync=%v reason=%q err=%q",
					captured.Status.OutOfSync, captured.Status.OutOfSyncReason, captured.Status.OutOfSyncError)
			}
			if res.CellsErrored != 0 {
				t.Errorf("reconcile errors: %v", res.Errors)
			}
		})
	}
}
