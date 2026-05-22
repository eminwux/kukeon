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
// single-cell YAML doc and idempotently create-and-starts the cell; the
// `-p/--profile` form materializes a CellDoc from a per-user CellProfile under
// $HOME/.kuke/profiles.d (or $KUKE_PROFILES_DIR) and walks the same path.
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
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewRunCmd builds the `kuke run` cobra command. `-f` reads a single-cell
// YAML doc; `-p` reads a per-user CellProfile and materializes the same
// CellDoc shape. Exactly one of the two is required. By default, after the
// cell starts the CLI drops the operator into the cell's attachable terminal;
// `-d/--detach` opts out of the post-start attach.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run (-f <file> | -p <profile> | -b <blueprint>)",
		Short: "Create and start a single cell from a YAML file, a profile, or a daemon-stored blueprint",
		Long: "Create and start a single cell from a YAML file or stdin (-f), " +
			"from a per-user profile under $HOME/.kuke/profiles.d/<name>.yaml (-p), " +
			"or from a daemon-stored CellBlueprint (-b), resolved from the scope named by " +
			"--realm/--space/--stack. -b substitutes scalar --param values and materializes " +
			"a fresh <prefix>-<6hex> cell every invocation; structural slot fill (repo URLs, " +
			"secret sources) requires a CellConfig and `kuke run -c` (#625). " +
			"-f is create-or-attach by metadata.name: a missing cell is created and " +
			"attached; a Ready cell is attached as a no-op; a Stopped cell is started " +
			"then attached; a divergent on-disk spec is refused (use `kuke apply -f` to " +
			"update); a cell in an error or partial state is refused with a " +
			"`kuke delete cell <name>` pointer. To re-attach to an existing cell use " +
			"`kuke attach <cell>`.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRun,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin); mutually exclusive with -p")
	_ = viper.BindPFlag(config.KUKE_RUN_FILE.ViperKey, cmd.Flags().Lookup("file"))

	cmd.Flags().StringP("profile", "p", "",
		"Cell profile name to load from $HOME/.kuke/profiles.d (or $KUKE_PROFILES_DIR); mutually exclusive with -f/-b. "+
			"Deprecated: apply a kind: CellBlueprint and use -b/-c instead.")
	_ = viper.BindPFlag(config.KUKE_RUN_PROFILE.ViperKey, cmd.Flags().Lookup("profile"))

	cmd.Flags().StringP("blueprint", "b", "",
		"Daemon-stored CellBlueprint name to run, resolved from the scope named by "+
			"--realm/--space/--stack; mutually exclusive with -f/-p. Substitutes scalar --param "+
			"values and materializes a fresh <prefix>-<6hex> cell.")
	_ = viper.BindPFlag(config.KUKE_RUN_BLUEPRINT.ViperKey, cmd.Flags().Lookup("blueprint"))

	cmd.Flags().String("name", "",
		"Override the materialized cell name (default: <metadata.name>-<6hex>). "+
			"Valid with -p/-b; rejected with -f, where metadata.name is the cell name verbatim.")
	_ = viper.BindPFlag(config.KUKE_RUN_NAME.ViperKey, cmd.Flags().Lookup("name"))

	cmd.Flags().StringArray("param", nil,
		"Scalar parameter override as KEY=VALUE; repeatable. Valid with -p/-b. "+
			"Each KEY must be declared in spec.parameters[]. Wins over the parameter's "+
			"default and over --param-file when both set the same key.")

	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed scalar parameters; one per line, "+
			"`#` starts a comment. Same declaration rules as --param. CLI --param wins on "+
			"duplicate keys.")
	_ = viper.BindPFlag(config.KUKE_RUN_PARAM_FILE.ViperKey, cmd.Flags().Lookup("param-file"))

	cmd.MarkFlagsMutuallyExclusive("file", "profile", "blueprint")
	cmd.MarkFlagsOneRequired("file", "profile", "blueprint")

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

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("profile", config.CompleteProfileNames)

	return cmd
}

// runFlags is the validated bundle of flag values runRun consumes after
// argument validation. Splitting it out keeps runRun under the funlen limit
// while preserving the original control flow.
type runFlags struct {
	file          string
	profileName   string
	blueprintName string
	output        string
	detach        bool
	containerFlag string
	autoDelete    bool
	nameOverride  string
	paramArgs     []string
	paramFile     string
}

func parseRunFlags(cmd *cobra.Command, _ []string) (runFlags, error) {
	flags := runFlags{
		file:          strings.TrimSpace(viper.GetString(config.KUKE_RUN_FILE.ViperKey)),
		profileName:   strings.TrimSpace(viper.GetString(config.KUKE_RUN_PROFILE.ViperKey)),
		blueprintName: strings.TrimSpace(viper.GetString(config.KUKE_RUN_BLUEPRINT.ViperKey)),
		detach:        viper.GetBool(config.KUKE_RUN_DETACH.ViperKey),
		containerFlag: strings.TrimSpace(viper.GetString(config.KUKE_RUN_CONTAINER.ViperKey)),
		autoDelete:    viper.GetBool(config.KUKE_RUN_RM.ViperKey),
		nameOverride:  strings.TrimSpace(viper.GetString(config.KUKE_RUN_NAME.ViperKey)),
		paramFile:     strings.TrimSpace(viper.GetString(config.KUKE_RUN_PARAM_FILE.ViperKey)),
	}

	paramArgs, err := cmd.Flags().GetStringArray("param")
	if err != nil {
		return runFlags{}, err
	}
	flags.paramArgs = paramArgs

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
		// shared by the -p and -b paths. With -f the file's metadata.name is
		// authoritative and the substitution surface doesn't apply, so reject
		// the combination rather than silently dropping the flag.
		if flags.nameOverride != "" {
			return runFlags{}, errors.New("--name is only valid with -p/--profile or -b/--blueprint")
		}
		if len(flags.paramArgs) > 0 {
			return runFlags{}, errors.New("--param is only valid with -p/--profile or -b/--blueprint")
		}
		if flags.paramFile != "" {
			return runFlags{}, errors.New("--param-file is only valid with -p/--profile or -b/--blueprint")
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
	// -f/-p paths ignore it during load.
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	cellDoc, err := loadCellDoc(cmd, client, flags)
	if err != nil {
		return err
	}

	resolveCellLocation(&cellDoc)

	if flags.autoDelete {
		// The flag is the imperative knob; it always wins over a missing/false
		// spec.autoDelete in the manifest. We never clear an explicit `true`
		// in the YAML even without --rm — that's a declarative intent the
		// operator wrote and the daemon should honor.
		cellDoc.Spec.AutoDelete = true
	}

	if validateErr := validateResolvedCell(cellDoc); validateErr != nil {
		return validateErr
	}

	pre, getErr := client.GetCell(cmd.Context(), cellDoc)
	switch {
	case getErr == nil && pre.MetadataExists:
		if changed := divergedFields(pre.Cell.Spec, cellDoc.Spec); len(changed) > 0 {
			return fmt.Errorf(
				"cell %q exists with diverging spec (%s); use `kuke apply -f` to update",
				cellDoc.Metadata.Name,
				strings.Join(changed, ", "),
			)
		}
		// A live cell whose on-disk spec matches the file: drive the
		// create-or-attach state machine instead of re-entering CreateCell.
		return runExistingCell(cmd, client, cellDoc, pre, flags)
	case getErr != nil && !errors.Is(getErr, errdefs.ErrCellNotFound):
		return getErr
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

// runExistingCell drives the create-or-attach state machine for a live cell
// whose on-disk spec already matches the file (the caller rejected divergence
// before delegating here). The four terminal states:
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

// loadCellDoc dispatches to the file, profile, or blueprint loader. Exactly
// one of flags.file / flags.profileName / flags.blueprintName is non-empty (the
// cobra mutex enforces this before the handler runs); --name and --param* are
// template knobs already rejected against -f in parseRunFlags. The blueprint
// path resolves over client; the file/profile paths ignore it.
func loadCellDoc(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) (v1beta1.CellDoc, error) {
	switch {
	case flags.blueprintName != "":
		return loadFromBlueprint(cmd, client, flags)
	case flags.profileName != "":
		fmt.Fprintln(cmd.ErrOrStderr(),
			"kuke run: -p/--profile is deprecated and will be removed (#626); "+
				"apply a kind: CellBlueprint and use `kuke run -b` (or `kuke run -c` for a CellConfig, #625).")
		return loadFromProfile(flags)
	default:
		return loadFromFile(flags.file)
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
// blueprints; explicitScope reads the raw flag/env instead. A blueprint that
// declares structural slots the inline path cannot fill (secret slots, required
// repo slots with no url) is refused by Materialize with a pointer to
// `kuke run -c`.
func loadFromBlueprint(cmd *cobra.Command, client kukeonv1.Client, flags runFlags) (v1beta1.CellDoc, error) {
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	lookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  flags.blueprintName,
			Realm: pickLocation("", &config.KUKE_RUN_REALM),
			Space: explicitScope(cmd, "space", &config.KUKE_RUN_SPACE),
			Stack: explicitScope(cmd, "stack", &config.KUKE_RUN_STACK),
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

// loadFromProfile resolves the active profiles directory, loads the named
// profile with `${KEY}` references substituted, and materializes its CellSpec
// body into a CellDoc. By default the cell name is `<prefix>-<6hex>` (prefix
// = spec.prefix or metadata.name); --name overrides it verbatim so callers
// like the orchestrator can pin a deterministic name.
//
// Parameter resolution order matches the spec for issue #355:
//  1. --param-file lines (lowest)
//  2. --param flags (override the file on duplicate keys)
//  3. spec.parameters[].default
//  4. os.Getenv(KEY) — drawn from kuke's process env
//  5. error if the parameter is required and still unset
//
// We thread os.LookupEnv as the env source because today profiles are
// loaded by the kuke CLI (a one-shot process), so kuke's env IS the
// substitution surface the spec calls "the kukeond process, not the
// caller". A future move to daemon-side profile loading would swap this
// for the daemon's env without changing the resolution order.
func loadFromProfile(flags runFlags) (v1beta1.CellDoc, error) {
	dir, err := cellprofile.ResolveDir()
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	profile, err := cellprofile.LoadResolved(dir, flags.profileName, cliParams, os.LookupEnv)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	return cellprofile.MaterializeWithName(profile, flags.nameOverride)
}

// buildParamMap layers --param flags on top of --param-file contents.
// Empty result is a valid input for LoadResolved (the profile may have all
// defaults). The function is split out so the run-time errors for malformed
// files / KEY=VALUE strings stay close to the call site.
func buildParamMap(flags runFlags) (map[string]string, error) {
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

// explicitScope returns a blueprint-lookup scope coordinate only when the
// operator set it explicitly — the cobra flag was changed on this invocation,
// or the backing env var (kv.Key) is present in the environment. It deliberately
// bypasses viper.GetString: DefineKV registers a viper default of "default" for
// the run space/stack keys, so viper would report "default" for an unset
// coordinate and narrow every lookup to default/default, hiding realm-scoped
// blueprints. An empty return means "don't constrain this coordinate".
func explicitScope(cmd *cobra.Command, flagName string, kv *config.Var) string {
	if cmd.Flags().Changed(flagName) {
		v, _ := cmd.Flags().GetString(flagName)
		return strings.TrimSpace(v)
	}
	if v, ok := os.LookupEnv(kv.Key); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

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
		diffs = append(diffs, "spec.realmId")
	}
	if actual.SpaceID != "" && actual.SpaceID != desired.SpaceID {
		diffs = append(diffs, "spec.spaceId")
	}
	if actual.StackID != "" && actual.StackID != desired.StackID {
		diffs = append(diffs, "spec.stackId")
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
			diffs = append(diffs, fmt.Sprintf("spec.containers[%q].root", ac.ID))
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
		fields = append(fields, "image")
	}
	if actual.Command != desired.Command {
		fields = append(fields, "command")
	}
	if !stringSlicesEqual(actual.Args, desired.Args) {
		fields = append(fields, "args")
	}
	if actual.WorkingDir != desired.WorkingDir {
		fields = append(fields, "workingDir")
	}
	if !stringSlicesEqual(actual.Env, desired.Env) {
		fields = append(fields, "env")
	}
	if !stringSlicesEqual(actual.Ports, desired.Ports) {
		fields = append(fields, "ports")
	}
	if !volumeMountsEqual(actual.Volumes, desired.Volumes) {
		fields = append(fields, "volumes")
	}
	if !stringSlicesEqual(actual.Networks, desired.Networks) {
		fields = append(fields, "networks")
	}
	if !stringSlicesEqual(actual.NetworksAliases, desired.NetworksAliases) {
		fields = append(fields, "networksAliases")
	}
	if actual.Privileged != desired.Privileged {
		fields = append(fields, "privileged")
	}
	if actual.HostNetwork != desired.HostNetwork {
		fields = append(fields, "hostNetwork")
	}
	if actual.HostPID != desired.HostPID {
		fields = append(fields, "hostPID")
	}
	if actual.HostCgroup != desired.HostCgroup {
		fields = append(fields, "hostCgroup")
	}
	if actual.Attachable != desired.Attachable {
		fields = append(fields, "attachable")
	}
	if actual.RestartPolicy != desired.RestartPolicy {
		fields = append(fields, "restartPolicy")
	}
	if !containerSecretsEqual(actual.Secrets, desired.Secrets) {
		fields = append(fields, "secrets")
	}
	if !containerTtysEqual(actual.Tty, desired.Tty) {
		fields = append(fields, "tty")
	}
	return fields
}

// containerSecretsEqual compares two ContainerSecret slices field-by-field.
// nil and empty are treated as equal so YAML that omits secrets does not
// register as drift against on-disk metadata that persisted it as nil.
// ContainerSecret carries only scalar string fields, so a direct == on each
// element is enough.
func containerSecretsEqual(a, b []v1beta1.ContainerSecret) bool {
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
