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
	"io"
	"text/tabwriter"

	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// ConfigRenderRequest builds the daemon-side render request for a
// `--from-config` source (issue #1418). The Config is looked up at the scope
// the operator named — an unset --space/--stack resolves to "default" while an
// explicit empty one keeps a realm-scoped Config reachable — and the daemon
// renders the cell (defaults merge, slot fills, realm images, name
// allocation). The client never renders a CellConfig itself.
func ConfigRenderRequest(cmd *cobra.Command, flags SourceFlags, scope ScopeVars) kukeonv1.CellFromConfigRequest {
	return kukeonv1.CellFromConfigRequest{
		Config: v1beta1.CellConfigMetadata{
			Name:  flags.ConfigName,
			Realm: kukeshared.PickLookupRealm(cmd, scope.Realm),
			Space: kukeshared.ExplicitScope(cmd, "space", scope.Space),
			Stack: kukeshared.ExplicitScope(cmd, "stack", scope.Stack),
		},
		Name:               flags.Name,
		EnvOverrides:       append([]string(nil), flags.EnvArgs...),
		IgnoreDiskPressure: flags.IgnoreDiskPressure,
	}
}

// createFromConfig implements `kuke create cell <name> --from-config <cfg>`:
// the daemon renders and persists the cell stopped. With dryRun it previews
// the render instead — the Cell YAML, or with explain the table of every
// merged value and the level that set it — without writing anything.
func createFromConfig(
	cmd *cobra.Command, client kukeonv1.Client, flags SourceFlags, scope ScopeVars, dryRun, explain bool,
) error {
	if err := ValidateOverrideSymmetry(flags); err != nil {
		return err
	}
	req := ConfigRenderRequest(cmd, flags, scope)

	if dryRun {
		preview, err := client.PreviewCellFromConfig(cmd.Context(), req)
		if err != nil {
			return err
		}
		if explain {
			return PrintRenderExplain(cmd.OutOrStdout(), preview.Cell, preview.Digest, preview.Values)
		}
		return printRenderedCell(cmd.OutOrStdout(), preview.Cell)
	}

	result, err := client.CreateCellFromConfig(cmd.Context(), req)
	if err != nil {
		return err
	}
	printCellResult(cmd, result.CreateCellResult)
	if explain {
		return PrintRenderExplain(cmd.OutOrStdout(), result.Cell, result.Digest, result.Values)
	}
	return nil
}

// printRenderedCell writes the rendered Cell document as YAML.
func printRenderedCell(w io.Writer, doc v1beta1.CellDoc) error {
	data, err := yaml.Marshal(doc)
	if err != nil {
		return fmt.Errorf("render cell yaml: %w", err)
	}
	_, err = w.Write(data)
	return err
}

// PrintRenderExplain prints the rendered cell's identity and digest, then one
// row per merged value with the level that set it (`--explain`, #1418).
func PrintRenderExplain(w io.Writer, doc v1beta1.CellDoc, digest string, values []v1beta1.RenderedValue) error {
	if _, err := fmt.Fprintf(w, "Cell %q (realm %q, space %q, stack %q)\nDigest: %s\n\n",
		doc.Metadata.Name, doc.Spec.RealmID, doc.Spec.SpaceID, doc.Spec.StackID, digest); err != nil {
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, explainColumnPadding, ' ', 0)
	if _, err := fmt.Fprintln(tw, "KIND\tKEY\tVALUE\tORIGIN"); err != nil {
		return err
	}
	for _, v := range values {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", v.Kind, v.Key, v.Value, v.Origin); err != nil {
			return err
		}
	}
	if len(values) == 0 {
		if _, err := fmt.Fprintln(tw, "-\t-\t-\t-"); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// explainColumnPadding is the gap between --explain table columns.
const explainColumnPadding = 2

// errDryRunNeedsConfig is returned when --dry-run / --explain are combined
// with a source other than --from-config.
var errDryRunNeedsConfig = errors.New("--dry-run and --explain are only supported with --from-config")
