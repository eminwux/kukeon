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

package webserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"gopkg.in/yaml.v3"
)

// Env masks (issue #1768). They have fixed lengths, never the value's: an env
// value shows only whether it is the same on both sides of the diff.
const (
	envMask        = "++++++++"
	envMaskChanged = "++++++++++++"
)

// cellDriftResponse is the body of GET .../cells/{name}/drift: the stored cell
// and the cell its lineage Config renders to now, as YAML with env values
// masked, plus the drift check's reasons.
type cellDriftResponse struct {
	Live string `json:"live"`
	// Desired is empty when the lineage Config was deleted.
	Desired     string   `json:"desired,omitempty"`
	ConfigFound bool     `json:"configFound"`
	Reasons     []string `json:"reasons"`
	Paths       []string `json:"paths"`
	ChangeType  string   `json:"changeType,omitempty"`
}

// driftManifest is the part of a cell document the diff shows: the status is
// not part of what a Config renders.
type driftManifest struct {
	APIVersion v1beta1.Version      `yaml:"apiVersion"`
	Kind       v1beta1.Kind         `yaml:"kind"`
	Metadata   v1beta1.CellMetadata `yaml:"metadata"`
	Spec       v1beta1.CellSpec     `yaml:"spec"`
}

// cellDrift reads the drift diff of one cell for the browser.
func cellDrift(ctx context.Context, c kukeonv1.Client, r *http.Request) (any, error) {
	result, err := c.DiffCellFromConfig(ctx, v1beta1.CellDoc{
		Metadata: v1beta1.CellMetadata{Name: r.PathValue("name")},
		Spec: v1beta1.CellSpec{
			RealmID: r.PathValue("realm"),
			SpaceID: r.PathValue("space"),
			StackID: r.PathValue("stack"),
		},
	})
	if err != nil {
		return nil, err
	}

	live := redactDriftDoc(result.Live)
	var desired *v1beta1.CellDoc
	if result.Desired != nil {
		doc := redactDriftDoc(*result.Desired)
		desired = &doc
	}
	maskEnvPair(&live, desired)

	response := cellDriftResponse{
		ConfigFound: result.ConfigFound,
		Reasons:     nonNil(result.Reasons),
		Paths:       nonNil(result.Paths),
		ChangeType:  result.ChangeType,
	}
	if response.Live, err = manifestYAML(live); err != nil {
		return nil, err
	}
	if desired != nil {
		if response.Desired, err = manifestYAML(*desired); err != nil {
			return nil, err
		}
	}
	return response, nil
}

// redactDriftDoc copies a document so masking never writes into the client's
// data, and drops what the browser never gets (redactCell): runtime env and
// provenance. Env stays for maskEnvPair to mask.
func redactDriftDoc(doc v1beta1.CellDoc) v1beta1.CellDoc {
	doc.Spec.Containers = append([]v1beta1.ContainerSpec(nil), doc.Spec.Containers...)
	for i := range doc.Spec.Containers {
		doc.Spec.Containers[i].Env = append([]string(nil), doc.Spec.Containers[i].Env...)
	}
	doc.Spec.RuntimeEnv = nil
	doc.Spec.Provenance = nil
	doc.Status = v1beta1.CellStatus{}
	return doc
}

// maskEnvPair replaces every env value in both documents with a mask, pairing
// containers by ID: an entry whose key holds the same value on both sides
// gets the same mask on both, a key whose value differs gets envMask on the
// live side and envMaskChanged on the desired side, and a key on one side
// only, an entry with no "=", or any entry of a container on one side only
// gets envMask. desired may be nil (the lineage Config was deleted).
func maskEnvPair(live, desired *v1beta1.CellDoc) {
	desiredEnv := map[string][]string{}
	if desired != nil {
		for _, c := range desired.Spec.Containers {
			desiredEnv[c.ID] = c.Env
		}
	}
	liveEnv := map[string][]string{}
	for _, c := range live.Spec.Containers {
		liveEnv[c.ID] = c.Env
	}

	for i := range live.Spec.Containers {
		c := &live.Spec.Containers[i]
		other, paired := desiredEnv[c.ID]
		c.Env = maskEnv(c.Env, other, paired, false)
	}
	if desired == nil {
		return
	}
	for i := range desired.Spec.Containers {
		c := &desired.Spec.Containers[i]
		other, paired := liveEnv[c.ID]
		c.Env = maskEnv(c.Env, other, paired, true)
	}
}

// maskEnv masks one container's env against the same container's env on the
// other side; paired is false when the other side has no such container.
// changedSide picks the longer mask for a value that differs.
func maskEnv(env, other []string, paired, changedSide bool) []string {
	if env == nil {
		return nil
	}
	otherValues := map[string]string{}
	for _, entry := range other {
		if key, value, ok := strings.Cut(entry, "="); ok {
			otherValues[key] = value
		}
	}
	masked := make([]string, len(env))
	for i, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			masked[i] = envMask
			continue
		}
		mask := envMask
		if otherValue, found := otherValues[key]; paired && found && otherValue != value && changedSide {
			mask = envMaskChanged
		}
		masked[i] = key + "=" + mask
	}
	return masked
}

// manifestYAML serializes the manifest part of a cell document.
func manifestYAML(doc v1beta1.CellDoc) (string, error) {
	raw, err := yaml.Marshal(driftManifest{
		APIVersion: doc.APIVersion,
		Kind:       doc.Kind,
		Metadata:   doc.Metadata,
		Spec:       doc.Spec,
	})
	if err != nil {
		return "", fmt.Errorf("serialize cell manifest: %w", err)
	}
	return string(raw), nil
}
