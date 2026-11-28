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
	"maps"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/connector/scopedefaults"
	"github.com/eminwux/kukeon/internal/controller/runner"
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

// RoleName names the team role a Config's labels and annotations belong to,
// as `<team>/<role>`, or just the role outside a team. Empty without a role.
// A New agent copy has no kukeon.io/team label but runs its template's role:
// its kukeon.io/agent-team annotation names the team (issue #1692).
func RoleName(labels, annotations map[string]string) string {
	role := strings.TrimSpace(labels[ext.LabelRole])
	if role == "" {
		return ""
	}
	team := strings.TrimSpace(labels[ext.LabelTeam])
	if team == "" {
		team = strings.TrimSpace(annotations[ext.AnnotationAgentTeam])
	}
	if team != "" {
		return team + "/" + role
	}
	return role
}

// CellKey names a cell by its scope and name.
type CellKey struct {
	Realm, Space, Stack, Name string
}

// OtherRoleOnStateVolume returns a role, other than own's role, that uses ref
// as its login identity — through a stored Config naming it as its state
// volume, or a cell that picked it (`--connector <connector>=<volume>`, kept
// in the cell's provenance); empty when none does. Roles may share a login
// identity at the operator's risk — the harness state directory also holds
// role content each role rewrites on every start — and this names the other
// role for the warning that says so (issue #1789). Configs and cells without
// a role may share a volume with each other, and one that cannot be read is
// skipped. self is the
// cell being rendered, whose own pick does not count; zero when there is none.
func OtherRoleOnStateVolume(r runner.Runner, ref ext.VolumeRef, own ext.CellConfigMetadata, self CellKey) string {
	ownRole := RoleName(own.Labels, own.Annotations)
	if ownRole == "" || !safeVolumeRef(ref) {
		return ""
	}
	if role := otherConfigRole(r, ref, own, ownRole); role != "" {
		return role
	}
	return otherCellRole(r, ref, ownRole, self)
}

// safeVolumeRef reports whether every coordinate of ref is safe to join into
// the daemon's metadata paths; an unsafe one is refused where it is rendered.
func safeVolumeRef(ref ext.VolumeRef) bool {
	for _, seg := range []string{ref.Name, ref.Realm, ref.Space, ref.Stack} {
		if !scopedefaults.SafePathSegment(seg) {
			return false
		}
	}
	return true
}

// otherConfigRole is the role of a stored Config, other than own, that names
// ref as its state volume and is not ownRole.
func otherConfigRole(r runner.Runner, ref ext.VolumeRef, own ext.CellConfigMetadata, ownRole string) string {
	configs, err := r.ListConfigs(ref.Realm, "", "")
	if err != nil {
		return ""
	}
	for _, md := range configs {
		if md.Metadata.Name == own.Name && md.Metadata.Realm == own.Realm &&
			md.Metadata.Space == own.Space && md.Metadata.Stack == own.Stack {
			continue
		}
		doc, ok := readConfig(r, md.Metadata)
		if !ok {
			continue
		}
		role := RoleName(doc.Metadata.Labels, doc.Metadata.Annotations)
		if role == "" || role == ownRole {
			continue
		}
		if other := StateVolumeRef(doc.Spec.Values); other != nil && *other == ref {
			return role
		}
	}
	return ""
}

// otherCellRole is the role of a cell, other than self, that picked ref as
// its login identity and whose Config is not of ownRole. A cell's role is its
// Config's: cells carry no role label.
func otherCellRole(r runner.Runner, ref ext.VolumeRef, ownRole string, self CellKey) string {
	cells, err := r.ListCells(ref.Realm, "", "")
	if err != nil {
		return ""
	}
	want := intmodel.VolumeRef{Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack}
	for _, cell := range cells {
		key := CellKey{
			Realm: cell.Spec.RealmName, Space: cell.Spec.SpaceName,
			Stack: cell.Spec.StackName, Name: cell.Metadata.Name,
		}
		prov := cell.Spec.Provenance
		if key == self || prov == nil || !slices.Contains(slices.Collect(maps.Values(prov.ConnectorVolumes)), want) {
			continue
		}
		md, ok := cellConfigRef(cell)
		if !ok {
			continue
		}
		doc, ok := readConfig(r, md)
		if !ok {
			continue
		}
		if role := RoleName(doc.Metadata.Labels, doc.Metadata.Annotations); role != "" && role != ownRole {
			return role
		}
	}
	return ""
}

// cellConfigRef is the Config a cell was rendered from, from its provenance;
// an empty binding realm is the cell's own.
func cellConfigRef(cell intmodel.Cell) (intmodel.CellConfigMetadata, bool) {
	prov := cell.Spec.Provenance
	if prov == nil || prov.BindingKind != ext.BindingKindConfig || strings.TrimSpace(prov.BindingRef.Name) == "" {
		return intmodel.CellConfigMetadata{}, false
	}
	realm := strings.TrimSpace(prov.BindingRef.Realm)
	if realm == "" {
		realm = cell.Spec.RealmName
	}
	return intmodel.CellConfigMetadata{
		Name: strings.TrimSpace(prov.BindingRef.Name), Realm: realm,
		Space: strings.TrimSpace(prov.BindingRef.Space), Stack: strings.TrimSpace(prov.BindingRef.Stack),
	}, true
}

func readConfig(r runner.Runner, md intmodel.CellConfigMetadata) (ext.CellConfigDoc, bool) {
	cfg, err := r.GetConfig(intmodel.CellConfig{Metadata: md})
	if err != nil {
		return ext.CellConfigDoc{}, false
	}
	doc, err := apischeme.ConvertCellConfigToExternal(cfg)
	if err != nil {
		return ext.CellConfigDoc{}, false
	}
	return doc, true
}

// LoginIdentityRoleWarning warns when a Config binds, through a method that
// keeps its login in the state volume, a login identity a Config or cell of
// another role already uses; empty when none does. Sharing one is allowed at
// the operator's risk (issue #1789): the harness keeps role content in its
// state directory, so each role's start rewrites it, and a Codex refresh token
// is single-use, so one role's refresh can sign the other out.
func LoginIdentityRoleWarning(r runner.Runner, cfgDoc ext.CellConfigDoc, bpDoc ext.CellBlueprintDoc) string {
	ref := StateVolumeRef(cfgDoc.Spec.Values)
	if ref == nil || RoleName(cfgDoc.Metadata.Labels, cfgDoc.Metadata.Annotations) == "" {
		return ""
	}
	keepsLogin := slices.ContainsFunc(bpDoc.Spec.Components, func(comp ext.BlueprintComponent) bool {
		return comp.Connector != nil && comp.Connector.Delivery == ext.ConnectorDeliveryStateVolume &&
			(!comp.Optional || slices.Contains(cfgDoc.Spec.Components, comp.Name))
	})
	if !keepsLogin {
		return ""
	}
	other := OtherRoleOnStateVolume(r, *ref, cfgDoc.Metadata, CellKey{})
	if other == "" {
		return ""
	}
	return fmt.Sprintf(
		"config %q: %s",
		cfgDoc.Metadata.Name,
		SharedLoginIdentityWarning(
			fmt.Sprintf("%q", ref.Name),
			other,
			RoleName(cfgDoc.Metadata.Labels, cfgDoc.Metadata.Annotations),
		),
	)
}

// SharedLoginIdentityWarning says that role own uses volume, the login
// identity of role other, and what sharing it risks.
func SharedLoginIdentityWarning(volume, other, own string) string {
	return fmt.Sprintf(
		"volume %s is also the login identity of role %s; role %s shares it at the operator's risk: "+
			"each role's start rewrites its role content in the harness state directory, "+
			"and a single-use refresh token (Codex) refreshed by one role signs the other out",
		volume, other, own)
}
