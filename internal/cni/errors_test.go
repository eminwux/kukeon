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

package cni

import (
	"errors"
	"strings"
	"testing"

	libcni "github.com/containernetworking/cni/libcni"
	"github.com/eminwux/kukeon/internal/errdefs"
)

func TestTranslateCNIError(t *testing.T) {
	// The real-world error from the gaps doc — plugin output concatenated by libcni.
	rangeErr := errors.New(`plugin type="bridge" failed (add): failed to create bridge "kuke-system-kukeon": could not add "kuke-system-kukeon": numerical result out of range`)

	tests := []struct {
		name         string
		err          error
		networkName  string
		bridge       string
		wantSentinel error
		wantSubstr   string
		// nil means: output == input (passthrough).
		passthrough bool
	}{
		{
			name: "nil error passes through",
			err:  nil,
		},
		{
			name:         "ERANGE + oversized bridge → ErrBridgeNameTooLong",
			err:          rangeErr,
			networkName:  "kuke-system-kukeon",
			bridge:       "kuke-system-kukeon", // 18 chars
			wantSentinel: errdefs.ErrBridgeNameTooLong,
			wantSubstr:   "IFNAMSIZ",
		},
		{
			name:        "ERANGE but short bridge → passthrough (unrelated cause)",
			err:         rangeErr,
			networkName: "net",
			bridge:      "cni0",
			passthrough: true,
		},
		{
			name:         "missing plugin binary → ErrCNIPluginNotFound",
			err:          errors.New(`failed to find plugin "bridge" in path [/opt/cni/bin]`),
			networkName:  "net",
			bridge:       "cni0",
			wantSentinel: errdefs.ErrCNIPluginNotFound,
			wantSubstr:   "containernetworking-plugins",
		},
		{
			name:         "exec not found → ErrCNIPluginNotFound",
			err:          errors.New(`fork/exec /opt/cni/bin/bridge: executable file not found in $PATH`),
			networkName:  "net",
			bridge:       "cni0",
			wantSentinel: errdefs.ErrCNIPluginNotFound,
			wantSubstr:   "CNI plugin",
		},
		{
			name:        "unrelated error → passthrough",
			err:         errors.New("connection refused"),
			networkName: "net",
			bridge:      "cni0",
			passthrough: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := translateCNIError(tt.err, tt.networkName, tt.bridge)

			if tt.err == nil {
				if got != nil {
					t.Fatalf("translateCNIError(nil) = %v, want nil", got)
				}
				return
			}

			if tt.passthrough {
				if got != tt.err {
					t.Fatalf("passthrough expected: got %v, want identical to input %v", got, tt.err)
				}
				return
			}

			if !errors.Is(got, tt.wantSentinel) {
				t.Errorf("sentinel: got %v, want errors.Is(_, %v) == true", got, tt.wantSentinel)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("original error not preserved via %%w: %v", got)
			}
			if tt.wantSubstr != "" && !strings.Contains(got.Error(), tt.wantSubstr) {
				t.Errorf("message %q missing substring %q", got.Error(), tt.wantSubstr)
			}
		})
	}
}

func TestBridgeNameFromNetConf(t *testing.T) {
	tests := []struct {
		name string
		conf *libcni.NetworkConfigList
		want string
	}{
		{
			name: "nil conflist",
			conf: nil,
			want: "",
		},
		{
			name: "bridge plugin present",
			conf: &libcni.NetworkConfigList{
				Plugins: []*libcni.NetworkConfig{
					{Bytes: []byte(`{"type":"bridge","bridge":"kuke0"}`)},
					{Bytes: []byte(`{"type":"loopback"}`)},
				},
			},
			want: "kuke0",
		},
		{
			name: "no bridge plugin",
			conf: &libcni.NetworkConfigList{
				Plugins: []*libcni.NetworkConfig{
					{Bytes: []byte(`{"type":"loopback"}`)},
				},
			},
			want: "",
		},
		{
			name: "malformed plugin bytes skipped",
			conf: &libcni.NetworkConfigList{
				Plugins: []*libcni.NetworkConfig{
					{Bytes: []byte(`not-json`)},
					{Bytes: []byte(`{"type":"bridge","bridge":"kuke1"}`)},
				},
			},
			want: "kuke1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bridgeNameFromNetConf(tt.conf); got != tt.want {
				t.Errorf("bridgeNameFromNetConf() = %q, want %q", got, tt.want)
			}
		})
	}
}
