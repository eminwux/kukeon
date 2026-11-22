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

package secret_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/create/secret"
	"github.com/eminwux/kukeon/cmd/types"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"github.com/spf13/viper"
)

// runConnectorCreate executes `kuke create secret` over the test catalog — the
// embedded connectors plus the test-only one — against a fake daemon.
func runConnectorCreate(t *testing.T, stdin string, args ...string) (*v1beta1.SecretDoc, string, error) {
	t.Helper()
	t.Cleanup(viper.Reset)

	var sent *v1beta1.SecretDoc
	fake := &fakeClient{createSecretFn: func(doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
		sent = &doc
		// The daemon's reply carries the readable part only.
		reply := doc
		reply.Spec = v1beta1.SecretSpec{
			Type: doc.Spec.Type, Connector: doc.Spec.Connector, Identity: doc.Spec.Identity,
		}
		return kukeonv1.CreateSecretResult{Secret: reply, Created: true}, nil
	}}

	cmd := secret.NewSecretCmdWithCatalog(connectortest.Catalog(t))
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetIn(strings.NewReader(stdin))
	ctx := context.WithValue(
		context.Background(), types.CtxLogger, slog.New(slog.NewTextHandler(io.Discard, nil)))
	cmd.SetContext(context.WithValue(ctx, secret.MockControllerKey{}, kukeonv1.Client(fake)))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return sent, out.String(), err
}

// TestCreateSecret_ConnectorTagsEveryType: opaque, basic-auth and ssh-key
// secrets are each created tagged with a connector, the tag is echoed, and no
// material appears in the output.
func TestCreateSecret_ConnectorTagsEveryType(t *testing.T) {
	keyFile := writeTypedFile(t, "id_ed25519", typedTestPrivateKey)
	tokenFile := writeTypedFile(t, "token", "tok-material\n")

	tests := []struct {
		name         string
		stdin        string
		args         []string
		wantType     string
		wantTag      string
		wantIdentity map[string]string
		material     string
	}{
		{
			name:     "opaque token",
			args:     []string{"gh-token", "--connector", "github", "--from-file", tokenFile},
			wantType: "", wantTag: "github", material: "tok-material",
		},
		{
			name:  "basic-auth: the one type the connector accepts is implied",
			stdin: "pw-material\n",
			args: []string{
				"registry-ghcr-io", "--connector", "registry", "--username", "octo", "--password-stdin",
			},
			wantType: v1beta1.SecretTypeBasicAuth, wantTag: "registry", material: "pw-material",
		},
		{
			name: "ssh-key git identity with its fields",
			args: []string{
				"git-ssh", "--connector", "git", "--type", "ssh-key", "--from-file", keyFile,
				"--author", "Ada Lovelace <ada@example.com>", "--hosts", "github.com, gitlab.com",
				"--sign", "commits,tags", "--identity", "committer=Ada Bot <bot@example.com>",
				"--allowed-signers-ref", "signers@default",
			},
			wantType: v1beta1.SecretTypeSSHKey, wantTag: "git", material: "Ym9keQ==",
			wantIdentity: map[string]string{
				"author": "Ada Lovelace <ada@example.com>", "committer": "Ada Bot <bot@example.com>",
				"hosts": "github.com, gitlab.com", "sign": "commits,tags", "allowedSignersRef": "signers@default",
			},
		},
		{
			name:  "basic-auth git identity",
			stdin: "https-token\n",
			args: []string{
				"git-https", "--connector", "git", "--type", "basic-auth", "--username", "x-access-token",
				"--token-stdin", "--hosts", "github.com",
			},
			wantType: v1beta1.SecretTypeBasicAuth, wantTag: "git", material: "https-token",
			wantIdentity: map[string]string{"hosts": "github.com"},
		},
		{
			// Issue #1591: no value, no --hosts.
			name:     "author-only git identity",
			args:     []string{"me", "--connector", "git", "--type", "opaque", "--author", "Me <me@example.com>"},
			wantType: "", wantTag: "git", material: "kukeon:git-author-only",
			wantIdentity: map[string]string{"author": "Me <me@example.com>"},
		},
		{
			name:     "author-only git identity without --type",
			args:     []string{"me", "--connector", "git", "--author", "Me <me@example.com>"},
			wantType: "", wantTag: "git", material: "kukeon:git-author-only",
			wantIdentity: map[string]string{"author": "Me <me@example.com>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, out, err := runConnectorCreate(t, tt.stdin, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if doc == nil {
				t.Fatal("the daemon was never called")
			}
			if doc.Spec.Connector != tt.wantTag || doc.Spec.Type != tt.wantType {
				t.Errorf("connector = %q type = %q, want %q / %q",
					doc.Spec.Connector, doc.Spec.Type, tt.wantTag, tt.wantType)
			}
			if !reflect.DeepEqual(doc.Spec.Identity, tt.wantIdentity) {
				t.Errorf("identity = %v, want %v", doc.Spec.Identity, tt.wantIdentity)
			}
			if tt.wantTag == "git" && tt.wantType == "" &&
				doc.Spec.Data != "kukeon:git-author-only" {
				t.Errorf("author-only data = %q, want the placeholder", doc.Spec.Data)
			}
			if !strings.Contains(out, "connector: "+tt.wantTag) {
				t.Errorf("output does not show the tag:\n%s", out)
			}
			if strings.Contains(out, tt.material) {
				t.Errorf("output carries secret material:\n%s", out)
			}
		})
	}
}

// TestCreateSecret_ConnectorRejections: the connector must exist, the type
// must be one it accepts, the identity must fit its schema — and no error
// echoes the secret's material.
func TestCreateSecret_ConnectorRejections(t *testing.T) {
	keyFile := writeTypedFile(t, "id_ed25519", typedTestPrivateKey)
	tokenFile := writeTypedFile(t, "token", "tok-material\n")

	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"unknown connector", []string{"x", "--connector", "nope", "--from-file", tokenFile}, "unknown connector"},
		{
			"type the connector does not accept",
			[]string{"x", "--connector", "github", "--type", "ssh-key", "--from-file", keyFile},
			"accepts a opaque secret",
		},
		{
			"several accepted types need --type",
			[]string{"x", "--connector", "git", "--from-file", keyFile, "--hosts", "github.com"},
			"requires --type",
		},
		{
			"author-only git identity takes no value",
			[]string{
				"x",
				"--connector",
				"git",
				"--type",
				"opaque",
				"--author",
				"A <a@example.com>",
				"--from-file",
				tokenFile,
			},
			"an author-only git identity stores no value; use --type ssh-key for a key",
		},
		{
			"author-only git identity takes no literal",
			[]string{"x", "--connector", "git", "--author", "A <a@example.com>", "--from-literal", "tok-material"},
			"an author-only git identity stores no value",
		},
		{
			"author-only git identity needs an author",
			[]string{"x", "--connector", "git", "--type", "opaque"},
			`requires identity field "author"`,
		},
		{
			"author-only git identity takes no hosts",
			[]string{"x", "--connector", "git", "--author", "A <a@example.com>", "--hosts", "github.com"},
			`identity field "hosts" applies to a ssh-key or basic-auth secret, not opaque`,
		},
		{
			"missing required field",
			[]string{"x", "--connector", "git", "--type", "ssh-key", "--from-file", keyFile},
			`requires identity field "hosts"`,
		},
		{
			"field of another connector",
			[]string{"x", "--connector", "github", "--from-file", tokenFile, "--hosts", "github.com"},
			`declares no identity field "hosts"`,
		},
		{
			"malformed person",
			[]string{
				"x", "--connector", "git", "--type", "ssh-key", "--from-file", keyFile,
				"--hosts", "github.com", "--author", "just-a-name",
			},
			`identity field "author"`,
		},
		{
			"enum",
			[]string{
				"x", "--connector", "git", "--type", "ssh-key", "--from-file", keyFile,
				"--hosts", "github.com", "--sign", "everything",
			},
			"must be one of commits, tags",
		},
		{
			"field limited to another secret type",
			[]string{
				"x", "--connector", "git", "--type", "basic-auth", "--username", "u", "--token-file", tokenFile,
				"--hosts", "github.com", "--sign", "commits",
			},
			`identity field "sign" applies to a ssh-key secret`,
		},
		{
			"identity without connector",
			[]string{"x", "--from-file", tokenFile, "--identity", "a=b"},
			"requires --connector",
		},
		{
			"field flag without connector",
			[]string{"x", "--from-file", tokenFile, "--hosts", "h"},
			"requires --connector",
		},
		{
			"field set twice",
			[]string{
				"x", "--connector", "git", "--type", "ssh-key", "--from-file", keyFile,
				"--hosts", "github.com", "--identity", "hosts=gitlab.com",
			},
			`both set "hosts"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, out, err := runConnectorCreate(t, "", tt.args...)
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
			if doc != nil {
				t.Error("the daemon was called for a rejected secret")
			}
			for _, material := range []string{"tok-material", "Ym9keQ=="} {
				if strings.Contains(err.Error(), material) || strings.Contains(out, material) {
					t.Errorf("error or output carries secret material: %v\n%s", err, out)
				}
			}
		})
	}
}

// TestCreateSecret_TestOnlyConnector is the "connectors are data" check for
// the CLI: the test-only connector, defined by one connector.yaml and named by
// no Go code, is accepted by --connector, and its identity field is a flag.
func TestCreateSecret_TestOnlyConnector(t *testing.T) {
	tokenFile := writeTypedFile(t, "token", "tok-material\n")

	cmd := secret.NewSecretCmdWithCatalog(connectortest.Catalog(t))
	if usage := cmd.Flags().Lookup("connector").Usage; !strings.Contains(usage, connectortest.Name) {
		t.Errorf("--connector help does not list %q: %s", connectortest.Name, usage)
	}
	field := cmd.Flags().Lookup(connectortest.Field)
	if field == nil {
		t.Fatalf("the connector's identity field %q is not a flag", connectortest.Field)
	}
	if !strings.Contains(field.Usage, "Account the token belongs to") {
		t.Errorf("--%s help does not carry the field's help text: %s", connectortest.Field, field.Usage)
	}

	doc, out, err := runConnectorCreate(t, "",
		"example", "--connector", connectortest.Name, "--from-file", tokenFile,
		"--"+connectortest.Field, "acme")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc == nil || doc.Spec.Connector != connectortest.Name ||
		!reflect.DeepEqual(doc.Spec.Identity, map[string]string{connectortest.Field: "acme"}) {
		t.Fatalf("sent secret = %+v", doc)
	}
	if !strings.Contains(out, "connector: "+connectortest.Name) {
		t.Errorf("output does not show the tag:\n%s", out)
	}

	// Its schema is enforced like any other connector's.
	if _, _, err = runConnectorCreate(t, "",
		"example", "--connector", connectortest.Name, "--from-file", tokenFile); err == nil ||
		!strings.Contains(err.Error(), `requires identity field "`+connectortest.Field+`"`) {
		t.Errorf("missing required field: error = %v", err)
	}
}

func TestFieldFlagNames(t *testing.T) {
	cmd := secret.NewSecretCmdWithCatalog(connectortest.Catalog(t))
	for _, flag := range []string{"author", "committer", "hosts", "sign", "allowed-signers-ref", "identity"} {
		if cmd.Flags().Lookup(flag) == nil {
			t.Errorf("flag --%s is missing", flag)
		}
	}
}
