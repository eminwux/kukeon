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

package apply

import (
	"fmt"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/controller/runner"
	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

// The harness state volume of a Config (issue #1685). A Config names the
// Volume its harness keeps its state — and, for a login method, its login —
// in with four values, which the shared blueprint mounts through a
// fully-qualified volumeRef.

// StateVolumeValue is the Config value naming the state volume.
const StateVolumeValue = "STATE_VOLUME"

// StateVolumeScopeValues are the Config values naming the realm, space and
// stack of the state volume.
//
//nolint:gochecknoglobals // read-only list of value names.
var StateVolumeScopeValues = [3]string{"STATE_VOLUME_REALM", "STATE_VOLUME_SPACE", "STATE_VOLUME_STACK"}

// StateVolumeRef is the state volume a set of Config values names, with its
// scope, or nil when they name no volume, no realm or an incomplete scope (a
// stack without a space).
func StateVolumeRef(values map[string]string) *ext.VolumeRef {
	name := strings.TrimSpace(values[StateVolumeValue])
	var scope [3]string
	for i, key := range StateVolumeScopeValues {
		scope[i] = strings.TrimSpace(values[key])
	}
	if name == "" || scope[0] == "" || (scope[2] != "" && scope[1] == "") {
		return nil
	}
	return &ext.VolumeRef{Name: name, Realm: scope[0], Space: scope[1], Stack: scope[2]}
}

// RoleName names the team role a set of Config labels belongs to, as
// `<team>/<role>`, or just the role outside a team. Empty without a role.
func RoleName(labels map[string]string) string {
	role := strings.TrimSpace(labels[ext.LabelRole])
	if role == "" {
		return ""
	}
	if team := strings.TrimSpace(labels[ext.LabelTeam]); team != "" {
		return team + "/" + role
	}
	return role
}

// OtherRoleOnStateVolume returns the role of a stored Config, other than
// own's role, that names ref as its state volume; empty when none does. One
// login identity serves one role: the harness state directory also holds role
// content the role rewrites on every start. Configs without a role label may
// share a volume with each other, and a Config that cannot be read is skipped.
func OtherRoleOnStateVolume(r runner.Runner, ref ext.VolumeRef, own ext.CellConfigMetadata) string {
	ownRole := RoleName(own.Labels)
	if ownRole == "" {
		return ""
	}
	configs, err := r.ListConfigs(ref.Realm, "", "")
	if err != nil {
		return ""
	}
	for _, md := range configs {
		if md.Metadata.Name == own.Name && md.Metadata.Realm == own.Realm &&
			md.Metadata.Space == own.Space && md.Metadata.Stack == own.Stack {
			continue
		}
		cfg, getErr := r.GetConfig(intmodel.CellConfig{Metadata: md.Metadata})
		if getErr != nil {
			continue
		}
		doc, convErr := apischeme.ConvertCellConfigToExternal(cfg)
		if convErr != nil {
			continue
		}
		role := RoleName(doc.Metadata.Labels)
		if role == "" || role == ownRole {
			continue
		}
		if other := StateVolumeRef(doc.Spec.Values); other != nil && *other == ref {
			return role
		}
	}
	return ""
}

// validateLoginIdentityRole refuses to store a Config that binds, through a
// method that keeps its login in the state volume, a login identity Configs of
// another role already use. Refusing the bind — rather than every later render
// — keeps the role that already uses the identity working.
func validateLoginIdentityRole(r runner.Runner, cfgDoc ext.CellConfigDoc, bpDoc ext.CellBlueprintDoc) error {
	ref := StateVolumeRef(cfgDoc.Spec.Values)
	if ref == nil || RoleName(cfgDoc.Metadata.Labels) == "" {
		return nil
	}
	keepsLogin := slices.ContainsFunc(bpDoc.Spec.Components, func(comp ext.BlueprintComponent) bool {
		return comp.Connector != nil && comp.Connector.Delivery == ext.ConnectorDeliveryStateVolume &&
			(!comp.Optional || slices.Contains(cfgDoc.Spec.Components, comp.Name))
	})
	if !keepsLogin {
		return nil
	}
	if other := OtherRoleOnStateVolume(r, *ref, cfgDoc.Metadata); other != "" {
		return fmt.Errorf(
			"%w: config %q: volume %q is the login identity of role %s; role %s cannot share it "+
				"(the harness keeps role content in its state directory)",
			errdefs.ErrVolumeIdentity, cfgDoc.Metadata.Name, ref.Name, other, RoleName(cfgDoc.Metadata.Labels))
	}
	return nil
}
