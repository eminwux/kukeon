//go:build !integration

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

package controller

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

func TestValidateVolumes(t *testing.T) {
	tmpDir := t.TempDir()
	existing := tmpDir

	tests := []struct {
		name    string
		in      []intmodel.VolumeMount
		wantErr error
	}{
		{name: "empty", in: nil},
		{
			name: "valid rw",
			in:   []intmodel.VolumeMount{{Source: existing, Target: "/dst"}},
		},
		{
			name: "valid ro",
			in:   []intmodel.VolumeMount{{Source: existing, Target: "/dst", ReadOnly: true}},
		},
		{
			name:    "missing source",
			in:      []intmodel.VolumeMount{{Target: "/dst"}},
			wantErr: errdefs.ErrVolumeSourceRequired,
		},
		{
			name:    "missing target",
			in:      []intmodel.VolumeMount{{Source: existing}},
			wantErr: errdefs.ErrVolumeTargetRequired,
		},
		{
			name:    "named volume rejected",
			in:      []intmodel.VolumeMount{{Source: "my-vol", Target: "/dst"}},
			wantErr: errdefs.ErrVolumeNamedNotSupported,
		},
		{
			name:    "relative source rejected",
			in:      []intmodel.VolumeMount{{Source: "relative/path", Target: "/dst"}},
			wantErr: errdefs.ErrVolumeSourceNotAbsolute,
		},
		{
			name:    "relative target rejected",
			in:      []intmodel.VolumeMount{{Source: existing, Target: "relative"}},
			wantErr: errdefs.ErrVolumeTargetNotAbsolute,
		},
		{
			name:    "source does not exist",
			in:      []intmodel.VolumeMount{{Source: filepath.Join(tmpDir, "nope"), Target: "/dst"}},
			wantErr: errdefs.ErrVolumeSourceNotFound,
		},
		{
			name: "explicit bind kind accepted",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindBind, Source: existing, Target: "/dst"},
			},
		},
		{
			name: "tmpfs minimal",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindTmpfs, Target: "/run/cache"},
			},
		},
		{
			name: "tmpfs with size and mode",
			in: []intmodel.VolumeMount{
				{
					Kind:      intmodel.VolumeKindTmpfs,
					Target:    "/var/lib/containerd",
					SizeBytes: 1 << 30,
					Mode:      0o0755,
				},
			},
		},
		{
			name: "tmpfs rejects source",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindTmpfs, Source: "/host/path", Target: "/dst"},
			},
			wantErr: errdefs.ErrVolumeTmpfsSourceForbidden,
		},
		{
			name: "tmpfs requires target",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindTmpfs},
			},
			wantErr: errdefs.ErrVolumeTargetRequired,
		},
		{
			name: "tmpfs target must be absolute",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKindTmpfs, Target: "relative"},
			},
			wantErr: errdefs.ErrVolumeTargetNotAbsolute,
		},
		{
			name: "unknown kind rejected",
			in: []intmodel.VolumeMount{
				{Kind: intmodel.VolumeKind("nfs"), Target: "/dst"},
			},
			wantErr: errdefs.ErrVolumeKindUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := validateVolumes(tt.in)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want is %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(out) != len(tt.in) {
				t.Fatalf("len = %d, want %d", len(out), len(tt.in))
			}
		})
	}
}

func TestValidateVolumes_TrimsWhitespace(t *testing.T) {
	tmpDir := t.TempDir()
	in := []intmodel.VolumeMount{{Source: "  " + tmpDir + "  ", Target: "  /dst  "}}
	out, err := validateVolumes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out[0].Source != tmpDir || out[0].Target != "/dst" {
		t.Errorf("got %+v, expected source=%q target=\"/dst\"", out[0], tmpDir)
	}
}

func TestValidateVolumes_PreservesTmpfsFields(t *testing.T) {
	in := []intmodel.VolumeMount{{
		Kind:      intmodel.VolumeKindTmpfs,
		Target:    "/var/lib/containerd",
		ReadOnly:  true,
		SizeBytes: 1 << 30,
		Mode:      0o0755,
	}}
	out, err := validateVolumes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("len = %d, want 1", len(out))
	}
	got := out[0]
	if got.Kind != intmodel.VolumeKindTmpfs {
		t.Errorf("Kind = %q, want %q", got.Kind, intmodel.VolumeKindTmpfs)
	}
	if got.Source != "" {
		t.Errorf("Source = %q, want \"\"", got.Source)
	}
	if got.Target != "/var/lib/containerd" {
		t.Errorf("Target = %q, want \"/var/lib/containerd\"", got.Target)
	}
	if !got.ReadOnly {
		t.Errorf("ReadOnly = false, want true")
	}
	if got.SizeBytes != 1<<30 {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, 1<<30)
	}
	if got.Mode != 0o0755 {
		t.Errorf("Mode = %o, want %o", got.Mode, 0o0755)
	}
}
