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

package cell

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

func NewCellCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cell [name]",
		Aliases: []string{"ce"},
		Short:   "Create a cell within a stack (empty shell, --from-blueprint, or --from-config)",
		Long: "Create a cell within a stack. Three modes:\n\n" +
			"  - `kuke create cell <name>` (no source flag) — creates an empty Cell " +
			"shell (name + scope only, no containers). Workflow C: follow with " +
			"`kuke create container -c <name> --image ...` and `kuke start <name>`.\n" +
			"  - `kuke create cell <name> --from-blueprint <bp> [--param k=v] [--param-file path]` — " +
			"resolves the daemon-stored CellBlueprint, applies scalar params, " +
			"materialises the full Cell record (containers and all), and " +
			"persists it in a **stopped** state. Run `kuke start <name>` to " +
			"start it. Differs from `kuke run -b` (materialise + start + attach) " +
			"by leaving the cell stopped for later inspection or hand-off; " +
			"-b-lineage cells have no in-place reconcile, so updates flow through " +
			"delete-and-re-run (or promotion to a CellConfig).\n" +
			"  - `kuke create cell <name> --from-config <cfg>` — resolves the " +
			"daemon-stored CellConfig and its referenced Blueprint, applies the " +
			"Config's spec.values + repo/secret slot fills, materialises the " +
			"Cell record, and persists it in a **stopped** state. Run " +
			"`kuke start <name>` to start it. Differs from `kuke run <cfg>` " +
			"(materialise + start + attach) by leaving the cell stopped; later " +
			"reconcile against the lineage Config flows through " +
			"`kuke restart cell <name>` (OutOfSync-driven, #821) once the cell is " +
			"started.\n\n" +
			"--from-blueprint and --from-config are mutually exclusive. --param " +
			"and --param-file are valid with --from-blueprint (mirroring " +
			"`kuke run -b`); they are rejected with --from-config because a " +
			"CellConfig carries its own values (edit the Config instead).",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateCell,
	}

	cmd.Flags().String("realm", "", "Realm that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the cell")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("from-blueprint", "",
		"Materialise the cell from a daemon-stored CellBlueprint, resolved from the scope named "+
			"by --realm/--space/--stack. The cell record is persisted in a stopped state; run "+
			"`kuke start <name>` to start it. Mutually exclusive with --from-config.")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_FROM_BLUEPRINT.ViperKey, cmd.Flags().Lookup("from-blueprint"))

	cmd.Flags().String("from-config", "",
		"Materialise the cell from a daemon-stored CellConfig (resolves the Config's referenced "+
			"Blueprint, applies the Config's spec.values and repo/secret slot fills). The cell "+
			"record is persisted in a stopped state; run `kuke start <name>` to start it. "+
			"Mutually exclusive with --from-blueprint.")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_FROM_CONFIG.ViperKey, cmd.Flags().Lookup("from-config"))

	cmd.Flags().StringArray("param", nil,
		"Scalar parameter override as KEY=VALUE; repeatable. Valid with --from-blueprint. "+
			"Each KEY must be declared in spec.parameters[]. Wins over the parameter's default "+
			"and over --param-file when both set the same key. Rejected with --from-config: a "+
			"CellConfig carries its own spec.values, edit the Config instead.")

	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed scalar parameters; one per line, "+
			"`#` starts a comment. Same declaration rules as --param. CLI --param wins on "+
			"duplicate keys. Rejected with --from-config (same reason as --param).")
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_PARAM_FILE.ViperKey, cmd.Flags().Lookup("param-file"))

	cmd.MarkFlagsMutuallyExclusive("from-blueprint", "from-config")

	// Register autocomplete functions for flags
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("from-blueprint", config.CompleteBlueprintNames)
	_ = cmd.RegisterFlagCompletionFunc("from-config", config.CompleteConfigNames)

	return cmd
}

// createCellFlags is the validated bundle of flag values runCreateCell consumes
// after parseCreateCellFlags.
type createCellFlags struct {
	name          string
	realm         string
	space         string
	stack         string
	blueprintName string
	configName    string
	paramArgs     []string
	paramFile     string
}

// parseCreateCellFlags validates the flag combinations and trims values. The
// cobra-side mutex enforces --from-blueprint vs --from-config; this routine
// also rejects --param/--param-file when --from-config is set (a CellConfig
// carries its own spec.values, parity with `kuke run -c`).
func parseCreateCellFlags(cmd *cobra.Command, args []string) (createCellFlags, error) {
	flags := createCellFlags{
		blueprintName: strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_FROM_BLUEPRINT.ViperKey)),
		configName:    strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_FROM_CONFIG.ViperKey)),
		paramFile:     strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_PARAM_FILE.ViperKey)),
	}

	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"cell",
		viper.GetString(config.KUKE_CREATE_CELL_NAME.ViperKey),
	)
	if err != nil {
		return createCellFlags{}, err
	}
	flags.name = name

	flags.realm = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_REALM.ViperKey))
	if flags.realm == "" {
		flags.realm = strings.TrimSpace(config.KUKE_CREATE_CELL_REALM.ValueOrDefault())
	}

	flags.space = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_SPACE.ViperKey))
	if flags.space == "" {
		flags.space = strings.TrimSpace(config.KUKE_CREATE_CELL_SPACE.ValueOrDefault())
	}

	flags.stack = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_STACK.ViperKey))
	if flags.stack == "" {
		flags.stack = strings.TrimSpace(config.KUKE_CREATE_CELL_STACK.ValueOrDefault())
	}

	paramArgs, err := cmd.Flags().GetStringArray("param")
	if err != nil {
		return createCellFlags{}, err
	}
	flags.paramArgs = paramArgs

	if flags.configName != "" {
		if len(flags.paramArgs) > 0 {
			return createCellFlags{}, errors.New(
				"--param is not valid with --from-config; a CellConfig carries its own spec.values (edit the Config instead)",
			)
		}
		if flags.paramFile != "" {
			return createCellFlags{}, errors.New(
				"--param-file is not valid with --from-config; a CellConfig carries its own spec.values (edit the Config instead)",
			)
		}
	}
	return flags, nil
}

func runCreateCell(cmd *cobra.Command, args []string) error {
	flags, err := parseCreateCellFlags(cmd, args)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	switch {
	case flags.blueprintName != "":
		return runFromBlueprint(cmd, client, flags)
	case flags.configName != "":
		return runFromConfig(cmd, client, flags)
	default:
		return runEmptyShell(cmd, client, flags)
	}
}

// runEmptyShell preserves the pre-#818 behavior: create an empty Cell
// shell (name + scope only, no containers). The daemon's CreateCell is
// idempotent on an existing empty cell, so no pre-check is needed.
func runEmptyShell(cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags) error {
	doc := v1beta1.NewCellDoc(&v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: flags.name},
		Spec: v1beta1.CellSpec{
			RealmID: flags.realm,
			SpaceID: flags.space,
			StackID: flags.stack,
		},
	})

	result, err := client.CreateCell(cmd.Context(), *doc)
	if err != nil {
		return err
	}

	printCellResult(cmd, result)
	return nil
}

// runFromBlueprint resolves the named Blueprint, applies --param/--param-file,
// materialises the Cell record, refuses on name collision against an existing
// cell, then persists via MaterializeCell (no start). The operator runs
// `kuke start <name>` to start it.
//
// Lookup scope follows the explicit-coordinate rule from
// kukeshared.PickLookupRealm / ExplicitScope so realm-scoped Blueprints stay
// findable when --space/--stack are not set.
func runFromBlueprint(cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags) error {
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return err
	}

	lookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  flags.blueprintName,
			Realm: kukeshared.PickLookupRealm(cmd, &config.KUKE_CREATE_CELL_REALM),
			Space: kukeshared.ExplicitScope(cmd, "space", &config.KUKE_CREATE_CELL_SPACE),
			Stack: kukeshared.ExplicitScope(cmd, "stack", &config.KUKE_CREATE_CELL_STACK),
		},
	}
	bpRes, err := client.GetBlueprint(cmd.Context(), lookup)
	if err != nil {
		return err
	}
	if !bpRes.MetadataExists {
		return fmt.Errorf(
			"%w (blueprint %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, lookup.Metadata.Name,
			lookup.Metadata.Realm, lookup.Metadata.Space, lookup.Metadata.Stack,
		)
	}

	resolved, err := cellblueprint.Resolve(bpRes.Blueprint, cliParams, os.LookupEnv)
	if err != nil {
		return err
	}
	cellDoc, err := cellblueprint.MaterializeWithName(resolved, flags.name)
	if err != nil {
		return err
	}
	overlayScope(&cellDoc, flags)

	return materialiseAndPersist(cmd, client, cellDoc)
}

// runFromConfig resolves the named Config and its referenced Blueprint,
// materialises the Cell record via cellconfig.Materialize (which applies
// spec.values, repo/secret slot fills, the kukeon.io/config back-reference
// label, and the StableName), refuses on name collision, then persists via
// MaterializeCell (no start). Mirrors runFromBlueprint's scope-resolution
// strategy.
func runFromConfig(cmd *cobra.Command, client kukeonv1.Client, flags createCellFlags) error {
	cfgLookup := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  flags.configName,
			Realm: kukeshared.PickLookupRealm(cmd, &config.KUKE_CREATE_CELL_REALM),
			Space: kukeshared.ExplicitScope(cmd, "space", &config.KUKE_CREATE_CELL_SPACE),
			Stack: kukeshared.ExplicitScope(cmd, "stack", &config.KUKE_CREATE_CELL_STACK),
		},
	}
	cfgRes, err := client.GetConfig(cmd.Context(), cfgLookup)
	if err != nil {
		return err
	}
	if !cfgRes.MetadataExists {
		return fmt.Errorf(
			"%w (config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrConfigNotFound, cfgLookup.Metadata.Name,
			cfgLookup.Metadata.Realm, cfgLookup.Metadata.Space, cfgLookup.Metadata.Stack,
		)
	}

	bpRef := cfgRes.Config.Spec.Blueprint
	bpLookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  bpRef.Name,
			Realm: bpRef.Realm,
			Space: bpRef.Space,
			Stack: bpRef.Stack,
		},
	}
	bpRes, err := client.GetBlueprint(cmd.Context(), bpLookup)
	if err != nil {
		return err
	}
	if !bpRes.MetadataExists {
		return fmt.Errorf(
			"%w (blueprint %q referenced by config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, bpRef.Name, cfgRes.Config.Metadata.Name,
			bpRef.Realm, bpRef.Space, bpRef.Stack,
		)
	}

	cellDoc, err := cellconfig.MaterializeWithName(cfgRes.Config, bpRes.Blueprint, flags.name)
	if err != nil {
		return err
	}
	overlayScope(&cellDoc, flags)

	return materialiseAndPersist(cmd, client, cellDoc)
}

// materialiseAndPersist runs the existence pre-check and persists the
// materialised cell via MaterializeCell. Refuses if a cell with the same name
// already lives at the target scope — silent attach-to-existing would mask the
// spec divergence between the operator's chosen Blueprint/Config and whatever
// the existing cell was materialised from.
func materialiseAndPersist(cmd *cobra.Command, client kukeonv1.Client, cellDoc v1beta1.CellDoc) error {
	pre, err := client.GetCell(cmd.Context(), cellDoc)
	switch {
	case err == nil && pre.MetadataExists:
		return fmt.Errorf(
			"cell %q already exists in realm=%q space=%q stack=%q; "+
				"delete it with `kuke delete cell %s` (or, for Config-lineage cells, "+
				"reconcile via `kuke start cell %s` + `kuke restart cell %s`) before re-materialising",
			cellDoc.Metadata.Name,
			cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID, cellDoc.Spec.StackID,
			cellDoc.Metadata.Name, cellDoc.Metadata.Name, cellDoc.Metadata.Name,
		)
	case err != nil && !errors.Is(err, errdefs.ErrCellNotFound):
		return err
	}

	result, err := client.MaterializeCell(cmd.Context(), cellDoc)
	if err != nil {
		return err
	}
	printCellResult(cmd, result)
	return nil
}

// overlayScope fills realm/space/stack coordinates on the materialised cell
// from the CLI's --realm/--space/--stack flags when the Blueprint/Config
// metadata left them empty (e.g., a realm-only blueprint materialised into a
// fully-specified scope). The doc wins when it sets a value — mirrors
// run.resolveCellLocation but scoped to the create flags' viper keys.
func overlayScope(doc *v1beta1.CellDoc, flags createCellFlags) {
	if strings.TrimSpace(doc.Spec.RealmID) == "" {
		doc.Spec.RealmID = flags.realm
	}
	if strings.TrimSpace(doc.Spec.SpaceID) == "" {
		doc.Spec.SpaceID = flags.space
	}
	if strings.TrimSpace(doc.Spec.StackID) == "" {
		doc.Spec.StackID = flags.stack
	}
}

// buildParamMap layers --param flags on top of --param-file contents.
// Mirrors cmd/kuke/run.buildParamMap; kept local to avoid a cross-package
// import for ~10 lines.
func buildParamMap(flags createCellFlags) (map[string]string, error) {
	var fileParams map[string]string
	if flags.paramFile != "" {
		fp, err := cellprofile.ParseParamFile(flags.paramFile)
		if err != nil {
			return nil, err
		}
		fileParams = fp
	}
	cliParams, err := cellprofile.ParseParamArgs(flags.paramArgs)
	if err != nil {
		return nil, err
	}
	return cellprofile.MergeParams(fileParams, cliParams), nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func printCellResult(cmd *cobra.Command, result kukeonv1.CreateCellResult) {
	cmd.Printf(
		"Cell %q (realm %q, space %q, stack %q)\n",
		result.Cell.Metadata.Name,
		result.Cell.Spec.RealmID,
		result.Cell.Spec.SpaceID,
		result.Cell.Spec.StackID,
	)
	shared.PrintCreationOutcome(cmd, "metadata", result.MetadataExistsPost, result.Created)
	shared.PrintCreationOutcome(cmd, "cgroup", result.CgroupExistsPost, result.CgroupCreated)
	shared.PrintCreationOutcome(cmd, "root container", result.RootContainerExistsPost, result.RootContainerCreated)

	if len(result.Containers) == 0 {
		cmd.Println("  - containers: none defined")
	} else {
		for _, container := range result.Containers {
			label := fmt.Sprintf("container %q", container.Name)
			shared.PrintCreationOutcome(cmd, label, container.ExistsPost, container.Created)
		}
	}

	if result.Started {
		cmd.Println("  - containers: started")
	} else {
		cmd.Println("  - containers: not started")
	}
}

// PrintCellResult is exported for testing purposes.
func PrintCellResult(cmd *cobra.Command, result kukeonv1.CreateCellResult) {
	printCellResult(cmd, result)
}
