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

package cgroupinspect

import (
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// Declared reports a container's spec.resources fields with their source.
//
// Space defaults are merged into the persisted container spec at
// create/update time (modelhub.ApplySpaceDefaultsToContainer), and the merge
// copies the whole Resources struct only when the container left it nil. So a
// persisted Resources equal to the space's defaults.container.resources is
// reported as inherited; any other non-nil Resources came from the container
// spec. A container that sets exactly the space default values is
// indistinguishable from one that inherited them and reads as inherited.
func Declared(container, spaceDefault *v1beta1.ContainerResources) []kukeonv1.CgroupDeclaredResource {
	source := kukeonv1.CgroupSourceContainer
	if container != nil && spaceDefault != nil && resourcesEqual(*container, *spaceDefault) {
		source = kukeonv1.CgroupSourceSpaceDefault
	}
	var memory, cpu, pids *int64
	if container != nil {
		memory, cpu, pids = container.MemoryLimitBytes, container.CPUShares, container.PidsLimit
	}
	return []kukeonv1.CgroupDeclaredResource{
		declared("memoryLimitBytes", memory, source),
		declared("cpuShares", cpu, source),
		declared("pidsLimit", pids, source),
	}
}

func declared(field string, value *int64, source string) kukeonv1.CgroupDeclaredResource {
	if value == nil {
		return kukeonv1.CgroupDeclaredResource{Field: field, Source: kukeonv1.CgroupSourceUnset}
	}
	v := *value
	return kukeonv1.CgroupDeclaredResource{Field: field, Value: &v, Source: source}
}

func resourcesEqual(a, b v1beta1.ContainerResources) bool {
	return int64PtrEqual(a.MemoryLimitBytes, b.MemoryLimitBytes) &&
		int64PtrEqual(a.CPUShares, b.CPUShares) &&
		int64PtrEqual(a.PidsLimit, b.PidsLimit)
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
