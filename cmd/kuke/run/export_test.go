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

package run

import (
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// RunFn is the test-visible alias of the unexported runFn type. Tests build
// values of this type and store them under MockRunKey to bypass the real
// pkg/attach.Run, which would open the user's TTY and connect to a real
// control socket.
type RunFn = runFn

// PickAttachTarget exports the package-private selection helper for tests.
// Production code reaches the same path through runRun → attachAfterRun.
func PickAttachTarget(spec v1beta1.CellSpec, cellName, explicit string) (string, error) {
	return pickAttachTarget(spec, cellName, explicit)
}
