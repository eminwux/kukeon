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

package modelhub

// Team is the internal carrier of a stored team declaration (kind: Team,
// issue #1618). Like a CellConfig the daemon keeps the validated document as
// opaque bytes: it writes them under the realm and reads them back. The
// realm is lifted onto the metadata so storage can resolve the path without
// parsing the body.
type Team struct {
	Metadata TeamMetadata
	Document []byte
}

// TeamMetadata identifies a Team, or a TeamDefaults, by name and realm.
type TeamMetadata struct {
	Name  string
	Realm string
}

// TeamDefaults is the internal carrier of a realm's stored team defaults
// (kind: TeamDefaults, issue #1618), stored like a Team.
type TeamDefaults struct {
	Metadata TeamMetadata
	Document []byte
}
