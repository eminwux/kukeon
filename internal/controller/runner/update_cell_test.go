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

// TestContainerSpecChanged_OCIBakedFields pins issues #1154 and #1368's
// non-root recreate gates:
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
			s.Secrets = []intmodel.ContainerSecret{
				{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "default"}},
			}
		}, true},
		// Compatible env changes recreate only workload containers because the
		// OCI Process.Env is fixed at create and isn't refreshed by task restart.
		{"env", func(s *intmodel.ContainerSpec) { s.Env = []string{"FOO=bar"} }, true},
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
		Image: "busybox:latest",
		Secrets: []intmodel.ContainerSecret{
			{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "default"}},
		},
	}
	desired := actual
	desired.Secrets = []intmodel.ContainerSecret{
		{Name: "db", SecretRef: &intmodel.ContainerSecretRef{Name: "creds", Realm: "prod"}},
	}

	if !containerSpecChanged(&desired, &actual) {
		t.Error("expected containerSpecChanged=true for a SecretRef scope change")
	}
}

// TestContainerSpecChanged_GitMountAppears guards the #1472 recreate gate: the
// staged git directory is an OCI mount added at container create, so a git
// declaration appearing or disappearing — on the container or on any of its
// repos — must recreate. An edit inside an existing declaration must not: the
// bundle is re-rendered from the spec on every start.
func TestContainerSpecChanged_GitMountAppears(t *testing.T) {
	ref := func(name string) *intmodel.ContainerSecretRef {
		return &intmodel.ContainerSecretRef{Name: name, Realm: "default"}
	}
	plainRepo := intmodel.ContainerRepo{Name: "src", URL: "git@github.com:o/r.git", Target: "/src"}
	gitRepo := plainRepo
	gitRepo.Git = &intmodel.RepoGit{Credential: &intmodel.GitCredential{SecretRef: ref("repo-key")}}
	gitBlock := func(secret string) *intmodel.ContainerGit {
		return &intmodel.ContainerGit{
			Author:      &intmodel.GitIdentity{Name: "Dev", Email: "dev@example.com"},
			Credentials: []intmodel.GitCredential{{Host: "github.com", SecretRef: ref(secret)}},
		}
	}
	spec := func(git *intmodel.ContainerGit, repos ...intmodel.ContainerRepo) intmodel.ContainerSpec {
		return intmodel.ContainerSpec{Image: "busybox:latest", Command: "sleep", Git: git, Repos: repos}
	}

	cases := []struct {
		name            string
		desired, actual intmodel.ContainerSpec
		want            bool
	}{
		{"container git added", spec(gitBlock("k")), spec(nil), true},
		{"container git removed", spec(nil), spec(gitBlock("k")), true},
		{"repo git added", spec(nil, gitRepo), spec(nil, plainRepo), true},
		{"repo git removed", spec(nil, plainRepo), spec(nil, gitRepo), true},
		// The mount already exists either way: no recreate.
		{"credential edited", spec(gitBlock("rotated")), spec(gitBlock("k")), false},
		{"repo git added next to container git", spec(gitBlock("k"), gitRepo), spec(gitBlock("k"), plainRepo), false},
		{"container git replaced by repo git", spec(nil, gitRepo), spec(gitBlock("k"), plainRepo), false},
		{"plain repo added, no git anywhere", spec(nil, plainRepo), spec(nil), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := containerSpecChanged(&tc.desired, &tc.actual); got != tc.want {
				t.Errorf("containerSpecChanged = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApplyCompatibleCellFields is case 8 of issue #1569: UpdateCell persists
// the cell-level fields DiffCell routes to it as Compatible (spec.autoDelete,
// spec.tty), and the render provenance only when desired carries one, so a
// hand-written Cell document does not wipe the Config lineage record.
func TestApplyCompatibleCellFields(t *testing.T) {
	liveProv := &intmodel.CellProvenance{
		BindingKind: "config",
		BindingRef:  intmodel.CellBindingRef{Name: "cfg", Realm: "default"},
		Params:      map[string]string{"CMD": "old"},
	}
	freshProv := &intmodel.CellProvenance{
		BindingKind: "config",
		BindingRef:  intmodel.CellBindingRef{Name: "cfg", Realm: "default"},
		Params:      map[string]string{"CMD": "new"},
	}

	cases := []struct {
		name       string
		desired    intmodel.CellSpec
		wantAuto   bool
		wantTty    string
		wantParams string
	}{
		{
			name: "autoDelete and tty land; provenance replaced",
			desired: intmodel.CellSpec{
				AutoDelete: true,
				Tty:        &intmodel.CellTty{Default: "work"},
				Provenance: freshProv,
			},
			wantAuto:   true,
			wantTty:    "work",
			wantParams: "new",
		},
		{
			name:       "desired without provenance keeps the live record",
			desired:    intmodel.CellSpec{AutoDelete: true},
			wantAuto:   true,
			wantParams: "old",
		},
		{
			name:       "autoDelete and tty cleared",
			desired:    intmodel.CellSpec{Provenance: freshProv},
			wantParams: "new",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			existing := intmodel.Cell{Spec: intmodel.CellSpec{
				AutoDelete: !tc.wantAuto,
				Tty:        &intmodel.CellTty{Default: "stale"},
				Provenance: intmodel.CloneCellProvenance(liveProv),
			}}
			applyCompatibleCellFields(&existing, intmodel.Cell{Spec: tc.desired})
			if existing.Spec.AutoDelete != tc.wantAuto {
				t.Errorf("AutoDelete = %v, want %v", existing.Spec.AutoDelete, tc.wantAuto)
			}
			gotTty := ""
			if existing.Spec.Tty != nil {
				gotTty = existing.Spec.Tty.Default
			}
			if gotTty != tc.wantTty {
				t.Errorf("Tty.Default = %q, want %q", gotTty, tc.wantTty)
			}
			if existing.Spec.Provenance == nil || existing.Spec.Provenance.Params["CMD"] != tc.wantParams {
				t.Errorf("Provenance = %+v, want params CMD=%s", existing.Spec.Provenance, tc.wantParams)
			}
			if tc.desired.Provenance != nil && existing.Spec.Provenance == tc.desired.Provenance {
				t.Error("provenance aliased to desired; want a copy")
			}
		})
	}
}
