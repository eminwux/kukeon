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
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
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
		"kind: CellBlueprint and use `kuke run -b <name>` (or `kuke run <config>` " +
		"for a daemon-stored CellConfig); see docs/site/guides/migrate-cellprofile-to-blueprint.md",
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewRunCmd builds the `kuke run` cobra command. The optional positional arg
// names a daemon-stored CellConfig; `-f` reads a single-cell YAML doc; `-b`
// names a daemon-stored CellBlueprint. Exactly one of the three is required.
// By default, after the cell starts the CLI drops the operator into the
// cell's attachable terminal; `-d/--detach` opts out of the post-start attach.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run [<config> | -f <file> | -b <blueprint>]",
		Short: "Create and start a single cell from a daemon-stored CellConfig (positional), a YAML file, or a daemon-stored blueprint",
		Long: "Create and start a single cell from a daemon-stored CellConfig " +
			"(positional `<config>` name), from a YAML file or stdin (-f), or from " +
			"a daemon-stored CellBlueprint (-b), resolved from the scope named by " +
			"--realm/--space/--stack. The positional `<config>` is the highest-frequency " +
			"path (daily dev-cell spawn-and-attach); the other sources stay flag-driven " +
			"so the positional case is unambiguous. -b substitutes scalar --param values " +
			"and materializes a fresh <prefix>-<6hex> cell every invocation (always " +
			"fresh). The positional config path walks the identity state machine of " +
			"the named CellConfig: at most one live cell per Config, with a " +
			"deterministic name (the Config's metadata.name), materialising from the " +
			"referenced Blueprint with the Config's scalar values plus repo / secret " +
			"slot fills the first time and attaching to the existing cell on " +
			"subsequent runs (idempotent). When the live cell's spec differs from the " +
			"materialisation of the current Config + Blueprint, the positional config " +
			"path refuses to attach and points the operator at " +
			"`kuke restart <cell>` to reconcile (the daemon's OutOfSync detector " +
			"picks up the Config divergence and the restart reconciles implicitly: " +
			"stops, updates, starts). -b with --name applies the same divergence " +
			"discipline against the pinned cell name, but -b-lineage cells have no " +
			"implicit reconcile — the pointer is `kuke delete cell <cell>` + re-run " +
			"(or promote to a CellConfig for `kuke restart` reconcile workflows). " +
			"--new on the " +
			"positional config path materializes a fresh `<config-name>-<6hex>` cell on " +
			"every invocation instead — opt-in fire-and-forget sandboxes from a " +
			"Config, preserving the kukeon.io/config lineage label. `--new --name X` " +
			"creates a cell named X from the Config and fails if X is already in the " +
			"target realm (create-or-fail; unlike `--name X` alone, which idempotently " +
			"attaches on collision). -f is " +
			"create-or-attach by metadata.name: a missing cell is created and " +
			"attached; a Ready cell is attached as a no-op; a Stopped cell is started " +
			"then attached; a divergent on-disk spec is refused (use `kuke apply -f` " +
			"to update); a cell in an error or partial state is refused with a " +
			"`kuke delete cell <name>` pointer. To re-attach to an existing cell use " +
			"`kuke attach <cell>`. --env KEY=VALUE is the orthogonal runtime knob " +
			"(repeatable; per-invocation injection into the attachable container's env at " +
			"start time). It works with every source (positional, -f, -b) and every " +
			"identity flag (--new, --clone, --reuse); the entries do not change the cell " +
			"spec and do not persist, so the divergent-spec check above does not trip on " +
			"prior --env-injected keys.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: config.CompleteConfigNames,
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

	cmd.Flags().
		StringP("file", "f", "", "File to read YAML from (use - for stdin); mutually exclusive with the <config> positional and -b")
	_ = viper.BindPFlag(config.KUKE_RUN_FILE.ViperKey, cmd.Flags().Lookup("file"))

	cmd.Flags().StringP("blueprint", "b", "",
		"Daemon-stored CellBlueprint name to run, resolved from the scope named by "+
			"--realm/--space/--stack; mutually exclusive with -f and the <config> positional. Substitutes scalar --param "+
			"values and materializes a fresh <prefix>-<6hex> cell.")
	_ = viper.BindPFlag(config.KUKE_RUN_BLUEPRINT.ViperKey, cmd.Flags().Lookup("blueprint"))

	cmd.Flags().String("name", "",
		"Override the materialized cell name (default: <metadata.name>-<6hex>). "+
			"Valid with -b; rejected with -f, where metadata.name is the cell name "+
			"verbatim. Valid with the <config> positional: `<config> --name X` does "+
			"idempotent attach to cell X using the Config's spec; `<config> --new "+
			"--name X` is the create-or-fail variant (fail if X exists). Without "+
			"--name on the <config> path the cell uses the Config's stable name.")
	_ = viper.BindPFlag(config.KUKE_RUN_NAME.ViperKey, cmd.Flags().Lookup("name"))

	cmd.Flags().Bool("new", false,
		"Materialize a fresh `<config-name>-<6hex>` cell on every invocation of the <config> "+
			"positional instead of the Config's deterministic stable name. Each invocation produces a distinct "+
			"cell; the kukeon.io/config=<name> lineage label is preserved (operator can list "+
			"all spawns: `kuke get cells -l kukeon.io/config=<name>`). Combinable with --name "+
			"to pin a specific cell name (create-or-fail: `--new --name X` fails if cell X "+
			"already exists in the realm) and with --rm (one-shot ephemeral cell). Only "+
			"valid with the <config> positional — rejected with -f (where metadata.name is the "+
			"cell name verbatim) and redundant for -b (which already generates a fresh name). "+
			"Mutually exclusive with --clone (which forks a persistent clone Config) "+
			"and --reuse (which restarts an existing clone). For multi-instance from one Config use --clone instead.")
	_ = viper.BindPFlag(config.KUKE_RUN_NEW.ViperKey, cmd.Flags().Lookup("new"))

	cmd.Flags().Bool("clone", false,
		"Fork the <config> Config into a new persistent clone Config, then run "+
			"the cell from the clone (#839). The clone's metadata.name is `<src>-<N>` "+
			"with N the lowest unused integer >= 0 among clones of <src> in the target "+
			"realm (gap-fill counter, atomic under concurrent invocations). Combine with "+
			"--name X for an explicit-name create-or-fail clone (fails if a CellConfig X "+
			"already exists). The clone carries metadata.annotations."+
			"kukeon.io/source-config=<src> as the lineage marker, and its spec is a deep "+
			"copy of the source's — independently editable via `kuke apply -f`. The cell "+
			"started from the clone uses the clone's stable name, so subsequent "+
			"`kuke run <clone-name>` is idempotent. Use cases: interactive multi-instance "+
			"(kukeon-dev-0, kukeon-dev-1, ...) and cron pool seeding for --reuse (#835). "+
			"Only valid with the <config> positional; mutually exclusive with --new, "+
			"--reuse, and --rm.")
	_ = viper.BindPFlag(config.KUKE_RUN_CLONE.ViperKey, cmd.Flags().Lookup("clone"))

	cmd.Flags().Bool("reuse", false,
		"Pick a healthy-Stopped clone of the <config> Config (lowest counter N "+
			"first, ascending), start its cell in-place via StartCell — preserving "+
			"the containerd overlay filesystem (project repo clone, `.claude.json`, "+
			"any per-cell state) across the stop/start transition — and attach (#835). "+
			"On an empty pool, falls back to --clone's code path (atomic gap-fill "+
			"counter allocation, new clone CellConfig, fresh cell), so the operator "+
			"never sees a 'pool empty' error on the first tick or after a host "+
			"reboot. Running clones are invisible to the pool query — concurrent "+
			"--reuse invocations against the same source pick distinct cells via "+
			"StartCell's daemon-side atomic claim, and an all-Running pool falls "+
			"back to --clone (forks the next-N). Never deletes a clone's cell. "+
			"Cells in Pending/Failed/Unknown sub-states are excluded from the pick "+
			"set. Driver use case: cron-driven skill execution (`kuke run <cfg> "+
			"--reuse --env KEY=val -d`) where the project repo clone happens once "+
			"per pool member rather than once per tick. Only valid with the <config> "+
			"positional; mutually exclusive with --new, --clone, --name, and --rm.")
	_ = viper.BindPFlag(config.KUKE_RUN_REUSE.ViperKey, cmd.Flags().Lookup("reuse"))

	cmd.Flags().StringArray("env", nil,
		"Runtime container env entry as KEY=VALUE; repeatable. Injects extra env into "+
			"the cell's attachable container at start time, in addition to "+
			"spec.cell.containers[<attachable>].env. Empty value (KEY=) is allowed; missing "+
			"`=` is rejected. A key that collides with an existing entry in the attachable "+
			"container's spec env OVERRIDES the spec value. --env is the per-invocation knob "+
			"(runtime; does not change the cell spec); for render-time spec substitution use "+
			"--param (blueprint path only). Valid with all source paths (the <config> "+
			"positional, -f, -b) and all identity flags (--new, --clone, --reuse). The "+
			"injected entries do NOT persist into the cell metadata, so the divergent-spec "+
			"check on a subsequent `kuke run <config>` (without --env) does not trip on the "+
			"prior injection. With --reuse, each invocation re-injects against the restarted "+
			"cell; the cell's stored spec env is unchanged across restarts.")
	// --env is read via cmd.Flags().GetStringArray("env") in parseRunFlags;
	// viper.BindPFlag is intentionally omitted because StringArray flags do
	// not round-trip cleanly through viper's GetStringSlice (issue #834).
	// KUKE_RUN_ENV stays declared in cmd/config/env.go for parity but is
	// not bound here.

	cmd.Flags().StringArray("param", nil,
		"Scalar parameter override as KEY=VALUE; repeatable. Valid with -b. "+
			"Each KEY must be declared in spec.parameters[]. Wins over the parameter's "+
			"default and over --param-file when both set the same key. Rejected with the "+
			"<config> positional: a CellConfig carries its own spec.values, edit the Config instead.")

	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed scalar parameters; one per line, "+
			"`#` starts a comment. Same declaration rules as --param. CLI --param wins on "+
			"duplicate keys. Rejected with the <config> positional (same reason as --param).")
	_ = viper.BindPFlag(config.KUKE_RUN_PARAM_FILE.ViperKey, cmd.Flags().Lookup("param-file"))

	// `config` is now a positional (see Args / ValidArgsFunction above), so the
	// cobra-side mutex/required-one machinery covers only the remaining flag
	// sources; parseRunFlags hand-rolls the positional-vs-flag mutex and the
	// "exactly one source required" check (cobra has no MarkFlagsOneRequired
	// equivalent that spans flags + positionals).
	cmd.MarkFlagsMutuallyExclusive("file", "blueprint")
	// Identity-flag mutex on the <config> path (#839): --new and --clone are
	// distinct operations (ephemeral cell vs. fork the Config). The
	// `--clone ↔ --rm` mutex lands below after the --rm flag itself is
	// declared (MarkFlagsMutuallyExclusive panics on an unknown flag name).

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
		"Refuse to attach when the live cell's spec diverges from the materialisation "+
			"of the requested source (CellConfig + Blueprint, CellBlueprint + --param, or "+
			"`-f` file). Default behavior is warn-and-attach: print a one-line `notice:` "+
			"naming the diverging fields and the reconcile pointer, then drop the operator "+
			"into the live state — `kuke run` is the interactive escape hatch for live cells, "+
			"and obstruction on drift defeats the workflow. Use --require-synced for "+
			"CI/scripted callers that need a hard fail on divergence; the error message "+
			"matches the pre-#986 refuse-on-divergence behaviour.")
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

	// `--clone ↔ --rm` mutex (#839): a persistent clone Config whose cell is
	// removed on exit is operationally messy; `kuke apply -f <clone-spec>`
	// is the right path if you want the slot without an attached cell. Must
	// land here (after --rm and --clone are declared) — MarkFlagsMutuallyExclusive
	// panics on an unknown flag name. `--new ↔ --clone` is declared with the
	// other source-mutex block above where both flags are already present.
	cmd.MarkFlagsMutuallyExclusive("new", "clone")
	cmd.MarkFlagsMutuallyExclusive("clone", "rm")
	// `--reuse` mutex set (#835):
	//   - `--reuse ↔ --new`: different operations (start an existing clone vs.
	//     materialize a fresh ephemeral cell).
	//   - `--reuse ↔ --clone`: different operations — `--reuse` *falls back*
	//     to `--clone`'s code path internally on empty pool, but the flags
	//     themselves don't combine.
	//   - `--reuse ↔ --name`: `--reuse` picks from the pool, can't dictate
	//     which slot to pick by name (use `kuke run <clone-name>` for that).
	//   - `--reuse ↔ --rm`: `--rm` would remove the cell on exit, leaving a
	//     clone Config whose cell is gone — next `--reuse` would see the
	//     clone in the pool but skip it (cell-less), forcing fork. Pool
	//     fills with cell-less clone-Config carcasses.
	cmd.MarkFlagsMutuallyExclusive("new", "reuse")
	cmd.MarkFlagsMutuallyExclusive("clone", "reuse")
	cmd.MarkFlagsMutuallyExclusive("name", "reuse")
	cmd.MarkFlagsMutuallyExclusive("reuse", "rm")

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("blueprint", config.CompleteBlueprintNames)
	// The positional <config> is wired via ValidArgsFunction on the cobra
	// command above (where `-c` previously routed flag completion).

	return cmd
}

// runFlags is the validated bundle of flag values runRun consumes after
// argument validation. Splitting it out keeps runRun under the funlen limit
// while preserving the original control flow.
type runFlags struct {
	file          string
	blueprintName string
	configName    string
	output        string
	detach        bool
	containerFlag string
	autoDelete    bool
	nameOverride  string
	newCell       bool
	clone         bool
	reuse         bool
	paramArgs     []string
	paramFile     string
	// envArgs are the validated `KEY=VALUE` entries supplied via repeatable
	// --env flags (issue #834). Empty value (`KEY=`) is preserved as-is;
	// missing `=` is rejected by parseEnvArgs before runRun observes them.
	// Threaded to the daemon via cellDoc.Spec.RuntimeEnv (yaml:"-" — never
	// persisted) where the runner merges them into the attachable
	// container's OCI process env at start time.
	envArgs []string
	// requireSynced flips warnDivergentNamedCell from warn-and-attach
	// (default, #986) to fail-fast: when set the diverging-spec branch
	// returns the same error shape the pre-#986 rejectDivergentNamedCell
	// produced so CI/scripted callers that opt in retain the exit-non-zero
	// drift signal. See the flag commentary in NewRunCmd.
	requireSynced bool
}

// validateSourceMutex enforces the "exactly one source" contract across the
// three `kuke run` inputs after issue #813 moved CellConfig from `-c/--config`
// to the positional `<config>` argument. Cobra's MarkFlagsMutuallyExclusive
// / MarkFlagsOneRequired only span flags, so this check hand-rolls the
// positional-vs-flag mutex and the at-least-one-source guard; the
// flag-vs-flag mutex among `-f`/`-b` stays cobra-side in NewRunCmd.
// Pulling the branches out of parseRunFlags keeps gocyclo within its
// budget for the larger validator.
func validateSourceMutex(flags runFlags) error {
	if flags.configName != "" {
		switch {
		case flags.file != "":
			return errors.New(
				"the <config> positional is mutually exclusive with -f/--file " +
					"(pick one source: <config>, -f, or -b)")
		case flags.blueprintName != "":
			return errors.New(
				"the <config> positional is mutually exclusive with -b/--blueprint " +
					"(pick one source: <config>, -f, or -b)")
		}
	}
	if flags.configName == "" && flags.file == "" && flags.blueprintName == "" {
		return errors.New(
			"at least one of <config> (positional), -f/--file, or -b/--blueprint is required")
	}
	return nil
}

func parseRunFlags(cmd *cobra.Command, args []string) (runFlags, error) {
	flags := runFlags{
		file:          strings.TrimSpace(viper.GetString(config.KUKE_RUN_FILE.ViperKey)),
		blueprintName: strings.TrimSpace(viper.GetString(config.KUKE_RUN_BLUEPRINT.ViperKey)),
		detach:        viper.GetBool(config.KUKE_RUN_DETACH.ViperKey),
		containerFlag: strings.TrimSpace(viper.GetString(config.KUKE_RUN_CONTAINER.ViperKey)),
		autoDelete:    viper.GetBool(config.KUKE_RUN_RM.ViperKey),
		nameOverride:  strings.TrimSpace(viper.GetString(config.KUKE_RUN_NAME.ViperKey)),
		newCell:       viper.GetBool(config.KUKE_RUN_NEW.ViperKey),
		clone:         viper.GetBool(config.KUKE_RUN_CLONE.ViperKey),
		reuse:         viper.GetBool(config.KUKE_RUN_REUSE.ViperKey),
		paramFile:     strings.TrimSpace(viper.GetString(config.KUKE_RUN_PARAM_FILE.ViperKey)),
		requireSynced: viper.GetBool(config.KUKE_RUN_REQUIRE_SYNCED.ViperKey),
	}

	if len(args) == 1 {
		flags.configName = strings.TrimSpace(args[0])
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
	if flags.file != "" {
		// --name, --param, --param-file are template knobs (per issue #355)
		// for the -b path. With -f the file's metadata.name is authoritative
		// and the substitution surface doesn't apply, so reject the
		// combination rather than silently dropping the flag.
		if flags.nameOverride != "" {
			return runFlags{}, errors.New("--name is only valid with -b/--blueprint")
		}
		if len(flags.paramArgs) > 0 {
			return runFlags{}, errors.New("--param is only valid with -b/--blueprint")
		}
		if flags.paramFile != "" {
			return runFlags{}, errors.New("--param-file is only valid with -b/--blueprint")
		}
		if flags.newCell {
			return runFlags{}, errors.New(
				"--new is only valid with the <config> positional; -f uses metadata.name verbatim")
		}
		if flags.clone {
			return runFlags{}, errors.New(
				"--clone is only valid with the <config> positional; -f does not fork a Config")
		}
		if flags.reuse {
			return runFlags{}, errors.New(
				"--reuse is only valid with the <config> positional; -f does not draw from a clone pool")
		}
	}
	// --new is a CellConfig-only knob (only the <config> positional reaches the
	// daemon-stored CellConfig path). With -b the default already generates
	// a fresh <prefix>-<6hex> per invocation, so accepting --new there would
	// silently no-op and seed the mental model that --new toggles a default
	// that isn't actually flipped — reject so the operator notices.
	if flags.newCell && flags.configName == "" && flags.file == "" {
		return runFlags{}, errors.New(
			"--new is only valid with the <config> positional; -b already materializes a " +
				"fresh <prefix>-<6hex> cell per invocation")
	}
	// --clone is a CellConfig-only knob; it forks a daemon-stored CellConfig
	// and only the <config> positional reaches that artifact. Reject with
	// -b for the same "no silent no-op" reason as --new.
	if flags.clone && flags.configName == "" && flags.file == "" {
		return runFlags{}, errors.New(
			"--clone is only valid with the <config> positional; -b has no Config to fork")
	}
	// --reuse is a CellConfig-only knob; it pulls from the clone pool of a
	// daemon-stored CellConfig and only the <config> positional reaches the
	// pool. Reject with -b for the same "no silent no-op" reason as --new.
	if flags.reuse && flags.configName == "" && flags.file == "" {
		return runFlags{}, errors.New(
			"--reuse is only valid with the <config> positional; -b has no clone pool to draw from")
	}
	if flags.configName != "" {
		// A CellConfig carries its own scalar values, so --param / --param-file
		// would silently shadow the Config's values; reject them rather than
		// silently apply.
		//
		// --name is accepted on the <config> path (#833 relaxed the old
		// MarkFlagsMutuallyExclusive("name", "generate-name") and broadened
		// --name's reach onto the CellConfig positional). The four reachable
		// shapes:
		//   - `<cfg>`: cell name = StableName(<cfg>); idempotent attach.
		//   - `<cfg> --name X`: cell name = X; idempotent attach (the AC's
		//     attach-if-exists escape valve referenced by the --new --name
		//     collision error).
		//   - `<cfg> --new`: cell name = <cfg>-<6hex>; create-or-fail (hex
		//     collisions are statistically negligible but surfaced).
		//   - `<cfg> --new --name X`: cell name = X; create-or-fail.
		if len(flags.paramArgs) > 0 {
			return runFlags{}, errors.New(
				"--param is not valid with the <config> positional; edit the Config's spec.values instead")
		}
		if flags.paramFile != "" {
			return runFlags{}, errors.New(
				"--param-file is not valid with the <config> positional; edit the Config's spec.values instead")
		}
	}
	return flags, nil
}

func runRun(cmd *cobra.Command, args []string) error {
	flags, err := parseRunFlags(cmd, args)
	if err != nil {
		return err
	}

	// Resolve the client first: the -b path resolves its blueprint from daemon
	// storage over this client, so it must be live before loadCellDoc runs. The
	// -f path ignores it during load.
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	loaded, err := loadCellDoc(cmd, client, flags)
	if err != nil {
		return err
	}
	cellDoc := loaded.Doc

	resolveCellLocation(&cellDoc)

	if flags.autoDelete {
		// The flag is the imperative knob; it always wins over a missing/false
		// spec.autoDelete in the manifest. We never clear an explicit `true`
		// in the YAML even without --rm — that's a declarative intent the
		// operator wrote and the daemon should honor.
		cellDoc.Spec.AutoDelete = true
	}

	// --env (#834): thread the CLI-supplied runtime env entries onto the
	// transport-only Spec.RuntimeEnv field. The v1beta1 type carries
	// yaml:"-" on this field so the daemon's writeDocWithGeneration never
	// persists it; the runner reads it at OCI build time and merges into
	// the attachable container's process env. Set after autoDelete so the
	// two CLI knobs sit together; the merge semantics (override-on-key,
	// last-wins across collisions) are owned by the runner-side mergeRuntimeEnv.
	if len(flags.envArgs) > 0 {
		cellDoc.Spec.RuntimeEnv = flags.envArgs
	}

	if validateErr := validateResolvedCell(cellDoc); validateErr != nil {
		return validateErr
	}

	if loaded.PreStarted {
		// --reuse claimed a healthy-Stopped clone via StartCell during load
		// (#835). The cell is already Ready and its containerd overlay
		// filesystem was preserved across the stop/start transition (project
		// repo clone, `.claude.json`, any per-cell state). Skip the
		// GetCell → existing-cell branch and emit the "started" rollup
		// directly, then attach (or detach per -d).
		return runAfterReuseClaim(cmd, client, cellDoc, loaded.StartResult, flags)
	}

	pre, getErr := client.GetCell(cmd.Context(), cellDoc)
	if flags.newCell {
		// --new is a strict "create new" intent: a live cell at the chosen
		// name is a fail rather than an attach. The two reachable shapes:
		//   - `--new --name X`: the operator pinned the name. The error
		//     directs them at `--name X` alone (idempotent attach) so the
		//     escape valve is explicit.
		//   - `--new` alone: the cell name is `<config>-<6hex>` from
		//     cellconfig.GenerateName. A collision here is statistically
		//     negligible (24 bits of entropy) but real — surface it with a
		//     rerun pointer rather than silently attaching to an unrelated
		//     spawn that happens to carry the same hex suffix.
		switch {
		case getErr == nil && pre.MetadataExists:
			if flags.nameOverride != "" {
				return fmt.Errorf(
					"cell %q already exists; --new --name requires the name to be free "+
						"(use `--name %s` alone for attach-if-exists semantics)",
					cellDoc.Metadata.Name, cellDoc.Metadata.Name,
				)
			}
			return fmt.Errorf(
				"cell %q already exists in realm %q (hex collision against a prior "+
					"`kuke run %s --new` spawn); rerun --new to allocate a fresh suffix",
				cellDoc.Metadata.Name, cellDoc.Spec.RealmID, flags.configName,
			)
		case getErr != nil && !errors.Is(getErr, errdefs.ErrCellNotFound):
			return getErr
		}
	} else {
		switch {
		case getErr == nil && pre.MetadataExists:
			if changed := divergedFields(pre.Cell.Spec, cellDoc.Spec); len(changed) > 0 {
				// Default (#986) is warn-and-attach: warnDivergentNamedCell
				// emits the `notice:` and returns nil so the flow falls
				// through into runExistingCell against the live spec. Under
				// --require-synced it instead returns the refusal error and
				// CreateCell/StartCell never fire.
				if warnErr := warnDivergentNamedCell(cmd.ErrOrStderr(), cellDoc, changed, flags); warnErr != nil {
					return warnErr
				}
			}
			// Either the on-disk spec matches the materialisation, or the
			// caller accepted the warn-and-attach default — drive the
			// create-or-attach state machine instead of re-entering CreateCell.
			return runExistingCell(cmd, client, cellDoc, pre, flags)
		case getErr != nil && !errors.Is(getErr, errdefs.ErrCellNotFound):
			return getErr
		}
	}

	// No live cell (ErrCellNotFound, or a nil read with no metadata): create
	// the cell and attach.
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

// runAfterReuseClaim drives the post-StartCell flow for the --reuse path
// (issue #835). The cell was already Stopped → Ready'd by
// pickAndStartReusableClone during load, so the standard runRun branches
// would either misclassify the cell (the Ready branch prints "already
// existed" — true of the metadata, but the operator's mental model is
// "I just told kuke to bring this clone back up") or fall through to
// CreateCell (an unsafe re-entry against a live cell).
//
// We re-read the cell here so the printer sees the post-start container
// statuses, and we emit the matching-spec + Started rollup that
// `<config>` Stopped → Started already uses. Auto-delete is rejected
// upstream (--reuse ↔ --rm mutex), so attach is the only follow-up.
func runAfterReuseClaim(
	cmd *cobra.Command,
	client kukeonv1.Client,
	cellDoc v1beta1.CellDoc,
	startRes kukeonv1.StartCellResult,
	flags runFlags,
) error {
	pre, getErr := client.GetCell(cmd.Context(), cellDoc)
	if getErr != nil {
		return getErr
	}
	if !pre.MetadataExists {
		// Shouldn't happen: we just StartCell'd it. Fail loudly rather
		// than silently fall through to a CreateCell on a phantom.
		return fmt.Errorf(
			"--reuse: cell %q vanished after StartCell — daemon state inconsistent",
			cellDoc.Metadata.Name,
		)
	}
	if printErr := printRunResult(cmd, startedResultFromGet(pre, startRes.Started), flags.output); printErr != nil {
		return printErr
	}
	if !flags.detach {
		return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
	}
	return nil
}

// warnDivergentNamedCell reacts to a named live cell whose spec diverges from
// the materialization the operator just asked us to run. Every `kuke run`
// source that pins a deterministic cell name (`-f`, `<config>` without `--new`,
// and `-b --name`) reaches this branch — `run` is otherwise a pure
// read-and-materialize verb, so divergence is the operator's signal to either
// reconcile (Config-lineage cells) or delete and re-run (Blueprint-lineage and
// bare `-f` cells).
//
// The default behaviour (#986) is warn-and-attach: print a one-line `notice:`
// naming the diverging fields and the per-source reconcile pointer, then
// return nil so runRun falls through to runExistingCell and attaches to the
// live state. `kuke run` is the operator's interactive escape hatch into a
// live cell and obstruction on drift defeats the workflow — the notice
// conveys the OutOfSync state plus the recovery path without blocking the
// immediate task. CI/scripted callers that need a hard fail opt in via
// --require-synced; the function then returns the pre-#986 refusal error
// shape (same per-source pointer the notice cites).
//
// The pointer per source: `<config>` → `kuke restart <name>` (#821's
// restart picks up the daemon-side OutOfSync detection #820 wires and
// reconciles implicitly; the cell name is the Config's StableName), `-b --name
// <cell>` → `kuke delete cell <cell>` + re-run (Blueprint-lineage cells have
// no implicit reconcile per #819's umbrella; the operator promotes to a
// CellConfig for restart-driven reconcile workflows), and the bare `-f`
// fallback keeps the `kuke apply -f` pointer because that path carries the
// spec on disk. The `--new` path never reaches this function: it takes a
// separate branch in runRun that treats any existing cell as a hard collision
// (no divergence reconcile pointer makes sense — `restart cell` would
// reconcile against the Config's stable name, not the `--new` cell's
// generated/pinned name).
func warnDivergentNamedCell(w io.Writer, cellDoc v1beta1.CellDoc, changed []string, flags runFlags) error {
	fields := strings.Join(changed, ", ")
	source, pointer := divergentSourcePointer(cellDoc, flags)
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

// divergentSourcePointer returns the source descriptor and the per-source
// recovery pointer for the divergence notice/error. Shared between the
// warn-and-attach default and the --require-synced refusal so both surface
// the same operator-facing wording per source path.
func divergentSourcePointer(cellDoc v1beta1.CellDoc, flags runFlags) (source, pointer string) {
	switch {
	case flags.configName != "" && !flags.newCell:
		return fmt.Sprintf("CellConfig %q", flags.configName),
			fmt.Sprintf("run `kuke restart %s` to reconcile (stops, updates, starts)",
				cellDoc.Metadata.Name)
	case flags.blueprintName != "" && flags.nameOverride != "":
		return fmt.Sprintf("CellBlueprint %q", flags.blueprintName),
			fmt.Sprintf("-b cells have no in-place reconcile; delete it with "+
				"`kuke delete cell %s` and re-run (or promote to a CellConfig for "+
				"`kuke restart` reconcile workflows)", cellDoc.Metadata.Name)
	default:
		return "on-disk spec",
			"use `kuke apply -f` to update"
	}
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

// loadResult bundles the resolved CellDoc with the bookkeeping a follow-up
// step needs to drive the post-load flow. PreStarted is true exactly when
// the load path itself called StartCell against the resolved cell — today
// that means --reuse claimed a healthy-Stopped clone (#835); on that path
// runRun skips its own GetCell → existing-cell branch and emits the
// "started" rollup directly. Every other source returns
// PreStarted=false; runRun walks the standard create-or-attach flow.
type loadResult struct {
	Doc         v1beta1.CellDoc
	PreStarted  bool
	StartResult kukeonv1.StartCellResult
}

// loadCellDoc dispatches to the file, blueprint, or config loader. Exactly
// one of flags.file / flags.blueprintName / flags.configName is non-empty
// (the cobra mutex enforces this before the handler runs); --name and --param*
// are template knobs already rejected against -f and the <config> positional
// in parseRunFlags. The blueprint and config paths resolve over client; the
// file path ignores it.
func loadCellDoc(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) (loadResult, error) {
	switch {
	case flags.configName != "":
		return loadFromConfig(cmd, client, flags)
	case flags.blueprintName != "":
		doc, err := loadFromBlueprint(cmd, client, flags)
		return loadResult{Doc: doc}, err
	default:
		doc, err := loadFromFile(flags.file)
		return loadResult{Doc: doc}, err
	}
}

// loadFromBlueprint resolves the named CellBlueprint from daemon storage at the
// scope named by --realm/--space/--stack, substitutes scalar --param values,
// and materializes a fresh `<prefix>-<6hex>` CellDoc (--name overrides the name
// verbatim). The blueprint's metadata scope drives the materialized cell's
// realm/space/stack; resolveCellLocation later fills any coordinate the
// blueprint left empty from the run flags / session defaults.
//
// The lookup scope takes realm from --realm (defaulted to "default") and
// space/stack only when the operator set them *explicitly* — via the flag or
// the KUKE_RUN_SPACE/STACK env var. The session default for those keys is
// "default" (DefineKV calls viper.SetDefault), so reading them through viper
// would wrongly narrow every lookup to default/default and hide realm-scoped
// blueprints; cmd/kuke/shared.ExplicitScope reads the raw flag/env instead.
// A blueprint that declares structural slots the inline path cannot fill
// (secret slots, required repo slots with no url) is refused by Materialize
// with a pointer to the CellConfig workflow.
func loadFromBlueprint(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) (v1beta1.CellDoc, error) {
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	lookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  flags.blueprintName,
			Realm: kukshared.PickLookupRealm(cmd, &config.KUKE_RUN_REALM),
			Space: kukshared.ExplicitScope(cmd, "space", &config.KUKE_RUN_SPACE),
			Stack: kukshared.ExplicitScope(cmd, "stack", &config.KUKE_RUN_STACK),
		},
	}

	res, err := client.GetBlueprint(cmd.Context(), lookup)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !res.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (blueprint %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, lookup.Metadata.Name,
			lookup.Metadata.Realm, lookup.Metadata.Space, lookup.Metadata.Stack,
		)
	}

	resolved, err := cellblueprint.Resolve(res.Blueprint, cliParams, os.LookupEnv)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	return cellblueprint.MaterializeWithName(resolved, flags.nameOverride)
}

// loadFromConfig resolves the named CellConfig from daemon storage, looks up
// the referenced Blueprint from daemon storage (which may live in a different
// realm — cross-scope references are explicitly allowed), and materializes the
// CellDoc via cellconfig.Materialize: scalar values from the Config's spec,
// structural slot fills (repo URLs, secret sources) applied, deterministic
// stable name + kukeon.io/config back-ref label set. The resulting CellDoc
// carries the Config's scope coordinates (not the blueprint's), so the cell
// is created where the Config is bound.
//
// The lookup walks progressively shallower scopes (full → space-only →
// realm-only) using pickLocation as the starting scope so the probe matches
// where resolveCellLocation would place the materialised cell. A Config
// stored at default/default/default would otherwise be invisible to a bare
// `kuke run <config>` lookup that used ExplicitScope (empty space/stack
// unless --space/--stack are set). Mirrors the daemon-side reconciler
// (internal/controller/reconcile_outofsync.go lookupLineageConfig), which
// is also re-used by controller.StartCell's OutOfSync reapply (#983), so
// all paths resolve the same Config regardless of which scope it was
// originally bound at.
func loadFromConfig(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) (loadResult, error) {
	realm := kukshared.PickLookupRealm(cmd, &config.KUKE_RUN_REALM)
	space := pickLocation("", &config.KUKE_RUN_SPACE)
	stack := pickLocation("", &config.KUKE_RUN_STACK)

	cfgRes, err := lookupConfigWithFallback(cmd.Context(), client, flags.configName, realm, space, stack)
	if err != nil {
		return loadResult{}, err
	}
	if !cfgRes.MetadataExists {
		return loadResult{}, fmt.Errorf(
			"%w (config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrConfigNotFound, flags.configName, realm, space, stack,
		)
	}

	preStarted := false
	var startResult kukeonv1.StartCellResult

	// --reuse: pick a healthy-Stopped clone of the source CellConfig and
	// claim it via StartCell (#835). On an empty pool, fall through to
	// --clone's allocation path below — the operator never sees a "pool
	// empty" error on the first tick or after a host reboot.
	if flags.reuse {
		clone, startRes, reuseErr := pickAndStartReusableClone(
			cmd.Context(), client, cfgRes.Config, flags.envArgs,
		)
		switch {
		case reuseErr == nil:
			cfgRes.Config = clone
			startResult = startRes
			preStarted = true
		case errors.Is(reuseErr, errReusePoolEmpty):
			// Empty pool: fork a new clone Config as --clone would. The
			// gap-fill counter loop in cloneCellConfig is itself atomic, so
			// concurrent --reuse → fallback invocations pick distinct N's.
			fresh, cloneErr := cloneCellConfig(cmd.Context(), client, cfgRes.Config, "")
			if cloneErr != nil {
				return loadResult{}, cloneErr
			}
			cfgRes.Config = fresh
		default:
			return loadResult{}, reuseErr
		}
	} else if flags.clone {
		// --clone: fork the source CellConfig into a new persistent clone and
		// drive the cell create from the clone's stable identity (#839). The
		// gap-fill counter (or explicit --name) lives in cloneCellConfig; the
		// cell-side flow downstream walks the standard stable-name path against
		// the clone's name, so re-running `kuke run <clone-name>` later is the
		// idempotent attach the AC's "first-class CellConfig" line guarantees.
		clone, cloneErr := cloneCellConfig(cmd.Context(), client, cfgRes.Config, flags.nameOverride)
		if cloneErr != nil {
			return loadResult{}, cloneErr
		}
		cfgRes.Config = clone
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
		return loadResult{}, err
	}
	if !bpRes.MetadataExists {
		return loadResult{}, fmt.Errorf(
			"%w (blueprint %q referenced by config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrBlueprintNotFound, bpRef.Name, cfgRes.Config.Metadata.Name,
			bpRef.Realm, bpRef.Space, bpRef.Stack,
		)
	}

	nameOverride := ""
	switch {
	case flags.nameOverride != "":
		// `<config> --name X` (with or without --new): cell name = X. The
		// `--new --name X` collision check lives in runRun (which knows about
		// the daemon's GetCell view); the bare `--name X` form falls through
		// to runRun's idempotent-attach path. Either way, threading the
		// pinned name through Materialize keeps the kukeon.io/config lineage
		// label on the cell so `kuke get cells -l kukeon.io/config=<name>`
		// still enumerates it.
		nameOverride = flags.nameOverride
	case flags.newCell:
		// `--new` alone: `<config-name>-<6hex>` — fresh cell per invocation,
		// kukeon.io/config label preserved by Materialize so operators can
		// still enumerate spawns with `kuke get cells -l
		// kukeon.io/config=<name>` (#833). The Config's idempotent-attach
		// contract from #742 still owns the bare `<config>` path.
		generated, genErr := cellconfig.GenerateName(cfgRes.Config.Metadata.Name)
		if genErr != nil {
			return loadResult{}, genErr
		}
		nameOverride = generated
	}
	doc, materializeErr := cellconfig.MaterializeWithName(cfgRes.Config, bpRes.Blueprint, nameOverride)
	if materializeErr != nil {
		return loadResult{}, materializeErr
	}
	return loadResult{Doc: doc, PreStarted: preStarted, StartResult: startResult}, nil
}

// lookupConfigWithFallback probes for the named Config from the operator's
// effective scope (realm/space/stack) down to space-only and then realm-only,
// returning the first hit. The starting scope comes from pickLocation (which
// falls back to viper / env / "default") so a `kuke run <config>` against a
// Config bound at default/default/default resolves without forcing the
// operator to repeat --space default --stack default. Probes are deduplicated
// when the space or stack is already empty (a realm-scoped lookup only takes
// the one probe its own scope names). A real RPC error short-circuits the
// walk so the operator sees the underlying failure instead of a misleading
// "not found" at realm scope. The last miss is returned when every probe
// reports MetadataExists=false so the caller can surface a single
// ErrConfigNotFound with the full-scope coordinates.
func lookupConfigWithFallback(
	ctx context.Context, client kukeonv1.Client,
	name, realm, space, stack string,
) (kukeonv1.GetConfigResult, error) {
	type probe struct{ space, stack string }
	probes := []probe{{space, stack}}
	if stack != "" {
		probes = append(probes, probe{space, ""})
	}
	if space != "" {
		probes = append(probes, probe{"", ""})
	}

	var lastMiss kukeonv1.GetConfigResult
	for _, p := range probes {
		res, err := client.GetConfig(ctx, v1beta1.CellConfigDoc{
			APIVersion: v1beta1.APIVersionV1Beta1,
			Kind:       v1beta1.KindCellConfig,
			Metadata: v1beta1.CellConfigMetadata{
				Name:  name,
				Realm: realm,
				Space: p.space,
				Stack: p.stack,
			},
		})
		if err != nil {
			return kukeonv1.GetConfigResult{}, err
		}
		if res.MetadataExists {
			return res, nil
		}
		lastMiss = res
	}
	return lastMiss, nil
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

// buildParamMap layers --param flags on top of --param-file contents.
// Empty result is a valid input for cellblueprint.Resolve (the blueprint may
// have all defaults). The function is split out so the run-time errors for
// malformed files / KEY=VALUE strings stay close to the call site.
func buildParamMap(flags runFlags) (map[string]string, error) {
	var fileParams map[string]string
	if flags.paramFile != "" {
		fp, err := cellblueprint.ParseParamFile(flags.paramFile)
		if err != nil {
			return nil, err
		}
		fileParams = fp
	}
	cliParams, err := cellblueprint.ParseParamArgs(flags.paramArgs)
	if err != nil {
		return nil, err
	}
	return cellblueprint.MergeParams(fileParams, cliParams), nil
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
// positive (e.g. issue #984 — `kuke run <config>` rejecting immediately after
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
