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
	"github.com/eminwux/kukeon/internal/consts"
	"github.com/eminwux/kukeon/internal/util/fs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
)

func TestContainerTTYDir(t *testing.T) {
	got := fs.ContainerTTYDir("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	want := "/opt/kukeon/realm/space/stack/cell/c1/" + consts.KukeonContainerTTYDir
	if got != want {
		t.Errorf("ContainerTTYDir = %q, want %q", got, want)
	}
}

func TestContainerSocketPath(t *testing.T) {
	got := fs.ContainerSocketPath("/opt/kukeon", "realm", "space", "stack", "cell", "c1")
	want := "/opt/kukeon/realm/space/stack/cell/c1/" +
		consts.KukeonContainerTTYDir + "/" + consts.KukeonContainerSocketFile
	if got != want {
		t.Errorf("ContainerSocketPath = %q, want %q", got, want)
	}
}

func TestContainerMetadataDir_AnchorsContainerTTYDir(t *testing.T) {
	// The tty dir (and the socket inside it) must live under the
	// container's metadata dir, not anywhere else — `kuke attach` (#66)
	// and the OCI bind-mount source rely on this being the single source
	// of truth.
	dir := fs.ContainerMetadataDir("/opt/kukeon", "r", "s", "st", "c", "co")
	ttyDir := fs.ContainerTTYDir("/opt/kukeon", "r", "s", "st", "c", "co")
	wantTTY := dir + "/" + consts.KukeonContainerTTYDir
	if ttyDir != wantTTY {
		t.Errorf("tty dir = %q, want it under container metadata dir = %q", ttyDir, wantTTY)
	}
	sock := fs.ContainerSocketPath("/opt/kukeon", "r", "s", "st", "c", "co")
	wantSock := wantTTY + "/" + consts.KukeonContainerSocketFile
	if sock != wantSock {
		t.Errorf("socket path = %q, want it under tty dir = %q", sock, wantSock)
	}
}

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
