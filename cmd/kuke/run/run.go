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

// Package run implements the `kuke run` verb. The original `-f <file>` form
// (phase 1c) parses a single-cell YAML doc and idempotently create-and-starts
// the cell. Phase 2a added `-a/--attach` so the same invocation can drop the
// operator into the cell's attachable terminal. Phase 2b adds `-p/--profile`,
// which materializes a CellDoc from a per-user CellProfile under
// $HOME/.kuke/profiles.d (or $KUKE_PROFILES_DIR) and then walks the same
// create+start[+attach] path.
package run

import (
	"bytes"
	"errors"
	"fmt"
	"io"
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
// CellDoc shape. Exactly one of the two is required. `-a` then drops the
// operator into the cell's attachable terminal once the cell is up.
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

	cmd.Flags().BoolP("attach", "a", false,
		"After start, attach to the cell's attachable container (precedence: --container > cell.tty.default > first attachable)")
	_ = viper.BindPFlag(config.KUKE_RUN_ATTACH.ViperKey, cmd.Flags().Lookup("attach"))
	cmd.Flags().String("container", "",
		"Container to attach to (only valid with -a; must be attachable)")
	_ = viper.BindPFlag(config.KUKE_RUN_CONTAINER.ViperKey, cmd.Flags().Lookup("container"))
	cmd.Flags().Bool("rm", false,
		"Best-effort delete the cell after it is no longer needed "+
			"(any rc). Without -a, the trigger is the root container's "+
			"task exit. With -a, the trigger is the attach loop exiting "+
			"because the workload terminated, the peer hung up, or an "+
			"unrecoverable controller error fired — the CLI then sends "+
			"KillCell so a long-lived root (e.g. `sleep infinity`) does "+
			"not pin the cell. A clean ^]^] detach is NOT a trigger: the "+
			"cell stays alive so the operator can re-attach later "+
			"(parity with `kuke attach`). Daemon-mode only — incompatible "+
			"with --no-daemon. Cleanup runs from kukeond's reconcile "+
			"loop, so latency is bounded by the reconcile interval "+
			"rather than firing the instant the trigger fires.")
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
	doAttach      bool
	containerFlag string
	autoDelete    bool
}

func parseRunFlags(cmd *cobra.Command, _ []string) (runFlags, error) {
	flags := runFlags{
		file:          strings.TrimSpace(viper.GetString(config.KUKE_RUN_FILE.ViperKey)),
		profileName:   strings.TrimSpace(viper.GetString(config.KUKE_RUN_PROFILE.ViperKey)),
		doAttach:      viper.GetBool(config.KUKE_RUN_ATTACH.ViperKey),
		containerFlag: strings.TrimSpace(viper.GetString(config.KUKE_RUN_CONTAINER.ViperKey)),
		autoDelete:    viper.GetBool(config.KUKE_RUN_RM.ViperKey),
	}

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return runFlags{}, err
	}
	flags.output = strings.TrimSpace(output)
	if flags.output != "" && flags.output != "json" && flags.output != "yaml" {
		return runFlags{}, fmt.Errorf("invalid --output %q: want json or yaml", flags.output)
	}

	if !flags.doAttach && flags.containerFlag != "" {
		return runFlags{}, errors.New("--container is only valid together with -a/--attach")
	}
	if flags.doAttach && flags.output != "" {
		// -a hands the terminal to sbsh; mixing structured -o output with an
		// interactive attach loop produces garbled output that nothing can
		// parse. Reject the combination so callers pick one mode.
		return runFlags{}, errors.New("--output is incompatible with -a/--attach")
	}
	if flags.autoDelete && viper.GetBool(config.KUKEON_ROOT_NO_DAEMON.ViperKey) {
		// --rm needs a long-lived process to watch the root task and trigger
		// cleanup. The --no-daemon CLI exits as soon as create+start returns,
		// so the watcher would never run.
		return runFlags{}, errors.New("--rm is incompatible with --no-daemon")
	}
	return flags, nil
}

func runRun(cmd *cobra.Command, args []string) error {
	flags, err := parseRunFlags(cmd, args)
	if err != nil {
		return err
	}

	cellDoc, err := loadCellDoc(flags.file, flags.profileName)
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
			if flags.doAttach {
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
	if flags.doAttach {
		return attachAndMaybeAutoDelete(cmd, client, cellDoc, flags)
	}
	return nil
}

// attachAndMaybeAutoDelete drives the attach loop and, under -a --rm,
// fires KillCell once the loop returns — but only if the loop exited
// because the workload ended (peer hangup, shell exit, controller
// error). A clean ^]^] detach (issue #279) leaves the cell alive so the
// operator can re-attach later, matching `kuke attach`'s exit-0
// semantics.
//
// Both the create-and-start path and the already-Ready idempotent
// short-circuit funnel through here so re-running `kuke run -a --rm`
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

// autoDeleteAfterAttach drives the -a --rm cleanup path. KillCell is
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

// loadCellDoc dispatches to the file or profile loader. Exactly one of file or
// profileName is non-empty (the cobra mutex enforces it before the handler
// runs).
func loadCellDoc(file, profileName string) (v1beta1.CellDoc, error) {
	if profileName != "" {
		return loadFromProfile(profileName)
	}
	return loadFromFile(file)
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
// profile, and materializes its CellSpec body into a CellDoc. The cell name
// is `<prefix>-<6hex>` — prefix defaults to metadata.name and is overridden
// by spec.prefix — so every invocation produces a fresh cell.
func loadFromProfile(profileName string) (v1beta1.CellDoc, error) {
	dir, err := cellprofile.ResolveDir()
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	profile, err := cellprofile.Load(dir, profileName)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	return cellprofile.Materialize(profile)
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
