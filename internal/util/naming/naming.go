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
	"fmt"
	"strings"

	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// BuildSpaceNetworkName constructs the canonical network name for a space.
func BuildSpaceNetworkName(doc *v1beta1.SpaceDoc) (string, error) {
	if doc == nil {
		return "", errdefs.ErrSpaceDocRequired
	}
	spaceName := strings.TrimSpace(doc.Metadata.Name)
	if spaceName == "" {
		return "", errdefs.ErrSpaceNameRequired
	}
	realmName := strings.TrimSpace(doc.Spec.RealmID)
	if realmName == "" && doc.Metadata.Labels != nil {
		if realmLabel, ok := doc.Metadata.Labels[consts.KukeonRealmLabelKey]; ok &&
			strings.TrimSpace(realmLabel) != "" {
			realmName = strings.TrimSpace(realmLabel)
		}
	}
	if realmName == "" {
		return "", errdefs.ErrRealmNameRequired
	}
	return fmt.Sprintf("%s-%s", realmName, spaceName), nil
}

// BuildContainerName constructs the canonical container name for a container.
// The format is: containerID-realmID-spaceID-cellName
// This can be used for any container ID (e.g., "pause", "debian").
func BuildContainerName(realmID, spaceID, cellName, containerID string) string {
	return fmt.Sprintf("%s-%s-%s-%s", realmID, spaceID, cellName, containerID)
}
