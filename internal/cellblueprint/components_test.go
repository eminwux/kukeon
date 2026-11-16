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

package cellblueprint_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// harnessDoc is the epic's canonical shape (#1472): one always-on harness
// component carrying two modes, plus an optional connector, landing on the
// attachable "work" container next to a root container.
func harnessDoc() v1beta1.CellBlueprintDoc {
	return v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata:   v1beta1.CellBlueprintMetadata{Name: "claude", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{
			Prefix:   "claude",
			Requires: []string{"git"},
			Parameters: []v1beta1.CellBlueprintParameter{
				{Name: "IMAGE", Type: v1beta1.ParameterTypeImage, Default: strptr("registry.example.com/claude:v1")},
			},
			Components: []v1beta1.BlueprintComponent{
				{
					Kind:     v1beta1.ComponentKindHarness,
					Name:     "claude",
					Requires: []string{"claude", "git"},
					Parameters: []v1beta1.CellBlueprintParameter{{
						Name: "ANTHROPIC_MODEL", Type: v1beta1.ParameterTypeEnum,
						Enum: []string{"opus", "sonnet"}, Default: strptr("opus"), Group: "harness",
					}},
					Env: []string{"ANTHROPIC_MODEL=${ANTHROPIC_MODEL}"},
					Modes: map[string]v1beta1.BlueprintComponentMode{
						v1beta1.ModeInteractive: {
							OnInit: []v1beta1.TtyStage{{Script: "exec claude"}},
						},
						v1beta1.ModeHeadless: {
							OnInit:     []v1beta1.TtyStage{{Script: `exec claude -p "$PROMPT"`}},
							Parameters: []v1beta1.CellBlueprintParameter{{Name: "PROMPT", Required: true}},
							Env:        []string{"PROMPT=${PROMPT}"},
							Secrets: []v1beta1.BlueprintSecretSlot{
								{Name: "api-key", EnvName: "ANTHROPIC_API_KEY", Required: true},
							},
						},
					},
				},
				{
					Kind:     v1beta1.ComponentKindConnector,
					Name:     "github",
					Optional: true,
					Requires: []string{"gh"},
					Env:      []string{"GH_PROMPT_DISABLED=1"},
					Secrets: []v1beta1.BlueprintSecretSlot{
						{Name: "gh-token", EnvName: "GH_TOKEN", Required: true},
					},
				},
			},
			Cell: v1beta1.BlueprintCellSpec{
				Containers: []v1beta1.BlueprintContainer{
					{ID: "root", Root: true, Image: "registry.example.com/pause:1"},
					{
						ID: "work", Image: "${IMAGE}", Attachable: true,
						Env: []string{"EDITOR=vi"},
						Tty: &v1beta1.ContainerTty{Prompt: "> ", OnInit: []v1beta1.TtyStage{{Script: "echo base"}}},
					},
				},
			},
		},
	}
}

func container(t *testing.T, doc v1beta1.CellBlueprintDoc, id string) v1beta1.BlueprintContainer {
	t.Helper()
	for _, c := range doc.Spec.Cell.Containers {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("container %q not found", id)
	return v1beta1.BlueprintContainer{}
}

func paramNames(doc v1beta1.CellBlueprintDoc) []string {
	var out []string
	for _, p := range doc.Spec.Parameters {
		out = append(out, p.Name)
	}
	return out
}

func slotNames(c v1beta1.BlueprintContainer) []string {
	var out []string
	for _, s := range c.Secrets {
		out = append(out, s.Name)
	}
	return out
}

func TestFlatten_NoComponentsOnlyNormalizesRequires(t *testing.T) {
	doc := baseDoc()
	doc.Spec.Requires = []string{" gh ", "git", "gh", ""}
	flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{})
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if want := []string{"gh", "git"}; !reflect.DeepEqual(flat.Doc.Spec.Requires, want) {
		t.Errorf("Requires = %v, want %v", flat.Doc.Spec.Requires, want)
	}
	if flat.Mode != "" || len(flat.Components) != 0 || len(flat.ModeParameters) != 0 {
		t.Errorf("component-less blueprint reported mode %q components %v mode params %v",
			flat.Mode, flat.Components, flat.ModeParameters)
	}
	flat.Doc.Spec.Requires = doc.Spec.Requires
	if !reflect.DeepEqual(flat.Doc, doc) {
		t.Errorf("component-less blueprint changed beyond Requires:\n got %+v\nwant %+v", flat.Doc, doc)
	}
}

// TestFlatten_RequiredOnOptionalOff locks the default view: the always-on
// harness is folded into the attachable container, the optional connector
// contributes nothing until a Config enables it.
func TestFlatten_RequiredOnOptionalOff(t *testing.T) {
	flat, err := cellblueprint.Flatten(harnessDoc(), cellblueprint.FlattenOptions{})
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !reflect.DeepEqual(flat.Components, []string{"claude"}) {
		t.Errorf("Components = %v, want only the non-optional harness", flat.Components)
	}
	if len(flat.Doc.Spec.Components) != 0 {
		t.Errorf("flattened doc still carries components: %v", flat.Doc.Spec.Components)
	}
	if want := []string{"IMAGE", "ANTHROPIC_MODEL"}; !reflect.DeepEqual(paramNames(flat.Doc), want) {
		t.Errorf("parameters = %v, want %v", paramNames(flat.Doc), want)
	}
	if want := []string{"claude", "git"}; !reflect.DeepEqual(flat.Doc.Spec.Requires, want) {
		t.Errorf("Requires = %v, want the de-duplicated union %v", flat.Doc.Spec.Requires, want)
	}
	work := container(t, flat.Doc, "work")
	if want := []string{"EDITOR=vi", "ANTHROPIC_MODEL=${ANTHROPIC_MODEL}"}; !reflect.DeepEqual(work.Env, want) {
		t.Errorf("work env = %v, want %v", work.Env, want)
	}
	if len(work.Secrets) != 0 {
		t.Errorf("work secrets = %v, want none (connector off, interactive mode has none)", slotNames(work))
	}
	if root := container(t, flat.Doc, "root"); len(root.Env) != 0 || len(root.Secrets) != 0 {
		t.Errorf("root container received component contributions: env %v secrets %v", root.Env, slotNames(root))
	}
}

func TestFlatten_EnablesOptionalComponent(t *testing.T) {
	flat, err := cellblueprint.Flatten(harnessDoc(), cellblueprint.FlattenOptions{
		// Naming a non-optional component is harmless.
		Components: []string{"github", "claude"},
	})
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if want := []string{"claude", "github"}; !reflect.DeepEqual(flat.Components, want) {
		t.Errorf("Components = %v, want declaration order %v", flat.Components, want)
	}
	if want := []string{"claude", "gh", "git"}; !reflect.DeepEqual(flat.Doc.Spec.Requires, want) {
		t.Errorf("Requires = %v, want %v", flat.Doc.Spec.Requires, want)
	}
	work := container(t, flat.Doc, "work")
	if !slices.Contains(work.Env, "GH_PROMPT_DISABLED=1") {
		t.Errorf("work env = %v, want the connector's env", work.Env)
	}
	if len(work.Secrets) != 1 || work.Secrets[0].Name != "gh-token" || !work.Secrets[0].Required {
		t.Errorf("work secrets = %+v, want the connector's required gh-token slot", work.Secrets)
	}
}

func TestFlatten_UnknownComponent(t *testing.T) {
	_, err := cellblueprint.Flatten(harnessDoc(), cellblueprint.FlattenOptions{Components: []string{"gitlab"}})
	if !errors.Is(err, errdefs.ErrBlueprintComponent) {
		t.Fatalf("err = %v, want ErrBlueprintComponent", err)
	}
	if !strings.Contains(err.Error(), `"gitlab"`) {
		t.Errorf("err = %v, want it to name the unknown component", err)
	}
}

func TestFlatten_DoesNotMutateInput(t *testing.T) {
	doc := harnessDoc()
	want := harnessDoc()
	if _, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{
		Components: []string{"github"}, Mode: v1beta1.ModeHeadless,
	}); err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("Flatten mutated its input:\n got %+v\nwant %+v", doc, want)
	}
}

func TestFlatten_TargetContainer(t *testing.T) {
	t.Run("explicit container", func(t *testing.T) {
		doc := harnessDoc()
		doc.Spec.Cell.Containers = append(doc.Spec.Cell.Containers,
			v1beta1.BlueprintContainer{ID: "side", Image: "registry.example.com/side:1"})
		doc.Spec.Components[1].Container = "side"
		flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{Components: []string{"github"}})
		if err != nil {
			t.Fatalf("Flatten: %v", err)
		}
		if side := container(t, flat.Doc, "side"); !reflect.DeepEqual(slotNames(side), []string{"gh-token"}) {
			t.Errorf("side secrets = %v, want the connector's slot", slotNames(side))
		}
		if work := container(t, flat.Doc, "work"); len(work.Secrets) != 0 {
			t.Errorf("work secrets = %v, want none", slotNames(work))
		}
	})

	t.Run("cell default attach container", func(t *testing.T) {
		doc := harnessDoc()
		doc.Spec.Cell.Containers = append(doc.Spec.Cell.Containers,
			v1beta1.BlueprintContainer{ID: "shell", Image: "registry.example.com/sh:1", Attachable: true})
		doc.Spec.Cell.Tty = &v1beta1.CellTty{Default: "shell"}
		flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{})
		if err != nil {
			t.Fatalf("Flatten: %v", err)
		}
		if shell := container(t, flat.Doc, "shell"); !slices.Contains(shell.Env, "ANTHROPIC_MODEL=${ANTHROPIC_MODEL}") {
			t.Errorf("shell env = %v, want the harness env on the default attach container", shell.Env)
		}
	})

	t.Run("first non-root when nothing is attachable", func(t *testing.T) {
		doc := harnessDoc()
		doc.Spec.Cell.Containers[1].Attachable = false
		flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{})
		if err != nil {
			t.Fatalf("Flatten: %v", err)
		}
		if work := container(t, flat.Doc, "work"); !slices.Contains(work.Env, "ANTHROPIC_MODEL=${ANTHROPIC_MODEL}") {
			t.Errorf("work env = %v, want the harness env", work.Env)
		}
	})

	t.Run("unknown container", func(t *testing.T) {
		doc := harnessDoc()
		doc.Spec.Components[0].Container = "nope"
		_, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{})
		if !errors.Is(err, errdefs.ErrBlueprintComponent) {
			t.Fatalf("err = %v, want ErrBlueprintComponent", err)
		}
	})

	t.Run("only a root container", func(t *testing.T) {
		doc := harnessDoc()
		doc.Spec.Cell.Containers = doc.Spec.Cell.Containers[:1]
		_, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{})
		if !errors.Is(err, errdefs.ErrBlueprintComponent) {
			t.Fatalf("err = %v, want ErrBlueprintComponent", err)
		}
	})
}

func TestFlatten_Conflicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1beta1.CellBlueprintDoc)
		want   string
	}{
		{
			name: "parameter redeclared",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Parameters = []v1beta1.CellBlueprintParameter{{Name: "IMAGE"}}
			},
			want: `redeclares parameter "IMAGE"`,
		},
		{
			name: "env key already set by the container",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Env = []string{"EDITOR=nano"}
			},
			want: `sets env "EDITOR"`,
		},
		{
			name: "env key already set by another component",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Env = []string{"ANTHROPIC_MODEL=haiku"}
			},
			want: `sets env "ANTHROPIC_MODEL"`,
		},
		{
			name: "env key is an env-mode secret destination",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Cell.Containers[1].Secrets = []v1beta1.BlueprintSecretSlot{{Name: "x", EnvName: "GH_PROMPT_DISABLED"}}
			},
			want: `sets env "GH_PROMPT_DISABLED"`,
		},
		{
			name: "secret slot name already declared",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Cell.Containers[1].Secrets = []v1beta1.BlueprintSecretSlot{{Name: "gh-token", EnvName: "OTHER"}}
			},
			want: "slot name already declared",
		},
		{
			name: "secret env destination already used",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Cell.Containers[1].Secrets = []v1beta1.BlueprintSecretSlot{{Name: "other", EnvName: "GH_TOKEN"}}
			},
			want: `env destination "GH_TOKEN" already used by slot "other"`,
		},
		{
			name: "secret env destination is a literal container env",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Cell.Containers[1].Env = append(d.Spec.Cell.Containers[1].Env, "GH_TOKEN=literal")
			},
			want: `env destination "GH_TOKEN" is already set by the container`,
		},
		{
			name: "secret mount path already used",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Cell.Containers[1].Secrets = []v1beta1.BlueprintSecretSlot{
					{Name: "a", Mode: v1beta1.BlueprintSecretModeFile, MountPath: "/run/token"},
				}
				d.Spec.Components[1].Secrets = []v1beta1.BlueprintSecretSlot{
					{Name: "b", Mode: v1beta1.BlueprintSecretModeFile, MountPath: "/run/token"},
				}
			},
			want: `mount path "/run/token" already used by slot "a"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := harnessDoc()
			tc.mutate(&doc)
			_, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{Components: []string{"github"}})
			if !errors.Is(err, errdefs.ErrBlueprintComponentConflict) {
				t.Fatalf("err = %v, want ErrBlueprintComponentConflict", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
			// A conflict lives only in the enabled set: with the connector
			// off, the same blueprint flattens.
			if _, err = cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{}); err != nil {
				t.Errorf("Flatten with the conflicting optional component off: %v", err)
			}
		})
	}
}

// TestFlatten_ModePickOrder walks the documented resolution: the requested
// mode, else the Config default, else the only enabled mode, else
// "interactive", else the first enabled mode by name.
func TestFlatten_ModePickOrder(t *testing.T) {
	withBatch := func() v1beta1.CellBlueprintDoc {
		doc := harnessDoc()
		doc.Spec.Components[0].Modes["batch"] = v1beta1.BlueprintComponentMode{}
		return doc
	}
	for _, tc := range []struct {
		name string
		doc  v1beta1.CellBlueprintDoc
		opts cellblueprint.FlattenOptions
		want string
	}{
		{
			name: "requested wins over the default",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{Mode: v1beta1.ModeHeadless, DefaultMode: v1beta1.ModeInteractive},
			want: v1beta1.ModeHeadless,
		},
		{
			name: "config default",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{DefaultMode: v1beta1.ModeHeadless},
			want: v1beta1.ModeHeadless,
		},
		{
			name: "only enabled mode",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{AllowedModes: []string{v1beta1.ModeHeadless}},
			want: v1beta1.ModeHeadless,
		},
		{
			name: "interactive when nothing narrows it",
			doc:  withBatch(),
			opts: cellblueprint.FlattenOptions{},
			want: v1beta1.ModeInteractive,
		},
		{
			name: "first enabled by name without interactive",
			doc:  withBatch(),
			opts: cellblueprint.FlattenOptions{AllowedModes: []string{v1beta1.ModeHeadless, "batch"}},
			want: "batch",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flat, err := cellblueprint.Flatten(tc.doc, tc.opts)
			if err != nil {
				t.Fatalf("Flatten: %v", err)
			}
			if flat.Mode != tc.want {
				t.Errorf("Mode = %q, want %q", flat.Mode, tc.want)
			}
		})
	}
}

func TestFlatten_ModeRejections(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  v1beta1.CellBlueprintDoc
		opts cellblueprint.FlattenOptions
		want string
	}{
		{
			name: "requested mode not declared",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{Mode: "batch"},
			want: `declares no mode "batch"`,
		},
		{
			name: "requested mode not enabled",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{
				Mode: v1beta1.ModeHeadless, AllowedModes: []string{v1beta1.ModeInteractive},
			},
			want: `mode "headless" is not enabled`,
		},
		{
			name: "enabled mode not declared",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{AllowedModes: []string{"batch"}},
			want: `declares no mode "batch"`,
		},
		{
			name: "default mode not enabled",
			doc:  harnessDoc(),
			opts: cellblueprint.FlattenOptions{
				DefaultMode: v1beta1.ModeHeadless, AllowedModes: []string{v1beta1.ModeInteractive},
			},
			want: `mode "headless" is not enabled`,
		},
		{
			name: "mode on a blueprint without modes",
			doc:  baseDoc(),
			opts: cellblueprint.FlattenOptions{Mode: v1beta1.ModeInteractive},
			want: "declares no harness modes",
		},
		{
			name: "enabled modes on a blueprint without modes",
			doc:  baseDoc(),
			opts: cellblueprint.FlattenOptions{AllowedModes: []string{v1beta1.ModeInteractive}},
			want: "declares no harness modes",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cellblueprint.Flatten(tc.doc, tc.opts)
			if !errors.Is(err, errdefs.ErrBlueprintMode) {
				t.Fatalf("err = %v, want ErrBlueprintMode", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestFlatten_ModeContributions asserts only the picked mode's onInit, env,
// secrets and parameters land on the harness container — so a required
// API-key slot is checked for headless cells and never for interactive ones.
func TestFlatten_ModeContributions(t *testing.T) {
	interactive, err := cellblueprint.Flatten(harnessDoc(), cellblueprint.FlattenOptions{})
	if err != nil {
		t.Fatalf("Flatten interactive: %v", err)
	}
	work := container(t, interactive.Doc, "work")
	if interactive.Mode != v1beta1.ModeInteractive || len(interactive.ModeParameters) != 0 {
		t.Errorf("interactive: mode %q mode params %v", interactive.Mode, interactive.ModeParameters)
	}
	if work.Tty == nil || len(work.Tty.OnInit) != 1 || work.Tty.OnInit[0].Script != "exec claude" {
		t.Errorf("interactive onInit = %+v, want the mode's entrypoint replacing the container's", work.Tty)
	}
	if work.Tty != nil && work.Tty.Prompt != "> " {
		t.Errorf("interactive tty prompt = %q, want the container's prompt kept", work.Tty.Prompt)
	}
	if len(work.Secrets) != 0 || slices.Contains(paramNames(interactive.Doc), "PROMPT") {
		t.Errorf("interactive carries headless contributions: secrets %v params %v",
			slotNames(work), paramNames(interactive.Doc))
	}

	headless, err := cellblueprint.Flatten(harnessDoc(), cellblueprint.FlattenOptions{Mode: v1beta1.ModeHeadless})
	if err != nil {
		t.Fatalf("Flatten headless: %v", err)
	}
	work = container(t, headless.Doc, "work")
	if !reflect.DeepEqual(headless.ModeParameters, []string{"PROMPT"}) {
		t.Errorf("ModeParameters = %v, want [PROMPT]", headless.ModeParameters)
	}
	if work.Tty == nil || len(work.Tty.OnInit) != 1 || !strings.Contains(work.Tty.OnInit[0].Script, "-p") {
		t.Errorf("headless onInit = %+v, want the headless entrypoint", work.Tty)
	}
	if !slices.Contains(work.Env, "PROMPT=${PROMPT}") {
		t.Errorf("headless env = %v, want PROMPT", work.Env)
	}
	if len(work.Secrets) != 1 || work.Secrets[0].Name != "api-key" || !work.Secrets[0].Required {
		t.Errorf("headless secrets = %+v, want the required api-key slot", work.Secrets)
	}
	var prompt *v1beta1.CellBlueprintParameter
	for i := range headless.Doc.Spec.Parameters {
		if headless.Doc.Spec.Parameters[i].Name == "PROMPT" {
			prompt = &headless.Doc.Spec.Parameters[i]
		}
	}
	if prompt == nil || !prompt.Required {
		t.Errorf("headless PROMPT parameter = %+v, want it declared and required", prompt)
	}
}

// TestFlatten_ModeWithoutOnInitKeepsContainerTty asserts a mode that declares
// no onInit leaves the container's own stages alone.
func TestFlatten_ModeWithoutOnInitKeepsContainerTty(t *testing.T) {
	doc := harnessDoc()
	doc.Spec.Components[0].Modes["batch"] = v1beta1.BlueprintComponentMode{}
	flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{Mode: "batch"})
	if err != nil {
		t.Fatalf("Flatten: %v", err)
	}
	work := container(t, flat.Doc, "work")
	if work.Tty == nil || len(work.Tty.OnInit) != 1 || work.Tty.OnInit[0].Script != "echo base" {
		t.Errorf("onInit = %+v, want the container's own stage", work.Tty)
	}
}

// TestFlatten_AllView locks the apply-time validation view: every component
// and every mode folded in, nothing an optional component or a mode adds is
// required, no mode's onInit applied, and no mode reported as picked.
func TestFlatten_AllView(t *testing.T) {
	doc := harnessDoc()
	// Alternative modes legitimately repeat each other; the All view tolerates it.
	interactive := doc.Spec.Components[0].Modes[v1beta1.ModeInteractive]
	interactive.Parameters = []v1beta1.CellBlueprintParameter{{Name: "PROMPT"}}
	interactive.Env = []string{"PROMPT=${PROMPT}"}
	doc.Spec.Components[0].Modes[v1beta1.ModeInteractive] = interactive
	// A mandatory component's own required slot stays required.
	doc.Spec.Components[0].Secrets = []v1beta1.BlueprintSecretSlot{
		{Name: "harness-state", EnvName: "STATE_KEY", Required: true},
	}

	flat, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{All: true})
	if err != nil {
		t.Fatalf("Flatten All: %v", err)
	}
	if flat.Mode != "" || len(flat.ModeParameters) != 0 {
		t.Errorf("All view picked mode %q / mode params %v, want none", flat.Mode, flat.ModeParameters)
	}
	if want := []string{"claude", "github"}; !reflect.DeepEqual(flat.Components, want) {
		t.Errorf("Components = %v, want every component %v", flat.Components, want)
	}
	if want := []string{"claude", "gh", "git"}; !reflect.DeepEqual(flat.Doc.Spec.Requires, want) {
		t.Errorf("Requires = %v, want %v", flat.Doc.Spec.Requires, want)
	}
	if want := []string{"IMAGE", "ANTHROPIC_MODEL", "PROMPT"}; !reflect.DeepEqual(paramNames(flat.Doc), want) {
		t.Errorf("parameters = %v, want %v (PROMPT once)", paramNames(flat.Doc), want)
	}
	for _, p := range flat.Doc.Spec.Parameters {
		if p.Name == "PROMPT" && p.Required {
			t.Errorf("mode parameter PROMPT is required in the All view")
		}
	}
	work := container(t, flat.Doc, "work")
	required := map[string]bool{}
	for _, s := range work.Secrets {
		required[s.Name] = s.Required
	}
	want := map[string]bool{"harness-state": true, "api-key": false, "gh-token": false}
	if !reflect.DeepEqual(required, want) {
		t.Errorf("secret slots (name → required) = %v, want %v", required, want)
	}
	if work.Tty == nil || len(work.Tty.OnInit) != 1 || work.Tty.OnInit[0].Script != "echo base" {
		t.Errorf("All view applied a mode onInit: %+v", work.Tty)
	}
	// Mode options are ignored rather than rejected in the validation view.
	if _, err = cellblueprint.Flatten(baseDoc(), cellblueprint.FlattenOptions{All: true, Mode: "x"}); err != nil {
		t.Errorf("All view on a mode-less blueprint: %v", err)
	}
}

func TestModes(t *testing.T) {
	if got := cellblueprint.Modes(baseDoc()); got != nil {
		t.Errorf("Modes(no components) = %v, want nil", got)
	}
	want := []string{v1beta1.ModeHeadless, v1beta1.ModeInteractive}
	if got := cellblueprint.Modes(harnessDoc()); !reflect.DeepEqual(got, want) {
		t.Errorf("Modes = %v, want sorted %v", got, want)
	}
}

func TestNormalizeRequires(t *testing.T) {
	got := cellblueprint.NormalizeRequires([]string{"gh", " git ", "", "gh", "claude", "  "})
	if want := []string{"claude", "gh", "git"}; !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizeRequires = %v, want %v", got, want)
	}
	if got = cellblueprint.NormalizeRequires(nil); got != nil {
		t.Errorf("NormalizeRequires(nil) = %v, want nil", got)
	}
}

func TestValidateDeclaration_Accepts(t *testing.T) {
	if err := cellblueprint.ValidateDeclaration(harnessDoc()); err != nil {
		t.Fatalf("ValidateDeclaration(harness): %v", err)
	}
	if err := cellblueprint.ValidateDeclaration(baseDoc()); err != nil {
		t.Fatalf("ValidateDeclaration(component-less): %v", err)
	}
}

func TestValidateDeclaration_Rejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1beta1.CellBlueprintDoc)
		sentry error
		want   string
	}{
		{
			name: "untyped top-level parameter",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Parameters[0].Type = "integer"
			},
			sentry: errdefs.ErrBlueprintParameterType,
			want:   "spec.parameters[0]",
		},
		{
			name:   "component name",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Name = "GitHub" },
			sentry: errdefs.ErrBlueprintComponent,
			want:   "must be a lowercase identifier",
		},
		{
			name:   "duplicate component",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Name = "claude" },
			sentry: errdefs.ErrBlueprintComponent,
			want:   "declared twice",
		},
		{
			name:   "component kind",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Kind = "plugin" },
			sentry: errdefs.ErrBlueprintComponent,
			want:   "must be harness, connector or forge",
		},
		{
			name: "component parameter type",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Parameters = []v1beta1.CellBlueprintParameter{{Name: "E", Type: "enum"}}
			},
			sentry: errdefs.ErrBlueprintParameterType,
			want:   "spec.components[1].parameters[0]",
		},
		{
			name:   "component env entry",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Env = []string{"NOVALUE"} },
			sentry: errdefs.ErrBlueprintComponent,
			want:   "want KEY=value",
		},
		{
			name: "modes on a connector",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Modes = map[string]v1beta1.BlueprintComponentMode{"x": {}}
				d.Spec.Components[0].Modes = nil
			},
			sentry: errdefs.ErrBlueprintComponent,
			want:   "only a harness component declares modes",
		},
		{
			name: "two mode owners",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Kind = v1beta1.ComponentKindHarness
				d.Spec.Components[1].Modes = map[string]v1beta1.BlueprintComponentMode{"x": {}}
			},
			sentry: errdefs.ErrBlueprintComponent,
			want:   "only one component may declare modes",
		},
		{
			name: "mode name",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[0].Modes["Head Less"] = v1beta1.BlueprintComponentMode{}
			},
			sentry: errdefs.ErrBlueprintMode,
			want:   "mode name must be a lowercase identifier",
		},
		{
			name: "mode parameter type",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				m := d.Spec.Components[0].Modes[v1beta1.ModeHeadless]
				m.Parameters = []v1beta1.CellBlueprintParameter{{Name: "PROMPT", Type: "text"}}
				d.Spec.Components[0].Modes[v1beta1.ModeHeadless] = m
			},
			sentry: errdefs.ErrBlueprintParameterType,
			want:   "spec.components[0].modes.headless.parameters[0]",
		},
		{
			name: "mode env entry",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				m := d.Spec.Components[0].Modes[v1beta1.ModeHeadless]
				m.Env = []string{"=x"}
				d.Spec.Components[0].Modes[v1beta1.ModeHeadless] = m
			},
			sentry: errdefs.ErrBlueprintComponent,
			want:   "spec.components[0].modes.headless.env[0]",
		},
		{
			name:   "component targets an unknown container",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Container = "nope" },
			sentry: errdefs.ErrBlueprintComponent,
			want:   `unknown container "nope"`,
		},
		{
			// A clash among always-on parts would fail every cell, so it is
			// refused at apply time even though the All view tolerates repeats.
			name: "mandatory component clashes with the cell template",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[0].Env = []string{"EDITOR=nano"}
			},
			sentry: errdefs.ErrBlueprintComponentConflict,
			want:   `sets env "EDITOR"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := harnessDoc()
			tc.mutate(&doc)
			err := cellblueprint.ValidateDeclaration(doc)
			if !errors.Is(err, tc.sentry) {
				t.Fatalf("err = %v, want %v", err, tc.sentry)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want substring %q", err, tc.want)
			}
		})
	}
}

// TestResolve_FlattensComponentsInline covers `kuke run -b` on a
// component-bearing blueprint: always-on components and the default mode are
// folded in before parameters resolve, so a component parameter is a normal
// --param and its env substitutes like any other field.
func TestResolve_FlattensComponentsInline(t *testing.T) {
	out, err := cellblueprint.Resolve(harnessDoc(), map[string]string{"ANTHROPIC_MODEL": "sonnet"}, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(out.Spec.Components) != 0 {
		t.Errorf("resolved blueprint still carries components")
	}
	work := container(t, out, "work")
	if work.Image != "registry.example.com/claude:v1" {
		t.Errorf("image = %q, want the IMAGE default substituted", work.Image)
	}
	if !slices.Contains(work.Env, "ANTHROPIC_MODEL=sonnet") {
		t.Errorf("env = %v, want the component env with --param substituted", work.Env)
	}
	if slices.ContainsFunc(work.Env, func(e string) bool { return strings.HasPrefix(e, "GH_") }) {
		t.Errorf("env = %v, want the optional connector left off", work.Env)
	}
	if work.Tty == nil || len(work.Tty.OnInit) != 1 || work.Tty.OnInit[0].Script != "exec claude" {
		t.Errorf("onInit = %+v, want the default (interactive) mode's entrypoint", work.Tty)
	}

	// The component parameter keeps its type on the inline path.
	_, err = cellblueprint.Resolve(harnessDoc(), map[string]string{"ANTHROPIC_MODEL": "haiku"}, nil)
	if !errors.Is(err, errdefs.ErrBlueprintParameterType) {
		t.Fatalf("err = %v, want ErrBlueprintParameterType for a value outside the component's enum", err)
	}
	// A parameter only the non-default mode declares is not settable inline.
	if _, err = cellblueprint.Resolve(harnessDoc(), map[string]string{"PROMPT": "hi"}, nil); err == nil {
		t.Fatal("expected an undeclared-parameter error for a headless-only parameter on the inline path")
	}
}

// TestFlatten_ModeOfDisabledHarnessRejected: a requested mode whose harness
// component is optional and not enabled is an error, not a cell in no mode.
func TestFlatten_ModeOfDisabledHarnessRejected(t *testing.T) {
	doc := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{Name: "bp", Realm: "default"},
		Spec: v1beta1.CellBlueprintSpec{
			Components: []v1beta1.BlueprintComponent{{
				Kind: v1beta1.ComponentKindHarness, Name: "agent", Optional: true,
				Modes: map[string]v1beta1.BlueprintComponentMode{v1beta1.ModeHeadless: {}},
			}},
			Cell: v1beta1.BlueprintCellSpec{Containers: []v1beta1.BlueprintContainer{{ID: "work", Image: "img"}}},
		},
	}
	_, err := cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{Mode: v1beta1.ModeHeadless})
	if !errors.Is(err, errdefs.ErrBlueprintMode) {
		t.Fatalf("err = %v, want ErrBlueprintMode for a mode of a disabled component", err)
	}
	if _, err = cellblueprint.Flatten(doc, cellblueprint.FlattenOptions{
		Mode: v1beta1.ModeHeadless, Components: []string{"agent"},
	}); err != nil {
		t.Fatalf("enabled harness: %v", err)
	}
}
