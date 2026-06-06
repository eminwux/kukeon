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

// Package blueprint implements `kuke create blueprint <name>` (issue #816,
// child of the #814 epic:create umbrella). A CellBlueprint is declaratively
// complex (containers, scalar parameters, repo/secret slots), so per the
// umbrella's per-kind pattern selection this subcommand emits a starter YAML
// to stdout — the operator edits in their preferred editor and pipes to
// `kuke apply -f -`. Nothing is written to the daemon: pure scaffolding, not a
// parallel write path.
package blueprint

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// NewBlueprintCmd builds `kuke create blueprint <name>`.
func NewBlueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "blueprint [name]",
		Aliases: []string{"bp"},
		Short:   "Scaffold a kind: CellBlueprint starter YAML (stdout)",
		Long: `Scaffold a starter CellBlueprint YAML to stdout.

Emits a syntactically-valid CellBlueprint document with a single placeholder
container, the operator's --realm/--space/--stack as scope, and inline
` + "`# TODO`" + ` markers on the required ` + "`image:`" + ` field plus comment markers for
optional sections (parameters, ports, volumes, repos, secrets) so operators
know what they can add.

Operator workflow:

  kuke create blueprint web > web.yaml
  $EDITOR web.yaml          # fill image, add parameters/repos/secrets/...
  kuke apply -f web.yaml

No daemon call — pure stdout emission.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateBlueprint,
	}

	cmd.Flags().String("realm", "", "Realm that owns the blueprint")
	_ = viper.BindPFlag(config.KUKE_CREATE_BLUEPRINT_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the blueprint")
	_ = viper.BindPFlag(config.KUKE_CREATE_BLUEPRINT_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the blueprint")
	_ = viper.BindPFlag(config.KUKE_CREATE_BLUEPRINT_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runCreateBlueprint(cmd *cobra.Command, args []string) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"blueprint",
		viper.GetString(config.KUKE_CREATE_BLUEPRINT_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_BLUEPRINT_REALM.ViperKey))
	if realm == "" {
		realm = strings.TrimSpace(config.KUKE_CREATE_BLUEPRINT_REALM.ValueOrDefault())
	}
	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_BLUEPRINT_SPACE.ViperKey))
	if space == "" {
		space = strings.TrimSpace(config.KUKE_CREATE_BLUEPRINT_SPACE.ValueOrDefault())
	}
	stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_BLUEPRINT_STACK.ViperKey))
	if stack == "" {
		stack = strings.TrimSpace(config.KUKE_CREATE_BLUEPRINT_STACK.ValueOrDefault())
	}

	return emitBlueprintYAML(cmd.OutOrStdout(), name, realm, space, stack)
}

// emitBlueprintYAML renders the CellBlueprint scaffold. The output is
// hand-formatted (rather than yaml.Marshal'd from a CellBlueprintDoc) so it
// can carry inline `# TODO` / `# optional` comments next to fields the
// operator must fill or may opt into — yaml.v3's struct marshaler cannot
// attach comments without dropping to its Node API, which would obscure the
// simple shape we emit here. The same approach is used by `kuke create
// config` (cmd/kuke/create/config/config.go).
func emitBlueprintYAML(out io.Writer, name, realm, space, stack string) error {
	var b strings.Builder

	b.WriteString("apiVersion: ")
	b.WriteString(string(v1beta1.APIVersionV1Beta1))
	b.WriteString("\n")
	b.WriteString("kind: ")
	b.WriteString(string(v1beta1.KindCellBlueprint))
	b.WriteString("\n")

	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", yamlScalar(name))
	fmt.Fprintf(&b, "  realm: %s\n", yamlScalar(realm))
	if space != "" {
		fmt.Fprintf(&b, "  space: %s\n", yamlScalar(space))
	}
	if stack != "" {
		fmt.Fprintf(&b, "  stack: %s\n", yamlScalar(stack))
	}

	b.WriteString("spec:\n")
	b.WriteString(
		"  # prefix: " + yamlScalar(
			name,
		) + "  # optional: cell-name prefix used by `kuke run --from-blueprint` (defaults to metadata.name)\n",
	)
	b.WriteString("  # parameters:\n")
	b.WriteString("  #   - name: KEY\n")
	b.WriteString("  #     # description: short explanation shown in --help\n")
	b.WriteString("  #     # default: <value>      # omit to require a value at run time\n")
	b.WriteString("  #     # required: true        # fail fast if no default and no run-time override\n")
	b.WriteString("  cell:\n")
	b.WriteString("    containers:\n")
	b.WriteString("      - id: main\n")
	b.WriteString("        image: \"\" # TODO (required)\n")
	b.WriteString("        # command: <entrypoint override>\n")
	b.WriteString("        # args: []\n")
	b.WriteString("        # env: [\"KEY=value\"]\n")
	b.WriteString("        # ports: [\"8080/tcp\"]\n")
	b.WriteString("        # volumes:\n")
	b.WriteString("        #   - source: <host-path-or-named-volume>\n")
	b.WriteString("        #     target: /in/container\n")
	b.WriteString("        #     # readOnly: true\n")
	b.WriteString("        # repos:\n")
	b.WriteString(
		"        #   - name: <slot-name>          # leave url empty to declare a fillable slot for CellConfig\n",
	)
	b.WriteString("        #     target: /src/app\n")
	b.WriteString("        #     # url: <inline-url>        # set to bake the repo into the blueprint\n")
	b.WriteString("        #     # required: true\n")
	b.WriteString("        # secrets:\n")
	b.WriteString("        #   - name: <slot-name>\n")
	b.WriteString("        #     mode: env                  # or \"file\"\n")
	b.WriteString("        #     envName: MY_ENV            # required when mode=env\n")
	b.WriteString("        #     # mountPath: /run/secrets/foo  # required when mode=file\n")
	b.WriteString("        #     # required: true\n")

	_, err := io.WriteString(out, b.String())
	return err
}

// yamlScalar formats a string scalar for the emitted YAML. Bare names made of
// [A-Za-z0-9-_./] stay unquoted (matching how yaml.v3 round-trips them);
// anything else — including reserved bare scalars like "true" or "null" that
// YAML would type as bool/null — is double-quoted with strconv.Quote, whose
// escape set (\\, \", \n, \t, \r, \uXXXX, \xHH) is a subset of YAML 1.2's
// double-quoted scalar escapes. Mirrors the helper in cmd/kuke/create/config.
func yamlScalar(s string) string {
	if s == "" {
		return `""`
	}
	if needsQuoting(s) {
		return strconv.Quote(s)
	}
	return s
}

func needsQuoting(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == '/':
		default:
			return true
		}
	}
	return isReservedBareScalar(s)
}

func isReservedBareScalar(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "yes", "no", "on", "off", "null", "~":
		return true
	}
	return false
}
