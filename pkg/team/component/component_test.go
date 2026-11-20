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

package component_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/component"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/team/components"
)

// TestBundledLibrary locks the components kuke ships: the GitHub Issues forge
// builds on the catalog's GitHub connector without a binding of its own, and
// does not load without the catalog.
func TestBundledLibrary(t *testing.T) {
	if _, err := component.Load(components.FS); err == nil ||
		!strings.Contains(err.Error(), `needs unknown component "github"`) {
		t.Fatalf("the bundled library loaded without the connector catalog: %v", err)
	}
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	lib, err := component.Load(components.FS, catalog.Components()...)
	if err != nil {
		t.Fatal(err)
	}
	find := func(name string) component.Component {
		t.Helper()
		for _, c := range lib {
			if c.Metadata.Name == name {
				return c
			}
		}
		t.Fatalf("component %q is not in the library: %+v", name, lib)
		return component.Component{}
	}
	github, issues := find("github"), find("github-issues")
	if github.Spec.Kind != api.ComponentKindConnector || !slices.Equal(github.Spec.Requires, []string{"gh"}) ||
		len(github.Spec.Needs) != 0 || len(github.Spec.Environment) != 0 || len(github.Spec.Parameters) != 0 {
		t.Fatalf("github connector = %+v", github.Spec)
	}
	if len(github.Spec.Secrets) != 1 {
		t.Fatalf("github secrets = %+v", github.Spec.Secrets)
	}
	token := github.Spec.Secrets[0]
	if token.Name != "github-token" || token.EnvName != "GH_TOKEN" || !token.Required ||
		token.SecretName != "gh-token" || !slices.Equal(token.Aliases, []string{"gh-token"}) {
		t.Fatalf("github token slot = %+v", token)
	}
	if issues.Spec.Kind != api.ComponentKindForge || !slices.Equal(issues.Spec.Requires, []string{"gh"}) ||
		!slices.Equal(issues.Spec.Needs, []string{"github"}) || len(issues.Spec.Secrets) != 0 {
		t.Fatalf("github-issues forge = %+v", issues.Spec)
	}
}

func document(name, spec string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(
		"apiVersion: kuketeams.io/v1\nkind: Component\nmetadata:\n  name: " + name + "\nspec:\n" + spec,
	)}
}

func TestLoadOrdersConnectorsBeforeForges(t *testing.T) {
	lib, err := component.Load(fstest.MapFS{
		"a-forge/component.yaml":   document("a-forge", "  kind: forge\n  needs: [z-conn]\n"),
		"z-conn/component.yaml":    document("z-conn", "  kind: connector\n"),
		"b-conn/component.yaml":    document("b-conn", "  kind: connector\n  requires: [curl]\n"),
		"m-forge/component.yaml":   document("m-forge", "  kind: forge\n"),
		"README.md":                {Data: []byte("not a component")},
		"nested/x/component.yaml":  {Data: []byte("ignored: deeper than one directory")},
		"a-forge/notes/other.yaml": {Data: []byte("ignored")},
	})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range lib {
		names = append(names, c.Metadata.Name)
	}
	if want := []string{"b-conn", "z-conn", "a-forge", "m-forge"}; !slices.Equal(names, want) {
		t.Fatalf("order = %v, want %v", names, want)
	}
	if empty, emptyErr := component.Load(nil); emptyErr != nil || empty != nil {
		t.Fatalf("nil source: %v %v", empty, emptyErr)
	}
	if empty, emptyErr := component.Load(fstest.MapFS{}); emptyErr != nil || len(empty) != 0 {
		t.Fatalf("empty source: %v %v", empty, emptyErr)
	}
}

func TestLoadRejectsInvalidLibraries(t *testing.T) {
	valid := document("tracker", "  kind: connector\n")
	for _, tc := range []struct {
		name string
		fsys fstest.MapFS
		want string
	}{
		{"harness-kind", fstest.MapFS{"tracker/component.yaml": document("tracker", "  kind: harness\n")}, "spec.kind: expected connector or forge"},
		{"missing-kind", fstest.MapFS{"tracker/component.yaml": document("tracker", "  description: none\n")}, "spec.kind"},
		{"wrong-document-kind", fstest.MapFS{"tracker/component.yaml": {Data: []byte(
			"apiVersion: kuketeams.io/v1\nkind: Harness\nmetadata: {name: tracker}\nspec: {kind: connector}\n",
		)}}, "require kuketeams.io/v1 Component"},
		{"wrong-api-version", fstest.MapFS{"tracker/component.yaml": {Data: []byte(
			"apiVersion: v1beta1\nkind: Component\nmetadata: {name: tracker}\nspec: {kind: connector}\n",
		)}}, "require kuketeams.io/v1 Component"},
		{"invalid-name", fstest.MapFS{"Tracker/component.yaml": document("Tracker", "  kind: connector\n")}, "valid metadata.name"},
		{"name-differs-from-directory", fstest.MapFS{"tracker/component.yaml": document("other", "  kind: connector\n")}, "metadata.name must match its directory"},
		{"needs-unknown", fstest.MapFS{"tracker/component.yaml": document("tracker", "  kind: forge\n  needs: [absent]\n")}, `needs unknown component "absent"`},
		{"needs-itself", fstest.MapFS{"tracker/component.yaml": document("tracker", "  kind: forge\n  needs: [tracker]\n")}, `needs unknown component "tracker"`},
		{"duplicate-requires", fstest.MapFS{"tracker/component.yaml": document("tracker", "  kind: connector\n  requires: [gh, gh]\n")}, `duplicate or invalid name "gh"`},
		{"invalid-requires", fstest.MapFS{"tracker/component.yaml": document("tracker", "  kind: connector\n  requires: [\"Bad Cap\"]\n")}, "duplicate or invalid name"},
		{"duplicate-parameter", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  parameters:\n    - {name: URL}\n    - {name: URL}\n")}, "spec.parameters: invalid or duplicate name"},
		{"invalid-parameter", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  parameters:\n    - {name: lower}\n")}, "spec.parameters: invalid or duplicate name"},
		{"invalid-environment", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  environment: {lower-case: x}\n")}, "spec.environment: invalid name"},
		{
			"secret-collides-with-environment",
			fstest.MapFS{"tracker/component.yaml": document("tracker",
				"  kind: connector\n  environment: {TRACKER_TOKEN: literal}\n  secrets:\n    - {name: token, envName: TRACKER_TOKEN}\n")},
			`secret "token": invalid or duplicate envName`,
		},
		{
			"two-secrets-one-destination",
			fstest.MapFS{"tracker/component.yaml": document("tracker",
				"  kind: connector\n  secrets:\n    - {name: first, envName: TOKEN}\n    - {name: second, envName: TOKEN}\n")},
			`secret "second": invalid or duplicate envName`,
		},
		{
			"duplicate-secret-name",
			fstest.MapFS{"tracker/component.yaml": document("tracker",
				"  kind: connector\n  secrets:\n    - {name: token, envName: A}\n    - {name: token, envName: B}\n")},
			`invalid or duplicate secret name "token"`,
		},
		{"secret-without-destination", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  secrets:\n    - {name: token}\n")}, "invalid or duplicate envName"},
		{"invalid-secret-name-default", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  secrets:\n    - {name: token, envName: A, secretName: \"Bad Name\"}\n")}, "invalid secretName"},
		{"invalid-alias", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  secrets:\n    - {name: token, envName: A, aliases: [\"Bad Alias\"]}\n")}, "invalid alias"},
		// A component never declares a git credential: the schema has no field for one.
		{"git-credential-field", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  git:\n    credentials: []\n")}, "field git not found"},
		{"mount-secret", fstest.MapFS{"tracker/component.yaml": document("tracker",
			"  kind: connector\n  secrets:\n    - {name: token, envName: A, mountPath: /run/token}\n")}, "field mountPath not found"},
		{
			"extra-document",
			fstest.MapFS{"tracker/component.yaml": {Data: append(slices.Clone(valid.Data), []byte("---\nkind: Component\n")...)}},
			"expected exactly one YAML document",
		},
		{"one-bad-of-two", fstest.MapFS{
			"tracker/component.yaml": valid,
			"broken/component.yaml":  document("broken", "  kind: nonsense\n"),
		}, "broken/component.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lib, err := component.Load(tc.fsys)
			if err == nil || lib != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("lib=%v err=%v, want %q", lib, err, tc.want)
			}
		})
	}
}
