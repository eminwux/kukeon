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

package parser_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// contractBlueprint is the #1472 shared-blueprint shape: typed parameters, a
// capability requirement, an always-on harness component with two modes and an
// optional connector.
const contractBlueprint = `apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: claude
  realm: default
spec:
  prefix: claude
  requires: [git]
  parameters:
    - name: IMAGE
      type: image
      default: registry.example.com/claude:v1
    - name: WORKDIR
      type: path
      default: /workspace
    - name: VERBOSE
      type: bool
      default: "false"
  components:
    - kind: harness
      name: claude
      requires: [claude]
      parameters:
        - name: ANTHROPIC_MODEL
          type: enum
          enum: [opus, sonnet]
          default: opus
          group: harness
      env: ["ANTHROPIC_MODEL=${ANTHROPIC_MODEL}"]
      modes:
        interactive:
          onInit:
            - script: exec claude
        headless:
          onInit:
            - script: exec claude -p "$PROMPT"
          parameters:
            - name: PROMPT
              required: true
          env: ["PROMPT=${PROMPT}"]
          secrets:
            - name: api-key
              envName: ANTHROPIC_API_KEY
              required: true
    - kind: connector
      name: github
      optional: true
      requires: [gh]
      secrets:
        - name: gh-token
          envName: GH_TOKEN
          required: true
  cell:
    containers:
      - id: work
        image: ${IMAGE}
        attachable: true
`

// mutateBlueprint parses contractBlueprint and lets the test break one thing
// on the typed document before validation, so each case names exactly the
// field it is about instead of repeating the whole YAML.
func mutateBlueprint(t *testing.T, mutate func(*v1beta1.CellBlueprintDoc)) *parser.Document {
	t.Helper()
	doc := parseBlueprint(t, contractBlueprint)
	if doc.CellBlueprintDoc == nil {
		t.Fatal("parsed blueprint has no typed document")
	}
	mutate(doc.CellBlueprintDoc)
	return doc
}

func TestValidateDocument_BlueprintContract_Valid(t *testing.T) {
	doc := parseBlueprint(t, contractBlueprint)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected the contract blueprint to validate, got: %v", err)
	}
	spec := doc.CellBlueprintDoc.Spec
	if len(spec.Components) != 2 || len(spec.Components[0].Modes) != 2 {
		t.Fatalf("components did not round-trip through the parser: %+v", spec.Components)
	}
	if got := spec.Components[0].Parameters[0]; got.Type != v1beta1.ParameterTypeEnum || len(got.Enum) != 2 ||
		got.Group != "harness" {
		t.Errorf("typed parameter did not round-trip: %+v", got)
	}
}

func TestValidateDocument_BlueprintContract_Rejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1beta1.CellBlueprintDoc)
		want   error
		detail string
	}{
		{
			name:   "unknown parameter type",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Parameters[0].Type = "integer" },
			want:   errdefs.ErrBlueprintParameterType,
			detail: `unknown type "integer"`,
		},
		{
			name: "enum without values",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Parameters[0] = v1beta1.CellBlueprintParameter{Name: "IMAGE", Type: v1beta1.ParameterTypeEnum}
			},
			want:   errdefs.ErrBlueprintParameterType,
			detail: "lists its values in enum",
		},
		{
			name: "default outside the component's enum",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				bad := "haiku"
				d.Spec.Components[0].Parameters[0].Default = &bad
			},
			want:   errdefs.ErrBlueprintParameterType,
			detail: "want one of: opus, sonnet",
		},
		{
			name:   "non-bool default",
			mutate: func(d *v1beta1.CellBlueprintDoc) { bad := "yes"; d.Spec.Parameters[2].Default = &bad },
			want:   errdefs.ErrBlueprintParameterType,
			detail: "want true or false",
		},
		{
			name:   "component kind",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Kind = "plugin" },
			want:   errdefs.ErrBlueprintComponent,
			detail: "must be harness, connector or forge",
		},
		{
			name:   "duplicate component",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Name = "claude" },
			want:   errdefs.ErrBlueprintComponent,
			detail: "declared twice",
		},
		{
			name:   "component targets an unknown container",
			mutate: func(d *v1beta1.CellBlueprintDoc) { d.Spec.Components[1].Container = "nope" },
			want:   errdefs.ErrBlueprintComponent,
			detail: `unknown container "nope"`,
		},
		{
			name: "modes on a connector",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[1].Modes = d.Spec.Components[0].Modes
				d.Spec.Components[0].Modes = nil
			},
			want:   errdefs.ErrBlueprintComponent,
			detail: "only a harness component declares modes",
		},
		{
			name: "mode name",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[0].Modes["Head_Less"] = v1beta1.BlueprintComponentMode{}
			},
			want:   errdefs.ErrBlueprintMode,
			detail: "mode name must be a lowercase identifier",
		},
		{
			name: "mandatory component redeclares a blueprint parameter",
			mutate: func(d *v1beta1.CellBlueprintDoc) {
				d.Spec.Components[0].Parameters = append(d.Spec.Components[0].Parameters,
					v1beta1.CellBlueprintParameter{Name: "IMAGE"})
			},
			want:   errdefs.ErrBlueprintComponentConflict,
			detail: `redeclares parameter "IMAGE"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := parser.ValidateDocument(mutateBlueprint(t, tc.mutate))
			requireValidationErr(t, err, tc.want)
			if !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("err = %v, want detail %q", err, tc.detail)
			}
		})
	}
}

// TestValidateDocument_BlueprintContract_SecretSlotShape asserts component and
// mode secret slots go through the same slot-shape gate as container slots,
// with the error naming where the slot lives.
func TestValidateDocument_BlueprintContract_SecretSlotShape(t *testing.T) {
	t.Run("component slot", func(t *testing.T) {
		err := parser.ValidateDocument(mutateBlueprint(t, func(d *v1beta1.CellBlueprintDoc) {
			d.Spec.Components[1].Secrets[0].EnvName = ""
		}))
		if err == nil || !strings.Contains(err.Error(), `component "github"`) {
			t.Fatalf("err = %v, want a slot-shape error naming component github", err)
		}
	})
	t.Run("mode slot", func(t *testing.T) {
		err := parser.ValidateDocument(mutateBlueprint(t, func(d *v1beta1.CellBlueprintDoc) {
			m := d.Spec.Components[0].Modes[v1beta1.ModeHeadless]
			m.Secrets = []v1beta1.BlueprintSecretSlot{{Name: "api-key", Mode: v1beta1.BlueprintSecretModeFile}}
			d.Spec.Components[0].Modes[v1beta1.ModeHeadless] = m
		}))
		if err == nil || !strings.Contains(err.Error(), `component "claude" mode "headless"`) {
			t.Fatalf("err = %v, want a slot-shape error naming the headless mode", err)
		}
	})
}

// TestValidateDocument_BlueprintContract_GitBlocks asserts the template
// containers' git blocks and their repos' per-source git are validated at
// apply time, with the error naming the container (and repo).
func TestValidateDocument_BlueprintContract_GitBlocks(t *testing.T) {
	t.Run("container git", func(t *testing.T) {
		err := parser.ValidateDocument(mutateBlueprint(t, func(d *v1beta1.CellBlueprintDoc) {
			d.Spec.Cell.Containers[0].Git = &v1beta1.ContainerGit{
				Credentials: []v1beta1.GitCredential{{Host: "github.com"}},
			}
		}))
		if err == nil || !strings.Contains(err.Error(), `container "work"`) ||
			!strings.Contains(err.Error(), "secretRef requires name and realm") {
			t.Fatalf("err = %v, want a credential secretRef error on container work", err)
		}
	})
	t.Run("repo git", func(t *testing.T) {
		err := parser.ValidateDocument(mutateBlueprint(t, func(d *v1beta1.CellBlueprintDoc) {
			d.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{{
				Name:   "project",
				Target: "/workspace/project",
				Git:    &v1beta1.RepoGit{Author: &v1beta1.GitIdentity{Name: "Only Name"}},
			}}
		}))
		if err == nil || !strings.Contains(err.Error(), `repo "project"`) ||
			!strings.Contains(err.Error(), "git.author requires both name and email") {
			t.Fatalf("err = %v, want an identity error on repo project", err)
		}
	})
}

// contractConfig answers contractBlueprint: it enables the connector, narrows
// the modes, and states both a cell-wide and a per-source git identity.
const contractConfig = `apiVersion: v1beta1
kind: CellConfig
metadata:
  name: dev
  realm: default
spec:
  blueprint:
    name: claude
    realm: default
  requires: [node]
  components: [github]
  modes:
    enabled: [interactive, headless]
    default: interactive
  git:
    author: { name: Dev Agent, email: dev@example.com }
    credentials:
      - host: github.com
        secretRef: { name: github-key, realm: default }
    signing:
      secretRef: { name: signing-key, realm: default }
    sign: [commits]
  repos:
    project:
      url: git@github.com:example/project.git
      git:
        author: { name: Project Bot, email: bot@example.com }
        credential:
          secretRef: { name: project-key, realm: default }
`

func mutateConfig(t *testing.T, mutate func(*v1beta1.CellConfigDoc)) *parser.Document {
	t.Helper()
	doc := parseConfig(t, contractConfig)
	if doc.CellConfigDoc == nil {
		t.Fatal("parsed config has no typed document")
	}
	mutate(doc.CellConfigDoc)
	return doc
}

func TestValidateDocument_ConfigContract_Valid(t *testing.T) {
	doc := parseConfig(t, contractConfig)
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("expected the contract config to validate, got: %v", err)
	}
	spec := doc.CellConfigDoc.Spec
	if len(spec.Requires) != 1 || len(spec.Components) != 1 || spec.Modes == nil ||
		spec.Modes.Default != v1beta1.ModeInteractive || spec.Git == nil || len(spec.Git.Credentials) != 1 {
		t.Errorf("contract fields did not round-trip through the parser: %+v", spec)
	}
	if fill := spec.Repos["project"]; fill.Git == nil || fill.Git.Credential == nil ||
		fill.Git.Author.Email != "bot@example.com" {
		t.Errorf("repo fill git did not round-trip: %+v", fill)
	}

	// A default with no enabled list is judged against the blueprint later,
	// not here.
	onlyDefault := mutateConfig(t, func(c *v1beta1.CellConfigDoc) {
		c.Spec.Modes = &v1beta1.CellConfigModes{Default: v1beta1.ModeHeadless}
	})
	if err := parser.ValidateDocument(onlyDefault); err != nil {
		t.Errorf("modes.default without modes.enabled: %v", err)
	}
}

// A Config that turns signing on without naming a key applies: the key may be
// inherited from a Realm, Space or Stack default (#1472, step 6). Whether the
// merged block has one is checked when the cell is rendered.
func TestValidateDocument_ConfigContract_InheritedSigningKey(t *testing.T) {
	doc := mutateConfig(t, func(c *v1beta1.CellConfigDoc) {
		c.Spec.Git.Signing = nil
		if len(c.Spec.Git.Sign) == 0 {
			c.Spec.Git.Sign = []string{v1beta1.GitSignCommits}
		}
	})
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("config with git.sign and an inherited key: %v", err)
	}
}

func TestValidateDocument_ConfigContract_Rejections(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*v1beta1.CellConfigDoc)
		want   error
		detail string
	}{
		{
			name:   "empty component entry",
			mutate: func(c *v1beta1.CellConfigDoc) { c.Spec.Components = []string{"github", " "} },
			want:   errdefs.ErrBlueprintComponent,
			detail: "entries must not be empty",
		},
		{
			name:   "component listed twice",
			mutate: func(c *v1beta1.CellConfigDoc) { c.Spec.Components = []string{"github", "github"} },
			want:   errdefs.ErrBlueprintComponent,
			detail: `lists "github" twice`,
		},
		{
			name:   "empty enabled mode",
			mutate: func(c *v1beta1.CellConfigDoc) { c.Spec.Modes.Enabled = []string{""} },
			want:   errdefs.ErrBlueprintMode,
			detail: "spec.modes.enabled entries must not be empty",
		},
		{
			name: "default not enabled",
			mutate: func(c *v1beta1.CellConfigDoc) {
				c.Spec.Modes = &v1beta1.CellConfigModes{
					Enabled: []string{v1beta1.ModeInteractive}, Default: v1beta1.ModeHeadless,
				}
			},
			want:   errdefs.ErrBlueprintMode,
			detail: `spec.modes.default "headless" is not in spec.modes.enabled`,
		},
		{
			name:   "git identity half set",
			mutate: func(c *v1beta1.CellConfigDoc) { c.Spec.Git.Author.Email = "" },
			want:   errors.New("git.author requires both name and email"),
		},
		{
			name: "duplicate credential host",
			mutate: func(c *v1beta1.CellConfigDoc) {
				c.Spec.Git.Credentials = append(c.Spec.Git.Credentials, c.Spec.Git.Credentials[0])
			},
			want: errors.New("duplicate credential host"),
		},
		{
			name: "repo fill credential names a host",
			mutate: func(c *v1beta1.CellConfigDoc) {
				fill := c.Spec.Repos["project"]
				fill.Git.Credential.Host = "github.com"
				c.Spec.Repos["project"] = fill
			},
			want:   errors.New("git.credential.host must be empty"),
			detail: `repos["project"]`,
		},
		{
			name: "repo fill credential without a secret",
			mutate: func(c *v1beta1.CellConfigDoc) {
				fill := c.Spec.Repos["project"]
				fill.Git.Credential.SecretRef = nil
				c.Spec.Repos["project"] = fill
			},
			want:   errors.New("git.credential.secretRef requires name and realm"),
			detail: `repos["project"]`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := parser.ValidateDocument(mutateConfig(t, tc.mutate))
			requireValidationErr(t, err, tc.want)
			if tc.detail != "" && !strings.Contains(err.Error(), tc.detail) {
				t.Errorf("err = %v, want detail %q", err, tc.detail)
			}
		})
	}
}

// TestValidateDocument_BlueprintRepoTargetParameter: a shared blueprint asks
// for the checkout path as an answer (`target: ${ROLE_SOURCE_DIR}`), so a
// parameterized target is accepted at apply and judged once resolved; a
// literal relative target is still refused here.
func TestValidateDocument_BlueprintRepoTargetParameter(t *testing.T) {
	doc := mutateBlueprint(t, func(bp *v1beta1.CellBlueprintDoc) {
		bp.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{
			{Name: "role-source", Target: "${ROLE_SOURCE_DIR}", Required: true},
		}
	})
	if err := parser.ValidateDocument(doc); err != nil {
		t.Fatalf("parameterized repo target: %v", err)
	}
	doc = mutateBlueprint(t, func(bp *v1beta1.CellBlueprintDoc) {
		bp.Spec.Cell.Containers[0].Repos = []v1beta1.ContainerRepo{{Name: "role-source", Target: "relative/dir"}}
	})
	if err := parser.ValidateDocument(doc); err == nil ||
		!strings.Contains(err.Error(), errdefs.ErrRepoTargetNotAbsolute.Error()) {
		t.Fatalf("err = %v, want ErrRepoTargetNotAbsolute for a literal relative target", err)
	}
}
