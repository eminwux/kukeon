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

// Package config implements `kuke create config <name> --from-blueprint <bp>`
// (issue #817, child of the #814 epic:create umbrella). A CellConfig is always
// derived from a CellBlueprint, so the subcommand reads the referenced
// Blueprint from the daemon, introspects its declared scalar parameters and
// structural repo/secret slots, and emits a starter Config YAML to stdout that
// the operator fills and pipes to `kuke apply -f -`. Nothing is written to the
// daemon — this is a scaffolding affordance, not a parallel write path.
package config

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewConfigCmd builds `kuke create config <name> --from-blueprint <bp>`. The
// flag is required: a Config without a Blueprint reference is meaningless.
func NewConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config [name]",
		Aliases: []string{"cfg"},
		Short:   "Scaffold a kind: CellConfig from a CellBlueprint (stdout)",
		Long: `Scaffold a CellConfig YAML from a CellBlueprint.

Reads the referenced Blueprint from the daemon, introspects its declared scalar
parameters and structural repo/secret slots, and emits a starter Config YAML to
stdout with defaults pre-filled and TODO markers where the operator must fill
required-no-default parameters and slot sources. The output is not written to
the daemon — pipe it to ` + "`kuke apply -f -`" + ` after editing.`,
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateConfig,
	}

	cmd.Flags().String("realm", "", "Realm that owns the config (also the default Blueprint lookup scope)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONFIG_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the config (also the default Blueprint lookup scope)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONFIG_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the config (also the default Blueprint lookup scope)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONFIG_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("from-blueprint", "", "Source CellBlueprint name (required)")
	_ = viper.BindPFlag(config.KUKE_CREATE_CONFIG_BLUEPRINT.ViperKey, cmd.Flags().Lookup("from-blueprint"))

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("from-blueprint", config.CompleteBlueprintNames)

	return cmd
}

func runCreateConfig(cmd *cobra.Command, args []string) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"config",
		viper.GetString(config.KUKE_CREATE_CONFIG_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	blueprintName := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONFIG_BLUEPRINT.ViperKey))
	if blueprintName == "" {
		return errors.New("--from-blueprint is required (the source CellBlueprint to scaffold from)")
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONFIG_REALM.ViperKey))
	if realm == "" {
		realm = strings.TrimSpace(config.KUKE_CREATE_CONFIG_REALM.ValueOrDefault())
	}
	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONFIG_SPACE.ViperKey))
	if space == "" {
		space = strings.TrimSpace(config.KUKE_CREATE_CONFIG_SPACE.ValueOrDefault())
	}
	stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CONFIG_STACK.ViperKey))
	if stack == "" {
		stack = strings.TrimSpace(config.KUKE_CREATE_CONFIG_STACK.ValueOrDefault())
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	lookup := v1beta1.CellBlueprintDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellBlueprint,
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  blueprintName,
			Realm: realm,
			Space: space,
			Stack: stack,
		},
	}

	res, err := client.GetBlueprint(cmd.Context(), lookup)
	if err != nil {
		if errors.Is(err, errdefs.ErrBlueprintNotFound) {
			return notFoundError(blueprintName, realm, space, stack)
		}
		return err
	}
	if !res.MetadataExists {
		return notFoundError(blueprintName, realm, space, stack)
	}

	return emitConfigYAML(cmd.OutOrStdout(), name, realm, space, stack, &res.Blueprint)
}

func notFoundError(name, realm, space, stack string) error {
	return fmt.Errorf(
		"%w (blueprint %q in scope realm=%q space=%q stack=%q)",
		errdefs.ErrBlueprintNotFound, name, realm, space, stack,
	)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

// emitConfigYAML renders the CellConfig scaffold. The output is hand-formatted
// (rather than yaml.Marshal'd from a CellConfigDoc) so it can carry inline
// `# TODO` comments next to the fields the operator must fill — yaml.v3's
// struct marshaler cannot attach comments without dropping to its Node API,
// which would obscure the simple shape we emit here.
func emitConfigYAML(out io.Writer, configName, realm, space, stack string, bp *v1beta1.CellBlueprintDoc) error {
	var b strings.Builder

	b.WriteString("apiVersion: ")
	b.WriteString(string(v1beta1.APIVersionV1Beta1))
	b.WriteString("\n")
	b.WriteString("kind: ")
	b.WriteString(string(v1beta1.KindCellConfig))
	b.WriteString("\n")

	b.WriteString("metadata:\n")
	fmt.Fprintf(&b, "  name: %s\n", yamlScalar(configName))
	fmt.Fprintf(&b, "  realm: %s\n", yamlScalar(realm))
	if space != "" {
		fmt.Fprintf(&b, "  space: %s\n", yamlScalar(space))
	}
	if stack != "" {
		fmt.Fprintf(&b, "  stack: %s\n", yamlScalar(stack))
	}

	b.WriteString("spec:\n")

	// Blueprint reference: copy the scope from the resolved Blueprint document
	// so the emitted YAML records exactly where GetBlueprint resolved (and so a
	// cross-scope reference is preserved verbatim — see CellConfigBlueprintRef's
	// docstring: a Config may instantiate a Blueprint owned by another scope).
	b.WriteString("  blueprint:\n")
	fmt.Fprintf(&b, "    name: %s\n", yamlScalar(bp.Metadata.Name))
	fmt.Fprintf(&b, "    realm: %s\n", yamlScalar(bp.Metadata.Realm))
	if bp.Metadata.Space != "" {
		fmt.Fprintf(&b, "    space: %s\n", yamlScalar(bp.Metadata.Space))
	}
	if bp.Metadata.Stack != "" {
		fmt.Fprintf(&b, "    stack: %s\n", yamlScalar(bp.Metadata.Stack))
	}

	writeValues(&b, bp.Spec.Parameters)
	writeRepoSlots(&b, bp.Spec.Cell.Containers)
	writeSecretSlots(&b, bp.Spec.Cell.Containers)

	_, err := io.WriteString(out, b.String())
	return err
}

func writeValues(b *strings.Builder, params []v1beta1.CellProfileParameter) {
	if len(params) == 0 {
		b.WriteString("  values: {}\n")
		return
	}
	b.WriteString("  values:\n")
	for _, p := range params {
		switch {
		case p.Default != nil:
			fmt.Fprintf(b, "    %s: %s\n", p.Name, yamlScalar(*p.Default))
		case p.Required:
			fmt.Fprintf(b, "    %s: \"\" # TODO (required, no default)\n", p.Name)
		default:
			fmt.Fprintf(b, "    %s: \"\" # optional (no default)\n", p.Name)
		}
	}
}

// repoSlot summarises one structural repo slot the Blueprint declares. The slot
// is identified by name across all containers (ValidateSlotFill matches by
// name, OR-ing required across declarations); we collapse same-named entries
// the same way so the emitted Config has at most one fill per slot name.
type repoSlot struct {
	Name       string
	Required   bool
	Containers []string
}

func writeRepoSlots(b *strings.Builder, containers []v1beta1.BlueprintContainer) {
	order, slots := collectRepoSlots(containers)
	if len(order) == 0 {
		return
	}
	b.WriteString("  repos:\n")
	for _, name := range order {
		s := slots[name]
		fmt.Fprintf(b, "    %s: # %s repo slot (container %s)\n",
			name, requiredLabel(s.Required), joinQuoted(s.Containers))
		b.WriteString("      url: \"\" # TODO\n")
	}
}

func collectRepoSlots(containers []v1beta1.BlueprintContainer) ([]string, map[string]*repoSlot) {
	slots := map[string]*repoSlot{}
	order := []string{}
	for _, c := range containers {
		for _, r := range c.Repos {
			if strings.TrimSpace(r.URL) != "" {
				continue // inline url is scalar-mode, not a fillable slot
			}
			name := strings.TrimSpace(r.Name)
			if name == "" {
				continue
			}
			if existing, ok := slots[name]; ok {
				existing.Required = existing.Required || r.Required
				existing.Containers = append(existing.Containers, c.ID)
				continue
			}
			slots[name] = &repoSlot{Name: name, Required: r.Required, Containers: []string{c.ID}}
			order = append(order, name)
		}
	}
	return order, slots
}

type secretSlotEntry struct {
	Name       string
	Required   bool
	Containers []string
	// Mode/EnvName/MountPath capture the consumption side from the *first*
	// declaration; later declarations of the same slot name across containers
	// only widen Required (matching ValidateSlotFill's per-name aggregation).
	Mode      string
	EnvName   string
	MountPath string
}

func writeSecretSlots(b *strings.Builder, containers []v1beta1.BlueprintContainer) {
	order, slots := collectSecretSlots(containers)
	if len(order) == 0 {
		return
	}
	b.WriteString("  secrets:\n")
	for _, name := range order {
		s := slots[name]
		fmt.Fprintf(b, "    %s: # %s secret slot (container %s, %s)\n",
			name, requiredLabel(s.Required), joinQuoted(s.Containers), consumptionDescription(s))
		b.WriteString("      secretRef:\n")
		b.WriteString("        name: \"\" # TODO\n")
		b.WriteString("        realm: \"\" # TODO\n")
	}
}

func collectSecretSlots(containers []v1beta1.BlueprintContainer) ([]string, map[string]*secretSlotEntry) {
	slots := map[string]*secretSlotEntry{}
	order := []string{}
	for _, c := range containers {
		for _, s := range c.Secrets {
			name := strings.TrimSpace(s.Name)
			if name == "" {
				continue
			}
			if existing, ok := slots[name]; ok {
				existing.Required = existing.Required || s.Required
				existing.Containers = append(existing.Containers, c.ID)
				continue
			}
			slots[name] = &secretSlotEntry{
				Name:       name,
				Required:   s.Required,
				Containers: []string{c.ID},
				Mode:       s.Mode,
				EnvName:    s.EnvName,
				MountPath:  s.MountPath,
			}
			order = append(order, name)
		}
	}
	return order, slots
}

func consumptionDescription(s *secretSlotEntry) string {
	mode := s.Mode
	if mode == "" {
		mode = v1beta1.BlueprintSecretModeEnv
	}
	switch mode {
	case v1beta1.BlueprintSecretModeFile:
		return fmt.Sprintf("file mount %q", s.MountPath)
	default:
		return fmt.Sprintf("env %q", s.EnvName)
	}
}

func requiredLabel(required bool) string {
	if required {
		return "required"
	}
	return "optional"
}

func joinQuoted(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%q", n)
	}
	return strings.Join(parts, ", ")
}

// yamlScalar formats a string scalar for the emitted YAML. Bare names made of
// [A-Za-z0-9-_./] stay unquoted (matching how yaml.v3 round-trips them);
// anything else — including reserved bare scalars like "true" or "null" that
// YAML would type as bool/null — is double-quoted with strconv.Quote, whose
// escape set (\\, \", \n, \t, \r, \uXXXX, \xHH) is a subset of YAML 1.2's
// double-quoted scalar escapes.
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
