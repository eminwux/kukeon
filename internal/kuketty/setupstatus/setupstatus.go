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

// Package setupstatus is the wire contract for the GetSetupStatus RPC verb
// (issue #642, phase 1b). kuketty registers the verb as a custom JSON-RPC
// service on its sbsh control socket — the same socket the daemon already
// dials for `kuke attach` — and reports the per-repo outcome of its pre-Serve
// clone/fetch step (issue #617). kukeond pulls the result post-Serve and
// writes it into ContainerStatus.Repos, surfaced in `kuke get`.
//
// The package is deliberately leaf-level (only the standard library) so it can
// be imported by both the kuketty wrapper (whose import closure must stay
// small — see cmd/kuketty/main.go) and the daemon-side controller without
// dragging in either side's heavy dependency set.
//
// ContainerStatus is the single source of truth for setup status: there is no
// status file in the container. The earlier file-based first cut
// (repos-status.json read back through the tty-dir bind mount) is not used.
package setupstatus

// ServiceName is the net/rpc service name kuketty registers on the sbsh
// control server via server.WithHandlers. A client invokes the verb with the
// wire method "<ServiceName>.<MethodName>" — see Method.
const ServiceName = "Setup"

// MethodName is the exported method on the registered receiver.
const MethodName = "GetSetupStatus"

// Method is the full "Service.Method" string the daemon-side JSON-RPC client
// passes to (*rpc.Client).Call. Kept in one place so the producer
// (registration) and consumer (call) cannot drift.
const Method = ServiceName + "." + MethodName

// Args is the (empty) request payload for GetSetupStatus. The verb takes no
// parameters — kuketty already knows its own repos[] outcome. An exported
// struct (rather than a bare type) keeps the net/rpc signature scheme happy
// and leaves room for future request fields without a wire break.
type Args struct{}

// Reply is the GetSetupStatus response: the per-repo outcome of kuketty's
// pre-Serve clone/fetch step, in the order the repos were declared on the
// container spec. An empty Repos means the container declared no repos[].
type Reply struct {
	Repos []Repo `json:"repos"`
}

// Repo is the resolved state of a single ContainerRepo after kuketty's
// pre-Serve step. Field set mirrors api/model/v1beta1.RepoStatus so the
// daemon can map it straight into ContainerStatus.Repos.
type Repo struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	// State is one of StateCloned, StateFetched, or StateFailed.
	State string `json:"state"`
	// Commit is the resolved HEAD commit (full SHA) on success; empty on
	// failure.
	Commit string `json:"commit,omitempty"`
	// Error is the failure detail when State == StateFailed; empty otherwise.
	Error string `json:"error,omitempty"`
}

// Repo state values. These match the strings phase 1a already logs and the
// values api/model/v1beta1.RepoStatus.State documents.
const (
	// StateCloned means the target had no .git and was freshly cloned.
	StateCloned = "cloned"
	// StateFetched means the target already had a checkout and was
	// fetched + fast-forwarded.
	StateFetched = "fetched"
	// StateFailed means the clone/fetch failed; Error carries the detail.
	StateFailed = "failed"
)
