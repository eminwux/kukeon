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
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func DefaultRealmSpec(doc *v1beta1.RealmDoc) ctr.CgroupSpec {
	group := fmt.Sprintf("%s/%s", consts.KukeonCgroupRoot, doc.Metadata.Name)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}

func DefaultSpaceSpec(realm *v1beta1.RealmDoc, space *v1beta1.SpaceDoc) ctr.CgroupSpec {
	group := fmt.Sprintf("%s/%s/%s", consts.KukeonCgroupRoot, realm.Metadata.Name, space.Metadata.Name)
	return ctr.CgroupSpec{
		Group: group,
		Resources: ctr.CgroupResources{
			CPU:    nil,
			Memory: nil,
			IO:     nil,
		},
	}
}
