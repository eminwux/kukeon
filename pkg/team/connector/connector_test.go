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

package connector_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
)

func document(name, spec string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(
		"apiVersion: v1beta1\nkind: Connector\nmetadata:\n  name: " + name + "\nspec:\n" + spec,
	)}
}

const envMethod = `  title: T
  identity: {secretTypes: [opaque]}
  methods:
    - name: token
      default: true
      delivery: env
      secret: {name: %s, envName: SOME_TOKEN}
`

// TestEmbeddedCatalog locks what the catalog kuke ships must provide: every
// harness kuke bundles an auth connector for has a login default that needs no
// identity, and the two handler-delivered connectors exist.
func TestEmbeddedCatalog(t *testing.T) {
	catalog, err := connector.Default()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Connectors) == 0 || !slices.IsSorted(catalog.Names()) {
		t.Fatalf("catalog names = %v", catalog.Names())
	}
	deliveries := map[string]int{}
	// A component name identifies one connector method in a blueprint.
	components := map[string]string{}
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		name := conn.Metadata.Name
		if len(conn.Spec.Methods) == 0 {
			deliveries[conn.Spec.Delivery]++
			desc := conn.Descriptor(connector.Method{})
			if desc.Name != name || desc.Delivery != conn.Spec.Delivery || len(desc.SecretTypes) == 0 {
				t.Fatalf("%s: descriptor = %+v", name, desc)
			}
			continue
		}
		method, ok := conn.Method("")
		if !ok {
			t.Fatalf("%s has no default method", name)
		}
		if conn.Spec.Harness != "" {
			// A harness's default auth is the login it already had, so a
			// team that chooses nothing renders the cell it always did.
			if method.Delivery != api.ConnectorDeliveryStateVolume || method.Secret != nil {
				t.Fatalf("%s: default method %q is not a state-volume login", name, method.Name)
			}
			if found, isHarness := catalog.ForHarness(conn.Spec.Harness); !isHarness || found != conn {
				t.Fatalf("%s: ForHarness(%q) = %v", name, conn.Spec.Harness, found)
			}
		}
		for _, m := range conn.Spec.Methods {
			component := conn.ComponentName(m)
			if other, dup := components[component]; dup {
				t.Fatalf("%s/%s: component name %q is also %s", name, m.Name, component, other)
			}
			components[component] = name + "/" + m.Name
			desc := conn.Descriptor(m)
			if desc.Name != name || desc.Method != m.Name || desc.Delivery != m.Delivery {
				t.Fatalf("%s/%s: descriptor = %+v", name, m.Name, desc)
			}
		}
	}
	if deliveries[api.ConnectorDeliveryGit] != 1 || deliveries[api.ConnectorDeliveryRegistry] != 1 {
		t.Fatalf("handler-delivered connectors = %v", deliveries)
	}
	if _, ok := catalog.ForHarness("no-such-harness"); ok {
		t.Fatal("ForHarness matched an unknown harness")
	}
}

// TestCatalogComponents: one component per method, grouped so the methods of
// one connector exclude each other; a git-delivered connector is one ungrouped
// component; a registry connector renders none.
func TestCatalogComponents(t *testing.T) {
	catalog := connectortest.Catalog(t)
	byName := map[string]int{}
	components := catalog.Components()
	for i, c := range components {
		if c.Spec.Kind != api.ComponentKindConnector || c.Spec.Connector == nil {
			t.Fatalf("component %s = %+v", c.Metadata.Name, c.Spec)
		}
		if _, dup := byName[c.Metadata.Name]; dup {
			t.Fatalf("component %s rendered twice", c.Metadata.Name)
		}
		byName[c.Metadata.Name] = i
		if err := c.Validate(); err != nil {
			t.Fatalf("component %s: %v", c.Metadata.Name, err)
		}
	}
	for i := range catalog.Connectors {
		conn := &catalog.Connectors[i]
		switch {
		case len(conn.Spec.Methods) > 1:
			for _, m := range conn.Spec.Methods {
				c := components[byName[conn.ComponentName(m)]]
				if c.Spec.Group != conn.Group() || c.Spec.Group == "" {
					t.Fatalf("%s: group = %q", c.Metadata.Name, c.Spec.Group)
				}
				if (m.Secret == nil) != (len(c.Spec.Secrets) == 0) {
					t.Fatalf("%s: secrets = %+v", c.Metadata.Name, c.Spec.Secrets)
				}
			}
		case len(conn.Spec.Methods) == 0 && conn.Spec.Delivery == api.ConnectorDeliveryRegistry:
			if _, rendered := byName[conn.Metadata.Name]; rendered {
				t.Fatalf("registry connector %s rendered a component", conn.Metadata.Name)
			}
		case len(conn.Spec.Methods) == 0:
			c := components[byName[conn.Metadata.Name]]
			if c.Spec.Group != "" || len(c.Spec.Secrets) != 0 {
				t.Fatalf("git-delivered component = %+v", c.Spec)
			}
		}
	}
	// The test-only connector: a single method, so no exclusion group, and a
	// required slot named by the file.
	c := components[byName[connectortest.Name+"-token"]]
	if c.Spec.Group != "" || len(c.Spec.Secrets) != 1 || c.Spec.Secrets[0].Name != connectortest.Slot ||
		c.Spec.Secrets[0].EnvName != connectortest.EnvName || !c.Spec.Secrets[0].Required ||
		c.Spec.Connector.Fields[0].Name != connectortest.Field {
		t.Fatalf("test connector component = %+v", c.Spec)
	}
}

func TestLoadRejections(t *testing.T) {
	valid := func(slot string) string { return strings.Replace(envMethod, "%s", slot, 1) }
	for _, tc := range []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{
			"name must match directory",
			fstest.MapFS{"a/connector.yaml": document("b", valid("a-token"))},
			"metadata.name must match its directory",
		},
		{
			"duplicate slot",
			fstest.MapFS{
				"a/connector.yaml": document("a", valid("shared")),
				"b/connector.yaml": document("b", valid("shared")),
			},
			`secret name "shared" is already used by connector "a"`,
		},
		{
			"duplicate component",
			fstest.MapFS{
				"a/connector.yaml": document("a", strings.Replace(valid("a-token"), "name: token", "name: token\n      component: same", 1)),
				"b/connector.yaml": document("b", strings.Replace(valid("b-token"), "name: token", "name: token\n      component: same", 1)),
			},
			`component "same" is already defined by connector "a"`,
		},
		{
			"unknown field",
			fstest.MapFS{"a/connector.yaml": document("a", valid("a-token")+"  surprise: true\n")},
			"surprise",
		},
		{
			"no title",
			fstest.MapFS{"a/connector.yaml": document("a", "  identity: {secretTypes: [opaque]}\n  delivery: git\n")},
			"spec.title is required",
		},
		{
			"no secret types",
			fstest.MapFS{"a/connector.yaml": document("a", "  title: T\n  delivery: git\n")},
			"spec.identity.secretTypes",
		},
		{
			"no methods and no handler",
			fstest.MapFS{"a/connector.yaml": document("a", "  title: T\n  identity: {secretTypes: [opaque]}\n")},
			"delivered by git or registry",
		},
		{
			"delivery next to methods",
			fstest.MapFS{"a/connector.yaml": document("a", valid("a-token")+"  delivery: git\n")},
			"states the delivery on each method",
		},
		{
			"no default method",
			fstest.MapFS{"a/connector.yaml": document("a", strings.Replace(valid("a-token"), "      default: true\n", "", 1))},
			"exactly one method must be the default",
		},
		{
			"env method without envName",
			fstest.MapFS{"a/connector.yaml": document("a", strings.Replace(valid("a-token"), ", envName: SOME_TOKEN", "", 1))},
			"an env method requires envName",
		},
		{
			"file method with a relative path",
			fstest.MapFS{"a/connector.yaml": document("a", strings.Replace(
				strings.Replace(valid("a-token"), "delivery: env", "delivery: file", 1),
				"envName: SOME_TOKEN", "mountPath: relative/path", 1))},
			"a file method requires an absolute mountPath",
		},
		{
			"login with a secret",
			fstest.MapFS{"a/connector.yaml": document("a", strings.Replace(valid("a-token"), "delivery: env", "delivery: stateVolume", 1))},
			"a stateVolume method keeps a login and declares no secret",
		},
		{
			"unknown delivery",
			fstest.MapFS{"a/connector.yaml": document("a", strings.Replace(valid("a-token"), "delivery: env", "delivery: carrier-pigeon", 1))},
			"expected env, file or stateVolume",
		},
		{
			"harness on a handler connector",
			fstest.MapFS{"a/connector.yaml": document("a", "  title: T\n  identity: {secretTypes: [ssh-key]}\n  delivery: git\n  harness: codex\n")},
			"only a connector with methods authenticates a harness",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog, err := connector.Load(tc.files)
			if err == nil || catalog != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("catalog=%v err=%v, want %q", catalog, err, tc.want)
			}
		})
	}
	if catalog, err := connector.Load(nil); err != nil || len(catalog.Connectors) != 0 {
		t.Fatalf("nil source: %v %v", catalog, err)
	}
}

func TestMethodLookup(t *testing.T) {
	catalog := connectortest.Catalog(t)
	conn, _ := catalog.Find(connectortest.Name)
	if method, ok := conn.Method(""); !ok || method.Name != "token" {
		t.Fatalf("default method = %+v %v", method, ok)
	}
	if _, ok := conn.Method("nope"); ok {
		t.Fatal("unknown method resolved")
	}
	if !slices.Equal(conn.MethodNames(), []string{"token"}) || conn.Group() != connectortest.Name {
		t.Fatalf("methods=%v group=%q", conn.MethodNames(), conn.Group())
	}
	var none *connector.Catalog
	if _, ok := none.Find("x"); ok || none.Names() != nil || none.Components() != nil {
		t.Fatal("a nil catalog is not empty")
	}
}
