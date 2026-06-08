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

package runner

import (
	"testing"

	intmodel "github.com/eminwux/kukeon/internal/modelhub"
)

// TestContainerSpecChanged_OCIBakedFields pins issue #1154's non-root side:
// the UpdateCell recreate gate must fire for every OCI-baked field the runner
// resolves at container create and never re-resolves on the in-place
// task-restart path. A secrets-only edit on a workload (non-root) container
// must recreate it so resolveSecrets re-runs and the new env var reaches the
// running OCI Process.Env — the symptom #1154 fixes.
func TestContainerSpecChanged_OCIBakedFields(t *testing.T) {
	base := intmodel.ContainerSpec{
		Image:   "busybox:latest",
		Command: "sleep",
		Args:    []string{"60"},
	}

	cases := []struct {
		name string
		mut  func(s *intmodel.ContainerSpec)
		want bool
	}{
		// Pre-existing gate fields — must still trigger a recreate.
		{"image", func(s *intmodel.ContainerSpec) { s.Image = "busybox:1.36" }, true},
		{"command", func(s *intmodel.ContainerSpec) { s.Command = "echo" }, true},
		{"args", func(s *intmodel.ContainerSpec) { s.Args = []string{"120"} }, true},
		// Fields newly gated by #1154 — must trigger a recreate.
		{"workingDir", func(s *intmodel.ContainerSpec) { s.WorkingDir = "/opt/app" }, true},
		{"securityOpts", func(s *intmodel.ContainerSpec) { s.SecurityOpts = []string{"no-new-privileges"} }, true},
		{"volumes", func(s *intmodel.ContainerSpec) {
			s.Volumes = []intmodel.VolumeMount{{Source: "/host", Target: "/cell"}}
		}, true},
		{"secretsFromEnv", func(s *intmodel.ContainerSpec) {
			s.Secrets = []intmodel.ContainerSecret{{Name: "tok", FromEnv: "CLAUDE_CODE_OAUTH_TOKEN"}}
		}, true},
		{"secretsRef", func(s *intmodel.ContainerSpec) {
			s.Secrets = []intmodel.ContainerSecret{{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "default"}}}
		}, true},
		// Compatible field — must NOT trigger a recreate (rebuilt at start).
		{"env", func(s *intmodel.ContainerSpec) { s.Env = []string{"FOO=bar"} }, false},
		// No diff — must NOT trigger a recreate.
		{"identical", func(_ *intmodel.ContainerSpec) {}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desired := base
			tc.mut(&desired)
			if got := containerSpecChanged(&desired, &base); got != tc.want {
				t.Errorf("containerSpecChanged(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestContainerSpecChanged_SecretRefScopeChange guards the SecretRef
// pointer-value comparison: a scope-only change (Realm default→prod, same
// ref name) must still recreate so the daemon re-resolves against the new
// scope coordinate. Mirrors the diff layer's secretRefEqual semantics.
func TestContainerSpecChanged_SecretRefScopeChange(t *testing.T) {
	actual := intmodel.ContainerSpec{
		Image:   "busybox:latest",
		Secrets: []intmodel.ContainerSecret{{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "default"}}},
	}
	desired := actual
	desired.Secrets = []intmodel.ContainerSecret{{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "prod"}}}

	if !containerSpecChanged(&desired, &actual) {
		t.Error("expected containerSpecChanged=true for a SecretRef scope change")
	}
}
