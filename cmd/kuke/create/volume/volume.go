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

// Package volume implements `kuke create volume <name>` (issue #1236, step 2 of
// the volumes epic #1015). Unlike `kuke create blueprint` — which scaffolds a
// YAML document to stdout because a CellBlueprint is declaratively complex — a
// Volume's spec is empty and the resource is the on-host directory the daemon
// provisions, so this verb is imperative like `kuke create secret`: it persists
// the Volume to the daemon and prints the outcome. A Volume is scopable at
// realm/space/stack only; cell scope is structurally unrepresentable (there is
// no --cell flag), satisfying the AC's "cell scope rejected".
package volume

import (
	"errors"
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/cmd/config"
	"github.com/eminwux/kukeon/cmd/kuke/create/shared"
	kukeshared "github.com/eminwux/kukeon/cmd/kuke/shared"
	"github.com/eminwux/kukeon/internal/connector/loginidentity"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// MockControllerKey is used to inject a mock kukeonv1.Client via context in tests.
type MockControllerKey struct{}

// NewVolumeCmd builds `kuke create volume <name>` (issue #1236).
func NewVolumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "volume [name]",
		Aliases: []string{"vol"},
		Short:   "Create a kind: Volume within a scope",
		Long: "Create a standalone, daemon-managed Volume within a scope.\n\n" +
			"A Volume is the on-host directory the daemon provisions; its spec is empty. " +
			"It is scopable at realm/space/stack only — never cell — so a volume can outlive " +
			"the cells that mount it (the mount kind lands in step 4, #1016).",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE:          runCreateVolume,
	}

	cmd.Flags().String("realm", "", "Realm that owns the volume")
	_ = viper.BindPFlag(config.KUKE_CREATE_VOLUME_REALM.ViperKey, cmd.Flags().Lookup("realm"))

	cmd.Flags().String("space", "", "Space that owns the volume")
	_ = viper.BindPFlag(config.KUKE_CREATE_VOLUME_SPACE.ViperKey, cmd.Flags().Lookup("space"))

	cmd.Flags().String("stack", "", "Stack that owns the volume")
	_ = viper.BindPFlag(config.KUKE_CREATE_VOLUME_STACK.ViperKey, cmd.Flags().Lookup("stack"))

	cmd.Flags().String("connector", "",
		"Tag the volume as a login identity of this connector: the volume holds the login a "+
			"harness keeps in its state directory. `kuke connector login` creates one and logs in")
	cmd.Flags().String("method", "",
		"Login method of --connector the identity belongs to (default: the connector's login method)")

	_ = cmd.RegisterFlagCompletionFunc("realm", config.CompleteRealmNames)
	_ = cmd.RegisterFlagCompletionFunc("space", config.CompleteSpaceNames)
	_ = cmd.RegisterFlagCompletionFunc("stack", config.CompleteStackNames)

	return cmd
}

func runCreateVolume(cmd *cobra.Command, args []string) error {
	name, err := shared.RequireNameArgOrDefault(
		cmd,
		args,
		"volume",
		viper.GetString(config.KUKE_CREATE_VOLUME_NAME.ViperKey),
	)
	if err != nil {
		return err
	}

	realm := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_VOLUME_REALM.ViperKey))
	if realm == "" {
		realm = strings.TrimSpace(config.KUKE_CREATE_VOLUME_REALM.ValueOrDefault())
	}
	space := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_VOLUME_SPACE.ViperKey))
	if space == "" {
		space = strings.TrimSpace(config.KUKE_CREATE_VOLUME_SPACE.ValueOrDefault())
	}
	stack := strings.TrimSpace(viper.GetString(config.KUKE_CREATE_VOLUME_STACK.ViperKey))
	if stack == "" {
		stack = strings.TrimSpace(config.KUKE_CREATE_VOLUME_STACK.ValueOrDefault())
	}

	doc := v1beta1.VolumeDoc{
		APIVersion: v1beta1.APIVersionV1Beta1,
		Kind:       v1beta1.KindVolume,
		Metadata: v1beta1.VolumeMetadata{
			Name:  name,
			Realm: realm,
			Space: space,
			Stack: stack,
		},
	}
	connectorName, _ := cmd.Flags().GetString("connector")
	methodName, _ := cmd.Flags().GetString("method")
	if doc, err = tagLoginIdentity(doc, connectorName, methodName); err != nil {
		return err
	}

	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	result, err := client.CreateVolume(cmd.Context(), doc)
	if err != nil {
		return err
	}

	printVolumeResult(cmd, result)
	return nil
}

// tagLoginIdentity turns the Volume into a login identity of the named
// connector (--connector): tagged with the connector and its login method,
// carrying the method's login marker, and retained. Without --connector the
// Volume is returned as is, and --method alone is refused.
func tagLoginIdentity(doc v1beta1.VolumeDoc, connectorName, methodName string) (v1beta1.VolumeDoc, error) {
	connectorName, methodName = strings.TrimSpace(connectorName), strings.TrimSpace(methodName)
	if connectorName == "" {
		if methodName != "" {
			return doc, errors.New("--method needs --connector")
		}
		return doc, nil
	}
	catalog, err := connector.Default()
	if err != nil {
		return doc, err
	}
	conn, ok := catalog.Find(connectorName)
	if !ok {
		return doc, fmt.Errorf("unknown connector %q (connectors: %s)",
			connectorName, strings.Join(catalog.Names(), ", "))
	}
	method, err := loginidentity.Method(conn, methodName)
	if err != nil {
		return doc, err
	}
	return loginidentity.VolumeDoc(conn, method, v1beta1.VolumeRef(doc.Metadata)), nil
}

func resolveClient(cmd *cobra.Command) (kukeonv1.Client, error) {
	if mockClient, ok := cmd.Context().Value(MockControllerKey{}).(kukeonv1.Client); ok {
		return mockClient, nil
	}
	return kukeshared.DaemonClientFromCmd(cmd)
}

func printVolumeResult(cmd *cobra.Command, result kukeonv1.CreateVolumeResult) {
	cmd.Printf(
		"Volume %q (realm %q, space %q, stack %q)\n",
		result.Volume.Metadata.Name,
		result.Volume.Metadata.Realm,
		result.Volume.Metadata.Space,
		result.Volume.Metadata.Stack,
	)
	// The volume directory always exists post-create (WriteVolume is
	// idempotent), so existsPost=true renders "already existed" on a re-create.
	shared.PrintCreationOutcome(cmd, "directory", true, result.Created)
}
