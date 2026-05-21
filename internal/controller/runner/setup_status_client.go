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

package runner

import (
	"context"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"time"

	"github.com/eminwux/kukeon/internal/kuketty/setupstatus"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// setupStatusDialTimeout bounds the connect + round-trip to a container's
// kuketty control socket. The pull is a best-effort enrichment of a `kuke get`
// read (see populateRepoStatuses): a slow or wedged kuketty must never stall
// the status read, so the call gives up quickly and leaves Repos empty rather
// than blocking the operator's `kuke get`.
const setupStatusDialTimeout = 2 * time.Second

// pullSetupStatus dials a container's kuketty control socket and invokes the
// GetSetupStatus verb (issue #642), returning the per-repo clone/fetch outcome
// kuketty reported after its pre-Serve step. The socket is the same one
// `kuke attach` connects to; the verb is served over the same JSON-RPC codec,
// so a stdlib net/rpc/jsonrpc client is wire-compatible for this non-FD method
// (sbsh's own pkg/rpcclient uses the same codec for its non-FD verbs).
//
// The whole call is bounded by setupStatusDialTimeout. Callers treat any error
// as "status not available yet" and proceed with an empty Repos — the verb is
// only reachable once kuketty is past Serve (container Ready), and a container
// that exited on a required-repo failure never serves it (AC #5).
func pullSetupStatus(ctx context.Context, socketPath string) ([]setupstatus.Repo, error) {
	dialCtx, cancel := context.WithTimeout(ctx, setupStatusDialTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial kuketty socket %q: %w", socketPath, err)
	}
	defer func() { _ = conn.Close() }()

	// Bound the round-trip on the conn too: DialContext only covers connect,
	// not the Call. A deadline on the conn unblocks a hung read/write.
	if deadline, ok := dialCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	client := rpc.NewClientWithCodec(jsonrpc.NewClientCodec(conn))
	defer func() { _ = client.Close() }()

	var reply setupstatus.Reply
	if callErr := client.Call(setupstatus.Method, setupstatus.Args{}, &reply); callErr != nil {
		return nil, fmt.Errorf("call %s on %q: %w", setupstatus.Method, socketPath, callErr)
	}
	return reply.Repos, nil
}

// setupStatusToInternal maps the wire payload onto the internal RepoStatus
// type the controller persists in ContainerStatus.Repos. Returns nil for an
// empty input so a container with no repos[] reports a nil Repos (mirrors the
// omitempty wire/YAML shape) rather than an empty slice.
func setupStatusToInternal(repos []setupstatus.Repo) []intmodel.RepoStatus {
	if len(repos) == 0 {
		return nil
	}
	out := make([]intmodel.RepoStatus, len(repos))
	for i, r := range repos {
		out[i] = intmodel.RepoStatus{
			Name:   r.Name,
			Target: r.Target,
			State:  r.State,
			Commit: r.Commit,
			Error:  r.Error,
		}
	}
	return out
}
