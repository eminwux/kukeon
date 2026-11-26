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

package connector

import (
	"errors"
	"fmt"

	"github.com/eminwux/kukeon/cmd/kuke/run"
	"github.com/eminwux/kukeon/internal/connector/loginidentity"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/spf13/cobra"
)

// MockAttachKey injects the attach step of `kuke connector login` via context
// for tests.
type MockAttachKey struct{}

// AttachFunc attaches the terminal to an identity cell's container and
// reports whether the operator detached while the login was still running.
type AttachFunc func(cmd *cobra.Command, doc v1beta1.CellDoc, container string) (detached bool, err error)

func newLoginCmd(catalog *connector.Catalog) *cobra.Command {
	var realm, space, stack, method, image string
	cmd := &cobra.Command{
		Use:   "login CONNECTOR [NAME]",
		Short: "Create a login identity and log in to it",
		Long: "Create a login identity of a connector and log in to it.\n\n" +
			"A login identity is a Volume tagged with the connector: the harness keeps its login in\n" +
			"its state directory, and that directory is the Volume. The command creates the Volume\n" +
			"when it does not exist (reclaimPolicy Retain), starts an identity cell — one container\n" +
			"on the harness image that mounts only that Volume and runs the harness login — attaches\n" +
			"your terminal to it, and deletes the cell when the login ends. Run it again on an\n" +
			"existing identity to log in again; a team's state volume is an identity too.\n\n" +
			"NAME defaults to the connector's name. Without --space and --stack the identity is\n" +
			"scoped to the realm. Detach with ^]^] to leave the login running; the same command\n" +
			"attaches to it again instead of starting a second one.",
		Example: "  kuke connector login claude personal-max\n" +
			"  kuke connector login codex --realm default --space teams --stack kukeon kukeon-dev-codex-state",
		Args:          cobra.RangeArgs(1, 2), //nolint:mnd // CONNECTOR and an optional NAME
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			req := loginidentity.Request{
				Connector: args[0], Method: method, Image: image,
				Volume: v1beta1.VolumeRef{Realm: realm, Space: space, Stack: stack},
			}
			if len(args) > 1 {
				req.Volume.Name = args[1]
			}
			return runLogin(cmd, catalog, req)
		},
	}
	cmd.Flags().StringVar(&realm, "realm", "", "Realm of the identity (default \"default\")")
	cmd.Flags().StringVar(&space, "space", "", "Space of the identity; empty scopes it to the realm")
	cmd.Flags().StringVar(&stack, "stack", "", "Stack of the identity; needs --space")
	cmd.Flags().StringVar(&method, "method", "", "Login method of the connector (default: its login method)")
	cmd.Flags().StringVar(&image, "image", "",
		"Image the identity cell runs (default: the image of a Config using the identity, else the harness image)")
	return cmd
}

func runLogin(cmd *cobra.Command, catalog *connector.Catalog, req loginidentity.Request) error {
	client, err := resolveClient(cmd)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	session, err := loginidentity.Start(cmd.Context(), client, catalog, req)
	if err != nil {
		return err
	}
	ref := v1beta1.VolumeRef{
		Name: session.Volume.Metadata.Name, Realm: session.Volume.Metadata.Realm,
		Space: session.Volume.Metadata.Space, Stack: session.Volume.Metadata.Stack,
	}
	where := ref.Name + "@" + loginidentity.ScopePath(ref)
	errOut := cmd.ErrOrStderr()
	switch {
	case session.VolumeCreated:
		fmt.Fprintf(errOut, "Created login identity %s (connector %s).\n", where, req.Connector)
	case session.Attached:
		fmt.Fprintf(errOut, "A login is already running for %s; attaching to it.\n", where)
	}

	detached, attachErr := attachFunc(cmd, client)(cmd, session.Cell, session.Container)
	if detached {
		fmt.Fprintf(errOut,
			"Detached; the login is still running in cell %q. Run the same command to attach again.\n",
			session.Cell.Metadata.Name)
		return attachErr
	}
	// The cell deletes itself when the login exits; remove it now so the
	// identity is free for the next login at once. Best-effort. A failed
	// attach to another client's session — one still starting, say — leaves
	// it alone: that login is theirs.
	if attachErr == nil || !session.Attached {
		cleanupIdentityCell(cmd, client, session.Cell)
	}
	if attachErr != nil {
		return attachErr
	}

	got, err := client.GetVolume(cmd.Context(), session.Volume)
	if err != nil {
		return err
	}
	status := "-"
	if login := got.Volume.Status.Login; login != nil {
		status = login.State
	}
	cmd.Printf("Login identity %s: %s\n", where, status)
	if status == v1beta1.LoginStateEmpty {
		return fmt.Errorf("%w: the login did not complete: identity %s holds no login",
			errdefs.ErrVolumeIdentity, where)
	}
	return nil
}

// attachFunc returns the attach step: an injected one in tests, else the
// attach loop `kuke run` uses.
func attachFunc(cmd *cobra.Command, client Client) AttachFunc {
	if mock, ok := cmd.Context().Value(MockAttachKey{}).(AttachFunc); ok {
		return mock
	}
	return func(cmd *cobra.Command, doc v1beta1.CellDoc, container string) (bool, error) {
		full, ok := client.(kukeonv1.Client)
		if !ok {
			return false, errors.New("attach needs a daemon client")
		}
		return run.AttachCell(cmd, full, doc, container)
	}
}

// cleanupIdentityCell kills and deletes an identity cell whose session ended.
// A cell the daemon already reaped is the expected outcome.
func cleanupIdentityCell(cmd *cobra.Command, client Client, doc v1beta1.CellDoc) {
	report := func(step string, err error) {
		if err != nil && !errors.Is(err, errdefs.ErrCellNotFound) {
			fmt.Fprintf(cmd.ErrOrStderr(), "kuke connector login: failed to %s identity cell %q: %v\n",
				step, doc.Metadata.Name, err)
		}
	}
	if _, err := client.KillCell(cmd.Context(), doc); err != nil {
		report("stop", err)
		return
	}
	_, err := client.DeleteCell(cmd.Context(), doc)
	report("delete", err)
}
