// Copyright 2026 Emiliano Spinella (eminwux)
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

package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/get/source"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	"github.com/spf13/viper"
)

const kukeonDoc = "apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: kukeon\n  realm: default\n" +
	"spec:\n  url: github.com/eminwux/kukeon\n  branch: main\n"

type fakeClient struct {
	kukeonv1.FakeClient

	sources  []kukeonv1.GetSourceResult
	gotRealm string
	gotRef   kukeonv1.SourceRef
}

func (f *fakeClient) ListSources(_ context.Context, realm string) ([]kukeonv1.GetSourceResult, error) {
	f.gotRealm = realm
	return f.sources, nil
}

func (f *fakeClient) GetSource(_ context.Context, ref kukeonv1.SourceRef) (kukeonv1.GetSourceResult, error) {
	f.gotRef = ref
	for _, s := range f.sources {
		if s.Summary.Name == ref.Name && s.Summary.Realm == ref.Realm {
			return s, nil
		}
	}
	return kukeonv1.GetSourceResult{}, nil
}

func stored() *fakeClient {
	return &fakeClient{sources: []kukeonv1.GetSourceResult{
		{
			Exists: true, Document: []byte(kukeonDoc),
			Summary: kukeonv1.SourceSummary{
				Name: "kukeon", Realm: "default", URL: "github.com/eminwux/kukeon", Ref: "branch main",
			},
		},
		{
			Exists: true, Document: []byte("apiVersion: v1beta1\nkind: Source\nmetadata:\n  name: agents\n"),
			Summary: kukeonv1.SourceSummary{
				Name: "agents", Realm: "default", URL: "git@github.com:eminwux/agents", Ref: "tag v2", Identity: "gh",
			},
		},
	}}
}

func run(t *testing.T, fake *fakeClient, args ...string) (string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)
	cmd := source.NewSourceCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetContext(context.WithValue(context.Background(), source.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// TestGetSources pins AC1's list: name, address, ref and identity.
func TestGetSources(t *testing.T) {
	fake := stored()
	out, err := run(t, fake)
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotRealm != "" {
		t.Errorf("realm = %q, want every realm", fake.gotRealm)
	}
	for _, want := range []string{
		"NAME", "ADDRESS", "REF", "IDENTITY",
		"kukeon", "github.com/eminwux/kukeon", "branch main",
		"agents", "git@github.com:eminwux/agents", "tag v2", "gh",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestGetSourceYAMLIsTheStoredDocument(t *testing.T) {
	fake := stored()
	out, err := run(t, fake, "kukeon", "-o", "yaml")
	if err != nil || out != kukeonDoc {
		t.Fatalf("-o yaml = %q, %v", out, err)
	}
	if fake.gotRef != (kukeonv1.SourceRef{Name: "kukeon", Realm: "default"}) {
		t.Fatalf("ref = %+v", fake.gotRef)
	}
}

func TestGetSourcesJSON(t *testing.T) {
	out, err := run(t, stored(), "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var got []kukeonv1.SourceSummary
	if err = json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode %q: %v", out, err)
	}
	if len(got) != 2 || got[1].Identity != "gh" || got[0].Ref != "branch main" {
		t.Fatalf("json = %+v", got)
	}
}

func TestGetSourceMissingAndEmpty(t *testing.T) {
	if _, err := run(t, stored(), "nothing"); err == nil || !strings.Contains(err.Error(), "kuke get sources") {
		t.Fatalf("err = %v", err)
	}
	out, err := run(t, &fakeClient{})
	if err != nil || !strings.Contains(out, "No sources found.") {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}
