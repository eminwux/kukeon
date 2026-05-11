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

package kuketty_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/kuketty"
)

func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	in := &kuketty.Metadata{
		APIVersion: kuketty.APIVersion,
		Kind:       kuketty.Kind,
		Meta:       kuketty.Meta{ContainerID: "c1"},
		Spec: kuketty.Spec{
			RunPath: "/run/kukeon/tty",
			Socket: kuketty.SocketSpec{
				Path: "/run/kukeon/tty/socket",
				Mode: "0660",
				GID:  986,
			},
			Capture: kuketty.CaptureSpec{Path: "/run/kukeon/tty/capture"},
			Log:     kuketty.LogSpec{Path: "/run/kukeon/tty/log"},
			Shell: kuketty.ShellSpec{
				Prompt: "$ ",
				OnInit: []kuketty.Stage{{Script: "echo hello"}},
			},
		},
	}
	data, err := kuketty.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	out, err := kuketty.Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("round-trip mismatch:\n got=%+v\nwant=%+v", out, in)
	}
}

func TestValidate_RejectsBadInputs(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*kuketty.Metadata)
		wantErr string
	}{
		{
			name:    "wrong apiVersion",
			mutate:  func(m *kuketty.Metadata) { m.APIVersion = "kuketty.kukeon.io/v0" },
			wantErr: "apiVersion",
		},
		{
			name:    "wrong kind",
			mutate:  func(m *kuketty.Metadata) { m.Kind = "Other" },
			wantErr: "kind",
		},
		{
			name:    "missing runPath",
			mutate:  func(m *kuketty.Metadata) { m.Spec.RunPath = "" },
			wantErr: "runPath",
		},
		{
			name:    "missing socket path",
			mutate:  func(m *kuketty.Metadata) { m.Spec.Socket.Path = "" },
			wantErr: "socket.path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &kuketty.Metadata{
				APIVersion: kuketty.APIVersion,
				Kind:       kuketty.Kind,
				Spec: kuketty.Spec{
					RunPath: "/run/kukeon/tty",
					Socket:  kuketty.SocketSpec{Path: "/run/kukeon/tty/socket"},
				},
			}
			tc.mutate(m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("Validate returned nil, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate error = %q, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestUnmarshal_MalformedJSON(t *testing.T) {
	_, err := kuketty.Unmarshal([]byte("{ not json"))
	if err == nil {
		t.Fatalf("Unmarshal returned nil, want parse error")
	}
}
