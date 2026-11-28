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
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	teamconnector "github.com/eminwux/kukeon/pkg/team/connector"
)

// descriptor is a connector with one field of every type, so the rules are
// exercised without naming a real connector.
func descriptor() v1beta1.BlueprintConnector {
	return v1beta1.BlueprintConnector{
		Name:        "sample",
		Delivery:    v1beta1.ConnectorDeliveryEnv,
		SecretTypes: []string{v1beta1.SecretTypeSSHKey, v1beta1.SecretTypeBasicAuth},
		Scopes:      []string{v1beta1.ConnectorScopeRealm, v1beta1.ConnectorScopeSpace},
		Fields: []v1beta1.ConnectorField{
			{Name: "owner", Type: v1beta1.ConnectorFieldPerson},
			{Name: "hosts", Type: v1beta1.ConnectorFieldList, Required: true},
			{
				Name: "sign", Type: v1beta1.ConnectorFieldList, Enum: []string{"commits", "tags"},
				SecretTypes: []string{v1beta1.SecretTypeSSHKey},
			},
			{Name: "verifier", Type: v1beta1.ConnectorFieldSecret},
			{Name: "tier", Enum: []string{"free", "paid"}},
		},
	}
}

func TestValidateDescriptor(t *testing.T) {
	if err := connector.ValidateDescriptor(descriptor()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*v1beta1.BlueprintConnector)
		want string
	}{
		{"name", func(d *v1beta1.BlueprintConnector) { d.Name = "Not Valid" }, "must be a lowercase identifier"},
		{"delivery", func(d *v1beta1.BlueprintConnector) { d.Delivery = "pigeon" }, "delivery \"pigeon\""},
		{"secret type", func(d *v1beta1.BlueprintConnector) { d.SecretTypes = []string{"x509"} }, "unknown secret type"},
		{"scope", func(d *v1beta1.BlueprintConnector) { d.Scopes = []string{"cell"} }, "must be realm, space or stack"},
		{"field name", func(d *v1beta1.BlueprintConnector) { d.Fields[0].Name = "has space" }, "is not an identifier"},
		{"duplicate field", func(d *v1beta1.BlueprintConnector) { d.Fields[1].Name = "owner" }, "declared twice"},
		{"field type", func(d *v1beta1.BlueprintConnector) { d.Fields[0].Type = "date" }, "must be string, list, person or secret"},
		{
			"field secret type",
			func(d *v1beta1.BlueprintConnector) { d.Fields[2].SecretTypes = []string{"x509"} },
			"field \"sign\": unknown secret type",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			desc := descriptor()
			tc.edit(&desc)
			err := connector.ValidateDescriptor(desc)
			if !errors.Is(err, errdefs.ErrConnectorInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestScopesAndDelivery(t *testing.T) {
	desc := descriptor()
	if !connector.AcceptsScope(desc, v1beta1.ConnectorScopeRealm) ||
		connector.AcceptsScope(desc, v1beta1.ConnectorScopeStack) {
		t.Fatal("a connector accepts exactly the scopes it lists")
	}
	desc.Scopes = nil
	if !connector.AcceptsScope(desc, v1beta1.ConnectorScopeStack) {
		t.Fatal("a connector that lists no scope accepts every scope")
	}
	if connector.NeedsIdentity(v1beta1.ConnectorDeliveryStateVolume) ||
		!connector.NeedsIdentity(v1beta1.ConnectorDeliveryEnv) {
		t.Fatal("only a state-volume login needs no identity")
	}
}

func TestCheckSecret(t *testing.T) {
	desc := descriptor()
	tagged := func(secretType string, identity map[string]string) v1beta1.SecretSpec {
		return v1beta1.SecretSpec{Type: secretType, Connector: desc.Name, Identity: identity}
	}
	for _, tc := range []struct {
		name   string
		secret v1beta1.SecretSpec
		want   string // empty: accepted
	}{
		{"tagged and complete", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"owner": "Dev Bot <dev@example.com>", "hosts": "github.com, gitlab.com/group",
			"sign": "commits,tags", "verifier": "signers@ops/shared", "tier": "paid",
		}), ""},
		{"untagged of an accepted type", v1beta1.SecretSpec{Type: v1beta1.SecretTypeBasicAuth}, ""},
		// An untagged Secret predates connectors: its fields are not checked.
		{"untagged skips the schema", v1beta1.SecretSpec{
			Type: v1beta1.SecretTypeSSHKey, Identity: map[string]string{"anything": "goes"},
		}, ""},
		{"another connector's identity", v1beta1.SecretSpec{
			Type: v1beta1.SecretTypeSSHKey, Connector: "other",
		}, `it is an identity of connector "other", not "sample"`},
		{"wrong type", tagged(v1beta1.SecretTypeOpaque, nil), "accepts a ssh-key or basic-auth secret, not opaque"},
		{"empty type is opaque", v1beta1.SecretSpec{}, "not opaque"},
		{"missing required field", tagged(v1beta1.SecretTypeSSHKey, nil), `requires identity field "hosts"`},
		{"unknown field", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com", "nope": "x",
		}), `declares no identity field "nope"`},
		{"person", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com", "owner": "no email",
		}), "want `Name <email>`"},
		{"list entry with a space", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com, bad host",
		}), "must not contain spaces or quotes"},
		{"list enum", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com", "sign": "pushes",
		}), `"pushes" must be one of commits, tags`},
		{"string enum", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com", "tier": "gold",
		}), `"gold" must be one of free, paid`},
		{"secret reference", tagged(v1beta1.SecretTypeSSHKey, map[string]string{
			"hosts": "github.com", "verifier": "a@b/c/d/e",
		}), "want name[@realm[/space[/stack]]]"},
		{"field of another secret type", tagged(v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": "github.com", "sign": "commits",
		}), `identity field "sign" applies to a ssh-key secret, not basic-auth`},
		// An empty value is the same as an absent field.
		{"blank optional field", tagged(v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": "github.com", "sign": " ",
		}), ""},
		// A reserved key (issue #1693) needs no field declaration, so a
		// descriptor copied into a Blueprint before it existed still accepts it.
		{"token-from link", tagged(v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": "github.com", connector.IdentityTokenFrom: "github-token@default",
		}), ""},
		{"token-from that is not a reference", tagged(v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": "github.com", connector.IdentityTokenFrom: "a@b/c/d/e",
		}), `identity key "kukeon.io/token-from"`},
		{"unknown reserved key", tagged(v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": "github.com", "kukeon.io/other": "x",
		}), `identity key "kukeon.io/other" is reserved`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := connector.CheckSecret(desc, tc.secret)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, errdefs.ErrConnectorIdentity) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParsers(t *testing.T) {
	if got := connector.ParseList(" a, ,b,, c "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("list = %v", got)
	}
	if connector.ParseList(" , ") != nil {
		t.Fatal("an empty list is nil")
	}
	name, email, err := connector.ParsePerson("  Dev Bot  < dev@example.com >")
	if err != nil || name != "Dev Bot" || email != "dev@example.com" {
		t.Fatalf("person = %q %q %v", name, email, err)
	}
	if connector.FormatPerson(name, email) != "Dev Bot <dev@example.com>" {
		t.Fatal("FormatPerson is not the inverse of ParsePerson")
	}
	for _, bad := range []string{"", "Dev Bot", "<dev@example.com>", "Dev <not-an-email>"} {
		if _, _, err = connector.ParsePerson(bad); err == nil {
			t.Fatalf("person %q accepted", bad)
		}
	}
}

func TestSecretRefs(t *testing.T) {
	base := v1beta1.ContainerSecretRef{Name: "ignored", Realm: "ops", Space: "shared", Stack: "agents"}
	for value, want := range map[string]v1beta1.ContainerSecretRef{
		"key":                 {Name: "key", Realm: "ops", Space: "shared", Stack: "agents"},
		"key@lab":             {Name: "key", Realm: "lab"},
		"key@lab/team":        {Name: "key", Realm: "lab", Space: "team"},
		"key@lab/team/agents": {Name: "key", Realm: "lab", Space: "team", Stack: "agents"},
	} {
		got, err := connector.ParseSecretRef(value, base)
		if err != nil || got != want {
			t.Fatalf("%q = %+v %v, want %+v", value, got, err, want)
		}
		// The written form reads back as the same Secret.
		again, err := connector.ParseSecretRef(connector.FormatSecretRef(got), v1beta1.ContainerSecretRef{})
		if err != nil || !connector.SameSecret(got, again) {
			t.Fatalf("%q does not round-trip: %+v %v", value, again, err)
		}
	}
	// Surrounding whitespace is not part of a reference.
	if got, err := connector.ParseSecretRef(" key@lab ", base); err != nil || got.Name != "key" || got.Realm != "lab" {
		t.Fatalf("padded reference = %+v %v", got, err)
	}
	for _, bad := range []string{"", "@lab", "key@", "key@lab//agents", "key@a/b/c/d", "../key", "a b", "key@lab/.."} {
		if _, err := connector.ParseSecretRef(bad, base); err == nil {
			t.Fatalf("reference %q accepted", bad)
		}
	}
	if _, err := connector.ParseSecretRef("key", v1beta1.ContainerSecretRef{}); err == nil {
		t.Fatal("a reference without a realm was accepted")
	}
	if connector.SameSecret(
		v1beta1.ContainerSecretRef{Name: "key", Realm: "ops"},
		v1beta1.ContainerSecretRef{Name: "key", Realm: "ops", Space: "shared"},
	) {
		t.Fatal("secrets of different scopes compare equal")
	}
}

func TestParseMethods(t *testing.T) {
	got, err := connector.ParseMethods([]string{"codex=api-key", " claude = subscription-key "})
	want := map[string]string{"codex": "api-key", "claude": "subscription-key"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("methods = %+v %v", got, err)
	}
	if got, err = connector.ParseMethods(nil); got != nil || err != nil {
		t.Fatalf("no arguments = %+v %v", got, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"codex"}, "want <connector>=<method>"},
		{[]string{"=api-key"}, "want <connector>=<method>"},
		{[]string{"codex="}, "want <connector>=<method>"},
		{[]string{"codex=Api Key"}, "want <connector>=<method>"},
		{[]string{"codex=manual", "codex=api-key"}, `names "codex" more than once`},
	} {
		_, err = connector.ParseMethods(tc.args)
		if !errors.Is(err, errdefs.ErrConnectorInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestParseOverrides(t *testing.T) {
	got, err := connector.ParseOverrides([]string{"forge=work-token", " scm = key@lab/team "}, "ops")
	want := map[string]v1beta1.ContainerSecretRef{
		"forge": {Name: "work-token", Realm: "ops"},
		"scm":   {Name: "key", Realm: "lab", Space: "team"},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %+v %v", got, err)
	}
	if got, err = connector.ParseOverrides(nil, "ops"); got != nil || err != nil {
		t.Fatalf("no arguments = %+v %v", got, err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"forge"}, "want <connector>=<secret>"},
		{[]string{"=token"}, "want <connector>=<secret>"},
		{[]string{"forge="}, "want <connector>=<secret>"},
		{[]string{"Forge=token"}, "want <connector>=<secret>"},
		{[]string{"forge=a", "forge=b"}, `names "forge" more than once`},
		{[]string{"forge=a@b/c/d/e"}, "want name[@realm[/space[/stack]]]"},
	} {
		_, err = connector.ParseOverrides(tc.args, "ops")
		if !errors.Is(err, errdefs.ErrConnectorInvalid) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: err = %v, want %q", tc.args, err, tc.want)
		}
	}
}

func TestRegistryHandler(t *testing.T) {
	for server, want := range map[string]string{
		"ghcr.io":                      "registry-ghcr-io",
		"  Registry.Example.com:5000/": "registry-registry-example-com-5000",
		"":                             "registry-default",
		"://":                          "registry-default",
	} {
		if got := connector.RegistrySecretName(server); got != want {
			t.Fatalf("RegistrySecretName(%q) = %q, want %q", server, got, want)
		}
	}

	secret := connector.RegistrySecret("ops", "registry-ghcr-io", "bot", "hunter2")
	if secret.Kind != v1beta1.KindSecret || secret.Metadata.Realm != "ops" || secret.Metadata.Space != "" ||
		secret.Spec.Type != v1beta1.SecretTypeBasicAuth || secret.Spec.Connector != connector.RegistryIdentityTag ||
		secret.Spec.BasicAuth == nil || secret.Spec.BasicAuth.Username != "bot" ||
		secret.Spec.BasicAuth.Password != "hunter2" {
		t.Fatalf("registry secret = %+v", secret)
	}

	inline := []v1beta1.RegistryCredentials{
		{ServerAddress: "ghcr.io", Username: "old", Password: "old-password"},
		{ServerAddress: "docker.io", Username: "hub", Password: "hub-password"},
	}
	before := append([]v1beta1.RegistryCredentials(nil), inline...)
	// An existing entry is replaced in place: the inline pair moves into a Secret.
	got := connector.UpsertRegistryCredential(inline, "ghcr.io", "ops", "registry-ghcr-io")
	ref := &v1beta1.ContainerSecretRef{Name: "registry-ghcr-io", Realm: "ops"}
	if len(got) != 2 ||
		!reflect.DeepEqual(got[0], v1beta1.RegistryCredentials{ServerAddress: "ghcr.io", SecretRef: ref}) ||
		!reflect.DeepEqual(got[1], inline[1]) {
		t.Fatalf("upsert = %+v", got)
	}
	got = connector.UpsertRegistryCredential(inline, "quay.io", "ops", "registry-quay-io")
	if len(got) != 3 || got[2].ServerAddress != "quay.io" || got[2].SecretRef.Name != "registry-quay-io" {
		t.Fatalf("append = %+v", got)
	}

	redacted := connector.RedactRegistryPasswords(inline)
	for i, cred := range redacted {
		if cred.Password != "" || cred.Username != inline[i].Username || cred.ServerAddress != inline[i].ServerAddress {
			t.Fatalf("redacted[%d] = %+v", i, cred)
		}
	}
	if !reflect.DeepEqual(inline, before) {
		t.Fatalf("the input was modified: %+v", inline)
	}
	if connector.RedactRegistryPasswords(nil) != nil {
		t.Fatal("no credentials stay nil")
	}
}

// TestGitIdentityTypes checks the bundled git connector (issue #1591): an
// ssh-key or basic-auth identity still names the hosts it authenticates, and
// an opaque one is author-only — it takes no hosts, no signing and no
// allowed-signers secret, and must carry an author.
func TestGitIdentityTypes(t *testing.T) {
	catalog, err := teamconnector.Default()
	if err != nil {
		t.Fatal(err)
	}
	conn, ok := catalog.Find(connector.GitIdentityTag)
	if !ok {
		t.Fatal("the bundled catalog has no git connector")
	}
	method, _ := conn.Method("")
	desc := conn.Descriptor(method)
	const author = "Me <me@example.com>"
	for _, tc := range []struct {
		name     string
		typ      string
		identity map[string]string
		want     string // empty: accepted
	}{
		{"ssh-key with hosts", v1beta1.SecretTypeSSHKey, map[string]string{"hosts": "github.com"}, ""},
		{
			"ssh-key without hosts", v1beta1.SecretTypeSSHKey,
			map[string]string{"author": author},
			`requires identity field "hosts"`,
		},
		{"basic-auth without hosts", v1beta1.SecretTypeBasicAuth, nil, `requires identity field "hosts"`},
		// The git identity a GitHub token is stored as (issue #1693).
		{"basic-auth from a GitHub token", v1beta1.SecretTypeBasicAuth, map[string]string{
			"hosts": connector.GitHubTokenHost, "author": author, connector.IdentityTokenFrom: "github-token@default",
		}, ""},
		{"author-only", v1beta1.SecretTypeOpaque, map[string]string{"author": author, "committer": author}, ""},
		{
			"author-only with hosts", v1beta1.SecretTypeOpaque,
			map[string]string{"author": author, "hosts": "github.com"},
			`identity field "hosts" applies to a ssh-key or basic-auth secret, not opaque`,
		},
		{
			"author-only with sign", v1beta1.SecretTypeOpaque,
			map[string]string{"author": author, "sign": "commits"},
			`identity field "sign" applies to a ssh-key secret, not opaque`,
		},
		{
			"author-only with allowed signers", v1beta1.SecretTypeOpaque,
			map[string]string{"author": author, "allowedSignersRef": "signers"},
			`identity field "allowedSignersRef" applies to a ssh-key secret, not opaque`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			checkErr := connector.CheckSecret(desc, v1beta1.SecretSpec{
				Type: tc.typ, Connector: connector.GitIdentityTag, Identity: tc.identity,
			})
			if tc.want == "" {
				if checkErr != nil {
					t.Fatal(checkErr)
				}
				return
			}
			if !errors.Is(checkErr, errdefs.ErrConnectorIdentity) || !strings.Contains(checkErr.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", checkErr, tc.want)
			}
		})
	}

	authorOnly := v1beta1.SecretSpec{Connector: connector.GitIdentityTag, Data: connector.GitAuthorOnlyValue}
	if !connector.IsGitAuthorOnly(authorOnly) {
		t.Fatal("an untyped (opaque) git identity is author-only")
	}
	if err = connector.CheckGitAuthorOnly(authorOnly); !errors.Is(err, errdefs.ErrConnectorIdentity) ||
		!strings.Contains(err.Error(), `"author"`) {
		t.Fatalf("CheckGitAuthorOnly(no author) = %v, want it to require the author", err)
	}
	authorOnly.Identity = map[string]string{"author": author}
	if err = connector.CheckGitAuthorOnly(authorOnly); err != nil {
		t.Fatalf("CheckGitAuthorOnly(author) = %v", err)
	}
	for _, other := range []v1beta1.SecretSpec{
		{Type: v1beta1.SecretTypeSSHKey, Connector: connector.GitIdentityTag},
		{Connector: "other"},
	} {
		if connector.IsGitAuthorOnly(other) || connector.CheckGitAuthorOnly(other) != nil {
			t.Errorf("%+v is not an author-only git identity", other)
		}
	}
}
