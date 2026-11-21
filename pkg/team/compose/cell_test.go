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

package compose_test

import (
	"maps"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
)

const (
	interactive = "interactive"
	headless    = "headless"
)

// renderCell produces the cell a pair's CellConfig stamps in one harness mode,
// the way the daemon does: flatten the shared blueprint for the Config's
// enabled components and the picked mode, drop the Config's fills for slots
// that mode does not use, layer the Realm defaults `kuke team` writes for the
// slots the Config leaves to them (issue #1548), then materialize. params are
// per-cell values (the PROMPT of a headless run). mode is empty for a harness
// without modes.
func renderCell(t *testing.T, pair compose.Pair, mode string, params map[string]string) api.CellDoc {
	t.Helper()
	flat, err := cellblueprint.Flatten(*pair.Blueprint, cellblueprint.FlattenOptions{
		Components: pair.Config.Spec.Components, Mode: mode,
	})
	if err != nil {
		t.Fatalf("flatten %s for %s: %v", pair.Blueprint.Metadata.Name, pair.Config.Metadata.Name, err)
	}
	all, err := cellblueprint.Flatten(*pair.Blueprint, cellblueprint.FlattenOptions{All: true})
	if err != nil {
		t.Fatalf("flatten all %s: %v", pair.Blueprint.Metadata.Name, err)
	}
	slots := func(doc api.CellBlueprintDoc) map[string]bool {
		out := map[string]bool{}
		for _, c := range doc.Spec.Cell.Containers {
			for _, slot := range c.Secrets {
				out[slot.Name] = true
			}
		}
		return out
	}
	declared, active := slots(all.Doc), slots(flat.Doc)
	cfg := *pair.Config
	cfg.Spec.Values = maps.Clone(pair.Config.Spec.Values)
	if cfg.Spec.Values == nil {
		cfg.Spec.Values = map[string]string{}
	}
	maps.Copy(cfg.Spec.Values, params)
	cfg.Spec.Secrets = map[string]api.CellConfigSecretFill{}
	for name, fill := range pair.Config.Spec.Secrets {
		if !declared[name] {
			t.Fatalf("%s fills secret slot %q the blueprint never declares", cfg.Metadata.Name, name)
		}
		if active[name] {
			cfg.Spec.Secrets[name] = fill
		}
	}
	// A slot the Config does not fill resolves to the Realm default.
	for _, required := range pair.Secrets {
		name := required.Destination.Name
		if _, filled := cfg.Spec.Secrets[name]; filled || !required.Default || !active[name] {
			continue
		}
		ref := required.Reference
		cfg.Spec.Secrets[name] = api.CellConfigSecretFill{SecretRef: &api.ContainerSecretRef{
			Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
		}}
	}
	doc, err := cellconfig.MaterializeWithName(cfg, flat.Doc, "cell-under-test")
	if err != nil {
		t.Fatalf("materialize %s in mode %q: %v", cfg.Metadata.Name, mode, err)
	}
	return doc
}

// workOf returns the agent container of a rendered cell.
func workOf(t *testing.T, doc api.CellDoc) api.ContainerSpec {
	t.Helper()
	for _, c := range doc.Spec.Containers {
		if c.ID == "work" {
			return c
		}
	}
	t.Fatal("rendered cell has no work container")
	return api.ContainerSpec{}
}

// work renders a pair in a mode and returns its agent container.
func work(t *testing.T, pair compose.Pair, mode string) api.ContainerSpec {
	t.Helper()
	var params map[string]string
	if mode == headless {
		params = map[string]string{"PROMPT": "do the thing"}
	}
	return workOf(t, renderCell(t, pair, mode, params))
}

// script joins the onInit stages of a rendered container.
func script(c api.ContainerSpec) string {
	var out strings.Builder
	if c.Tty == nil {
		return ""
	}
	for _, stage := range c.Tty.OnInit {
		out.WriteString(stage.Script)
		out.WriteByte('\n')
	}
	return out.String()
}

// envOf maps a rendered container's env entries by key.
func envOf(c api.ContainerSpec) map[string]string {
	out := map[string]string{}
	for _, entry := range c.Env {
		key, value, _ := strings.Cut(entry, "=")
		out[key] = value
	}
	return out
}

// stateVolume returns the harness state volume a rendered container mounts by
// a fully-qualified reference.
func stateVolume(c api.ContainerSpec) (api.VolumeMount, bool) {
	for _, volume := range c.Volumes {
		if volume.Kind == api.VolumeKindVolume && volume.VolumeRef != nil {
			return volume, true
		}
	}
	return api.VolumeMount{}, false
}
