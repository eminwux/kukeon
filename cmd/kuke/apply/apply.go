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

package apply

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/cellblueprint"
	"github.com/eminwux/kukeon/internal/cellconfig"
	"github.com/eminwux/kukeon/internal/cellprofile"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

const (
	outputFormatJSON = "json"
	outputFormatYAML = "yaml"
)

// NewApplyCmd builds the `kuke apply` cobra command. `-f` reads a multi-document
// YAML stream from disk or stdin (unchanged). `-b` resolves a daemon-stored
// CellBlueprint by name + scope, substitutes `--param` values, and reconciles
// the resulting cell against the live state. `-c` resolves a daemon-stored
// CellConfig by name + scope and reconciles its materialised cell. The three
// sources are mutually exclusive; exactly one is required.
func NewApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply (-f <file> | -b <blueprint> | -c <config>)",
		Short: "Apply resource definitions from YAML or reconcile a daemon-stored Blueprint/Config",
		Long: "Apply resource definitions from a YAML file or stdin (-f), reconcile a " +
			"daemon-stored CellBlueprint by ref (-b), or reconcile a daemon-stored " +
			"CellConfig by ref (-c). The -b/-c forms materialise a CellDoc the same way " +
			"`kuke run -b/-c` does and reconcile it against the live cell: a missing cell " +
			"is created and started; an identical live cell is a no-op; a divergent live " +
			"cell is stopped, updated, and started (no attach). A live cell whose " +
			"lineage label (kukeon.io/blueprint or kukeon.io/config) does not match the " +
			"-b/-c source is refused, so apply never silently takes over an unrelated cell.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runApply,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin); mutually exclusive with -b/-c")
	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")

	cmd.Flags().StringP("blueprint", "b", "",
		"Daemon-stored CellBlueprint name to reconcile, resolved from the scope named by "+
			"--realm/--space/--stack; mutually exclusive with -f/-c. Substitutes scalar "+
			"--param values; without --name, materialises a fresh <prefix>-<6hex> cell name.")
	cmd.Flags().StringP("config", "c", "",
		"Daemon-stored CellConfig name to reconcile, resolved from the scope named by "+
			"--realm/--space/--stack; mutually exclusive with -f/-b. The cell name is the "+
			"Config's deterministic stable name unless --name overrides it. Rejects --param "+
			"/ --param-file (edit the Config's spec.values instead).")

	cmd.Flags().String("name", "",
		"Override the materialised cell name (default for -b: <prefix>-<6hex>; default for "+
			"-c: the Config's stable name). Rejected with -f.")
	cmd.Flags().StringArray("param", nil,
		"Scalar parameter override as KEY=VALUE; repeatable. Valid with -b only. Each KEY "+
			"must be declared in spec.parameters[]. Wins over the parameter's default and "+
			"over --param-file when both set the same key.")
	cmd.Flags().String("param-file", "",
		"File of KEY=VALUE lines whose values seed scalar parameters; one per line, `#` "+
			"starts a comment. Valid with -b only. CLI --param wins on duplicate keys.")

	cmd.Flags().String("realm", "", "Realm to resolve the Blueprint/Config from (default: default)")
	cmd.Flags().String("space", "", "Space to resolve the Blueprint/Config from")
	cmd.Flags().String("stack", "", "Stack to resolve the Blueprint/Config from")

	cmd.MarkFlagsMutuallyExclusive("file", "blueprint", "config")
	cmd.MarkFlagsOneRequired("file", "blueprint", "config")

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	_ = cmd.RegisterFlagCompletionFunc("blueprint", config.CompleteBlueprintNames)
	_ = cmd.RegisterFlagCompletionFunc("config", config.CompleteConfigNames)

	return cmd
}

// applyFlags is the validated bundle of flag values runApply consumes.
type applyFlags struct {
	file          string
	blueprintName string
	configName    string
	output        string
	nameOverride  string
	paramArgs     []string
	paramFile     string
}

func parseApplyFlags(cmd *cobra.Command) (applyFlags, error) {
	flags := applyFlags{}
	var err error
	if flags.file, err = cmd.Flags().GetString("file"); err != nil {
		return flags, err
	}
	if flags.blueprintName, err = cmd.Flags().GetString("blueprint"); err != nil {
		return flags, err
	}
	if flags.configName, err = cmd.Flags().GetString("config"); err != nil {
		return flags, err
	}
	if flags.output, err = cmd.Flags().GetString("output"); err != nil {
		return flags, err
	}
	if flags.nameOverride, err = cmd.Flags().GetString("name"); err != nil {
		return flags, err
	}
	if flags.paramArgs, err = cmd.Flags().GetStringArray("param"); err != nil {
		return flags, err
	}
	if flags.paramFile, err = cmd.Flags().GetString("param-file"); err != nil {
		return flags, err
	}

	flags.file = strings.TrimSpace(flags.file)
	flags.blueprintName = strings.TrimSpace(flags.blueprintName)
	flags.configName = strings.TrimSpace(flags.configName)
	flags.output = strings.TrimSpace(flags.output)
	flags.nameOverride = strings.TrimSpace(flags.nameOverride)
	flags.paramFile = strings.TrimSpace(flags.paramFile)

	if flags.output != "" && flags.output != outputFormatJSON && flags.output != outputFormatYAML {
		return flags, fmt.Errorf("invalid --output %q: want json or yaml", flags.output)
	}

	if flags.file != "" {
		// --name and --param* are template knobs that only make sense for the
		// -b/-c refs; a -f stream carries metadata.name verbatim and a fully
		// resolved spec.
		if flags.nameOverride != "" {
			return flags, errors.New("--name is only valid with -b/--blueprint or -c/--config")
		}
		if len(flags.paramArgs) > 0 {
			return flags, errors.New("--param is only valid with -b/--blueprint")
		}
		if flags.paramFile != "" {
			return flags, errors.New("--param-file is only valid with -b/--blueprint")
		}
	}
	if flags.configName != "" {
		// A CellConfig carries its own spec.values; --param would either
		// silently shadow them or break the Config's idempotent identity.
		if len(flags.paramArgs) > 0 {
			return flags, errors.New(
				"--param is not valid with -c/--config; edit the Config's spec.values instead")
		}
		if flags.paramFile != "" {
			return flags, errors.New(
				"--param-file is not valid with -c/--config; edit the Config's spec.values instead")
		}
	}
	return flags, nil
}

func runApply(cmd *cobra.Command, _ []string) error {
	flags, err := parseApplyFlags(cmd)
	if err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if flags.blueprintName != "" || flags.configName != "" {
		return runApplyRef(cmd, client, flags)
	}
	return runApplyFile(cmd, client, flags)
}

// runApplyFile is the unchanged `kuke apply -f` path: read YAML, send to the
// daemon's ApplyDocuments, print result.
func runApplyFile(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) error {
	if flags.file == "" {
		return errors.New("file flag is required (use -f <file> or -f - for stdin)")
	}

	reader, cleanup, err := kukshared.ReadFileOrStdin(flags.file)
	if err != nil {
		return err
	}
	defer func() { _ = cleanup() }()

	rawYAML, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read input: %w", err)
	}

	result, err := client.ApplyDocuments(cmd.Context(), rawYAML)
	if err != nil {
		return err
	}

	if flags.output == outputFormatJSON || flags.output == outputFormatYAML {
		return printApplyResultJSON(cmd, result, flags.output)
	}
	return printApplyResult(cmd, result)
}

// runApplyRef handles the `-b`/`-c` form. It materialises a CellDoc from the
// named Blueprint or Config, validates the lineage against any live cell at
// the resolved name, and routes the marshaled doc through the daemon's
// ApplyDocuments — which owns the missing/matches/differs reconciliation for
// the Cell kind. Equivalent specs from -f, -b, or -c all converge on the same
// daemon-side reconcile path.
func runApplyRef(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) error {
	cellDoc, err := loadCellRef(cmd, client, flags)
	if err != nil {
		return err
	}
	resolveCellLocation(cmd, &cellDoc)

	if lineageErr := assertCellLineage(cmd.Context(), client, cellDoc, flags); lineageErr != nil {
		return lineageErr
	}

	rawYAML, err := yaml.Marshal(&cellDoc)
	if err != nil {
		return fmt.Errorf("marshal materialised cell: %w", err)
	}

	result, err := client.ApplyDocuments(cmd.Context(), rawYAML)
	if err != nil {
		return err
	}

	if flags.output == outputFormatJSON || flags.output == outputFormatYAML {
		return printApplyResultJSON(cmd, result, flags.output)
	}
	return printApplyResult(cmd, result)
}

// loadCellRef materialises the CellDoc for the -b or -c path. Mirrors
// run.loadFromBlueprint / loadFromConfig (cmd/kuke/run/run.go) so equivalent
// specs from `kuke run` and `kuke apply` compare equal under DiffCell.
func loadCellRef(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) (v1beta1.CellDoc, error) {
	switch {
	case flags.configName != "":
		return loadFromConfig(cmd, client, flags)
	case flags.blueprintName != "":
		return loadFromBlueprint(cmd, client, flags)
	}
	return v1beta1.CellDoc{}, errors.New("internal error: loadCellRef called without -b/-c")
}

func loadFromBlueprint(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) (v1beta1.CellDoc, error) {
	cliParams, err := buildParamMap(flags)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}

	lookup := v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  flags.blueprintName,
			Realm: pickRealm(cmd),
			Space: explicitScope(cmd, "space"),
			Stack: explicitScope(cmd, "stack"),
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

func loadFromConfig(cmd *cobra.Command, client kukeonv1.Client, flags applyFlags) (v1beta1.CellDoc, error) {
	lookup := v1beta1.CellConfigDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindCellConfig,
		Metadata: v1beta1.CellConfigMetadata{
			Name:  flags.configName,
			Realm: pickRealm(cmd),
			Space: explicitScope(cmd, "space"),
			Stack: explicitScope(cmd, "stack"),
		},
	}

	cfgRes, err := client.GetConfig(cmd.Context(), lookup)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if !cfgRes.MetadataExists {
		return v1beta1.CellDoc{}, fmt.Errorf(
			"%w (config %q in scope realm=%q space=%q stack=%q)",
			errdefs.ErrConfigNotFound, lookup.Metadata.Name,
			lookup.Metadata.Realm, lookup.Metadata.Space, lookup.Metadata.Stack,
		)
	}

	bpRef := cfgRes.Config.Spec.Blueprint
	bpRes, err := client.GetBlueprint(cmd.Context(), v1beta1.CellBlueprintDoc{
		Metadata: v1beta1.CellBlueprintMetadata{
			Name:  bpRef.Name,
			Realm: bpRef.Realm,
			Space: bpRef.Space,
			Stack: bpRef.Stack,
		},
	})
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

	cellDoc, err := cellconfig.Materialize(cfgRes.Config, bpRes.Blueprint)
	if err != nil {
		return v1beta1.CellDoc{}, err
	}
	if flags.nameOverride != "" {
		cellDoc.Metadata.Name = flags.nameOverride
		cellDoc.Spec.ID = flags.nameOverride
	}
	return cellDoc, nil
}

// buildParamMap layers --param flags on top of --param-file contents, matching
// `kuke run -b`'s precedence. Empty result is valid (the blueprint may use only
// defaults).
func buildParamMap(flags applyFlags) (map[string]string, error) {
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

// resolveCellLocation fills missing realm/space/stack on the materialised doc
// from --realm/--space/--stack. The materialised doc usually carries the
// Blueprint/Config's coordinates already; this only fills empties.
func resolveCellLocation(cmd *cobra.Command, doc *v1beta1.CellDoc) {
	if strings.TrimSpace(doc.Spec.RealmID) == "" {
		doc.Spec.RealmID = pickRealm(cmd)
	}
	if strings.TrimSpace(doc.Spec.SpaceID) == "" {
		doc.Spec.SpaceID = explicitScope(cmd, "space")
	}
	if strings.TrimSpace(doc.Spec.StackID) == "" {
		doc.Spec.StackID = explicitScope(cmd, "stack")
	}
}

// pickRealm returns the --realm value if set, falling back to "default" so the
// lookup always names a realm (parity with `kuke run`'s default).
func pickRealm(cmd *cobra.Command) string {
	if v, _ := cmd.Flags().GetString("realm"); strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return "default"
}

// explicitScope returns the named scope flag only when the operator set it
// explicitly. Mirrors run.explicitScope so realm-scoped Blueprints/Configs (no
// space or stack coordinate) are findable; the cobra flag default of "" stays
// "do not constrain this coordinate".
func explicitScope(cmd *cobra.Command, flagName string) string {
	if !cmd.Flags().Changed(flagName) {
		return ""
	}
	v, _ := cmd.Flags().GetString(flagName)
	return strings.TrimSpace(v)
}

// assertCellLineage refuses to reconcile a live cell whose lineage label does
// not match the source the operator named. The check protects hand-built cells
// (no lineage label) and cells materialised from a different Blueprint/Config
// (different label value) from silent takeover. A missing cell skips the
// check — there is nothing to overwrite.
func assertCellLineage(
	ctx context.Context, client kukeonv1.Client, desired v1beta1.CellDoc, flags applyFlags,
) error {
	pre, err := client.GetCell(ctx, desired)
	if err != nil {
		if errors.Is(err, errdefs.ErrCellNotFound) {
			return nil
		}
		return err
	}
	if !pre.MetadataExists {
		return nil
	}

	labelKey, wantValue := lineageExpectation(flags)
	gotValue := strings.TrimSpace(pre.Cell.Metadata.Labels[labelKey])

	if gotValue == wantValue {
		return nil
	}
	return lineageMismatchError(desired.Metadata.Name, labelKey, wantValue, pre.Cell.Metadata.Labels)
}

// lineageExpectation maps the active source flag to the (labelKey, wantValue)
// pair the live cell must carry. The caller already verified exactly one of -b
// or -c is set.
func lineageExpectation(flags applyFlags) (string, string) {
	if flags.configName != "" {
		return cellconfig.LabelConfig, flags.configName
	}
	return cellblueprint.LabelBlueprint, flags.blueprintName
}

// lineageMismatchError formats the refusal so the operator sees both what
// `apply` expected and the existing cell's source (if any). A hand-built cell
// reports "no lineage label"; a sibling-source cell reports the other label.
func lineageMismatchError(cellName, wantKey, wantValue string, got map[string]string) error {
	var existingSource string
	switch {
	case strings.TrimSpace(got[cellconfig.LabelConfig]) != "":
		existingSource = fmt.Sprintf("%s=%s", cellconfig.LabelConfig, got[cellconfig.LabelConfig])
	case strings.TrimSpace(got[cellblueprint.LabelBlueprint]) != "":
		existingSource = fmt.Sprintf("%s=%s", cellblueprint.LabelBlueprint, got[cellblueprint.LabelBlueprint])
	default:
		existingSource = "no lineage label"
	}
	return fmt.Errorf(
		"cell %q exists with lineage %s; refusing to reconcile against %s=%s",
		cellName, existingSource, wantKey, wantValue,
	)
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukshared.DaemonClientFromCmd(cmd)
}

func printApplyResult(cmd *cobra.Command, result kukeonv1.ApplyDocumentsResult) error {
	hasFailures := false
	for _, resource := range result.Resources {
		switch resource.Action {
		case "created":
			cmd.Printf("%s %q: created\n", resource.Kind, resource.Name)
		case "updated":
			cmd.Printf("%s %q: updated\n", resource.Kind, resource.Name)
			for _, change := range resource.Changes {
				cmd.Printf("  - %s\n", change)
			}
		case "unchanged":
			cmd.Printf("%s %q: unchanged\n", resource.Kind, resource.Name)
		case "failed":
			hasFailures = true
			cmd.Printf("%s %q: failed\n", resource.Kind, resource.Name)
			if resource.Error != "" {
				cmd.Printf("  Error: %s\n", resource.Error)
			}
		}
	}

	if hasFailures {
		return fmt.Errorf("%w: some resources failed to apply", errdefs.ErrConfig)
	}

	return nil
}

func printApplyResultJSON(cmd *cobra.Command, result kukeonv1.ApplyDocumentsResult, format string) error {
	output := struct {
		Resources []kukeonv1.ApplyResourceResult `json:"resources" yaml:"resources"`
	}{
		Resources: result.Resources,
	}
	return kukshared.PrintJSONOrYAML(cmd, output, format)
}
