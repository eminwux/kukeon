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

package cgroups

import (
	"fmt"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func DefaultRealmSpec(realm intmodel.Realm) ctr.CgroupSpec {
	group := fmt.Sprintf("%s/%s", consts.KukeonCgroupRoot, realm.Metadata.Name)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}

func DefaultSpaceSpec(space intmodel.Space) ctr.CgroupSpec {
	group := fmt.Sprintf("%s/%s/%s", consts.KukeonCgroupRoot, space.Spec.RealmName, space.Metadata.Name)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}

func DefaultStackSpec(stack intmodel.Stack) ctr.CgroupSpec {
	group := fmt.Sprintf(
		"%s/%s/%s/%s",
		consts.KukeonCgroupRoot,
		stack.Spec.RealmName,
		stack.Spec.SpaceName,
		stack.Metadata.Name,
	)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}

func DefaultCellSpec(cell intmodel.Cell) ctr.CgroupSpec {
	group := fmt.Sprintf(
		"%s/%s/%s/%s/%s",
		consts.KukeonCgroupRoot,
		cell.Spec.RealmName,
		cell.Spec.SpaceName,
		cell.Spec.StackName,
		cell.Metadata.Name,
	)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}
