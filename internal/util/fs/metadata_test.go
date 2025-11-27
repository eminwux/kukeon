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

package fs_test

import (
	"encoding/json"
	"testing"

	"github.com/eminwux/kukeon/internal/apischeme"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestDetectMetadataVersion(t *testing.T) {
	tests := []struct {
		name        string
		raw         []byte
		wantVersion v1beta1.Version
		wantErr     bool
	}{
		{
			name: "valid v1beta1 metadata",
			raw: func() []byte {
				doc := map[string]interface{}{
					"apiVersion": "v1beta1",
					"kind":       "Realm",
				}
				data, _ := json.Marshal(doc)
				return data
			}(),
			wantVersion: v1beta1.Version("v1beta1"), // DefaultVersion returns as-is when not empty
			wantErr:     false,
		},
		{
			name: "valid kukeon/v1beta1 metadata",
			raw: func() []byte {
				doc := map[string]interface{}{
					"apiVersion": "kukeon/v1beta1",
					"kind":       "Realm",
				}
				data, _ := json.Marshal(doc)
				return data
			}(),
			wantVersion: v1beta1.APIVersionV1Beta1,
			wantErr:     false,
		},
		{
			name: "empty apiVersion defaults to v1beta1",
			raw: func() []byte {
				doc := map[string]interface{}{
					"apiVersion": "",
					"kind":       "Realm",
				}
				data, _ := json.Marshal(doc)
				return data
			}(),
			wantVersion: apischeme.VersionV1Beta1,
			wantErr:     false,
		},
		{
			name:    "invalid JSON",
			raw:     []byte("{invalid json}"),
			wantErr: true,
		},
		{
			name:    "empty bytes",
			raw:     []byte(""),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, err := fs.DetectMetadataVersion(tt.raw)
			if (err != nil) != tt.wantErr {
				t.Errorf("DetectMetadataVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && version != tt.wantVersion {
				t.Errorf("DetectMetadataVersion() version = %v, want %v", version, tt.wantVersion)
			}
		})
	}
}
