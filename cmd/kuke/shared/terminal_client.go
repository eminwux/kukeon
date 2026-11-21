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

package shared

import (
	"github.com/eminwux/kukeon/internal/logging"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	rpcterminal "github.com/eminwux/sbsh/pkg/rpcclient/terminal"
	"github.com/spf13/cobra"
)

// ConnectTerminalClient resolves target (ResolveTerminalTarget) and returns
// an sbsh terminal RPC client whose every dial goes through
// client.ConnectContainerTerminal, for one-shot control calls such as
// Screenshot and Write. The preflight connect runs the daemon's Attachable /
// task-Running gates, so a refused target fails here with `kuke attach`'s
// errors (WrapTerminalConnectError).
//
// The caller must call the returned release func (it closes a parked
// preflight connection the RPC never consumed) and Close the client.
func ConnectTerminalClient(
	cmd *cobra.Command,
	client kukeonv1.Client,
	target TerminalTarget,
) (rpcterminal.Client, func(), error) {
	ctx := cmd.Context()
	doc, err := ResolveTerminalTarget(ctx, client, target)
	if err != nil {
		return nil, nil, err
	}

	logger, err := LoggerFromCmd(cmd)
	if err != nil {
		logger = logging.NewNoopLogger()
	}

	dialer := NewTerminalDialer(client, doc)
	rc, err := dialer.TerminalClient(ctx, logger)
	if err != nil {
		dialer.Close()
		return nil, nil, WrapTerminalConnectError(err, doc)
	}
	return rc, dialer.Close, nil
}
