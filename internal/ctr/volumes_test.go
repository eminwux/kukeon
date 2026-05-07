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

package ctr

import (
	"reflect"
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestBuildVolumeMounts_Bind(t *testing.T) {
	tests := []struct {
		name    string
		in      []intmodel.VolumeMount
		wantLen int
		wantRO  []bool // per-mount; "ro" option expected
	}{
		{name: "empty", in: nil, wantLen: 0},
		{
			name:    "rw",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: "/b"}},
			wantLen: 1,
			wantRO:  []bool{false},
		},
		{
			name:    "ro",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: "/b", ReadOnly: true}},
			wantLen: 1,
			wantRO:  []bool{true},
		},
		{
			name: "mixed",
			in: []intmodel.VolumeMount{
				{Source: "/a", Target: "/a"},
				{Source: "/b", Target: "/b", ReadOnly: true},
			},
			wantLen: 2,
			wantRO:  []bool{false, true},
		},
		{
			name:    "explicit bind kind matches default",
			in:      []intmodel.VolumeMount{{Kind: intmodel.VolumeKindBind, Source: "/a", Target: "/b"}},
			wantLen: 1,
			wantRO:  []bool{false},
		},
		{
			name:    "skip empty source",
			in:      []intmodel.VolumeMount{{Source: "", Target: "/b"}},
			wantLen: 0,
		},
		{
			name:    "skip empty target",
			in:      []intmodel.VolumeMount{{Source: "/a", Target: ""}},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildVolumeMounts(tt.in)
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for i, m := range got {
				if m.Type != "bind" {
					t.Errorf("[%d] Type = %q, want \"bind\"", i, m.Type)
				}
				if m.Source != tt.in[i].Source || m.Destination != tt.in[i].Target {
					t.Errorf("[%d] Source/Destination = %q/%q, want %q/%q",
						i, m.Source, m.Destination, tt.in[i].Source, tt.in[i].Target)
				}
				hasRO := false
				hasRW := false
				hasRbind := false
				for _, o := range m.Options {
					switch o {
					case "ro":
						hasRO = true
					case "rw":
						hasRW = true
					case "rbind":
						hasRbind = true
					}
				}
				if !hasRbind {
					t.Errorf("[%d] Options %v missing rbind", i, m.Options)
				}
				if tt.wantRO[i] {
					if !hasRO || hasRW {
						t.Errorf("[%d] Options %v want ro", i, m.Options)
					}
				} else {
					if hasRO || !hasRW {
						t.Errorf("[%d] Options %v want rw", i, m.Options)
					}
				}
			}
		})
	}
}

// TestBuildVolumeMounts_BindParity locks the all-bind output to today's exact
// runtimespec.Mount shape so unrelated callers (kukeond cell spec, attachable
// wrapping, secret-injected mounts) keep their byte-identical OCI mounts after
// the tmpfs branch landed. Acceptance criterion from issue #309.
func TestBuildVolumeMounts_BindParity(t *testing.T) {
	in := []intmodel.VolumeMount{
		{Source: "/host/run", Target: "/run/kukeon"},
		{Source: "/host/opt", Target: "/opt/kukeon", ReadOnly: true},
	}
	want := []runtimespec.Mount{
		{
			Destination: "/run/kukeon",
			Source:      "/host/run",
			Type:        "bind",
			Options:     []string{"rbind", "rw"},
		},
		{
			Destination: "/opt/kukeon",
			Source:      "/host/opt",
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
	}
	got := buildVolumeMounts(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildVolumeMounts(bind list) = %+v, want %+v", got, want)
	}
}

func TestBuildVolumeMounts_Tmpfs(t *testing.T) {
	tests := []struct {
		name string
		in   intmodel.VolumeMount
		want runtimespec.Mount
		skip bool
	}{
		{
			name: "minimal",
			in:   intmodel.VolumeMount{Kind: intmodel.VolumeKindTmpfs, Target: "/var/lib/containerd"},
			want: runtimespec.Mount{
				Destination: "/var/lib/containerd",
				Source:      "tmpfs",
				Type:        "tmpfs",
				Options:     []string{"rw"},
			},
		},
		{
			name: "size and mode",
			in: intmodel.VolumeMount{
				Kind:      intmodel.VolumeKindTmpfs,
				Target:    "/scratch",
				SizeBytes: 1 << 30, // 1 GiB
				Mode:      0o755,
			},
			want: runtimespec.Mount{
				Destination: "/scratch",
				Source:      "tmpfs",
				Type:        "tmpfs",
				Options:     []string{"size=1073741824", "mode=0755", "rw"},
			},
		},
		{
			name: "read-only",
			in: intmodel.VolumeMount{
				Kind:     intmodel.VolumeKindTmpfs,
				Target:   "/cache",
				ReadOnly: true,
			},
			want: runtimespec.Mount{
				Destination: "/cache",
				Source:      "tmpfs",
				Type:        "tmpfs",
				Options:     []string{"ro"},
			},
		},
		{
			name: "skip empty target",
			in:   intmodel.VolumeMount{Kind: intmodel.VolumeKindTmpfs, Target: ""},
			skip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildVolumeMounts([]intmodel.VolumeMount{tt.in})
			if tt.skip {
				if len(got) != 0 {
					t.Fatalf("expected entry to be skipped, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("len = %d, want 1", len(got))
			}
			if !reflect.DeepEqual(got[0], tt.want) {
				t.Fatalf("got %+v, want %+v", got[0], tt.want)
			}
		})
	}
}

// TestBuildVolumeMounts_Mixed verifies that a list mixing bind and tmpfs
// entries preserves declaration order and emits each kind's expected OCI
// shape independently.
func TestBuildVolumeMounts_Mixed(t *testing.T) {
	in := []intmodel.VolumeMount{
		{Source: "/host/run", Target: "/run/kukeon"},
		{Kind: intmodel.VolumeKindTmpfs, Target: "/var/lib/containerd", SizeBytes: 512 << 20},
		{Source: "/host/opt", Target: "/opt/kukeon", ReadOnly: true},
	}
	want := []runtimespec.Mount{
		{
			Destination: "/run/kukeon",
			Source:      "/host/run",
			Type:        "bind",
			Options:     []string{"rbind", "rw"},
		},
		{
			Destination: "/var/lib/containerd",
			Source:      "tmpfs",
			Type:        "tmpfs",
			Options:     []string{"size=536870912", "rw"},
		},
		{
			Destination: "/opt/kukeon",
			Source:      "/host/opt",
			Type:        "bind",
			Options:     []string{"rbind", "ro"},
		},
	}
	got := buildVolumeMounts(in)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildVolumeMounts(mixed) = %+v, want %+v", got, want)
	}
}
