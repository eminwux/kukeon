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

package teamdefaults_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/get/teamdefaults"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

const defaultsDoc = `apiVersion: v1beta1
kind: TeamDefaults
metadata:
  name: default
  realm: default
spec:
  defaults:
    sources:
      - name: kukeon
        url: https://github.com/eminwux/kukeon
        branch: main
      - name: agents
        url: https://github.com/eminwux/agents
        tag: v2
        clonePath: /home/user/agents
    roles:
      - {name: dev, source: agents, path: dev}
    harnesses: []
`

type fakeClient struct {
	kukeonv1.FakeClient

	result   kukeonv1.GetTeamDefaultsResult
	gotRealm string
}

func (f *fakeClient) GetTeamDefaults(_ context.Context, realm string) (kukeonv1.GetTeamDefaultsResult, error) {
	f.gotRealm = realm
	return f.result, nil
}

func stored() *fakeClient {
	return &fakeClient{result: kukeonv1.GetTeamDefaultsResult{
		Exists: true, Realm: "default", Document: []byte(defaultsDoc),
	}}
}

func run(t *testing.T, fake *fakeClient, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := teamdefaults.NewTeamDefaultsCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), teamdefaults.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestGetTeamDefaults pins AC5: the registered sources and the default roles
// and harnesses.
func TestGetTeamDefaults(t *testing.T) {
	fake := stored()
	out, err := run(t, fake)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotRealm != "default" {
		t.Errorf("realm = %q, want default", fake.gotRealm)
	}
	for _, want := range []string{
		"SOURCE", "URL", "REF", "CLONE PATH",
		"kukeon", "branch main", "agents", "tag v2", "/home/user/agents",
		"Default roles:     dev=agents:dev", "Default harnesses: none",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestGetTeamDefaultsYAMLIsTheStoredDocument(t *testing.T) {
	out, err := run(t, stored(), "-o", "yaml")
	if err != nil || out != defaultsDoc {
		t.Fatalf("-o yaml = %q, %v", out, err)
	}
}

func TestGetTeamDefaultsJSON(t *testing.T) {
	out, err := run(t, stored(), "-o", "json", "--realm", "other")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Sources []struct {
			Name string `json:"name"`
			Ref  string `json:"ref"`
		} `json:"sources"`
		Roles     []string `json:"roles"`
		Harnesses []string `json:"harnesses"`
	}
	if err = json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(got.Sources) != 2 || got.Sources[1].Ref != "tag v2" || len(got.Roles) != 1 ||
		got.Harnesses == nil || len(got.Harnesses) != 0 {
		t.Fatalf("json = %+v", got)
	}
}

func TestGetTeamDefaultsMissing(t *testing.T) {
	out, err := run(t, &fakeClient{})
	if err != nil || !strings.Contains(out, "stores no TeamDefaults") || !strings.Contains(out, "kuke team init") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
	if _, err = run(t, &fakeClient{}, "-o", "yaml"); err == nil || !strings.Contains(err.Error(), "kuke team init") {
		t.Fatalf("-o yaml on a missing TeamDefaults err = %v", err)
	}
}
