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

package build

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/spf13/cobra"
)

func withRealm(t *testing.T, images []v1beta1.RealmImage) {
	t.Helper()
	orig := lookupRealm
	lookupRealm = func(_ *cobra.Command, realm string) (kukeonv1.GetRealmResult, error) {
		return kukeonv1.GetRealmResult{
			MetadataExists: true,
			Realm: v1beta1.RealmDoc{
				Metadata: v1beta1.RealmMetadata{Name: realm},
				Spec:     v1beta1.RealmSpec{Images: images},
			},
		}, nil
	}
	t.Cleanup(func() { lookupRealm = orig })
}

// TestRunBuildRealmImage: --realm-image fills the tag, Dockerfile, build args
// and context from the realm's spec.images entry (#1418).
func TestRunBuildRealmImage(t *testing.T) {
	withRealm(t, []v1beta1.RealmImage{{Name: "agent", Build: &v1beta1.RealmImageBuild{
		Context:    "/src/agent",
		Dockerfile: "/src/agent/Dockerfile.agent",
		Args:       map[string]string{"VERSION": "1", "BASE": "alpine"},
	}}})
	var gotArgv []string
	withStubs(t,
		func(string) (string, error) { return "/usr/local/bin/kukebuild", nil },
		func(_ string, argv []string, _ []string) error { gotArgv = argv; return nil },
	)

	cmd := NewBuildCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--realm", "team", "--realm-image", "agent"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{
		"kukebuild",
		"--tag", "kukeon.internal/team/agent:latest",
		"--realm", "team",
		"--file", "/src/agent/Dockerfile.agent",
		"--build-arg", "BASE=alpine",
		"--build-arg", "VERSION=1",
		"/src/agent",
	}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("argv = %v, want %v", gotArgv, want)
	}
}

func TestRunBuildRealmImageErrors(t *testing.T) {
	withRealm(t, []v1beta1.RealmImage{{Name: "base", Ref: "docker.io/library/alpine:3.20"}})
	withStubs(t,
		func(string) (string, error) { return "/usr/local/bin/kukebuild", nil },
		func(string, []string, []string) error { t.Fatal("execHandoff must not run"); return nil },
	)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--realm-image", "missing"}, "declares no image"},
		{[]string{"--realm-image", "base"}, "not a Dockerfile build"},
		{[]string{"--realm-image", "base", "-t", "x:1", "/ctx"}, "mutually exclusive"},
	} {
		cmd := NewBuildCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(tc.args)
		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("args %v: error = %v, want one containing %q", tc.args, err, tc.want)
		}
	}
}
