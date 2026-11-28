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
	"bytes"
	"errors"
	"fmt"
	"io"

	kukshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/apply/parser"
	"github.com/eminwux/kukeon/internal/errdefs"
	teamcli "github.com/eminwux/kukeon/internal/team/cli"
	"github.com/eminwux/kukeon/internal/team/initialize"
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
// YAML stream from disk or stdin — the sole shape `apply` supports. The
// daemon-side reconcile-by-ref forms (`-b`/`-c`) were retired under #819; the
// equivalent operator workflow is `kuke restart <name>` (which sees
// OutOfSync on Config-lineage cells and reconciles implicitly).
func NewApplyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply -f <file>",
		Short: "Apply resource definitions from a YAML file or stdin",
		Long: "Apply resource definitions from a YAML file or stdin (-f).\n\n" +
			"A Team document is composed by kuke, exactly as kuke team apply -f composes it, and kukeond stores " +
			"it with the Configs composed from it; the other documents are applied as they are.",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runApply,
	}

	cmd.Flags().StringP("file", "f", "", "File to read YAML from (use - for stdin)")
	cmd.Flags().StringP("output", "o", "", "Output format: json, yaml (default: human-readable)")

	return cmd
}

// applyFlags is the validated bundle of flag values runApply consumes.
type applyFlags struct {
	file   string
	output string
}

func parseApplyFlags(cmd *cobra.Command) (applyFlags, error) {
	flags := applyFlags{}
	var err error
	if flags.file, err = cmd.Flags().GetString("file"); err != nil {
		return flags, err
	}
	if flags.output, err = cmd.Flags().GetString("output"); err != nil {
		return flags, err
	}

	if flags.output != "" && flags.output != outputFormatJSON && flags.output != outputFormatYAML {
		return flags, fmt.Errorf("invalid --output %q: want json or yaml", flags.output)
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

	return runApplyFile(cmd, client, flags)
}

// runApplyFile is the `kuke apply -f` path: read YAML, send to the daemon's
// ApplyDocuments, print result.
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

	rest, teams, hasDefaults, err := splitTeams(rawYAML)
	if err != nil {
		return err
	}

	// Documents other than Teams are applied as before, first, so a
	// TeamDefaults in the same file is what its Teams inherit.
	var result kukeonv1.ApplyDocumentsResult
	if len(rest) > 0 {
		if err = warnTeamConfigs(cmd.ErrOrStderr(), rest); err != nil {
			return err
		}
		if result, err = client.ApplyDocuments(cmd.Context(), rest); err != nil {
			return err
		}
	}
	structured := flags.output == outputFormatJSON || flags.output == outputFormatYAML
	var applyErr error
	if !structured {
		applyErr = printApplyResult(cmd, result)
		if hasDefaults && applyErr == nil {
			teams, inhErr := initialize.InheritingTeams(cmd.Context(), client)
			if inhErr != nil {
				return inhErr
			}
			if err = teamcli.PrintInheriting(cmd.OutOrStdout(), teams); err != nil {
				return err
			}
		}
	}
	// A Team composes against what the rest of the file applied, so one
	// failed document there skips every Team rather than composing against
	// defaults the file meant to change.
	if failedResults(result) && len(teams) > 0 {
		for _, team := range teams {
			name := teamName(team)
			if structured {
				result.Resources = append(result.Resources, kukeonv1.ApplyResourceResult{
					Kind: string(v1beta1.KindTeam), Name: name, Action: "failed",
					Error: "not applied: another document in the file failed",
				})
				continue
			}
			cmd.Printf("%s %q: skipped (another document in the file failed)\n", v1beta1.KindTeam, name)
		}
		if structured {
			return printApplyResultJSON(cmd, result, flags.output)
		}
		return applyErr
	}
	// Each Team is composed here, as kuke team apply -f composes it (D7):
	// kukeond stores it only with the Configs composed from it. A Team that
	// fails is reported and the rest are still tried, so the output names
	// every document in the file.
	var teamErrs []error
	for _, team := range teams {
		out, teamErr := initialize.ApplyFile(cmd.Context(), initialize.Options{Client: client}, team, flags.file)
		if teamErr != nil {
			name := teamName(team)
			teamErrs = append(teamErrs, fmt.Errorf("%s %q: %w", v1beta1.KindTeam, name, teamErr))
			if structured {
				result.Resources = append(result.Resources, kukeonv1.ApplyResourceResult{
					Kind: string(v1beta1.KindTeam), Name: name, Action: "failed", Error: teamErr.Error(),
				})
				continue
			}
			cmd.Printf("%s %q: failed\n", v1beta1.KindTeam, name)
			continue
		}
		if structured {
			result.Resources = append(result.Resources, kukeonv1.ApplyResourceResult{
				Kind: string(v1beta1.KindTeam), Name: out.Result.Team, Action: "applied",
			})
			continue
		}
		if err = teamcli.PrintOutcome(cmd.OutOrStdout(), out); err != nil {
			return err
		}
	}
	teamsErr := errors.Join(teamErrs...)
	if structured {
		if err = printApplyResultJSON(cmd, result, flags.output); err != nil {
			return err
		}
		return teamsErr
	}
	if teamsErr != nil {
		return teamsErr
	}
	return applyErr
}

func failedResults(result kukeonv1.ApplyDocumentsResult) bool {
	for _, resource := range result.Resources {
		if resource.Action == "failed" {
			return true
		}
	}
	return false
}

// teamName reads a Team document's metadata.name for a message.
func teamName(doc []byte) string {
	var head struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	_ = yaml.Unmarshal(doc, &head)
	return head.Metadata.Name
}

// warnTeamConfigs prints one warning per CellConfig in docs that a team owns
// (label kukeon.io/team, issue #1649, AC11): the team's next apply renders
// the Config whole again, so a hand edit of it does not last. The apply still
// happens. docs are the documents sent as they are, never the Configs a Team
// document in the same file composes.
func warnTeamConfigs(w io.Writer, docs []byte) error {
	parsed, err := parser.ParseDocuments(bytes.NewReader(docs))
	if err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}
	for _, doc := range parsed {
		if kind, kindErr := parser.DetectKind(doc); kindErr != nil || kind != v1beta1.KindCellConfig {
			continue
		}
		var head struct {
			Metadata struct {
				Name   string            `yaml:"name"`
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		}
		if yaml.Unmarshal(doc, &head) != nil {
			continue
		}
		team := head.Metadata.Labels[v1beta1.LabelTeam]
		if team == "" {
			continue
		}
		if _, err = fmt.Fprintf(w, "warning: config %s belongs to team %s; the team's next apply replaces "+
			"this edit. Change the team instead: kuke team connector, kuke team apply -f, or the Teams page.\n",
			head.Metadata.Name, team); err != nil {
			return err
		}
	}
	return nil
}

// splitTeams separates a file's Team documents from the rest, and reports
// whether it holds a TeamDefaults.
func splitTeams(rawYAML []byte) ([]byte, [][]byte, bool, error) {
	docs, err := parser.ParseDocuments(bytes.NewReader(rawYAML))
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to parse YAML: %w", err)
	}
	var rest []byte
	var teams [][]byte
	hasDefaults := false
	for _, doc := range docs {
		kind, kindErr := parser.DetectKind(doc)
		if kindErr != nil {
			return nil, nil, false, kindErr
		}
		switch kind { //nolint:exhaustive // only the team kinds are told apart
		case v1beta1.KindTeam:
			teams = append(teams, doc)
			continue
		case v1beta1.KindTeamDefaults:
			hasDefaults = true
		}
		if len(rest) > 0 {
			rest = append(rest, []byte("\n---\n")...)
		}
		rest = append(rest, doc...)
	}
	return rest, teams, hasDefaults, nil
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
		// A warned resource was applied: the warning names a risk the
		// operator accepted by applying it (issue #1789).
		for _, warning := range resource.Warnings {
			if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning); err != nil {
				return err
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
