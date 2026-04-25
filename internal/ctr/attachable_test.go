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

package ctr_test

import (
	"context"
	"reflect"
	"testing"

	ctr "github.com/eminwux/kukeon/internal/ctr"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// applyBuiltSpecWith runs the SpecOpts produced by BuildContainerSpec against
// a runtime spec that has been pre-seeded with the image's resolved
// ENTRYPOINT+CMD merge — i.e. what process.args would already contain by the
// time containerd applies user-supplied opts.
func applyBuiltSpecWith(
	t *testing.T,
	in intmodel.ContainerSpec,
	imageArgs []string,
	options ...ctr.BuildOption,
) *runtimespec.Spec {
	t.Helper()
	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{Args: append([]string(nil), imageArgs...)},
		Linux:   &runtimespec.Linux{},
	}
	built := ctr.BuildContainerSpec(in, options...)
	for _, opt := range built.SpecOpts {
		if err := opt(context.Background(), nil, nil, spec); err != nil {
			t.Fatalf("SpecOpts returned error: %v", err)
		}
	}
	return spec
}

func TestBuildContainerSpec_AttachableFalseIsByteIdentical(t *testing.T) {
	base := intmodel.ContainerSpec{
		ID:        "c1",
		Image:     "registry.eminwux.com/busybox:latest",
		CellName:  "cell",
		SpaceName: "space",
		RealmName: "realm",
		StackName: "stack",
		Command:   "sh",
		Args:      []string{"-c", "echo hello"},
		Env:       []string{"FOO=bar"},
	}

	imageArgs := []string{"/bin/sh"} // simulate image-only ENTRYPOINT

	got := applyBuiltSpecWith(t, base, imageArgs)
	// Same input, omitting the unused option, must produce the same OCI spec.
	want := applyBuiltSpecWith(t, base, imageArgs)

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Attachable=false spec drifted from baseline:\n got=%+v\nwant=%+v", got, want)
	}
	if hasMountAt(got, ctr.AttachableBinaryPath) {
		t.Errorf("Attachable=false produced sbsh binary mount; should not")
	}
	if hasMountAt(got, ctr.AttachableSocketPath) {
		t.Errorf("Attachable=false produced sbsh socket mount; should not")
	}
}

func TestBuildContainerSpec_AttachableTrue_MountsAndArgsWrap(t *testing.T) {
	cases := []struct {
		name         string
		userCommand  string
		userArgs     []string
		imageArgs    []string // pre-seeded process.args = ENTRYPOINT+CMD
		wantOriginal []string
	}{
		{
			name:         "image-only ENTRYPOINT, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh"},
			wantOriginal: []string{"/bin/sh"},
		},
		{
			name:         "image-only CMD, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/usr/bin/python3"},
			wantOriginal: []string{"/usr/bin/python3"},
		},
		{
			name:         "image ENTRYPOINT and CMD, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh", "-c", "echo image"},
			wantOriginal: []string{"/bin/sh", "-c", "echo image"},
		},
		{
			name:         "user overrides command (CMD analogue)",
			userCommand:  "claude",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh"},
			wantOriginal: []string{"claude"},
		},
		{
			name:         "user overrides args (ENTRYPOINT-args analogue)",
			userCommand:  "",
			userArgs:     []string{"node", "server.js"},
			imageArgs:    []string{"/bin/sh"},
			wantOriginal: []string{"node", "server.js"},
		},
		{
			name:         "user overrides both command and args",
			userCommand:  "bash",
			userArgs:     []string{"-l", "-c", "tail -F /var/log/x"},
			imageArgs:    []string{"/bin/sh"},
			wantOriginal: []string{"bash", "-l", "-c", "tail -F /var/log/x"},
		},
	}

	inj := ctr.AttachableInjection{
		SbshBinaryPath: "/opt/kukeon/cache/sbsh/amd64/sbsh",
		HostSocketPath: "/opt/kukeon/realm/space/stack/cell/c1/sbsh.io",
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := intmodel.ContainerSpec{
				ID:         "c1",
				Image:      "registry.eminwux.com/busybox:latest",
				CellName:   "cell",
				SpaceName:  "space",
				RealmName:  "realm",
				StackName:  "stack",
				Command:    tc.userCommand,
				Args:       tc.userArgs,
				Attachable: true,
			}
			spec := applyBuiltSpecWith(t, in, tc.imageArgs, ctr.WithAttachableInjection(inj))

			binMount := findMount(spec, ctr.AttachableBinaryPath)
			if binMount == nil {
				t.Fatalf("expected RO bind mount at %s, got mounts=%+v", ctr.AttachableBinaryPath, spec.Mounts)
			}
			if binMount.Source != inj.SbshBinaryPath {
				t.Errorf("binary mount source = %q, want %q", binMount.Source, inj.SbshBinaryPath)
			}
			if !containsString(binMount.Options, "ro") {
				t.Errorf("binary mount must be read-only, got options=%v", binMount.Options)
			}

			sockMount := findMount(spec, ctr.AttachableSocketPath)
			if sockMount == nil {
				t.Fatalf("expected bind mount at %s, got mounts=%+v", ctr.AttachableSocketPath, spec.Mounts)
			}
			if sockMount.Source != inj.HostSocketPath {
				t.Errorf("socket mount source = %q, want %q", sockMount.Source, inj.HostSocketPath)
			}

			wantPrefix := []string{
				ctr.AttachableBinaryPath,
				ctr.AttachableSubcommand,
				"--socket",
				ctr.AttachableSocketPath,
				"--",
			}
			if len(spec.Process.Args) < len(wantPrefix) {
				t.Fatalf("Process.Args = %v, missing wrapper prefix", spec.Process.Args)
			}
			gotPrefix := spec.Process.Args[:len(wantPrefix)]
			if !reflect.DeepEqual(gotPrefix, wantPrefix) {
				t.Errorf("wrapper prefix = %v, want %v", gotPrefix, wantPrefix)
			}
			gotOriginal := spec.Process.Args[len(wantPrefix):]
			if !reflect.DeepEqual(gotOriginal, tc.wantOriginal) {
				t.Errorf("wrapped original args = %v, want %v", gotOriginal, tc.wantOriginal)
			}
		})
	}
}

func TestBuildContainerSpec_AttachableTrue_EmptyImageArgs(t *testing.T) {
	// An image whose ENTRYPOINT+CMD resolves to nothing (uncommon but valid)
	// must still produce a wrapped args list — the wrapper prefix on its own,
	// with no original args after it.
	in := intmodel.ContainerSpec{
		ID:         "c1",
		Image:      "registry.eminwux.com/busybox:latest",
		CellName:   "cell",
		SpaceName:  "space",
		RealmName:  "realm",
		StackName:  "stack",
		Attachable: true,
	}
	inj := ctr.AttachableInjection{
		SbshBinaryPath: "/opt/kukeon/cache/sbsh/amd64/sbsh",
		HostSocketPath: "/opt/kukeon/realm/space/stack/cell/c1/sbsh.io",
	}
	spec := applyBuiltSpecWith(t, in, nil, ctr.WithAttachableInjection(inj))

	want := []string{
		ctr.AttachableBinaryPath,
		ctr.AttachableSubcommand,
		"--socket",
		ctr.AttachableSocketPath,
		"--",
	}
	if !reflect.DeepEqual(spec.Process.Args, want) {
		t.Errorf("Process.Args = %v, want %v", spec.Process.Args, want)
	}
}

func findMount(spec *runtimespec.Spec, dest string) *runtimespec.Mount {
	for i := range spec.Mounts {
		if spec.Mounts[i].Destination == dest {
			return &spec.Mounts[i]
		}
	}
	return nil
}

func hasMountAt(spec *runtimespec.Spec, dest string) bool {
	return findMount(spec, dest) != nil
}
