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

package naming

import (
	"crypto/rand"
	"encoding/hex"
)

// DefaultCellNameSuffixBytes is the canonical entropy width for the
// `<prefix>-<6hex>` suffix appended to generated cell names by `kuke run -b`,
// `-p`, and `-c -g`. 3 bytes → 6 lowercase hex chars; matches K8s
// `generateName`'s suffix shape closely enough that the names read familiar
// at a glance.
const DefaultCellNameSuffixBytes = 3

// RandomHexSuffix returns n cryptographically-random bytes hex-encoded as a
// lowercase string of length 2n, suitable as a name suffix. Callers wrap any
// error with their own context (which document the suffix is for).
func RandomHexSuffix(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
