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

// Package kukeworkspace contains the user-owned Workspace manifest model.
// Workspace manifests describe agent sources, projects, project teams, and
// workspace-scoped cells. They contain credential references, never secret
// values.
package kukeworkspace

// APIVersionV1 is the Workspace manifest API version.
const APIVersionV1 = "kukeworkspace.io/v1"

// KindWorkspace identifies a Workspace manifest.
const KindWorkspace = "Workspace"
