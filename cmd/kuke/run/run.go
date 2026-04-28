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

// Package run implements the `kuke run -f <file>` verb: parse a single-cell
// YAML doc, idempotently create-and-start the cell, and report the same
// outcome other lifecycle verbs print. Phase 2a adds -a/--attach so the
// same invocation can drop the operator into the cell's attachable
// terminal; -p (profile) lands in phase 2b.
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
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewRunCmd builds the `kuke run` cobra command. Phase 1c implemented
// `-f/--file`; phase 2a adds `-a/--attach` and `--container`; `-p/--profile`
// (#D) extends this scaffold.
func NewRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "run -f <file>",
		Short:         "Create and start a single cell from a YAML file",
		Long:          "Create and start a single cell from a YAML file or stdin. Conceptually `kuke apply -f` (single-cell) plus `kuke start cell`, but refuses to update a divergent on-disk spec.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runRun,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin)")
	_ = viper.BindPFlag(config.KUKE_RUN_FILE.ViperKey, cmd.Flags().Lookup("file"))
	_ = cmd.MarkFlagRequired("file")

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

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runRun(cmd *cobra.Command, _ []string) error {
	file, err := cmd.Flags().GetString("file")
	if err != nil {
		return err
	}
	if strings.TrimSpace(file) == "" {
		return errors.New("file flag is required (use -f <file> or -f - for stdin)")
	}

	output, err := cmd.Flags().GetString("output")
	if err != nil {
		return err
	}
	output = strings.TrimSpace(output)
	if output != "" && output != "json" && output != "yaml" {
		return fmt.Errorf("invalid --output %q: want json or yaml", output)
	}

	doAttach := viper.GetBool(config.KUKE_RUN_ATTACH.ViperKey)
	containerFlag := strings.TrimSpace(viper.GetString(config.KUKE_RUN_CONTAINER.ViperKey))
	if !doAttach && containerFlag != "" {
		return errors.New("--container is only valid together with -a/--attach")
	}
	if doAttach && output != "" {
		// -a hands the terminal to sbsh; mixing structured -o output with an
		// interactive attach loop produces garbled output that nothing can
		// parse. Reject the combination so callers pick one mode.
		return errors.New("--output is incompatible with -a/--attach")
	}

	reader, cleanup, err := kukshared.ReadFileOrStdin(file)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	rawYAML, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	cellDoc, err := parseSingleCellDoc(rawYAML)
	if err != nil {
		return err
	}

	resolveCellLocation(&cellDoc)

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
			if printErr := printRunResult(cmd, noOpResultFromGet(pre), output); printErr != nil {
				return printErr
			}
			if doAttach {
				return attachAfterRun(cmd, client, cellDoc, containerFlag)
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

	if printErr := printRunResult(cmd, result, output); printErr != nil {
		return printErr
	}
	if doAttach {
		return attachAfterRun(cmd, client, cellDoc, containerFlag)
	}
	return nil
}

// attachAfterRun resolves the post-start attach target per the documented
// precedence and drives the in-process sbsh attach loop. The selection runs
// against cellDoc.Spec rather than re-querying the daemon: by this point the
// resolved doc is what we just told the daemon to create, so the attachable
// set the user authored is authoritative.
func attachAfterRun(
	cmd *cobra.Command,
	client kukeonv1.Client,
	doc v1beta1.CellDoc,
	containerFlag string,
) error {
	target, err := pickAttachTarget(doc.Spec, doc.Metadata.Name, containerFlag)
	if err != nil {
		return err
	}
	return runAttachLoop(cmd, client, doc, target)
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
