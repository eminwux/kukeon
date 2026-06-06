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

// Package run implements the `kuke run` verb. The `-f <file>` form parses a
// single-cell YAML doc and idempotently create-and-starts the cell; the `-b`
// form resolves a daemon-stored CellBlueprint, and the positional `<config>`
// form resolves a daemon-stored CellConfig — both walk the same
// create-or-attach state machine. The legacy `-p/--profile` form
// (client-side CellProfile loader under $HOME/.kuke/profiles.d) was removed
// in #626 — it is intercepted at flag-parse time and replaced with a
// migration pointer.
//
// `kuke run` attaches to the cell's attachable container by default, matching
// `docker run` and `kubectl run -it`. Pass `-d/--detach` to return immediately
// after start without attaching.
package run

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/cell"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// errRemovedProfileFlag is the migration-pointer error swapped in by the
// SetFlagErrorFunc below when the operator types the removed `-p`/`--profile`
// flag. Defined once so tests can match on it via errors.Is.
var errRemovedProfileFlag = errors.New(
	"kuke run: -p/--profile (CellProfile) was removed in #626 — apply a " +
		"kind: CellBlueprint and use `kuke run --from-blueprint <name>` (or " +
		"`kuke run --from-config <cfg>` for a daemon-stored CellConfig); " +
		"see docs/site/guides/migrate-cellprofile-to-blueprint.md",
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewRunCmd builds the `kuke run` cobra command — the docker-model fused verb
// (epic:cell-identity #1025). The optional positional names an existing cell to
// start + attach; `--from-blueprint` / `--from-config` / `--clone` create + start
// + attach a fresh cell, delegating the create half to the same materialization
// the un-fused `kuke create cell` runs; `-f` create-or-attaches a cell from a
// YAML manifest. Exactly one source is required. By default the CLI drops the
// operator into the cell's attachable terminal after start; `-d/--detach` opts
// out of the post-start attach.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [<cell>]",
		Short: "Start and attach an existing cell, or create+start+attach a new one (--from-blueprint/--from-config/--clone/-f)",
		Long: "Start and attach a cell. `kuke run` is the fused docker-model verb: " +
			"`docker create` -> `kuke create cell`, `docker start` -> `kuke start`, " +
			"`docker run` -> `kuke run` (create? + start + attach).\n\n" +
			"  - `kuke run <cell>` starts + attaches an existing cell (~ `kuke start " +
			"<cell>` + `kuke attach <cell>`). `-d/--detach` starts without attaching. " +
			"The cell must already exist; a missing cell is an error pointing at " +
			"`kuke create cell` / `kuke run --from-...`.\n" +
			"  - `kuke run --from-blueprint <bp> [--param K=V]`, `kuke run --from-config " +
			"<cfg> [--env K=V]`, and `kuke run --clone <cell>` create + start + attach a " +
			"fresh cell. The create half delegates to the same materialization function " +
			"`kuke create cell` runs (shared FlagSet, no drift): the produced CellDoc is " +
			"identical to `kuke create cell --from-...` followed by `kuke start`. The cell " +
			"name is `--name X` when given, else a generated `<prefix>-<6hex>`.\n" +
			"  - `kuke run -f <file>` create-or-attaches by metadata.name: a missing cell " +
			"is created and attached; a Ready cell is attached as a no-op; a Stopped cell " +
			"is started then attached; a divergent on-disk spec is refused (use `kuke " +
			"apply -f` to update, or --require-synced to hard-fail); a cell in an error or " +
			"partial state is refused with a `kuke delete cell <name>` pointer.\n\n" +
			"--rm best-effort deletes the cell once the workload exits (a clean ^]^] " +
			"detach keeps it alive). --env KEY=VALUE on the existing-cell and -f paths " +
			"injects runtime env into the attachable container at start time (does not " +
			"persist); on the --from-config / --clone paths it is the persisted per-cell " +
			"override baked into the materialised CellDoc.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: config.CompleteCellNames,
		SilenceUsage:      true,
		SilenceErrors:     false,
		RunE:              runRun,
	}

	// Intercept pflag's "unknown shorthand flag: 'p'" / "unknown flag: --profile"
	// at parse time and swap in the migration pointer — issue #626 removed the
	// -p/--profile flag, and a generic cobra error wouldn't name the replacement.
	// Other unknown-flag errors fall through unchanged.
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		if err == nil {
			return nil
		}
		msg := err.Error()
		// pflag emits "unknown shorthand flag: 'p' in -p" for -p and the exact
		// "unknown flag: --profile" for both --profile and --profile=<v> (the
		// value is stripped before the message is built). Match the long form
		// exactly so a future flag with a "profile" prefix can't be swallowed.
		if strings.Contains(msg, "shorthand flag: 'p'") || msg == "unknown flag: --profile" {
			return errRemovedProfileFlag
		}
		return err
	})

	// Shared cell-source flags (--from-blueprint/--from-config/--clone/--param/
	// --param-file/--env/--ignore-disk-pressure). One definition set shared with
	// `kuke create cell` (cell.RegisterSourceFlags) so the fused create+start+attach
	// form cannot drift from the un-fused `create cell` + `start` (#1025, AC#4).
	// run reads these via cmd.Flags() (not viper) — see parseRunFlags.
	cell.RegisterSourceFlags(cmd)

	cmd.Flags().
		StringP("file", "f", "", "File to read a single-cell YAML manifest from (use - for stdin); mutually exclusive with the <cell> positional and --from-blueprint/--from-config/--clone")
	_ = viper.BindPFlag(config.KUKE_RUN_FILE.ViperKey, cmd.Flags().Lookup("file"))

	cmd.Flags().String("name", "",
		"Name the cell materialised by --from-blueprint/--from-config/--clone "+
			"(default: a generated <prefix>-<6hex>). Rejected with the <cell> positional "+
			"(the positional IS the cell name) and with -f (metadata.name is authoritative).")
	_ = viper.BindPFlag(config.KUKE_RUN_NAME.ViperKey, cmd.Flags().Lookup("name"))

	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")
	_ = viper.BindPFlag(config.KUKE_RUN_OUTPUT.ViperKey, cmd.Flags().Lookup("output"))

	cmd.Flags().String("realm", "", "Realm that owns the cell (overrides spec.realmId only when the doc is empty)")
	_ = viper.BindPFlag(config.KUKE_RUN_REALM.ViperKey, cmd.Flags().Lookup("realm"))
	cmd.Flags().String("space", "", "Space that owns the cell (overrides spec.spaceId only when the doc is empty)")
	_ = viper.BindPFlag(config.KUKE_RUN_SPACE.ViperKey, cmd.Flags().Lookup("space"))
	cmd.Flags().String("stack", "", "Stack that owns the cell (overrides spec.stackId only when the doc is empty)")
	_ = viper.BindPFlag(config.KUKE_RUN_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().BoolP("detach", "d", false,
		"Return immediately after start without attaching (default: attach to the cell's "+
			"attachable container, precedence --container > cell.tty.default > first attachable)")
	_ = viper.BindPFlag(config.KUKE_RUN_DETACH.ViperKey, cmd.Flags().Lookup("detach"))
	cmd.Flags().String("container", "",
		"Container to attach to (only valid in attach mode; rejected with -d/--detach; must be attachable)")
	_ = viper.BindPFlag(config.KUKE_RUN_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().Bool("require-synced", false,
		"With -f: refuse to attach when the live cell's spec diverges from the on-disk "+
			"manifest. Default behavior is warn-and-attach: print a one-line `notice:` "+
			"naming the diverging fields and the `kuke apply -f` pointer, then drop the "+
			"operator into the live state. Use --require-synced for CI/scripted callers "+
			"that need a hard fail on divergence.")
	_ = viper.BindPFlag(config.KUKE_RUN_REQUIRE_SYNCED.ViperKey, cmd.Flags().Lookup("require-synced"))

	cmd.Flags().Bool("rm", false,
		"Best-effort delete the cell after it is no longer needed "+
			"(any rc). With -d/--detach, the trigger is the root "+
			"container's task exit. In the default attach mode, the "+
			"trigger is the attach loop exiting because the workload "+
			"terminated, the peer hung up, or an unrecoverable "+
			"controller error fired — the CLI then sends KillCell so a "+
			"long-lived root (e.g. `sleep infinity`) does not pin the "+
			"cell. A clean ^]^] detach is NOT a trigger: the cell stays "+
			"alive so the operator can re-attach later (parity with "+
			"`kuke attach`). Cleanup runs from kukeond's reconcile loop, "+
			"so latency is bounded by the reconcile interval rather "+
			"than firing the instant the trigger fires.")
	_ = viper.BindPFlag(config.KUKE_RUN_RM.ViperKey, cmd.Flags().Lookup("rm"))

	// --file is mutually exclusive with each fused source; the from-* sources are
	// already mutually exclusive among themselves (cell.RegisterSourceFlags). The
	// positional-vs-flag mutex is hand-rolled in parseRunFlags (cobra's mutex
	// machinery spans flags only).
	cmd.MarkFlagsMutuallyExclusive("file", "from-blueprint")
	cmd.MarkFlagsMutuallyExclusive("file", "from-config")
	cmd.MarkFlagsMutuallyExclusive("file", "clone")

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

// runFlags is the validated bundle of flag values runRun consumes after
// argument validation. Exactly one source is non-empty: cellName (the
// positional, an existing cell to start+attach), file (-f manifest), or one of
// blueprintName/configName/cloneSource (the fused create+start+attach sources
// delegated to cmd/kuke/create/cell.Materialize).
type runFlags struct {
	// cellName is the positional `<cell>` — an existing cell to start+attach.
	cellName string
	// file is the -f manifest path (create-or-attach by metadata.name).
	file string
	// blueprintName/configName/cloneSource are the fused create+start+attach
	// sources (--from-blueprint/--from-config/--clone). Reading them via
	// cmd.Flags() (not viper) is deliberate: the same flag definitions are
	// shared with `kuke create cell` (cell.RegisterSourceFlags), and viper-binding
	// them in both commands would race on the global key.
	blueprintName string
	configName    string
	cloneSource   string

	output        string
	detach        bool
	containerFlag string
	autoDelete    bool
	// name is --name: the cell name for the fused create+start+attach sources
	// (default a generated <prefix>-<6hex>). Rejected with the positional and -f.
	name      string
	paramArgs []string
	paramFile string
	// envArgs are the validated `KEY=VALUE` --env entries. On the existing-cell
	// and -f paths they thread onto Spec.RuntimeEnv (transport-only runtime
	// injection, #834); on the --from-config/--clone paths they are the persisted
	// per-cell override baked into the materialised CellDoc (#1023).
	envArgs []string
	// requireSynced flips the -f divergence handling from warn-and-attach
	// (default, #986) to fail-fast. Only the -f path compares against a source.
	requireSynced bool
	// ignoreDiskPressure threads `--ignore-disk-pressure` onto the transport-only
	// Spec.IgnoreDiskPressure field so the daemon's CreateCell guard is bypassed.
	ignoreDiskPressure bool
}

// fused reports whether the invocation is a fused create+start+attach
// (--from-blueprint / --from-config / --clone), the path that delegates its
// create half to cell.Materialize.
func (f runFlags) fused() bool {
	return f.blueprintName != "" || f.configName != "" || f.cloneSource != ""
}

// validateSourceMutex enforces the "exactly one source" contract across the
// `kuke run` inputs. Cobra's MarkFlagsMutuallyExclusive spans flags only, and
// already covers -f vs the from-* sources and the from-* sources among
// themselves (NewRunCmd / cell.RegisterSourceFlags); this hand-rolls the
// positional-vs-flag mutex and the at-least-one-source guard.
func validateSourceMutex(flags runFlags) error {
	if flags.cellName != "" {
		switch {
		case flags.file != "":
			return errors.New(
				"the <cell> positional is mutually exclusive with -f/--file " +
					"(start an existing cell, or create one from -f / --from-...)")
		case flags.fused():
			return errors.New(
				"the <cell> positional (start an existing cell) is mutually exclusive with " +
					"--from-blueprint/--from-config/--clone (create a new one)")
		}
	}
	if flags.cellName == "" && flags.file == "" && !flags.fused() {
		return errors.New(
			"a source is required: <cell> (start an existing cell), -f/--file, or " +
				"--from-blueprint/--from-config/--clone (create+start+attach a new one)")
	}
	return nil
}

func parseRunFlags(cmd *cobra.Command, args []string) (runFlags, error) {
	flags := runFlags{
		file:          strings.TrimSpace(viper.GetString(config.KUKE_RUN_FILE.ViperKey)),
		detach:        viper.GetBool(config.KUKE_RUN_DETACH.ViperKey),
		containerFlag: strings.TrimSpace(viper.GetString(config.KUKE_RUN_CONTAINER.ViperKey)),
		autoDelete:    viper.GetBool(config.KUKE_RUN_RM.ViperKey),
		name:          strings.TrimSpace(viper.GetString(config.KUKE_RUN_NAME.ViperKey)),
		requireSynced: viper.GetBool(config.KUKE_RUN_REQUIRE_SYNCED.ViperKey),
	}

	// The fused-source flags share their definitions with `kuke create cell`
	// (cell.RegisterSourceFlags) and are read directly off cmd.Flags() — never
	// via viper — so the two commands don't collide on a global viper key.
	// --ignore-disk-pressure is in the same shared FlagSet and reads off
	// cmd.Flags() for the same reason; reading it via viper would always see
	// the zero value (no Bind on the run side) and silently drop the operator's
	// opt-in past the daemon's disk-pressure guard.
	for dst, name := range map[*string]string{
		&flags.blueprintName: "from-blueprint",
		&flags.configName:    "from-config",
		&flags.cloneSource:   "clone",
		&flags.paramFile:     "param-file",
	} {
		v, err := cmd.Flags().GetString(name)
		if err != nil {
			return runFlags{}, err
		}
		*dst = strings.TrimSpace(v)
	}

	idp, err := cmd.Flags().GetBool("ignore-disk-pressure")
	if err != nil {
		return runFlags{}, err
	}
	flags.ignoreDiskPressure = idp

	if len(args) == 1 {
		flags.cellName = strings.TrimSpace(args[0])
	}
	if err := validateSourceMutex(flags); err != nil {
		return runFlags{}, err
	}

	paramArgs, err := cmd.Flags().GetStringArray("param")
	if err != nil {
		return runFlags{}, err
	}
	flags.paramArgs = paramArgs

	envRaw, err := cmd.Flags().GetStringArray("env")
	if err != nil {
		return runFlags{}, err
	}
	envArgs, err := parseEnvArgs(envRaw)
	if err != nil {
		return runFlags{}, err
	}
	flags.envArgs = envArgs

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return runFlags{}, err
	}
	flags.output = strings.TrimSpace(output)
	if flags.output != "" && flags.output != "json" && flags.output != "yaml" {
		return runFlags{}, fmt.Errorf("invalid --output %q: want json or yaml", flags.output)
	}

	if flags.detach && flags.containerFlag != "" {
		return runFlags{}, errors.New("--container is incompatible with -d/--detach")
	}
	if !flags.detach && flags.output != "" {
		// The default attach mode hands the terminal to sbsh; mixing
		// structured -o output with an interactive attach loop produces
		// garbled output that nothing can parse. Reject the combination
		// so callers pick one mode.
		return runFlags{}, errors.New("--output is incompatible with attach mode (pass -d/--detach)")
	}

	if err = validateSourceFlagCompat(flags); err != nil {
		return runFlags{}, err
	}
	return flags, nil
}

// validateSourceFlagCompat rejects flag/source combinations the dispatch can't
// honor. --name names the cell only on the fused create path; --param/--param-file
// are render-time knobs only on the fused path (and only --from-blueprint, where
// cell.ValidateOverrideSymmetry enforces the --from-config rejection); and
// --require-synced compares the live cell against the source manifest, which
// only -f supplies (the fused paths refuse on --name collision rather than
// diverge, and the existing-cell positional has no source). The existing-cell
// and -f paths take neither --name nor --param/--param-file: the positional IS
// the name, and -f's metadata.name is authoritative.
func validateSourceFlagCompat(flags runFlags) error {
	// --require-synced is -f-only on every source path; reject explicitly on
	// the positional and the fused paths rather than silently no-op'ing it.
	if flags.requireSynced && flags.file == "" {
		return errors.New(
			"--require-synced is only valid with -f/--file (the only path that " +
				"compares the live cell against a source manifest)")
	}
	if flags.fused() {
		return nil
	}
	// Non-fused sources: the <cell> positional or -f.
	target := "the <cell> positional"
	if flags.file != "" {
		target = "-f/--file"
	}
	if flags.name != "" {
		return fmt.Errorf("--name is only valid with --from-blueprint/--from-config/--clone, not %s", target)
	}
	if len(flags.paramArgs) > 0 {
		return fmt.Errorf("--param is only valid with --from-blueprint, not %s", target)
	}
	if flags.paramFile != "" {
		return fmt.Errorf("--param-file is only valid with --from-blueprint, not %s", target)
	}
	return nil
}

func runRun(cmd *cobra.Command, args []string) error {
	flags, err := parseRunFlags(cmd, args)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	switch {
	case flags.fused():
		return runFused(cmd, client, flags)
	case flags.file != "":
		return runFromFile(cmd, client, flags)
	default:
		return runExisting(cmd, client, flags)
	}
}

// applyRuntimeKnobs threads the imperative run flags onto the cell doc handed to
// CreateCell / StartCell: --rm (auto-delete on workload exit), --env (runtime
// env injection into the attachable container, transport-only Spec.RuntimeEnv
// per #834), and --ignore-disk-pressure (transport-only guard bypass, #1035).
// All three are transport-only fields the daemon does not persist. The
// --from-config / --clone per-cell *override* env (the persisted kind) is
// applied earlier by cell.Materialize, not here.
func applyRuntimeKnobs(cellDoc *v1beta1.CellDoc, flags runFlags) {
	if flags.autoDelete {
		cellDoc.Spec.AutoDelete = true
	}
	if len(flags.envArgs) > 0 {
		cellDoc.Spec.RuntimeEnv = flags.envArgs
	}
	if flags.ignoreDiskPressure {
		cellDoc.Spec.IgnoreDiskPressure = true
	}
}

// runFused implements the create+start+attach form (--from-blueprint /
// --from-config / --clone). The create half delegates to cell.Materialize — the
// same function `kuke create cell` runs — so the produced CellDoc is identical
// to `kuke create cell --from-...` followed by `kuke start` (epic:cell-identity
// #1025, AC#2/#4). run then create+starts it via CreateCell (where create cell
// would call MaterializeCell and leave it stopped) and attaches.
func runFused(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) error {
	src := cell.SourceFlags{
		Name:               flags.name,
		Realm:              pickLocation("", &config.KUKE_RUN_REALM),
		Space:              pickLocation("", &config.KUKE_RUN_SPACE),
		Stack:              pickLocation("", &config.KUKE_RUN_STACK),
		BlueprintName:      flags.blueprintName,
		ConfigName:         flags.configName,
		CloneSource:        flags.cloneSource,
		ParamArgs:          flags.paramArgs,
		ParamFile:          flags.paramFile,
		EnvArgs:            flags.envArgs,
		IgnoreDiskPressure: flags.ignoreDiskPressure,
	}
	// The scope-Var bundle `kuke run` feeds cell.Materialize for binding-lookup
	// scope (built inline rather than as a package global per gochecknoglobals).
	cellDoc, err := cell.Materialize(cmd, client, src, cell.ScopeVars{
		Realm: &config.KUKE_RUN_REALM,
		Space: &config.KUKE_RUN_SPACE,
		Stack: &config.KUKE_RUN_STACK,
	})
	if err != nil {
		return err
	}
	// --rm is a run-only knob (not part of the shared SourceFlags); apply it
	// after materialisation. --env on this path is the persisted per-cell
	// override already baked in by Materialize, so applyRuntimeKnobs is not used.
	if flags.autoDelete {
		cellDoc.Spec.AutoDelete = true
	}

	// The fused form is a create: refuse if a cell already lives at the
	// materialised name (parity with `kuke create cell`'s collision refusal; the
	// generated <prefix>-<6hex> name is probed free, so this only fires for an
	// explicit --name collision). Point the operator at `kuke run <cell>` for the
	// start+attach-existing path.
	pre, getErr := client.GetCell(cmd.Context(), cellDoc)
	switch {
	case getErr == nil && pre.MetadataExists:
		return fmt.Errorf(
			"cell %q already exists in realm=%q space=%q stack=%q; run `kuke run %s` to "+
				"start+attach it, or `kuke delete cell %s` before re-creating",
			cellDoc.Metadata.Name, cellDoc.Spec.RealmID, cellDoc.Spec.SpaceID,
			cellDoc.Spec.StackID, cellDoc.Metadata.Name, cellDoc.Metadata.Name,
		)
	case getErr != nil && !errors.Is(getErr, errdefs.ErrCellNotFound):
		return getErr
	}

	result, err := client.CreateCell(cmd.Context(), cellDoc)
	if err != nil {
		return err
	}
	if printErr := printRunResult(cmd, result, flags.output); printErr != nil {
		return printErr
	}
	if !flags.detach {
		return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
	}
	return nil
}

// runFromFile implements the -f manifest path: parse the single-cell YAML,
// resolve scope, apply the imperative runtime knobs, then create-or-attach by
// metadata.name. A divergent on-disk spec warns-and-attaches by default
// (#986) or hard-fails under --require-synced.
func runFromFile(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) error {
	cellDoc, err := loadFromFile(flags.file)
	if err != nil {
		return err
	}
	resolveCellLocation(&cellDoc)
	applyRuntimeKnobs(&cellDoc, flags)

	if validateErr := validateResolvedCell(cellDoc); validateErr != nil {
		return validateErr
	}

	pre, getErr := client.GetCell(cmd.Context(), cellDoc)
	switch {
	case getErr == nil && pre.MetadataExists:
		if changed := divergedFields(pre.Cell.Spec, cellDoc.Spec); len(changed) > 0 {
			if warnErr := warnDivergentNamedCell(cmd.ErrOrStderr(), cellDoc, changed, flags); warnErr != nil {
				return warnErr
			}
		}
		return runExistingCell(cmd, client, cellDoc, pre, flags)
	case getErr != nil && !errors.Is(getErr, errdefs.ErrCellNotFound):
		return getErr
	}

	// No live cell: create and attach.
	result, err := client.CreateCell(cmd.Context(), cellDoc)
	if err != nil {
		return err
	}
	if printErr := printRunResult(cmd, result, flags.output); printErr != nil {
		return printErr
	}
	if !flags.detach {
		return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
	}
	return nil
}

// runExisting implements `kuke run <cell>`: start + attach an existing cell (the
// docker `start -a` analogue). There is no source to materialise from — the cell
// must already exist; a miss is an error pointing at the create paths. --env
// injects runtime env at start time; --rm deletes on workload exit.
func runExisting(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) error {
	lookup := v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: flags.cellName},
	}
	resolveCellLocation(&lookup)

	pre, getErr := client.GetCell(cmd.Context(), lookup)
	switch {
	case errors.Is(getErr, errdefs.ErrCellNotFound) || (getErr == nil && !pre.MetadataExists):
		return fmt.Errorf(
			"%w (cell %q in realm=%q space=%q stack=%q); create it first with "+
				"`kuke create cell` / `kuke run --from-...` or `kuke run -f <file>`",
			errdefs.ErrCellNotFound, flags.cellName,
			lookup.Spec.RealmID, lookup.Spec.SpaceID, lookup.Spec.StackID,
		)
	case getErr != nil:
		return getErr
	}

	// Drive start+attach against the live cell's spec (its containers), threading
	// the runtime knobs onto the doc StartCell receives.
	cellDoc := pre.Cell
	applyRuntimeKnobs(&cellDoc, flags)
	return runExistingCell(cmd, client, cellDoc, pre, flags)
}

// warnDivergentNamedCell reacts to a live cell whose spec diverges from the -f
// manifest the operator asked to run. Only the -f path materialises a cell from
// an authoritative on-disk spec and so reaches this branch; the fused
// create+start+attach sources always create a fresh cell (or refuse on name
// collision), and the `kuke run <cell>` path has no source to compare against.
//
// The default behaviour (#986) is warn-and-attach: print a one-line `notice:`
// naming the diverging fields and the `kuke apply -f` pointer, then return nil
// so runFromFile falls through to runExistingCell and attaches to the live
// state — `kuke run` is the operator's interactive escape hatch and obstruction
// on drift defeats the workflow. CI/scripted callers that need a hard fail opt
// in via --require-synced, which returns the pre-#986 refusal error shape.
func warnDivergentNamedCell(w io.Writer, cellDoc v1beta1.CellDoc, changed []string, flags runFlags) error {
	fields := strings.Join(changed, ", ")
	const source = "on-disk spec"
	const pointer = "use `kuke apply -f` to update"
	if flags.requireSynced {
		return fmt.Errorf(
			"live cell %q spec differs from %s (%s) — refusing to attach (--require-synced); %s",
			cellDoc.Metadata.Name, source, fields, pointer,
		)
	}
	fmt.Fprintf(w,
		"notice: cell %q is OutOfSync with %s (diverging: %s); attaching to current live state — %s\n",
		cellDoc.Metadata.Name, source, fields, pointer,
	)
	return nil
}

// runExistingCell drives the create-or-attach state machine for a live cell
// whose on-disk spec either matches the file or whose divergence the caller
// already chose to warn-and-attach past (the #986 default; the
// `--require-synced` opt-in still refuses upstream and never reaches here).
// The four terminal states:
//
//   - Ready   → reconcile against containerd first. The recorded state asserts
//     a live root container; if containerd has lost it (a daemon/host restart
//     per #648 dropped the containers while the on-disk metadata survived), the
//     no-op summary would report phantom `container … already existed` /
//     `containers: started` and then attach to a dead socket
//     (`connection refused`, #654). On that divergence, refuse with a
//     delete-then-rerun pointer. Otherwise print a no-op summary, then attach.
//     Re-entering the daemon's CreateCell→StartCell path on a healthy cell trips
//     the runner's CNI duplicate-allocation bug (#630), so the short-circuit is
//     mandatory, not just an optimisation.
//   - Stopped → StartCell, then attach. The routing is the correct verb (the
//     prior fall-through to CreateCell was itself an unsafe re-entry). The
//     live start+attach is gated on the same CNI fix (#630): StartCell re-ADDs
//     the root container to its bridge network, which the daemon rejects with
//     `duplicate allocation` until it releases the prior allocation on
//     teardown. The path converges with no further CLI change once #630 lands.
//   - error / partial (Pending, Failed, Unknown) → refuse with a
//     `kuke delete cell <name>` pointer (parity with the `-c` identity contract
//     in #625). `run` does not reconcile a degraded cell in place; the operator
//     deletes and re-runs.
func runExistingCell(
	cmd *cobra.Command,
	client kukeonv1.Client,
	cellDoc v1beta1.CellDoc,
	pre kukeonv1.GetCellResult,
	flags runFlags,
) error {
	switch pre.Cell.Status.State {
	case v1beta1.CellStateReady:
		// Divergence guard (#654, #683): the metadata records this cell as
		// Ready, but its root-container task is gone from containerd — typically
		// a daemon/host restart (#648) dropped the cell's tasks while the
		// container records and on-disk metadata survived. Keying on record
		// existence alone (#654's original guard) passes in that case because
		// the records outlive the tasks, so we gate on task liveness instead
		// (#683). Trusting the stale metadata would print phantom `already
		// existed` / `containers: started` lines and then attach to a socket
		// backed by nothing (`connection refused`). Refuse with a
		// delete-then-rerun pointer instead. We deliberately do not recreate in
		// place: re-entering CreateCell/StartCell here is the exact #630 CNI
		// duplicate-allocation re-entry the Ready short-circuit exists to avoid,
		// and the operator-driven delete releases any half-held allocation
		// cleanly. (A legitimately Stopped cell has no live root task by
		// design — StopCell deletes it — so this guard is scoped to Ready, where
		// the task is asserted live.)
		if err := kukshared.GuardCellTaskLiveness(pre, cellDoc.Metadata.Name); err != nil {
			return err
		}
		if printErr := printRunResult(cmd, noOpResultFromGet(pre), flags.output); printErr != nil {
			return printErr
		}
	case v1beta1.CellStateStopped:
		startRes, err := client.StartCell(cmd.Context(), cellDoc)
		if err != nil {
			return err
		}
		if printErr := printRunResult(cmd, startedResultFromGet(pre, startRes.Started), flags.output); printErr != nil {
			return printErr
		}
	case v1beta1.CellStatePending, v1beta1.CellStateFailed, v1beta1.CellStateUnknown:
		// error / partial: no clean start path. Refuse and point the operator
		// at delete-then-rerun. Listing the states explicitly (rather than a
		// default) keeps the exhaustive linter as a forward guard: a new
		// CellState must be categorised here deliberately.
		return fmt.Errorf(
			"cell %q exists in %s state; delete it with `kuke delete cell %s` before re-running",
			cellDoc.Metadata.Name,
			pre.Cell.Status.State.String(),
			cellDoc.Metadata.Name,
		)
	}

	if !flags.detach {
		return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
	}
	return nil
}

// attachAndMaybeAutoDelete drives the attach loop and, under --rm in
// the default attach mode, fires KillCell once the loop returns — but
// only if the loop exited because the workload ended (peer hangup,
// shell exit, controller error). A clean ^]^] detach (issue #279)
// leaves the cell alive so the operator can re-attach later, matching
// `kuke attach`'s exit-0 semantics.
//
// Both the create-and-start path and the already-Ready idempotent
// short-circuit funnel through here so re-running `kuke run --rm`
// against an up cell still gets cleanup on workload exit (issue #265
// regression: the original fix only patched the create path).
//
// When the attach target is a peer of a long-lived root (`sleep
// infinity` is the standard idiom), the root task never exits on its
// own and the reconciler's auto-delete trigger never fires. KillCell on
// the workload-ended path SIGKILL's the root task so the next reconcile
// pass reaps the cell. Best-effort per the --rm contract: a cleanup
// failure does not override attachErr.
func attachAndMaybeAutoDelete(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	flags runFlags,
) error {
	detached, attachErr := attachAfterRun(cmd, client, doc, flags.containerFlag)
	if flags.autoDelete && !detached {
		autoDeleteAfterAttach(cmd, client, doc)
	}
	return attachErr
}

// autoDeleteAfterAttach drives the --rm cleanup path under attach mode. KillCell is
// idempotent — if the attach target was the root and the user typed
// `exit`, the root task is already gone and KillCell just confirms it;
// the kill+delete sequence in autoDeleteCell tolerates a missing task.
// A KillCell failure (daemon down, RPC error, cell already gone) is
// reported but not returned: the operator has detached, --rm is
// best-effort, and the daemon's reconciler still owns final cleanup.
func autoDeleteAfterAttach(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) {
	if _, err := client.KillCell(cmd.Context(), doc); err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return
		}
		fmt.Fprintf(cmd.ErrOrStderr(),
			"kuke run: --rm cleanup: failed to kill cell %q: %v\n",
			doc.Metadata.Name, err)
	}
}

// attachAfterRun resolves the post-start attach target per the
// documented precedence and drives the in-process sbsh attach loop.
// The selection runs against cellDoc.Spec rather than re-querying the
// daemon: by this point the resolved doc is what we just told the
// daemon to create, so the attachable set the user authored is
// authoritative.
//
// Returns the (detached, err) tri-state from runAttachLoop. A
// pre-attach selection failure is reported as (false, err) so
// attachAndMaybeAutoDelete still fires KillCell — the operator asked
// for --rm and the cell exists; whether or not the attach loop ever ran
// does not change that intent.
func attachAfterRun(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	containerFlag string,
) (bool, error) {
	target, err := pickAttachTarget(doc.Spec, doc.Metadata.Name, containerFlag)
	if err != nil {
		return false, err
	}
	return runAttachLoop(cmd, client, doc, target)
}

// loadFromFile preserves the phase-1c -f path: read the file (or stdin),
// parse the single Cell document, and return it.
func loadFromFile(file string) (v1beta1.CellDoc, error) {
	reader, cleanup, err := kukshared.ReadFileOrStdin(file)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	defer func() { _ = cleanup() }()

	rawYAML, err := io.ReadAll(reader)
	if err != nil {
		return v1beta1.CellDoc{}, fmt.Errorf("failed to read input: %w", err)
	}
	return parseSingleCellDoc(rawYAML)
}

// parseSingleCellDoc parses raw YAML and returns the single-doc Cell. It errors
// on multi-doc input and on non-Cell kinds, matching the AC for `kuke run -f`.
// Spec validation is deferred to validateResolvedCell so the caller can fill
// realm/space/stack from --realm/--space/--stack or session defaults first.
func parseSingleCellDoc(raw []byte) (v1beta1.CellDoc, error) {
	rawDocs, err := parser.ParseDocuments(bytes.NewReader(raw))
	if err != nil {
		return v1beta1.CellDoc{}, fmt.Errorf("failed to parse YAML: %w", err)
	}
	if len(rawDocs) != 1 {
		return v1beta1.CellDoc{},
			fmt.Errorf(
				"expected a single Cell document, got %d (use `kuke apply -f` for multi-document streams)",
				len(rawDocs),
			)
	}

	doc, parseErr := parser.ParseDocument(0, rawDocs[0])
	if parseErr != nil {
		return v1beta1.CellDoc{}, parseErr
	}
	if doc.Kind != v1beta1.KindCell {
		return v1beta1.CellDoc{},
			fmt.Errorf(
				"expected kind %q, got %q (use `kuke apply -f` for non-Cell resources)",
				v1beta1.KindCell,
				doc.Kind,
			)
	}
	if doc.CellDoc == nil {
		return v1beta1.CellDoc{}, errors.New("cell document is nil after parse")
	}
	return *doc.CellDoc, nil
}

// validateResolvedCell runs the parser's structural validation after location
// flags have been merged onto the doc.
func validateResolvedCell(doc v1beta1.CellDoc) error {
	wrapper := &parser.Document{
		Index:      0,
		APIVersion: doc.APIVersion,
		Kind:       v1beta1.KindCell,
		CellDoc:    &doc,
	}
	if validationErr := parser.ValidateDocument(wrapper); validationErr != nil {
		return validationErr
	}
	return nil
}

// resolveCellLocation fills missing realm/space/stack on the doc from
// --realm/--space/--stack flags or the session defaults declared on
// config.KUKE_RUN_{REALM,SPACE,STACK}. The doc wins when it sets a value.
func resolveCellLocation(doc *v1beta1.CellDoc) {
	doc.Spec.RealmID = pickLocation(doc.Spec.RealmID, &config.KUKE_RUN_REALM)
	doc.Spec.SpaceID = pickLocation(doc.Spec.SpaceID, &config.KUKE_RUN_SPACE)
	doc.Spec.StackID = pickLocation(doc.Spec.StackID, &config.KUKE_RUN_STACK)
}

// pickLocation fills a realm/space/stack coordinate on the materialised cell
// doc itself. The doc wins when it sets a value; otherwise viper.GetString
// (bound to the cobra flag in NewRunCmd) provides the operator's per-session
// pin; otherwise kv.ValueOrDefault walks env → default. Lookup-side scope
// resolution lives in cmd/kuke/shared.PickLookupRealm / ExplicitScope —
// those bypass viper for the IsSet-trip reason their commentary explains;
// pickLocation here is the materialised-cell fill path that does want the
// viper-backed session pin.
func pickLocation(fromDoc string, kv *config.Var) string {
	if v := strings.TrimSpace(fromDoc); v != "" {
		return v
	}
	if v := strings.TrimSpace(viper.GetString(kv.ViperKey)); v != "" {
		return v
	}
	return strings.TrimSpace(kv.ValueOrDefault())
}

// divergedFields names the fields where the on-disk cell disagrees with the
// file. The check covers structural identity (container count, container id
// set, cell location) and the operator-controlled per-container fields the
// runner does NOT rewrite at create time (image, command, args, env, ports,
// volumes, networks, host-namespace opts, privileged, attachable,
// restartPolicy, workingDir). Per the AC for issue #468, a genuine spec
// rewrite — including an image swap — must route through `kuke apply -f`
// instead of silently no-opping under `kuke run -f`.
//
// Each entry is shaped `<path> (actual=<v>, desired=<v>)` so the reject error
// surfaces both sides verbatim — without this, an operator hitting a false
// positive (e.g. issue #984 — `kuke run --from-config <cfg>` rejecting immediately after
// a successful `kuke restart` per the OutOfSync reconcile path) has no
// way to tell which side of the comparison normalised differently from the
// other. Test assertions on the field path itself (e.g.
// `spec.containers["main"].image`) still match because the path prefix is
// preserved verbatim before the `(actual=…)` suffix.
//
// Root containers are filtered out of the count/id-set comparison on both
// sides. The runner synthesizes a root container during create if the YAML
// omitted one (`docs/examples/hello-world.yaml` is the canonical case), so a
// raw count comparison would treat every re-run of an existing file as
// divergent (actual=root+web, desired=web) and route the operator to
// `kuke apply -f` even though nothing changed. Comparing only the
// user-supplied (non-root) entries restores the idempotent path while still
// catching real adds/removes/renames among the user containers.
//
// Fields filled in by the runner from `space.spec.defaults.container`
// (user, readOnlyRootFilesystem, capabilities, securityOpts, tmpfs,
// resources) are deliberately excluded from the per-container check: a
// YAML that omits them would be filled in on create and then compare
// non-equal on the next `run` against the same file, false-flagging the
// idempotent path. Those fields still drive divergence detection on
// `kuke apply -f`, which has the on-disk + post-merge view; `run` stops
// at the user-authored surface.
func divergedFields(actual, desired v1beta1.CellSpec) []string {
	var diffs []string

	if actual.RealmID != "" && actual.RealmID != desired.RealmID {
		diffs = append(diffs, fmt.Sprintf(
			"spec.realmId (actual=%q, desired=%q)", actual.RealmID, desired.RealmID))
	}
	if actual.SpaceID != "" && actual.SpaceID != desired.SpaceID {
		diffs = append(diffs, fmt.Sprintf(
			"spec.spaceId (actual=%q, desired=%q)", actual.SpaceID, desired.SpaceID))
	}
	if actual.StackID != "" && actual.StackID != desired.StackID {
		diffs = append(diffs, fmt.Sprintf(
			"spec.stackId (actual=%q, desired=%q)", actual.StackID, desired.StackID))
	}

	if len(actual.Containers) == 0 {
		return diffs
	}

	actualUser := nonRootContainers(actual.Containers)
	desiredUser := nonRootContainers(desired.Containers)

	if len(actualUser) != len(desiredUser) {
		diffs = append(diffs, fmt.Sprintf(
			"spec.containers (count: actual=%d, desired=%d)",
			len(actualUser), len(desiredUser),
		))
		return diffs
	}

	desiredByID := make(map[string]v1beta1.ContainerSpec, len(desiredUser))
	for _, c := range desiredUser {
		desiredByID[c.ID] = c
	}
	for _, ac := range actualUser {
		dc, ok := desiredByID[ac.ID]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("spec.containers[%q] (missing in file)", ac.ID))
			continue
		}
		if ac.Root != dc.Root {
			diffs = append(diffs, fmt.Sprintf(
				"spec.containers[%q].root (actual=%v, desired=%v)", ac.ID, ac.Root, dc.Root))
		}
		for _, field := range divergedContainerFields(ac, dc) {
			diffs = append(diffs, fmt.Sprintf("spec.containers[%q].%s", ac.ID, field))
		}
	}
	return diffs
}

// divergedContainerFields returns the user-authored container fields where
// actual disagrees with desired. Restricted to fields the runner does NOT
// fill in or normalize at create/start time (so a fresh cell never
// false-flags on the next `kuke run -f` of the same file): image, command,
// args, workingDir, env, ports, volumes, networks, networksAliases,
// privileged, hostNetwork, hostPID, hostCgroup, attachable, restartPolicy,
// secrets, tty.
// Fields that inherit from `space.spec.defaults.container` (see
// internal/modelhub.ApplySpaceDefaultsToContainer) are deliberately
// excluded — their on-disk value is post-merge while the YAML side is
// pre-merge, so comparing them would always trip on a default-using YAML.
func divergedContainerFields(actual, desired v1beta1.ContainerSpec) []string {
	var fields []string
	if actual.Image != desired.Image {
		fields = append(fields, scalarDiff("image", actual.Image, desired.Image))
	}
	if actual.Command != desired.Command {
		fields = append(fields, scalarDiff("command", actual.Command, desired.Command))
	}
	if !stringSlicesEqual(actual.Args, desired.Args) {
		fields = append(fields, sliceDiff("args", actual.Args, desired.Args))
	}
	if actual.WorkingDir != desired.WorkingDir {
		fields = append(fields, scalarDiff("workingDir", actual.WorkingDir, desired.WorkingDir))
	}
	if !stringSlicesEqual(actual.Env, desired.Env) {
		fields = append(fields, sliceDiff("env", actual.Env, desired.Env))
	}
	if !stringSlicesEqual(actual.Ports, desired.Ports) {
		fields = append(fields, sliceDiff("ports", actual.Ports, desired.Ports))
	}
	if !volumeMountsEqual(actual.Volumes, desired.Volumes) {
		fields = append(fields, valueDiff("volumes", actual.Volumes, desired.Volumes))
	}
	if !stringSlicesEqual(actual.Networks, desired.Networks) {
		fields = append(fields, sliceDiff("networks", actual.Networks, desired.Networks))
	}
	if !stringSlicesEqual(actual.NetworksAliases, desired.NetworksAliases) {
		fields = append(fields, sliceDiff("networksAliases", actual.NetworksAliases, desired.NetworksAliases))
	}
	if actual.Privileged != desired.Privileged {
		fields = append(fields, boolDiff("privileged", actual.Privileged, desired.Privileged))
	}
	if actual.HostNetwork != desired.HostNetwork {
		fields = append(fields, boolDiff("hostNetwork", actual.HostNetwork, desired.HostNetwork))
	}
	if actual.HostPID != desired.HostPID {
		fields = append(fields, boolDiff("hostPID", actual.HostPID, desired.HostPID))
	}
	if actual.HostCgroup != desired.HostCgroup {
		fields = append(fields, boolDiff("hostCgroup", actual.HostCgroup, desired.HostCgroup))
	}
	if actual.Attachable != desired.Attachable {
		fields = append(fields, boolDiff("attachable", actual.Attachable, desired.Attachable))
	}
	if actual.RestartPolicy != desired.RestartPolicy {
		fields = append(fields, scalarDiff("restartPolicy", actual.RestartPolicy, desired.RestartPolicy))
	}
	if !containerSecretsEqual(actual.Secrets, desired.Secrets) {
		fields = append(fields, valueDiff("secrets", actual.Secrets, desired.Secrets))
	}
	if !containerTtysEqual(actual.Tty, desired.Tty) {
		fields = append(fields, valueDiff("tty", actual.Tty, desired.Tty))
	}
	return fields
}

// scalarDiff formats a single string field's actual/desired pair as
// `<name> (actual=%q, desired=%q)`. Used by divergedContainerFields so the
// reject error names both sides — see divergedFields' rationale.
func scalarDiff(name, actual, desired string) string {
	return fmt.Sprintf("%s (actual=%q, desired=%q)", name, actual, desired)
}

// boolDiff is scalarDiff's bool sibling — prints `actual=true desired=false`
// rather than quoting the values.
func boolDiff(name string, actual, desired bool) string {
	return fmt.Sprintf("%s (actual=%v, desired=%v)", name, actual, desired)
}

// sliceDiff formats a string-slice's actual/desired pair via `%q` so each
// entry stays quoted (`["a" "b"]`) and an embedded space is unambiguous.
func sliceDiff(name string, actual, desired []string) string {
	return fmt.Sprintf("%s (actual=%q, desired=%q)", name, actual, desired)
}

// valueDiff is the fallback formatter for non-string-slice composite fields
// (Volumes, Secrets, Tty). Uses `%+v` so struct field names render alongside
// their values — the reader needs to see e.g. `{Source:/host …}` to diagnose
// the divergence, not a bare positional `{[…] …}`.
func valueDiff(name string, actual, desired interface{}) string {
	return fmt.Sprintf("%s (actual=%+v, desired=%+v)", name, actual, desired)
}

// containerSecretsEqual compares two ContainerSecret slices field-by-field.
// nil and empty are treated as equal so YAML that omits secrets does not
// register as drift against on-disk metadata that persisted it as nil.
// ContainerSecret carries a *ContainerSecretRef pointer; struct == on it
// would compare that pointer by identity, and the apischeme round-trip
// allocates a fresh *ContainerSecretRef on each conversion, so YAML-decoded
// and daemon-persisted sides are always address-distinct even when
// value-equal. Issue #920.
func containerSecretsEqual(a, b []v1beta1.ContainerSecret) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name ||
			a[i].FromFile != b[i].FromFile ||
			a[i].FromEnv != b[i].FromEnv ||
			a[i].MountPath != b[i].MountPath ||
			!secretRefEqual(a[i].SecretRef, b[i].SecretRef) {
			return false
		}
	}
	return true
}

// secretRefEqual compares two *ContainerSecretRef by deref'd value, treating
// matching nil-ness as equal. ContainerSecretRef is all scalar strings, so a
// direct == on the dereferenced struct is safe.
func secretRefEqual(a, b *v1beta1.ContainerSecretRef) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// containerTtysEqual compares two ContainerTty pointers. Empty values on
// either side are treated as equal because
// internal/apischeme.convertContainerTtyToInternal normalizes an IsEmpty
// input to nil at persistence: on-disk Tty=nil and a YAML carrying an
// otherwise-empty &ContainerTty{} must not register as drift.
func containerTtysEqual(a, b *v1beta1.ContainerTty) bool {
	if a.IsEmpty() && b.IsEmpty() {
		return true
	}
	if a.IsEmpty() != b.IsEmpty() {
		return false
	}
	if a.Prompt != b.Prompt ||
		a.LogFile != b.LogFile ||
		a.LogLevel != b.LogLevel {
		return false
	}
	if len(a.OnInit) != len(b.OnInit) {
		return false
	}
	for i := range a.OnInit {
		if a.OnInit[i] != b.OnInit[i] {
			return false
		}
	}
	return true
}

// stringSlicesEqual reports whether two []string carry the same elements
// in the same order. nil and empty are treated as equal so YAML that
// omits an optional list does not register as drift against on-disk
// metadata that persisted it as an empty array.
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// volumeMountsEqual compares two VolumeMount slices field-by-field. The
// VolumeMount struct has no slice/map fields, so a direct == on each
// element is enough.
func volumeMountsEqual(a, b []v1beta1.VolumeMount) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// nonRootContainers returns the user-supplied subset of cs — entries where
// Root is false. Used to exclude the runner-synthesized root from divergence
// comparison; see divergedFields.
func nonRootContainers(cs []v1beta1.ContainerSpec) []v1beta1.ContainerSpec {
	out := make([]v1beta1.ContainerSpec, 0, len(cs))
	for _, c := range cs {
		if !c.Root {
			out = append(out, c)
		}
	}
	return out
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.DaemonClientFromCmd(cmd)
}
