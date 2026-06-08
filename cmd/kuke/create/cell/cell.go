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
		Short:   "Create a cell within a stack from a Blueprint or Config",
		Long: "Create a cell within a stack. Exactly one of --from-blueprint or " +
			"--from-config is required; use `kuke apply -f <file>` to materialise " +
			"a cell from a full manifest. Two modes:\n\n" +
			"  - `kuke create cell <name> --from-blueprint <bp> [--param k=v] [--param-file path]` — " +
			"resolves the daemon-stored CellBlueprint, applies scalar params, " +
			"materialises the full Cell record (containers and all), and " +
			"persists it in a **stopped** state. Run `kuke start <name>` to " +
			"start it. Differs from `kuke run --from-blueprint` (materialise + " +
			"start + attach) by leaving the cell stopped for later inspection or " +
			"hand-off; blueprint-lineage cells have no in-place reconcile, so " +
			"updates flow through delete-and-re-run (or promotion to a " +
			"CellConfig).\n" +
			"  - `kuke create cell <name> --from-config <cfg>` — resolves the " +
			"daemon-stored CellConfig and its referenced Blueprint, applies the " +
			"Config's spec.values + repo/secret slot fills, materialises the " +
			"Cell record, and persists it in a **stopped** state. Run " +
			"`kuke start <name>` to start it. Differs from `kuke run --from-config` " +
			"(materialise + start + attach) by leaving the cell stopped; later " +
			"reconcile against the lineage Config flows through " +
			"`kuke restart <name>` (OutOfSync-driven, #821) once the cell is " +
			"started.\n\n" +
			"--from-blueprint and --from-config are mutually exclusive. --param " +
			"and --param-file are valid with --from-blueprint (mirroring " +
			"`kuke run --from-blueprint`); they are rejected with --from-config because a " +
			"CellConfig carries its own values (edit the Config instead). " +
			"Symmetrically, --env KEY=VALUE is valid with --from-config (a " +
			"per-cell override layered on top of the Config's resolved values) " +
			"and rejected with --from-blueprint. Override precedence on the " +
			"--from-config path is binding values (the Config's spec.values) → " +
			"blueprint parameter defaults → per-cell --env (overrides win); on " +
			"the --from-blueprint path it is parameter defaults → per-cell " +
			"--param (overrides win). Both override sets are baked into the " +
			"materialised CellDoc and recorded in its Spec.Provenance block.",
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

	// Source flags (--from-blueprint/--from-config/--clone/--param/--param-file/
	// --env/--ignore-disk-pressure) live in one shared registration so `kuke run`
	// and `kuke create cell` cannot drift (epic:cell-identity #1025, AC#4).
	RegisterSourceFlags(cmd)

	// `kuke create cell` additionally binds the source-name flags to viper for
	// env-var fallback (KUKE_CREATE_CELL_*); `kuke run` reads its copies of the
	// same flags directly via cmd.Flags() (no viper bind) so the two commands
	// never race on a shared global viper key. param/env are intentionally not
	// viper-bound here — StringArray flags don't round-trip viper cleanly (#834).
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_FROM_BLUEPRINT.ViperKey, cmd.Flags().Lookup("from-blueprint"))
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_FROM_CONFIG.ViperKey, cmd.Flags().Lookup("from-config"))
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_CLONE.ViperKey, cmd.Flags().Lookup("clone"))
	_ = viper.BindPFlag(config.KUKE_CREATE_CELL_PARAM_FILE.ViperKey, cmd.Flags().Lookup("param-file"))
	_ = viper.BindPFlag(
		config.KUKE_CREATE_CELL_IGNORE_DISK_PRESSURE.ViperKey,
		cmd.Flags().Lookup("ignore-disk-pressure"),
	)

	// Register autocomplete functions for the scope flags. The source flags'
	// completions are registered by RegisterSourceFlags.
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

// RegisterSourceFlags registers the cell-source flag definitions
// (--from-blueprint, --from-config, --clone, --param, --param-file, --env,
// --ignore-disk-pressure), their mutual-exclusion, and their shell-completion
// funcs on cmd. Both `kuke create cell` and `kuke run` call it so the two verbs
// share one flag-definition set and cannot drift (epic:cell-identity #1025).
//
// It deliberately does NOT call viper.BindPFlag: viper is a process-global
// singleton and two commands binding the same key would race (last-registered
// wins). `kuke create cell` adds its own KUKE_CREATE_CELL_* binds after calling
// this (for env-var fallback); `kuke run` reads the flags directly via
// cmd.Flags(). Scope flags (--realm/--space/--stack) are NOT registered here —
// they bind command-specific viper keys and each command owns its own.
func RegisterSourceFlags(cmd *cobra.Command) {
	cmd.Flags().String("from-blueprint", "",
		"Materialise the cell from a daemon-stored CellBlueprint, resolved from the scope named "+
			"by --realm/--space/--stack. Substitutes scalar --param values. "+
			"Mutually exclusive with --from-config and --clone.")

	cmd.Flags().String("from-config", "",
		"Materialise the cell from a daemon-stored CellConfig (resolves the Config's referenced "+
			"Blueprint, applies the Config's spec.values and repo/secret slot fills). "+
			"Mutually exclusive with --from-blueprint and --clone.")

	cmd.Flags().String("clone", "",
		"Fork an existing cell's recipe into a new cell: read the source cell's "+
			"Spec.Provenance (the binding it was materialised from plus any per-cell "+
			"--param/--env overrides) and re-materialise from that same binding. The clone "+
			"copies the source's Spec.Provenance verbatim, inherits "+
			"its kukeon.io/config or kukeon.io/blueprint lineage label, and carries a "+
			"kukeon.io/source-cell=<src> annotation. Omitted name → <source-name>-<6hex>; "+
			"explicit name used verbatim. Mutually exclusive with --from-blueprint/--from-config.")

	cmd.Flags().StringArray("param", nil,
		"Scalar parameter override as KEY=VALUE; repeatable. Valid with --from-blueprint. "+
			"Each KEY must be declared in spec.parameters[]. Wins over the parameter's default "+
			"and over --param-file when both set the same key. Rejected with --from-config: a "+
			"CellConfig carries its own spec.values, edit the Config instead.")

	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed scalar parameters; one per line, "+
			"`#` starts a comment. Same declaration rules as --param. CLI --param wins on "+
			"duplicate keys. Rejected with --from-config (same reason as --param).")

	cmd.Flags().StringArray("env", nil,
		"Per-cell environment override as KEY=VALUE; repeatable. Valid with --from-config "+
			"only (the symmetric counterpart of --param on --from-blueprint). Each entry is "+
			"baked into the attachable container's env in the materialised CellDoc and recorded "+
			"in Spec.Provenance.envOverrides, winning over any value the Config's spec.values "+
			"resolved for the same key. Rejected with --from-blueprint: materialise from a "+
			"Config (or edit the Blueprint) to layer env overrides.")

	cmd.Flags().Bool("ignore-disk-pressure", false,
		"Bypass kukeond's data-volume disk-pressure guard for this cell's "+
			"creation. The daemon normally refuses to provision a new cell once "+
			"the data volume crosses the hard threshold. (issue #1035)")

	cmd.MarkFlagsMutuallyExclusive("from-blueprint", "from-config", "clone")

	_ = cmd.RegisterFlagCompletionFunc("from-blueprint", config.CompleteBlueprintNames)
	_ = cmd.RegisterFlagCompletionFunc("from-config", config.CompleteConfigNames)
	_ = cmd.RegisterFlagCompletionFunc("clone", config.CompleteCellNames)
}

// SourceFlags is the validated bundle of source-flag values the materialize
// path consumes. It is shared between `kuke create cell` (which persists the
// materialised doc stopped via MaterializeCell) and `kuke run` (which
// create+starts it via CreateCell then attaches) — the two verbs build a
// SourceFlags from their own flag surfaces and feed it to the single
// Materialize entrypoint so the materialisation cannot drift (epic:cell-identity
// #1025, AC#4). Fields are exported so the run package can populate them.
type SourceFlags struct {
	Name          string
	Realm         string
	Space         string
	Stack         string
	BlueprintName string
	ConfigName    string
	// CloneSource is the source cell name passed via `--clone <src>` (the
	// third source kind, epic:cell-identity #1073). When set, the cell is
	// forked from the source cell's Spec.Provenance rather than resolved from a
	// Blueprint/Config named on the CLI.
	CloneSource string
	ParamArgs   []string
	ParamFile   string
	// EnvArgs holds the validated `--env KEY=VALUE` per-cell overrides. Valid
	// with --from-config only (parity with --param on --from-blueprint); baked
	// into the attachable container's env and recorded in
	// Spec.Provenance.EnvOverrides. Issue #1023.
	EnvArgs []string
	// IgnoreDiskPressure threads `--ignore-disk-pressure` onto the
	// transport-only Spec.IgnoreDiskPressure field so the daemon's
	// CreateCell guard is bypassed for this invocation. Issue #1035.
	IgnoreDiskPressure bool
}

// parseCreateCellFlags validates the flag combinations and trims values. The
// cobra-side mutex enforces --from-blueprint vs --from-config; this routine
// also requires exactly one of those flags (the empty-shell mode retired with
// epic:bye-container step 3) and enforces the per-path override symmetry
// (issue #1023):
//
//   - --param/--param-file are rejected with --from-config (a CellConfig
//     carries its own spec.values, parity with `kuke run -c`);
//   - --env is rejected with --from-blueprint (its symmetric counterpart —
//     materialise from a Config to layer env overrides).
//
// --env entries are validated here (parseEnvArgs) so a malformed override
// fails before any daemon round-trip.
func parseCreateCellFlags(cmd *cobra.Command, args []string) (SourceFlags, error) {
	flags := SourceFlags{
		BlueprintName: strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_FROM_BLUEPRINT.ViperKey)),
		ConfigName:    strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_FROM_CONFIG.ViperKey)),
		CloneSource:   strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_CLONE.ViperKey)),
		ParamFile:     strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_PARAM_FILE.ViperKey)),
	}

	if flags.BlueprintName == "" && flags.ConfigName == "" && flags.CloneSource == "" {
		return SourceFlags{}, errors.New(
			"kuke create cell requires --from-blueprint, --from-config, or --clone " +
				"(use 'kuke apply -f <file>' for a full manifest)",
		)
	}

	// The clone source kind supports an omitted name (auto-generated
	// `<source-name>-<6hex>`, AC#2 of #1073), so it does not require the
	// positional/viper name the --from-blueprint / --from-config paths demand.
	// finalizeCellName resolves the empty name to a generated one.
	if flags.CloneSource != "" {
		flags.Name = optionalNameArgOrDefault(
			args, viper.GetString(config.KUKE_CREATE_CELL_NAME.ViperKey),
		)
	} else {
		name, err := shared.RequireNameArgOrDefault(
			cmd,
			args,
			"cell",
			viper.GetString(config.KUKE_CREATE_CELL_NAME.ViperKey),
		)
		if err != nil {
			return SourceFlags{}, err
		}
		flags.Name = name
	}

	flags.Realm = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_REALM.ViperKey))
	if flags.Realm == "" {
		flags.Realm = strings.TrimSpace(config.KUKE_CREATE_CELL_REALM.ValueOrDefault())
	}

	flags.Space = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_SPACE.ViperKey))
	if flags.Space == "" {
		flags.Space = strings.TrimSpace(config.KUKE_CREATE_CELL_SPACE.ValueOrDefault())
	}

	flags.Stack = strings.TrimSpace(viper.GetString(config.KUKE_CREATE_CELL_STACK.ViperKey))
	if flags.Stack == "" {
		flags.Stack = strings.TrimSpace(config.KUKE_CREATE_CELL_STACK.ValueOrDefault())
	}

	paramArgs, err := cmd.Flags().GetStringArray("param")
	if err != nil {
		return SourceFlags{}, err
	}
	flags.ParamArgs = paramArgs

	rawEnv, err := cmd.Flags().GetStringArray("env")
	if err != nil {
		return SourceFlags{}, err
	}
	envArgs, err := parseEnvArgs(rawEnv)
	if err != nil {
		return SourceFlags{}, err
	}
	flags.EnvArgs = envArgs

	flags.IgnoreDiskPressure = viper.GetBool(config.KUKE_CREATE_CELL_IGNORE_DISK_PRESSURE.ViperKey)

	return flags, nil
}

// ValidateOverrideSymmetry enforces the per-path --param/--env override
// symmetry shared by `kuke create cell` and `kuke run` (epic:cell-identity
// #1023/#1025):
//
//   - --param/--param-file are rejected with --from-config (a CellConfig
//     carries its own spec.values);
//   - --env is rejected with --from-blueprint (its symmetric counterpart —
//     materialise from a Config to layer env overrides).
//
// The clone source kind carries its own per-lineage variants of these checks
// (cloneFromConfig / cloneFromBlueprint) because the rejected flag depends on
// the source cell's recorded bindingKind, not on a CLI flag — so this validator
// is a no-op for clone. Materialize calls it up front, which is why `kuke run`
// (which feeds Materialize a SourceFlags it built itself) inherits the same
// rules without re-implementing them.
func ValidateOverrideSymmetry(flags SourceFlags) error {
	if flags.CloneSource != "" {
		return nil
	}
	if flags.ConfigName != "" {
		if len(flags.ParamArgs) > 0 {
			return errors.New(
				"--param is not valid with --from-config; a CellConfig carries its own spec.values (edit the Config instead)",
			)
		}
		if flags.ParamFile != "" {
			return errors.New(
				"--param-file is not valid with --from-config; a CellConfig carries its own spec.values (edit the Config instead)",
			)
		}
	}
	if flags.BlueprintName != "" && len(flags.EnvArgs) > 0 {
		return errors.New(
			"--env is not valid with --from-blueprint; materialise from a Config (kuke create cell --from-config) to layer env overrides",
		)
	}
	return nil
}

// ScopeVars names the command-specific scope viper Vars the materialize path
// reads to resolve binding-lookup scope. An unset --space/--stack defaults to
// the var's "default" (issue #1156) so the lookup hits the full default scope;
// a realm-scoped Blueprint/Config stays reachable via an explicit empty
// `--space "" --stack ""`. `kuke create cell` passes its KUKE_CREATE_CELL_*
// vars; `kuke run` passes its KUKE_RUN_* vars. Threading the
// Vars in (rather than hard-coding KUKE_CREATE_CELL_*) is what lets the two
// verbs share one Materialize entrypoint without colliding on a global viper
// key (epic:cell-identity #1025).
type ScopeVars struct {
	Realm *config.Var
	Space *config.Var
	Stack *config.Var
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

	// The scope-Var bundle `kuke create cell` feeds Materialize (built inline
	// rather than as a package global per the gochecknoglobals lint).
	cellDoc, err := Materialize(cmd, client, flags, ScopeVars{
		Realm: &config.KUKE_CREATE_CELL_REALM,
		Space: &config.KUKE_CREATE_CELL_SPACE,
		Stack: &config.KUKE_CREATE_CELL_STACK,
	})
	if err != nil {
		return err
	}
	return materialiseAndPersist(cmd, client, cellDoc)
}

// Materialize resolves the source binding named by flags (--from-blueprint /
// --from-config / --clone) and returns the fully-finalized CellDoc — name
// allocated, scope overlaid, provenance + overrides applied — WITHOUT persisting
// it. `kuke create cell` persists the doc stopped via MaterializeCell; `kuke
// run` create+starts it via CreateCell then attaches. This single entrypoint is
// the shared materialization function of epic:cell-identity #1025 (AC#4): the
// two verbs cannot drift because the materialised doc comes from the same code.
// The caller is responsible for the existence pre-check + persist
// (materialiseAndPersist for create cell; CreateCell for run).
func Materialize(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, scope ScopeVars,
) (v1beta1.CellDoc, error) {
	if err := ValidateOverrideSymmetry(flags); err != nil {
		return v1beta1.CellDoc{}, err
	}
	switch {
	case flags.CloneSource != "":
		return materializeClone(cmd, client, flags)
	case flags.BlueprintName != "":
		return materializeFromBlueprint(cmd, client, flags, scope)
	default:
		return materializeFromConfig(cmd, client, flags, scope)
	}
}

// materializeFromBlueprint resolves the named Blueprint, applies
// --param/--param-file, materialises the Cell record, overlays scope, and
// finalizes the cell name — returning the doc without persisting it.
//
// Lookup scope follows the default-coordinate rule from
// kukeshared.PickLookupRealm / ExplicitScope: an unset --space/--stack
// resolves to "default" so the lookup hits the full default scope, while a
// realm-scoped Blueprint stays reachable via an explicit empty --space/--stack.
func materializeFromBlueprint(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, scope ScopeVars,
) (v1beta1.CellDoc, error) {
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	lookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  flags.BlueprintName,
			Realm: kukeshared.PickLookupRealm(cmd, scope.Realm),
			Space: kukeshared.ExplicitScope(cmd, "space", scope.Space),
			Stack: kukeshared.ExplicitScope(cmd, "stack", scope.Stack),
		},
	}
	bpRes, err := client.GetBlueprint(cmd.Context(), lookup)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !bpRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (blueprint %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, lookup.Metadata.Name,
			lookup.Metadata.Realm, lookup.Metadata.Space, lookup.Metadata.Stack,
		)
	}

	resolved, err := cellblueprint.Resolve(bpRes.Blueprint, cliParams, os.LookupEnv)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	cellDoc, err := cellblueprint.MaterializeWithName(resolved, flags.Name, cliParams)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	overlayScope(&cellDoc, flags)
	if err = finalizeCellName(cmd, client, &cellDoc, flags.Name, cellblueprint.Prefix(resolved)); err != nil {
		return v1beta1.CellDoc{}, err
	}
	applyIgnoreDiskPressure(&cellDoc, flags)
	return cellDoc, nil
}

// materializeFromConfig resolves the named Config and its referenced Blueprint,
// materialises the Cell record via cellconfig.Materialize (which applies
// spec.values, repo/secret slot fills, and the kukeon.io/config back-reference
// label), finalizes the cell name (explicit or generated <prefix>-<6hex> per
// epic:cell-identity #1022), applies --env overrides — returning the doc
// without persisting it. Mirrors materializeFromBlueprint's scope-resolution
// strategy.
func materializeFromConfig(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, scope ScopeVars,
) (v1beta1.CellDoc, error) {
	cfgLookup := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  flags.ConfigName,
			Realm: kukeshared.PickLookupRealm(cmd, scope.Realm),
			Space: kukeshared.ExplicitScope(cmd, "space", scope.Space),
			Stack: kukeshared.ExplicitScope(cmd, "stack", scope.Stack),
		},
	}
	cfgRes, err := client.GetConfig(cmd.Context(), cfgLookup)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !cfgRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
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
		return v1beta1.CellDoc{}, err
	}
	if !bpRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (blueprint %q referenced by config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, bpRef.Name, cfgRes.Config.Metadata.Name,
			bpRef.Realm, bpRef.Space, bpRef.Stack,
		)
	}

	cellDoc, err := cellconfig.MaterializeWithName(cfgRes.Config, bpRes.Blueprint, flags.Name)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	overlayScope(&cellDoc, flags)
	if err = finalizeCellName(cmd, client, &cellDoc, flags.Name, cellconfig.Prefix(cfgRes.Config)); err != nil {
		return v1beta1.CellDoc{}, err
	}
	cellconfig.ApplyEnvOverrides(&cellDoc, flags.EnvArgs)
	applyIgnoreDiskPressure(&cellDoc, flags)
	return cellDoc, nil
}

// finalizeCellName resolves the cell's final name via the unified generator
// (epic:cell-identity #1022) once its scope is settled by overlayScope, then
// stamps it onto both metadata.name and Spec.ID. An explicit --name is used
// verbatim (materialiseAndPersist rejects an in-scope collision); an omitted
// name becomes a generated `<prefix>-<6hex>` probed free against the daemon at
// the cell's scope. Materialize is called with the explicit name (or "") up
// front so the rest of the cell — labels, provenance, scope — is built before
// the name is settled; finalizeCellName overwrites the placeholder name last.
func finalizeCellName(
	cmd *cobra.Command, client kukeonv1.Client, cellDoc *v1beta1.CellDoc, explicit, prefix string,
) error {
	name, err := kukeshared.ResolveCellName(
		cmd.Context(), client, explicit, prefix,
		cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID, cellDoc.Spec.StackID,
	)
	if err != nil {
		return err
	}
	cellDoc.Metadata.Name = name
	cellDoc.Spec.ID = name
	return nil
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
				"reconcile via `kuke start %s` + `kuke restart %s`) before re-materialising",
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

// applyIgnoreDiskPressure threads `--ignore-disk-pressure` onto the
// transport-only Spec.IgnoreDiskPressure field (yaml:"-" — never persisted) so
// the daemon's CreateCell guard is bypassed for this invocation. The flag only
// ever sets the override true. Issue #1035.
func applyIgnoreDiskPressure(doc *v1beta1.CellDoc, flags SourceFlags) {
	if flags.IgnoreDiskPressure {
		doc.Spec.IgnoreDiskPressure = true
	}
}

// overlayScope fills realm/space/stack coordinates on the materialised cell
// from the CLI's --realm/--space/--stack flags when the Blueprint/Config
// metadata left them empty (e.g., a realm-only blueprint materialised into a
// fully-specified scope). The doc wins when it sets a value — mirrors
// run.resolveCellLocation but scoped to the create flags' viper keys.
func overlayScope(doc *v1beta1.CellDoc, flags SourceFlags) {
	if strings.TrimSpace(doc.Spec.RealmID) == "" {
		doc.Spec.RealmID = flags.Realm
	}
	if strings.TrimSpace(doc.Spec.SpaceID) == "" {
		doc.Spec.SpaceID = flags.Space
	}
	if strings.TrimSpace(doc.Spec.StackID) == "" {
		doc.Spec.StackID = flags.Stack
	}
}

// parseEnvArgs validates the repeatable `--env KEY=VALUE` flag and returns the
// normalized entries. Mirrors cmd/kuke/run.parseEnvArgs (issue #834); kept
// local to avoid a cross-package import (parity with buildParamMap below).
// Rules: each entry needs a `=`; the KEY (before the first `=`) must be
// non-empty after trimming; the VALUE (after the first `=`) is preserved
// verbatim including empty; a duplicate KEY with the same VALUE is collapsed,
// a duplicate KEY with a different VALUE is rejected (no silent last-wins).
func parseEnvArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	seen := make(map[string]string, len(args))
	out := make([]string, 0, len(args))
	for _, raw := range args {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("--env requires KEY=VALUE (got: %q)", raw)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("--env KEY must be non-empty (got: %q)", raw)
		}
		if prior, dup := seen[key]; dup {
			if prior != value {
				return nil, fmt.Errorf(
					"--env %s supplied twice with different values (%q vs %q); pick one",
					key, prior, value,
				)
			}
			continue
		}
		seen[key] = value
		out = append(out, key+"="+value)
	}
	return out, nil
}

// buildParamMap layers --param flags on top of --param-file contents.
// Mirrors cmd/kuke/run.buildParamMap; kept local to avoid a cross-package
// import for ~10 lines.
func buildParamMap(flags SourceFlags) (map[string]string, error) {
	var fileParams map[string]string
	if flags.ParamFile != "" {
		fp, err := cellblueprint.ParseParamFile(flags.ParamFile)
		if err != nil {
			return nil, err
		}
		fileParams = fp
	}
	cliParams, err := cellblueprint.ParseParamArgs(flags.ParamArgs)
	if err != nil {
		return nil, err
	}
	return cellblueprint.MergeParams(fileParams, cliParams), nil
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
