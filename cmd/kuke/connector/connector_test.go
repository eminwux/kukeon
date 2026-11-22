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

package connector_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/cmd/kuke/connector"
	"github.com/eminwux/kukeon/internal/connector/gitimport"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	v1beta1 "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/connector/connectortest"
	"gopkg.in/yaml.v3"
)

// fakeDaemon serves realms and Secrets and records every write.
type fakeDaemon struct {
	realms  map[string]v1beta1.RealmDoc
	secrets map[string]v1beta1.SecretDoc // "realm/name"
	created []v1beta1.SecretDoc
	applied int
}

func newFakeDaemon() *fakeDaemon {
	return &fakeDaemon{
		realms: map[string]v1beta1.RealmDoc{
			"default":     {Metadata: v1beta1.RealmMetadata{Name: "default"}},
			"kuke-system": {Metadata: v1beta1.RealmMetadata{Name: "kuke-system"}},
		},
		secrets: map[string]v1beta1.SecretDoc{},
	}
}

func (f *fakeDaemon) Close() error { return nil }

func (f *fakeDaemon) GetRealm(_ context.Context, doc v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	realm, ok := f.realms[doc.Metadata.Name]
	return kukeonv1.GetRealmResult{Realm: realm, MetadataExists: ok}, nil
}

func (f *fakeDaemon) GetSpace(context.Context, v1beta1.SpaceDoc) (kukeonv1.GetSpaceResult, error) {
	return kukeonv1.GetSpaceResult{}, kukeonv1.ErrUnexpectedCall
}

func (f *fakeDaemon) GetStack(context.Context, v1beta1.StackDoc) (kukeonv1.GetStackResult, error) {
	return kukeonv1.GetStackResult{}, kukeonv1.ErrUnexpectedCall
}

func (f *fakeDaemon) GetSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.GetSecretResult, error) {
	secret, ok := f.secrets[doc.Metadata.Realm+"/"+doc.Metadata.Name]
	return kukeonv1.GetSecretResult{Secret: secret, MetadataExists: ok}, nil
}

func (f *fakeDaemon) ApplyDocuments(_ context.Context, raw []byte) (kukeonv1.ApplyDocumentsResult, error) {
	var realm v1beta1.RealmDoc
	if err := yaml.Unmarshal(raw, &realm); err != nil {
		return kukeonv1.ApplyDocumentsResult{}, err
	}
	f.applied++
	f.realms[realm.Metadata.Name] = realm
	return kukeonv1.ApplyDocumentsResult{Resources: []kukeonv1.ApplyResourceResult{{
		Kind: string(v1beta1.KindRealm), Name: realm.Metadata.Name, Action: "updated",
	}}}, nil
}

func (f *fakeDaemon) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) {
	out := make([]v1beta1.RealmDoc, 0, len(f.realms))
	for _, realm := range f.realms {
		out = append(out, realm)
	}
	return out, nil
}

func (f *fakeDaemon) ListSecrets(context.Context, string, string, string, string) ([]v1beta1.SecretDoc, error) {
	out := make([]v1beta1.SecretDoc, 0, len(f.secrets))
	for _, secret := range f.secrets {
		out = append(out, secret)
	}
	return out, nil
}

func (f *fakeDaemon) CreateSecret(_ context.Context, doc v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	key := doc.Metadata.Realm + "/" + doc.Metadata.Name
	_, existed := f.secrets[key]
	f.created = append(f.created, doc)
	// The daemon keeps the readable part only.
	f.secrets[key] = v1beta1.SecretDoc{Metadata: doc.Metadata, Spec: v1beta1.SecretSpec{
		Type: doc.Spec.Type, Connector: doc.Spec.Connector, Identity: doc.Spec.Identity,
	}}
	return kukeonv1.CreateSecretResult{Secret: doc, Created: !existed}, nil
}

func (f *fakeDaemon) realmGitDefault() *v1beta1.ContainerSecretRef {
	defaults := f.realms["default"].Spec.Defaults
	if defaults == nil || defaults.Git == nil {
		return nil
	}
	return defaults.Git.IdentityRef
}

type fakeGit map[string]string

func (f fakeGit) Lookup(_ context.Context, key string, _ bool) (string, error) {
	if value, ok := f[key]; ok {
		return value, nil
	}
	return "", gitimport.ErrMissing
}

const (
	armor    = "PRIVATE" + " KEY-----"
	plainKey = "-----BEGIN OPENSSH " + armor + "\nplain-key-material-canary\n-----END OPENSSH " + armor + "\n"
	pubKey   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFixture operator@host"
)

// run executes `kuke connector` against daemon (nil: no daemon may be
// reached), with home and git standing in for the host.
func run(t *testing.T, daemon *fakeDaemon, home string, git fakeGit, args ...string) (string, error) {
	t.Helper()
	cmd := connector.NewConnectorCmdWithCatalog(connectortest.Catalog(t))
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	ctx := context.WithValue(context.Background(), connector.MockGitKey{}, gitimport.GitReader(git))
	ctx = context.WithValue(ctx, connector.MockHomeKey{}, home)
	var client connector.Client = &unreachable{}
	if daemon != nil {
		client = daemon
	}
	cmd.SetContext(context.WithValue(ctx, connector.MockControllerKey{}, client))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// unreachable fails every daemon call: --dry-run must make none.
type unreachable struct{ fakeDaemon }

var errReached = errors.New("the daemon was reached")

func (*unreachable) ListRealms(context.Context) ([]v1beta1.RealmDoc, error) { return nil, errReached }

func (*unreachable) CreateSecret(context.Context, v1beta1.SecretDoc) (kukeonv1.CreateSecretResult, error) {
	return kukeonv1.CreateSecretResult{}, errReached
}

func (*unreachable) GetRealm(context.Context, v1beta1.RealmDoc) (kukeonv1.GetRealmResult, error) {
	return kukeonv1.GetRealmResult{}, errReached
}

func (*unreachable) ApplyDocuments(context.Context, []byte) (kukeonv1.ApplyDocumentsResult, error) {
	return kukeonv1.ApplyDocumentsResult{}, errReached
}

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// keyHome is a home with an ed25519 key the operator signs with.
func keyHome(t *testing.T) (string, fakeGit) {
	t.Helper()
	home := t.TempDir()
	key := writeFile(t, filepath.Join(home, ".ssh/id_ed25519"), plainKey)
	writeFile(t, key+".pub", pubKey+"\n")
	writeFile(t, filepath.Join(home, ".ssh/allowed_signers"), "dev@example.com "+pubKey+"\n")
	return home, fakeGit{
		"user.name": "Dev Operator", "user.email": "dev@example.com",
		"user.signingkey": key + ".pub", "commit.gpgsign": "true",
		"gpg.ssh.allowedSignersFile": "~/.ssh/allowed_signers",
	}
}

func assertNoMaterial(t *testing.T, text string) {
	t.Helper()
	if strings.Contains(text, "key-material-canary") || strings.Contains(text, "PRIVATE KEY") {
		t.Fatalf("output carries key material:\n%s", text)
	}
}

// TestImportGitWithKey is AC7 and AC9's --default: the key identity and its
// allowed-signers file are stored, and the Realm default names the identity.
func TestImportGitWithKey(t *testing.T) {
	home, git := keyHome(t)
	daemon := newFakeDaemon()
	out, err := run(t, daemon, home, git, "import", "git", "--hosts", "github.com,gitlab.com", "--default")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoMaterial(t, out)
	if len(daemon.created) != 2 {
		t.Fatalf("created %d secrets, want the allowed signers and the key", len(daemon.created))
	}
	signers, key := daemon.created[0], daemon.created[1]
	if signers.Metadata.Name != "git-allowed-signers" ||
		key.Metadata != (v1beta1.SecretMetadata{Name: "git-ssh", Realm: "default"}) {
		t.Fatalf("created %v then %v", signers.Metadata, key.Metadata)
	}
	want := map[string]string{
		"author": "Dev Operator <dev@example.com>", "committer": "Dev Operator <dev@example.com>",
		"hosts": "github.com,gitlab.com", "sign": "commits", "allowedSignersRef": "git-allowed-signers",
	}
	if key.Spec.Type != v1beta1.SecretTypeSSHKey || key.Spec.Connector != "git" ||
		!strings.Contains(key.Spec.SSHKey.PrivateKey, "canary") || len(key.Spec.Identity) != len(want) {
		t.Fatalf("key secret = %+v", key.Spec)
	}
	for field, value := range want {
		if key.Spec.Identity[field] != value {
			t.Errorf("identity %s = %q, want %q", field, key.Spec.Identity[field], value)
		}
	}
	if ref := daemon.realmGitDefault(); ref == nil || ref.Name != "git-ssh" || ref.Realm != "default" {
		t.Fatalf("realm default = %+v, want git-ssh@default", ref)
	}
	if !strings.Contains(out, `Realm "default" default git identity: git-ssh@default (was none)`) {
		t.Errorf("output does not report the default:\n%s", out)
	}

	// A rerun updates the Secrets in place; --default over another identity
	// says what it replaced.
	daemon.secrets["default/other"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "other", Realm: "default"},
		Spec: v1beta1.SecretSpec{
			Type:      v1beta1.SecretTypeOpaque,
			Connector: "git",
			Identity:  map[string]string{"author": "O <o@example.com>"},
		},
	}
	if out, err = run(t, daemon, home, git, "default", "git", "other"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err = run(t, daemon, home, git, "import", "git", "--default")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, `Secret "git-ssh" (realm "default"): updated`) ||
		!strings.Contains(out, "git-ssh@default (replaces other@default)") {
		t.Errorf("rerun output:\n%s", out)
	}
	if ref := daemon.realmGitDefault(); ref == nil || ref.Name != "git-ssh" {
		t.Fatalf("realm default = %+v, want git-ssh again", ref)
	}
}

// TestImportGitAuthorOnly is AC8: no key → an opaque author-only identity, and
// neither a key nor an author is an error naming both.
func TestImportGitAuthorOnly(t *testing.T) {
	daemon := newFakeDaemon()
	home := t.TempDir()
	out, err := run(t, daemon, home, fakeGit{"user.name": "Me", "user.email": "me@example.com"}, "import", "git")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(daemon.created) != 1 {
		t.Fatalf("created = %+v", daemon.created)
	}
	doc := daemon.created[0]
	if doc.Metadata.Name != "git-author" || doc.Spec.Type != v1beta1.SecretTypeOpaque || doc.Spec.Connector != "git" ||
		doc.Spec.Data != "kukeon:git-author-only" || doc.Spec.Identity["author"] != "Me <me@example.com>" ||
		doc.Spec.Identity["hosts"] != "" {
		t.Fatalf("author-only secret = %+v", doc)
	}
	if !strings.Contains(out, "author-only") || daemon.applied != 0 {
		t.Errorf("output does not say it is author-only, or a default was set:\n%s", out)
	}

	out, err = run(t, newFakeDaemon(), home, fakeGit{}, "import", "git")
	if !errors.Is(err, errdefs.ErrGitImport) || !strings.Contains(err.Error(), "no SSH private key") ||
		!strings.Contains(err.Error(), "no git author") {
		t.Fatalf("err = %v\n%s", err, out)
	}
}

// TestImportGitDryRun is AC9's --dry-run: what would be stored, never key
// material, and no daemon call.
func TestImportGitDryRun(t *testing.T) {
	home, git := keyHome(t)
	out, err := run(t, nil, home, git, "import", "git", "--dry-run", "--default", "--name", "work")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	assertNoMaterial(t, out)
	for _, want := range []string{
		"Dry run: nothing is stored.",
		"Home: " + home,
		"Secret: work@default (type ssh-key, connector git)",
		"Key: " + filepath.Join(home, ".ssh/id_ed25519"),
		"Allowed signers: " + filepath.Join(home, ".ssh/allowed_signers"),
		"  author: Dev Operator <dev@example.com>",
		"  hosts: github.com",
		"  sign: commits",
		`Would set the realm "default" default git identity to work@default.`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run output lacks %q:\n%s", want, out)
		}
	}
}

// TestConnectorDefault is AC10's default: the same checks as the web route,
// and --clear.
func TestConnectorDefault(t *testing.T) {
	daemon := newFakeDaemon()
	daemon.secrets["default/me"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "me", Realm: "default"},
		Spec:     v1beta1.SecretSpec{Connector: "git", Identity: map[string]string{"author": "Me <me@example.com>"}},
	}
	daemon.secrets["default/token"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "token", Realm: "default"},
		Spec:     v1beta1.SecretSpec{Connector: connectortest.Name},
	}
	if out, err := run(t, daemon, "", nil, "default", "git", "me@default"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if ref := daemon.realmGitDefault(); ref == nil || ref.Name != "me" {
		t.Fatalf("realm default = %+v", ref)
	}
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"missing secret":         {[]string{"default", "git", "nope"}, "secret not found"},
		"another connector's":    {[]string{"default", "git", "token"}, `identity of connector "` + connectortest.Name + `"`},
		"unknown connector":      {[]string{"default", "nope", "me"}, "unknown connector"},
		"no identity":            {[]string{"default", "git"}, "or pass --clear"},
		"clear with an identity": {[]string{"default", "git", "me", "--clear"}, "--clear takes no identity"},
		"bad reference":          {[]string{"default", "git", "me@a/b/c/d"}, "want name[@realm"},
		"unknown realm":          {[]string{"default", "git", "me@default", "--realm", "nowhere"}, "realm not found"},
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := run(t, daemon, "", nil, tc.args...); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out)
			}
		})
	}
	out, err := run(t, daemon, "", nil, "default", "git", "--clear")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if ref := daemon.realmGitDefault(); ref != nil || !strings.Contains(out, "cleared (was me@default)") {
		t.Fatalf("realm default = %+v after --clear\n%s", ref, out)
	}
}

// TestConnectorList is AC10's list: every connector, its identities and the
// identity each Realm default names.
func TestConnectorList(t *testing.T) {
	daemon := newFakeDaemon()
	daemon.secrets["default/git-ssh"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "git-ssh", Realm: "default"},
		Spec:     v1beta1.SecretSpec{Type: v1beta1.SecretTypeSSHKey, Connector: "git"},
	}
	daemon.secrets["default/me"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "me", Realm: "default", Space: "dev"},
		Spec:     v1beta1.SecretSpec{Connector: "git"},
	}
	daemon.secrets["default/plain"] = v1beta1.SecretDoc{
		Metadata: v1beta1.SecretMetadata{Name: "plain", Realm: "default"},
	}
	realm := daemon.realms["default"]
	realm.Spec.Defaults = &v1beta1.ScopeDefaults{
		Git: &v1beta1.ContainerGit{IdentityRef: &v1beta1.ContainerSecretRef{Name: "git-ssh"}},
		Secrets: map[string]v1beta1.CellConfigSecretFill{
			connectortest.Slot: {SecretRef: &v1beta1.ContainerSecretRef{Name: "tok", Realm: "default"}},
		},
	}
	daemon.realms["default"] = realm

	out, err := run(t, daemon, "", nil, "list")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	identities, defaults, ok := strings.Cut(out, "REALM DEFAULTS")
	if !ok {
		t.Fatalf("no realm defaults section:\n%s", out)
	}
	for _, want := range [][]string{
		{"git", "git-ssh", "default", "ssh-key"},
		{"git", "me", "default/dev", "opaque"},
		{connectortest.Name, "-", "-", "-"},
	} {
		if !hasRow(identities, want) {
			t.Errorf("identities lack row %v:\n%s", want, identities)
		}
	}
	if strings.Contains(identities, "plain") {
		t.Errorf("an untagged secret is listed as an identity:\n%s", identities)
	}
	if !hasRow(defaults, []string{"git", "-", "default", "git-ssh@default"}) ||
		!hasRow(defaults, []string{connectortest.Name, "token", "default", "tok@default"}) {
		t.Errorf("realm defaults:\n%s", defaults)
	}
}

func hasRow(table string, cells []string) bool {
	for _, line := range strings.Split(table, "\n") {
		if strings.Join(strings.Fields(line), " ") == strings.Join(cells, " ") {
			return true
		}
	}
	return false
}
