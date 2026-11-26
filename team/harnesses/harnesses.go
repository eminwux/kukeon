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

// Package harnesses embeds the definition and image catalog of every harness
// kuke ships: `<name>/harness.yaml` and `<name>/images.yaml`. `kuke team`
// reads harnesses from the team's sources; this copy is what `kuke connector
// login` and the web UI use to start an identity cell without a team (issue
// #1685): the harness a connector authenticates gives the state directory and
// the image.
package harnesses

import "embed"

// FS holds every bundled `<name>/harness.yaml` and `<name>/images.yaml`.
//
//go:embed */harness.yaml */images.yaml
var FS embed.FS
