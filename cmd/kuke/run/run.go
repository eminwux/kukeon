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
		Use:   "run (-f <file> | -p <profile>)",
		Short: "Create and start a single cell from a YAML file or a profile",
		Long: "Create and start a single cell from a YAML file or stdin (-f), " +
			"or from a per-user profile under $HOME/.kuke/profiles.d/<name>.yaml (-p). " +
			"Conceptually `kuke apply -f` (single-cell) plus `kuke start cell`, but refuses " +
			"to update a divergent on-disk spec. To re-attach to an existing cell use " +
			"`kuke attach <cell>`.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRun,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin); mutually exclusive with -p")
	_ = viper.BindPFlag(config.KUKE_RUN_FILE.ViperKey, cmd.Flags().Lookup("file"))

	cmd.Flags().StringP("profile", "p", "",
		"Cell profile name to load from $HOME/.kuke/profiles.d (or $KUKE_PROFILES_DIR); mutually exclusive with -f")
	_ = viper.BindPFlag(config.KUKE_RUN_PROFILE.ViperKey, cmd.Flags().Lookup("profile"))

	cmd.Flags().String("name", "",
		"Override the materialized cell name (default: <metadata.name>-<6hex>). "+
			"Only valid with -p; rejected with -f, where metadata.name is the cell name verbatim.")
	_ = viper.BindPFlag(config.KUKE_RUN_NAME.ViperKey, cmd.Flags().Lookup("name"))

	cmd.Flags().StringArray("param", nil,
		"Profile parameter override as KEY=VALUE; repeatable. Only valid with -p. "+
			"Each KEY must be declared in spec.parameters[]. Wins over the parameter's "+
			"default and over --param-file when both set the same key.")

	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed profile parameters; one per line, "+
			"`#` starts a comment. Same declaration rules as --param. CLI --param wins on "+
			"duplicate keys.")
	_ = viper.BindPFlag(config.KUKE_RUN_PARAM_FILE.ViperKey, cmd.Flags().Lookup("param-file"))

	cmd.MarkFlagsMutuallyExclusive("file", "profile")
	cmd.MarkFlagsOneRequired("file", "profile")

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
			"`kuke attach`). Daemon-mode only — incompatible with "+
			"--no-daemon. Cleanup runs from kukeond's reconcile loop, "+
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
	if flags.autoDelete && viper.GetBool(config.KUKEON_ROOT_NO_DAEMON.ViperKey) {
		// --rm needs a long-lived process to watch the root task and trigger
		// cleanup. The --no-daemon CLI exits as soon as create+start returns,
		// so the watcher would never run.
		return runFlags{}, errors.New("--rm is incompatible with --no-daemon")
	}
	if flags.file != "" {
		// --name, --param, --param-file are profile-only knobs (per issue
		// #355). With -f the file's metadata.name is authoritative and the
		// substitution surface doesn't apply, so reject the combination
		// rather than silently dropping the flag.
		if flags.nameOverride != "" {
			return runFlags{}, errors.New("--name is only valid with -p/--profile")
		}
		if len(flags.paramArgs) > 0 {
			return runFlags{}, errors.New("--param is only valid with -p/--profile")
		}
		if flags.paramFile != "" {
			return runFlags{}, errors.New("--param-file is only valid with -p/--profile")
		}
	}
	return flags, nil
}

func runRun(cmd *cobra.Command, args []string) error {
	flags, err := parseRunFlags(cmd, args)
	if err != nil {
		return err
	}

	cellDoc, err := loadCellDoc(flags)
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

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

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
		// Matching spec + already-Ready cell: print a no-op summary without
		// re-entering the daemon's CreateCell→StartCell path. That path is
		// not safe to re-enter on a healthy cell today: runner.StartCell
		// re-attaches the root container to its CNI network, which the
		// bridge plugin rejects with `duplicate allocation`. Pre-existing
		// daemon bug, surfaced here because the AC requires idempotency.
		if pre.Cell.Status.State == v1beta1.CellStateReady {
			if printErr := printRunResult(cmd, noOpResultFromGet(pre), flags.output); printErr != nil {
				return printErr
			}
			if !flags.detach {
				return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
			}
			return nil
		}
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

// loadCellDoc dispatches to the file or profile loader. Exactly one of
// flags.file or flags.profileName is non-empty (the cobra mutex enforces
// this before the handler runs); --name and --param* are profile-only and
// already rejected against -f in parseRunFlags.
func loadCellDoc(flags runFlags) (v1beta1.CellDoc, error) {
	if flags.profileName != "" {
		return loadFromProfile(flags)
	}
	return loadFromFile(flags.file)
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

func pickLocation(fromDoc string, kv *config.Var) string {
	if v := strings.TrimSpace(fromDoc); v != "" {
		return v
	}
	if v := strings.TrimSpace(viper.GetString(kv.ViperKey)); v != "" {
		return v
	}
	return strings.TrimSpace(kv.ValueOrDefault())
}

// divergedFields names the structural fields where the on-disk cell disagrees
// with the file. We deliberately stop at structural identity (container count,
// container id set, root container id, cell location) and do NOT compare
// images/args/env: the runner normalizes container images on create
// (auto-default root rewrites e.g. user-supplied image to docker.io/library/
// busybox:latest), so a deep field-by-field diff would flag a freshly-created
// cell as divergent on the very next `kuke run -f` invocation. Per the AC, a
// genuine spec rewrite — adding/removing containers, swapping the root, moving
// realm/space/stack — must route through `kuke apply -f` instead.
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
	if len(actual.Containers) != len(desired.Containers) {
		diffs = append(diffs, fmt.Sprintf(
			"spec.containers (count: actual=%d, desired=%d)",
			len(actual.Containers), len(desired.Containers),
		))
		return diffs
	}

	desiredByID := make(map[string]v1beta1.ContainerSpec, len(desired.Containers))
	for _, c := range desired.Containers {
		desiredByID[c.ID] = c
	}
	for _, ac := range actual.Containers {
		dc, ok := desiredByID[ac.ID]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("spec.containers[%q] (missing in file)", ac.ID))
			continue
		}
		if ac.Root != dc.Root {
			diffs = append(diffs, fmt.Sprintf("spec.containers[%q].root", ac.ID))
		}
	}
	return diffs
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.ClientFromCmd(cmd)
}
