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

// Package connectors embeds the connector catalog kuke ships: one
// `<name>/connector.yaml` per kind of access a cell can need (a harness login,
// an API token, a git identity, a registry credential). `kuke team`, the
// `kuke create` commands and the web UI all read this catalog and nothing
// else, so adding a connector is adding one file here — see
// docs/site/concepts/connectors.md.
package connectors

import "embed"

// FS holds every bundled `<name>/connector.yaml`.
//
//go:embed */connector.yaml
var FS embed.FS
