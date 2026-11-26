// Copyright 2026 Emiliano Spinella (eminwux)
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

// Package agent implements `kuke create agent` (issue #1692): New agent from
// the CLI. It copies the harness's agent template into a Config of its own,
// then creates and starts that Config's first cell, and with -a attaches to
// it like `kuke run -a`.
package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/run"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	agentpkg "github.com/eminwux/kukeon/internal/agent"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

// MockControllerKey injects a kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// MockAttachKey injects the attach step via context in tests.
type MockAttachKey struct{}

// AttachFunc attaches to a started cell.
type AttachFunc func(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error

// NewAgentCmd builds `kuke create agent`.
func NewAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent --harness claude|codex",
		Short: "Create an agent from its template and start it (New agent)",
		Long: `Create a Claude or Codex agent, the CLI's New agent.

The agent is a copy of the harness's agent template, default-agent-claude or
default-agent-codex, which kuke team init stores with the default team. The
copy is a CellConfig of its own, named <template>-<6 hex>, with the template's
spec unchanged: every agent of a harness uses the same state volume, and so
the same login. It carries the labels kukeon.io/role and kukeon.io/harness and
the annotations kukeon.io/agent-template and kukeon.io/agent-team (the
template's team, whose role the copy runs, so it shares the template's login
identity); --name stores the agent's display
name as kukeon.io/display-name. It has no kukeon.io/team label, so applying a
team never removes it.

The command then creates the Config's first cell, with a generated name, and
starts it. -a attaches to it as kuke run -a does; detach with ^]^].

When creating or starting the cell fails, the Config is kept: the error names
it, and kuke run --from-config <config> (or kuke start <cell>) finishes the job.`,
		Example: `  kuke create agent --harness claude --name "Reviewer"
  kuke create agent --harness codex -a`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateAgent,
	}
	cmd.Flags().String("harness", "", "Harness of the agent: claude or codex (required)")
	cmd.Flags().String("name", "", "Display name of the agent (kukeon.io/display-name)")
	cmd.Flags().String("realm", "", "Realm to store the agent's Config in (default: the template's)")
	cmd.Flags().String("space", "", "Space to store the agent's Config in (requires --realm)")
	cmd.Flags().String("stack", "", "Stack to store the agent's Config in (requires --space)")
	cmd.Flags().BoolP("attach", "a", false, "Attach to the agent's cell once it is started")
	_ = cmd.RegisterFlagCompletionFunc(
		"harness",
		func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return agentpkg.Harnesses(), cobra.ShellCompDirectiveNoFileComp
		},
	)
	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)
	return cmd
}

func runCreateAgent(cmd *cobra.Command, _ []string) error {
	flag := func(name string) string {
		value, _ := cmd.Flags().GetString(name)
		return strings.TrimSpace(value)
	}
	harness := flag("harness")
	if harness == "" {
		return errors.New("--harness is required: claude or codex")
	}
	if _, err := agentpkg.TemplateName(harness); err != nil {
		return err
	}
	req := agentpkg.Request{
		Harness: harness, DisplayName: flag("name"),
		Realm: flag("realm"), Space: flag("space"), Stack: flag("stack"),
	}
	attach, _ := cmd.Flags().GetBool("attach")

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	res, err := agentpkg.Create(cmd.Context(), client, req, agentpkg.Options{})
	if res.Config.Metadata.Name != "" {
		printConfig(cmd, res.Config)
	}
	if err != nil {
		return err
	}
	cmd.Printf("Cell %q created and started in %s\n", res.Cell.Metadata.Name,
		scopePath(res.Cell.Spec.RealmID, res.Cell.Spec.SpaceID, res.Cell.Spec.StackID))
	if !attach {
		cmd.Printf("Attach with: kuke attach %s --realm %s --space %s --stack %s\n", res.Cell.Metadata.Name,
			res.Cell.Spec.RealmID, res.Cell.Spec.SpaceID, res.Cell.Spec.StackID)
		return nil
	}
	return attachFunc(cmd)(cmd, client, res.Cell)
}

func printConfig(cmd *cobra.Command, cfg v1beta1.CellConfigDoc) {
	md := cfg.Metadata
	label := ""
	if name := md.Annotations[v1beta1.AnnotationDisplayName]; name != "" {
		label = fmt.Sprintf(" (%s)", name)
	}
	cmd.Printf("Config %q%s stored in %s, from template %s\n", md.Name, label,
		scopePath(md.Realm, md.Space, md.Stack), md.Annotations[v1beta1.AnnotationAgentTemplate])
}

func scopePath(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "/")
}

func attachFunc(cmd *cobra.Command) AttachFunc {
	if mock, ok := cmd.Context().Value(MockAttachKey{}).(AttachFunc); ok {
		return mock
	}
	return func(cmd *cobra.Command, client kukeonv1.Client, doc v1beta1.CellDoc) error {
		container, err := run.AttachTarget(doc.Spec, doc.Metadata.Name)
		if err != nil {
			return err
		}
		_, err = run.AttachCell(cmd, client, doc, container)
		return err
	}
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}
