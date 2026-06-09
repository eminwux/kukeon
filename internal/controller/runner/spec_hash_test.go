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

package runner_test

import (
	"testing"

	"github.com/eminwux/kukeon/internal/controller/apply"
	"github.com/eminwux/kukeon/internal/controller/runner"
	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// ComputeContainerSpecHash is re-exported here as a short local alias so the
// table-driven tests below stay readable. The hash itself lives in
// internal/controller/runner/spec_hash.go.
var ComputeContainerSpecHash = runner.ComputeContainerSpecHash

// TestComputeContainerSpecHash_Deterministic locks that the hash function is
// pure and stable across equal inputs. A hash that drifts across invocations
// would surface as spurious StartCell refusals on every restart.
func TestComputeContainerSpecHash_Deterministic(t *testing.T) {
	spec := intmodel.ContainerSpec{
		Image:   "docker.io/library/alpine:latest",
		Command: "sleep",
		Args:    []string{"infinity"},
	}
	got1 := ComputeContainerSpecHash(spec)
	got2 := ComputeContainerSpecHash(spec)
	if got1 != got2 {
		t.Fatalf("ComputeContainerSpecHash is not deterministic: %q != %q", got1, got2)
	}
	if len(got1) != 64 {
		t.Errorf("ComputeContainerSpecHash should return a 64-char hex SHA-256; got len=%d (%q)", len(got1), got1)
	}
}

// TestComputeContainerSpecHash_NilEmptyArgsEqual locks that nil and zero-len
// Args yield the same hash. containerd treats both as "no args"; a
// nil-vs-empty hash divergence would cause a healthy restart to be misread as
// drift on a CellSpec whose YAML omitted `args:` but whose actual spec was
// `[]`.
func TestComputeContainerSpecHash_NilEmptyArgsEqual(t *testing.T) {
	specNil := intmodel.ContainerSpec{
		Image:   "alpine",
		Command: "sleep",
		Args:    nil,
	}
	specEmpty := intmodel.ContainerSpec{
		Image:   "alpine",
		Command: "sleep",
		Args:    []string{},
	}
	if ComputeContainerSpecHash(specNil) != ComputeContainerSpecHash(specEmpty) {
		t.Errorf("nil and empty Args must hash identically; nil=%q empty=%q",
			ComputeContainerSpecHash(specNil), ComputeContainerSpecHash(specEmpty))
	}
}

// TestComputeContainerSpecHash_BreakingFieldsChangeHash locks that each
// Breaking-on-root field independently changes the hash. A field accidentally
// dropped from containerSpecHashPayload would silently let a drifted CellSpec
// pass the StartCell guard. The set mirrors `apply.DiffCell`'s Breaking-on-root
// classification — see `diffContainerSpec` in
// `internal/controller/apply/diff.go`. Issues #867, #990.
func TestComputeContainerSpecHash_BreakingFieldsChangeHash(t *testing.T) {
	base := intmodel.ContainerSpec{
		Image:   "alpine:1.0",
		Command: "sleep",
		Args:    []string{"60"},
	}
	baseHash := ComputeContainerSpecHash(base)

	cases := []struct {
		name string
		mut  func(s *intmodel.ContainerSpec)
	}{
		{"image", func(s *intmodel.ContainerSpec) { s.Image = "alpine:2.0" }},
		{"command", func(s *intmodel.ContainerSpec) { s.Command = "echo" }},
		{"args", func(s *intmodel.ContainerSpec) { s.Args = []string{"120"} }},
		{"privileged", func(s *intmodel.ContainerSpec) { s.Privileged = true }},
		{"user", func(s *intmodel.ContainerSpec) { s.User = "nobody" }},
		{"readOnlyRootFilesystem", func(s *intmodel.ContainerSpec) { s.ReadOnlyRootFilesystem = true }},
		{"capabilities", func(s *intmodel.ContainerSpec) {
			s.Capabilities = &intmodel.ContainerCapabilities{Add: []string{"CAP_NET_ADMIN"}}
		}},
		{"tmpfs", func(s *intmodel.ContainerSpec) {
			s.Tmpfs = []intmodel.ContainerTmpfsMount{{Path: "/tmp", SizeBytes: 1 << 20}}
		}},
		{"resources", func(s *intmodel.ContainerSpec) {
			limit := int64(64 << 20)
			s.Resources = &intmodel.ContainerResources{MemoryLimitBytes: &limit}
		}},
		// OCI-baked fields reclassified Breaking-on-root by issue #1154.
		{"workingDir", func(s *intmodel.ContainerSpec) { s.WorkingDir = "/opt/app" }},
		{"securityOpts", func(s *intmodel.ContainerSpec) { s.SecurityOpts = []string{"no-new-privileges"} }},
		{"volumes", func(s *intmodel.ContainerSpec) {
			s.Volumes = []intmodel.VolumeMount{{Source: "/host", Target: "/cell"}}
		}},
		{"secrets", func(s *intmodel.ContainerSpec) {
			s.Secrets = []intmodel.ContainerSecret{{Name: "db", FromEnv: "DB_PASS"}}
		}},
		{"secretsRef", func(s *intmodel.ContainerSpec) {
			s.Secrets = []intmodel.ContainerSecret{{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "prod"}}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mut := base
			tc.mut(&mut)
			if ComputeContainerSpecHash(mut) == baseHash {
				t.Errorf("changing %s must change the spec hash; base and mutated produced %q", tc.name, baseHash)
			}
		})
	}
}

// TestComputeContainerSpecHash_CompatibleFieldsDoNotChangeHash locks that
// fields `apply.DiffCell` classifies as Compatible (env, ports, repos) are
// *not* part of the hash. Hashing them would force a recreate-or-refuse on
// every `kuke apply -f` that only tweaked a compatible field, defeating the
// in-place UpdateCell path the apply layer already exercises for those
// fields. (volumes, securityOpts, and secrets moved to the Breaking set in
// issue #1154 — they are OCI-baked at create and now change the hash.)
func TestComputeContainerSpecHash_CompatibleFieldsDoNotChangeHash(t *testing.T) {
	base := intmodel.ContainerSpec{
		Image:   "alpine",
		Command: "sleep",
		Args:    []string{"infinity"},
	}
	baseHash := ComputeContainerSpecHash(base)

	cases := []struct {
		name string
		mut  func(s *intmodel.ContainerSpec)
	}{
		{"env", func(s *intmodel.ContainerSpec) { s.Env = []string{"FOO=bar"} }},
		{"ports", func(s *intmodel.ContainerSpec) { s.Ports = []string{"8080:80"} }},
		{"repos", func(s *intmodel.ContainerSpec) {
			s.Repos = []intmodel.ContainerRepo{{Name: "app", URL: "https://example.com/app.git", Target: "/src"}}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mut := base
			tc.mut(&mut)
			if ComputeContainerSpecHash(mut) != baseHash {
				t.Errorf("changing compatible field %s must NOT change the spec hash (apply.DiffCell handles it in-place); base=%q mutated=%q",
					tc.name, baseHash, ComputeContainerSpecHash(mut))
			}
		})
	}
}

// TestSpecHashDomainPinsToDiffCellBreakingFields is the regression guard that
// keeps containerSpecHashPayload's field set in lock-step with
// `apply.DiffCell`'s "requires containerd recreate" classification. If a
// future change widens the apply-layer breaking domain (e.g., classifies
// SecurityOpts as Breaking-on-root) without also extending
// containerSpecHashPayload, this test fails: the apply layer would force a
// recreate while StartCell silently resumes a stale snapshot.
//
// The shape: build a pair of cells that differ only in field X. Feed them
// through DiffCell. Whenever DiffCell reports a Breaking change for the root
// container, ComputeContainerSpecHash on the two root specs must differ. If
// DiffCell reports Compatible or no change, ComputeContainerSpecHash must
// match (the apply layer handles it in place, so the hash must not force a
// refuse). Mirrors `apply.DiffCell`'s root-side classification — see
// `diffContainerSpec`'s per-field Breaking-vs-Compatible calls in
// `internal/controller/apply/diff.go`. Issues #867, #990.
//
// Companion guard: TestSpecHashDomainVersionPinsToPayload (in-package, beside
// the unexported payload it reflects over) forces a SpecHashDomainVersion bump
// in the same edit as any payload field-set change — together the two keep the
// payload pinned to both the apply-layer breaking domain and a domain version,
// so a domain widening can never strand pre-existing cells unversioned. Issue
// #1171.
func TestSpecHashDomainPinsToDiffCellBreakingFields(t *testing.T) {
	makeCell := func(root intmodel.ContainerSpec) intmodel.Cell {
		root.Root = true
		root.ID = "root"
		return intmodel.Cell{
			Metadata: intmodel.CellMetadata{Name: "c"},
			Spec: intmodel.CellSpec{
				ID:         "c",
				RealmName:  "r",
				SpaceName:  "s",
				StackName:  "st",
				Containers: []intmodel.ContainerSpec{root},
			},
		}
	}

	baseRoot := intmodel.ContainerSpec{
		Image:   "alpine:1.0",
		Command: "sleep",
		Args:    []string{"60"},
	}

	cases := []struct {
		name             string
		mut              func(s *intmodel.ContainerSpec)
		expectRootBreaks bool // DiffCell's classification for this field on the root container
	}{
		// Breaking-on-root domain — must change the hash.
		{"image", func(s *intmodel.ContainerSpec) { s.Image = "alpine:2.0" }, true},
		{"command", func(s *intmodel.ContainerSpec) { s.Command = "echo" }, true},
		{"args", func(s *intmodel.ContainerSpec) { s.Args = []string{"120"} }, true},
		{"privileged", func(s *intmodel.ContainerSpec) { s.Privileged = true }, true},
		{"user", func(s *intmodel.ContainerSpec) { s.User = "nobody" }, true},
		{"readOnlyRootFilesystem", func(s *intmodel.ContainerSpec) { s.ReadOnlyRootFilesystem = true }, true},
		{"capabilities", func(s *intmodel.ContainerSpec) {
			s.Capabilities = &intmodel.ContainerCapabilities{Add: []string{"CAP_NET_ADMIN"}}
		}, true},
		{"tmpfs", func(s *intmodel.ContainerSpec) {
			s.Tmpfs = []intmodel.ContainerTmpfsMount{{Path: "/tmp", SizeBytes: 1 << 20}}
		}, true},
		{"resources", func(s *intmodel.ContainerSpec) {
			limit := int64(64 << 20)
			s.Resources = &intmodel.ContainerResources{MemoryLimitBytes: &limit}
		}, true},
		// OCI-baked fields reclassified Breaking-on-root by issue #1154 —
		// must change the hash so the bare-start drift guard catches them too.
		{"workingDir", func(s *intmodel.ContainerSpec) { s.WorkingDir = "/opt/app" }, true},
		{"securityOpts", func(s *intmodel.ContainerSpec) { s.SecurityOpts = []string{"no-new-privileges"} }, true},
		{"volumes", func(s *intmodel.ContainerSpec) {
			s.Volumes = []intmodel.VolumeMount{{Source: "/host", Target: "/cell"}}
		}, true},
		{"secrets", func(s *intmodel.ContainerSpec) {
			s.Secrets = []intmodel.ContainerSecret{{Name: "db", FromEnv: "DB_PASS"}}
		}, true},
		// Compatible domain — must NOT change the hash.
		{"env", func(s *intmodel.ContainerSpec) { s.Env = []string{"FOO=bar"} }, false},
		{"ports", func(s *intmodel.ContainerSpec) { s.Ports = []string{"8080:80"} }, false},
		{"repos", func(s *intmodel.ContainerSpec) {
			s.Repos = []intmodel.ContainerRepo{{Name: "app", URL: "https://example.com/app.git", Target: "/src"}}
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desiredRoot := baseRoot
			tc.mut(&desiredRoot)

			desiredCell := makeCell(desiredRoot)
			actualCell := makeCell(baseRoot)

			diff := apply.DiffCell(desiredCell, actualCell)

			// Did DiffCell flag the root container as breaking?
			gotRootBreaks := diff.RootContainerChanged && diff.ChangeType == apply.ChangeTypeBreaking
			if gotRootBreaks != tc.expectRootBreaks {
				t.Fatalf("DiffCell classification drift on field %q: gotRootBreaks=%v want=%v (diff=%+v)",
					tc.name, gotRootBreaks, tc.expectRootBreaks, diff)
			}

			hashBefore := ComputeContainerSpecHash(baseRoot)
			hashAfter := ComputeContainerSpecHash(desiredRoot)

			if tc.expectRootBreaks {
				if hashBefore == hashAfter {
					t.Errorf("field %q is Breaking-on-root in DiffCell but ComputeContainerSpecHash is unchanged — apply.DiffCell would force a recreate while StartCell silently resumes a stale snapshot; before=%q after=%q",
						tc.name, hashBefore, hashAfter)
				}
			} else {
				if hashBefore != hashAfter {
					t.Errorf("field %q is Compatible in DiffCell but ComputeContainerSpecHash changed — StartCell would refuse to resume a record the apply layer would have updated in place; before=%q after=%q",
						tc.name, hashBefore, hashAfter)
				}
			}
		})
	}
}
