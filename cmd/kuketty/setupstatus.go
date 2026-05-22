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

package main

import (
	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	sbshserver "github.com/eminwux/sbsh/pkg/terminal/server"
)

// setupStatusHandler is the receiver kuketty registers as a custom JSON-RPC
// service on the sbsh control server (issue #642). It serves the per-repo
// outcome of the pre-Serve clone/fetch step (issue #617) so kukeond can pull
// it post-Serve and write ContainerStatus.Repos.
//
// The statuses are captured once, before Serve brings the control RPC live,
// and never mutated afterward, so the read in GetSetupStatus needs no
// synchronization — the slice is published happens-before the server goroutine
// that serves the verb is started.
type setupStatusHandler struct {
	repos []setupstatus.Repo
}

// GetSetupStatus reports kuketty's pre-Serve repo outcomes. The wire method is
// "<setupstatus.ServiceName>.GetSetupStatus" (see setupstatus.Method). The
// net/rpc signature scheme requires (args, reply *T) error with exported
// argument/reply types — satisfied by setupstatus.Args and setupstatus.Reply.
// The error return is mandated by that signature even though this read-only
// verb never fails; net/rpc would reject the method as an RPC handler without
// it.
//
//nolint:unparam // error return required by net/rpc's handler signature (see godoc above)
func (h *setupStatusHandler) GetSetupStatus(_ setupstatus.Args, reply *setupstatus.Reply) error {
	reply.Repos = h.repos
	return nil
}

// setupStatusOption returns the sbsh server option that registers the
// GetSetupStatus verb on the control socket, carrying the resolved repo
// statuses. Returned as a slice so run() can splat it into server.New
// uniformly whether or not there are repos to report — an empty repos[] still
// registers the verb (kukeond gets an empty Reply rather than a dial error,
// keeping the daemon-side pull a single code path).
func setupStatusOption(repos []setupstatus.Repo) []sbshserver.Option {
	return []sbshserver.Option{
		sbshserver.WithHandlers(sbshserver.Handler{
			Name:     setupstatus.ServiceName,
			Receiver: &setupStatusHandler{repos: repos},
		}),
	}
}
