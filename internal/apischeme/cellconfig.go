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

package apischeme

import (
	"fmt"
	"time"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	ext "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// ConvertCellConfigDocToInternal converts an external CellConfigDoc to the
// internal hub carrier (issue #624). The body is serialized verbatim into the
// carrier's Document — the daemon stores and round-trips it without
// interpreting it — while the scope coordinates are lifted onto the metadata so
// the storage runner can resolve the on-disk path. The apiVersion/kind are
// normalized on the way in so the stored document is always canonical.
func ConvertCellConfigDocToInternal(in ext.CellConfigDoc) (intmodel.CellConfig, error) {
	switch in.APIVersion {
	case VersionV1Beta1, "": // default/empty treated as v1beta1
		canonical := in
		canonical.APIVersion = ext.APIVersionV1Beta1
		canonical.Kind = ext.KindCellConfig
		// Status is daemon-owned: a document that carries one (the output of
		// `kuke get config -o yaml`, re-applied) is stored without it.
		canonical.Status = nil
		document, err := yaml.Marshal(canonical)
		if err != nil {
			return intmodel.CellConfig{}, fmt.Errorf("marshal CellConfig document: %w", err)
		}
		return intmodel.CellConfig{
			Metadata: intmodel.CellConfigMetadata{
				Name:        in.Metadata.Name,
				Realm:       in.Metadata.Realm,
				Space:       in.Metadata.Space,
				Stack:       in.Metadata.Stack,
				Labels:      copyLabels(in.Metadata.Labels),
				Annotations: copyLabels(in.Metadata.Annotations),
			},
			Document: document,
			Schedule: scheduleSummary(in.Spec.Schedule),
		}, nil
	default:
		return intmodel.CellConfig{}, fmt.Errorf("unsupported apiVersion for CellConfig: %s", in.APIVersion)
	}
}

// NormalizeCellConfig takes an external CellConfigDoc request and returns an
// internal carrier and the chosen apiVersion.
func NormalizeCellConfig(req ext.CellConfigDoc) (intmodel.CellConfig, ext.Version, error) {
	version := DefaultVersion(req.APIVersion)
	internal, err := ConvertCellConfigDocToInternal(req)
	if err != nil {
		return intmodel.CellConfig{}, "", err
	}
	return internal, version, nil
}

// ConvertCellConfigToExternal reconstructs the external CellConfigDoc from the
// internal carrier's stored Document (issue #624). It is the inverse of
// ConvertCellConfigDocToInternal, used by ReconcileConfig to validate slot fills
// against the referenced blueprint and (in #625) by `kuke run -c`.
func ConvertCellConfigToExternal(in intmodel.CellConfig) (ext.CellConfigDoc, error) {
	var doc ext.CellConfigDoc
	if err := yaml.Unmarshal(in.Document, &doc); err != nil {
		return ext.CellConfigDoc{}, fmt.Errorf("unmarshal CellConfig document: %w", err)
	}
	doc.Status = configStatusToExternal(in)
	return doc, nil
}

// scheduleSummary lifts a document's spec.schedule onto the carrier.
func scheduleSummary(in *ext.CellConfigSchedule) *intmodel.CellConfigSchedule {
	if in == nil {
		return nil
	}
	return &intmodel.CellConfigSchedule{Cron: in.Cron, Timezone: in.Timezone, Suspend: in.Suspend}
}

// configStatusToExternal builds the external status from the carrier's stored
// schedule state and the connector resolution the controller computed; nil
// when there is neither, so a Config with nothing to report renders no status
// block.
func configStatusToExternal(cfg intmodel.CellConfig) *ext.CellConfigStatus {
	in := cfg.ScheduleStatus
	if in == nil && len(cfg.Connectors) == 0 {
		return nil
	}
	out := &ext.CellConfigStatus{Connectors: cloneConnectorStatuses(cfg.Connectors)}
	if in != nil {
		out.Schedule = &ext.CellConfigScheduleStatus{
			LastScheduleTime: copyTime(in.LastScheduleTime),
			NextScheduleTime: copyTime(in.NextScheduleTime),
			LastCell:         in.LastCell,
			ActiveCells:      append([]string(nil), in.ActiveCells...),
			SkippedFires:     in.SkippedFires,
			LastError:        in.LastError,
		}
	}
	return out
}

// cloneConnectorStatuses deep-copies the connector statuses of a Config.
func cloneConnectorStatuses(in []ext.ConnectorStatus) []ext.ConnectorStatus {
	if len(in) == 0 {
		return nil
	}
	out := make([]ext.ConnectorStatus, len(in))
	for i, status := range in {
		out[i] = status
		if status.Identity != nil {
			identity := *status.Identity
			out[i].Identity = &identity
		}
		if status.VolumeRef != nil {
			ref := *status.VolumeRef
			out[i].VolumeRef = &ref
		}
	}
	return out
}

func copyTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// ConvertCellConfigMetadataToExternal builds a metadata-only external
// CellConfigDoc from the internal carrier's metadata (issue #644). Unlike
// ConvertCellConfigToExternal it does not read the stored Document — the list
// and delete verbs surface scope coordinates, name, labels and annotations
// only, never the config body (blueprint ref, values, slot fills). The one exception is the schedule
// (#1503): the carrier's lifted spec.schedule summary and stored schedule
// state come along, so a list can show a Config's cadence and next fire.
func ConvertCellConfigMetadataToExternal(in intmodel.CellConfig) ext.CellConfigDoc {
	doc := ext.CellConfigDoc{
		APIVersion: ext.APIVersionV1Beta1,
		Kind:       ext.KindCellConfig,
		Metadata: ext.CellConfigMetadata{
			Name:        in.Metadata.Name,
			Realm:       in.Metadata.Realm,
			Space:       in.Metadata.Space,
			Stack:       in.Metadata.Stack,
			Labels:      copyLabels(in.Metadata.Labels),
			Annotations: copyLabels(in.Metadata.Annotations),
		},
		Status: configStatusToExternal(in),
	}
	if in.Schedule != nil {
		doc.Spec.Schedule = &ext.CellConfigSchedule{
			Cron:     in.Schedule.Cron,
			Timezone: in.Schedule.Timezone,
			Suspend:  in.Schedule.Suspend,
		}
	}
	return doc
}

// ConvertCellConfigListToExternal maps a slice of internal CellConfigs to
// metadata-only external CellConfigDocs (issue #644).
func ConvertCellConfigListToExternal(in []intmodel.CellConfig) []ext.CellConfigDoc {
	if in == nil {
		return nil
	}
	out := make([]ext.CellConfigDoc, len(in))
	for i := range in {
		out[i] = ConvertCellConfigMetadataToExternal(in[i])
	}
	return out
}
