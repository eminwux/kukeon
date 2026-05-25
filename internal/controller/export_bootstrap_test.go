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

package controller

import v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"

// BootstrapCellForTest invokes the unexported bootstrapCell on the system
// kukeond cell path so external tests can drive the image-drift branch
// without running the entire Bootstrap pipeline. Issue #868.
func (b *Exec) BootstrapCellForTest(section *CellSection, doc *v1beta1.CellDoc) error {
	return b.bootstrapCell(section, doc)
}
