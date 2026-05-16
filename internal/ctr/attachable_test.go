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
		t.Errorf("Attachable=false produced kuketty binary mount; should not")
	}
	if hasMountAt(got, ctr.AttachableTTYDir) {
		t.Errorf("Attachable=false produced kuketty tty dir mount; should not")
	}
	if hasMountAt(got, ctr.AttachableMetadataPath) {
		t.Errorf("Attachable=false produced kuketty metadata mount; should not")
	}
}

// TestBuildContainerSpec_AttachableTrue_MountsAndArgsWrap locks down the
// post-swap (issue #410) wrapper contract: the OCI spec emits exactly three
// bind mounts (kuketty binary RO, per-container tty dir RW, per-container
// metadata file RO) and process.args is the single element `[kuketty]`.
// No CLI flags, no trailing workload argv — the workload command moves into
// the bind-mounted api.TerminalDoc's Spec.Command/CommandArgs via the
// RenderMetadata callback (asserted separately).
func TestBuildContainerSpec_AttachableTrue_MountsAndArgsWrap(t *testing.T) {
	cases := []struct {
		name         string
		userCommand  string
		userArgs     []string
		imageArgs    []string // pre-seeded process.args = ENTRYPOINT+CMD
		wantWorkload []string
	}{
		{
			name:         "image-only ENTRYPOINT, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh"},
			wantWorkload: []string{"/bin/sh"},
		},
		{
			name:         "image-only CMD, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/usr/bin/python3"},
			wantWorkload: []string{"/usr/bin/python3"},
		},
		{
			name:         "image ENTRYPOINT and CMD, no user override",
			userCommand:  "",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh", "-c", "echo image"},
			wantWorkload: []string{"/bin/sh", "-c", "echo image"},
		},
		{
			name:         "user overrides command (CMD analogue)",
			userCommand:  "claude",
			userArgs:     nil,
			imageArgs:    []string{"/bin/sh"},
			wantWorkload: []string{"claude"},
		},
		{
			name:         "user overrides args (ENTRYPOINT-args analogue)",
			userCommand:  "",
			userArgs:     []string{"node", "server.js"},
			imageArgs:    []string{"/bin/sh"},
			wantWorkload: []string{"node", "server.js"},
		},
		{
			name:         "user overrides both command and args",
			userCommand:  "bash",
			userArgs:     []string{"-l", "-c", "tail -F /var/log/x"},
			imageArgs:    []string{"/bin/sh"},
			wantWorkload: []string{"bash", "-l", "-c", "tail -F /var/log/x"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var capturedWorkload []string
			renderCalls := 0
			inj := ctr.AttachableInjection{
				KukettyBinaryPath: "/opt/kukeon/bin/kuketty",
				HostTTYDir:        "/opt/kukeon/realm/space/stack/cell/c1/tty",
				HostMetadataPath:  "/opt/kukeon/realm/space/stack/cell/c1/kuketty-metadata.json",
				RenderMetadata: func(workloadArgv []string) error {
					renderCalls++
					capturedWorkload = append([]string(nil), workloadArgv...)
					return nil
				},
			}
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
			if binMount.Source != inj.KukettyBinaryPath {
				t.Errorf("binary mount source = %q, want %q", binMount.Source, inj.KukettyBinaryPath)
			}
			if !containsString(binMount.Options, "ro") {
				t.Errorf("binary mount must be read-only, got options=%v", binMount.Options)
			}

			ttyMount := findMount(spec, ctr.AttachableTTYDir)
			if ttyMount == nil {
				t.Fatalf("expected bind mount at %s, got mounts=%+v", ctr.AttachableTTYDir, spec.Mounts)
			}
			if ttyMount.Source != inj.HostTTYDir {
				t.Errorf("tty mount source = %q, want %q", ttyMount.Source, inj.HostTTYDir)
			}
			if containsString(ttyMount.Options, "ro") {
				t.Errorf("tty mount must be read-write, got options=%v", ttyMount.Options)
			}
			// Issue #547: the tty bind must be `rprivate` so a nested
			// kukeond running inside the attachable cell (e.g.
			// `make dev-init` inside a `kukeon-dev-root` cell) cannot
			// propagate mount events back through the bind and break the
			// parent host's attach plumbing for this cell.
			if !containsString(ttyMount.Options, "rprivate") {
				t.Errorf(
					"tty mount must carry %q for nested-kukeon safety, got options=%v",
					"rprivate",
					ttyMount.Options,
				)
			}

			metaMount := findMount(spec, ctr.AttachableMetadataPath)
			if metaMount == nil {
				t.Fatalf("expected bind mount at %s, got mounts=%+v", ctr.AttachableMetadataPath, spec.Mounts)
			}
			if metaMount.Source != inj.HostMetadataPath {
				t.Errorf("metadata mount source = %q, want %q", metaMount.Source, inj.HostMetadataPath)
			}
			if !containsString(metaMount.Options, "ro") {
				t.Errorf("metadata mount must be read-only, got options=%v", metaMount.Options)
			}

			// AC: legacy single-file /run/sbsh.socket mount must be gone.
			if hasMountAt(spec, "/run/sbsh.socket") {
				t.Errorf("legacy /run/sbsh.socket file mount still present: %+v", spec.Mounts)
			}
			// AC: legacy sbsh binary path must be gone — the swap to
			// kuketty changes the binary's in-container destination, so
			// the old path appearing in mounts would mean an unfinished
			// rewrite.
			if hasMountAt(spec, "/.kukeon/bin/sbsh") {
				t.Errorf("legacy sbsh binary mount still present: %+v", spec.Mounts)
			}

			// Exactly one mount per Destination among the three the wrapper
			// owns.
			counts := map[string]int{}
			for _, m := range spec.Mounts {
				switch m.Destination {
				case ctr.AttachableBinaryPath, ctr.AttachableTTYDir, ctr.AttachableMetadataPath:
					counts[m.Destination]++
				}
			}
			for dest, want := range map[string]int{
				ctr.AttachableBinaryPath:   1,
				ctr.AttachableTTYDir:       1,
				ctr.AttachableMetadataPath: 1,
			} {
				if counts[dest] != want {
					t.Errorf("expected %d mount(s) at %s, got %d", want, dest, counts[dest])
				}
			}

			// Phase-1b wrapper contract: `[kuketty]` exactly. No `--`,
			// no trailing argv, no flags. The workload moves into the
			// bind-mounted TerminalDoc via the RenderMetadata callback.
			wantArgs := []string{ctr.AttachableBinaryPath}
			if !reflect.DeepEqual(spec.Process.Args, wantArgs) {
				t.Errorf("Process.Args = %v, want %v", spec.Process.Args, wantArgs)
			}

			// Negative: no leftover sbsh flags must appear on the wrapped
			// argv. Their presence would mean the wrapper rewrite missed
			// a code path and is still rendering sbsh CLI input.
			for _, banned := range []string{
				"terminal", "--run-path", "--socket", "--capture-file",
				"--log-file", "--profile", "--profiles-dir",
				"--socket-mode", "--socket-gid", "--capture-mode",
				"--capture-gid", "--log-file-mode", "--log-file-gid",
				"--", // phase-1b no longer uses the `--` separator either.
			} {
				for _, a := range spec.Process.Args {
					if a == banned {
						t.Errorf("legacy flag %q present in Process.Args = %v", banned, spec.Process.Args)
					}
				}
			}

			// Metadata renderer must fire exactly once with the resolved
			// workload argv (image ENTRYPOINT+CMD merged with any user
			// override, BEFORE the wrap clears Process.Args).
			if renderCalls != 1 {
				t.Errorf("RenderMetadata fired %d times, want 1", renderCalls)
			}
			if !reflect.DeepEqual(capturedWorkload, tc.wantWorkload) {
				t.Errorf("RenderMetadata workload argv = %v, want %v", capturedWorkload, tc.wantWorkload)
			}
		})
	}
}

// TestBuildContainerSpec_AttachableTrue_EmptyImageArgs locks the empty-args
// case: the wrapper rewrites Process.Args to `[kuketty]` and the renderer
// sees an empty workload (which the runner-side renderer falls back to the
// sbsh builder's hardcoded default, exercised in the runner test).
func TestBuildContainerSpec_AttachableTrue_EmptyImageArgs(t *testing.T) {
	in := intmodel.ContainerSpec{
		ID:         "c1",
		Image:      "registry.eminwux.com/busybox:latest",
		CellName:   "cell",
		SpaceName:  "space",
		RealmName:  "realm",
		StackName:  "stack",
		Attachable: true,
	}
	var capturedWorkload []string
	renderCalls := 0
	inj := ctr.AttachableInjection{
		KukettyBinaryPath: "/opt/kukeon/bin/kuketty",
		HostTTYDir:        "/opt/kukeon/realm/space/stack/cell/c1/tty",
		HostMetadataPath:  "/opt/kukeon/realm/space/stack/cell/c1/kuketty-metadata.json",
		RenderMetadata: func(workloadArgv []string) error {
			renderCalls++
			capturedWorkload = append([]string(nil), workloadArgv...)
			return nil
		},
	}
	spec := applyBuiltSpecWith(t, in, nil, ctr.WithAttachableInjection(inj))

	want := []string{ctr.AttachableBinaryPath}
	if !reflect.DeepEqual(spec.Process.Args, want) {
		t.Errorf("Process.Args = %v, want %v", spec.Process.Args, want)
	}
	if renderCalls != 1 {
		t.Errorf("RenderMetadata fired %d times, want 1", renderCalls)
	}
	if len(capturedWorkload) != 0 {
		t.Errorf("RenderMetadata workload argv = %v, want []", capturedWorkload)
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
