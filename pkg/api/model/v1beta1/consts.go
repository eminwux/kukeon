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

package v1beta1

const (
	// APIVersionV1Beta1 is the canonical API version for this package.
	APIVersionV1Beta1 Version = "v1beta1"
)

// Kinds.
const (
	// KindCell identifies cell documents.
	KindCell Kind = "Cell"
	// KindContainer identifies container documents.
	KindContainer Kind = "Container"
	// KindRealm identifies realm documents.
	KindRealm Kind = "Realm"
	// KindSpace identifies space documents.
	KindSpace Kind = "Space"
	// KindStack identifies stack documents.
	KindStack Kind = "Stack"
)

// Common printable state strings.
const (
	StatePendingStr  = "Pending"
	StateReadyStr    = "Ready"
	StateFailedStr   = "Failed"
	StateUnknownStr  = "Unknown"
	StateCreatingStr = "Creating"
	StateDeletingStr = "Deleting"
)
