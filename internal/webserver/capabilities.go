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

package webserver

import (
	"net/http"

	"github.com/eminwux/kukeon/cmd/config"
)

// capabilityFeatures are the stable identifiers of the API areas this web
// server serves (#1754). A console extension names the ones it requires, and
// the console hides it on a host that does not list them. An identifier never
// changes meaning: an incompatible change to an area ships as a new version
// (attach.v2) next to the old one, and an area is only ever added here.
func capabilityFeatures() []string {
	return []string{
		"resources.v1",  // realm, space, stack, cell and container reads, create and lifecycle actions
		"catalog.v1",    // blueprints, configs, volumes and secrets
		"images.v1",     // realm images, the image inventory and registry credentials
		"connectors.v1", // the connector catalog, identities and defaults
		"teams.v1",      // stored teams, the team form and team actions
		"sources.v1",    // stored Sources
		"agents.v1",     // agent templates and New agent
		"attach.v1",     // the attach WebSocket, screenshots and terminal activity
		"exec.v1",       // exec in a container
		"health.v1",     // majordomo's health reports and sweeps
		"daemon.v1",     // the daemon connectivity check and its logs
	}
}

// capabilities is the body of GET /api/capabilities.
type capabilities struct {
	Version  string   `json:"version"`
	Features []string `json:"features"`
}

func registerCapabilitiesRoute(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/capabilities", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, capabilities{Version: config.Version, Features: capabilityFeatures()})
	})
}
